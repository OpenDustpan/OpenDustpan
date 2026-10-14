//go:build e2e

package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// overridePushDocument returns the pulled state with its lineage and serial
// changed, for the CLI to push on stdin. The resources and outputs stay as they
// were, so later scenarios still read the same state.
func overridePushDocument(t *testing.T, pulled, lineage string, serial int64) string {
	t.Helper()

	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(pulled), &doc); err != nil {
		t.Fatalf("decode the pulled state: %v", err)
	}

	doc["lineage"] = json.RawMessage(strconv.Quote(lineage))
	doc["serial"] = json.RawMessage(strconv.FormatInt(serial, 10))

	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode the push document: %v", err)
	}

	return string(out)
}

// overrideAgent is the User-Agent of the switch requests. The route coverage
// check counts requests that do not carry the harness agent.
const overrideAgent = "opendustpan-e2e-operator"

// overrideCurrent reads the lineage and the serial of the current state, which
// the next push must follow.
func overrideCurrent(t *testing.T, env *e2e.Env, name, path string) (string, int64) {
	t.Helper()

	var lineage string

	var serial int64

	env.APIStepA(t.Context(), t, e2e.APIStepSpec{
		UserAgent: overrideAgent,
		Name:      name,
		Method:    http.MethodGet,
		Path:      path,
		Body:      "",
		Want:      http.StatusOK,
		Check: func(body string) error {
			var doc stateDocument
			if err := json.Unmarshal([]byte(body), &doc); err != nil {
				return fmt.Errorf("decode the current state: %w", err)
			}

			lineage = doc.Data.Attributes.Lineage
			serial = doc.Data.Attributes.Serial

			return nil
		},
		Scheme: "",
	})

	return lineage, serial
}

// overrideSwitchesRejected sends an empty switch change and checks for 400.
func overrideSwitchesRejected(t *testing.T, env *e2e.Env, name, path string) {
	t.Helper()

	env.APIStepA(t.Context(), t, e2e.APIStepSpec{
		UserAgent: overrideAgent,
		Name:      name,
		Method:    http.MethodPut,
		Path:      path,
		Body:      "",
		Want:      http.StatusBadRequest,
		Check:     nil,
		Scheme:    "",
	})
}

// forcedCreate is one forced declare over the API and its expected answer.
type forcedCreate struct {
	name    string
	path    string
	serial  int64
	want    int
	refusal string
}

// overrideForcedCreate declares a forced state version over the API. The admin
// token holds the action, so only the switches decide the answer.
func overrideForcedCreate(t *testing.T, env *e2e.Env, c forcedCreate) {
	t.Helper()

	body := `{"data":{"type":"state-versions","attributes":{"serial":` + strconv.FormatInt(c.serial, 10) +
		`,"lineage":"lin-api-forced","md5":"d41d8cd98f00b204e9800998ecf8427e","force":true}}}`

	env.APIStepA(t.Context(), t, e2e.APIStepSpec{
		UserAgent: overrideAgent,
		Name:      c.name,
		Method:    http.MethodPost,
		Path:      c.path,
		Body:      body,
		Want:      c.want,
		Check: func(got string) error {
			if c.refusal != "" && !strings.Contains(got, c.refusal) {
				return fmt.Errorf("%w: refusal does not name %q: %s", errUnexpected, c.refusal, got)
			}

			return nil
		},
		Scheme: "",
	})
}

// overrideAuditHolds checks that the audit entries of the namespace hold every
// action. The audit route takes the installation token, as the other audit
// steps do.
func overrideAuditHolds(t *testing.T, env *e2e.Env, name string, actions ...string) {
	t.Helper()

	status, body, err := env.APIStatus(
		t.Context(),
		http.MethodGet,
		"/api/audit/entries?organization="+e2e.Namespace,
		"",
	)
	if err != nil || status != http.StatusOK {
		t.Errorf("%s: audit list answers %d, %v: %s", name, status, err, body)

		return
	}

	for _, action := range actions {
		if !strings.Contains(body, action) {
			t.Errorf("%s: audit has no %s entry: %s", name, action, body)
		}
	}
}

// overridePush is one state push with the real CLI. A refusal names the text
// that the CLI prints on stderr; an empty refusal expects success.
type overridePush struct {
	name    string
	args    []string
	doc     string
	refusal string
}

// run executes the push as a scenario step.
func (p overridePush) run(t *testing.T, env *e2e.Env) {
	t.Helper()

	spec := e2e.StepSpec{
		Check:  nil,
		Name:   p.name,
		Args:   p.args,
		Env:    nil,
		Stdin:  p.doc,
		Fail:   p.refusal != "",
		Redact: false,
	}

	if p.refusal != "" {
		spec.Check = func(res e2e.Result) error {
			if !strings.Contains(res.Stderr, p.refusal) {
				return fmt.Errorf("%w: refusal does not name %q: %s", errUnexpected, p.refusal, res.Stderr)
			}

			return nil
		}
	}

	env.Step(t.Context(), t, spec)
}

// overrideSwitches sets a switch body on path with the admin token.
func overrideSwitches(t *testing.T, env *e2e.Env, name, path, body string) {
	t.Helper()

	env.APIStepA(t.Context(), t, e2e.APIStepSpec{
		UserAgent: overrideAgent,
		Name:      name,
		Method:    http.MethodPut,
		Path:      path,
		Body:      body,
		Want:      http.StatusOK,
		Check:     nil,
		Scheme:    "",
	})
}

// overrideReadHolds checks that a switch read holds every fragment.
func overrideReadHolds(t *testing.T, env *e2e.Env, name, path string, fragments ...string) {
	t.Helper()

	env.APIStepA(t.Context(), t, e2e.APIStepSpec{
		UserAgent: overrideAgent,
		Name:      name,
		Method:    http.MethodGet,
		Path:      path,
		Body:      "",
		Want:      http.StatusOK,
		Check: func(body string) error {
			for _, fragment := range fragments {
				if !strings.Contains(body, fragment) {
					return fmt.Errorf("%w: switches do not hold %s: %s", errUnexpected, fragment, body)
				}
			}

			return nil
		},
		Scheme: "",
	})
}

// tofuOverrideSwitchesScenario proves the unlocked-write override and the
// switches of ADR 44 with the real OpenTofu CLI. A state push without the lock
// is refused by default, with the way out in its message. It is allowed once
// the namespace switch and the unit switch are on, and it is audited. Turning
// the unit switch off refuses it again. An empty switch change answers 400 and
// changes nothing. The scenario ends with both switches off, so later scenarios
// see the defaults.
func tofuOverrideSwitchesScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	wsID := env.WorkspaceID(t.Context(), t)
	currentPath := "/api/v2/workspaces/" + wsID + "/current-state-version"
	unitSwitches := "/api/v2/workspaces/" + wsID + "/override-switches"
	nsSwitches := "/api/server/namespaces/" + e2e.Namespace + "/override-switches"
	unlocked := []string{"state", "push", "-lock=false", "-"}

	pulled := env.Step(t.Context(), t, e2e.StepSpec{
		Check:  nil,
		Name:   "state pull for the override steps",
		Args:   []string{"state", "pull"},
		Env:    nil,
		Stdin:  "",
		Fail:   false,
		Redact: false,
	}).Stdout

	lineage, serial := overrideCurrent(t, env, "current state for the override steps", currentPath)

	overridePush{
		name:    "push without the lock refused by default",
		args:    unlocked,
		doc:     overridePushDocument(t, pulled, lineage, serial+1),
		refusal: "unlocked-write switch",
	}.run(t, env)

	overrideReadHolds(t, env, "namespace switches are off by default", nsSwitches,
		`"allowForcedWrites":false`, `"allowUnlockedWrites":false`)

	// An empty switch change answers 400 and changes nothing.
	overrideSwitchesRejected(t, env, "empty unit switch change answers 400", unitSwitches)
	overrideSwitchesRejected(t, env, "empty namespace switch change answers 400", nsSwitches)
	overrideReadHolds(t, env, "switches unchanged after the empty changes", unitSwitches,
		`"allow-forced-writes":false`, `"allow-unlocked-writes":false`)

	// The namespace switch and the unit switch allow the unlocked write.
	overrideSwitches(t, env, "turn on the namespace switches", nsSwitches,
		`{"allowForcedWrites":false,"allowUnlockedWrites":true}`)
	overrideSwitches(t, env, "turn on the unit switches", unitSwitches,
		`{"allow-forced-writes":false,"allow-unlocked-writes":true}`)
	overrideReadHolds(t, env, "unit switches read back on", unitSwitches,
		`"allow-unlocked-writes":true`)

	lineage, serial = overrideCurrent(t, env, "state before the push without the lock", currentPath)
	overridePush{
		name:    "push without the lock allowed by the switches",
		args:    unlocked,
		doc:     overridePushDocument(t, pulled, lineage, serial+1),
		refusal: "",
	}.run(t, env)

	overrideAuditHolds(t, env, "audit entries name the unlocked write", "unit:write-unlocked")

	// The unit switch narrows the namespace switch: off, the write is refused.
	overrideSwitches(t, env, "turn off the unit switches", unitSwitches,
		`{"allow-forced-writes":false,"allow-unlocked-writes":false}`)

	lineage, serial = overrideCurrent(t, env, "state before the narrowed push", currentPath)
	overridePush{
		name:    "push without the lock refused when the unit switch is off",
		args:    unlocked,
		doc:     overridePushDocument(t, pulled, lineage, serial+1),
		refusal: "unlocked-write switch",
	}.run(t, env)

	// A forced declare over the API needs the switches. Off, it is refused and
	// names the action; with both on, it is accepted and audited; off again, it is
	// refused again.
	createPath := "/api/v2/workspaces/" + wsID + "/state-versions"
	_, serial = overrideCurrent(t, env, "state before the forced declare", currentPath)
	overrideForcedCreate(t, env, forcedCreate{
		name: "forced declare refused with the switches off", path: createPath,
		serial: serial + 1, want: http.StatusForbidden, refusal: "unit:write-forced",
	})

	overrideSwitches(t, env, "turn on the namespace forced switch", nsSwitches,
		`{"allowForcedWrites":true,"allowUnlockedWrites":false}`)
	overrideSwitches(t, env, "turn on the unit forced switch", unitSwitches,
		`{"allow-forced-writes":true,"allow-unlocked-writes":false}`)
	// A forced write keeps the lock check, so the holder locks first.
	lockPath := "/api/v2/workspaces/" + wsID + "/actions/lock"
	env.APIStepA(t.Context(), t, e2e.APIStepSpec{
		UserAgent: overrideAgent,
		Name:      "lock for the forced declare",
		Method:    http.MethodPost,
		Path:      lockPath,
		Body:      `{"data":{}}`,
		Want:      http.StatusOK,
		Check:     nil,
		Scheme:    "",
	})
	overrideForcedCreate(t, env, forcedCreate{
		name: "forced declare accepted with both switches on", path: createPath,
		serial: serial + 1, want: http.StatusCreated, refusal: "",
	})
	overrideAuditHolds(t, env, "audit entries name the forced declare", "unit:write-forced")
	env.APIStepA(t.Context(), t, e2e.APIStepSpec{
		UserAgent: overrideAgent,
		Name:      "unlock after the forced declare",
		Method:    http.MethodPost,
		Path:      "/api/v2/workspaces/" + wsID + "/actions/unlock",
		Body:      "",
		Want:      http.StatusOK,
		Check:     nil,
		Scheme:    "",
	})

	overrideSwitches(t, env, "turn off the unit forced switch", unitSwitches,
		`{"allow-forced-writes":false,"allow-unlocked-writes":false}`)
	overrideForcedCreate(t, env, forcedCreate{
		name: "forced declare refused when the unit switch is off", path: createPath,
		serial: serial + 2, want: http.StatusForbidden, refusal: "unit:write-forced",
	})

	// Restore the defaults for the scenarios that follow.
	overrideSwitches(t, env, "turn the namespace switches off again", nsSwitches,
		`{"allowForcedWrites":false,"allowUnlockedWrites":false}`)
}
