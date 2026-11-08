//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// TestNoWorkingBreakGlassIsKept drives the disable and the delete of break-glass
// accounts through the real binary, on each store. The disable of one of two
// works. The disable and the delete of the last enabled one answer 409 with the
// code principal.no-working-break-glass, and the account stays enabled.
func TestNoWorkingBreakGlassIsKept(t *testing.T) {
	t.Parallel()

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	version := e2e.Pulumi().Single()
	if version == "" {
		t.Fatal("the Pulumi tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	cli, err := fetcher.Install(installCtx, e2e.Pulumi(), version)
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.Pulumi().Name, version, err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runNoWorkingBreakGlassOnStore(t, cli, db)
		})
	}
}

// e2eBreakGlassState is the part of a principal answer that the steps read.
type e2eBreakGlassState struct {
	DisabledAt *string `json:"disabledAt"`
}

// e2eRefusal is the error body of a refused request.
type e2eRefusal struct {
	Message   string `json:"message"`
	ErrorCode string `json:"errorCode"`
	Code      int    `json:"code"`
}

// runNoWorkingBreakGlassOnStore runs the steps on one store.
func runNoWorkingBreakGlassOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnvWithSignIn(ctx, t, cli, store)

	checkReady(ctx, t, env)

	ids := env.SeedBreakGlass(ctx, t, "first", "second")
	first, second := ids[0], ids[1]

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals/" + first + "/disable", "", http.StatusOK,
	}, nil)

	for _, step := range []principalStep{
		{http.MethodPost, "/api/server/principals/" + second + "/disable", "", http.StatusConflict},
		{http.MethodDelete, "/api/server/principals/" + second, "", http.StatusConflict},
	} {
		status, resp := freshServerCall(ctx, t, env, step)
		if status != step.want {
			t.Fatalf("%s %s answers %d, want %d (%s)", step.method, step.path, status, step.want, resp)
		}

		var refusal e2eRefusal
		if err := json.Unmarshal([]byte(resp), &refusal); err != nil {
			t.Fatalf("decode %s %s answer %q: %v", step.method, step.path, resp, err)
		}

		if refusal.ErrorCode != "principal.no-working-break-glass" ||
			!strings.Contains(refusal.Message, "The change leaves too few working break-glass accounts") {
			t.Fatalf("%s %s answers %+v, want the code principal.no-working-break-glass and its message",
				step.method, step.path, refusal)
		}
	}

	var state e2eBreakGlassState

	principalCall(ctx, t, env, principalStep{
		http.MethodGet, "/api/server/principals/" + second, "", http.StatusOK,
	}, &state)

	if state.DisabledAt != nil {
		t.Fatalf("the last break-glass account is disabled at %q after the refused requests", *state.DisabledAt)
	}
}
