//go:build e2e && journalrecording

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// TestPulumiJournalRefusalCancel proves the way out of a refused journal
// batch: the server refuses the last batch, which holds only elided entries,
// so the Pulumi CLI still sends complete with succeeded, and the server
// refuses that with 409. The update stays in progress and the stack stays
// locked until pulumi cancel ends it by building the checkpoint from the
// stored entries. A later up succeeds, and its export holds the resources
// the stored entries built.
func TestPulumiJournalRefusalCancel(t *testing.T) {
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
			runPulumiJournalRefusalCancel(t, fetcher, pulumiVersion, db)
		})
	}
}

// refusalProgram is the scratch YAML program of the refusal test. Its stack
// output makes the Pulumi CLI send a last batch that holds only an outputs
// entry, which the CLI ignores the error of.
const refusalProgram = "name: " + e2e.Project + "\nruntime: yaml\n" +
	"resources:\n" +
	"  ref:\n" +
	"    type: pulumi:pulumi:StackReference\n" +
	"    properties:\n" +
	"      name: " + e2e.Namespace + "/" + e2e.Project + "/other\n" +
	"outputs:\n" +
	"  refUrn: ${ref.urn}\n"

// runPulumiJournalRefusalCancel runs the refusal flow on one store: up with
// the outputs batch refused, complete refused, up blocked, cancel, up again,
// and the export comparison.
func runPulumiJournalRefusalCancel(t *testing.T, fetcher *e2e.Fetcher, pulumiLabel string, store e2e.Store) {
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

	t.Logf("pulumi %s on %s with journaling on and one batch refused", pulumiCLI.Version, store)

	refuser := &e2e.JournalRefuser{}
	env := e2e.NewEnvWithJournalRefuser(t.Context(), t, pulumiCLI, store, refuser)
	ctx := t.Context()

	env.WritePulumiProject(t, refusalProgram)

	qualified := fmt.Sprintf("%s/%s/%s", e2e.Namespace, e2e.Project, stackName)
	stackPath := fmt.Sprintf("/api/stacks/%s/%s", e2e.Namespace, e2e.Project)

	for _, s := range pulumiSetupSteps(env, e2e.Namespace, qualified, stackPath) {
		env.Step(ctx, t, s)
	}

	// The setup ends with the other stack selected, after its init. Select
	// it again, so the steps below name the updated stack explicitly.
	otherQualified := fmt.Sprintf("%s/%s/other", e2e.Namespace, e2e.Project)
	env.Step(ctx, t, step("stack select other", nil, "stack", "select", otherQualified))

	// The server refuses the last batch, which holds only the outputs entry.
	// The CLI ignores that refusal and sends complete with succeeded, which
	// the server refuses with 409.
	env.Step(ctx, t, failStep(
		"up with refused batch",
		failedCompleteNamesCancel,
		"up", "--yes", "--non-interactive",
	))

	checkRefusedComplete(t, env, refuser)

	// The update stays in progress, so the next up is blocked.
	env.Step(ctx, t, failStep(
		"up blocked after refused complete",
		outputContains("another update of this stack is in progress"),
		"up", "--yes", "--non-interactive",
	))

	env.Step(ctx, t, step("cancel", contains("has been canceled"), "cancel", "--yes"))

	exported := env.Step(ctx, t, step("stack export after cancel", nil, "stack", "export"))

	env.Step(ctx, t, step("up after cancel", nil, "up", "--yes", "--non-interactive"))

	exportedAgain := env.Step(ctx, t, step("stack export after up again", nil, "stack", "export"))

	checkCancelExportMatches(t, exported.Stdout, exportedAgain.Stdout)
}

// failedCompleteNamesCancel checks that the failed up shows the 409 of the
// refused complete: a journal batch was not stored, and pulumi cancel.
func failedCompleteNamesCancel(r e2e.Result) error {
	for _, want := range []string{"a journal batch was not stored", "pulumi cancel"} {
		if err := outputContains(want)(r); err != nil {
			return err
		}
	}

	return nil
}

// checkRefusedComplete proves the refusal flow through the request log: the
// refuser corrupted one batch, a journal batch answered 400, and a complete
// with succeeded answered 409 with the message that names pulumi cancel.
func checkRefusedComplete(t *testing.T, env *e2e.Env, refuser *e2e.JournalRefuser) {
	t.Helper()

	if got := refuser.Corrupted(); got != 1 {
		t.Fatalf("refuser corrupted %d batches, want 1", got)
	}

	checkRefusedBatch(t, env)
	checkRefusedSucceeded(t, env)
}

// checkRefusedBatch proves a journal batch answered 400: the refused outputs
// batch.
func checkRefusedBatch(t *testing.T, env *e2e.Env) {
	t.Helper()

	entries := env.Recorder.Entries()
	for i := range entries {
		e := entries[i]

		if e.Method == http.MethodPatch && strings.HasSuffix(e.Path, "/journalentries") &&
			e.Status == http.StatusBadRequest {
			return
		}
	}

	t.Fatal("no journal batch answered 400, want the refused outputs batch")
}

// checkRefusedSucceeded proves a complete with succeeded answered 409 with
// the message that names pulumi cancel.
func checkRefusedSucceeded(t *testing.T, env *e2e.Env) {
	t.Helper()

	entries := env.Recorder.Entries()
	for i := range entries {
		e := entries[i]

		if e.Method == http.MethodPost && strings.HasSuffix(e.Path, "/complete") &&
			e.Status == http.StatusConflict &&
			strings.Contains(e.ResponseBody, "was not stored") &&
			strings.Contains(e.ResponseBody, "pulumi cancel") {
			return
		}
	}

	t.Fatal("no complete answered 409 with the cancel message, want the refused succeeded end")
}

// checkCancelExportMatches proves the export after the cancel holds the same
// resources as the export after the later up: cancel built the checkpoint
// from the stored entries. Volatile resource fields and the outputs, which
// the refused batch never stored, stay out of the comparison.
func checkCancelExportMatches(t *testing.T, canceled, again string) {
	t.Helper()

	want := exportResources(t, canceled)
	got := exportResources(t, again)

	if !slices.Equal(want, got) {
		t.Fatalf("resources after cancel = %v, want the resources after the later up %v", want, got)
	}

	if len(want) == 0 {
		t.Fatal("the exports hold no resources, want the resources the stored entries built")
	}
}

// exportResource is one resource of a stack export, as decoded JSON.
type exportResource map[string]any

// exportDeployment is the deployment of a stack export: its resources.
type exportDeployment struct {
	Resources []exportResource `json:"resources"`
}

// exportDoc is a stack export: the schema version and the opaque deployment.
type exportDoc struct {
	Deployment exportDeployment `json:"deployment"`
}

// exportResources returns the sorted, normalized resources of a stack export:
// each resource without its volatile fields, as compact JSON. The outputs of
// the stack resource stay out: the refused outputs batch never stored them,
// so the cancel build holds none while the later up writes them.
func exportResources(t *testing.T, exported string) []string {
	t.Helper()

	var doc exportDoc

	if err := json.Unmarshal([]byte(exported), &doc); err != nil {
		t.Fatalf("decode stack export: %v", err)
	}

	out := make([]string, 0, len(doc.Deployment.Resources))

	for _, res := range doc.Deployment.Resources {
		delete(res, "created")
		delete(res, "modified")

		if res["type"] == "pulumi:pulumi:Stack" {
			delete(res, "outputs")
		}

		raw, err := json.Marshal(res)
		if err != nil {
			t.Fatalf("encode exported resource: %v", err)
		}

		out = append(out, string(raw))
	}

	slices.Sort(out)

	return out
}
