package tfe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/api/tofuunit"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// Reveal of one secret value (ADR 41 decisions 4 and 5): the caller names
// one value path of a finalized state version, the server answers the plain
// value only when the path is in the masked set of the masked state read.
// The masked set comes from the masked state read, so no second masking
// path exists.

const (
	// revealBodyLimit bounds one reveal request body on the wire. The body
	// holds one value path, so 8 KiB leaves ample room while a large body
	// cannot grow memory without bound.
	revealBodyLimit = 8 << 10
	// maxRevealPathLen caps the value path of one reveal request, in bytes.
	// The audit entry keeps the path, so a longer path is refused instead
	// of stored.
	maxRevealPathLen = 1024
	// revealResourceType is the JSON:API type of the reveal call.
	revealResourceType = "state-version-reveals"
	// noStoreValue is the Cache-Control of every reveal answer: a revealed
	// value never lands in a cache.
	noStoreValue = "no-store"
)

// revealAttributes holds the value path of the reveal call and, in the
// answer, the plain JSON value at that path.
type revealAttributes struct {
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

// revealData is the data object of the reveal call.
type revealData struct {
	ID         string           `json:"id,omitempty"`
	Type       string           `json:"type"`
	Attributes revealAttributes `json:"attributes"`
}

// revealDocument is the body of the reveal call.
type revealDocument struct {
	Data revealData `json:"data"`
}

// revealRoute returns the reveal call: it answers the plain value at one
// value path of a finalized state version. The path must be in the masked
// set of that version, else the call answers 404, so a caller cannot probe
// paths. The call needs unit:read and unit:read-secrets, and a console
// session with a recent sign-in: an access token gets 403 and an old sign-in
// gets 401, and each refusal writes a failure entry. It answers
// Cache-Control: no-store, and writes one audit entry of the kind reveal
// before it answers. If the append fails, the call answers 500 and no value.
// A request body over the limit answers 413. A log line may hold the path,
// never the value.
func (h *Handler) revealRoute() Route {
	return Route{
		Method: http.MethodPost,
		Path:   "/api/v2/state-versions/{state_version_id}/actions/reveal",
		// A reveal answers a plain secret value, so it needs unit:read
		// and unit:read-secrets on the unit of the workspace. The
		// resolver maps the version to that unit. Unknown versions
		// answer 404; a state the client encrypted answers 409.
		// A console reveal needs a console session with a recent sign-in. An
		// access token never gives one, so it gets 403.
		Scope:   ScopeConsole,
		Actions: []identity.Action{identity.ActionUnitRead, identity.ActionUnitReadSecrets},
		Resolve: h.resolveStateVersionID,
		Serve:   h.freshReveal(h.revealStateValue),
	}
}

// revealStateValue serves the reveal call.
func (h *Handler) revealStateValue(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	// Every answer of this route is no-store, the errors too: preset the
	// header so no later branch can answer without it.
	w.Header().Set("Cache-Control", noStoreValue)

	path, ok := h.revealPath(w, r)
	if !ok {
		return
	}

	id := workspace.StateVersionID(r.PathValue("state_version_id"))

	// The domain opens the sealed state as a stream for each pass of the
	// lookup, so no reveal ever reads the whole state into memory.
	value, err := workspace.RevealVersion(ctx, h.workspaces, id, path, h.logger)
	if err != nil {
		// A path outside the masked set answers 404 with no audit entry
		// and no value, so a caller cannot probe paths. A client-encrypted
		// state answers 409 with no entry either: no value leaves the
		// server (ADR 41 decision 5). The Pulumi route differs on purpose:
		// its issue asks for an entry when another secrets provider holds
		// the value.
		h.writeNoStore(ctx, w, err)

		return
	}

	if !h.appendRevealEntry(ctx, revealAuditEntry{
		request:   r,
		principal: p,
		object:    obj,
		version:   id.String(),
		path:      path,
		outcome:   audit.OutcomeSuccess,
	}) {
		h.writeNoStore(ctx, w, errRevealAuditFailed)

		return
	}

	h.logger.InfoContext(ctx, "reveal completed", slog.String("path", path))

	w.Header().Set("Cache-Control", noStoreValue)
	h.writeJSON(ctx, w, http.StatusOK, revealDocument{
		Data: revealData{
			ID:         id.String(),
			Type:       revealResourceType,
			Attributes: revealAttributes{Path: path, Value: value},
		},
	})
}

// errRevealAuditFailed reports a failed audit append before a reveal answer.
// It carries no value and no path detail beyond the status.
var errRevealAuditFailed = errors.New("audit entry failed")

// errRevealPathTooLong reports a reveal value path over maxRevealPathLen
// bytes. The audit entry keeps the path, so the call is refused.
var errRevealPathTooLong = errors.New("reveal path over 1024 bytes")

// revealPath reads the value path of one reveal body: a JSON:API document
// that holds the path. An empty path and an unreadable body answer 422; an
// oversized body answers 413. Every answer carries Cache-Control: no-store,
// so no reveal answer lands in a cache.
func (h *Handler) revealPath(w http.ResponseWriter, r *http.Request) (string, bool) {
	ctx := r.Context()

	body, err := io.ReadAll(
		io.LimitReader(http.MaxBytesReader(w, r.Body, revealBodyLimit), revealBodyLimit+1),
	)
	if err != nil {
		if tooLarge, ok := errors.AsType[*http.MaxBytesError](err); ok && tooLarge != nil {
			h.writeNoStore(ctx, w, fmt.Errorf("%w: body over %d bytes", workspace.ErrTooLarge, revealBodyLimit))
		} else {
			h.writeNoStore(ctx, w, errInvalidBody)
		}

		return "", false
	}

	if int64(len(body)) > revealBodyLimit {
		h.writeNoStore(ctx, w, fmt.Errorf("%w: body over %d bytes", workspace.ErrTooLarge, revealBodyLimit))

		return "", false
	}

	var doc revealDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		h.writeNoStore(ctx, w, errInvalidBody)

		return "", false
	}

	if doc.Data.Attributes.Path == "" {
		h.writeNoStore(ctx, w, errInvalidBody)

		return "", false
	}

	if len(doc.Data.Attributes.Path) > maxRevealPathLen {
		h.writeNoStore(ctx, w, errRevealPathTooLong)

		return "", false
	}

	return doc.Data.Attributes.Path, true
}

// revealAuditEntry holds what the audit entry of a reveal call records:
// the request, the caller, the unit, the version, the value path and the
// outcome.
type revealAuditEntry struct {
	request   *http.Request
	principal identity.Principal
	object    identity.Object
	version   string
	path      string
	outcome   audit.Outcome
}

// appendRevealEntry writes one audit entry of the kind reveal with the action
// unit:read-secrets for a reveal call, before the server answers. It reports
// whether the append worked. A failed append writes a log line with the path
// but never the value, and counts the failure: the caller then answers 500
// and no value.
func (h *Handler) appendRevealEntry(ctx context.Context, a revealAuditEntry) bool {
	entry := tofuunit.Entry(a.request, a.principal, a.object, tofuunit.Detail{
		At: time.Now().UTC(), Action: identity.ActionUnitReadSecrets, Version: a.version, ValuePath: a.path,
		Kind: audit.KindReveal, Outcome: a.outcome, Reason: "",
	})

	if err := h.rules().Append(ctx, entry); err != nil {
		h.logger.ErrorContext(ctx, "audit append failed", slog.String("path", a.path))
		h.failAudit(ctx)

		return false
	}

	return true
}

// writeNoStore answers err with Cache-Control: no-store, so no reveal
// answer, including an error, lands in a cache. It never writes a value.
func (h *Handler) writeNoStore(ctx context.Context, w http.ResponseWriter, err error) {
	w.Header().Set("Cache-Control", noStoreValue)
	h.writeError(ctx, w, err)
}
