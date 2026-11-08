package dustpan_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
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
				f.createNamespace(t, "acme", installationName)

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

			seedWebWorkspace(t, f)

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

			w, err := svc.Create(
				t.Context(), workspace.Ref{Namespace: "acme", Name: name}, placeWorkspace(t, f.store, "acme"),
			)
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

// forceUnlockNow returns the fixed time of the release tests.
func forceUnlockNow() time.Time {
	return time.Date(2026, time.October, 8, 21, 0, 0, 0, time.UTC)
}

// releasePath is one write path of a force-unlock. setup creates the workspace
// acme/web, writes the state of the path and takes its tool lock as the holder
// "admin". forceUnlock sends the force-unlock of the path and returns its
// status and body.
//
// The paths are the fenced release of a workspace with state (FenceAndUnlock),
// the unfenced release of a client-encrypted state (ReleaseUnfenced) and the
// release of a workspace without state (SaveReleased). Each store method has
// one caller, the force-unlock of its path.
type releasePath struct {
	name        string
	setup       func(t *testing.T, f fixture) workspace.Workspace
	forceUnlock func(t *testing.T, f fixture, w workspace.Workspace) (int, string)
}

// releasePaths returns the write paths of a force-unlock that the release
// tests cover.
func releasePaths() []releasePath {
	return []releasePath{
		{
			name: "fenced release",
			setup: func(t *testing.T, f fixture) workspace.Workspace {
				t.Helper()

				w := lockWebWorkspace(t, f, createWebWorkspace(t, f).ID)
				uploadWorkspaceState(t, f.srv.Workspaces(), w.ID, "{}")

				return w
			},
			forceUnlock: forceUnlockTFE,
		},
		{
			name: "unfenced release of a client-encrypted state",
			setup: func(t *testing.T, f fixture) workspace.Workspace {
				t.Helper()

				w := lockWebWorkspace(t, f, createWebWorkspace(t, f).ID)
				uploadWorkspaceState(t, f.srv.Workspaces(), w.ID,
					`{"meta":{},"encrypted_data":"c2VjcmV0","encryption_version":"v0"}`)
				allowForcedWrites(t, f, w)

				return w
			},
			forceUnlock: forceUnlockTFE,
		},
		{
			name: "release without state",
			setup: func(t *testing.T, f fixture) workspace.Workspace {
				t.Helper()

				return lockWebWorkspace(t, f, createWebWorkspace(t, f).ID)
			},
			forceUnlock: forceUnlockTFE,
		},
	}
}

// createWebWorkspace creates the workspace acme/web.
func createWebWorkspace(t *testing.T, f fixture) workspace.Workspace {
	t.Helper()

	name, err := workspace.ParseName("web")
	if err != nil {
		t.Fatalf("parse workspace name: %v", err)
	}

	w, err := f.srv.Workspaces().Create(
		t.Context(), workspace.Ref{Namespace: "acme", Name: name}, placeWorkspace(t, f.store, "acme"),
	)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	return w
}

// lockWebWorkspace takes the tool lock of the workspace id as the holder
// "admin", so a later state upload under that lock is allowed.
func lockWebWorkspace(t *testing.T, f fixture, id workspace.ID) workspace.Workspace {
	t.Helper()

	locked, err := f.srv.Workspaces().Lock(t.Context(), id, "admin", "apply")
	if err != nil {
		t.Fatalf("lock workspace: %v", err)
	}

	return locked
}

// allowForcedWrites turns the forced switch on for the namespace acme and for
// the workspace w, so a force-unlock may release a client-encrypted state
// without a fence. The tool-neutral API names the switches in camel case, the
// TFE-compatible API in the names of its own body.
func allowForcedWrites(t *testing.T, f fixture, w workspace.Workspace) {
	t.Helper()

	changes := []struct {
		path string
		body string
	}{
		{
			path: "/api/server/namespaces/acme/override-switches",
			body: `{"allowForcedWrites":true,"allowUnlockedWrites":false}`,
		},
		{
			path: "/api/v2/workspaces/" + w.ID.String() + "/override-switches",
			body: `{"allow-forced-writes":true,"allow-unlocked-writes":false}`,
		},
	}

	for _, change := range changes {
		status, body := doWithAuth(t, f, authRequest{
			method: http.MethodPut, path: change.path, auth: "Bearer " + adminToken, body: change.body,
		})
		if status != http.StatusOK {
			t.Fatalf("PUT %s = %d, want 200: %s", change.path, status, body)
		}
	}
}

// forceUnlockTFE sends the force-unlock of the workspace w through the
// TFE-compatible API and returns its status and body.
func forceUnlockTFE(t *testing.T, f fixture, w workspace.Workspace) (int, string) {
	t.Helper()

	return doWithAuth(t, f, authRequest{
		method: http.MethodPost,
		path:   "/api/v2/workspaces/" + w.ID.String() + "/actions/force-unlock",
		auth:   "Bearer " + adminToken,
		body:   "",
	})
}

// workspaceByID returns the stored workspace with the ID.
func workspaceByID(t *testing.T, f fixture, id workspace.ID) workspace.Workspace {
	t.Helper()

	w, err := f.srv.Workspaces().GetByID(t.Context(), id)
	if err != nil {
		t.Fatalf("get workspace %s: %v", id, err)
	}

	return w
}

// currentStateID returns the ID of the current state version of the workspace
// id, or the empty ID when the workspace has no state.
func currentStateID(t *testing.T, f fixture, id workspace.ID) workspace.StateVersionID {
	t.Helper()

	current, err := f.srv.Workspaces().Current(t.Context(), id)
	switch {
	case err == nil:
		return current.ID
	case errors.Is(err, workspace.ErrNoState):
		return ""
	default:
		t.Fatalf("current state of %s: %v", id, err)

		return ""
	}
}

// TestForceUnlockReleaseKeepsManualLock proves that every write path of a
// force-unlock keeps a manual lock on the unit. Each row puts the manual lock
// on the unit after the tool lock, runs the force-unlock of the path, and then
// checks that the unit is still refused with the reason of the manual lock.
func TestForceUnlockReleaseKeepsManualLock(t *testing.T) {
	t.Parallel()

	for _, path := range releasePaths() {
		t.Run(path.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			f.createNamespace(t, "acme", installationName)

			w := path.setup(t, f)
			unitManualLock().lock(t, f, "opentofu:acme/web")

			if status, body := path.forceUnlock(t, f, w); status != http.StatusOK {
				t.Fatalf("force-unlock = %d, want 200: %s", status, body)
			}

			status, body := tofuDo(t, f, "LOCK", tofuLockBody)
			if status != http.StatusLocked {
				t.Fatalf("LOCK after the force-unlock = %d, want 423: %s", status, body)
			}

			if !strings.Contains(body, manualLockReason) {
				t.Fatalf("refusal %q does not name the manual lock reason %q", body, manualLockReason)
			}
		})
	}
}

// movingFenceStore moves the failover version of every namespace once, just
// before the first release write of a force-unlock: FenceAndUnlock,
// ReleaseUnfenced or SaveReleased. The write then meets a fence that is no
// longer current.
type movingFenceStore struct {
	*sqlite.Store

	// move moves the failover version once. A nil move keeps the fence.
	move func()
}

// FenceAndUnlock implements workspace.StateStore. It moves the fence, then
// releases the workspace through the wrapped store.
//
//nolint:revive // The signature of the workspace.StateStore port.
func (s *movingFenceStore) FenceAndUnlock(
	ctx context.Context, w workspace.Workspace, sv workspace.StateVersion, expect workspace.StateVersionID,
	fence namespace.Fence, e audit.Entry,
) error {
	s.moveFence()

	return s.Store.FenceAndUnlock(ctx, w, sv, expect, fence, e)
}

// ReleaseUnfenced implements workspace.StateStore. It moves the fence, then
// saves the released workspace through the wrapped store.
func (s *movingFenceStore) ReleaseUnfenced(
	ctx context.Context, w workspace.Workspace, fence namespace.Fence, e audit.Entry,
) error {
	s.moveFence()

	return s.Store.ReleaseUnfenced(ctx, w, fence, e)
}

// SaveReleased implements workspace.StateStore. It moves the fence, then
// saves the released workspace through the wrapped store.
func (s *movingFenceStore) SaveReleased(
	ctx context.Context, w workspace.Workspace, fence namespace.Fence, e audit.Entry,
) error {
	s.moveFence()

	return s.Store.SaveReleased(ctx, w, fence, e)
}

// moveFence runs the move of the store.
func (s *movingFenceStore) moveFence() {
	if s.move != nil {
		s.move()
	}
}

// newMovingFenceFixture starts a server over a store that moves the failover
// version of every namespace once, just before the first release write of a
// force-unlock. The release then meets a fence that is no longer current.
func newMovingFenceFixture(t *testing.T) fixture {
	t.Helper()

	st, blobs, authn := requireServerPorts(t)

	var once sync.Once

	moving := &movingFenceStore{Store: st, move: func() {
		once.Do(func() {
			if _, err := st.BumpFailoverVersions(t.Context()); err != nil {
				t.Errorf("bump failover versions: %v", err)
			}
		})
	}}

	srv, err := dustpan.NewServer(append(
		fullServerOptions(t, st, blobs, authn),
		dustpan.WithWorkspaceStore(moving),
		dustpan.WithStateStore(moving),
		dustpan.WithClock(forceUnlockNow),
	)...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ts := httptest.NewServer(startedHandler(t, srv))
	t.Cleanup(ts.Close)

	return fixture{store: st, server: ts, srv: srv}
}

// TestForceUnlockRefusedWhenTheFenceMoved proves that every write path of a
// force-unlock checks the fence of the release in the store transaction of its
// write. The store moves the failover version of every namespace just before
// the release write, so the release meets a fence that is no longer current.
// The force-unlock must be refused with the fence sentinel and must write
// nothing: the workspace row and the current state version stay as they were.
func TestForceUnlockRefusedWhenTheFenceMoved(t *testing.T) {
	t.Parallel()

	for _, path := range releasePaths() {
		t.Run(path.name, func(t *testing.T) {
			t.Parallel()

			f := newMovingFenceFixture(t)
			f.createNamespace(t, "acme", installationName)

			w := path.setup(t, f)
			unitManualLock().lock(t, f, "opentofu:acme/web")

			before := workspaceByID(t, f, w.ID)
			beforeState := currentStateID(t, f, w.ID)

			status, body := path.forceUnlock(t, f, w)
			if status != http.StatusConflict {
				t.Fatalf("force-unlock after the fence moved = %d, want 409: %s", status, body)
			}

			if !strings.Contains(body, namespace.ErrFenced.Error()) {
				t.Fatalf("refusal %q does not name the fence sentinel %q", body, namespace.ErrFenced.Error())
			}

			if after := workspaceByID(t, f, w.ID); after != before {
				t.Fatalf("workspace after the refused force-unlock = %+v, want %+v", after, before)
			}

			if afterState := currentStateID(t, f, w.ID); afterState != beforeState {
				t.Fatalf("current state after the refused force-unlock = %s, want %s", afterState, beforeState)
			}
		})
	}
}
