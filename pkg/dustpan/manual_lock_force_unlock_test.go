package dustpan_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/workspace"
)

// manualLockReason is the reason that every row puts on its manual lock. A
// refused request must name it, which proves that the manual lock is still in
// place after the force-unlock.
const manualLockReason = "freeze"

// forceUnlockTool is one tool with a force-unlock. hold takes a tool lock on
// the unit and returns the ID that forceUnlock names. forceUnlock sends the
// tool's force-unlock. refuse sends the request that a unit under a manual lock
// refuses, and returns its answer.
type forceUnlockTool struct {
	name        string
	unit        string
	refusedWith int
	hold        func(t *testing.T, f fixture) string
	forceUnlock func(t *testing.T, f fixture, lockID string)
	refuse      func(t *testing.T, f fixture) (int, string)
}

// manualLockTarget is one object that a manual lock covers. lock puts the
// manual lock on the target of unit.
type manualLockTarget struct {
	name string
	lock func(t *testing.T, f fixture, unit string)
}

// TestForceUnlockKeepsManualLock proves that a tool force-unlock never removes
// a manual lock. Each row takes a tool lock on a unit, puts a manual lock on the
// unit, a collection or the namespace, runs the force-unlock of the tool, and
// then checks that the unit is still refused with the reason of the manual lock.
func TestForceUnlockKeepsManualLock(t *testing.T) {
	t.Parallel()

	tools := []forceUnlockTool{tofuTFEForceUnlock(), tofuHTTPForceUnlock(), pulumiCancelUpdate()}
	targets := []manualLockTarget{unitManualLock(), collectionManualLock(), namespaceManualLock()}

	for _, tool := range tools {
		for _, target := range targets {
			t.Run(tool.name+" under a manual lock on the "+target.name, func(t *testing.T) {
				t.Parallel()

				f := newFixture(t)
				f.createNamespace(t, "acme", region)

				lockID := tool.hold(t, f)
				target.lock(t, f, tool.unit)
				tool.forceUnlock(t, f, lockID)

				status, body := tool.refuse(t, f)
				if status != tool.refusedWith {
					t.Fatalf("request after the force-unlock = %d, want %d: %s", status, tool.refusedWith, body)
				}

				if !strings.Contains(body, manualLockReason) {
					t.Fatalf("refusal %q does not name the manual lock reason %q", body, manualLockReason)
				}
			})
		}
	}
}

// tofuTFEForceUnlock is the force-unlock of the TFE-compatible API. The
// OpenTofu LOCK takes the tool lock.
func tofuTFEForceUnlock() forceUnlockTool {
	return forceUnlockTool{
		name:        "TFE force-unlock",
		unit:        "opentofu:acme/web",
		refusedWith: http.StatusLocked,
		hold: func(t *testing.T, f fixture) string {
			t.Helper()

			if code, body := tofuDo(t, f, "LOCK", tofuLockBody); code != http.StatusOK {
				t.Fatalf("LOCK = %d, want 200: %s", code, body)
			}

			return ""
		},
		forceUnlock: func(t *testing.T, f fixture, _ string) {
			t.Helper()

			path := "/api/v2/workspaces/" + webWorkspace(t, f).ID.String() + "/actions/force-unlock"

			status, body := doWithAuth(t, f, authRequest{
				method: http.MethodPost, path: path, auth: "Bearer " + adminToken, body: "",
			})
			if status != http.StatusOK {
				t.Fatalf("force-unlock = %d, want 200: %s", status, body)
			}
		},
		refuse: func(t *testing.T, f fixture) (int, string) {
			t.Helper()

			return tofuDo(t, f, "LOCK", tofuLockBody)
		},
	}
}

// tofuHTTPForceUnlock is the forced UNLOCK of the OpenTofu http backend. Another
// principal holds the tool lock, so the UNLOCK by the admin is a force-unlock.
func tofuHTTPForceUnlock() forceUnlockTool {
	return forceUnlockTool{
		name:        "HTTP backend force-unlock",
		unit:        "opentofu:acme/web",
		refusedWith: http.StatusLocked,
		hold: func(t *testing.T, f fixture) string {
			t.Helper()

			name, err := workspace.ParseName("web")
			if err != nil {
				t.Fatalf("parse workspace name: %v", err)
			}

			svc := f.srv.Workspaces()

			w, err := svc.Create(t.Context(), workspace.Ref{Namespace: "acme", Name: name})
			if err != nil {
				t.Fatalf("create workspace: %v", err)
			}

			locked, err := svc.Lock(t.Context(), w.ID, "ci", "apply")
			if err != nil {
				t.Fatalf("lock workspace: %v", err)
			}

			return locked.Lock.ID
		},
		forceUnlock: func(t *testing.T, f fixture, lockID string) {
			t.Helper()

			body := `{"ID":"` + lockID + `"}`
			if code, got := tofuDo(t, f, "UNLOCK", body); code != http.StatusOK {
				t.Fatalf("forced UNLOCK = %d, want 200: %s", code, got)
			}
		},
		refuse: func(t *testing.T, f fixture) (int, string) {
			t.Helper()

			return tofuDo(t, f, "LOCK", tofuLockBody)
		},
	}
}

// pulumiCancelUpdate is `pulumi cancel` of a running update. The update holds
// the stack, and cancel ends it without its lease.
func pulumiCancelUpdate() forceUnlockTool {
	const stackPath = "/api/stacks/acme/web/prod/update"

	return forceUnlockTool{
		name:        "Pulumi cancel",
		unit:        "pulumi:acme/web/prod",
		refusedWith: http.StatusConflict,
		hold: func(t *testing.T, f fixture) string {
			t.Helper()

			mustDo(t, f, call{
				method: http.MethodPost, path: "/api/stacks/acme/web", body: `{"stackName":"prod"}`,
			}, http.StatusOK)

			updateID := createPulumiUpdate(t, f, stackPath)
			mustDo(t, f, call{
				method: http.MethodPost, path: stackPath + "/" + updateID, body: `{}`,
			}, http.StatusOK)

			return updateID
		},
		forceUnlock: func(t *testing.T, f fixture, updateID string) {
			t.Helper()

			mustDo(t, f, call{
				method: http.MethodPost, path: stackPath + "/" + updateID + "/cancel", body: "",
			}, http.StatusOK)
		},
		refuse: func(t *testing.T, f fixture) (int, string) {
			t.Helper()

			// A manual lock refuses the start of an update, not its creation.
			updateID := createPulumiUpdate(t, f, stackPath)

			return f.do(t, http.MethodPost, stackPath+"/"+updateID, adminToken, `{}`)
		},
	}
}

// unitManualLock puts the manual lock on the unit.
func unitManualLock() manualLockTarget {
	return manualLockTarget{
		name: "unit",
		lock: func(t *testing.T, f fixture, unit string) {
			t.Helper()

			mustDo(t, f, call{
				method: http.MethodPut,
				path:   "/api/inventory/unit-lock?unit=" + unit,
				body:   `{"reason":"` + manualLockReason + `"}`,
			}, http.StatusOK)
		},
	}
}

// collectionManualLock puts the unit in the collection payments and puts the
// manual lock on that collection.
func collectionManualLock() manualLockTarget {
	return manualLockTarget{
		name: "collection",
		lock: func(t *testing.T, f fixture, unit string) {
			t.Helper()

			mustDo(t, f, call{
				method: http.MethodPost,
				path:   "/api/dustpan/v1/namespaces/acme/collections",
				body:   `{"name":"payments"}`,
			}, http.StatusCreated)
			mustDo(t, f, call{
				method: http.MethodPut,
				path:   "/api/inventory/unit-collection?unit=" + unit,
				body:   `{"collection":"payments"}`,
			}, http.StatusOK)
			mustDo(t, f, call{
				method: http.MethodPut,
				path:   "/api/dustpan/v1/namespaces/acme/collections/payments/lock",
				body:   `{"reason":"` + manualLockReason + `"}`,
			}, http.StatusOK)
		},
	}
}

// namespaceManualLock puts the manual lock on the whole namespace acme.
func namespaceManualLock() manualLockTarget {
	return manualLockTarget{
		name: "namespace",
		lock: func(t *testing.T, f fixture, _ string) {
			t.Helper()

			lockNamespace(t, f)
		},
	}
}

// webWorkspace returns the workspace acme/web.
func webWorkspace(t *testing.T, f fixture) workspace.Workspace {
	t.Helper()

	name, err := workspace.ParseName("web")
	if err != nil {
		t.Fatalf("parse workspace name: %v", err)
	}

	ws, err := f.srv.Workspaces().Get(t.Context(), workspace.Ref{Namespace: "acme", Name: name})
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}

	return ws
}

// createPulumiUpdate creates an update of acme/web/prod at stackPath and
// returns its ID.
func createPulumiUpdate(t *testing.T, f fixture, stackPath string) string {
	t.Helper()

	var created struct {
		UpdateID string `json:"updateID"`
	}

	status, body := f.do(t, http.MethodPost, stackPath, adminToken, `{}`)
	if status != http.StatusOK || json.Unmarshal([]byte(body), &created) != nil || created.UpdateID == "" {
		t.Fatalf("create update: status %d, body %s", status, body)
	}

	return created.UpdateID
}
