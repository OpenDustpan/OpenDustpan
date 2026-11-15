package dustpan_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// destroyApprovalStore allows each write so that the service guard is tested
// without help from the store.
type destroyApprovalStore struct {
	job.Store

	current job.Job
	writes  int
}

func (s *destroyApprovalStore) Get(context.Context, job.ID) (job.Job, error) {
	return s.current, nil
}

func (s *destroyApprovalStore) Apply(
	_ context.Context, c job.Change, _ namespace.Fence, _ []audit.Entry,
) (job.Job, error) {
	s.writes++
	s.current.Phases[0].Approvals = append(s.current.Phases[0].Approvals, *c.Approval)

	return s.current, nil
}

// Unused ports fail if the approval calls a start or dispatch operation.
type destroyApprovalPorts struct {
	job.UnitResolver
	job.Planner
	job.Decider
	job.GateGuard
}

type destroyApprovalExecutor struct{ job.Executor }

func (destroyApprovalExecutor) Kind() job.ExecutorKind { return job.ExecutorRunner }

type destroyApprovalFence struct{}

type destroyApprovalRig struct {
	service job.Starter
	store   *destroyApprovalStore
}

func (destroyApprovalFence) Fence(_ context.Context, ns namespace.Name) (namespace.Fence, error) {
	return namespace.Fence{Namespace: ns, FailoverVersion: 1}, nil
}

func TestDestroyApprovalNeedsFreshStep(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		credential identity.Credential
		kind       identity.Kind
		agent      string
		auth       job.ConfirmAuth
		age        time.Duration
		want       error
	}{
		{"no proof", identity.CredentialSession, identity.KindPerson, "", "", 0, job.ErrAuthStale},
		{
			"stale proof",
			identity.CredentialSession,
			identity.KindPerson,
			"",
			job.ConfirmAuthMFA,
			time.Hour,
			job.ErrAuthStale,
		},
		{
			"future proof",
			identity.CredentialSession,
			identity.KindPerson,
			"",
			job.ConfirmAuthMFA,
			-time.Second,
			job.ErrAuthStale,
		},
		{
			"access token with proof",
			identity.CredentialAccessToken,
			identity.KindPerson,
			"",
			job.ConfirmAuthMFA,
			0,
			job.ErrAuthStale,
		},
		{
			"job token with proof",
			identity.CredentialJobToken,
			identity.KindPerson,
			"",
			job.ConfirmAuthMFA,
			0,
			job.ErrAuthStale,
		},
		{
			"trust token with proof",
			identity.CredentialTrustToken,
			identity.KindPerson,
			"",
			job.ConfirmAuthMFA,
			0,
			job.ErrAuthStale,
		},
		{"unknown credential", "unknown", identity.KindPerson, "", job.ConfirmAuthMFA, 0, job.ErrAuthStale},
		{"unknown kind", identity.CredentialSession, "", "", job.ConfirmAuthMFA, 0, job.ErrAuthStale},
		{
			"service session",
			identity.CredentialSession,
			identity.KindService,
			"",
			job.ConfirmAuthMFA,
			0,
			job.ErrAuthStale,
		},
		{
			"agent with proof",
			identity.CredentialSession,
			identity.KindPerson,
			"agent",
			job.ConfirmAuthMFA,
			0,
			job.ErrAuthStale,
		},
		{"fresh person", identity.CredentialSession, identity.KindPerson, "", job.ConfirmAuthMFA, time.Minute, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			at := time.Date(2026, time.November, 14, 12, 0, 0, 0, time.UTC)
			r := destroyApprovalService(t, at)
			var a job.Actor
			a.Principal.Subject, a.Principal.Admin = "approver", true
			a.Principal.Kind, a.Principal.Credential = tc.kind, tc.credential
			a.Agent, a.ConfirmAuth, a.AuthAt = tc.agent, tc.auth, at.Add(-tc.age)

			var binding job.PlanBinding
			_, err := r.service.Approve(t.Context(), a, r.store.current.ID, job.VerdictApprove, binding, "")
			checkDestroyApprovalResult(t, r.store, a.AuthAt, tc.want, err)
		})
	}
}

func TestDestroyApprovalServiceRefusesTokenAndStaleProof(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		edit func(*job.Actor)
	}{
		{"access token", func(a *job.Actor) { a.Principal.Credential = identity.CredentialAccessToken }},
		{"agent credential", func(a *job.Actor) { a.Agent = "agent" }},
		{"stale proof", func(a *job.Actor) { a.AuthAt = a.AuthAt.Add(-job.ConfirmFreshness - time.Millisecond) }},
		{"no fresh step", func(a *job.Actor) { a.ConfirmAuth, a.AuthAt = "", time.Time{} }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			at := time.Date(2026, time.November, 14, 12, 0, 0, 0, time.UTC)
			r := destroyApprovalService(t, at)
			var a job.Actor
			a.Principal.Subject, a.Principal.Admin = "approver", true
			a.Principal.Kind, a.Principal.Credential = identity.KindPerson, identity.CredentialSession
			a.ConfirmAuth, a.AuthAt = job.ConfirmAuthMFA, at
			tc.edit(&a)

			// The handler is not called, and the store allows every write.
			// Only the service can refuse these approvals.
			var binding job.PlanBinding
			_, err := r.service.Approve(t.Context(), a, r.store.current.ID, job.VerdictApprove, binding, "")
			if !errors.Is(err, job.ErrAuthStale) {
				t.Errorf("Approve = %v; want ErrAuthStale", err)
			}

			approvals := len(r.store.current.Phases[0].Approvals)
			if r.store.writes != 0 || approvals != 0 {
				t.Errorf("writes = %d, approvals = %d; want no write or approval", r.store.writes, approvals)
			}
		})
	}
}

func TestDestroyApprovalRefusesMalformedSessionProof(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		edit func(*job.Actor)
	}{
		{"no proof time", func(a *job.Actor) { a.AuthAt = time.Time{} }},
		{"session with token ID", func(a *job.Actor) { a.Principal.TokenID = "atk-forged" }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			at := time.Date(2026, time.November, 14, 12, 0, 0, 0, time.UTC)
			r := destroyApprovalService(t, at)
			var a job.Actor
			a.Principal.Subject, a.Principal.Admin = "approver", true
			a.Principal.Kind, a.Principal.Credential = identity.KindPerson, identity.CredentialSession
			a.ConfirmAuth, a.AuthAt = job.ConfirmAuthMFA, at
			tc.edit(&a)
			var binding job.PlanBinding
			_, err := r.service.Approve(t.Context(), a, r.store.current.ID, job.VerdictApprove, binding, "")
			checkDestroyApprovalResult(t, r.store, a.AuthAt, job.ErrAuthStale, err)
		})
	}
}

func checkDestroyApprovalResult(t *testing.T, st *destroyApprovalStore, authAt time.Time, want, err error) {
	t.Helper()

	if want != nil {
		if !errors.Is(err, want) || st.writes != 0 {
			t.Fatalf("Approve = %v, writes = %d; want ErrAuthStale and no write", err, st.writes)
		}

		return
	}

	if err != nil || st.writes != 1 {
		t.Fatalf("Approve = %v, writes = %d; want one write", err, st.writes)
	}

	ap := st.current.Phases[0].Approvals[0]
	if ap.Auth != job.ConfirmAuthMFA || !ap.AuthAt.Equal(authAt) {
		t.Fatal("the approval does not retain its fresh proof")
	}
}

func destroyApprovalService(t *testing.T, at time.Time) destroyApprovalRig {
	t.Helper()

	unit, err := identity.UnitObject("opentofu", "acme", "network")
	if err != nil {
		t.Fatal(err)
	}

	var j job.Job
	j.ID, j.State, j.StartedBy, j.Version = "job-destroy", job.StateAwaitingApproval, "author", 1
	j.Spec.Unit, j.Spec.Operation = unit.WithUnitID("unt-network"), job.OperationDestroy
	var phase job.Phase
	var approval job.Gate
	approval.Type, approval.Approvals = job.GateApproval, 1
	phase.Kind, phase.Gates = job.PhaseApply, []job.Gate{approval}
	j.Phases = []job.Phase{phase}
	st := &destroyApprovalStore{Store: nil, current: j, writes: 0}
	ports := destroyApprovalPorts{UnitResolver: nil, Planner: nil, Decider: nil, GateGuard: nil}
	var cfg job.ServiceConfig
	cfg.Store, cfg.Units, cfg.Planner, cfg.Decider, cfg.Gates = st, ports, ports, ports, ports
	cfg.Executors = []job.Executor{destroyApprovalExecutor{Executor: nil}}
	cfg.Authorizer, cfg.Fences = identity.AdminOnly{}, destroyApprovalFence{}
	cfg.Now, cfg.Log = func() time.Time { return at }, slog.New(slog.DiscardHandler)
	cfg.Approvers = &gate.ApproverPolicy{Production: false, IsDev: nil}

	svc, err := job.NewService(cfg)
	if err != nil {
		t.Fatal(err)
	}

	return destroyApprovalRig{service: svc, store: st}
}
