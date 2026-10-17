//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// TestPulumiJournalingBothModes runs each supported Pulumi CLI version with
// journaling on and with it off. Both runs do the same up and give the same
// normalized stack export. A difference fails the step. The off server runs
// with DUSTPAN_DISABLE_PULUMI_JOURNALING=true and answers journalVersion 0
// at each update start, so the CLI saves checkpoints.
func TestPulumiJournalingBothModes(t *testing.T) {
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

	for _, label := range pulumiVersions {
		t.Run(label, func(t *testing.T) {
			t.Parallel()

			installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
			defer cancel()

			pulumiCLI, err := fetcher.Install(installCtx, e2e.Pulumi(), label)
			if err != nil {
				t.Fatalf("install pulumi %s: %v", label, err)
			}

			for _, db := range databases {
				t.Run(string(db), func(t *testing.T) {
					t.Parallel()
					runPulumiJournalingBothModes(t, pulumiCLI, db)
				})
			}
		})
	}
}

// runPulumiJournalingBothModes runs one Pulumi CLI with journaling on and
// with it off on fresh servers and compares the normalized stack exports.
func runPulumiJournalingBothModes(t *testing.T, pulumiCLI e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()

	onCLI := pulumiCLI
	onCLI.Label += "-journaling-on"

	offCLI := pulumiCLI
	offCLI.Label += "-journaling-off"

	t.Logf("pulumi %s on %s with journaling on and off", pulumiCLI.Version, store)

	onEnv := e2e.NewEnv(ctx, t, onCLI, store)
	offEnv := e2e.NewEnvWithJournalingOff(ctx, t, offCLI, store)

	onExport := journalingUp(ctx, t, onEnv)
	offExport := journalingUp(ctx, t, offEnv)

	onCanonical, offCanonical := canonicalExport(t, onExport), canonicalExport(t, offExport)
	if onCanonical != offCanonical {
		t.Errorf(
			"stack export with journaling on differs from stack export with it off:\non=%s\noff=%s",
			clipCanonical(onCanonical),
			clipCanonical(offCanonical),
		)
	}

	checkJournalingOffAnswersZero(t, offEnv)
	checkNoJournalBatches(t, offEnv)
}

// journalingUp logs in, creates the stack and the other stack its program
// references, runs up and returns the stack export. It carries no secrets,
// so both journaling runs hold the same state.
func journalingUp(ctx context.Context, t *testing.T, env *e2e.Env) string {
	t.Helper()

	qualified := fmt.Sprintf("%s/%s/%s", e2e.Namespace, e2e.Project, stackName)

	env.Step(ctx, t, step("login", contains(env.URL), "login"))
	env.Step(ctx, t, step("stack init", nil, "stack", "init", qualified))
	env.Step(ctx, t, step("stack init other", nil, "stack", "init", "other"))
	env.Step(ctx, t, step("stack select", nil, "stack", "select", qualified))
	env.Step(ctx, t, step("up", nil, "up", "--yes", "--non-interactive"))

	return env.Step(ctx, t, step("stack export", nil, "stack", "export")).Stdout
}

// checkJournalingOffAnswersZero proves the off run answered journalVersion 0
// at each update start, so the CLI saved checkpoints. It fails when no start
// answered or when a start answered another version.
func checkJournalingOffAnswersZero(t *testing.T, env *e2e.Env) {
	t.Helper()

	entries := env.Recorder.Entries()

	starts := 0

	for i := range entries {
		if !isJournalVersionStart(&entries[i]) {
			continue
		}

		if version := journalVersionOf(t, entries[i].ResponseBody); version != 0 {
			t.Fatalf("update start answers journalVersion %d, want 0", version)
		}

		starts++
	}

	if starts == 0 {
		t.Fatal("no update start answered journalVersion")
	}
}

// isJournalVersionStart answers whether entry is an update start that names
// a journal version.
func isJournalVersionStart(entry *e2e.Entry) bool {
	if entry.Method != http.MethodPost || entry.Status != http.StatusOK {
		return false
	}

	_, after, ok := strings.Cut(entry.Path, "/update/")
	if !ok || after == "" || strings.Contains(after, "/") {
		return false
	}

	return strings.Contains(entry.ResponseBody, "journalVersion")
}

// journalVersionOf answers the journal version in an update start body.
func journalVersionOf(t *testing.T, body string) int {
	t.Helper()

	var started struct {
		JournalVersion *int `json:"journalVersion"`
	}

	if err := json.Unmarshal([]byte(body), &started); err != nil {
		t.Fatalf("decode update start: %v", err)
	}

	if started.JournalVersion == nil {
		t.Fatal("update start answers no journalVersion")
	}

	return *started.JournalVersion
}

// checkNoJournalBatches proves the off run sent no journal batch that the
// server accepted. With journaling off the CLI saves checkpoints.
func checkNoJournalBatches(t *testing.T, env *e2e.Env) {
	t.Helper()

	entries := env.Recorder.Entries()

	for i := range entries {
		if isAcceptedJournalBatch(&entries[i]) {
			t.Fatal("journal batch answered 200 with journaling off, want checkpoints")
		}
	}
}

// isAcceptedJournalBatch answers whether entry is a journal batch the server
// accepted.
func isAcceptedJournalBatch(entry *e2e.Entry) bool {
	return entry.Method == http.MethodPatch &&
		strings.HasSuffix(entry.Path, "/journalentries") &&
		entry.Status == http.StatusOK
}
