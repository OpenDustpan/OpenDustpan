package gate_test

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/gate/cedargo"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	principalpkg "opendustpan.org/opendustpan/pkg/principal"
)

// TestStateWriteJobClaim checks each rule of a state.write with a job token:
// the job model refuses first with identity.ErrDenied and writes no record,
// and a write that it allows meets the gates and writes a decision record.
func TestStateWriteJobClaim(t *testing.T) {
	t.Parallel()

	// want is "model" for a refusal of the job model, "gate" for a refusal
	// of the deny gate, and "allow" for an allowed write.
	tests := []struct {
		name     string
		subject  string
		unit     string
		op       gate.OperationKind
		planLock bool
		denyGate bool
		want     string
	}{
		{
			name: "out-of-phase: a plan phase writes no state", subject: "runner-plan", unit: "web",
			op: gate.OperationApply, planLock: false, denyGate: false, want: "model",
		},
		{
			name: "cross-unit: a unit outside the job is refused", subject: "runner-apply", unit: "other",
			op: gate.OperationApply, planLock: false, denyGate: false, want: "model",
		},
		{
			name: "cross-unit: the plan lock of a unit outside the job is refused", subject: "runner-plan",
			unit: "other", op: gate.OperationLock, planLock: true, denyGate: false, want: "model",
		},
		{
			name: "plan-lock: a plan phase takes the lock of its plan", subject: "runner-plan", unit: "web",
			op: gate.OperationLock, planLock: true, denyGate: false, want: "allow",
		},
		{
			name: "no-phase: a job token with no job claim is refused", subject: "runner", unit: "web",
			op: gate.OperationLock, planLock: true, denyGate: false, want: "model",
		},
		{
			name: "canceled job: a claim that does not resolve is refused", subject: "runner-canceled",
			unit: "web", op: gate.OperationApply, planLock: false, denyGate: false, want: "model",
		},
		{
			name: "deny gate: an allowed apply write is refused", subject: "runner-apply", unit: "web",
			op: gate.OperationApply, planLock: false, denyGate: true, want: "gate",
		},
		{
			name: "deny gate: the plan lock is refused", subject: "runner-plan", unit: "web",
			op: gate.OperationLock, planLock: true, denyGate: true, want: "gate",
		},
		{
			name: "deny gate: a Pulumi update cancel with the plan lock is refused", subject: "runner-plan",
			unit: "web", op: gate.OperationUnlock, planLock: true, denyGate: true, want: "gate",
		},
		{
			name: "deny gate: the job model refuses first and runs no gate", subject: "runner-plan",
			unit: "other", op: gate.OperationApply, planLock: false, denyGate: true, want: "model",
		},
		{
			name: "no gate match: an allowed apply write is allowed", subject: "runner-apply", unit: "web",
			op: gate.OperationApply, planLock: false, denyGate: false, want: "allow",
		},
		{
			name: "unlocked-write: a plan phase is refused", subject: "runner-plan", unit: "web",
			op: gate.OperationUnlockedWrite, planLock: false, denyGate: false, want: "model",
		},
		{
			name: "unlocked-write: a phase that passed apply meets the deny gate", subject: "runner-apply",
			unit: "web", op: gate.OperationUnlockedWrite, planLock: false, denyGate: true, want: "gate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := newSWEnv(t, swUnits{collections: nil, labels: nil}, nil)
			if tt.denyGate {
				e.putGate(t, newSWGate(atUnit, "deny-jobs", "", gate.OutcomeDeny, "No job writes this unit."))
			}

			w := swWrite(t, tt.subject, tt.unit, tt.op)
			w.PlanLock = tt.planLock

			d, err := e.svc.GuardStateWrite(t.Context(), w)

			switch tt.want {
			case "model":
				checkJobModelRefusal(t, e, err)
			case "gate":
				checkJobGateRefusal(t, e, err, tt.subject, "deny-jobs")
			default:
				checkJobGateAllow(t, e, d, err, tt.subject)
			}
		})
	}
}

// checkJobModelRefusal fails t unless the job model refused the write with
// identity.ErrDenied before any gate, so that no record exists.
func checkJobModelRefusal(t *testing.T, e swEnv, err error) {
	t.Helper()

	if !errors.Is(err, identity.ErrDenied) || errors.Is(err, gate.ErrDenied) {
		t.Fatalf("GuardStateWrite() = %v, want identity.ErrDenied from the job model", err)
	}

	if recs := e.records(t); len(recs) != 0 {
		t.Fatalf("records = %d, want none: the job model refused before the gates", len(recs))
	}
}

// checkJobGateRefusal fails t unless the deny gate id refused the write of
// subject, with the one decision record that names the gate.
func checkJobGateRefusal(t *testing.T, e swEnv, err error, subject string, id gate.ID) {
	t.Helper()

	refusal, ok := errors.AsType[*gate.RefusalError](err)
	if !ok || !errors.Is(err, gate.ErrDenied) || refusal.Record == "" {
		t.Fatalf("GuardStateWrite() = %v, want a deny gate refusal with a record", err)
	}

	recs := e.records(t)
	if len(recs) != 1 || recs[0].ID != refusal.Record || recs[0].Result != gate.ResultDeny ||
		recs[0].Subject != subject {
		t.Fatalf("records = %+v, want the one deny record %s of %s", recs, refusal.Record, subject)
	}

	if !recordNamesMatchedGate(recs[0], id) {
		t.Fatalf("record %+v does not name the matched gate %s", recs[0], id)
	}
}

// checkJobGateAllow fails t unless the gates allowed the write of subject with
// the one decision record d names.
func checkJobGateAllow(t *testing.T, e swEnv, d gate.Decision, err error, subject string) {
	t.Helper()

	if err != nil || d.Result != gate.ResultAllow || d.Record == "" {
		t.Fatalf("GuardStateWrite() = %+v, %v, want allow with a record", d, err)
	}

	if recs := e.records(t); len(recs) != 1 || recs[0].ID != d.Record || recs[0].Subject != subject {
		t.Fatalf("records = %+v, want the one allow record %s of %s", recs, d.Record, subject)
	}
}

// recordNamesMatchedGate reports whether a unit result of rec holds the gate
// id as matched.
func recordNamesMatchedGate(rec gate.Record, id gate.ID) bool {
	return slices.ContainsFunc(rec.Units, func(u gate.UnitResult) bool {
		return slices.ContainsFunc(u.Gates, func(p gate.PolicyResult) bool { return p.Policy.ID == id && p.Matched })
	})
}

// TestStateWriteRefusalCarriesTheJobClaim checks that GuardStateWrite puts the
// job claim of the subject on each refusal, and that a refusal with no claim
// has no job: a person, a job token with no claim, and a refusal of the
// subject source.
func TestStateWriteRefusalCarriesTheJobClaim(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		subject  string
		unit     string
		denyGate bool
		wantJob  string
	}{
		{name: "job model refusal", subject: "runner-plan", unit: "web", denyGate: false, wantJob: "42"},
		{name: "cross-unit refusal", subject: "runner-apply", unit: "other", denyGate: false, wantJob: "42"},
		{name: "deny gate refusal", subject: "runner-apply", unit: "web", denyGate: true, wantJob: "42"},
		{name: "person refused by a deny gate", subject: "bo", unit: "web", denyGate: true, wantJob: ""},
		{name: "job token with no claim", subject: "runner", unit: "web", denyGate: false, wantJob: ""},
		{name: "refusal of the subject source", subject: "runner-canceled", unit: "web", denyGate: false, wantJob: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := newSWEnv(t, swUnits{collections: nil, labels: nil}, nil)
			if tt.denyGate {
				e.putGate(t, newSWGate(atUnit, "deny-jobs", "", gate.OutcomeDeny, "No job writes this unit."))
			}

			_, err := e.svc.GuardStateWrite(t.Context(), swWrite(t, tt.subject, tt.unit, gate.OperationApply))
			checkRefusalJob(t, err, tt.wantJob)
		})
	}
}

// checkRefusalJob fails t unless err is a refusal whose job claim names the
// job wantJob in the phase apply, or, for an empty wantJob, a refusal with no
// claim.
func checkRefusalJob(t *testing.T, err error, wantJob string) {
	t.Helper()

	if err == nil {
		t.Fatal("GuardStateWrite() = nil, want a refusal")
	}

	refusal, ok := errors.AsType[*gate.RefusalError](err)
	if wantJob == "" {
		if ok && refusal.Job != nil {
			t.Fatalf("refusal job = %+v, want none", refusal.Job)
		}

		return
	}

	if !ok || refusal.Job == nil {
		t.Fatalf("GuardStateWrite() = %v, want a refusal with the job claim", err)
	}

	if refusal.Job.Job != wantJob || refusal.Job.Phase != "apply" {
		t.Fatalf("refusal job = %+v, want job %s in the phase apply", refusal.Job, wantJob)
	}
}

// TestStateWriteClaimlessDenialIsNotWrapped checks that GuardStateWrite
// returns the job-model denial of a job token with no claim unchanged: the
// error is identity.ErrDenied and is not a *RefusalError, so a refusal with
// no claim never becomes a job-model refusal.
func TestStateWriteClaimlessDenialIsNotWrapped(t *testing.T) {
	t.Parallel()

	e := newSWEnv(t, swUnits{collections: nil, labels: nil}, nil)

	w := swWrite(t, "runner", "web", gate.OperationLock)
	w.PlanLock = true

	_, err := e.svc.GuardStateWrite(t.Context(), w)
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("GuardStateWrite() = %v, want identity.ErrDenied from the job model", err)
	}

	if refusal, ok := errors.AsType[*gate.RefusalError](err); ok {
		t.Fatalf("GuardStateWrite() = %+v, want the job-model denial unchanged, not a *RefusalError", refusal)
	}

	if recs := e.records(t); len(recs) != 0 {
		t.Fatalf("records = %d, want none: the job model refused before the gates", len(recs))
	}
}

// nilRefusalFences is a fence source whose error is a typed-nil
// *RefusalError.
type nilRefusalFences struct{}

func (nilRefusalFences) Fence(context.Context, namespace.Name) (namespace.Fence, error) {
	var refused *gate.RefusalError

	return namespace.Fence{}, refused
}

// TestStateWriteTypedNilRefusal sends a typed-nil *RefusalError through
// GuardStateWrite for a job token with a claim. The call must return the
// error and must not panic, as if no other code kept a typed nil out of the
// error chain.
func TestStateWriteTypedNilRefusal(t *testing.T) {
	t.Parallel()

	e := newSWEnv(t, swUnits{collections: nil, labels: nil}, nil)

	svc, err := gate.NewService(gate.ServiceConfig{
		Store:      e.store,
		Engine:     cedargo.New(cedargo.WithAccessActions("unit:write", "unit:read")),
		Producers:  nil,
		Units:      swUnits{collections: nil, labels: nil},
		Subjects:   swSubjects{},
		Fences:     nilRefusalFences{},
		KindReader: fixedKind("production"),
		IsDev:      principalpkg.IsDevSubject,
		BreakGlass: nil,
		Now:        nil,
		Log:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.GuardStateWrite(t.Context(), swWrite(t, "runner-apply", "web", gate.OperationApply))
	if err == nil {
		t.Fatal("GuardStateWrite() = nil, want the error of the fence source")
	}

	if refused, ok := errors.AsType[*gate.RefusalError](err); !ok || refused != nil {
		t.Fatalf("GuardStateWrite() = %v, want the typed-nil refusal unchanged in the chain", err)
	}
}

// TestStateWriteJobClaimSetsNoBreakGlass checks that a job token cannot pass
// a gate with a break-glass pass: Evaluate refuses the change before any gate.
func TestStateWriteJobClaimSetsNoBreakGlass(t *testing.T) {
	t.Parallel()

	e := newSWEnv(t, swUnits{collections: nil, labels: nil}, nil)
	unit := swUnit(t, "web")

	subject, err := swSubjects{}.Subject(t.Context(), principal("runner-apply", false))
	if err != nil {
		t.Fatal(err)
	}

	d, err := e.svc.Evaluate(t.Context(), gate.Change{
		Point: gate.PointStateWrite, Namespace: unit.Namespace(),
		Ref:     gate.ChangeRef{Kind: gate.ChangeStateWrite, ID: ""},
		Subject: subject, Source: gate.SourceCLI, Author: subject.ID, Operation: gate.OperationApply,
		Units: []identity.Object{unit}, Holders: nil, PlanLock: false, BreakGlass: "audit-1", Fence: e.fence,
		State: nil,
	})
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("Evaluate() = %+v, %v, want identity.ErrDenied for a break-glass of a job token", d, err)
	}

	if recs := e.records(t); len(recs) != 0 {
		t.Fatalf("records = %d, want none", len(recs))
	}
}
