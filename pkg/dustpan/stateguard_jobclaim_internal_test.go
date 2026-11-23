package dustpan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// claimGuardJobStore is a job store that knows one job. Only Get is answered.
type claimGuardJobStore struct {
	job.Store

	j job.Job
}

// Get implements job.Store for the one job.
func (s *claimGuardJobStore) Get(_ context.Context, id job.ID) (job.Job, error) {
	if id != s.j.ID {
		return job.Job{}, job.ErrNotFound
	}

	return s.j, nil
}

// newClaimGuard returns the state.write gate service that the server builds,
// over a real store, with the job store jobs. A nil jobs builds a server with
// no job store.
func newClaimGuard(t *testing.T, jobs job.Store) *gateService {
	t.Helper()

	cfg, nss := newClaimGuardSettings(t, jobs)

	gates, err := newStateWriteGates(cfg, nss)
	if err != nil || gates.svc == nil {
		t.Fatalf("newStateWriteGates() = %v, %v", gates.svc, err)
	}

	return gates.svc
}

// newClaimGuardWithSubjects returns the state.write gate service with the
// parts that newStateWriteGates gives it, over a real store, but with the
// subject source subjects. A test uses it to put a job claim source that the
// server cannot build in front of the gates.
func newClaimGuardWithSubjects(t *testing.T, subjects gate.SubjectSource) *gateService {
	t.Helper()

	cfg, nss := newClaimGuardSettings(t, nil)

	store, ok := cfg.namespaceStore.(gate.Store)
	if !ok {
		t.Fatal("the store keeps no gates")
	}

	kindReader, err := newGateKindReader(cfg)
	if err != nil {
		t.Fatalf("newGateKindReader: %v", err)
	}

	svc, err := gate.NewService(gate.ServiceConfig{
		Store: store, Engine: newGateEngine(), Producers: nil,
		Units: newInventoryFacts(cfg), Subjects: subjects,
		Fences:     namespaceFences{namespaces: nss},
		KindReader: kindReader, IsDev: principal.IsDevSubject, Now: cfg.now, Log: slog.Default(),
		BreakGlass: nil,
	})
	if err != nil {
		t.Fatalf("gate.NewService: %v", err)
	}

	return svc
}

// newClaimGuardSettings returns the settings and the namespace service of a
// server over a real store, with the namespace acme, the workspace web and
// the job store jobs.
func newClaimGuardSettings(t *testing.T, jobs job.Store) (*settings, *namespace.Service) {
	t.Helper()

	ctx := t.Context()

	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "dustpan.db"))
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

	ns, err := nss.Create(ctx, "acme")
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	// A write that the job model allows meets the gates, and the gates read
	// the facts of a unit that exists.
	createClaimGuardWorkspace(t, st, ns.Fence(), "web")

	cfg := new(settings)
	cfg.namespaceStore, cfg.inventoryReader, cfg.now = st, st, time.Now
	cfg.installationKind = installation.KindProduction
	cfg.jobStore = jobs

	return cfg, nss
}

// createClaimGuardWorkspace creates the workspace name in the namespace acme.
func createClaimGuardWorkspace(t *testing.T, st *sqlite.Store, fence namespace.Fence, name workspace.Name) {
	t.Helper()

	w := workspace.Workspace{
		NamespaceID:  "",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
		Lock:         workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease:  workspace.NoRelease(),
		ID:           workspace.ID("ws-claim-" + string(name)),
		Ref:          workspace.Ref{Namespace: "acme", Name: name},
		Overrides:    namespace.Overrides{Forced: false, Unlocked: false},
		Revision:     1,
		Origin:       identity.StateOrigin{Job: "", Commit: ""},
		InCollection: false,
	}

	if err := st.CreateWorkspace(t.Context(), w, fence); err != nil {
		t.Fatalf("create workspace %s: %v", name, err)
	}
}

// TestStateWriteGatesNoJobStoreAudited proves that the real subject source of
// a server with no job store refuses a job-token write, and that the refusal
// gets its audit entry through the admission of the adapters.
func TestStateWriteGatesNoJobStoreAudited(t *testing.T) {
	t.Parallel()

	web, err := identity.UnitObject("opentofu", "acme", "web")
	if err != nil {
		t.Fatal(err)
	}

	admitter, err := stategate.New(newClaimGuard(t, nil), nil)
	if err != nil {
		t.Fatalf("stategate.New: %v", err)
	}

	p := gatePrincipal("job-token-1", "", identity.CredentialJobToken)
	p.JobID = "job-1"

	var reasons []string

	out := admitter.Admit(t.Context(), stategate.Write{
		Principal: p, Unit: web, Operation: gate.OperationLock, Action: identity.ActionUnitWrite,
		Source: gate.SourceCLI, State: nil, StateErr: nil,
	}, func(_ context.Context, reason string) { reasons = append(reasons, reason) })

	if out.Allowed || out.Refusal == nil || out.Code != errcode.GateDenied ||
		!errors.Is(out.Refusal, identity.ErrDenied) {
		t.Fatalf("Admit() = %+v, want a refusal with %s", out, errcode.GateDenied)
	}

	if len(reasons) != 1 || !strings.Contains(reasons[0], errcode.GateDenied) {
		t.Fatalf("audit reasons = %q, want one entry that names %s", reasons, errcode.GateDenied)
	}
}

// TestStateWriteGatesJobClaimRefusalsAudited proves that each refusal of the
// job claim reaches the admission of the adapters as a denial with one audit
// entry. Each job here is an apply-phase job of the unit web that the gates
// allow, so each case is allowed, or fails with another code, when its guard
// is gone.
func TestStateWriteGatesJobClaimRefusalsAudited(t *testing.T) {
	t.Parallel()

	web, err := identity.UnitObject("opentofu", "acme", "web")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		tokenID string
		jobID   job.ID
		state   job.State
		current int
		want    string
	}{
		{
			name: "empty job ID", tokenID: "", jobID: "", state: job.StateRunning, current: 1,
			want: "names no job",
		},
		{
			name: "blank job ID", tokenID: "  ", jobID: "  ", state: job.StateRunning, current: 1,
			want: "names no job",
		},
		{
			name: "unknown job", tokenID: "job-gone", jobID: "job-1", state: job.StateRunning, current: 1,
			want: "does not exist",
		},
		{
			name: "canceled job", tokenID: "job-1", jobID: "job-1", state: job.StateCanceled, current: 1,
			want: "takes no state write",
		},
		{
			name: "succeeded job", tokenID: "job-1", jobID: "job-1", state: job.StateSucceeded, current: 1,
			want: "takes no state write",
		},
		{
			name: "phase index past the last phase", tokenID: "job-1", jobID: "job-1", state: job.StateRunning,
			current: 2, want: "has no phase at index 2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			j := testPlanJob(tt.jobID, tt.current)
			j.State = tt.state
			j.Spec.Unit = web

			admitter, err := stategate.New(newClaimGuard(t, &claimGuardJobStore{Store: nil, j: j}), nil)
			if err != nil {
				t.Fatalf("stategate.New: %v", err)
			}

			p := gatePrincipal("job-token-1", "", identity.CredentialJobToken)
			p.JobID = tt.tokenID

			var reasons []string

			out := admitter.Admit(t.Context(), stategate.Write{
				Principal: p, Unit: web, Operation: gate.OperationLock, Action: identity.ActionUnitWrite,
				Source: gate.SourceCLI, State: nil, StateErr: nil,
			}, func(_ context.Context, reason string) { reasons = append(reasons, reason) })

			if out.Allowed || out.Refusal == nil || out.Code != errcode.GateDenied ||
				!errors.Is(out.Refusal, identity.ErrDenied) || !strings.Contains(out.Refusal.Error(), tt.want) {
				t.Fatalf("Admit() = %+v, want a refusal with %s that says %q", out, errcode.GateDenied, tt.want)
			}

			if len(reasons) != 1 || !strings.Contains(reasons[0], errcode.GateDenied) {
				t.Fatalf("audit reasons = %q, want one entry that names %s", reasons, errcode.GateDenied)
			}
		})
	}
}

// claimGuardFailingJobStore is a job store whose Get fails with an error that
// is not a denial. Only Get is answered.
type claimGuardFailingJobStore struct {
	job.Store
}

// Get implements job.Store with the error errDiskGone.
func (claimGuardFailingJobStore) Get(_ context.Context, _ job.ID) (job.Job, error) {
	return job.Job{}, errDiskGone
}

// deniedInnerSubjects gives the subject of a job token together with a
// denial, so that only the check of the inner error refuses the principal.
type deniedInnerSubjects struct{}

// Subject implements gate.SubjectSource.
func (deniedInnerSubjects) Subject(_ context.Context, p identity.Principal) (gate.Subject, error) {
	var s gate.Subject

	s.ID = p.Subject
	s.Credential = gate.CredentialJobToken

	return s, fmt.Errorf("%w: the inner source refuses the principal", identity.ErrDenied)
}

// TestStateWriteGatesJobClaimSourceRefusalsAudited proves that the refusals
// of the job claim source that the server build cannot reach (no inner
// source, an inner error) and a job store error reach the admission of the
// adapters with one audit entry. The job is an apply-phase job of the unit
// web that the gates allow. With the guard gone, the case with no inner
// source panics, the case with an inner error is allowed, and the store
// error becomes a phase denial with the code gate.denied.
func TestStateWriteGatesJobClaimSourceRefusalsAudited(t *testing.T) {
	t.Parallel()

	web, err := identity.UnitObject("opentofu", "acme", "web")
	if err != nil {
		t.Fatal(err)
	}

	j := testPlanJob("job-1", 1)
	j.Spec.Unit = web

	tests := []struct {
		name     string
		subjects gate.SubjectSource
		code     string
		denied   bool
		want     string
	}{
		{
			name:     "no inner source",
			subjects: newJobClaimSubjects(&claimGuardJobStore{Store: nil, j: j}, nil),
			code:     errcode.GateDenied, denied: true, want: "has no inner source",
		},
		{
			name:     "inner source error",
			subjects: newJobClaimSubjects(&claimGuardJobStore{Store: nil, j: j}, deniedInnerSubjects{}),
			code:     errcode.GateDenied, denied: true, want: "the inner source refuses the principal",
		},
		{
			// A store error is not a decision: the write is refused as a guard
			// that could not decide, not as a denial.
			name: "job store error",
			subjects: newJobClaimSubjects(claimGuardFailingJobStore{Store: nil},
				fakeInnerSubjects{}),
			code: errcode.GateUnavailable, denied: false, want: errDiskGone.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			admitter, err := stategate.New(newClaimGuardWithSubjects(t, tt.subjects), nil)
			if err != nil {
				t.Fatalf("stategate.New: %v", err)
			}

			p := gatePrincipal("job-token-1", "", identity.CredentialJobToken)
			p.JobID = "job-1"

			var reasons []string

			out := admitter.Admit(t.Context(), stategate.Write{
				Principal: p, Unit: web, Operation: gate.OperationLock, Action: identity.ActionUnitWrite,
				Source: gate.SourceCLI, State: nil, StateErr: nil,
			}, func(_ context.Context, reason string) { reasons = append(reasons, reason) })

			if out.Allowed || out.Refusal == nil || out.Code != tt.code ||
				errors.Is(out.Refusal, identity.ErrDenied) != tt.denied ||
				!strings.Contains(out.Refusal.Error(), tt.want) {
				t.Fatalf("Admit() = %+v, want a refusal with %s that says %q", out, tt.code, tt.want)
			}

			if len(reasons) != 1 || !strings.Contains(reasons[0], tt.code) {
				t.Fatalf("audit reasons = %q, want one entry that names %s", reasons, tt.code)
			}
		})
	}
}

// TestStateWriteGatesSeeTheJobClaim proves that the gate service that the
// server builds reads the job of a job token: a job past change.apply writes
// the state of its unit, and a plan-only job writes only its plan lock.
func TestStateWriteGatesSeeTheJobClaim(t *testing.T) {
	t.Parallel()

	web, err := identity.UnitObject("opentofu", "acme", "web")
	if err != nil {
		t.Fatal(err)
	}

	other, err := identity.UnitObject("opentofu", "acme", "other")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		current  int
		unit     identity.Object
		op       gate.OperationKind
		planLock bool
		allowed  bool
	}{
		{
			name: "apply phase writes the state of its unit", current: 1, unit: web,
			op: gate.OperationApply, planLock: false, allowed: true,
		},
		{
			name: "plan phase takes the lock of its plan", current: 0, unit: web,
			op: gate.OperationLock, planLock: true, allowed: true,
		},
		{
			name: "plan phase is refused a state write", current: 0, unit: web,
			op: gate.OperationApply, planLock: false, allowed: false,
		},
		{
			name: "apply phase is refused another unit", current: 1, unit: other,
			op: gate.OperationApply, planLock: false, allowed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			j := testPlanJob("job-1", tt.current)
			j.Spec.Unit = web

			svc := newClaimGuard(t, &claimGuardJobStore{Store: nil, j: j})

			p := gatePrincipal("job-token-1", "", identity.CredentialJobToken)
			p.JobID = "job-1"

			_, err := svc.GuardStateWrite(t.Context(), gate.StateWrite{
				Principal: p, Unit: tt.unit, Operation: tt.op, Action: identity.ActionUnitWrite,
				Source: gate.SourceCLI, PlanLock: tt.planLock, State: nil,
			})

			if tt.allowed && err != nil {
				t.Fatalf("GuardStateWrite() = %v, want the write allowed", err)
			}

			if !tt.allowed && !errors.Is(err, identity.ErrDenied) {
				t.Fatalf("GuardStateWrite() = %v, want identity.ErrDenied", err)
			}
		})
	}
}
