package gate_test

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/gate/cedargo"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// swUnits gives the collection and the labels of the units of a test.
type swUnits struct {
	collections map[string]collection.Name
	labels      map[string][]string
}

func (u swUnits) Collection(_ context.Context, unit identity.Object) (collection.Name, error) {
	if c, ok := u.collections[unit.String()]; ok {
		return c, nil
	}

	return "", gate.ErrNotFound
}

func (u swUnits) Labels(_ context.Context, unit identity.Object) ([]string, error) {
	return u.labels[unit.String()], nil
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
	case "runner":
		s.Kind, s.Credential = gate.PrincipalService, gate.CredentialJobToken
	case "glass":
		s.BreakGlassAccount = true
	case "stale":
		s.AuthAt = time.Time{}
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

func newSWEnv(t *testing.T, units swUnits, engineErr error) swEnv {
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

	nss, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Region: "test"})
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

	svc, err := gate.NewService(gate.ServiceConfig{
		Store: st, Engine: swEngine{Engine: cedargo.New(cedargo.WithAccessActions("unit:write", "unit:read")), fail: engineErr},
		Producers: nil, Members: units, Units: units, Subjects: swSubjects{}, Fences: swFences{nss: nss},
		Now: nil, Log: slog.New(slog.DiscardHandler),
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

// swGate is a gate at state.write in scope with the given behavior.
type swGate struct {
	scope     gate.Scope
	id        string
	condition string
	outcome   gate.Outcome
	message   string
	mode      gate.Mode
	onError   gate.OnError
}

func (e swEnv) putGate(t *testing.T, g swGate) {
	t.Helper()

	obj := gate.Gate{
		Scope: g.scope, ID: gate.ID(g.id), Version: 0, DecisionPoint: gate.PointStateWrite, Condition: g.condition,
		Unless: "", Outcome: g.outcome, Message: g.message, Mode: g.mode, OnError: g.onError, Final: false,
		CheckedWith: gate.CheckedWith{
			Engine: "test/1", Schema: gate.SchemaVersion{Subject: string(gate.PointStateWrite), Major: 1, Minor: 0},
		},
		State: "", Reads: nil,
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
		Point: gate.PointStateWrite, From: time.Unix(0, 0), To: time.Now().Add(time.Hour), Limit: gate.MaxSimulateRecords,
	})
	if err != nil {
		t.Fatalf("query records: %v", err)
	}

	return rs
}

func swWrite(t *testing.T, subject, unit string, op gate.OperationKind) gate.StateWrite {
	t.Helper()

	return gate.StateWrite{
		Principal: principal(subject, false), Unit: swUnit(t, unit), Operation: op,
		Action: identity.ActionUnitWrite, Source: gate.SourceCLI,
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

func TestGuardStateWriteGates(t *testing.T) {
	t.Parallel()

	units := swUnits{
		collections: map[string]collection.Name{"opentofu:acme/web": "prod"},
		labels:      map[string][]string{"opentofu:acme/web": {"team=core"}},
	}

	cases := []struct {
		name    string
		gates   func(t *testing.T, e swEnv, unit string) []swGate
		subject string
		op      gate.OperationKind
		want    error
		result  gate.Result
		message string
		warns   int
		reqs    int
	}{
		{
			name:    "no gate allows",
			gates:   func(*testing.T, swEnv, string) []swGate { return nil },
			subject: "alice", op: gate.OperationLock, want: nil, result: gate.ResultAllow,
		},
		{
			name: "an enforced deny that matches refuses and names the facts",
			gates: func(t *testing.T, _ swEnv, unit string) []swGate {
				return []swGate{{
					scope: swUnitScope(t, unit), id: "no-lock", condition: `context.operation.kind == "lock"`,
					outcome: gate.OutcomeDeny, message: "Locks of {{unit.collection}} stop: {{unit.labels}}",
					mode: gate.ModeEnforce, onError: gate.OnErrorFail,
				}}
			},
			subject: "alice", op: gate.OperationLock, want: gate.ErrDenied, result: gate.ResultDeny,
			message: "Locks of prod stop: team=core",
		},
		{
			name: "an enforced deny that does not match allows",
			gates: func(t *testing.T, _ swEnv, unit string) []swGate {
				return []swGate{{
					scope: swUnitScope(t, unit), id: "no-unlock", condition: `context.operation.kind == "unlock"`,
					outcome: gate.OutcomeDeny, message: "no", mode: gate.ModeEnforce, onError: gate.OnErrorFail,
				}}
			},
			subject: "alice", op: gate.OperationLock, want: nil, result: gate.ResultAllow,
		},
		{
			name: "an evaluate-mode deny that matches allows and is in the record",
			gates: func(t *testing.T, _ swEnv, unit string) []swGate {
				return []swGate{{
					scope: swUnitScope(t, unit), id: "watch", condition: "",
					outcome: gate.OutcomeDeny, message: "would stop", mode: gate.ModeEvaluate, onError: gate.OnErrorFail,
				}}
			},
			subject: "alice", op: gate.OperationLock, want: nil, result: gate.ResultAllow,
		},
		{
			name: "a warn is in the decision and does not refuse",
			gates: func(t *testing.T, _ swEnv, unit string) []swGate {
				return []swGate{{
					scope: swUnitScope(t, unit), id: "careful", condition: "",
					outcome: gate.OutcomeWarn, message: "take care", mode: gate.ModeEnforce, onError: gate.OnErrorFail,
				}}
			},
			subject: "alice", op: gate.OperationLock, want: nil, result: gate.ResultWarn, warns: 1,
		},
		{
			name: "a gate of the namespace holds for the unit",
			gates: func(*testing.T, swEnv, string) []swGate {
				return []swGate{{
					scope: gate.NamespaceScope("acme"), id: "ns-freeze", condition: `context.principal.kind == "service"`,
					outcome: gate.OutcomeDeny, message: "services wait", mode: gate.ModeEnforce, onError: gate.OnErrorFail,
				}}
			},
			subject: "ci", op: gate.OperationLock, want: gate.ErrDenied, result: gate.ResultDeny, message: "services wait",
		},
		{
			name: "a gate of the namespace skips a person it does not match",
			gates: func(*testing.T, swEnv, string) []swGate {
				return []swGate{{
					scope: gate.NamespaceScope("acme"), id: "ns-freeze", condition: `context.principal.kind == "service"`,
					outcome: gate.OutcomeDeny, message: "services wait", mode: gate.ModeEnforce, onError: gate.OnErrorFail,
				}}
			},
			subject: "alice", op: gate.OperationLock, want: nil, result: gate.ResultAllow,
		},
		{
			name: "a gate of another unit does not hold",
			gates: func(t *testing.T, _ swEnv, _ string) []swGate {
				return []swGate{{
					scope: swUnitScope(t, "other"), id: "other-unit", condition: "",
					outcome: gate.OutcomeDeny, message: "no", mode: gate.ModeEnforce, onError: gate.OnErrorFail,
				}}
			},
			subject: "alice", op: gate.OperationLock, want: nil, result: gate.ResultAllow,
		},
		{
			name: "an approval outcome refuses a CLI and names the change order",
			gates: func(t *testing.T, e swEnv, unit string) []swGate {
				rule := gate.ApprovalRule{
					Scope: gate.NamespaceScope("acme"), ID: "two-eyes", Version: 0,
					Approvers: gate.Approvers{Groups: []string{"sre"}, Users: nil, UnitOwners: false}, Count: 2,
					AuthorMayApprove: false, FreshAuth: gate.NoFreshAuth(), Timeout: 0,
				}
				if _, err := e.store.ApprovalRules().Put(t.Context(), rule, 0, e.fence, swEntry()); err != nil {
					t.Fatal(err)
				}

				return []swGate{{
					scope: swUnitScope(t, unit), id: "needs-two", condition: "", outcome: gate.ApprovalOutcome("two-eyes"),
					message: "Prod writes need review.", mode: gate.ModeEnforce, onError: gate.OnErrorFail,
				}}
			},
			subject: "alice", op: gate.OperationLock, want: gate.ErrDenied, result: gate.ResultAwaitingApproval,
			message: "open a change order for opentofu:acme/web", reqs: 1,
		},
		{
			name: "a gate in error fails closed",
			gates: func(t *testing.T, _ swEnv, unit string) []swGate {
				return []swGate{{
					scope: swUnitScope(t, unit), id: "broken", condition: `context.unit.nothing == "x"`,
					outcome: gate.OutcomeDeny, message: "x", mode: gate.ModeEnforce, onError: gate.OnErrorFail,
				}}
			},
			subject: "alice", op: gate.OperationLock, want: gate.ErrRuleFailed, result: gate.ResultError,
		},
		{
			name: "a gate in error with onError skip counts as no match",
			gates: func(t *testing.T, _ swEnv, unit string) []swGate {
				return []swGate{{
					scope: swUnitScope(t, unit), id: "broken-skip", condition: `context.unit.nothing == "x"`,
					outcome: gate.OutcomeDeny, message: "x", mode: gate.ModeEnforce, onError: gate.OnErrorSkip,
				}}
			},
			subject: "alice", op: gate.OperationLock, want: nil, result: gate.ResultAllow,
		},
		{
			name: "a gate in error in evaluate mode stops nothing",
			gates: func(t *testing.T, _ swEnv, unit string) []swGate {
				return []swGate{{
					scope: swUnitScope(t, unit), id: "broken-eval", condition: `context.unit.nothing == "x"`,
					outcome: gate.OutcomeDeny, message: "x", mode: gate.ModeEvaluate, onError: gate.OnErrorFail,
				}}
			},
			subject: "alice", op: gate.OperationLock, want: nil, result: gate.ResultAllow,
		},
		{
			name: "a message that names no fact fails closed",
			gates: func(t *testing.T, _ swEnv, unit string) []swGate {
				return []swGate{{
					scope: swUnitScope(t, unit), id: "bad-template", condition: "", outcome: gate.OutcomeDeny,
					message: "see {{unit.nothing}}", mode: gate.ModeEnforce, onError: gate.OnErrorFail,
				}}
			},
			subject: "alice", op: gate.OperationLock, want: gate.ErrRuleFailed, result: gate.ResultError,
		},
		{
			name: "a deny wins over a warn",
			gates: func(t *testing.T, _ swEnv, unit string) []swGate {
				return []swGate{
					{
						scope: swUnitScope(t, unit), id: "a-warn", condition: "", outcome: gate.OutcomeWarn, message: "w",
						mode: gate.ModeEnforce, onError: gate.OnErrorFail,
					},
					{
						scope: swUnitScope(t, unit), id: "b-deny", condition: "", outcome: gate.OutcomeDeny, message: "d",
						mode: gate.ModeEnforce, onError: gate.OnErrorFail,
					},
				}
			},
			subject: "alice", op: gate.OperationLock, want: gate.ErrDenied, result: gate.ResultDeny, message: "d",
		},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Each case holds its own store and its own unit name.
			name := "web"
			if i%2 == 1 {
				name = "web"
			}

			e := newSWEnv(t, units, nil)
			for _, g := range tc.gates(t, e, name) {
				e.putGate(t, g)
			}

			d, err := e.svc.GuardStateWrite(t.Context(), swWrite(t, tc.subject, name, tc.op))

			if tc.want == nil {
				if err != nil {
					t.Fatalf("GuardStateWrite() = %v, want nil", err)
				}

				if d.Result != tc.result || len(d.Warnings) != tc.warns {
					t.Fatalf("decision = %+v, want result %s and %d warnings", d, tc.result, tc.warns)
				}
			} else {
				refusal, ok := errors.AsType[*gate.RefusalError](err)
				if !ok || !errors.Is(err, tc.want) {
					t.Fatalf("GuardStateWrite() = %v, want a refusal that wraps %v", err, tc.want)
				}

				if !strings.Contains(strings.Join(refusal.Messages, " "), tc.message) {
					t.Fatalf("messages = %q, want %q in them", refusal.Messages, tc.message)
				}

				d.Record = refusal.Record
			}

			recs := e.records(t)
			if len(recs) != 1 {
				t.Fatalf("records = %d, want one record for one evaluation", len(recs))
			}

			if recs[0].ID != d.Record || recs[0].Result != tc.result || len(recs[0].Requirements) != tc.reqs {
				t.Fatalf("record = %s %s with %d requirements, want %s %s with %d",
					recs[0].ID, recs[0].Result, len(recs[0].Requirements), d.Record, tc.result, tc.reqs)
			}

			if recs[0].Subject != tc.subject || recs[0].Schema.String() != "state.write/1.0" {
				t.Fatalf("record subject %q schema %s", recs[0].Subject, recs[0].Schema)
			}

			if len(recs[0].Units) != 1 || recs[0].Units[0].Facts.Operation.Kind != tc.op || recs[0].Units[0].Digest == "" {
				t.Fatalf("record units = %+v", recs[0].Units)
			}
		})
	}
}

func TestEvaluateModeGateIsInTheRecord(t *testing.T) {
	t.Parallel()

	e := newSWEnv(t, swUnits{collections: nil, labels: nil}, nil)
	e.putGate(t, swGate{
		scope: swUnitScope(t, "web"), id: "watch", condition: "", outcome: gate.OutcomeDeny, message: "would stop",
		mode: gate.ModeEvaluate, onError: gate.OnErrorFail,
	})

	if _, err := e.svc.GuardStateWrite(t.Context(), swWrite(t, "alice", "web", gate.OperationLock)); err != nil {
		t.Fatalf("GuardStateWrite() = %v, want nil: the gate is in evaluate mode", err)
	}

	gates := e.records(t)[0].Units[0].Gates
	if len(gates) != 1 || !gates[0].Matched || gates[0].Mode != gate.ModeEvaluate || gates[0].Message != "would stop" {
		t.Fatalf("record gates = %+v, want the matched gate in evaluate mode", gates)
	}
}

func TestEngineErrorFailsClosed(t *testing.T) {
	t.Parallel()

	e := newSWEnv(t, swUnits{collections: nil, labels: nil}, errors.New("engine down"))
	e.putGate(t, swGate{
		scope: swUnitScope(t, "web"), id: "any", condition: "", outcome: gate.OutcomeDeny, message: "x",
		mode: gate.ModeEnforce, onError: gate.OnErrorSkip,
	})

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

	e := newSWEnv(t, swUnits{collections: nil, labels: nil}, errors.New("engine down"))
	e.putGate(t, swGate{
		scope: swUnitScope(t, "web"), id: "watch", condition: "", outcome: gate.OutcomeDeny, message: "x",
		mode: gate.ModeEvaluate, onError: gate.OnErrorFail,
	})

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

func TestBreakGlassChangeFailsClosed(t *testing.T) {
	t.Parallel()

	e := newSWEnv(t, swUnits{collections: nil, labels: nil}, nil)

	_, err := e.svc.Evaluate(t.Context(), gate.Change{
		Point: gate.PointStateWrite, Namespace: "acme", Ref: gate.ChangeRef{Kind: gate.ChangeStateWrite, ID: ""},
		Subject: gate.Subject{
			ID: "alice", Groups: nil, Kind: gate.PrincipalPerson, Credential: gate.CredentialSession,
			AuthAt: time.Time{}, BreakGlassAccount: false, Job: nil,
		},
		Source: gate.SourceCLI, Author: "alice", Operation: gate.OperationLock,
		Units:   []identity.Object{swUnit(t, "web")},
		Holders: nil, PlanLock: false, BreakGlass: "audit-1", Fence: e.fence,
	})
	if !errors.Is(err, gate.ErrUnavailable) {
		t.Fatalf("Evaluate(break-glass) = %v, want ErrUnavailable: no break-glass is implemented", err)
	}
}

func TestEvaluateRefusesAFenceOfAnotherNamespace(t *testing.T) {
	t.Parallel()

	e := newSWEnv(t, swUnits{collections: nil, labels: nil}, nil)

	_, err := e.svc.Evaluate(t.Context(), gate.Change{
		Point: gate.PointStateWrite, Namespace: "acme", Ref: gate.ChangeRef{Kind: gate.ChangeStateWrite, ID: ""},
		Subject: gate.Subject{
			ID: "alice", Groups: nil, Kind: gate.PrincipalPerson, Credential: gate.CredentialSession,
			AuthAt: time.Time{}, BreakGlassAccount: false, Job: nil,
		},
		Source: gate.SourceCLI, Author: "alice", Operation: gate.OperationLock,
		Units:   []identity.Object{swUnit(t, "web")},
		Holders: nil, PlanLock: false, BreakGlass: "", Fence: namespace.Fence{Namespace: "globex", FailoverVersion: 1},
	})
	if !errors.Is(err, gate.ErrInvalid) {
		t.Fatalf("Evaluate(fence of globex) = %v, want ErrInvalid", err)
	}

	if recs := e.records(t); len(recs) != 0 {
		t.Fatalf("records = %d, want none", len(recs))
	}
}

func TestAccessLimits(t *testing.T) {
	t.Parallel()

	units := swUnits{
		collections: map[string]collection.Name{"opentofu:acme/web": "prod"},
		labels:      nil,
	}

	forbidServices := `forbid (principal, action == OpenDustpan::Action::"unit:write", resource) ` +
		`when { context.principal.kind == "service" };`

	cases := []struct {
		name    string
		scope   func(t *testing.T) gate.Scope
		policy  string
		subject string
		unit    string
		credit  bool
		want    error
	}{
		{
			name: "a forbid of the unit refuses a service", scope: func(t *testing.T) gate.Scope { return swUnitScope(t, "web") },
			policy: forbidServices, subject: "ci", unit: "web", want: gate.ErrLimited,
		},
		{
			name: "a forbid of the unit passes a person", scope: func(t *testing.T) gate.Scope { return swUnitScope(t, "web") },
			policy: forbidServices, subject: "alice", unit: "web", want: nil,
		},
		{
			name: "a forbid of the namespace holds for the unit", scope: func(*testing.T) gate.Scope { return gate.NamespaceScope("acme") },
			policy: forbidServices, subject: "ci", unit: "web", want: gate.ErrLimited,
		},
		{
			name:   "a forbid of the collection holds for a unit in it",
			scope:  func(*testing.T) gate.Scope { return gate.CollectionScope("acme", "prod") },
			policy: forbidServices, subject: "ci", unit: "web", want: gate.ErrLimited,
		},
		{
			name:   "a forbid of the collection does not hold for a unit outside it",
			scope:  func(*testing.T) gate.Scope { return gate.CollectionScope("acme", "prod") },
			policy: forbidServices, subject: "ci", unit: "db", want: nil,
		},
		{
			name: "a forbid of another unit does not hold", scope: func(t *testing.T) gate.Scope { return swUnitScope(t, "db") },
			policy: forbidServices, subject: "ci", unit: "web", want: nil,
		},
		{
			name: "a forbid holds for a job token", scope: func(t *testing.T) gate.Scope { return swUnitScope(t, "web") },
			policy: forbidServices, subject: "runner", unit: "web", want: gate.ErrLimited,
		},
		{
			name: "a break-glass account is outside the limits", scope: func(t *testing.T) gate.Scope { return swUnitScope(t, "web") },
			policy: `forbid (principal, action, resource);`, subject: "glass", unit: "web", want: nil,
		},
		{
			name: "an unknown authentication age is not recent", scope: func(t *testing.T) gate.Scope { return swUnitScope(t, "web") },
			policy:  `forbid (principal, action, resource) unless { context.authAgeSeconds < 900 };`,
			subject: "stale", unit: "web", want: gate.ErrLimited,
		},
		{
			name: "a recent authentication passes", scope: func(t *testing.T) gate.Scope { return swUnitScope(t, "web") },
			policy:  `forbid (principal, action, resource) unless { context.authAgeSeconds < 900 };`,
			subject: "alice", unit: "web", want: nil,
		},
		{
			name: "a limit that gives an error fails closed", scope: func(t *testing.T) gate.Scope { return swUnitScope(t, "web") },
			policy:  `forbid (principal, action, resource) when { context.unit.nothing == "x" };`,
			subject: "alice", unit: "web", want: gate.ErrRuleFailed,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newSWEnv(t, units, nil)
			e.putLimit(t, tc.scope(t), "limit-1", tc.policy)

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
		})
	}
}

func TestLimitRefusalsShareOneRecord(t *testing.T) {
	t.Parallel()

	e := newSWEnv(t, swUnits{collections: nil, labels: nil}, nil)
	e.putLimit(t, swUnitScope(t, "web"), "limit-1",
		`forbid (principal, action, resource) when { context.principal.kind == "service" };`)

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
			t.Fatalf("refusal %d has record %s, want %s: refusals in one window share a record", i, refusal.Record, first)
		}
	}

	rec, err := e.store.Records().Get(t.Context(), "acme", first)
	if err != nil || rec.Count != 3 {
		t.Fatalf("record = %+v, %v, want Count 3", rec, err)
	}
}

func TestJobTokenSkipsGatesButNotLimits(t *testing.T) {
	t.Parallel()

	e := newSWEnv(t, swUnits{collections: nil, labels: nil}, nil)
	e.putGate(t, swGate{
		scope: swUnitScope(t, "web"), id: "deny-all", condition: "", outcome: gate.OutcomeDeny, message: "x",
		mode: gate.ModeEnforce, onError: gate.OnErrorFail,
	})

	d, err := e.svc.GuardStateWrite(t.Context(), swWrite(t, "runner", "web", gate.OperationLock))
	if err != nil || d.Result != gate.ResultAllow || d.Record != "" {
		t.Fatalf("GuardStateWrite(job token) = %+v, %v, want allow with no record: the job model decided the change", d, err)
	}

	if recs := e.records(t); len(recs) != 0 {
		t.Fatalf("records = %d, want none for a job token", len(recs))
	}
}

func TestMessageTemplateNeverRendersEmpty(t *testing.T) {
	t.Parallel()

	e := newSWEnv(t, swUnits{collections: nil, labels: nil}, nil)
	e.putGate(t, swGate{
		scope: swUnitScope(t, "web"), id: "plain", condition: "", outcome: gate.OutcomeDeny,
		message: "Stopped by line one.\nline two {{principal.kind}}", mode: gate.ModeEnforce, onError: gate.OnErrorFail,
	})

	_, err := e.svc.GuardStateWrite(t.Context(), swWrite(t, "alice", "web", gate.OperationLock))

	refusal, ok := errors.AsType[*gate.RefusalError](err)
	if !ok || len(refusal.Messages) != 1 {
		t.Fatalf("GuardStateWrite() = %v, want one refusal message", err)
	}

	if strings.ContainsAny(refusal.Messages[0], "\r\n") || !strings.HasSuffix(refusal.Messages[0], "line two person") {
		t.Fatalf("message = %q, want one line that ends with the rendered fact", refusal.Messages[0])
	}
}
