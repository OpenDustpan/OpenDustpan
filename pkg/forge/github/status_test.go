package github_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e/githubfake"
	"opendustpan.org/opendustpan/pkg/forge"
)

// cannedPAT is the token of the canned connection of cannedForge.
const cannedPAT = "github_pat_x"

func successStatus() forge.Status {
	return forge.Status{
		State: forge.StatusSuccess, Context: "opendustpan/plan", Description: "Plan ready",
		TargetURL: "https://dustpan.example/runs/1",
	}
}

// TestSetStatusMapsTheAnswer checks each answer of GitHub to a status call: a
// success must show the state and the context of the request, and each failure
// maps to a sentinel of the port with no part of the token in its text.
func TestSetStatusMapsTheAnswer(t *testing.T) {
	t.Parallel()

	good := `{"state":"success","context":"opendustpan/plan"}`
	limited := hostAnswer{
		status: http.StatusForbidden, body: `{"message":"rate limit"}`, header: map[string]string{"Retry-After": "7"},
		next: nil,
	}

	tests := []struct {
		name   string
		answer hostAnswer
		want   error
	}{
		{"a status that lands", statusAnswer(http.StatusCreated, good), nil},
		{
			"another state",
			statusAnswer(http.StatusCreated, `{"state":"failure","context":"opendustpan/plan"}`),
			forge.ErrUnavailable,
		},
		{
			"another context",
			statusAnswer(http.StatusCreated, `{"state":"success","context":"other"}`),
			forge.ErrUnavailable,
		},
		{"no state and no context", statusAnswer(http.StatusCreated, `{}`), forge.ErrUnavailable},
		{"an answer that does not parse", statusAnswer(http.StatusCreated, `not json`), forge.ErrUnavailable},
		{"a missing repository", statusAnswer(http.StatusNotFound, `{"message":"Not Found"}`), forge.ErrNotFound},
		{
			"a missing commit",
			statusAnswer(http.StatusUnprocessableEntity, `{"message":"No commit found for SHA: x"}`),
			forge.ErrNotFound,
		},
		{
			"the most statuses of a context",
			statusAnswer(
				http.StatusUnprocessableEntity,
				`{"message":"This SHA and context has reached the maximum number of statuses."}`,
			),
			forge.ErrRefused,
		},
		{
			"another 422",
			statusAnswer(http.StatusUnprocessableEntity, `{"message":"Validation Failed"}`),
			forge.ErrInvalid,
		},
		{
			"a missing permission",
			statusAnswer(http.StatusForbidden, `{"message":"Resource not accessible"}`),
			forge.ErrRefused,
		},
		{
			"a credential that is refused",
			statusAnswer(http.StatusUnauthorized, `{"message":"Bad credentials"}`),
			forge.ErrAuth,
		},
		{"a rate limit", limited, forge.ErrUnavailable},
		{"a bad gateway", statusAnswer(http.StatusBadGateway, ``), forge.ErrUnavailable},
		{"a service that is not available", statusAnswer(http.StatusServiceUnavailable, ``), forge.ErrUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := cannedForge(t, map[string]hostAnswer{"POST /repos/acme/infra/statuses/" + headSHA: tt.answer})

			err := f.SetStatus(t.Context(), "acme/infra", headSHA, successStatus())

			if tt.want == nil && err != nil || tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("SetStatus = %v, want %v", err, tt.want)
			}

			if err != nil && strings.Contains(err.Error(), cannedPAT) {
				t.Fatalf("the error holds the token: %v", err)
			}
		})
	}
}

// TestSetStatusSendsOnlyTheStatus checks the request: the fields of the status,
// no address when the status has none, and an installation token that has the
// permission to write the commit statuses and nothing else.
func TestSetStatusSendsOnlyTheStatus(t *testing.T) {
	t.Parallel()

	o := openAppWith(t, map[string]string{"statuses": "write", "metadata": "read"})
	c, err := o.host.Head(conformanceRepo, "main")
	if err != nil {
		t.Fatal(err)
	}

	bare := forge.Status{State: forge.StatusPending, Context: "opendustpan/apply", Description: "", TargetURL: ""}
	for _, s := range []forge.Status{successStatus(), bare} {
		if setErr := o.fg.SetStatus(t.Context(), conformanceRepo, c.ID, s); setErr != nil {
			t.Fatalf("SetStatus = %v", setErr)
		}
	}

	var bodies []string

	for _, r := range o.host.Requests() {
		if r.Method == http.MethodPost && strings.Contains(r.URI, "/statuses/") {
			bodies = append(bodies, string(r.Body))
		}
	}

	want := []string{
		`{"context":"opendustpan/plan","description":"Plan ready","state":"success",` +
			`"target_url":"https://dustpan.example/runs/1"}`,
		`{"context":"opendustpan/apply","description":"","state":"pending"}`,
	}
	if len(bodies) != 2 || bodies[0] != want[0] || bodies[1] != want[1] {
		t.Fatalf("the status requests = %v, want %v", bodies, want)
	}

	tokens := tokenBodies(o.host)
	if len(tokens) != 1 || tokens[0] != `{"repositories":["infra"],"permissions":{"statuses":"write"}}` {
		t.Fatalf("the token requests = %v, want one for the commit statuses only", tokens)
	}
}

// TestStatusCapabilityFollowsThePermissions checks that an installation with no
// permission to write the commit statuses lists no status capability after a
// check, and that SetStatus then makes no call for a status.
func TestStatusCapabilityFollowsThePermissions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		permissions map[string]string
		want        bool
	}{
		{"statuses write", map[string]string{"statuses": "write", "metadata": "read"}, true},
		{"statuses read", map[string]string{"statuses": "read", "metadata": "read"}, false},
		{"contents write only", map[string]string{"contents": "write", "metadata": "read"}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			o := openAppWith(t, tc.permissions)

			if !o.fg.Capabilities().Has(forge.CapStatus) {
				t.Fatal("the capability is not listed before the first check")
			}

			if _, err := o.fg.Check(t.Context()); err != nil {
				t.Fatalf("Check = %v", err)
			}

			if got := o.fg.Capabilities().Has(forge.CapStatus); got != tc.want {
				t.Fatalf("after Check, status listed = %v, want %v", got, tc.want)
			}

			before := len(o.host.Requests())
			err := o.fg.SetStatus(t.Context(), conformanceRepo, headSHA, successStatus())

			if !tc.want && (!errors.Is(err, forge.ErrUnsupported) || len(o.host.Requests()) != before) {
				t.Fatalf(
					"SetStatus = %v with %d calls, want ErrUnsupported and no call",
					err,
					len(o.host.Requests())-before,
				)
			}
		})
	}
}

// TestSetStatusRefusesAnInstallationThatCannotWrite checks the error of a
// token request that GitHub refuses for the permission to write statuses: the
// call is refused and the text names the permission.
func TestSetStatusRefusesAnInstallationThatCannotWrite(t *testing.T) {
	t.Parallel()

	// A host whose installation lacks the permission refuses the token request.
	host := newHostWith(t, func(c *githubfake.Config) {
		c.Permissions = map[string]string{"contents": "write", "metadata": "read"}
	})
	conn, cred := hostConnection(t, host, forge.AuthApp)

	fg, err := newHostOpener(t, host).Open(t.Context(), conn, cred)
	if err != nil {
		t.Fatal(err)
	}

	// The capability is listed until the first check, so the call reaches the token request.
	setErr := fg.SetStatus(t.Context(), conformanceRepo, headSHA, successStatus())
	if !errors.Is(setErr, forge.ErrRefused) || !strings.Contains(setErr.Error(), "commit statuses") {
		t.Fatalf("SetStatus = %v, want ErrRefused that names the commit statuses", setErr)
	}
}
