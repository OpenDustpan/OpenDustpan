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

	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/requestid"
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
	// revealChannelHeader declares where a request comes from: "api" or
	// "console". The audit entry records it as declared; unknown values
	// are "api".
	revealChannelHeader = "Dustpan-Channel"
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
// paths. The call needs unit:read and unit:read-secrets, answers
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
		Scope:   ScopeAuthorized,
		Actions: []identity.Action{identity.ActionUnitRead, identity.ActionUnitReadSecrets},
		Resolve: h.resolveStateVersionID,
		Serve:   h.revealStateValue,
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

	sv, err := h.workspaces.ReadState(ctx, id)
	if err != nil {
		h.writeNoStore(ctx, w, err)

		return
	}

	// The sealed state opens as a stream: decode from it directly, so no
	// reveal ever reads the whole state into memory.
	open := func() (io.ReadCloser, error) {
		content, openErr := h.workspaces.Open(ctx, sv)
		if openErr != nil {
			return nil, openErr
		}

		return &logCloseReader{
			ReadCloser: content,

			warn: func(closeErr error) {
				h.logger.WarnContext(ctx, "close state content failed", slog.String("error", closeErr.Error()))
			},
		}, nil
	}

	value, _, err := workspace.RevealValueFromOpener(open, sv.SensitiveMarks, path)
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

// logCloseReader wraps a state stream and logs a close failure. The lookup
// closes the stream; the log keeps the warning the direct close had.
type logCloseReader struct {
	io.ReadCloser

	warn func(closeErr error)
}

// Close closes the wrapped stream, logs a failure, and returns it.
func (r *logCloseReader) Close() error {
	if err := r.ReadCloser.Close(); err != nil {
		r.warn(err)

		return err //nolint:wrapcheck // Close returns the exact stream error so the lookup joins it.
	}

	return nil
}

// errRevealAuditFailed reports a failed audit append before a reveal answer.
// It carries no value and no path detail beyond the status.
var errRevealAuditFailed = errors.New("audit entry failed")

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
	if h.audit == nil {
		return true
	}

	ns, err := h.namespaces.Get(ctx, a.object.Namespace())
	if err != nil {
		h.failRevealAudit(ctx)

		return false
	}

	entry := audit.Entry{
		At:         time.Now().UTC(),
		Namespace:  a.object.Namespace(),
		Subject:    a.principal.Subject,
		IDPSubject: "",
		TokenID:    a.principal.TokenID,
		Action:     string(identity.ActionUnitReadSecrets),
		Object:     a.object.String(),
		Version:    a.version,
		ValuePath:  a.path,
		ValueCount: nil,
		SourceIP:   clientip.String(a.request),
		UserAgent:  truncateRevealUserAgent(a.request.UserAgent()),
		Channel:    audit.NormalizeChannel(a.request.Header.Get(revealChannelHeader)),
		Kind:       audit.KindReveal,
		Outcome:    a.outcome,
		RequestID:  requestid.FromContext(ctx),
		ObjectID:   a.object.UnitID(),
		PrevHash:   "",
		Hash:       "",
		Sequence:   0,
	}

	if _, err := h.audit.Append(ctx, ns.Fence(), entry); err != nil {
		h.logger.ErrorContext(ctx, "audit append failed", slog.String("path", a.path))
		h.failRevealAudit(ctx)

		return false
	}

	return true
}

// failRevealAudit records a lost audit entry: one log line with no secret
// and no value, and one count on the failure metric through the wired
// callback. It names no error text: the text may hold driver detail.
func (h *Handler) failRevealAudit(ctx context.Context) {
	h.logger.ErrorContext(ctx, "audit append failed")

	if h.onAuditFailure != nil {
		h.onAuditFailure()
	}
}

// truncateRevealUserAgent caps s at audit.MaxUserAgentLen bytes on a UTF-8
// rune boundary, so a multi-byte rune is never split.
func truncateRevealUserAgent(s string) string {
	if len(s) <= audit.MaxUserAgentLen {
		return s
	}

	cut := audit.MaxUserAgentLen
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}

	return s[:cut]
}

// writeNoStore answers err with Cache-Control: no-store, so no reveal
// answer, including an error, lands in a cache. It never writes a value.
func (h *Handler) writeNoStore(ctx context.Context, w http.ResponseWriter, err error) {
	w.Header().Set("Cache-Control", noStoreValue)
	h.writeError(ctx, w, err)
}
