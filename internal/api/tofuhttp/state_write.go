package tofuhttp

import (
	"context"
	"crypto/md5" //nolint:gosec // The Content-MD5 header is the protocol's integrity check, not a security control.
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/internal/bodygate"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// writeStateRoutes returns the state write row of the address. A POST needs
// unit:write. The unlocked and forced overrides need their own actions, which
// the handler checks through the allowance (ADR 49, decision 6).
func (h *Handler) writeStateRoutes() []Route {
	return []Route{
		{
			Method:  http.MethodPost,
			Path:    statePath,
			Actions: []identity.Action{identity.ActionUnitWrite},
			Serve:   h.postState,
		},
	}
}

// postState serves POST: one state version streamed from the body. The write
// creates the workspace on first use, after the caller holds unit:manage on it
// (ADR 49, decision 5). Its ?ID= names the lock; a write without it needs an
// unlocked unit and the unlocked override.
func (h *Handler) postState(w http.ResponseWriter, r *http.Request, c call) {
	ctx := r.Context()

	lease, ok := bodygate.NewLease(h.gate, bodygate.StreamReservation(r))
	if !ok {
		h.writeBusy(w)

		return
	}

	defer lease.Release()

	ctx = workspace.WithBudget(ctx, lease)

	// The job of a job token is the origin of the version this write
	// publishes. Any other token passes no job.
	ctx = workspace.WithJob(ctx, c.principal.JobTokenID())

	sum, err := contentMD5(r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	in := workspace.StreamInput{
		Holder: c.principal.Subject,
		LockID: r.URL.Query().Get("ID"),
		MD5:    sum,
	}

	ws, err := h.workspaceOf(ctx, c, func(ctx context.Context) error {
		return h.ws.AdmitFirstWrite(ctx, c.ref, in)
	})
	if err != nil {
		h.writeFirstRefusal(ctx, w, err, h.writeError)

		return
	}

	obj, err := unitObject(c.ref)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	allow, err := h.rules().Allowance(ctx, c.principal, ws, obj)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	// A write under a lock that the caller holds met the gates at that lock,
	// a forced write too. Each other write met no gate at a lock, so the
	// gates run here, before the body streams and before the transaction.
	if !holdsNamedLock(ws, in.LockID, c.principal.Subject) && !h.guardStateWrite(ctx, w, c, stateCheck{
		action: identity.ActionUnitWriteUnlocked, op: gate.OperationUnlockedWrite,
	}) {
		return
	}

	auth := workspace.StreamAuthority{Allow: allow, Record: h.recordOverride(r, c.principal, obj)}

	_, err = h.ws.WriteState(ctx, ws.ID, in, auth, r.Body)
	if err != nil {
		h.rules().RecordOverrideRefusal(ctx, r, c.principal, obj, err)
		h.writeStateRefusal(ctx, w, c, err)

		return
	}

	w.WriteHeader(http.StatusOK)
}

// holdsNamedLock reports whether lockID names the current lock of ws and
// holder holds it. Only such a write skips the gates, because its lock met
// them.
//
// Defense in depth: the domain refuses a write whose lock ID is not the
// current lock of its holder. This check does not rely on that refusal: a
// lock ID that is empty, that names no lock, or that names the lock of
// another holder meets the gates here all the same.
func holdsNamedLock(ws workspace.Workspace, lockID, holder string) bool {
	return lockID != "" && ws.Lock.Locked() && ws.Lock.ID == lockID && ws.Lock.Holder == holder
}

// writeBusy answers a state write that arrived while the gate is full. It
// uses the plain error shape of the other routes.
func (h *Handler) writeBusy(w http.ResponseWriter) {
	bodygate.SetRetryAfter(w)
	http.Error(w, "server is busy: try again in one second", http.StatusServiceUnavailable)
}

// writeStateRefusal answers a refused POST. A lock held by another principal, or
// a write without the lock ID of a locked unit, answers 423 with the current
// lock info. A lock ID other than the current lock answers 409 with the current
// lock info (ADR 49, decision 6).
func (h *Handler) writeStateRefusal(ctx context.Context, w http.ResponseWriter, c call, refusal error) {
	switch {
	case errors.Is(refusal, workspace.ErrLocked), errors.Is(refusal, workspace.ErrLockedByOther):
		h.writeHeld(ctx, w, c, refusal)
	case errors.Is(refusal, workspace.ErrLockMismatch):
		h.writeHeldOrConflict(ctx, w, c, refusal)
	default:
		h.writeError(ctx, w, refusal)
	}
}

// contentMD5 decodes the Content-MD5 header of a POST into the hex MD5 that the
// domain checks. A request without the header declares no MD5, and the bytes
// set it. A malformed header is refused as an invalid state.
func contentMD5(r *http.Request) (string, error) {
	raw := r.Header.Get("Content-MD5")
	if raw == "" {
		return "", nil
	}

	sum, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(sum) != md5.Size {
		return "", fmt.Errorf("%w: Content-MD5 header", workspace.ErrInvalidState)
	}

	return hex.EncodeToString(sum), nil
}

// forceUnlockWorkspace serves an UNLOCK by a principal that does not hold the
// lock: a force-unlock (ADR 49, decision 6). It needs unit:write-forced. The
// unlock writes the fence version of a unit with state; a client-encrypted state
// unlocks unfenced only while the forced-write switches are on (ADR 44).
//
// expect is the lock that the release must end: the lock that the caller read
// on a locked workspace, or the lock of the record of the last release on a
// free workspace. On a free workspace the service answers success only for a
// repeat of the forced release of the caller, and changes nothing.
func (h *Handler) forceUnlockWorkspace(
	w http.ResponseWriter, r *http.Request, c call, ws workspace.Workspace, expect workspace.Lock,
) {
	ctx := r.Context()

	obj, err := unitObject(c.ref)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if !h.rules().Permits(ctx, c.principal, identity.ActionUnitWriteForced, obj) {
		h.writeError(
			ctx,
			w,
			fmt.Errorf("%w: force-unlock needs %s", identity.ErrDenied, identity.ActionUnitWriteForced),
		)

		return
	}

	allow, err := h.rules().Allowance(ctx, c.principal, ws, obj)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if !h.guardStateWrite(ctx, w, c, stateCheck{action: identity.ActionUnitWriteForced, op: gate.OperationUnlock}) {
		return
	}

	record := func() error {
		return h.appendOverrideAudit(r, c.principal, obj, identity.ActionUnitWriteForced, "")
	}

	// The lock the caller saw is the one that must still be held: its ID and its
	// holder come from the same read. On a free workspace, expect names the lock
	// of the record of the last release, which a repeat must match.
	//
	// The job of a job token is the origin of the release: the state version
	// that the release publishes records it.
	_, _, err = h.ws.ForceUnlockLock(
		workspace.WithJob(ctx, c.principal.JobTokenID()),
		ws.ID,
		expect,
		c.principal.Subject,
		allow,
		record,
		h.forceUnlockEntry(r, c, obj),
	)
	if errors.Is(err, workspace.ErrLockedByOther) || errors.Is(err, workspace.ErrNotLocked) ||
		errors.Is(err, workspace.ErrLockMismatch) {
		h.writeHeldOrConflict(ctx, w, c, err)

		return
	}

	if err != nil {
		h.rules().RecordOverrideRefusal(ctx, r, c.principal, obj, err)
		h.writeError(ctx, w, err)

		return
	}

	w.WriteHeader(http.StatusOK)
}
