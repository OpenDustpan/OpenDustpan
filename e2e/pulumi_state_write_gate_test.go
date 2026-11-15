//go:build e2e

package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// pulumiFreezeText is freezeMessage as a refusal of a Pulumi write shows it.
const pulumiFreezeText = "Writes to pulumi are frozen for the e2e check."

// pulumiGateRun holds what the steps of the Pulumi state.write scenario share.
type pulumiGateRun struct {
	t   *testing.T
	env *e2e.Env
	org string
}

// scope returns the unit scope of the Pulumi stack name in the e2e project.
func (g *pulumiGateRun) scope(name string) gate.Scope {
	g.t.Helper()

	s, err := gate.UnitScope(namespace.Name(g.org), "pulumi", e2e.Project+"/"+name)
	if err != nil {
		g.t.Fatalf("unit scope: %v", err)
	}

	return s
}

// qualified returns the name of the stack as the Pulumi CLI reads it.
func (g *pulumiGateRun) qualified(name string) string {
	return fmt.Sprintf("%s/%s/%s", g.org, e2e.Project, name)
}

// put saves a gate of the stack name at state.write for the operation kind
// op, with the outcome.
func (g *pulumiGateRun) put(name, op string, outcome gate.Outcome) gate.ID {
	g.t.Helper()

	id := gate.ID("pulumi-" + op + "-" + string(outcome) + "-e2e")
	must(g.t, g.env.PutGate(g.t.Context(), g.t, gate.Gate{
		Scope: g.scope(name), ID: id, Version: 0, DecisionPoint: gate.PointStateWrite,
		Condition: `context.operation.kind == "` + op + `"`, Unless: "", Outcome: outcome,
		Message: freezeMessage, Mode: gate.ModeEnforce, OnError: gate.OnErrorFail, Final: false,
		CheckedWith: gate.CheckedWith{Engine: "", Schema: gate.SchemaVersion{Subject: "", Major: 0, Minor: 0}},
		State:       "", Reads: nil,
	}))

	return id
}

// remove removes the gate id of the stack name.
func (g *pulumiGateRun) remove(name string, id gate.ID) {
	g.t.Helper()
	must(g.t, g.env.DeleteGate(g.t.Context(), g.t, g.scope(name), id))
}

// step runs one CLI step.
func (g *pulumiGateRun) step(s e2e.StepSpec) e2e.Result {
	g.t.Helper()

	return g.env.Step(g.t.Context(), g.t, s)
}

// cliRefused returns a check of the output of a Pulumi command that a deny
// gate refused: the CLI shows the status, the gate message, the error code
// and the ID of the decision record.
func cliRefused() func(e2e.Result) error {
	return func(r e2e.Result) error {
		out := strings.Join(strings.Fields(r.Stdout+" "+r.Stderr), " ")

		for _, want := range []string{"403", pulumiFreezeText, "gate.denied", "decision record gdr-"} {
			if !strings.Contains(out, want) {
				return fmt.Errorf("%w: the CLI output lacks %q: %s", errUnexpected, want, out)
			}
		}

		return nil
	}
}

// pulumiStateWriteGateScenario proves, with the real Pulumi CLI, the decision
// point state.write at a stack import, a restore of a checkpoint, a cancel of
// an update, a stack rename and a stack rm. For each one, a deny gate for its
// operation kind refuses it in the Pulumi error shape and changes nothing,
// and a warn gate for the same operation kind lets it through. The restore has
// no CLI command, so its steps send the restore route.
func pulumiStateWriteGateScenario(t *testing.T, env *e2e.Env, org string) {
	t.Helper()

	// The steps write gates through the store of the in-process server, as the
	// OpenTofu scenario does. The server binary holds its database open, so in
	// the binary mode the steps do not run. The skip names the 403 lines of the
	// contract that only these steps produce.
	if e2e.UseBinaryServer() {
		stack := "/api/stacks/{namespace}/{project}/{stack}"
		env.SkipScenario(t, "pulumi state.write gates", "gate writes need the in-process server",
			e2e.Gap{Route: "DELETE " + stack, Status: http.StatusForbidden},
			e2e.Gap{Route: "POST " + stack + "/import", Status: http.StatusForbidden},
			e2e.Gap{Route: "POST " + stack + "/rename", Status: http.StatusForbidden},
			e2e.Gap{Route: "POST " + stack + "/update/{update}/cancel", Status: http.StatusForbidden})

		return
	}

	g := &pulumiGateRun{t: t, env: env, org: org}

	g.importGate()
	g.restoreGate()
	g.cancelGate()
	g.renameAndRemoveGate()
}

// importGate proves the gates at `pulumi stack import`.
func (g *pulumiGateRun) importGate() {
	g.t.Helper()

	renamed := g.qualified("renamed")
	exported := g.step(step("gate: stack export before the import", nil, "stack", "export", "--stack", renamed))
	path := writeImportFile(g.t, exported.Stdout)
	latest := latestHistoryVersion(g.t, g.env, g.org)

	id := g.put("renamed", "state-import", gate.OutcomeDeny)
	g.step(failStep("gate: stack import stops at a deny gate", cliRefused(),
		"stack", "import", "--file", path, "--stack", renamed))
	g.remove("renamed", id)

	if got := latestHistoryVersion(g.t, g.env, g.org); got != latest {
		g.t.Fatalf("latest version = %d after a refused import, want %d", got, latest)
	}

	id = g.put("renamed", "state-import", gate.OutcomeWarn)
	g.step(step("gate: stack import goes on under a warn gate", nil,
		"stack", "import", "--file", path, "--stack", renamed))
	g.remove("renamed", id)

	if got := latestHistoryVersion(g.t, g.env, g.org); got != latest+1 {
		g.t.Fatalf("latest version = %d after the import, want %d", got, latest+1)
	}
}

// restoreGate proves the gates at the restore of a checkpoint.
func (g *pulumiGateRun) restoreGate() {
	g.t.Helper()

	ctx := g.t.Context()
	path := fmt.Sprintf("/api/stacks/%s/%s/renamed/restore/1", g.org, e2e.Project)
	renamed := g.qualified("renamed")
	latest := latestHistoryVersion(g.t, g.env, g.org)

	id := g.put("renamed", "rollback", gate.OutcomeDeny)

	status, body, err := g.env.APIStatus(ctx, http.MethodPost, path, "")
	if err != nil {
		g.t.Fatalf("POST %s: %v", path, err)
	}

	g.remove("renamed", id)

	var refused struct {
		Code      int    `json:"code"`
		Message   string `json:"message"`
		ErrorCode string `json:"errorCode"`
	}

	if err := json.Unmarshal([]byte(body), &refused); err != nil || status != http.StatusForbidden ||
		refused.Code != http.StatusForbidden || refused.ErrorCode != "gate.denied" ||
		!strings.Contains(refused.Message, pulumiFreezeText) ||
		!strings.Contains(refused.Message, "decision record gdr-") {
		g.t.Fatalf("POST %s = %d %s (%v), want 403 in the Pulumi shape with the gate message", path, status, body, err)
	}

	if got := latestHistoryVersion(g.t, g.env, g.org); got != latest {
		g.t.Fatalf("latest version = %d after a refused restore, want %d", got, latest)
	}

	id = g.put("renamed", "rollback", gate.OutcomeWarn)
	restoreStackVersion(g.t, g.env, g.org, 1, http.StatusOK)
	g.remove("renamed", id)
	g.step(step("gate: stack history shows the restore", historyShowsKinds("restore"),
		"stack", "history", "--json", "--stack", renamed))
}

// cancelGate proves the gates at `pulumi cancel`.
func (g *pulumiGateRun) cancelGate() {
	g.t.Helper()

	ctx := g.t.Context()
	renamed := g.qualified("renamed")

	g.env.StartHeldUpdate(ctx, g.t, g.org, e2e.Project, "renamed")

	id := g.put("renamed", "unlock", gate.OutcomeDeny)
	g.step(failStep("gate: cancel stops at a deny gate", cliRefused(), "cancel", "--yes", "--stack", renamed))
	g.remove("renamed", id)

	// The refused cancel left the update running, so an up is still blocked.
	g.step(failStep("gate: the update still runs after a refused cancel",
		outputContains("another update of this stack is in progress"),
		"up", "--yes", "--non-interactive", "--stack", renamed))

	id = g.put("renamed", "unlock", gate.OutcomeWarn)
	g.step(step("gate: cancel goes on under a warn gate", contains("has been canceled"),
		"cancel", "--yes", "--stack", renamed))
	g.remove("renamed", id)
}

// renameAndRemoveGate proves the gates at `pulumi stack rename` and at
// `pulumi stack rm` on a stack of its own.
func (g *pulumiGateRun) renameAndRemoveGate() {
	g.t.Helper()

	before, after := g.qualified("gated"), g.qualified("gated-renamed")

	g.step(step("gate: stack init for the rename", nil, "stack", "init", before, "--no-select"))

	id := g.put("gated", "rename", gate.OutcomeDeny)
	g.step(failStep("gate: stack rename stops at a deny gate", cliRefused(),
		"stack", "rename", after, "--stack", before))
	g.remove("gated", id)
	g.step(step("gate: the stack keeps its name after a refused rename", lacks(`"gated-renamed"`),
		"stack", "ls", "--json"))

	id = g.put("gated", "rename", gate.OutcomeWarn)
	g.step(step("gate: stack rename goes on under a warn gate", nil, "stack", "rename", after, "--stack", before))
	// A gate of a unit holds for the unit and not for its name, so the gate
	// follows the stack to its new name.
	g.remove("gated-renamed", id)

	id = g.put("gated-renamed", "delete", gate.OutcomeDeny)
	g.step(failStep("gate: stack rm stops at a deny gate", cliRefused(), "stack", "rm", "--yes", "--stack", after))
	g.remove("gated-renamed", id)
	g.step(step("gate: the stack is still there after a refused rm", contains(`gated-renamed`),
		"stack", "ls", "--json"))

	// The gate stays with the deleted stack: its name no longer names a unit,
	// so the gate route cannot find the gate by that name.
	g.put("gated-renamed", "delete", gate.OutcomeWarn)
	g.step(step("gate: stack rm goes on under a warn gate", nil, "stack", "rm", "--yes", "--stack", after))
	g.step(step("gate: the stack is gone after the rm", lacks(`gated-renamed`), "stack", "ls", "--json"))
}

// lacks returns a check that stdout does not hold text.
func lacks(text string) func(e2e.Result) error {
	return func(r e2e.Result) error {
		if strings.Contains(r.Stdout, text) {
			return fmt.Errorf("%w: stdout holds %q: %s", errUnexpected, text, r.Stdout)
		}

		return nil
	}
}

// latestHistoryVersion returns the highest version in the history of the
// renamed stack. The history route answers one page, so the test reads the
// version and not the count of updates.
func latestHistoryVersion(t *testing.T, env *e2e.Env, org string) int {
	t.Helper()

	latest := 0

	for _, u := range readHistory(t.Context(), t, env, org) {
		latest = max(latest, u.Version)
	}

	return latest
}
