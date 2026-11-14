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

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/tofuunit"
	"opendustpan.org/opendustpan/internal/bodygate"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
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

// manualLockInfo returns the lock info of a manual lock. The backend shows ID
// and Info to the user, so both say that a manual lock blocks the run and name
// the route that removes it, after plain words. The ID is not a tool lock ID:
// `tofu force-unlock` cannot use it.
func manualLockInfo(e *manuallock.LockedError) lockInfo {
	routes := refusal.RemovalRoutes(e.Lock.Target)

	return lockInfo{
		ID:        "A manual lock blocks this run. To remove it, send `DELETE " + routes.Remove + "`.",
		Operation: "manual lock",
		Info: e.Refusal() + ". `tofu force-unlock` does not remove a manual lock. " +
			"The holder sends `DELETE " + routes.Remove + "`. A person with unit:manage sends `POST " + routes.Force + "`.",
		Who:     manuallock.SafeText(e.Lock.Holder),
		Version: "",
		Created: e.Lock.Since.Format(time.RFC3339),
		Path:    "",
	}
}

// writeManualLocked answers 423 for a manual lock. The lock info names the
// holder in Who, and the unit, the holder and the reason in Info, which the
// backend shows to the user.
func (h *Handler) writeManualLocked(w http.ResponseWriter, e *manuallock.LockedError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusLocked)

	if err := json.NewEncoder(w).Encode(manualLockInfo(e)); err != nil {
		h.logger.Warn("write lock info", slog.String("error", err.Error()))
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
		h.writeLockError(ctx, w, err)

		return
	}

	req := workspace.LockRequest{
		Holder: c.principal.Subject, Reason: info.Info, LockID: info.ID, JobID: c.principal.JobTokenID(),
	}

	ws, err := h.workspaceOf(ctx, c, func(ctx context.Context) error {
		return h.ws.AdmitFirstLock(ctx, c.ref, req)
	})
	if err != nil {
		h.writeFirstRefusal(ctx, w, err, h.writeLockError)

		return
	}

	// The gates run at the lock, before any plan or apply: a refusal of the
	// state version after the apply would leave resources that no stored state
	// records. The workspace must exist first, because the gates read its
	// facts.
	if !h.guardStateWrite(ctx, w, c, stateCheck{action: identity.ActionUnitWrite, op: gate.OperationLock}) {
		return
	}

	_, err = h.ws.LockWithIDForJob(ctx, ws.ID, req)
	if errors.Is(err, workspace.ErrLocked) || errors.Is(err, workspace.ErrConcurrentModification) {
		h.writeHeld(ctx, w, c, err)

		return
	}

	if err != nil {
		h.writeLockError(ctx, w, err)

		return
	}

	w.WriteHeader(http.StatusOK)
}

// writeLockError answers a failed LOCK. A fenced LOCK answers 503 with a
// Retry-After time, not 409: OpenTofu reads a 409 on LOCK as a lock that
// another client holds, and shows lock info that does not exist. It retries a
// 503, and the retry reads the namespace again. Any other error answers as
// writeError does.
func (h *Handler) writeLockError(ctx context.Context, w http.ResponseWriter, err error) {
	if !errors.Is(err, namespace.ErrFenced) {
		h.writeError(ctx, w, err)

		return
	}

	h.logger.WarnContext(ctx, "write fenced", slog.String("error", err.Error()))
	bodygate.SetRetryAfterJitter(w)
	http.Error(w, namespace.ErrFenced.Error(), http.StatusServiceUnavailable)
}

// writeFirstRefusal answers a first LOCK or POST that workspaceOf refused. A
// manual lock of the namespace refuses it before the workspace exists, in the
// admit check or in the create. That refusal answers 423 with the lock info of
// the manual lock, as for a workspace that exists, because the backend reads
// the body of a 423 as lock info. Any other error answers with fallback.
func (h *Handler) writeFirstRefusal(
	ctx context.Context, w http.ResponseWriter, err error,
	fallback func(ctx context.Context, w http.ResponseWriter, err error),
) {
	if manual, ok := errors.AsType[*manuallock.LockedError](err); ok && manual != nil {
		h.writeManualLocked(w, manual)

		return
	}

	fallback(ctx, w, err)
}

// writeHeld answers a refused LOCK with the lock that holds the workspace now.
// A lock that cannot be read answers with the refusal.
func (h *Handler) writeHeld(ctx context.Context, w http.ResponseWriter, c call, refused error) {
	if manual, ok := errors.AsType[*manuallock.LockedError](refused); ok {
		h.writeManualLocked(w, manual)

		return
	}

	lock, err := h.refusingLock(ctx, c, refused)
	if err != nil || !lock.Locked() {
		h.writeError(ctx, w, refused)

		return
	}

	h.writeLocked(w, lock)
}

// refusingLock returns the lock that refused the call. The domain names that
// lock in its refusal, as the refusing revision held it. A refusal without
// one, such as a lost compare-and-swap of the store, reads the workspace
// again.
func (h *Handler) refusingLock(ctx context.Context, c call, refused error) (workspace.Lock, error) {
	if lock, ok := workspace.RefusingLock(refused); ok {
		return lock, nil
	}

	current, err := h.ws.Get(ctx, c.ref)
	if err != nil {
		return workspace.Lock{}, err
	}

	return current.Lock, nil
}

// workspaceOf returns the workspace of the call, creating it on first use. A
// first request creates the workspace only when the request is admitted: the
// caller holds unit:manage on the unit, and admit, which runs the checks of the
// request against a workspace that does not exist yet, returns nil. A refused
// request leaves no workspace. Two first requests can both find the workspace
// missing: the create that loses answers ErrAlreadyExists, and the workspace is
// read again, so it is not a 500.
func (h *Handler) workspaceOf(
	ctx context.Context, c call, admit func(ctx context.Context) error,
) (workspace.Workspace, error) {
	ws, err := h.ws.Get(ctx, c.ref)
	if !errors.Is(err, workspace.ErrNotFound) {
		return h.requireCollection(ctx, c, ws, err)
	}

	ws, err = h.createWorkspace(ctx, c, admit)
	if errors.Is(err, workspace.ErrAlreadyExists) {
		// The workspace that the winning create made is read again. It meets the
		// same check as a workspace that the first read found.
		ws, err = h.ws.Get(ctx, c.ref)

		return h.requireCollection(ctx, c, ws, err)
	}

	return ws, err
}

// requireCollection returns ws and err unchanged when err is set. Else it
// refuses a workspace that no collection holds: such a workspace takes no LOCK
// and no state.
func (h *Handler) requireCollection(
	ctx context.Context, c call, ws workspace.Workspace, err error,
) (workspace.Workspace, error) {
	if err != nil {
		return ws, err
	}

	obj, objErr := unitObject(c.ref)
	if objErr != nil {
		return workspace.Workspace{}, objErr
	}

	if reqErr := h.placer.Require(ctx, obj); reqErr != nil {
		return workspace.Workspace{}, reqErr
	}

	return ws, nil
}

// createWorkspace creates the workspace of the path for a first LOCK or POST, after the caller
// holds unit:manage on its unit and admit returns nil. The caller authorizes
// first, so a caller without the action learns nothing from the checks of admit.
func (h *Handler) createWorkspace(
	ctx context.Context, c call, admit func(ctx context.Context) error,
) (workspace.Workspace, error) {
	obj, err := unitObject(c.ref)
	if err != nil {
		return workspace.Workspace{}, err
	}

	if authErr := h.guard.Authorize(ctx, c.request, c.principal, identity.ActionUnitManage, obj); authErr != nil {
		return workspace.Workspace{}, fmt.Errorf("authorize %s: %w", identity.ActionUnitManage, authErr)
	}

	if admitErr := admit(ctx); admitErr != nil {
		return workspace.Workspace{}, admitErr
	}

	// The job of the token, if any, decides whether a manual lock of the
	// namespace applies to the create: the phase of a job that started before
	// the lock passes it, as it does for the LOCK that follows.
	placement, err := h.placer.Place(ctx, tofuunit.PlaceRequest{
		Request: c.request, Principal: c.principal, Ref: c.ref, Guard: h.guard,
		Namespaces: h.namespaces, Workspaces: h.ws,
	})
	if err != nil {
		return workspace.Workspace{}, err
	}

	return h.ws.Create(workspace.WithJob(ctx, c.principal.JobTokenID()), c.ref, placement)
}

// unlockWorkspace serves UNLOCK. The lock ID of the body must be the current
// lock. An UNLOCK that names another lock, or a lock held by another principal,
// answers 409 with the current lock info. A principal that does not hold the lock
// makes a force-unlock, which forceUnlockWorkspace serves.
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

	if !ws.Lock.Locked() {
		h.unlockFree(w, r, c, ws, info.ID)

		return
	}

	if ws.Lock.ID != info.ID {
		h.recordLockRefusal(ctx, r, c, "unlock names a lock that is not the current lock")
		h.writeConflict(w, ws.Lock)

		return
	}

	if ws.Lock.Holder != c.principal.Subject {
		h.forceUnlockWorkspace(w, r, c, ws, ws.Lock)

		return
	}

	h.unlockHeld(w, r, c, ws.ID, info.ID)
}

// unlockFree serves an UNLOCK of a workspace that holds no lock. OpenTofu
// sends an UNLOCK again when the answer of a committed unlock or force-unlock
// is lost. An UNLOCK that repeats the last release of the workspace succeeds
// without a change: a normal release when the caller held the lock, a forced
// release when the caller forced it. The request must name the lock ID of the
// record. Any other UNLOCK answers 409 with no body, as before.
//
// The service applies the same rule again, with the record that it reads in
// the release, so a lock that another request takes after this read is not
// released here.
func (h *Handler) unlockFree(w http.ResponseWriter, r *http.Request, c call, ws workspace.Workspace, lockID string) {
	subject := c.principal.Subject

	normal := workspace.ReleaseRepeat{LockID: lockID, Holder: "", Caller: subject, Kind: workspace.ReleaseNormal}
	if h.ws.RepeatsRelease(ws, normal) {
		h.unlockHeld(w, r, c, ws.ID, lockID)

		return
	}

	forced := workspace.ReleaseRepeat{LockID: lockID, Holder: "", Caller: subject, Kind: workspace.ReleaseForced}
	if h.ws.RepeatsRelease(ws, forced) {
		// The force-unlock names the lock of the record, so the service matches
		// the repeat on the lock ID and on the holder of the record.
		expect := workspace.Lock{LockedAt: time.Time{}, ID: lockID, Holder: ws.LastRelease.Holder, Reason: ""}
		h.forceUnlockWorkspace(w, r, c, ws, expect)

		return
	}

	h.writeConflict(w, ws.Lock)
}

// unlockHeld releases the lock lockID of the caller, or answers the repeat of
// its last release. A lock that changed, or that another principal holds,
// answers 409 with the current lock info.
func (h *Handler) unlockHeld(w http.ResponseWriter, r *http.Request, c call, id workspace.ID, lockID string) {
	ctx := r.Context()

	// The job of a job token is the origin of the release: the state version
	// that the release publishes records it.
	_, err := h.ws.UnlockLock(workspace.WithJob(ctx, c.principal.JobTokenID()), id, lockID, c.principal.Subject)
	if errors.Is(err, workspace.ErrLockedByOther) || errors.Is(err, workspace.ErrNotLocked) ||
		errors.Is(err, workspace.ErrLockMismatch) {
		h.recordLockRefusal(ctx, r, c, "the lock changed or is held by another principal")
		h.writeHeldOrConflict(ctx, w, c, err)

		return
	}

	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.WriteHeader(http.StatusOK)
}

// recordLockRefusal writes the audit entry of an UNLOCK that the workspace
// refused, here and not in the middleware, because the middleware has allowed
// the request. The entry names the action of the lock call and the unit.
func (h *Handler) recordLockRefusal(ctx context.Context, r *http.Request, c call, reason string) {
	obj, err := unitObject(c.ref)
	if err != nil {
		h.logger.WarnContext(ctx, "audit of a lock refusal skipped: no unit object")

		return
	}

	denial := refusal.Denial{
		Action: identity.ActionUnitLock, Object: obj, Reason: reason, ReasonWithin: nil, Window: 0,
	}
	h.guard.Record(ctx, r, c.principal, denial)
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
func (h *Handler) writeHeldOrConflict(ctx context.Context, w http.ResponseWriter, c call, refused error) {
	lock, err := h.refusingLock(ctx, c, refused)
	if err != nil {
		h.writeError(ctx, w, refused)

		return
	}

	h.writeConflict(w, lock)
}
