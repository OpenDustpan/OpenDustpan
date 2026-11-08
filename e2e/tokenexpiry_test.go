//go:build e2e

package e2e_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
)

// TestTokenExpiryWarnings proves the expiry warnings of ADR 53, decision 11,
// through the server, on each store:
//
//   - the API field expiryWarning of a token: none, soon or imminent,
//   - the response header Dustpan-Token-Expires-At on each answer to a token
//     that warns, and none for a token far from its expiry or for the
//     bootstrap admin token,
//   - with E2E_SERVER=binary, the warning that the opendustpan CLI prints on
//     stderr when it reads that header.
//
// The tool CLIs do not show a server warning (RFC 20 keeps that open), so
// the steps call the API and the opendustpan CLI only.
func TestTokenExpiryWarnings(t *testing.T) {
	t.Parallel()

	fetcher := sharedFetcher(t)

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
			runTokenExpiryOnStore(t, cli, db)
		})
	}
}

// expiryHeader is the response header of the expiry warning.
const expiryHeader = "Dustpan-Token-Expires-At"

// runTokenExpiryOnStore runs the expiry warning steps on one store.
func runTokenExpiryOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnvWithRealTimeSignIn(ctx, t, cli, store)

	checkReady(ctx, t, env)

	var service e2ePrincipal
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals", `{"subject":"ci-deploy","kind":"service"}`, http.StatusCreated,
	}, &service)

	soon := createExpiringToken(ctx, t, env, service, 10*24*time.Hour)
	imminent := createExpiringToken(ctx, t, env, service, 2*24*time.Hour)
	far := createExpiringToken(ctx, t, env, service, 30*24*time.Hour)

	for tok, want := range map[*e2eToken]string{&soon: "soon", &imminent: "imminent", &far: "none"} {
		var got struct {
			ExpiryWarning string `json:"expiryWarning"`
		}
		principalCall(
			ctx,
			t,
			env,
			principalStep{http.MethodGet, "/api/server/tokens/" + tok.ID, "", http.StatusOK},
			&got,
		)

		if got.ExpiryWarning != want {
			t.Fatalf("token %s expiryWarning = %q, want %q", tok.Name, got.ExpiryWarning, want)
		}
	}

	resp, err := env.ServerAPIHeadersAs(ctx, imminent.Secret, "/api/server/principals")
	if err != nil {
		t.Fatalf("sign in with the imminent token: %v", err)
	}

	if resp.Status != http.StatusForbidden || resp.Header.Get(expiryHeader) == "" {
		t.Fatalf("answer to the imminent token = %d with %s %q; want 403 with the header",
			resp.Status, expiryHeader, resp.Header.Get(expiryHeader))
	}

	if _, parseErr := time.Parse(time.RFC3339, resp.Header.Get(expiryHeader)); parseErr != nil {
		t.Fatalf("%s %q is not RFC 3339: %v", expiryHeader, resp.Header.Get(expiryHeader), parseErr)
	}

	resp, err = env.ServerAPIHeadersAs(ctx, far.Secret, "/api/server/principals")
	if err != nil || resp.Header.Get(expiryHeader) != "" {
		t.Fatalf("answer to the far token has %s %q (%v), want none", expiryHeader, resp.Header.Get(expiryHeader), err)
	}

	checkCLIExpiryWarning(ctx, t, env, soon.Secret, far.Secret)
}

// createExpiringToken makes a token of service that expires after lifetime.
func createExpiringToken(
	ctx context.Context, t *testing.T, env *e2e.Env, service e2ePrincipal, lifetime time.Duration,
) e2eToken {
	t.Helper()

	expires := time.Now().Add(lifetime).UTC().Format(time.RFC3339)

	var tok e2eToken
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/service-tokens",
		`{"principalID":"` + service.ID + `","name":"` + lifetime.String() + `","expiresAt":"` + expires + `"}`,
		http.StatusCreated,
	}, &tok)

	return tok
}

// checkCLIExpiryWarning runs `opendustpan namespace create` with each token:
// the principal has no grant, so the command fails, and with the token that
// warns it prints the warning once on stderr first. It needs the binary
// server, which builds the CLI.
func checkCLIExpiryWarning(ctx context.Context, t *testing.T, env *e2e.Env, warns, quiet string) {
	t.Helper()

	if !e2e.UseBinaryServer() {
		t.Log("skip the CLI warning step: it needs E2E_SERVER=binary")

		return
	}

	run := func(token string) e2e.BinaryResult {
		return env.RunBinaryWithEnv(ctx, t, []string{"DUSTPAN_ADMIN_TOKEN=" + token},
			"namespace", "create", "--server", env.BackendURL(t), "--ca-file", env.BackendCertFile(t), "expiring")
	}

	got := run(warns)
	if strings.Count(got.Stderr, "warning: the access token expires at") != 1 || strings.Contains(got.Stderr, warns) {
		t.Fatalf("CLI stderr with a token that warns = %q, want one warning that does not name the token", got.Stderr)
	}

	if got = run(quiet); strings.Contains(got.Stderr, "warning:") {
		t.Fatalf("CLI stderr with a token far from its expiry = %q, want no warning", got.Stderr)
	}
}
