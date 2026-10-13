//go:build e2e && journalrecording

package e2e_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// TestPulumiJournalingOn runs the full Pulumi scenario on the recording build,
// which answers journal version 1, so the real Pulumi CLI sends its batches to
// the journal route (ADR 43, work item 9). The scenario must pass, and the
// server must accept at least one journal batch.
func TestPulumiJournalingOn(t *testing.T) {
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
			runPulumiJournalingOn(t, fetcher, pulumiVersions[0], db)
		})
	}
}

// runPulumiJournalingOn installs one Pulumi CLI and runs the Pulumi scenario
// on a recording-build server with journaling on.
func runPulumiJournalingOn(t *testing.T, fetcher *e2e.Fetcher, pulumiLabel string, store e2e.Store) {
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

	pulumiScenario(t, env)

	if accepted := acceptedJournalBatches(env.Recorder.Entries()); accepted == 0 {
		t.Fatal("the Pulumi CLI sent no journal batch that the server accepted")
	}
}

// acceptedJournalBatches counts the journal batches that the server answered
// with 200.
func acceptedJournalBatches(entries []e2e.Entry) int {
	accepted := 0

	for _, entry := range entries {
		if entry.Method == http.MethodPatch && strings.HasSuffix(entry.Path, "/journalentries") &&
			entry.Status == http.StatusOK {
			accepted++
		}
	}

	return accepted
}
