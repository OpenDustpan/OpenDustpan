package job_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

// TestStartDenialNamesTheRefusedAction proves that a refusal of the
// authorizer is a *job.DeniedError that names the action and the object that
// the authorizer refused, and still wraps identity.ErrDenied.
func TestStartDenialNamesTheRefusedAction(t *testing.T) {
	t.Parallel()

	repo := job.Location{Connection: "", Repository: "acme/infra", Ref: "main", Directory: "", Revision: ""}
	acme := identity.NamespaceObject("acme")

	tests := []struct {
		name      string
		revoke    identity.Action
		change    func(req *job.StartRequest)
		wantNSObj bool
	}{
		{"the start action", job.ActionUnitRunPlan, func(*job.StartRequest) {}, false},
		{
			"the action of a pool", job.ActionNamespaceManageRunners,
			func(req *job.StartRequest) { req.Pool = "ci" }, true,
		},
		{
			"the action of a location", job.ActionUnitDefineSteps,
			func(req *job.StartRequest) { req.Location = repo }, false,
		},
		{
			"the action of a break-glass", job.ActionNamespaceBreakFreeze,
			func(req *job.StartRequest) { req.BreakGlass = &job.BreakGlass{Reason: "incident", Freeze: ""} }, true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t)

			want := r.unit
			if tt.wantNSObj {
				want = acme
			}

			delete(r.authz.allow, string(tt.revoke)+" "+want.String())

			req := r.plan("")
			tt.change(&req)

			_, err := r.svc.Start(t.Context(), actorOf("ada"), req)

			var denied *job.DeniedError
			if !errors.As(err, &denied) || !errors.Is(err, identity.ErrDenied) {
				t.Fatalf("Start = %v, want a *job.DeniedError that wraps identity.ErrDenied", err)
			}

			if denied.Action != tt.revoke || denied.Object.String() != want.String() {
				t.Fatalf("denial = %s on %s, want %s on %s", denied.Action, denied.Object.String(), tt.revoke,
					want.String())
			}
		})
	}
}

// TestDeniedErrorWithNoCause proves that a DeniedError with no Err still
// wraps identity.ErrDenied and names its action and object.
func TestDeniedErrorWithNoCause(t *testing.T) {
	t.Parallel()

	err := &job.DeniedError{Action: job.ActionUnitRunApply, Object: identity.NamespaceObject("acme"), Err: nil}

	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("errors.Is(%v, identity.ErrDenied) = false, want true", err)
	}

	if got, want := err.Error(), identity.ErrDenied.Error()+": unit:run-apply on acme"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}
