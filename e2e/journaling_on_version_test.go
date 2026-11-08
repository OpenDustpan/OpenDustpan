//go:build e2e && journalrecording

package e2e_test

import (
	"context"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// TestPulumiJournalingOnVersion runs one journaled update with journaling on
// in the recording build and asserts that checkJournalingOn sees a nonzero
// journal version.
func TestPulumiJournalingOnVersion(t *testing.T) {
	t.Parallel()

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	pulumiVersion := e2e.Pulumi().Single()
	if pulumiVersion == "" {
		t.Fatal("the Pulumi tool needs at least one version in its matrix")
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runPulumiJournalingOnVersion(t, fetcher, pulumiVersion, db)
		})
	}
}

// runPulumiJournalingOnVersion installs one Pulumi CLI, runs one update with
// journaling on and proves the update start answered a nonzero journal
// version through checkJournalingOn.
func runPulumiJournalingOnVersion(t *testing.T, fetcher *e2e.Fetcher, pulumiLabel string, store e2e.Store) {
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

	t.Logf("pulumi %s on %s with journaling on", pulumiCLI.Version, store)

	env := e2e.NewEnv(t.Context(), t, pulumiCLI, store)
	ctx := t.Context()

	pairedUp(ctx, t, env)
	checkJournalingOn(t, env)
}
