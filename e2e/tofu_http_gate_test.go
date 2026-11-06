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

// httpGateRun holds what the state.write steps of the http backend share.
type httpGateRun struct {
	gateRun

	backendEnv []string
	path       string
}

// runTofuHTTPStateWriteGate proves, with the real OpenTofu CLI, the decision
// point state.write on the http backend: a deny gate stops the LOCK of a plan
// with 403 in plain text and leaves no lock, a gate in evaluate mode stops
// nothing and writes a record, a deny gate stops a state version with no lock
// and says that resources may have no stored state, a deny gate stops a
// force-unlock, and with no gate a plan goes on as before.
//
// OpenTofu prints a fixed text for a 403 of the http backend and not the
// body, so each step reads the answer of the server from the recorder.
func runTofuHTTPStateWriteGate(t *testing.T, env *e2e.Env, backendEnv []string) {
	t.Helper()

	// The steps write gates and read decision records through the store of the
	// in-process server. The server binary holds its database open, so in the
	// binary mode the steps do not run, and the rest of the scenario still does.
	if e2e.UseBinaryServer() {
		t.Log("skip the http backend state.write gate steps: gate writes need the in-process server")

		return
	}

	wsID := env.WorkspaceID(t.Context(), t)
	g := &httpGateRun{
		t: t, env: env, ns: namespace.Name(e2e.Namespace), lockPath: "", unlock: "",
		workspace: "/api/v2/workspaces/" + wsID, current: "/api/v2/workspaces/" + wsID + "/current-state-version",
		backendEnv: backendEnv,
		path:       "/tofu/state/" + e2e.Namespace + "/" + e2e.Workspace,
	}

	g.denyLock()
	g.evaluateLock()
	g.denyUnlockedWrite()
	g.denyForceUnlock()
	g.noGateLock()
}

// tofu runs one tofu command with the backend credentials.
func (g *httpGateRun) tofu(name string, fail bool, check func(e2e.Result) error, stdin string, args ...string) {
	g.t.Helper()

	g.env.Step(g.t.Context(), g.t, e2e.StepSpec{
		Check: check, Name: name, Args: args, Env: g.backendEnv, Stdin: stdin, Fail: fail, Redact: false,
	})
}

// refusal is what one refused step must show: the request that the server
// refused, the text that the CLI prints for it, and the texts of the answer.
type refusal struct {
	step, method, shown string
	wants               []string
}

// refusedBy returns a check that the step sent the method of want to the
// http backend and that the server answered 403 in plain text with each text
// of want, the code and the ID of a decision record. It also checks that the
// CLI printed the text of want and did not read the refusal as a held lock.
// OpenTofu prints the status of a refused write and not the body.
func (g *httpGateRun) refusedBy(want refusal) func(e2e.Result) error {
	stepName, method, wants := want.step, want.method, want.wants

	return func(r e2e.Result) error {
		out := r.Stdout + r.Stderr
		if strings.Contains(out, "already locked") || !strings.Contains(out, want.shown) {
			return fmt.Errorf("%w: the CLI output lacks %q, or reads the refusal as a held lock: %s",
				errUnexpected, want.shown, out)
		}

		entries := g.env.Recorder.Entries()
		for i := range entries {
			if e := &entries[i]; e.Step == stepName && e.Method == method && e.Path == g.path {
				return checkRefusalBody(e, append([]string{"gate.denied", "decision record gdr-"}, wants...))
			}
		}

		return fmt.Errorf("%w: step %q sent no %s to %s", errUnexpected, stepName, method, g.path)
	}
}

// checkRefusalBody checks that the server answered the request of e with 403
// and a body that holds each of wants.
func checkRefusalBody(e *e2e.Entry, wants []string) error {
	if e.Status != http.StatusForbidden {
		return fmt.Errorf("%w: %s answered %d, want 403: %s", errUnexpected, e.Method, e.Status, e.ResponseBody)
	}

	for _, want := range wants {
		if !strings.Contains(e.ResponseBody, want) {
			return fmt.Errorf("%w: the 403 body lacks %q: %s", errUnexpected, want, e.ResponseBody)
		}
	}

	return nil
}

// recordIDs returns the IDs of the decision records of state.write now.
func (g *httpGateRun) recordIDs() map[gate.RecordID]bool {
	g.t.Helper()

	records := stateWriteRecords(g.t, g.env)
	ids := map[gate.RecordID]bool{}

	for i := range records {
		ids[records[i].ID] = true
	}

	return ids
}

// oneNewRecord returns the one decision record that is not in before. The
// test clock of the server does not move, so the records of one test share
// one time, and the order of the list does not tell the new record.
func (g *httpGateRun) oneNewRecord(before map[gate.RecordID]bool) gate.Record {
	g.t.Helper()

	records := stateWriteRecords(g.t, g.env)

	var fresh []gate.Record

	for i := range records {
		if !before[records[i].ID] {
			fresh = append(fresh, records[i])
		}
	}

	if len(fresh) != 1 {
		g.t.Fatalf("new decision records = %+v, want exactly one", fresh)
	}

	return fresh[0]
}

// stateSerial reads the serial of the current state version.
func (g *httpGateRun) stateSerial(name string) int64 {
	g.t.Helper()

	var doc stateDocument

	g.get(name, g.current, func(body string) error {
		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			return fmt.Errorf("decode the state: %w", err)
		}

		return nil
	})

	return doc.Data.Attributes.Serial
}

// denyLock proves that a deny gate at the lock stops tofu plan, leaves no
// lock, changes no state and writes a record with the result deny.
func (g *httpGateRun) denyLock() {
	g.t.Helper()

	serial := g.stateSerial("http gate: state before the deny gate")
	before := g.recordIDs()

	const name = "http gate: tofu plan stops at a deny gate"

	g.put("http-freeze-e2e", lockCondition, gate.OutcomeDeny, gate.ModeEnforce)
	g.tofu(name, true, g.refusedBy(refusal{
		step: name, method: "LOCK", shown: "HTTP remote state endpoint invalid auth", wants: []string{freezeText},
	}), "",
		"plan", "-input=false", "-no-color", "-lock-timeout=0s")
	g.get("http gate: the workspace holds no lock after the refusal", g.workspace, checkNotLocked)

	if after := g.stateSerial("http gate: state after the deny gate"); after != serial {
		g.t.Fatalf("serial %d after a refused lock, want %d", after, serial)
	}

	got := g.oneNewRecord(before)
	if got.Result != gate.ResultDeny || len(got.Units) != 1 || len(got.Units[0].Gates) != 1 ||
		!got.Units[0].Gates[0].Matched || got.Units[0].Facts.Operation.Kind != gate.OperationLock {
		g.t.Fatalf("new record = %+v, want the result deny at the lock with the matched gate", got)
	}

	g.remove("http-freeze-e2e")
}

// evaluateLock proves that a gate in evaluate mode stops nothing and writes
// its record.
func (g *httpGateRun) evaluateLock() {
	g.t.Helper()

	before := g.recordIDs()

	g.put("http-watch-e2e", lockCondition, gate.OutcomeDeny, gate.ModeEvaluate)
	g.tofu("http gate: tofu plan goes on under a gate in evaluate mode", false, nil, "",
		"plan", "-input=false", "-no-color")

	got := g.oneNewRecord(before)
	if got.Result != gate.ResultAllow || len(got.Units[0].Gates) != 1 || !got.Units[0].Gates[0].Matched ||
		got.Units[0].Gates[0].Mode != gate.ModeEvaluate {
		g.t.Fatalf("new record = %+v, want the result allow with the matched gate in evaluate mode", got)
	}

	g.remove("http-watch-e2e")
}

// denyUnlockedWrite proves that a deny gate stops a state version with no
// lock, through the unlocked override, before the write: the state does not
// change, and the answer says that resources may have no stored state.
func (g *httpGateRun) denyUnlockedWrite() {
	g.t.Helper()

	pulled := g.env.Step(g.t.Context(), g.t, e2e.StepSpec{
		Check: nil, Name: "http gate: state pull for the unlocked push",
		Args: []string{"state", "pull"}, Env: g.backendEnv, Stdin: "", Fail: false, Redact: false,
	}).Stdout
	lineage, serial := overrideCurrent(g.t, g.env, "http gate: current state for the unlocked push", g.current)
	push := overridePushDocument(g.t, pulled, lineage, serial+1)

	nsSwitches := "/api/server/namespaces/" + e2e.Namespace + "/override-switches"
	unitSwitches := g.workspace + "/override-switches"

	overrideSwitches(g.t, g.env, "http gate: turn on the namespace switches", nsSwitches,
		`{"allowForcedWrites":false,"allowUnlockedWrites":true}`)
	overrideSwitches(g.t, g.env, "http gate: turn on the unit switches", unitSwitches,
		`{"allow-forced-writes":false,"allow-unlocked-writes":true}`)

	const name = "http gate: state push with no lock stops at a deny gate"

	g.put("http-unlocked-e2e", `context.operation.kind == "unlocked-write"`, gate.OutcomeDeny, gate.ModeEnforce)
	g.tofu(name, true, g.refusedBy(refusal{
		step: name, method: http.MethodPost, shown: "HTTP error: 403",
		wants: []string{freezeText, "resources may exist that no stored state records"},
	}), push, "state", "push", "-lock=false", "-")

	if after := g.stateSerial("http gate: state after the refused push"); after != serial {
		g.t.Fatalf("serial %d after a refused push, want %d", after, serial)
	}

	g.remove("http-unlocked-e2e")

	overrideSwitches(g.t, g.env, "http gate: turn off the unit switches", unitSwitches,
		`{"allow-forced-writes":false,"allow-unlocked-writes":false}`)
	overrideSwitches(g.t, g.env, "http gate: turn off the namespace switches", nsSwitches,
		`{"allowForcedWrites":false,"allowUnlockedWrites":false}`)
}

// denyForceUnlock proves that a deny gate stops a force-unlock of a lock that
// another principal holds, and that the lock stays.
func (g *httpGateRun) denyForceUnlock() {
	g.t.Helper()

	ctx := g.t.Context()

	status, body, err := g.env.TFEStatusB(ctx, "LOCK", g.path, tofuHTTPForceLock)
	if err != nil || status != http.StatusOK {
		g.t.Fatalf("LOCK by principal B: status %d, body %q, err %v", status, body, err)
	}

	const name = "http gate: force-unlock stops at a deny gate"

	g.put("http-unlock-e2e", `context.operation.kind == "unlock"`, gate.OutcomeDeny, gate.ModeEnforce)
	g.tofu(name, true, g.refusedBy(refusal{
		step: name, method: "UNLOCK", shown: "Unexpected HTTP response code 403", wants: []string{freezeText},
	}), "", "force-unlock", "-force", "e2e-lock-2")
	g.get("http gate: the lock of B stays after the refusal", g.workspace, func(body string) error {
		if lockErr := checkNotLocked(body); lockErr == nil {
			return fmt.Errorf("%w: the refused force-unlock released the lock", errUnexpected)
		}

		return nil
	})
	g.remove("http-unlock-e2e")

	status, body, err = g.env.TFEStatusB(ctx, "UNLOCK", g.path, tofuHTTPForceLock)
	if err != nil || status != http.StatusOK {
		g.t.Fatalf("UNLOCK by principal B: status %d, body %q, err %v", status, body, err)
	}
}

// noGateLock proves that, with no gate, a plan goes on and its lock writes a
// record with the result allow.
func (g *httpGateRun) noGateLock() {
	g.t.Helper()

	before := g.recordIDs()

	g.tofu("http gate: tofu plan goes on with no gate", false, nil, "", "plan", "-input=false", "-no-color")

	if got := g.oneNewRecord(before); got.Result != gate.ResultAllow {
		g.t.Fatalf("new record = %+v, want the result allow", got)
	}
}
