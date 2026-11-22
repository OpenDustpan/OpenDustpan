package runnerapi

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// TestClaimCallerBinding sends hostile callers straight to the binding of the
// handler, as if the token service and the registry failed: the binding
// refuses by itself a job token with no job, with another job, and for another
// workspace, and an access token that names a job.
func TestClaimCallerBinding(t *testing.T) {
	t.Parallel()

	unit, err := identity.UnitObject("opentofu", namespace.Name("acme"), "network")
	if err != nil {
		t.Fatalf("unit: %v", err)
	}

	fence := namespace.Fence{Namespace: "acme", FailoverVersion: 1}
	target := claimTarget{id: "ws-1", unit: unit.WithUnitID("ws-1"), fence: fence, deleted: nil}

	var person identity.Principal

	person.Subject = "ada"

	jobToken := func(job, unitID string) claimCaller {
		p := person
		p.JobID = job

		return claimCaller{principal: p, viaJobToken: true, tokenUnit: unit.WithUnitID(unitID)}
	}
	accessToken := claimCaller{principal: person, viaJobToken: false, tokenUnit: identity.Object{}}

	cases := []struct {
		name     string
		caller   claimCaller
		jobID    string
		register bool
		want     error
	}{
		{"job token of the job", jobToken("job-1", "ws-1"), "job-1", true, nil},
		{"job token, list", jobToken("job-1", "ws-1"), "", false, nil},
		{"job token that names no job and a request with none", jobToken("", "ws-1"), "", true, errClaimJobMismatch},
		{"job token with the request of no job", jobToken("job-1", "ws-1"), "", true, errClaimJobMismatch},
		{"job token with the request of another job", jobToken("job-1", "ws-1"), "job-2", true, errClaimJobMismatch},
		{
			"job token of another workspace, register",
			jobToken("job-1", "ws-2"),
			"job-1",
			true,
			workspace.ErrClaimNotFound,
		},
		{"job token of another workspace, list", jobToken("job-1", "ws-2"), "", false, workspace.ErrClaimNotFound},
		{"job token with no unit ID", jobToken("job-1", ""), "job-1", true, workspace.ErrClaimNotFound},
		{"access token, no job", accessToken, "", true, nil},
		{"access token that names a job", accessToken, "job-1", true, errClaimJobMismatch},
	}

	for _, c := range cases {
		got := c.caller.bindList(target)
		if got == nil && c.register {
			got = c.caller.bindJob(c.jobID)
		}

		if !errors.Is(got, c.want) && (c.want != nil || got != nil) {
			t.Errorf("%s: bind = %v, want %v", c.name, got, c.want)
		}
	}
}
