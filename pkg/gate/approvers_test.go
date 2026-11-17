package gate_test

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	principalpkg "opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// errKindDown is the error of a kind reader that cannot read the kind.
var errKindDown = errors.New("kind store down")

// fakeKinds is a gate.KindReader that gives kind, or err, and counts its reads
// in reads when reads is not nil.
type fakeKinds struct {
	kind  string
	err   error
	reads *atomic.Int64
}

func (k fakeKinds) ReadKind(context.Context) (string, error) {
	if k.reads != nil {
		k.reads.Add(1)
	}

	return k.kind, k.err
}

// fixedKind returns a reader that always gives kind.
func fixedKind(kind string) fakeKinds {
	return fakeKinds{kind: kind, err: nil, reads: nil}
}

// approverStore is a gate store that gives each approval rule of a snapshot
// the approvers a. It stands for stored data that the write path of a rule
// refuses: a rule whose approvers were removed, a restored row or a row of
// another version.
type approverStore struct {
	gate.Store

	approvers gate.Approvers
}

func (s approverStore) Snapshot(ctx context.Context, scopes []gate.Scope) (gate.Config, error) {
	cfg, err := s.Store.Snapshot(ctx, scopes)
	if err != nil {
		return cfg, err
	}

	for i := range cfg.ApprovalRules {
		cfg.ApprovalRules[i].Approvers = s.approvers
	}

	return cfg, nil
}

// users returns approvers that name the users only.
func users(names ...string) gate.Approvers {
	return gate.Approvers{Groups: nil, GroupIDs: nil, Users: names, UnitOwners: false}
}

// newApproverEnv returns a service over a store whose rule two-eyes has the
// approvers a, and a gate on the unit web whose outcome asks for that rule.
func newApproverEnv(t *testing.T, kinds gate.KindReader, a gate.Approvers) swEnv {
	t.Helper()

	e := newKindSWEnv(t, swUnitsOfTests(), nil, func(st *sqlite.Store) gate.Store {
		return approverStore{Store: st, approvers: a}
	}, kinds)

	e.putRule(t)
	e.putGate(t, newSWGate(atUnit, "needs-two", "", gate.ApprovalOutcome("two-eyes"), "Prod writes need review."))

	return e
}

// evaluateWeb calls Evaluate at state.write for a lock of the unit web by the
// person alice.
func (e swEnv) evaluateWeb(t *testing.T) (gate.Decision, error) {
	t.Helper()

	subject, err := swSubjects{}.Subject(t.Context(), principal("alice", false))
	if err != nil {
		t.Fatal(err)
	}

	return e.svc.Evaluate(t.Context(), gate.Change{
		Point: gate.PointStateWrite, Namespace: "acme", Ref: gate.ChangeRef{Kind: gate.ChangeStateWrite, ID: ""},
		Subject: subject, Source: gate.SourceCLI, Author: subject.ID, Operation: gate.OperationLock,
		Units: []identity.Object{swUnit(t, "web")}, Holders: nil, PlanLock: false, BreakGlass: "", Fence: e.fence,
		State: nil,
	})
}

// approverCase is one evaluation of the rule two-eyes with stored approvers.
type approverCase struct {
	name      string
	approvers gate.Approvers
	// result is the result of the record: deny for a rule that nobody can
	// meet, awaiting-approval for each other rule.
	result gate.Result
	// users are the users of the requirement in the record.
	users []string
}

// check evaluates the case on a fresh store with the kind reader kinds.
func (tc approverCase) check(t *testing.T, kinds gate.KindReader) {
	t.Helper()

	e := newApproverEnv(t, kinds, tc.approvers)

	_, err := e.evaluateWeb(t)

	refusal, ok := errors.AsType[*gate.RefusalError](err)
	if !ok || !errors.Is(err, gate.ErrDenied) {
		t.Fatalf("Evaluate() = %v, want a refusal that wraps ErrDenied", err)
	}

	if errors.Is(err, gate.ErrRuleFailed) {
		t.Fatalf("Evaluate() = %v, want a denial and not a gate in error", err)
	}

	unmeetable := strings.Contains(strings.Join(refusal.Messages, " "), "no approver can meet it")
	if unmeetable != (tc.result == gate.ResultDeny) {
		t.Fatalf("messages = %q, want the unmeetable message %v", refusal.Messages, tc.result == gate.ResultDeny)
	}

	rec := e.newestRecord(t)
	if rec.ID != refusal.Record || rec.Result != tc.result {
		t.Fatalf("record = %s %s, want %s %s", rec.ID, rec.Result, refusal.Record, tc.result)
	}

	// The gate keeps the requirement: it does not skip it.
	if len(rec.Requirements) != 1 {
		t.Fatalf("record requirements = %+v, want one", rec.Requirements)
	}

	if got := rec.Requirements[0].Approvers.Users; !slices.Equal(got, tc.users) {
		t.Fatalf("requirement users = %q, want %q", got, tc.users)
	}

	if got := len(rec.Requirements[0].Signatures); got != 0 {
		t.Fatalf("requirement signatures = %d, want 0", got)
	}
}

func TestEvaluateAppliesRuleApprovers(t *testing.T) {
	t.Parallel()

	sre := gate.Approvers{Groups: []string{"sre"}, GroupIDs: nil, Users: []string{"dev:admin"}, UnitOwners: false}

	nobody := []string{gate.NoApprover}
	open := gate.ResultAwaitingApproval

	tests := []approverCase{
		{"dev and real approvers keep the real one", users("dev:admin", "alice"), open, users("alice").Users},
		{"a dev-only rule denies as unmeetable", users("dev:admin", "DEV:demo"), gate.ResultDeny, nobody},
		{"NoApprover only denies", users(gate.NoApprover), gate.ResultDeny, nobody},
		{"NoApprover and a person keeps the person", users(gate.NoApprover, "alice"), open, users("alice").Users},
		{"a group stays open when its dev user goes", sre, open, nil},
		{"DEV:admin fills no slot", users("DEV:admin"), gate.ResultDeny, nobody},
		{"bare dev is a plain subject", users("dev"), open, users("dev").Users},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.check(t, fixedKind("production"))
		})
	}
}

// TestEvaluateDevTable4a4b runs the load case and the signature case of the
// layer table of the dev data rules through Evaluate. At state.write a write
// carries no signature, so the signature case checks that a subject whose
// signature cannot count is no approver of the requirement.
func TestEvaluateDevTable4a4b(t *testing.T) {
	t.Parallel()

	none := gate.Approvers{Groups: nil, GroupIDs: nil, Users: nil, UnitOwners: false}
	nobody := []string{gate.NoApprover}

	tests := []approverCase{
		{"4a: an approval rule with no approver left", none, gate.ResultDeny, nobody},
		{"4a: an approval rule with NoApprover only", users(gate.NoApprover), gate.ResultDeny, nobody},
		{"4b: dev:admin on production", users("dev:admin"), gate.ResultDeny, nobody},
		{"4b: DEV:Admin on production", users("DEV:Admin"), gate.ResultDeny, nobody},
		{"4b: !no-approver on production", users("!no-approver"), gate.ResultDeny, nobody},
		{"4b: all three on production", users("dev:admin", "DEV:Admin", "!no-approver"), gate.ResultDeny, nobody},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.check(t, fixedKind("production"))
		})
	}
}

func TestEvaluateKindErrorFailsClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		kinds fakeKinds
		gate  bool
	}{
		{"a read error with an approval gate", fakeKinds{kind: "", err: errKindDown, reads: nil}, true},
		{"a read error with no gate", fakeKinds{kind: "", err: errKindDown, reads: nil}, false},
		{"a read error that also gives production", fakeKinds{kind: "production", err: errKindDown, reads: nil}, true},
		{"an empty kind", fixedKind(""), true},
		{"an unknown kind", fixedKind("staging"), true},
		{"production in upper case", fixedKind("Production"), true},
		{"dev with a space", fixedKind("dev "), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newKindSWEnv(t, swUnitsOfTests(), nil, nil, tc.kinds)
			if tc.gate {
				e.putRule(t)
				e.putGate(t, newSWGate(atUnit, "needs-two", "", gate.ApprovalOutcome("two-eyes"), "review"))
			}

			d, err := e.evaluateWeb(t)
			if !errors.Is(err, gate.ErrUnavailable) {
				t.Fatalf("Evaluate() = %+v, %v, want an error that wraps ErrUnavailable", d, err)
			}

			if d.Result != "" {
				t.Fatalf("Evaluate() result = %s, want no decision", d.Result)
			}

			// The evaluation stops before any rule: it writes no record and
			// counts no signature.
			if recs := e.records(t); len(recs) != 0 {
				t.Fatalf("records = %d, want 0", len(recs))
			}
		})
	}
}

func TestNewServiceNeedsKindReader(t *testing.T) {
	t.Parallel()

	_, err := gate.NewService(gate.ServiceConfig{
		Store: stubStore{Store: nil}, Engine: stubEngine{Engine: nil}, Producers: nil, Units: stubUnits{},
		Subjects: subjects{}, Fences: stubFences{}, KindReader: nil, Now: nil, Log: slog.New(slog.DiscardHandler),
	})
	if !errors.Is(err, gate.ErrInvalid) {
		t.Fatalf("NewService(no kind reader) = %v, want ErrInvalid", err)
	}
}

func TestEvaluateDevInstallationKeepsDevApprovers(t *testing.T) {
	t.Parallel()

	devs := users("dev:admin", "DEV:demo")

	tests := []approverCase{
		{"dev approvers stay open", devs, gate.ResultAwaitingApproval, devs.Users},
		{"NoApprover still denies", users(gate.NoApprover), gate.ResultDeny, []string{gate.NoApprover}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.check(t, fixedKind("dev"))
		})
	}
}

func TestEvaluateEmptyApproverListDenies(t *testing.T) {
	t.Parallel()

	none := gate.Approvers{Groups: nil, GroupIDs: nil, Users: nil, UnitOwners: false}

	for _, kind := range []string{"production", "dev"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()

			approverCase{"empty list", none, gate.ResultDeny, []string{gate.NoApprover}}.check(t, fixedKind(kind))
		})
	}
}

func TestEvaluateReadsKindOncePerCall(t *testing.T) {
	t.Parallel()

	var reads atomic.Int64

	e := newApproverEnv(t, fakeKinds{kind: "production", err: nil, reads: &reads}, users("alice"))
	e.putGate(t, newSWGate(atNamespace, "also-two", "", gate.ApprovalOutcome("two-eyes"), "again"))

	for call := int64(1); call <= 2; call++ {
		if _, err := e.evaluateWeb(t); !errors.Is(err, gate.ErrDenied) {
			t.Fatalf("Evaluate() call %d = %v, want ErrDenied", call, err)
		}

		// One read for each call: a call does not reuse the kind of an earlier
		// call, and two approval gates do not read it twice.
		if got := reads.Load(); got != call {
			t.Fatalf("kind reads after call %d = %d, want %d", call, got, call)
		}
	}
}

func rule(a gate.Approvers) gate.ApprovalRule {
	var r gate.ApprovalRule

	r.Approvers = a

	return r
}

func owned(mode gate.Mode, a gate.Approvers) gate.OwnershipRule {
	var r gate.OwnershipRule

	r.Mode, r.Approvers = mode, a

	return r
}

func preset(a gate.Approvers) gate.ApprovalDefault {
	var d gate.ApprovalDefault

	d.Approvers = a

	return d
}

func TestGateRefusesRuleWithoutApprover(t *testing.T) {
	t.Parallel()

	none := gate.Approvers{Groups: nil, GroupIDs: nil, Users: nil, UnitOwners: false}
	nobody := gate.Approvers{Groups: nil, GroupIDs: nil, Users: []string{gate.NoApprover}, UnitOwners: false}
	mixed := gate.Approvers{Groups: nil, GroupIDs: nil, Users: []string{gate.NoApprover, "alice"}, UnitOwners: false}
	group := gate.Approvers{Groups: []string{"sre"}, GroupIDs: nil, Users: []string{gate.NoApprover}, UnitOwners: false}
	owners := gate.Approvers{Groups: nil, GroupIDs: nil, Users: []string{gate.NoApprover}, UnitOwners: true}

	tests := []struct {
		name string
		got  bool
		want bool
	}{
		{"approval rule, empty list", rule(none).Unmeetable(), true},
		{"approval rule, NoApprover only", rule(nobody).Unmeetable(), true},
		{"approval rule, NoApprover and a user", rule(mixed).Unmeetable(), false},
		{"approval rule, NoApprover and a group", rule(group).Unmeetable(), false},
		{"approval rule, NoApprover and unit owners", rule(owners).Unmeetable(), false},
		{"ownership rule enforce, empty list", owned(gate.ModeEnforce, none).Unmeetable(), true},
		{"ownership rule enforce, NoApprover only", owned(gate.ModeEnforce, nobody).Unmeetable(), true},
		{"ownership rule enforce, a user", owned(gate.ModeEnforce, mixed).Unmeetable(), false},
		{"ownership rule evaluate, NoApprover only", owned(gate.ModeEvaluate, nobody).Unmeetable(), false},
		{"approval default, NoApprover only", preset(nobody).Unmeetable(), true},
		{"approval default, empty list keeps its meaning", preset(none).Unmeetable(), false},
		{"approval default, NoApprover and a user", preset(mixed).Unmeetable(), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.got != tc.want {
				t.Errorf("Unmeetable = %v, want %v", tc.got, tc.want)
			}
		})
	}
}

func TestRuleApproversReadsAnEmptyListAsNoApprover(t *testing.T) {
	t.Parallel()

	got := gate.RuleApprovers(gate.Approvers{Groups: nil, GroupIDs: nil, Users: nil, UnitOwners: false})
	if len(got.Users) != 1 || got.Users[0] != gate.NoApprover || len(got.Groups) != 0 || got.UnitOwners {
		t.Errorf("RuleApprovers(empty) = %+v, want Users [NoApprover]", got)
	}

	keep := gate.Approvers{Groups: []string{"sre"}, GroupIDs: nil, Users: nil, UnitOwners: false}
	if got := gate.RuleApprovers(keep); len(got.Users) != 0 || len(got.Groups) != 1 {
		t.Errorf("RuleApprovers(groups) = %+v, want unchanged", got)
	}
}

func TestApprovalIgnoresDevSignatureInProduction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		subject    string
		production bool
		want       bool
	}{
		{"production, dev lower case", "dev:admin", true, false},
		{"production, dev mixed case", "DEV:Admin", true, false},
		{"production, NoApprover", gate.NoApprover, true, false},
		{"production, a person", "alice", true, true},
		{"dev installation, dev lower case", "dev:admin", false, true},
		{"dev installation, dev mixed case", "DEV:Admin", false, true},
		{"dev installation, NoApprover", gate.NoApprover, false, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			policy := gate.ApproverPolicy{Production: tc.production, IsDev: principalpkg.IsDevSubject}
			if got := policy.CountsAsSignature(tc.subject); got != tc.want {
				t.Errorf("CountsAsSignature(%q, production=%v) = %v, want %v", tc.subject, tc.production, got, tc.want)
			}
		})
	}

	// Fail closed: a production check without a matcher counts no signature.
	closed := gate.ApproverPolicy{Production: true, IsDev: nil}
	if closed.CountsAsSignature("alice") {
		t.Error("CountsAsSignature with no matcher on production = true, want false")
	}
}

func TestApproverPolicyValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		users      []string
		production bool
		wantErr    bool
	}{
		{"person", []string{"alice"}, true, false},
		{"NoApprover", []string{"alice", gate.NoApprover}, false, true},
		{"exclamation mark", []string{"!x"}, false, true},
		{"dev on production", []string{"dev:admin"}, true, true},
		{"dev mixed case on production", []string{"DEV:x"}, true, true},
		{"dev on dev installation", []string{"dev:admin"}, false, false},
		{"none", nil, true, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			policy := gate.ApproverPolicy{Production: tc.production, IsDev: principalpkg.IsDevSubject}
			err := policy.Validate(gate.Approvers{Groups: nil, GroupIDs: nil, Users: tc.users, UnitOwners: false})
			if (err != nil) != tc.wantErr || (err != nil && !errors.Is(err, gate.ErrInvalid)) {
				t.Errorf("ValidateApprovers(%v) = %v, want error %v wrapping ErrInvalid", tc.users, err, tc.wantErr)
			}
		})
	}
}
