//go:build e2e

package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// bulkLockBase is the path of the bulk lock routes of the e2e namespace.
const bulkLockBase = "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/unit-locks"

// codeAlreadyLocked is the error code of a unit that already has a manual lock.
const codeAlreadyLocked = "lock.already-locked"

// bulkUnitList is the part of the unit list answer that the bulk steps read.
type bulkUnitList struct {
	Units []bulkUnitRow `json:"units"`
}

// bulkUnitRow is one unit of the unit list.
type bulkUnitRow struct {
	Reference string `json:"reference"`
	UnitID    string `json:"unitID"`
}

// bulkAnswer is the answer of a bulk lock or a bulk unlock call.
type bulkAnswer struct {
	Results []bulkItemResult `json:"results"`
}

// bulkItemResult is the result of one unit of a bulk call.
type bulkItemResult struct {
	ID     string         `json:"id"`
	Status string         `json:"status"`
	Lock   *bulkLockInfo  `json:"lock"`
	Error  *bulkErrorInfo `json:"error"`
}

// bulkLockInfo is the lock of a result that locked a unit.
type bulkLockInfo struct {
	Kind   string `json:"kind"`
	Holder string `json:"holder"`
	Reason string `json:"reason"`
}

// bulkErrorInfo is the error of a result that failed.
type bulkErrorInfo struct {
	Code      int    `json:"code"`
	ErrorCode string `json:"errorCode"`
}

// bulkBody returns the JSON body of a bulk call for the unit IDs. An empty
// reason gives the body of a bulk unlock.
func bulkBody(reason string, ids []string) string {
	type item struct {
		ID string `json:"id"`
	}

	units := make([]item, 0, len(ids))
	for _, id := range ids {
		units = append(units, item{ID: id})
	}

	body := map[string]any{"units": units}
	if reason != "" {
		body["reason"] = reason
	}

	raw, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}

	return string(raw)
}

// checkBulkResults decodes the answer and checks it with want, which gets the
// results in the order of the request.
func checkBulkResults(n int, want func(a bulkAnswer) error) func(string) error {
	return func(body string) error {
		var a bulkAnswer
		if err := json.Unmarshal([]byte(body), &a); err != nil {
			return fmt.Errorf("%w: bulk answer is no JSON: %s", errUnexpected, body)
		}

		if len(a.Results) != n {
			return fmt.Errorf("%w: %d results in %s, want %d", errUnexpected, len(a.Results), body, n)
		}

		return want(a)
	}
}

// TestManualLockBulk proves, on each store, the two bulk calls of the manual
// locks guide, in the order of the page: lock three units while one already has
// a lock, read one lock through the single route, and unlock the units.
// OpenTofu fails on the workspace that the bulk call locked, and passes on the
// same workspace after the bulk unlock.
func TestManualLockBulk(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skipf("the combined suite needs Linux, not %s", runtime.GOOS)
	}

	fetcher := sharedFetcher(t)

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runManualLockBulkOnStore(t, fetcher, db)
		})
	}
}

func runManualLockBulkOnStore(t *testing.T, fetcher *e2e.Fetcher, store e2e.Store) {
	t.Helper()

	lab := newLockE2E(t, fetcher, store)
	ctx, env := t.Context(), lab.env

	ids, otherRef := bulkUnitIDs(t, env)

	// A lock of the same person on the third unit, so that the bulk call has a
	// mixed result.
	lockCall(t, env, actorA, collectionCall{
		name: "lock the third unit alone", method: http.MethodPut, path: unitLockPath(otherRef),
		body: lockBody("earlier lock"), want: http.StatusOK, check: nil,
	})

	lockCall(t, env, actorA, collectionCall{
		name: "bulk-lock three units", method: http.MethodPost, path: bulkLockBase + "/bulk-lock",
		body: bulkBody(lockReasonText, ids), want: http.StatusOK,
		check: checkBulkResults(len(ids), checkBulkLocked(ids)),
	})

	// The single lock route shows the same holder and reason.
	lockCall(t, env, actorA, collectionCall{
		name: "read the lock of the workspace", method: http.MethodGet, path: unitLockPath(tofuUnitRef()),
		body: "", want: http.StatusOK,
		check: func(body string) error {
			for _, want := range []string{`"holder":"` + e2e.Subject + `"`, `"reason":"` + lockReasonText + `"`} {
				if err := collectionHolds(want)(body); err != nil {
					return err
				}
			}

			return nil
		},
	})

	// OpenTofu fails on the workspace that the bulk call locked.
	env.SetCLI(lab.tofuCLI)

	stepName := "tofu plan under the bulk lock"
	env.Step(ctx, t, e2e.StepSpec{
		Check: checkPlanRefusedByManualLock(env, stepName), Name: stepName,
		Args: []string{"plan", "-input=false", "-no-color", "-lock-timeout=0s"},
		Env:  nil, Stdin: "", Fail: true, Redact: false,
	})

	lockCall(t, env, actorA, collectionCall{
		name: "bulk-unlock three units", method: http.MethodPost, path: bulkLockBase + "/bulk-unlock",
		body: bulkBody("", ids), want: http.StatusOK,
		check: checkBulkResults(len(ids), checkBulkUnlocked),
	})

	lockCall(t, env, actorA, collectionCall{
		name: "the lock of the workspace is gone", method: http.MethodGet, path: unitLockPath(tofuUnitRef()),
		body: "", want: http.StatusNotFound, check: nil,
	})

	env.Step(ctx, t, step("tofu plan after the bulk unlock", nil, "plan", "-input=false", "-no-color"))
}

// bulkUnitIDs reads the unit list and returns the IDs of the stack, the
// workspace and a third unit, and the reference of the third unit. The lab has the
// workspace, the stack and a second stack.
func bulkUnitIDs(t *testing.T, env *e2e.Env) ([]string, string) {
	t.Helper()

	var list bulkUnitList

	lockCall(t, env, actorA, collectionCall{
		name: "read the unit IDs", method: http.MethodGet,
		path: "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/units", body: "", want: http.StatusOK,
		check: func(body string) error {
			if err := json.Unmarshal([]byte(body), &list); err != nil {
				return fmt.Errorf("%w: unit list is no JSON: %s", errUnexpected, body)
			}

			return nil
		},
	})

	var workspaceID, stackID, otherID, otherRef string

	for _, u := range list.Units {
		switch u.Reference {
		case tofuUnitRef():
			workspaceID = u.UnitID
		case pulumiUnitRef():
			stackID = u.UnitID
		default:
			otherID, otherRef = u.UnitID, u.Reference
		}
	}

	if workspaceID == "" || stackID == "" || otherID == "" {
		t.Fatalf("the unit list lacks a unit: workspace %q, stack %q, other %q", workspaceID, stackID, otherID)
	}

	return []string{stackID, workspaceID, otherID}, otherRef
}

// checkBulkLocked checks the answer of the bulk lock: the first two units have
// the new lock, and the third one, which was locked before, has
// lock.already-locked.
func checkBulkLocked(ids []string) func(a bulkAnswer) error {
	return func(a bulkAnswer) error {
		for i, r := range a.Results {
			if r.ID != ids[i] {
				return fmt.Errorf("%w: result %d is for %q, want %q", errUnexpected, i, r.ID, ids[i])
			}
		}

		for _, r := range a.Results[:2] {
			if r.Status != "ok" || r.Lock == nil || r.Lock.Holder != e2e.Subject || r.Lock.Reason != lockReasonText {
				return fmt.Errorf("%w: result of %q is not the new lock: %+v", errUnexpected, r.ID, r)
			}
		}

		last := a.Results[2]
		if last.Status != "error" || last.Error == nil || last.Error.ErrorCode != codeAlreadyLocked ||
			last.Error.Code != http.StatusConflict {
			return fmt.Errorf("%w: third result is not %s: %+v", errUnexpected, codeAlreadyLocked, last)
		}

		return nil
	}
}

// checkBulkUnlocked checks that the bulk unlock removed each lock.
func checkBulkUnlocked(a bulkAnswer) error {
	for _, r := range a.Results {
		if r.Status != "ok" || r.Error != nil {
			return fmt.Errorf("%w: unlock result of %q is not ok: %+v", errUnexpected, r.ID, r)
		}
	}

	return nil
}
