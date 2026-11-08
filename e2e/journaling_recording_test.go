//go:build e2e && journalrecording

package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// TestPulumiJournalingOn runs one `pulumi up` on a new stack in the recording
// build, which answers journal version 1, so the real Pulumi CLI sends its
// batches to the journal route (ADR 43, work item 9). The test checks that the
// server accepts at least one batch and that no batch answers 4xx or 5xx.
//
// Work item 10 restores the full Pulumi scenario here. Until then, a journaled
// update leaves the stack without state: the server builds no checkpoint from
// the journal at completion. The release build is not affected, because it
// answers journal version 0.
func TestPulumiJournalingOn(t *testing.T) {
	t.Parallel()

	fetcher := sharedFetcher(t)

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
			runPulumiJournalingOn(t, fetcher, pulumiVersion, db)
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
	ctx := t.Context()

	qualified := fmt.Sprintf("%s/%s/%s", e2e.Namespace, e2e.Project, stackName)
	stackPath := fmt.Sprintf("/api/stacks/%s/%s", e2e.Namespace, e2e.Project)

	for _, s := range pulumiSetupSteps(env, e2e.Namespace, qualified, stackPath) {
		env.Step(ctx, t, s)
	}

	env.Step(ctx, t, step("up", nil, "up", "--yes", "--non-interactive"))

	entries := env.Recorder.Entries()

	if accepted := acceptedJournalBatches(entries); accepted == 0 {
		t.Fatal("the Pulumi CLI sent no journal batch that the server accepted")
	}

	if failed := rejectedJournalBatches(entries); failed != 0 {
		t.Fatalf("%d journal batches answered 4xx or 5xx", failed)
	}
}

// rejectedJournalBatches counts the journal batches that answered 4xx or 5xx.
func rejectedJournalBatches(entries []e2e.Entry) int {
	rejected := 0

	for i := range entries {
		if strings.HasSuffix(entries[i].Path, "/journalentries") && entries[i].Status >= http.StatusBadRequest {
			rejected++
		}
	}

	return rejected
}

// acceptedJournalBatches counts the journal batches that the server answered
// with 200.
func acceptedJournalBatches(entries []e2e.Entry) int {
	accepted := 0

	for i := range entries {
		if entries[i].Method == http.MethodPatch && strings.HasSuffix(entries[i].Path, "/journalentries") &&
			entries[i].Status == http.StatusOK {
			accepted++
		}
	}

	return accepted
}
