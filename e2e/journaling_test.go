//go:build e2e

package e2e_test

import (
	"context"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// TestPulumiJournalingOff runs the full Pulumi scenario on a server that
// turns Pulumi journaling off. With the setting on, each update answers
// journalVersion 0 at start, so the CLI saves checkpoints. The scenario must
// pass on every store under test, as it does with the default setting.
func TestPulumiJournalingOff(t *testing.T) {
	t.Parallel()

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	pulumiVersions := e2e.Pulumi().Versions()
	if len(pulumiVersions) == 0 {
		t.Fatal("the Pulumi tool needs at least one version in its matrix")
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runPulumiJournalingOff(t, fetcher, pulumiVersions[0], db)
		})
	}
}

// runPulumiJournalingOff installs one Pulumi CLI and runs the Pulumi scenario
// on a server with journaling off.
func runPulumiJournalingOff(t *testing.T, fetcher *e2e.Fetcher, pulumiLabel string, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	defer cancel()

	pulumiCLI, err := fetcher.Install(installCtx, e2e.Pulumi(), pulumiLabel)
	if err != nil {
		t.Fatalf("install pulumi %s: %v", pulumiLabel, err)
	}

	t.Logf("pulumi %s on %s with journaling off", pulumiCLI.Version, store)

	env := e2e.NewEnvWithJournalingOff(t.Context(), t, pulumiCLI, store)

	pulumiScenario(t, env)
}
