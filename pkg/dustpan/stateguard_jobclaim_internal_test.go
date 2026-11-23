package dustpan

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
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
// over a real store, with a job store that holds j.
func newClaimGuard(t *testing.T, j *job.Job) *gateService {
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

	if _, err = nss.Create(ctx, "acme"); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	cfg := new(settings)
	cfg.namespaceStore, cfg.inventoryReader, cfg.now = st, st, time.Now
	cfg.installationKind = installation.KindProduction
	cfg.jobStore = &claimGuardJobStore{Store: nil, j: *j}

	gates, err := newStateWriteGates(cfg, nss)
	if err != nil || gates.svc == nil {
		t.Fatalf("newStateWriteGates() = %v, %v", gates.svc, err)
	}

	return gates.svc
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

			svc := newClaimGuard(t, &j)

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
