package jobs_test

import (
	"fmt"
	"net/http"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

// TestStartDenialNamesTheRefusedAction proves that the audit entry of a
// refusal of the authorizer names the action and the object that the
// authorizer refused, that a refusal after the plan of the job names the start
// action of the job, and that any other refusal, or a refusal that names an
// object of another namespace, names the start of a plan on the unit.
func TestStartDenialNamesTheRefusedAction(t *testing.T) {
	t.Parallel()

	net, err := identity.UnitObject("opentofu", "acme", "net")
	if err != nil {
		t.Fatalf("UnitObject = %v", err)
	}

	other, err := identity.UnitObject("opentofu", "other", "net")
	if err != nil {
		t.Fatalf("UnitObject = %v", err)
	}

	denied := func(a identity.Action, o identity.Object) error {
		return fmt.Errorf("start: %w", &job.DeniedError{Action: a, Object: o, Err: identity.ErrDenied})
	}

	refused := func(a identity.Action, o identity.Object, cause error) error {
		return &job.StartRefusalError{Action: a, Object: o, Err: cause}
	}

	tests := []struct {
		name       string
		err        error
		wantAction identity.Action
		wantObject string
	}{
		{
			"the action of a pool", denied(job.ActionNamespaceManageRunners, identity.NamespaceObject("acme")),
			job.ActionNamespaceManageRunners, "acme",
		},
		{"the action of an apply", denied(job.ActionUnitRunApply, net), job.ActionUnitRunApply, "opentofu:acme/net"},
		{"a denial with no action", denied("", net), job.ActionUnitRunPlan, "opentofu:acme/net"},
		{
			"an object of another namespace", denied(job.ActionUnitRunApply, other), job.ActionUnitRunPlan,
			"opentofu:acme/net",
		},
		{"a refusal that is not a denial", job.ErrFrozen, job.ActionUnitRunPlan, "opentofu:acme/net"},
		{
			"a freeze of a destroy", refused(job.ActionUnitRunDestroy, net, job.ErrFrozen),
			job.ActionUnitRunDestroy, "opentofu:acme/net",
		},
		{
			"a limit of an apply", refused(job.ActionUnitRunApply, net, job.ErrLimit),
			job.ActionUnitRunApply, "opentofu:acme/net",
		},
		{
			"a refusal that names another namespace", refused(job.ActionUnitRunDestroy, other, job.ErrFrozen),
			job.ActionUnitRunPlan, "opentofu:acme/net",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			starter := newFakeStarter(t)
			starter.err = tt.err
			entries := &recordingAudit{Store: nil, mu: sync.Mutex{}, entries: nil}
			h := newStartHandler(t, newAuthorizer(readsNamespace()...), newFakeJobs(), starter, entries)

			if rec := post(t, h, testToken, goodBody); rec.Code == http.StatusCreated {
				t.Fatalf("status = %d, want a refusal", rec.Code)
			}

			if len(entries.entries) != 1 {
				t.Fatalf("audit entries = %d, want 1", len(entries.entries))
			}

			e := entries.entries[0]
			if e.Action != string(tt.wantAction) || e.Object != tt.wantObject || e.Namespace != "acme" {
				t.Fatalf("entry = %s on %s in %s, want %s on %s in acme", e.Action, e.Object, e.Namespace,
					tt.wantAction, tt.wantObject)
			}
		})
	}
}
