package stategate_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// jobID is the job of the job token of the hostile cases.
const jobID = "job-plan-1"

// jobCase is one write of a job token at state.write. refused is true when the
// gate must refuse the write.
type jobCase struct {
	name    string
	job     gate.JobClaim
	unit    identity.Object
	op      gate.OperationKind
	refused bool
}

// jobClaims gives the Subject of a job token from a table of jobs, as the job
// store does for the server. A token of an unknown job is denied.
type jobClaims struct {
	jobs map[string]gate.JobClaim
}

// Subject implements gate.SubjectSource.
func (j jobClaims) Subject(_ context.Context, p identity.Principal) (gate.Subject, error) {
	if p.JobTokenID() == "" {
		var none gate.Subject

		return none, fmt.Errorf("%w: the principal holds no job token", identity.ErrDenied)
	}

	claim, ok := j.jobs[p.JobTokenID()]
	if !ok {
		var none gate.Subject

		return none, fmt.Errorf("%w: unknown job %q", identity.ErrDenied, p.JobTokenID())
	}

	return gate.Subject{
		ID: p.Subject, Groups: nil, Kind: gate.PrincipalService, Credential: gate.CredentialJobToken,
		AuthAt: time.Time{}, BreakGlassAccount: false, Job: &claim,
	}, nil
}

// idleEngine is a gate engine that evaluates nothing: the store holds no gate,
// so the service asks the engine only for its version.
type idleEngine struct {
	gate.Engine
}

// Version implements gate.Engine.
func (idleEngine) Version() string {
	return "idle/1"
}

// unitStub knows each unit, with no collection and no label.
type unitStub struct{}

// Unit implements gate.UnitSource.
func (unitStub) Unit(context.Context, identity.Object) (gate.UnitInfo, error) {
	var info gate.UnitInfo

	return info, nil
}

// namespaceFences gives the fence of a namespace from the namespace service.
type namespaceFences struct{ nss *namespace.Service }

// Fence implements gate.FenceSource.
func (f namespaceFences) Fence(ctx context.Context, ns namespace.Name) (namespace.Fence, error) {
	n, err := f.nss.Get(ctx, ns)
	if err != nil {
		return namespace.Fence{}, err
	}

	return n.Fence(), nil
}

// kindStub is a production installation.
type kindStub struct{}

// ReadKind implements gate.KindReader.
func (kindStub) ReadKind(context.Context) (string, error) {
	return "production", nil
}

// newJobGate returns the real gate service with the subjects of a job token.
// The store is a real store with the namespace acme, and it holds no gate and
// no limit, so the job model decides each job-token write.
func newJobGate(t *testing.T, subjects gate.SubjectSource) *gate.Service {
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

	svc, err := gate.NewService(gate.ServiceConfig{
		Store:      st,
		Engine:     idleEngine{Engine: nil},
		Producers:  nil,
		Units:      unitStub{},
		Subjects:   subjects,
		Fences:     namespaceFences{nss: nss},
		KindReader: kindStub{},
		IsDev:      func(string) bool { return false },
		BreakGlass: nil,
		Now:        nil,
		Log:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("gate.NewService: %v", err)
	}

	return svc
}

// jobUnit returns the unit name of tool in the namespace acme.
func jobUnit(t *testing.T, tool, name string) identity.Object {
	t.Helper()

	unit, err := identity.UnitObject(tool, "acme", name)
	if err != nil {
		t.Fatalf("unit %s: %v", name, err)
	}

	return unit
}

// jobWrite returns a write by p to unit with the operation op.
func jobWrite(unit identity.Object, op gate.OperationKind, p identity.Principal) stategate.Write {
	return stategate.Write{
		Principal: p, Unit: unit, Operation: op, Action: identity.ActionUnitWrite, Source: gate.SourceCLI,
		State: nil, StateErr: nil,
	}
}

// runJobCases sends each write of the table through the state.write gate and
// the admission. A refused write gives identity.ErrDenied from the gate, and
// one audit entry from the admission. An allowed write gives no error and no
// entry.
func runJobCases(t *testing.T, cases []jobCase) {
	t.Helper()

	for i := range cases {
		tc := &cases[i]

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			claim := tc.job
			guard := newJobGate(t, jobClaims{jobs: map[string]gate.JobClaim{jobID: claim}})
			w := jobWrite(tc.unit, tc.op, principal(identity.CredentialJobToken, jobID))

			_, err := guard.GuardStateWrite(t.Context(), stategate.StateWrite(w))
			if tc.refused {
				checkJobRefused(t, guard, w, err)

				return
			}

			checkJobAllowed(t, guard, w, err)
		})
	}
}

// checkJobRefused fails t unless the gate gave identity.ErrDenied for w, and
// the admission refused w with one audit entry.
func checkJobRefused(t *testing.T, guard stategate.Guard, w stategate.Write, err error) {
	t.Helper()

	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("GuardStateWrite() = %v, want identity.ErrDenied", err)
	}

	rec := &recorder{mu: sync.Mutex{}, reasons: nil}
	out := newAdmitter(t, guard).Admit(t.Context(), w, rec.record)
	checkRefused(t, out, rec, errcode.GateDenied)

	if !errors.Is(out.Refusal, identity.ErrDenied) {
		t.Fatalf("refusal = %v, want it to wrap identity.ErrDenied", out.Refusal)
	}
}

// checkJobAllowed fails t unless the gate allowed w, and the admission allowed
// it with no audit entry.
func checkJobAllowed(t *testing.T, guard stategate.Guard, w stategate.Write, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("GuardStateWrite() = %v, want the write allowed", err)
	}

	rec := &recorder{mu: sync.Mutex{}, reasons: nil}

	out := newAdmitter(t, guard).Admit(t.Context(), w, rec.record)
	if !out.Allowed || len(rec.entries()) != 0 {
		t.Fatalf("outcome = %+v, entries %q, want allowed with no entry", out, rec.entries())
	}
}

// TestPulumiJobTokenStateWrites sends the operations of the Pulumi routes that
// call the state.write guard (import, restore, cancel, delete and rename)
// through the gate and the admission, with the claim of a plan-only job token.
// It calls the guard directly: the route-level cases are in the server tests.
// Each operation is refused. A token of another unit and a route unit other
// than the job's unit are refused, also for the plan lock. The plan lock of its
// own unit and a job past change.apply stay allowed, so that a refusal is not
// a broken fixture.
func TestPulumiJobTokenStateWrites(t *testing.T) {
	t.Parallel()

	web := jobUnit(t, "pulumi", "web")
	api := jobUnit(t, "pulumi", "api")

	plan := gate.JobClaim{Job: jobID, Phase: "plan", PassedApply: false, Units: []identity.Object{web}}
	planOfAPI := gate.JobClaim{Job: jobID, Phase: "plan", PassedApply: false, Units: []identity.Object{api}}
	applied := gate.JobClaim{Job: jobID, Phase: "apply", PassedApply: true, Units: []identity.Object{web}}

	runJobCases(t, []jobCase{
		{name: "import in the plan phase", job: plan, unit: web, op: gate.OperationStateImport, refused: true},
		{name: "restore in the plan phase", job: plan, unit: web, op: gate.OperationRollback, refused: true},
		{name: "cancel in the plan phase", job: plan, unit: web, op: gate.OperationUnlock, refused: true},
		{name: "delete in the plan phase", job: plan, unit: web, op: gate.OperationDelete, refused: true},
		{name: "rename in the plan phase", job: plan, unit: web, op: gate.OperationRename, refused: true},
		{
			name: "token of another unit on this unit", job: planOfAPI, unit: web,
			op: gate.OperationStateImport, refused: true,
		},
		{
			name: "route unit other than the job unit, plan lock", job: plan, unit: api,
			op: gate.OperationLock, refused: true,
		},
		{
			name: "control: plan lock of the job unit", job: plan, unit: web,
			op: gate.OperationLock, refused: false,
		},
		{
			name: "control: import operation after change.apply", job: applied, unit: web,
			op: gate.OperationStateImport, refused: false,
		},
	})
}
