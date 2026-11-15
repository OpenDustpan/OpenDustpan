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

// TestStoreCheck proves `opendustpan store check` with RunBinary against the
// running binary server: a check of the blob store exits 0 and prints the
// report, a second check of the same target answers the cooldown, a check of
// the backup target runs, and a target that is not on the list is refused.
// It runs only with E2E_SERVER=binary, on every store under test.
func TestStoreCheck(t *testing.T) {
	t.Parallel()

	if !e2e.UseBinaryServer() {
		t.Skip("needs E2E_SERVER=binary")
	}

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
			runStoreCheckOnStore(t, cli, db)
		})
	}
}

// runStoreCheckOnStore runs the store check steps on one store.
func runStoreCheckOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnv(ctx, t, cli, store)

	check := env.RunBinary(
		ctx,
		t,
		"store",
		"check",
		"--server",
		env.URL,
		"--ca-file",
		env.CABundle(),
		"--target",
		"blobs",
	)
	if check.ExitCode != 0 {
		t.Fatalf("store check exits with status %d, want 0:\n%s%s", check.ExitCode, check.Stdout, check.Stderr)
	}

	for _, want := range []string{
		"on target blobs: succeeded", "pass  put then get returns the same bytes",
		"pass  delete of the prefix removes every object", "conditional writes: ", "prefix deleted: true",
	} {
		if !strings.Contains(check.Stdout, want) {
			t.Fatalf("store check output lacks %q:\n%s", want, check.Stdout)
		}
	}

	again := env.RunBinary(
		ctx,
		t,
		"store",
		"check",
		"--server",
		env.URL,
		"--ca-file",
		env.CABundle(),
		"--target",
		"blobs",
	)
	if again.ExitCode != 1 || !strings.Contains(again.Stdout+again.Stderr, "ended less than 15 minutes ago") {
		t.Fatalf("a second store check exits with status %d, want 1 with the cooldown:\n%s%s",
			again.ExitCode, again.Stdout, again.Stderr)
	}

	backup := env.RunBinary(
		ctx,
		t,
		"store",
		"check",
		"--server",
		env.URL,
		"--ca-file",
		env.CABundle(),
		"--target",
		"backup",
	)
	if backup.ExitCode != 0 || !strings.Contains(backup.Stdout, "on target backup: succeeded") {
		t.Fatalf("store check of the backup target exits with status %d, want 0:\n%s%s",
			backup.ExitCode, backup.Stdout, backup.Stderr)
	}

	checkStoreCheckRoutes(ctx, t, env)
}

// storeCheckListed is one check in the list answer.
type storeCheckListed struct {
	ID     string          `json:"id"`
	Target string          `json:"target"`
	State  string          `json:"state"`
	Report json.RawMessage `json:"report"`
}

// storeCheckList is the list answer.
type storeCheckList struct {
	StoreChecks []storeCheckListed `json:"storeChecks"`
}

// checkStoreCheckRoutes drives the routes with the harness: the list holds the
// two checks newest first with no report, a body with an address is refused, an
// unknown ID answers 404, and a request without a token answers 401.
func checkStoreCheckRoutes(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	list := readStoreCheckList(ctx, t, env)

	status, body, err := env.ServerAPIStatus(ctx, http.MethodGet, "/api/dustpan/v1/store-checks/"+list[1].ID, "")
	if err != nil || status != http.StatusOK || !strings.Contains(body, `"prefixDeleted":true`) {
		t.Fatalf("read store check = %d %s, %v", status, body, err)
	}

	status, body, err = env.ServerAPIStatus(
		ctx, http.MethodPost, "/api/dustpan/v1/store-checks", `{"target":"https://s3.example.test"}`,
	)
	if err != nil || status != http.StatusBadRequest || strings.Contains(body, "example.test") {
		t.Fatalf("start with an address = %d %s, %v; want 400 that does not repeat the address", status, body, err)
	}

	status, body, err = env.ServerAPIStatus(
		ctx,
		http.MethodGet,
		"/api/dustpan/v1/store-checks/chk-unknownunknownunkn",
		"",
	)
	if err != nil || status != http.StatusNotFound {
		t.Fatalf("read unknown store check = %d %s, %v; want 404", status, body, err)
	}

	checkStoreCheckUnauthorized(ctx, t, env, list[0].ID)
}

// readStoreCheckList reads the list and fails unless it holds the backup check
// and then the blob check, with no report.
func readStoreCheckList(ctx context.Context, t *testing.T, env *e2e.Env) []storeCheckListed {
	t.Helper()

	status, body, err := env.ServerAPIStatus(ctx, http.MethodGet, "/api/dustpan/v1/store-checks", "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("list store checks = %d %s, %v", status, body, err)
	}

	var list storeCheckList

	if decodeErr := json.Unmarshal([]byte(body), &list); decodeErr != nil || len(list.StoreChecks) != 2 {
		t.Fatalf("list = %s, %v; want 2 checks", body, decodeErr)
	}

	checks := list.StoreChecks
	if checks[0].Target != "backup" || checks[1].Target != "blobs" || len(checks[0].Report) != 0 {
		t.Fatalf("list = %s; want the backup check first, with no report", body)
	}

	return checks
}

// checkStoreCheckUnauthorized fails unless each store check route answers 401
// to a request without a token.
func checkStoreCheckUnauthorized(ctx context.Context, t *testing.T, env *e2e.Env, id string) {
	t.Helper()

	for _, target := range []struct{ method, path string }{
		{http.MethodPost, "/api/dustpan/v1/store-checks"},
		{http.MethodGet, "/api/dustpan/v1/store-checks"},
		{http.MethodGet, "/api/dustpan/v1/store-checks/" + id},
	} {
		status, body, err := env.ServerAPIStatusBare(ctx, target.method, target.path)
		if err != nil || status != http.StatusUnauthorized {
			t.Fatalf("%s %s without a token = %d %s, %v; want 401", target.method, target.path, status, body, err)
		}
	}
}
