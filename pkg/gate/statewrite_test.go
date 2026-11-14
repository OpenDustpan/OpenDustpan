package gate_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/gate/cedargo"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// swUnits gives the collection and the labels of the units of a test.
type swUnits struct {
	collections map[string]collection.Name
	labels      map[string][]string
}

func (u swUnits) Unit(_ context.Context, unit identity.Object) (gate.UnitInfo, error) {
	return gate.UnitInfo{Collection: u.collections[unit.String()], Labels: u.labels[unit.String()]}, nil
}

// swSubjects gives the Subject of a principal by its subject: "alice" is a
// person with an access token, "ci" a service with a trust token, "runner"
// a service with a job token, and "glass" a break-glass account.
type swSubjects struct{}

func (swSubjects) Subject(_ context.Context, p identity.Principal) (gate.Subject, error) {
	s := gate.Subject{
		ID: p.Subject, Groups: nil, Kind: gate.PrincipalPerson, Credential: gate.CredentialAccessToken,
		AuthAt: time.Now(), BreakGlassAccount: false, Job: nil,
	}

	switch p.Subject {
	case "ci":
		s.Kind, s.Credential = gate.PrincipalService, gate.CredentialTrustToken
	case "runner", "runner-apply", "runner-plan", "runner-other":
		s.Kind, s.Credential = gate.PrincipalService, gate.CredentialJobToken

		held, err := identity.UnitObject("opentofu", "acme", map[string]string{
			"runner-apply": "web", "runner-plan": "web", "runner-other": "db",
		}[p.Subject])
		if p.Subject != "runner" && err == nil {
			s.Job = &gate.JobClaim{
				Job: "42", Phase: "apply", PassedApply: p.Subject != "runner-plan", Units: []identity.Object{held},
			}
		}
	case "glass":
		s.BreakGlassAccount = true
	case "stale":
		s.AuthAt = time.Time{}
	default:
	}

	return s, nil
}

// swFences gives the fence of the namespaces of the test service.
type swFences struct{ nss *namespace.Service }

func (f swFences) Fence(ctx context.Context, ns namespace.Name) (namespace.Fence, error) {
	n, err := f.nss.Get(ctx, ns)
	if err != nil {
		return namespace.Fence{}, err
	}

	return n.Fence(), nil
}

// errEngineDown is the error of an engine that does not answer.
var errEngineDown = errors.New("engine down")

// swEngine wraps an engine and fails the evaluation when fail is set.
type swEngine struct {
	gate.Engine

	fail error
}

func (e swEngine) Evaluate(ctx context.Context, set gate.PolicySet, reqs []gate.Request) ([]gate.Answer, error) {
	if e.fail != nil {
		return nil, e.fail
	}

	return e.Engine.Evaluate(ctx, set, reqs)
}

// swEnv is a service over a real store with the namespace acme.
type swEnv struct {
	svc   *gate.Service
	store *sqlite.Store
	fence namespace.Fence
}

func newSWEnv(t *testing.T, units gate.UnitSource, engineErr error) swEnv {
	t.Helper()

	return newWrappedSWEnv(t, units, engineErr, nil)
}

// newWrappedSWEnv is newSWEnv with a gate store that wrap makes from the real
// store, or the real store when wrap is nil.
func newWrappedSWEnv(
	t *testing.T, units gate.UnitSource, engineErr error, wrap func(*sqlite.Store) gate.Store,
) swEnv {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	nss, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Installation: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	if _, err = nss.Create(t.Context(), "acme"); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	fence, err := swFences{nss: nss}.Fence(t.Context(), "acme")
	if err != nil {
		t.Fatal(err)
	}

	// The gates of a unit belong to the unit, so the units of the tests exist.
	for _, name := range []workspace.Name{"web", "other"} {
		w := workspace.Workspace{
			NamespaceID: "",
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
			Lock:        workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
			LastRelease: workspace.NoRelease(),
			ID:          workspace.ID("ws-test-" + string(name)),
			Ref:         workspace.Ref{Namespace: "acme", Name: name},
			Overrides: namespace.Overrides{
				Forced:   false,
				Unlocked: false,
			},
			Revision: 1,
			Origin:   identity.StateOrigin{Job: "", Commit: ""},
		}
		if err = st.CreateWorkspace(t.Context(), w, fence); err != nil {
			t.Fatalf("create workspace %s: %v", name, err)
		}
	}

	var gates gate.Store = st
	if wrap != nil {
		gates = wrap(st)
	}

	svc, err := gate.NewService(gate.ServiceConfig{
		Store:     gates,
		Engine:    swEngine{Engine: cedargo.New(cedargo.WithAccessActions("unit:write", "unit:read")), fail: engineErr},
		Producers: nil,
		Units:     units,
		Subjects:  swSubjects{},
		Fences:    swFences{nss: nss},
		Now:       nil,
		Log:       slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}

	return swEnv{svc: svc, store: st, fence: fence}
}

func swUnit(t *testing.T, name string) identity.Object {
	t.Helper()

	u, err := identity.UnitObject("opentofu", "acme", name)
	if err != nil {
		t.Fatal(err)
	}

	return u
}

func swEntry() audit.Entry {
	return audit.Entry{
		At: time.Now(), Namespace: "acme", Subject: "root", IDPSubject: "", TokenID: "",
		Action: "namespace:manage-gates", Object: "acme", Version: "", ValuePath: "", ValueCount: nil,
		Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}
}

// Where a test gate sits: on the unit under test, on its namespace, or on a
// unit that is not under test.
const (
	atUnit      = "unit"
	atNamespace = "namespace"
	atOtherUnit = "other-unit"
)

// swGate is a gate at state.write with the given behavior.
type swGate struct {
	at        string
	id        string
	condition string
	outcome   gate.Outcome
	message   string
	mode      gate.Mode
	onError   gate.OnError
}

// newSWGate returns a gate in enforce mode that fails on an error.
func newSWGate(at, id, condition string, outcome gate.Outcome, message string) swGate {
	return swGate{
		at: at, id: id, condition: condition, outcome: outcome, message: message, mode: gate.ModeEnforce,
		onError: gate.OnErrorFail,
	}
}

// inEvaluate returns g in evaluate mode.
func (g swGate) inEvaluate() swGate {
	g.mode = gate.ModeEvaluate

	return g
}

// skippingErrors returns g with onError skip.
func (g swGate) skippingErrors() swGate {
	g.onError = gate.OnErrorSkip

	return g
}

// scopeAt returns the scope that name stands for, for the unit web.
func scopeAt(t *testing.T, at string) gate.Scope {
	t.Helper()

	switch at {
	case atNamespace:
		return gate.NamespaceScope("acme")
	case atOtherUnit:
		return swUnitScope(t, "other")
	default:
		return swUnitScope(t, "web")
	}
}

func (e swEnv) putGate(t *testing.T, g swGate) {
	t.Helper()

	obj := gate.Gate{
		Scope: scopeAt(
			t,
			g.at,
		),
		ID:            gate.ID(g.id),
		Version:       0,
		DecisionPoint: gate.PointStateWrite,
		Condition:     g.condition,
		Unless:        "",
		Outcome:       g.outcome,
		Message:       g.message,
		Mode:          g.mode,
		OnError:       g.onError,
		Final:         false,
		CheckedWith: gate.CheckedWith{
			Engine: "test/1", Schema: gate.SchemaVersion{Subject: string(gate.PointStateWrite), Major: 1, Minor: 0},
		},
		State: "",
		Reads: nil,
	}

	if _, err := e.store.Gates().Put(t.Context(), obj, 0, e.fence, swEntry()); err != nil {
		t.Fatalf("put gate %s: %v", g.id, err)
	}
}

func (e swEnv) putLimit(t *testing.T, scope gate.Scope, id, policy string) {
	t.Helper()

	l := gate.AccessLimit{Scope: scope, ID: gate.ID(id), Version: 0, Policy: policy}
	if _, err := e.store.AccessLimits().Put(t.Context(), l, 0, e.fence, swEntry()); err != nil {
		t.Fatalf("put limit %s: %v", id, err)
	}
}

func (e swEnv) records(t *testing.T) []gate.Record {
	t.Helper()

	rs, err := e.store.Records().Query(t.Context(), "acme", gate.RecordQuery{
		Point: gate.PointStateWrite,
		From:  time.Unix(0, 0),
		To:    time.Now().Add(time.Hour),
		Limit: gate.MaxSimulateRecords,
	})
	if err != nil {
		t.Fatalf("query records: %v", err)
	}

	return rs
}

// newestRecord returns the newest record and fails the test when there is none.
func (e swEnv) newestRecord(t *testing.T) gate.Record {
	t.Helper()

	recs := e.records(t)
	if len(recs) == 0 {
		t.Fatal("no decision record")
	}

	return recs[0]
}

func swWrite(t *testing.T, subject, unit string, op gate.OperationKind) gate.StateWrite {
	t.Helper()

	return gate.StateWrite{
		Principal: principal(subject, false), Unit: swUnit(t, unit), Operation: op,
		Action: identity.ActionUnitWrite, Source: gate.SourceCLI, PlanLock: false, State: nil,
	}
}

func swUnitScope(t *testing.T, name string) gate.Scope {
	t.Helper()

	s, err := gate.UnitScope("acme", "opentofu", name)
	if err != nil {
		t.Fatal(err)
	}

	return s
}

// swUnitsOfTests are the collection and the labels of the unit web.
func swUnitsOfTests() swUnits {
	return swUnits{
		collections: map[string]collection.Name{"opentofu:acme/web": "prod"},
		labels:      map[string][]string{"opentofu:acme/web": {"team=core"}},
	}
}

// swCase is one case of the gate table: the gates, the caller and the result.
type swCase struct {
	name     string
	gates    []swGate
	rule     bool
	subject  string
	op       gate.OperationKind
	want     error
	result   gate.Result
	message  string
	warnings int
	requires int
}

// allowCase is a case that nothing refuses, for the person alice.
func allowCase(name string, result gate.Result, gates ...swGate) swCase {
	return swCase{
		name: name, gates: gates, rule: false, subject: "alice", op: gate.OperationLock, want: nil, result: result,
		message: "", warnings: 0, requires: 0,
	}
}

// refuseCase is a case that a gate refuses with want, for the person alice.
func refuseCase(name string, want error, result gate.Result, message string, gates ...swGate) swCase {
	return swCase{
		name: name, gates: gates, rule: false, subject: "alice", op: gate.OperationLock, want: want, result: result,
		message: message, warnings: 0, requires: 0,
	}
}

// bySubject returns tc for another caller.
func (tc swCase) bySubject(subject string) swCase {
	tc.subject = subject

	return tc
}

func swCases() []swCase {
	lock := `context.operation.kind == "lock"`
	broken := `context.unit.nothing == "x"`

	warnCase := allowCase("a warn is in the decision and does not refuse", gate.ResultWarn,
		newSWGate(atUnit, "careful", "", gate.OutcomeWarn, "take care"))
	warnCase.warnings = 1

	approval := refuseCase(
		"an approval outcome refuses a CLI and names the rule and the unit",
		gate.ErrDenied,
		gate.ResultAwaitingApproval,
		"needs approval by rule two-eyes. A write from a CLI cannot wait for approval, so the server refuses the write to opentofu:acme/web",
		newSWGate(atUnit, "needs-two", "", gate.ApprovalOutcome("two-eyes"), "Prod writes need review."),
	)
	approval.rule, approval.requires = true, 1

	skip := newSWGate(atUnit, "broken-skip", broken, gate.OutcomeDeny, "x").skippingErrors()

	return []swCase{
		allowCase("no gate allows", gate.ResultAllow),
		refuseCase("an enforced deny that matches refuses and names the facts", gate.ErrDenied, gate.ResultDeny,
			"Locks of prod stop: team=core",
			newSWGate(atUnit, "no-lock", lock, gate.OutcomeDeny, "Locks of {{unit.collection}} stop: {{unit.labels}}")),
		allowCase("an enforced deny that does not match allows", gate.ResultAllow,
			newSWGate(atUnit, "no-unlock", `context.operation.kind == "unlock"`, gate.OutcomeDeny, "no")),
		allowCase("an evaluate-mode deny that matches allows", gate.ResultAllow,
			newSWGate(atUnit, "watch", "", gate.OutcomeDeny, "would stop").inEvaluate()),
		warnCase,
		refuseCase(
			"a gate of the namespace holds for the unit",
			gate.ErrDenied,
			gate.ResultDeny,
			"services wait",
			newSWGate(
				atNamespace,
				"ns-freeze",
				`context.principal.kind == "service"`,
				gate.OutcomeDeny,
				"services wait",
			),
		).bySubject("ci"),
		allowCase(
			"a gate of the namespace skips a person it does not match",
			gate.ResultAllow,
			newSWGate(
				atNamespace,
				"ns-freeze",
				`context.principal.kind == "service"`,
				gate.OutcomeDeny,
				"services wait",
			),
		),
		allowCase("a gate of another unit does not hold", gate.ResultAllow,
			newSWGate(atOtherUnit, "other-unit", "", gate.OutcomeDeny, "no")),
		approval,
		refuseCase("a gate in error fails closed", gate.ErrRuleFailed, gate.ResultError, "broken",
			newSWGate(atUnit, "broken", broken, gate.OutcomeDeny, "x")),
		allowCase("a gate in error with onError skip counts as no match", gate.ResultAllow, skip),
		allowCase("a gate in error in evaluate mode stops nothing", gate.ResultAllow,
			newSWGate(atUnit, "broken-eval", broken, gate.OutcomeDeny, "x").inEvaluate()),
		refuseCase("a message that names no fact fails closed", gate.ErrRuleFailed, gate.ResultError,
			"unit.nothing",
			newSWGate(atUnit, "bad-template", "", gate.OutcomeDeny, "see {{unit.nothing}}")),
		refuseCase("a deny wins over a warn", gate.ErrDenied, gate.ResultDeny, "d",
			newSWGate(atUnit, "a-warn", "", gate.OutcomeWarn, "w"),
			newSWGate(atUnit, "b-deny", "", gate.OutcomeDeny, "d")),
	}
}

// putRule stores the approval rule two-eyes in the namespace.
func (e swEnv) putRule(t *testing.T) {
	t.Helper()

	rule := gate.ApprovalRule{
		Scope: gate.NamespaceScope("acme"), ID: "two-eyes", Version: 0,
		Approvers: gate.Approvers{Groups: []string{"sre"}, GroupIDs: nil, Users: nil, UnitOwners: false}, Count: 2,
		AuthorMayApprove: false, FreshAuth: gate.NoFreshAuth(), Timeout: 0,
	}

	if _, err := e.store.ApprovalRules().Put(t.Context(), rule, 0, e.fence, swEntry()); err != nil {
		t.Fatal(err)
	}
}

// run checks the case on a fresh store.
func (tc swCase) run(t *testing.T) {
	t.Helper()

	e := newSWEnv(t, swUnitsOfTests(), nil)

	if tc.rule {
		e.putRule(t)
	}

	for _, g := range tc.gates {
		e.putGate(t, g)
	}

	d, err := e.svc.GuardStateWrite(t.Context(), swWrite(t, tc.subject, "web", tc.op))

	tc.checkRecord(t, e, tc.checkOutcome(t, d, err))
}

// checkOutcome checks the decision or the refusal of the case and returns the
// ID of the record that it names.
func (tc swCase) checkOutcome(t *testing.T, d gate.Decision, err error) gate.RecordID {
	t.Helper()

	if tc.want == nil {
		if err != nil || d.Result != tc.result || len(d.Warnings) != tc.warnings {
			t.Fatalf("GuardStateWrite() = %+v, %v, want result %s and %d warnings", d, err, tc.result, tc.warnings)
		}

		return d.Record
	}

	refusal, ok := errors.AsType[*gate.RefusalError](err)
	if !ok || !errors.Is(err, tc.want) {
		t.Fatalf("GuardStateWrite() = %v, want a refusal that wraps %v", err, tc.want)
	}

	if !strings.Contains(strings.Join(refusal.Messages, " "), tc.message) {
		t.Fatalf("messages = %q, want %q in them", refusal.Messages, tc.message)
	}

	return refusal.Record
}

// checkRecord checks that the evaluation wrote exactly one record.
func (tc swCase) checkRecord(t *testing.T, e swEnv, record gate.RecordID) {
	t.Helper()

	recs := e.records(t)
	if len(recs) != 1 {
		t.Fatalf("records = %d, want one record for one evaluation", len(recs))
	}

	rec := recs[0]
	if rec.ID != record || rec.Result != tc.result || len(rec.Requirements) != tc.requires {
		t.Fatalf("record = %s %s with %d requirements, want %s %s with %d",
			rec.ID, rec.Result, len(rec.Requirements), record, tc.result, tc.requires)
	}

	if rec.Subject != tc.subject || rec.Schema.String() != "state.write/1.0" || rec.Count != 1 {
		t.Fatalf("record subject %q schema %s count %d", rec.Subject, rec.Schema, rec.Count)
	}

	if len(rec.Units) != 1 || rec.Units[0].Facts.Operation.Kind != tc.op || rec.Units[0].Digest == "" {
		t.Fatalf("record units = %+v", rec.Units)
	}
}

func TestGuardStateWriteGates(t *testing.T) {
	t.Parallel()

	for _, tc := range swCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t)
		})
	}
}

func TestEvaluateModeGateIsInTheRecord(t *testing.T) {
	t.Parallel()

	e := newSWEnv(t, swUnits{collections: nil, labels: nil}, nil)
	e.putGate(t, newSWGate(atUnit, "watch", "", gate.OutcomeDeny, "would stop").inEvaluate())

	if _, err := e.svc.GuardStateWrite(t.Context(), swWrite(t, "alice", "web", gate.OperationLock)); err != nil {
		t.Fatalf("GuardStateWrite() = %v, want nil: the gate is in evaluate mode", err)
	}

	gates := e.newestRecord(t).Units[0].Gates
	if len(gates) != 1 || !gates[0].Matched || gates[0].Mode != gate.ModeEvaluate || gates[0].Message != "would stop" {
		t.Fatalf("record gates = %+v, want the matched gate in evaluate mode", gates)
	}
}

func TestEngineErrorFailsClosed(t *testing.T) {
	t.Parallel()

	e := newSWEnv(t, swUnits{collections: nil, labels: nil}, errEngineDown)
	e.putGate(t, newSWGate(atUnit, "any", "", gate.OutcomeDeny, "x").skippingErrors())

	_, err := e.svc.GuardStateWrite(t.Context(), swWrite(t, "alice", "web", gate.OperationLock))
	if !errors.Is(err, gate.ErrUnavailable) {
		t.Fatalf("GuardStateWrite() = %v, want ErrUnavailable: an engine that does not answer refuses", err)
	}

	recs := e.records(t)
	if len(recs) != 1 || recs[0].Result != gate.ResultError {
		t.Fatalf("records = %+v, want one record with the result error", recs)
	}
}

func TestEngineErrorWithoutEnforcedGateAllows(t *testing.T) {
	t.Parallel()

	e := newSWEnv(t, swUnits{collections: nil, labels: nil}, errEngineDown)
	e.putGate(t, newSWGate(atUnit, "watch", "", gate.OutcomeDeny, "x").inEvaluate())

	if _, err := e.svc.GuardStateWrite(t.Context(), swWrite(t, "alice", "web", gate.OperationLock)); err != nil {
		t.Fatalf("GuardStateWrite() = %v, want nil: only an evaluate-mode gate hit the engine error", err)
	}
}

func TestEachEvaluationWritesOneRecord(t *testing.T) {
	t.Parallel()

	e := newSWEnv(t, swUnits{collections: nil, labels: nil}, nil)

	for _, op := range []gate.OperationKind{
		gate.OperationLock, gate.OperationUnlock, gate.OperationRollback, gate.OperationDelete,
	} {
		if _, err := e.svc.GuardStateWrite(t.Context(), swWrite(t, "alice", "web", op)); err != nil {
			t.Fatalf("GuardStateWrite(%s) = %v", op, err)
		}
	}

	recs := e.records(t)
	if len(recs) != 4 {
		t.Fatalf("records = %d, want 4: one for each evaluation", len(recs))
	}

	ids := map[gate.RecordID]bool{}
	for i := range recs {
		ids[recs[i].ID] = true
	}

	if len(ids) != 4 {
		t.Fatalf("record IDs = %v, want 4 distinct", ids)
	}
}

// swChange returns a change at state.write for the unit web.
func swChange(t *testing.T, fence namespace.Fence) gate.Change {
	t.Helper()

	return gate.Change{
		Point: gate.PointStateWrite, Namespace: "acme", Ref: gate.ChangeRef{Kind: gate.ChangeStateWrite, ID: ""},
		Subject: gate.Subject{
			ID: "alice", Groups: nil, Kind: gate.PrincipalPerson, Credential: gate.CredentialSession,
			AuthAt: time.Time{}, BreakGlassAccount: false, Job: nil,
		},
		Source: gate.SourceCLI, Author: "alice", Operation: gate.OperationLock,
		Units:   []identity.Object{swUnit(t, "web")},
		Holders: nil, PlanLock: false, BreakGlass: "", Fence: fence, State: nil,
	}
}

func TestBreakGlassChangeFailsClosed(t *testing.T) {
	t.Parallel()

	e := newSWEnv(t, swUnits{collections: nil, labels: nil}, nil)
	c := swChange(t, e.fence)
	c.BreakGlass = "audit-1"

	if _, err := e.svc.Evaluate(t.Context(), c); !errors.Is(err, gate.ErrUnavailable) {
		t.Fatalf("Evaluate(break-glass) = %v, want ErrUnavailable: no break-glass is implemented", err)
	}
}

func TestEvaluateRefusesAFenceOfAnotherNamespace(t *testing.T) {
	t.Parallel()

	e := newSWEnv(t, swUnits{collections: nil, labels: nil}, nil)
	c := swChange(t, namespace.Fence{Namespace: "globex", FailoverVersion: 1})

	if _, err := e.svc.Evaluate(t.Context(), c); !errors.Is(err, gate.ErrInvalid) {
		t.Fatalf("Evaluate(fence of globex) = %v, want ErrInvalid", err)
	}

	if recs := e.records(t); len(recs) != 0 {
		t.Fatalf("records = %d, want none", len(recs))
	}
}

// swLimitCase is one case of the access limit table.
type swLimitCase struct {
	name    string
	scope   func() gate.Scope
	policy  string
	subject string
	unit    string
	want    error
}

const forbidServices = `forbid (principal, action == OpenDustpan::Action::"unit:write", resource) ` +
	`when { context.principal.kind == "service" };`

const recentOnly = `forbid (principal, action, resource) unless { context.authAgeSeconds < 900 };`

func swLimitCases(t *testing.T) []swLimitCase {
	t.Helper()

	web, other := swUnitScope(t, "web"), swUnitScope(t, "other")
	prod := gate.CollectionScope("acme", "prod")
	ns := gate.NamespaceScope("acme")
	at := func(s gate.Scope) func() gate.Scope { return func() gate.Scope { return s } }

	return []swLimitCase{
		{"a forbid of the unit refuses a service", at(web), forbidServices, "ci", "web", gate.ErrLimited},
		{"a forbid of the unit passes a person", at(web), forbidServices, "alice", "web", nil},
		{"a forbid of the namespace holds for the unit", at(ns), forbidServices, "ci", "web", gate.ErrLimited},
		{"a forbid of the collection holds for a unit in it", at(prod), forbidServices, "ci", "web", gate.ErrLimited},
		{"a forbid of the collection does not hold for a unit outside it", at(prod), forbidServices, "ci", "db", nil},
		{"a forbid of another unit does not hold", at(other), forbidServices, "ci", "web", nil},
		{"a forbid holds for a job token", at(web), forbidServices, "runner", "web", gate.ErrLimited},
		{
			"a break-glass account is outside the limits",
			at(web),
			`forbid (principal, action, resource);`,
			"glass",
			"web",
			nil,
		},
		{"an unknown authentication age is not recent", at(web), recentOnly, "stale", "web", gate.ErrLimited},
		{"a recent authentication passes", at(web), recentOnly, "alice", "web", nil},
		{
			"a limit that gives an error fails closed", at(web),
			`forbid (principal, action, resource) when { context.unit.nothing == "x" };`, "alice", "web",
			gate.ErrRuleFailed,
		},
	}
}

func (tc swLimitCase) run(t *testing.T) {
	t.Helper()

	units := swUnits{collections: map[string]collection.Name{"opentofu:acme/web": "prod"}, labels: nil}
	e := newSWEnv(t, units, nil)
	e.putLimit(t, tc.scope(), "limit-1", tc.policy)

	_, err := e.svc.GuardStateWrite(t.Context(), swWrite(t, tc.subject, tc.unit, gate.OperationLock))
	if tc.want == nil {
		if err != nil {
			t.Fatalf("GuardStateWrite() = %v, want nil", err)
		}

		return
	}

	refusal, ok := errors.AsType[*gate.RefusalError](err)
	if !ok || !errors.Is(err, tc.want) {
		t.Fatalf("GuardStateWrite() = %v, want a refusal that wraps %v", err, tc.want)
	}

	if !slices.ContainsFunc(refusal.Messages, func(m string) bool { return strings.Contains(m, "limit-1") }) {
		t.Fatalf("messages = %q, want the limit ID", refusal.Messages)
	}

	rec, getErr := e.store.Records().Get(t.Context(), "acme", refusal.Record)
	if getErr != nil || rec.Change.Kind != gate.ChangeAccess || rec.Action != "unit:write" {
		t.Fatalf("record = %+v, %v, want an access record for unit:write", rec, getErr)
	}
}

func TestAccessLimits(t *testing.T) {
	t.Parallel()

	for _, tc := range swLimitCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t)
		})
	}
}

func TestLimitRefusalsShareOneRecord(t *testing.T) {
	t.Parallel()

	e := newSWEnv(t, swUnits{collections: nil, labels: nil}, nil)
	e.putLimit(t, swUnitScope(t, "web"), "limit-1", forbidServices)

	var first gate.RecordID

	for i := range 3 {
		_, err := e.svc.GuardStateWrite(t.Context(), swWrite(t, "ci", "web", gate.OperationLock))

		refusal, ok := errors.AsType[*gate.RefusalError](err)
		if !ok {
			t.Fatalf("GuardStateWrite() = %v, want a refusal", err)
		}

		if i == 0 {
			first = refusal.Record
		}

		if refusal.Record != first {
			t.Fatalf(
				"refusal %d has record %s, want %s: refusals in one window share a record",
				i,
				refusal.Record,
				first,
			)
		}
	}

	rec, err := e.store.Records().Get(t.Context(), "acme", first)
	if err != nil || rec.Count != 3 {
		t.Fatalf("record = %+v, %v, want Count 3", rec, err)
	}
}

func TestJobTokenWritesOnlyWhatTheJobModelAllows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		subject  string
		planLock bool
		denied   bool
	}{
		{"a phase that passed apply writes a unit of its job", "runner-apply", false, false},
		{"a plan phase takes the lock of its plan", "runner-plan", true, false},
		{"a plan phase writes no other state", "runner-plan", false, true},
		{"a job token with no job is denied", "runner", false, true},
		{"a job token with no job is denied for a plan lock", "runner", true, true},
		{"a unit outside the job is denied", "runner-other", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := newSWEnv(t, swUnits{collections: nil, labels: nil}, nil)
			e.putGate(t, newSWGate(atUnit, "deny-all", "", gate.OutcomeDeny, "x"))

			w := swWrite(t, tt.subject, "web", gate.OperationLock)
			w.PlanLock = tt.planLock

			d, err := e.svc.GuardStateWrite(t.Context(), w)

			if tt.denied {
				if !errors.Is(err, identity.ErrDenied) {
					t.Fatalf("GuardStateWrite() = %+v, %v, want identity.ErrDenied", d, err)
				}
			} else if err != nil || d.Result != gate.ResultAllow || d.Record != "" {
				t.Fatalf("GuardStateWrite() = %+v, %v, want allow with no record: the job model decided", d, err)
			}

			if recs := e.records(t); len(recs) != 0 {
				t.Fatalf("records = %d, want none for a job token", len(recs))
			}
		})
	}
}

func TestMessageTemplateNeverRendersEmpty(t *testing.T) {
	t.Parallel()

	e := newSWEnv(t, swUnits{collections: nil, labels: nil}, nil)
	e.putGate(t, newSWGate(atUnit, "plain", "", gate.OutcomeDeny,
		"Stopped by line one.\nline two {{principal.kind}}"))

	_, err := e.svc.GuardStateWrite(t.Context(), swWrite(t, "alice", "web", gate.OperationLock))

	refusal, ok := errors.AsType[*gate.RefusalError](err)
	if !ok || len(refusal.Messages) != 1 {
		t.Fatalf("GuardStateWrite() = %v, want one refusal message", err)
	}

	if strings.ContainsAny(refusal.Messages[0], "\r\n") || !strings.HasSuffix(refusal.Messages[0], "line two person") {
		t.Fatalf("message = %q, want one line that ends with the rendered fact", refusal.Messages[0])
	}
}

// countingUnits counts the reads of the unit facts.
type countingUnits struct {
	swUnits

	mu    sync.Mutex
	reads int
}

func (c *countingUnits) Unit(ctx context.Context, unit identity.Object) (gate.UnitInfo, error) {
	c.mu.Lock()
	c.reads++
	c.mu.Unlock()

	return c.swUnits.Unit(ctx, unit)
}

func TestGuardReadsTheUnitFactsOnce(t *testing.T) {
	t.Parallel()

	units := &countingUnits{swUnits: swUnitsOfTests(), mu: sync.Mutex{}, reads: 0}
	e := newSWEnv(t, units, nil)
	e.putLimit(t, swUnitScope(t, "web"), "limit-1", forbidServices)
	e.putGate(t, newSWGate(atUnit, "watch", "", gate.OutcomeWarn, "careful"))

	if _, err := e.svc.GuardStateWrite(t.Context(), swWrite(t, "alice", "web", gate.OperationLock)); err != nil {
		t.Fatalf("GuardStateWrite() = %v", err)
	}

	units.mu.Lock()
	defer units.mu.Unlock()

	if units.reads != 1 {
		t.Fatalf("reads = %d, want 1 for one check of the limits and the gates", units.reads)
	}
}

// countingStore counts the reads of the gate configuration, and fails each
// read with fail when fail is set.
type countingStore struct {
	*sqlite.Store

	mu        sync.Mutex
	snapshots int
	fail      error
}

func (c *countingStore) Snapshot(ctx context.Context, scopes []gate.Scope) (gate.Config, error) {
	c.mu.Lock()
	c.snapshots++
	fail := c.fail
	c.mu.Unlock()

	if fail != nil {
		return gate.Config{}, fail
	}

	return c.Store.Snapshot(ctx, scopes)
}

// newCountingSWEnv returns an env whose gate store counts its reads.
func newCountingSWEnv(t *testing.T, fail error) (swEnv, *countingStore) {
	t.Helper()

	counting := &countingStore{Store: nil, mu: sync.Mutex{}, snapshots: 0, fail: fail}

	e := newWrappedSWEnv(t, swUnitsOfTests(), nil, func(st *sqlite.Store) gate.Store {
		counting.Store = st

		return counting
	})

	return e, counting
}

// TestGuardReadsTheGateConfigurationOnce checks that one guard call reads the
// configuration of the unit once for the access limits and the gates, and
// that both checks still decide on it: the limit refuses a service, and the
// gate denies a person.
func TestGuardReadsTheGateConfigurationOnce(t *testing.T) {
	t.Parallel()

	e, counting := newCountingSWEnv(t, nil)
	e.putLimit(t, swUnitScope(t, "web"), "limit-1", forbidServices)
	e.putGate(t, newSWGate(atUnit, "no-person", `context.principal.kind == "person"`, gate.OutcomeDeny, "no"))

	_, err := e.svc.GuardStateWrite(t.Context(), swWrite(t, "ci", "web", gate.OperationLock))
	if !errors.Is(err, gate.ErrLimited) {
		t.Fatalf("GuardStateWrite(service) = %v, want ErrLimited by the access limit", err)
	}

	_, err = e.svc.GuardStateWrite(t.Context(), swWrite(t, "alice", "web", gate.OperationLock))
	if !errors.Is(err, gate.ErrDenied) {
		t.Fatalf("GuardStateWrite(person) = %v, want ErrDenied by the gate", err)
	}

	counting.mu.Lock()
	defer counting.mu.Unlock()

	// The service stops at the limit, before the gates: 1 read. The person
	// passes the limit and meets the gate with the same read: 1 read.
	if counting.snapshots != 2 {
		t.Fatalf("configuration reads = %d, want 1 for each of the 2 guard calls", counting.snapshots)
	}
}

// errSnapshotDown is the error of a gate store that cannot read the
// configuration.
var errSnapshotDown = errors.New("snapshot down")

// TestGuardFailsClosedWhenTheConfigurationCannotBeRead checks that a store
// that cannot read the configuration refuses the write, at the access limits
// and, for a break-glass account that skips the limits, at the gates.
func TestGuardFailsClosedWhenTheConfigurationCannotBeRead(t *testing.T) {
	t.Parallel()

	for _, subject := range []string{"alice", "glass"} {
		e, counting := newCountingSWEnv(t, errSnapshotDown)

		_, err := e.svc.GuardStateWrite(t.Context(), swWrite(t, subject, "web", gate.OperationLock))
		if !errors.Is(err, gate.ErrUnavailable) || !errors.Is(err, errSnapshotDown) {
			t.Fatalf("GuardStateWrite(%s) = %v, want ErrUnavailable with the store error", subject, err)
		}

		counting.mu.Lock()
		reads := counting.snapshots
		counting.mu.Unlock()

		if reads != 1 {
			t.Fatalf("configuration reads of %s = %d, want 1: a failed read is not read again", subject, reads)
		}

		if rs := e.records(t); len(rs) != 0 {
			t.Fatalf("records = %d after a failed read, want none: no decision was made", len(rs))
		}
	}
}

// errUnitsDown is the error of a unit source that does not answer.
var errUnitsDown = errors.New("units down")

// swFailingUnits is a unit source that does not answer.
type swFailingUnits struct{}

func (swFailingUnits) Unit(context.Context, identity.Object) (gate.UnitInfo, error) {
	return gate.UnitInfo{Collection: "", Labels: nil}, errUnitsDown
}

// TestGuardFailsClosedWhenTheUnitFactsCannotBeRead checks that a unit source
// that does not answer refuses the write, at the access limits and, for a
// break-glass account that skips the limits, at the gates.
func TestGuardFailsClosedWhenTheUnitFactsCannotBeRead(t *testing.T) {
	t.Parallel()

	for _, subject := range []string{"alice", "glass"} {
		e := newSWEnv(t, swFailingUnits{}, nil)

		_, err := e.svc.GuardStateWrite(t.Context(), swWrite(t, subject, "web", gate.OperationLock))
		if !errors.Is(err, gate.ErrUnavailable) || !errors.Is(err, errUnitsDown) {
			t.Fatalf("GuardStateWrite(%s) = %v, want ErrUnavailable with the source error", subject, err)
		}
	}
}

func TestHugeLabelSetIsTruncatedInTheFactDocument(t *testing.T) {
	t.Parallel()

	huge := strings.Repeat("x", gate.MaxFactDocumentBytes)
	units := swUnits{collections: nil, labels: map[string][]string{"opentofu:acme/web": {"big=" + huge}}}
	e := newSWEnv(t, units, nil)
	e.putGate(t, newSWGate(atUnit, "watch", "", gate.OutcomeWarn, "careful"))

	d, err := e.svc.GuardStateWrite(t.Context(), swWrite(t, "alice", "web", gate.OperationLock))
	if err != nil || d.Result != gate.ResultWarn {
		t.Fatalf("GuardStateWrite() = %+v, %v, want warn: the gate still runs on the smaller document", d, err)
	}

	facts := e.newestRecord(t).Units[0].Facts
	if len(facts.Unit.Labels) != 0 || !slices.Equal(facts.Document.Truncated, []string{"unit.labels"}) {
		t.Fatalf("facts = %+v, want the labels emptied and named in document.truncated", facts)
	}
}

// swUnknownUnits knows no unit.
type swUnknownUnits struct {
	swUnits
}

func (swUnknownUnits) Unit(context.Context, identity.Object) (gate.UnitInfo, error) {
	return gate.UnitInfo{Collection: "", Labels: nil}, gate.ErrNotFound
}

// A unit reference that resolves to nothing fails closed at state.write. The
// gates never see a unit with no collection and no labels in its place, so a
// gate that matches on labels cannot be skipped with a reference to an unknown
// unit.
func TestUnknownUnitFailsClosedAtStateWrite(t *testing.T) {
	t.Parallel()

	e := newSWEnv(t, swUnknownUnits{swUnits{collections: nil, labels: nil}}, nil)

	_, err := e.svc.GuardStateWrite(t.Context(), swWrite(t, "alice", "web", gate.OperationLock))
	if !errors.Is(err, gate.ErrUnavailable) {
		t.Fatalf("GuardStateWrite(unknown unit) = %v, want ErrUnavailable", err)
	}
}

// TestStateWriteGateStaysWithTheUnit checks that the admission of a state
// write finds the gates of the unit by its stable ID: an enforced deny of the
// workspace web refuses its writes, and a new workspace web, made after the
// delete of the old one, starts with no gate.
func TestStateWriteGateStaysWithTheUnit(t *testing.T) {
	t.Parallel()

	e := newSWEnv(t, swUnits{collections: nil, labels: nil}, nil)
	e.putGate(t, newSWGate(atUnit, "no-writes", "", gate.OutcomeDeny, "frozen"))

	_, err := e.svc.GuardStateWrite(t.Context(), swWrite(t, "alice", "web", gate.OperationLock))
	if !errors.Is(err, gate.ErrDenied) {
		t.Fatalf("GuardStateWrite(web) = %v, want ErrDenied: the gate of the unit holds", err)
	}

	ref := workspace.Ref{Namespace: "acme", Name: "web"}
	del := inventory.Deletion{Entry: swEntry(), PurgeAfter: time.Now().Add(time.Hour)}

	if err = e.store.DeleteWorkspace(t.Context(), ref, e.fence, del); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}

	w := workspace.Workspace{
		NamespaceID: "",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Lock:        workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease: workspace.NoRelease(),
		ID:          "ws-test-web-again",
		Ref:         ref,
		Overrides: namespace.Overrides{
			Forced:   false,
			Unlocked: false,
		},
		Revision: 1,
		Origin:   identity.StateOrigin{Job: "", Commit: ""},
	}
	if err = e.store.CreateWorkspace(t.Context(), w, e.fence); err != nil {
		t.Fatalf("create workspace again: %v", err)
	}

	if _, err = e.svc.GuardStateWrite(t.Context(), swWrite(t, "alice", "web", gate.OperationLock)); err != nil {
		t.Fatalf("GuardStateWrite(new web) = %v, want nil: a new unit with a used name starts clean", err)
	}
}

// deleteSWWeb deletes the workspace web of e.
func (e swEnv) deleteSWWeb(t *testing.T) {
	t.Helper()

	ref := workspace.Ref{Namespace: "acme", Name: "web"}
	del := inventory.Deletion{Entry: swEntry(), PurgeAfter: time.Now().Add(time.Hour)}

	if err := e.store.DeleteWorkspace(t.Context(), ref, e.fence, del); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}
}

// reuseSWWeb makes a new workspace web, after the delete of the old one, with
// a deny gate and an access limit of its own.
func (e swEnv) reuseSWWeb(t *testing.T) {
	t.Helper()

	w := workspace.Workspace{
		NamespaceID: "",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Lock:        workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease: workspace.NoRelease(),
		ID:          "ws-test-web-new",
		Ref:         workspace.Ref{Namespace: "acme", Name: "web"},
		Overrides: namespace.Overrides{
			Forced:   false,
			Unlocked: false,
		},
		Revision: 1,
		Origin:   identity.StateOrigin{Job: "", Commit: ""},
	}
	if err := e.store.CreateWorkspace(t.Context(), w, e.fence); err != nil {
		t.Fatalf("create workspace again: %v", err)
	}

	e.putGate(t, newSWGate(atUnit, "new-deny", "", gate.OutcomeDeny, "new unit"))
	e.putLimit(t, swUnitScope(t, "web"), "new-limit", forbidServices)
}

// swDeletedWeb returns a state write of the deleted workspace web, named by
// its stable ID, as the restore and the purge of a deleted unit name it.
func swDeletedWeb(t *testing.T, subject string, op gate.OperationKind) gate.StateWrite {
	t.Helper()

	w := swWrite(t, subject, "web", op)
	w.Unit = w.Unit.WithUnitID("ws-test-web")

	return w
}

// swDeletedCase is one case of TestDeletedUnitWriteChecksItsOwnGates.
type swDeletedCase struct {
	op gate.OperationKind
	// reuse makes a new live workspace web after the delete.
	reuse bool
}

// TestDeletedUnitWriteChecksItsOwnGates checks that the restore and the purge
// of a deleted unit, which name it by its stable ID, check the gates and the
// access limits of that unit: with no live unit of its name, and with a new
// live unit of its name, whose gates and limits do not hold.
func TestDeletedUnitWriteChecksItsOwnGates(t *testing.T) {
	t.Parallel()

	for _, op := range []gate.OperationKind{gate.OperationRollback, gate.OperationDelete} {
		for _, reuse := range []bool{false, true} {
			tc := swDeletedCase{op: op, reuse: reuse}
			name := fmt.Sprintf("%s with a new unit of the name: %v", op, reuse)

			t.Run(name+"/gate", func(t *testing.T) {
				t.Parallel()
				tc.checkGate(t)
			})

			t.Run(name+"/limit", func(t *testing.T) {
				t.Parallel()
				tc.checkLimit(t)
			})
		}
	}
}

// checkGate checks that the deleted unit's deny gate refuses the write, and
// that no gate of a new unit with its name is in the refusal.
func (tc swDeletedCase) checkGate(t *testing.T) {
	t.Helper()

	e := newSWEnv(t, swUnits{collections: nil, labels: nil}, nil)
	e.putGate(t, newSWGate(atUnit, "old-deny", "", gate.OutcomeDeny, "old unit"))
	e.deleteSWWeb(t)

	if tc.reuse {
		e.reuseSWWeb(t)
	}

	_, err := e.svc.GuardStateWrite(t.Context(), swDeletedWeb(t, "alice", tc.op))

	refusal, ok := errors.AsType[*gate.RefusalError](err)
	if !ok || !errors.Is(err, gate.ErrDenied) {
		t.Fatalf("GuardStateWrite(deleted web) = %v, want ErrDenied by the deleted unit's gate", err)
	}

	if !slices.Contains(refusal.Messages, "old unit") || slices.Contains(refusal.Messages, "new unit") {
		t.Fatalf("messages = %q, want the deleted unit's gate only", refusal.Messages)
	}
}

// checkLimit checks that the deleted unit's access limit refuses the write,
// and that the access limit of a new unit with its name does not.
func (tc swDeletedCase) checkLimit(t *testing.T) {
	t.Helper()

	e := newSWEnv(t, swUnits{collections: nil, labels: nil}, nil)

	if !tc.reuse {
		e.putLimit(t, swUnitScope(t, "web"), "old-limit", forbidServices)
	}

	e.deleteSWWeb(t)

	if tc.reuse {
		e.reuseSWWeb(t)
	}

	_, err := e.svc.GuardStateWrite(t.Context(), swDeletedWeb(t, "ci", tc.op))

	if limited := errors.Is(err, gate.ErrLimited); limited == tc.reuse {
		t.Fatalf("GuardStateWrite(deleted web) = %v, want ErrLimited only by the deleted unit's own limit", err)
	}
}
