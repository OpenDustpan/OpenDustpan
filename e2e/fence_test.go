//go:build e2e

package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// emptyMD5 is the hex MD5 of empty content. The stale job's declare names it.
const emptyMD5 = "d41d8cd98f00b204e9800998ecf8427e"

// stateAttributes holds the lineage and the serial of a state version.
type stateAttributes struct {
	Lineage string `json:"lineage"`
	Serial  int64  `json:"serial"`
}

// stateData holds the data object of a state version document.
type stateData struct {
	Attributes stateAttributes `json:"attributes"`
}

// stateDocument reads the lineage and the serial of a state version document.
type stateDocument struct {
	Data stateData `json:"data"`
}

// tofuForceUnlockFenceScenario proves ADR 44 with the real OpenTofu CLI. The
// stale job and job B share one token, so the server sees one holder, which
// is the case the ADR guards. After B's force-unlock, the fence is the current
// state, B's apply writes through it, and the stale job's next declare fails
// with the lineage error.
func tofuForceUnlockFenceScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	wsID := env.WorkspaceID(ctx, t)
	lockPath := "/api/v2/workspaces/" + wsID + "/actions/lock"
	unlockPath := "/api/v2/workspaces/" + wsID + "/actions/unlock"
	currentPath := "/api/v2/workspaces/" + wsID + "/current-state-version"
	declarePath := "/api/v2/workspaces/" + wsID + "/state-versions"

	var before stateDocument

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Name:   "current state before the fence",
		Method: http.MethodGet,
		Path:   currentPath,
		Body:   "",
		Want:   http.StatusOK,
		Check: func(body string) error {
			if err := json.Unmarshal([]byte(body), &before); err != nil {
				return fmt.Errorf("decode the current state: %w", err)
			}

			return nil
		},
		UserAgent: "",
	})

	// The stale job locks, then stops before its upload.
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Name:      "lock by the stale job",
		Method:    http.MethodPost,
		Path:      lockPath,
		Body:      `{"data":{}}`,
		Want:      http.StatusOK,
		Check:     nil,
		UserAgent: "",
	})

	// Job B force-unlocks with the real CLI. The release writes the fence.
	env.Step(ctx, t, e2e.StepSpec{
		Check:  nil,
		Name:   "force-unlock by job B",
		Args:   []string{"force-unlock", "-force", e2e.Namespace + "/" + e2e.Workspace},
		Env:    nil,
		Stdin:  "",
		Fail:   false,
		Redact: false,
	})

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Name:   "fence is current with a new lineage and the next serial",
		Method: http.MethodGet,
		Path:   currentPath,
		Body:   "",
		Want:   http.StatusOK,
		Check: func(body string) error {
			var after stateDocument
			if err := json.Unmarshal([]byte(body), &after); err != nil {
				return fmt.Errorf("decode the fenced state: %w", err)
			}

			if after.Data.Attributes.Lineage == before.Data.Attributes.Lineage {
				return fmt.Errorf("%w: lineage did not change at the fence", errUnexpected)
			}

			if after.Data.Attributes.Serial != before.Data.Attributes.Serial+1 {
				return fmt.Errorf("%w: serial is not one more than before the fence", errUnexpected)
			}

			return nil
		},
		UserAgent: "",
	})

	// Job B writes with the real CLI after the fence: it reads the fenced
	// state and its write carries the new lineage.
	env.Step(ctx, t, e2e.StepSpec{
		Check:  nil,
		Name:   "apply by job B after the fence",
		Args:   []string{"apply", "-input=false", "-auto-approve", "-no-color"},
		Env:    nil,
		Stdin:  "",
		Fail:   false,
		Redact: false,
	})

	// Job B takes the lock next. The stale job's declare names the old
	// lineage, so the server refuses it with the lineage error (422).
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Name:      "lock by job B after the fence",
		Method:    http.MethodPost,
		Path:      lockPath,
		Body:      `{"data":{}}`,
		Want:      http.StatusOK,
		Check:     nil,
		UserAgent: "",
	})

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Name:   "stale job declares with the old lineage",
		Method: http.MethodPost,
		Path:   declarePath,
		Body: `{"data":{"type":"state-versions","attributes":{"serial":` +
			strconv.FormatInt(before.Data.Attributes.Serial+1, 10) +
			`,"lineage":"` + before.Data.Attributes.Lineage + `","md5":"` + emptyMD5 + `"}}}`,
		Want:      http.StatusUnprocessableEntity,
		Check:     nil,
		UserAgent: "",
	})

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Name:      "unlock by job B",
		Method:    http.MethodPost,
		Path:      unlockPath,
		Body:      "",
		Want:      http.StatusOK,
		Check:     nil,
		UserAgent: "",
	})
}
