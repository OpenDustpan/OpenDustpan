package tofuhttp

import (
	"context"
	"crypto/md5" //nolint:gosec // The Content-MD5 header is the protocol's integrity check, not a security control.
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"

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

	sum, err := contentMD5(r)
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

	obj, err := unitObject(c.ref)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	allow, err := h.allowance(ctx, c.principal, ws, obj)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	in := workspace.StreamInput{
		Holder: c.principal.Subject,
		LockID: r.URL.Query().Get("ID"),
		MD5:    sum,
	}

	auth := workspace.Authority{Allow: allow, Record: h.recordOverride(r, c.principal, obj)}

	_, err = h.ws.WriteState(ctx, ws.ID, in, auth, r.Body)
	if err != nil {
		h.writeStateRefusal(ctx, w, c, err)

		return
	}

	w.WriteHeader(http.StatusOK)
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
		current, err := h.ws.Get(ctx, c.ref)
		if err != nil {
			h.writeError(ctx, w, refusal)

			return
		}

		h.writeConflict(w, current.Lock)
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
