//go:build e2e

package e2e_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// serviceSubjectSpellings are the subjects that a client sends to make the
// namespace service principal, or a principal that looks like it: the exact
// subject, other cases, and subjects with more text.
func serviceSubjectSpellings() []string {
	return []string{
		"system:namespace-service:teamns",
		"System:Namespace-Service:teamns",
		"SYSTEM:NAMESPACE-SERVICE:TEAMNS",
		"system:namespace-service:",
		"system:namespace-service:teamns/extra",
		"system:namespace-service:other-namespace",
	}
}

// TestNamespaceServicePrincipal proves, through the REST API of the server on
// each store, that no API call makes the namespace service principal, gives
// it a grant or a token, or lists it. The server builds that principal in its
// own process, so nothing about it is in the store or in the API.
func TestNamespaceServicePrincipal(t *testing.T) {
	t.Parallel()

	stores, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, store := range stores {
		t.Run(string(store), func(t *testing.T) {
			t.Parallel()

			if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			env := e2e.NewEnvWithRealTimeSignIn(t.Context(), t, e2e.NoCLI(), store)

			runNamespaceServicePrincipal(t.Context(), t, env)
		})
	}
}

// runNamespaceServicePrincipal runs the steps on one server.
func runNamespaceServicePrincipal(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	checkReady(ctx, t, env)

	principalCall(ctx, t, env,
		principalStep{http.MethodPost, "/api/server/namespaces", `{"name":"teamns"}`, http.StatusCreated}, nil)

	for _, subject := range serviceSubjectSpellings() {
		refuseServicePrincipalCreate(ctx, t, env, subject)
		refuseServicePrincipalGrantAndToken(ctx, t, env, subject)
	}

	// The refused calls left nothing behind: no principal, no grant and no
	// token names the subject.
	for _, path := range []string{
		"/api/server/principals?prefix=" + url.QueryEscape("system:"),
		"/api/server/principals?prefix=" + url.QueryEscape("System:"),
		"/api/server/namespaces/teamns/principals",
		"/api/server/namespaces/teamns/grants",
		"/api/server/grants",
	} {
		status, body, err := env.ServerAPIStatus(ctx, http.MethodGet, path, "")
		if err != nil || status != http.StatusOK {
			t.Fatalf("GET %s = %d, %v (%s); want 200", path, status, err, body)
		}

		if strings.Contains(strings.ToLower(body), "namespace-service") {
			t.Fatalf("GET %s = %s; want no mention of the namespace service principal", path, body)
		}
	}

	var page struct {
		Principals []e2ePrincipal `json:"principals"`
	}

	principalCall(ctx, t, env, principalStep{
		http.MethodGet, "/api/server/principals?prefix=" + url.QueryEscape("system:"), "", http.StatusOK,
	}, &page)

	if len(page.Principals) != 0 {
		t.Fatalf("principals with the prefix system: = %+v, want none", page.Principals)
	}
}

// refuseServicePrincipalCreate sends the subject to the two routes that make
// a principal, as the installation route and as the route of a namespace.
func refuseServicePrincipalCreate(ctx context.Context, t *testing.T, env *e2e.Env, subject string) {
	t.Helper()

	for _, step := range []principalStep{
		{http.MethodPost, "/api/server/principals", `{"subject":"` + subject + `","kind":"service"}`, http.StatusBadRequest},
		{http.MethodPost, "/api/server/principals", `{"subject":"` + subject + `","kind":"person"}`, http.StatusBadRequest},
		{http.MethodPost, "/api/server/namespaces/teamns/principals", `{"subject":"` + subject + `"}`, http.StatusBadRequest},
	} {
		refusedCall(ctx, t, env, step)
	}
}

// refuseServicePrincipalGrantAndToken sends the subject, as the name of a
// principal, to the routes that give a grant or a token. No principal has
// that name, so each call answers a 4xx status.
func refuseServicePrincipalGrantAndToken(ctx context.Context, t *testing.T, env *e2e.Env, subject string) {
	t.Helper()

	id := url.PathEscape(subject)

	for _, step := range []principalStep{
		{
			http.MethodPost, "/api/server/grants",
			`{"subject":"principal:` + subject + `","role":"viewer"}`, http.StatusNotFound,
		},
		{
			http.MethodPost, "/api/server/namespaces/teamns/grants",
			`{"subject":"principal:` + subject + `","role":"admin"}`, http.StatusNotFound,
		},
		{http.MethodPost, "/api/server/principals/" + id + "/tokens", `{"name":"ci"}`, http.StatusNotFound},
		{
			http.MethodPost, "/api/server/service-tokens",
			`{"principalID":"` + subject + `","name":"ci"}`, http.StatusNotFound,
		},
	} {
		refusedCall(ctx, t, env, step)
	}
}

// refusedCall sends step and fails unless the server answers a client error
// (status 400 to 499): the call is refused, and the exact status depends on
// the route and on the form of the name.
func refusedCall(ctx context.Context, t *testing.T, env *e2e.Env, step principalStep) {
	t.Helper()

	status, body := freshServerCall(ctx, t, env, step)
	if status < http.StatusBadRequest || status >= http.StatusInternalServerError {
		t.Fatalf("%s %s answers %d (%s), want a client error", step.method, step.path, status, body)
	}
}
