package tofuhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// maxLockBodyBytes bounds a LOCK or UNLOCK body. Lock info is a few hundred
// bytes; the bound keeps a large body from filling memory.
const maxLockBodyBytes = 64 << 10

// lockInfo is the lock info of the OpenTofu backend: the body of a LOCK and an
// UNLOCK, and the body of a 423 that names the current lock.
//
//nolint:tagliatelle // The OpenTofu http backend sends and reads these PascalCase keys.
type lockInfo struct {
	ID        string `json:"ID"`
	Operation string `json:"Operation"`
	Info      string `json:"Info"`
	Who       string `json:"Who"`
	Version   string `json:"Version"`
	Created   string `json:"Created"`
	Path      string `json:"Path"`
}

// lockRoutes returns the lock rows of the address. LOCK and UNLOCK need
// unit:write. LOCK also needs unit:manage to create a workspace that does not
// exist; the handler checks it, because only the create needs it.
func (h *Handler) lockRoutes() []Route {
	return []Route{
		{
			Method:  "LOCK",
			Path:    statePath,
			Actions: []identity.Action{identity.ActionUnitWrite},
			Serve:   h.lockWorkspace,
		},
		{
			Method:  "UNLOCK",
			Path:    statePath,
			Actions: []identity.Action{identity.ActionUnitWrite},
			Serve:   h.unlockWorkspace,
		},
	}
}

// readLockInfo reads the lock info of a LOCK or UNLOCK body. The ID is required:
// it names the lock in each later write.
func readLockInfo(w http.ResponseWriter, r *http.Request) (lockInfo, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxLockBodyBytes))
	if err != nil {
		return lockInfo{}, fmt.Errorf("%w: lock body", workspace.ErrInvalidLockID)
	}

	var info lockInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return lockInfo{}, fmt.Errorf("%w: lock body", workspace.ErrInvalidLockID)
	}

	if info.ID == "" {
		return lockInfo{}, fmt.Errorf("%w: lock body without an ID", workspace.ErrInvalidLockID)
	}

	return info, nil
}

// lockInfoOf returns the lock info of a held lock, for a 423 body.
func lockInfoOf(l workspace.Lock) lockInfo {
	return lockInfo{
		ID:        l.ID,
		Operation: "",
		Info:      l.Reason,
		Who:       l.Holder,
		Version:   "",
		Created:   l.LockedAt.Format(time.RFC3339),
		Path:      "",
	}
}

// writeLocked answers 423 with the lock info of the current lock, which the
// backend shows to the user (ADR 49, decision 6).
func (h *Handler) writeLocked(w http.ResponseWriter, held workspace.Lock) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusLocked)

	if err := json.NewEncoder(w).Encode(lockInfoOf(held)); err != nil {
		h.logger.Warn("write lock info", slog.String("error", err.Error()))
	}
}

// lockWorkspace serves LOCK. It creates the workspace on first use, after the
// caller holds unit:manage on it (ADR 49, decision 5). A lock held by someone
// else answers 423 with the current lock info.
func (h *Handler) lockWorkspace(w http.ResponseWriter, r *http.Request, c call) {
	ctx := r.Context()

	info, err := readLockInfo(w, r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	ws, err := h.ws.Get(ctx, c.ref)
	if errors.Is(err, workspace.ErrNotFound) {
		ws, err = h.createWorkspace(ctx, c)
	}

	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	_, err = h.ws.LockWithID(ctx, ws.ID, c.principal.Subject, info.Info, info.ID)
	if errors.Is(err, workspace.ErrLocked) || errors.Is(err, workspace.ErrConcurrentModification) {
		h.writeHeld(ctx, w, c, err)

		return
	}

	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.WriteHeader(http.StatusOK)
}

// writeHeld answers a refused LOCK with the lock that holds the workspace now.
// A lock that cannot be read answers with the refusal.
func (h *Handler) writeHeld(ctx context.Context, w http.ResponseWriter, c call, refusal error) {
	current, err := h.ws.Get(ctx, c.ref)
	if err != nil || !current.Lock.Locked() {
		h.writeError(ctx, w, refusal)

		return
	}

	h.writeLocked(w, current.Lock)
}

// createWorkspace creates the workspace of the path for a LOCK, after the caller
// holds unit:manage on its unit.
func (h *Handler) createWorkspace(ctx context.Context, c call) (workspace.Workspace, error) {
	obj, err := unitObject(c.ref)
	if err != nil {
		return workspace.Workspace{}, err
	}

	if authErr := h.authz.Authorize(ctx, c.principal, identity.ActionUnitManage, obj); authErr != nil {
		return workspace.Workspace{}, fmt.Errorf("authorize %s: %w", identity.ActionUnitManage, authErr)
	}

	return h.ws.Create(ctx, c.ref)
}

// unlockWorkspace serves UNLOCK. The lock ID of the body must be the current
// lock. An UNLOCK that names another lock, or a lock held by another principal,
// answers 409 with the current lock info. Forced unlocks land with their e2e
// step.
func (h *Handler) unlockWorkspace(w http.ResponseWriter, r *http.Request, c call) {
	ctx := r.Context()

	info, err := readLockInfo(w, r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	ws, err := h.ws.Get(ctx, c.ref)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if !ws.Lock.Locked() || ws.Lock.ID != info.ID {
		h.writeConflict(w, ws.Lock)

		return
	}

	_, err = h.ws.Unlock(ctx, ws.ID, c.principal.Subject)
	if errors.Is(err, workspace.ErrLockedByOther) || errors.Is(err, workspace.ErrNotLocked) {
		h.writeHeldOrConflict(ctx, w, c, err)

		return
	}

	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.WriteHeader(http.StatusOK)
}

// writeConflict answers 409 with the current lock info, or with no body when
// the workspace is unlocked.
func (h *Handler) writeConflict(w http.ResponseWriter, held workspace.Lock) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)

	if !held.Locked() {
		return
	}

	if err := json.NewEncoder(w).Encode(lockInfoOf(held)); err != nil {
		h.logger.Warn("write lock info", slog.String("error", err.Error()))
	}
}

// writeHeldOrConflict answers a refused UNLOCK with the lock that holds the
// workspace now, as 409.
func (h *Handler) writeHeldOrConflict(ctx context.Context, w http.ResponseWriter, c call, refusal error) {
	current, err := h.ws.Get(ctx, c.ref)
	if err != nil {
		h.writeError(ctx, w, refusal)

		return
	}

	h.writeConflict(w, current.Lock)
}
