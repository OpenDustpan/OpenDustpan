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
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// freezeMessage is the message of the deny gate of the scenario. The
// placeholder names a fact, so the check also proves the rendering.
const freezeMessage = "Writes to {{unit.tool}} are frozen for the e2e check."

// freezeText is freezeMessage as the refusal shows it.
const freezeText = "Writes to opentofu are frozen for the e2e check."

// must fails the test on an error.
func must(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("gate setup: %v", err)
	}
}

// stateWriteRecords returns the decision records of state.write in the e2e
// namespace, newest first.
func stateWriteRecords(t *testing.T, env *e2e.Env) []gate.Record {
	t.Helper()

	records, err := env.StateWriteRecords(t.Context(), namespace.Name(e2e.Namespace))
	must(t, err)

	return records
}

// gateScope returns the unit scope of the e2e workspace.
func gateScope(t *testing.T) gate.Scope {
	t.Helper()

	s, err := gate.UnitScope(namespace.Name(e2e.Namespace), "opentofu", e2e.Workspace)
	if err != nil {
		t.Fatalf("unit scope: %v", err)
	}

	return s
}

// stateWriteGate returns a gate at state.write for the e2e workspace.
func stateWriteGate(t *testing.T, id, condition string, outcome gate.Outcome, mode gate.Mode) gate.Gate {
	t.Helper()

	return gate.Gate{
		Scope: gateScope(t), ID: gate.ID(id), Version: 0, DecisionPoint: gate.PointStateWrite, Condition: condition,
		Unless: "", Outcome: outcome, Message: freezeMessage, Mode: mode, OnError: gate.OnErrorFail, Final: false,
		CheckedWith: gate.CheckedWith{Engine: "", Schema: gate.SchemaVersion{Subject: "", Major: 0, Minor: 0}},
		State:       "", Reads: nil,
	}
}

// refusalItem is one error of a JSON:API document with the code member.
type refusalItem struct {
	Status string `json:"status"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// refusalDocument is a JSON:API error document with the code member.
type refusalDocument struct {
	Errors []refusalItem `json:"errors"`
}

// refusalCheck returns a check of a 403 JSON:API document: the status, the
// machine code, the text and the ID of the decision record.
func refusalCheck(code, text string) func(string) error {
	return func(body string) error {
		var doc refusalDocument

		if err := json.Unmarshal([]byte(body), &doc); err != nil || len(doc.Errors) != 1 {
			return fmt.Errorf("%w: %q is not one JSON:API error: %w", errUnexpected, body, err)
		}

		e := doc.Errors[0]
		if e.Status != "403" || e.Code != code || !strings.Contains(e.Detail, text) ||
			!strings.Contains(e.Detail, "decision record gdr-") {
			return fmt.Errorf(
				"%w: error = %+v, want status 403, code %s, %q and a record ID",
				errUnexpected,
				e,
				code,
				text,
			)
		}

		return nil
	}
}

// checkNotLocked proves that the workspace holds no lock.
func checkNotLocked(body string) error {
	var doc lockDocument
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return fmt.Errorf("decode the workspace: %w", err)
	}

	if doc.Data.Attributes.LockID != "" {
		return fmt.Errorf(
			"%w: the workspace is locked by %q after the refusal",
			errUnexpected,
			doc.Data.Attributes.LockHolder,
		)
	}

	return nil
}

// checkRefusedWithoutALock returns a check of the output of a tofu command that
// the gate refused: it failed, it did not read the refusal as a held lock, which
// the CLI would retry, and it shows the error code and the ID of the decision
// record. It also shows the text of the gate message or the name of the limit.
// The CLI wraps its lines and draws a frame of box characters around an error,
// so the check joins the words of the output before it reads the text.
func checkRefusedWithoutALock(code, text string) func(e2e.Result) error {
	return func(r e2e.Result) error {
		out := r.Stdout + r.Stderr

		if strings.Contains(out, "already locked") {
			return fmt.Errorf("%w: the CLI read the refusal as a held lock: %s", errUnexpected, out)
		}

		words := strings.Join(strings.Fields(strings.ReplaceAll(out, "│", " ")), " ")

		for _, want := range []string{"Error acquiring the state lock", code, "gdr-", text} {
			if !strings.Contains(words, want) {
				return fmt.Errorf("%w: the CLI output lacks %q: %s", errUnexpected, want, out)
			}
		}

		return nil
	}
}

// gateRun holds what the steps of the state.write scenario share.
type gateRun struct {
	t         *testing.T
	env       *e2e.Env
	ns        namespace.Name
	lockPath  string
	unlock    string
	workspace string
	current   string
}

const lockCondition = `context.operation.kind == "lock"`

// get runs one TFE API read as the admin and expects 200.
func (g *gateRun) get(name, path string, check func(string) error) {
	g.t.Helper()

	g.env.APIStepA(g.t.Context(), g.t, e2e.APIStepSpec{
		Name: name, Method: http.MethodGet, Path: path, Body: "", Want: http.StatusOK, Check: check,
		UserAgent: "", Scheme: "",
	})
}

// post runs one TFE API post as the admin.
func (g *gateRun) post(name, path string, want int, check func(string) error) {
	g.t.Helper()

	g.env.APIStepA(g.t.Context(), g.t, e2e.APIStepSpec{
		Name: name, Method: http.MethodPost, Path: path, Body: `{"data":{}}`, Want: want, Check: check,
		UserAgent: "", Scheme: "",
	})
}

// planStep runs a tofu plan with no lock wait. A refused plan must fail with the
// check.
func (g *gateRun) planStep(name string, fail bool, check func(e2e.Result) error) {
	g.t.Helper()

	g.env.Step(g.t.Context(), g.t, e2e.StepSpec{
		Check: check, Name: name, Args: []string{"plan", "-input=false", "-no-color", "-lock-timeout=0s"},
		Env: nil, Stdin: "", Fail: fail, Redact: false,
	})
}

// newRecords reads the records and checks that there are want more than before.
func (g *gateRun) newRecords(before, want int) []gate.Record {
	g.t.Helper()

	got := stateWriteRecords(g.t, g.env)
	if len(got) != before+want {
		g.t.Fatalf("records = %d (before %d), want %d new", len(got), before, want)
	}

	return got
}

// put saves a gate of the unit.
func (g *gateRun) put(id, condition string, outcome gate.Outcome, mode gate.Mode) {
	g.t.Helper()
	must(g.t, g.env.PutGate(g.t.Context(), g.t, stateWriteGate(g.t, id, condition, outcome, mode)))
}

// remove removes a gate of the unit.
func (g *gateRun) remove(id string) {
	g.t.Helper()
	must(g.t, g.env.DeleteGate(g.t.Context(), g.t, gateScope(g.t), gate.ID(id)))
}

// tofuStateWriteGateScenario proves, with the real OpenTofu CLI, the decision
// point state.write at the lock: a deny gate and an access limit stop the lock
// with a 403 in the TFE error shape and leave no lock, a gate in evaluate mode
// stops nothing and writes a record, an approval outcome names the change
// order, and a write that nothing refuses goes on as before. Each evaluation
// writes one decision record.
func tofuStateWriteGateScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	// The steps write gates and read decision records through the store of the
	// in-process server. The server binary holds its database open, so in the
	// binary mode the steps do not run, and the rest of the scenario still does.
	if e2e.UseBinaryServer() {
		env.SkipScenario(t, "tofu state.write gates",
			"the steps read decision records, which have no route, so they need the in-process server",
			e2e.Gap{Route: "POST /api/v2/workspaces/{id}/actions/lock", Status: http.StatusForbidden})

		return
	}

	wsID := env.WorkspaceID(t.Context(), t)
	g := &gateRun{
		t: t, env: env, ns: namespace.Name(e2e.Namespace), lockPath: "/api/v2/workspaces/" + wsID + "/actions/lock",
		unlock: "/api/v2/workspaces/" + wsID + "/actions/unlock", workspace: "/api/v2/workspaces/" + wsID,
		current: "/api/v2/workspaces/" + wsID + "/current-state-version",
	}

	g.denyGate()
	g.evaluateGate()
	g.approvalGate()
	g.accessLimit()
	g.noGate()
}

// denyGate proves that a deny gate in enforce mode stops the lock, leaves no
// lock and changes no state, and that each refusal has a record.
func (g *gateRun) denyGate() {
	g.t.Helper()

	var before stateDocument

	g.get("gate: state before the gates", g.current, func(body string) error {
		if err := json.Unmarshal([]byte(body), &before); err != nil {
			return fmt.Errorf("decode the state: %w", err)
		}

		return nil
	})

	records := len(stateWriteRecords(g.t, g.env))

	g.put("freeze-e2e", lockCondition, gate.OutcomeDeny, gate.ModeEnforce)
	g.post("gate: the lock is refused with 403 in the TFE shape", g.lockPath, http.StatusForbidden,
		refusalCheck("gate.denied", freezeText))
	g.planStep("gate: tofu plan stops at the gate", true, checkRefusedWithoutALock("gate.denied", freezeText))
	g.get("gate: the workspace holds no lock after the refusal", g.workspace, checkNotLocked)
	g.get("gate: the state did not change", g.current, func(body string) error {
		var after stateDocument
		if err := json.Unmarshal([]byte(body), &after); err != nil {
			return fmt.Errorf("decode the state: %w", err)
		}

		if after.Data.Attributes != before.Data.Attributes {
			return fmt.Errorf("%w: the state changed under a refused lock", errUnexpected)
		}

		return nil
	})

	got := g.newRecords(records, 2)
	if got[0].Result != gate.ResultDeny || len(got[0].Units) != 1 || len(got[0].Units[0].Gates) != 1 ||
		!got[0].Units[0].Gates[0].Matched {
		g.t.Fatalf("newest record = %+v, want the result deny with the matched gate", got[0])
	}

	g.remove("freeze-e2e")
}

// evaluateGate proves that a gate in evaluate mode stops nothing and writes
// its record.
func (g *gateRun) evaluateGate() {
	g.t.Helper()

	records := len(stateWriteRecords(g.t, g.env))

	g.put("watch-e2e", lockCondition, gate.OutcomeDeny, gate.ModeEvaluate)
	g.env.Step(g.t.Context(), g.t, step("gate: tofu plan goes on under a gate in evaluate mode", nil,
		"plan", "-input=false", "-no-color"))

	got := g.newRecords(records, 1)
	if got[0].Result != gate.ResultAllow || len(got[0].Units[0].Gates) != 1 || !got[0].Units[0].Gates[0].Matched ||
		got[0].Units[0].Gates[0].Mode != gate.ModeEvaluate {
		g.t.Fatalf("newest record = %+v, want the result allow with the matched gate in evaluate mode", got[0])
	}

	g.remove("watch-e2e")
}

// approvalGate proves that an approval outcome refuses a CLI and names the
// rule and the unit.
func (g *gateRun) approvalGate() {
	g.t.Helper()

	rule := gate.ApprovalRule{
		Scope:   gate.NamespaceScope(g.ns),
		ID:      "review-e2e",
		Version: 0,
		Approvers: gate.Approvers{
			Groups:     nil,
			GroupIDs:   nil,
			Users:      []string{"e2e-reviewer"},
			UnitOwners: false,
		},
		Count:            1,
		AuthorMayApprove: false,
		FreshAuth:        gate.NoFreshAuth(),
		Timeout:          0,
	}

	must(g.t, g.env.PutApprovalRule(g.t.Context(), g.t, rule))
	g.put("review-gate-e2e", lockCondition, gate.ApprovalOutcome("review-e2e"), gate.ModeEnforce)
	g.post(
		"gate: an approval outcome refuses the lock and names the rule",
		g.lockPath,
		http.StatusForbidden,
		refusalCheck(
			"gate.denied",
			"needs approval by rule review-e2e. A write from a CLI cannot wait for approval, so the server refuses the write to "+tofuUnitRef(),
		),
	)
	g.remove("review-gate-e2e")
	must(g.t, g.env.DeleteApprovalRule(g.t.Context(), g.t, rule.Scope, rule.ID))
}

// accessLimit proves that a forbid access limit stops the lock.
func (g *gateRun) accessLimit() {
	g.t.Helper()

	limit := gate.AccessLimit{
		Scope: gateScope(g.t), ID: "no-writes-e2e", Version: 0,
		Policy: `forbid (principal, action == OpenDustpan::Action::"unit:write", resource);`,
	}

	must(g.t, g.env.PutAccessLimit(g.t.Context(), g.t, limit))
	g.post("gate: an access limit refuses the lock", g.lockPath, http.StatusForbidden,
		refusalCheck("gate.limited", "no-writes-e2e"))
	g.planStep(
		"gate: tofu plan stops at the access limit",
		true,
		checkRefusedWithoutALock("gate.limited", "no-writes-e2e"),
	)
	must(g.t, g.env.DeleteAccessLimit(g.t.Context(), g.t, limit.Scope, limit.ID))
}

// noGate proves that, with no gate and no limit, a write goes on as before:
// the lock, the plan and the unlock work, and each lock writes one record.
func (g *gateRun) noGate() {
	g.t.Helper()

	records := len(stateWriteRecords(g.t, g.env))

	g.env.Step(
		g.t.Context(),
		g.t,
		step("gate: tofu plan goes on with no gate", nil, "plan", "-input=false", "-no-color"),
	)
	g.post("gate: the lock goes on with no gate", g.lockPath, http.StatusOK, nil)
	g.post("gate: the unlock goes on with no gate", g.unlock, http.StatusOK, nil)

	if got := g.newRecords(records, 2); got[0].Result != gate.ResultAllow {
		g.t.Fatalf("newest record = %+v, want the result allow", got[0])
	}
}

// restoreGateCase names the operation that a gate refuses and the deleted unit
// that the check lists.
type restoreGateCase struct {
	operation, name string
}

// restoreGateSteps prove the decision point state.write at the restore and at
// the final delete of a deleted unit of the tool-neutral inventory API. A deny
// gate for the operation answers 403 and changes nothing. They run with the
// unit that target names, listed under name.
func restoreGateSteps(
	ctx context.Context, tb testing.TB, env *e2e.Env, target func() unitTarget, c restoreGateCase,
) []e2e.StepSpec {
	tb.Helper()

	operation, name := c.operation, c.name
	id := gate.ID("deleted-" + operation + "-e2e")
	ns := namespace.Name(e2e.Namespace)
	// The gate holds for the collection of the deleted unit only, so the
	// refusal proves that the guard read the real collection of the unit.
	scope := gate.CollectionScope(ns, deletedGateCollection)

	refused := func(status int, body string) error {
		if status != http.StatusForbidden || !strings.Contains(body, "Writes to opentofu are frozen") ||
			!strings.Contains(body, "gate.denied") || !strings.Contains(body, "decision record gdr-") {
			return fmt.Errorf("%w: status %d (%s), want 403 with the gate message, the code and a record ID",
				errUnexpected, status, body)
		}

		return nil
	}

	return []e2e.StepSpec{
		step("a gate refuses the "+operation+" of the deleted workspace", func(e2e.Result) error {
			g := gate.Gate{
				Scope: scope, ID: id, Version: 0, DecisionPoint: gate.PointStateWrite,
				Condition: `context.operation.kind == "` + operation + `"`, Unless: "", Outcome: gate.OutcomeDeny,
				Message: freezeMessage, Mode: gate.ModeEnforce, OnError: gate.OnErrorFail, Final: false,
				CheckedWith: gate.CheckedWith{Engine: "", Schema: gate.SchemaVersion{Subject: "", Major: 0, Minor: 0}},
				State:       "", Reads: nil,
			}
			if err := env.PutGate(ctx, tb, g); err != nil {
				return err
			}

			var (
				status int
				body   string
				err    error
			)

			if operation == "rollback" {
				status, body, err = restoreUnit(ctx, env, target(), "")
			} else {
				path := unitPath(target(), "")
				status, body, err = env.APIStatus(ctx, http.MethodDelete, path, "")
			}

			if err != nil {
				return err
			}

			return refused(status, body)
		}, "workspace", "list", "-no-color"),
		step("the refused "+operation+" changed nothing", func(e2e.Result) error {
			return checkDeletedListed(ctx, env, target(), name)
		}, "workspace", "list", "-no-color"),
		step("remove the gate of the "+operation, func(e2e.Result) error {
			return env.DeleteGate(ctx, tb, scope, id)
		}, "workspace", "list", "-no-color"),
	}
}
