package tfe

import (
	"bytes"
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
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/manuallock"
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

// refusingHolder returns the holder of the lock that refused err. The domain
// names that lock in its refusal, as the refusing revision held it. A
// refusal without one, such as a lost compare-and-swap of the store, reads
// the workspace again.
func (h *Handler) refusingHolder(ctx context.Context, id workspace.ID, err error) string {
	if lock, ok := workspace.RefusingLock(err); ok {
		return lock.Holder
	}

	return h.lockHolder(ctx, id)
}

// lockConflict returns the 409 of a workspace whose lock holder is another
// principal, with the detail of lockConflictDetail.
func lockConflict(id workspace.ID, holder string) errorClass {
	return errorClass{
		status: http.StatusConflict, title: titleConflict, detail: lockConflictDetail(id, holder),
		code: errcode.WorkspaceLocked, args: nil,
	}
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
// lost compare-and-swap also answers so: the loser retries once, through the
// gates again, then reports the held lock, so the CLI waits and retries under
// -lock-timeout.
func (h *Handler) lockWorkspace(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()
	id := workspace.ID(r.PathValue("id"))

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		h.writeError(ctx, w, errInvalidBody)

		return
	}

	reason := lockReason(body)

	// A held lock refuses the call with 409 whatever the gates say, and a CLI
	// that waits under -lock-timeout retries it. The answer comes from this
	// read alone: the handler never calls LockForJob for a lock that it saw
	// held. If the holder unlocks after the read, the caller gets 409 and
	// retries, so no call takes the lock without the gates. A retry writes no
	// new decision record.
	if holder := h.lockHolder(ctx, id); holder != "" {
		h.writeClassError(ctx, w, lockConflict(id, holder))

		return
	}

	c := lockCall{w: w, r: r, p: p, obj: obj, id: id, reason: reason}
	if !h.lockOnce(ctx, c) {
		return
	}

	// The compare-and-swap was lost. Read the holder again: a held lock ends the
	// call with 409, and a free lock goes through the gates again.
	if holder := h.lockHolder(ctx, id); holder != "" {
		h.writeClassError(ctx, w, lockConflict(id, holder))

		return
	}

	if !h.lockOnce(ctx, c) {
		return
	}

	h.writeLockRefusal(ctx, w, id, workspace.ErrConcurrentModification)
}

// lockCall holds the parts of one lock request.
type lockCall struct {
	w      http.ResponseWriter
	r      *http.Request
	p      identity.Principal
	obj    identity.Object
	id     workspace.ID
	reason string
}

// lockOnce takes the lock of a workspace that was free at the last read. The
// gates run before the LockForJob call. It answers every outcome itself and
// returns false, except a lost compare-and-swap: it then writes nothing and
// returns true, so that the caller can read the holder and try again. A win
// answers 200 with the workspace. A refusal answers 409 with a detail holding
// "is locked by User" and the holder, so the CLI waits and retries under
// -lock-timeout.
func (h *Handler) lockOnce(ctx context.Context, c lockCall) bool {
	if !h.guardStateWrite(c.w, c.r, c.p, c.obj, lockCheck()) {
		return false
	}

	// A workspace that no collection holds takes no lock.
	if err := h.placer.Require(ctx, c.obj); err != nil {
		h.writeError(ctx, c.w, err)

		return false
	}

	locked, err := h.workspaces.LockForJob(ctx, c.id, c.p.Subject, c.reason, c.p.JobTokenID())

	switch {
	case err == nil:
		h.writeWorkspace(ctx, c.w, http.StatusOK, c.p, locked)
	case errors.Is(err, workspace.ErrLocked):
		h.writeLockRefusal(ctx, c.w, c.id, err)
	case errors.Is(err, workspace.ErrConcurrentModification):
		return true
	default:
		h.writeError(ctx, c.w, err)
	}

	return false
}

// writeLockRefusal answers a refused lock request with 409. A manual lock
// answers lock.locked and names the object, the holder and the reason in its
// own words. A lock of a tool answers workspace.locked, with the words that
// go-tfe parses.
func (h *Handler) writeLockRefusal(ctx context.Context, w http.ResponseWriter, id workspace.ID, err error) {
	if manual, ok := errors.AsType[*manuallock.LockedError](err); ok {
		h.writeManualLock(ctx, w, manual, refusal.LockedText(manual))

		return
	}

	h.writeClassError(ctx, w, lockConflict(id, h.refusingHolder(ctx, id, err)))
}

// unlockWorkspace serves POST /api/v2/workspaces/{id}/actions/unlock. The
// holder unlocks. An unlock by another principal answers 409 with a detail
// holding "is locked by User" and the holder. On an unlocked workspace, an
// unlock by the holder of the last normal release answers success with no
// change, because OpenTofu sends an unlock again when its answer is lost. The
// workspace service applies that rule: the protocol sends no lock ID, so the
// handler has no check of its own to make before it.
func (h *Handler) unlockWorkspace(
	w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object,
) {
	ctx := r.Context()
	id := workspace.ID(r.PathValue("id"))

	// The job of a job token is the origin of the release: the state version
	// that the release publishes records it.
	unlocked, err := h.workspaces.Unlock(workspace.WithJob(ctx, p.JobTokenID()), id, p.Subject)
	if err != nil {
		if errors.Is(err, workspace.ErrLockedByOther) {
			h.writeClassError(ctx, w, lockConflict(id, h.refusingHolder(ctx, id, err)))

			return
		}

		h.writeError(ctx, w, err)

		return
	}

	h.writeWorkspace(ctx, w, http.StatusOK, p, unlocked)
}

// forceUnlockAttributes holds the attributes of a force-unlock body. Every
// attribute is optional.
type forceUnlockAttributes struct {
	Reason     string `json:"reason"`
	LockID     string `json:"lock-id"`     //nolint:tagliatelle // The TFE wire name; it must not change.
	LockHolder string `json:"lock-holder"` //nolint:tagliatelle // The TFE wire name; it must not change.
}

// forceUnlockData holds the data object of a force-unlock body.
type forceUnlockData struct {
	Attributes forceUnlockAttributes `json:"attributes"`
}

// forceUnlockBody is the body of a force-unlock request. `tofu force-unlock`
// sends none. Every field other than data.attributes is ignored.
type forceUnlockBody struct {
	Data forceUnlockData `json:"data"`
}

// parseForceUnlock reads the attributes of a force-unlock body. An empty body
// is valid. A body that is not valid JSON, a lock holder without a lock ID,
// and a reason that audit.ValidateReason refuses fail the request, so a
// check that the caller asked for never passes by silence.
func parseForceUnlock(body []byte) (forceUnlockAttributes, error) {
	var none forceUnlockAttributes

	if len(bytes.TrimSpace(body)) == 0 {
		return none, nil
	}

	var req forceUnlockBody
	if err := json.Unmarshal(body, &req); err != nil {
		return none, errInvalidBody
	}

	attrs := req.Data.Attributes
	if attrs.LockHolder != "" && attrs.LockID == "" {
		return none, fmt.Errorf("%w: lock-holder needs lock-id", errInvalidBody)
	}

	if err := audit.ValidateReason(attrs.Reason); err != nil {
		return none, err
	}

	return attrs, nil
}

// forceUnlockEntry returns the audit entry of a force-unlock from the request
// fields. The workspace service sets the rest: the time, the namespace, the
// subject, the unit ID, the changes and the new state version.
func forceUnlockEntry(r *http.Request, p identity.Principal, obj identity.Object, reason string) audit.Entry {
	return tofuunit.Entry(r, p, obj, tofuunit.Detail{
		At: time.Time{}, Action: identity.ActionUnitWriteForced, Version: "", ValuePath: "",
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, Reason: reason,
	})
}

// forceUnlockWorkspace serves POST
// /api/v2/workspaces/{id}/actions/force-unlock. It releases the lock whoever
// holds it. The body is optional: `tofu force-unlock` sends none. A body can
// hold data.attributes.reason, which the audit entry of the release keeps,
// and data.attributes.lock-id and lock-holder, which name the lock that the
// caller saw. A lock that differs answers 409 and changes nothing. A
// force-unlock of an unlocked workspace answers 409, except a repeat of the
// last forced release by the same principal: the workspace service answers it
// with success and no change, because OpenTofu sends a force-unlock again
// when its answer is lost. A success writes one
// audit entry of the kind change in the transaction of the release, and one
// info log line with the action, the unit, the principal and the released
// holder, and never with the request path or query. The handler owns the
// logger through its Config.
func (h *Handler) forceUnlockWorkspace(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()
	id := workspace.ID(r.PathValue("id"))

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		h.writeError(ctx, w, errInvalidBody)

		return
	}

	attrs, err := parseForceUnlock(body)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	// One read gives the holder for the log and the switches of the
	// allowance. The domain reads the workspace again in its transaction.
	ws, err := h.workspaces.GetByID(ctx, id)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	holder := ""
	if ws.Lock.Locked() {
		holder = ws.Lock.Holder
	}

	allow, err := h.rules().Allowance(ctx, p, ws, obj)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if !h.guardStateWrite(w, r, p, obj, forceUnlockCheck()) {
		return
	}

	// An unfenced release is an override. Its audit entry is written before the
	// release, and a failed entry stops it (ADR 41, decision 5).
	record := func() error {
		return h.appendOverrideAudit(r, p, obj, identity.ActionUnitWriteForced, "")
	}

	expect := workspace.Lock{LockedAt: time.Time{}, ID: attrs.LockID, Holder: attrs.LockHolder, Reason: ""}

	unlocked, _, err := h.workspaces.ForceUnlockLock(
		workspace.WithJob(ctx, p.JobTokenID()), id, expect, p.Subject, allow, record,
		forceUnlockEntry(r, p, obj, attrs.Reason),
	)
	if err != nil {
		h.rules().RecordOverrideRefusal(ctx, r, p, obj, err)
		h.writeError(ctx, w, err)

		return
	}

	h.logForceUnlock(ctx, p, obj, holder)

	h.writeWorkspace(ctx, w, http.StatusOK, p, unlocked)
}

// logForceUnlock writes the info line of a force-unlock that answered 200. A
// release with no holder at the read is a repeat of the last forced release.
// It changes nothing, so its line does not claim a release.
func (h *Handler) logForceUnlock(ctx context.Context, p identity.Principal, obj identity.Object, holder string) {
	if holder == "" {
		h.logger.InfoContext(
			ctx,
			"force-unlock repeated with no change",
			slog.String("action", "force-unlock"),
			slog.String("object", obj.String()),
			slog.String("principal", p.Subject),
		)

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
}

// lockCheck is the state check of the lock of a workspace.
func lockCheck() stateCheck {
	return newStateCheck(identity.ActionUnitWrite, gate.OperationLock)
}

// forceUnlockCheck is the state check of a force-unlock of a workspace.
func forceUnlockCheck() stateCheck {
	return newStateCheck(identity.ActionUnitWriteForced, gate.OperationUnlock)
}
