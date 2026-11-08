//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// TestTofuSeededStateRead proves the state read routes through the real CLI:
// with state that the harness writes straight to the store, output reads the
// seeded outputs and state pull reads the seeded state. It runs on every
// store for every OpenTofu version in the matrix. Unlike the main scenario it
// writes no report: its run would overwrite the matrix report of the same
// version and store.
func TestTofuSeededStateRead(t *testing.T) {
	t.Parallel()

	tool := e2e.OpenTofu()

	if runtime.GOOS != "linux" {
		// OpenTofu must trust the test server's certificate, and Go honors
		// SSL_CERT_FILE only on Linux; elsewhere it uses the system store.
		t.Skipf("the OpenTofu suite needs Linux, not %s", runtime.GOOS)
	}

	fetcher := sharedFetcher(t)

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, label := range tool.Versions() {
		t.Run(label, func(t *testing.T) {
			t.Parallel()

			installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
			defer cancel()

			cli, err := fetcher.Install(installCtx, tool, label)
			if err != nil {
				t.Fatalf("install %s %s: %v", tool.Name, label, err)
			}

			t.Logf("%s %s (checksum %s) in %s", cli.Tool, cli.Version, cli.ChecksumSource, cli.Dir)

			for _, db := range databases {
				t.Run(string(db), func(t *testing.T) {
					t.Parallel()
					runSeededOnStore(t, cli, db)
				})
			}
		})
	}
}

// runSeededOnStore runs the seeded state read scenario without writing a
// report.
func runSeededOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	env := e2e.NewEnv(t.Context(), t, cli, store)
	ctx := t.Context()

	env.Step(ctx, t, step("init", checkTofuInit(ctx, env), "init", "-input=false", "-no-color"))
	env.SeedTofuState(ctx, t)
	env.Step(ctx, t, step("output", checkSeededOutput(env), "output", "-json", "-no-color"))
	env.Step(ctx, t, step("state pull", checkSeededStatePull(env), "state", "pull"))
}

// checkSeededOutput proves output prints the seeded greeting and read the
// sensitive output through the single-output route.
func checkSeededOutput(env *e2e.Env) func(e2e.Result) error {
	return func(r e2e.Result) error {
		if !strings.Contains(r.Stdout, `"hello"`) {
			return fmt.Errorf("%w: stdout lacks the seeded greeting: %s", errUnexpected, r.Stdout)
		}

		entries := env.Recorder.Entries()
		for i := range entries {
			if e := &entries[i]; e.Step == "output" && e.Method == http.MethodGet &&
				strings.HasPrefix(e.Path, "/api/v2/state-version-outputs/") && e.Status == http.StatusOK {
				return nil
			}
		}

		return fmt.Errorf("%w: output never read a single state version output", errUnexpected)
	}
}

// checkSeededStatePull proves state pull prints the seeded state and fetched
// it through the download route.
func checkSeededStatePull(env *e2e.Env) func(e2e.Result) error {
	return func(r e2e.Result) error {
		if !strings.Contains(r.Stdout, `"greeting"`) {
			return fmt.Errorf("%w: stdout lacks the seeded state: %s", errUnexpected, r.Stdout)
		}

		entries := env.Recorder.Entries()
		for i := range entries {
			if e := &entries[i]; e.Step == "state pull" && e.Method == http.MethodGet &&
				strings.HasSuffix(e.Path, "/download") && e.Status == http.StatusOK {
				return nil
			}
		}

		return fmt.Errorf("%w: state pull never used the download route", errUnexpected)
	}
}
