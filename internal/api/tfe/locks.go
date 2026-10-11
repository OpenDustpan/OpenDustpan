package tfe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// lockRequestData holds the data object of a lock body.
type lockRequestData struct {
	Attributes lockRequestAttributes `json:"attributes"`
}

// lockRequestAttributes holds the reason in a lock body.
type lockRequestAttributes struct {
	Reason string `json:"reason"`
}

// lockRequestBody is the body of a lock request: the reason in
// data.attributes. Every other field is ignored. An empty or malformed body
// locks with an empty reason.
type lockRequestBody struct {
	Data lockRequestData `json:"data"`
}

// lockReason reads the reason from the lock request body, or "" when the body
// holds none.
func lockReason(body []byte) string {
	if len(body) == 0 {
		return ""
	}

	var req lockRequestBody
	if err := json.Unmarshal(body, &req); err != nil {
		return ""
	}

	return req.Data.Attributes.Reason
}

// lockConflictDetail reports a held lock in the words go-tfe parses: the
// phrase "is locked by User" followed by the holder.
func lockConflictDetail(id workspace.ID, holder string) string {
	return fmt.Sprintf("Workspace %s is locked by User %s", id, holder)
}

// lockHolder returns the holder of the stored workspace, or "" when the
// workspace is missing or unlocked.
func (h *Handler) lockHolder(ctx context.Context, id workspace.ID) string {
	ws, err := h.workspaces.GetByID(ctx, id)
	if err != nil || !ws.Lock.Locked() {
		return ""
	}

	return ws.Lock.Holder
}

// lockWorkspace serves POST /api/v2/workspaces/{id}/actions/lock.
//
// The protocol carries no lock ID from the client, so CI jobs that share one
// workspace need distinct principals. The server mints the lock ID and
// records the subject of the caller as the holder. A lock on a held workspace
// answers 409 with a detail holding "is locked by User" and the holder. A
// lost compare-and-swap also answers so: the loser retries once, then reports
// the held lock, so the CLI waits and retries under -lock-timeout.
func (h *Handler) lockWorkspace(
	w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object,
) {
	ctx := r.Context()
	id := workspace.ID(r.PathValue("id"))

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		h.writeError(ctx, w, errInvalidBody)

		return
	}

	reason := lockReason(body)

	locked, err := h.workspaces.Lock(ctx, id, p.Subject, reason)
	if err != nil {
		if errors.Is(err, workspace.ErrLocked) {
			h.writeConflict(ctx, w, lockConflictDetail(id, h.lockHolder(ctx, id)))

			return
		}

		if errors.Is(err, workspace.ErrConcurrentModification) {
			h.retryLock(ctx, w, id, p, reason)

			return
		}

		h.writeError(ctx, w, err)

		return
	}

	h.writeWorkspace(ctx, w, http.StatusOK, p, locked)
}

// retryLock retries one lock after a lost compare-and-swap. A win answers
// 200 with the workspace. A further conflict answers 409 with a detail
// holding "is locked by User" and the holder, so the CLI waits and retries
// under -lock-timeout.
func (h *Handler) retryLock(
	ctx context.Context, w http.ResponseWriter, id workspace.ID, p identity.Principal, reason string,
) {
	locked, err := h.workspaces.Lock(ctx, id, p.Subject, reason)
	if err == nil {
		h.writeWorkspace(ctx, w, http.StatusOK, p, locked)

		return
	}

	if errors.Is(err, workspace.ErrLocked) || errors.Is(err, workspace.ErrConcurrentModification) {
		h.writeConflict(ctx, w, lockConflictDetail(id, h.lockHolder(ctx, id)))

		return
	}

	h.writeError(ctx, w, err)
}

// unlockWorkspace serves POST /api/v2/workspaces/{id}/actions/unlock. The
// holder unlocks. An unlock by another principal answers 409 with a detail
// holding "is locked by User" and the holder.
func (h *Handler) unlockWorkspace(
	w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object,
) {
	ctx := r.Context()
	id := workspace.ID(r.PathValue("id"))

	unlocked, err := h.workspaces.Unlock(ctx, id, p.Subject)
	if err != nil {
		if errors.Is(err, workspace.ErrLockedByOther) {
			h.writeConflict(ctx, w, lockConflictDetail(id, h.lockHolder(ctx, id)))

			return
		}

		h.writeError(ctx, w, err)

		return
	}

	h.writeWorkspace(ctx, w, http.StatusOK, p, unlocked)
}

// forceUnlockWorkspace serves POST
// /api/v2/workspaces/{id}/actions/force-unlock. It releases the lock whoever
// holds it. A force-unlock of an unlocked workspace answers 409. A success
// writes one info log line with the action, the unit, the principal and the
// released holder, and never with the request path or query (ADR 28 decision
// 10, ADR 32 decision 7). The handler owns the logger through its Config.
func (h *Handler) forceUnlockWorkspace(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()
	id := workspace.ID(r.PathValue("id"))

	holder := h.lockHolder(ctx, id)

	unlocked, err := h.workspaces.ForceUnlock(ctx, id)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.logger.InfoContext(
		ctx,
		"force-unlock completed",
		slog.String("action", "force-unlock"),
		slog.String("object", obj.String()),
		slog.String("principal", p.Subject),
		slog.String("released", holder),
	)

	h.writeWorkspace(ctx, w, http.StatusOK, p, unlocked)
}
