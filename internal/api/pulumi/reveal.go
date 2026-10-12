package pulumi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/stack"
)

const (
	// revealBodyLimit bounds one reveal request body on the wire. The body
	// holds one value path, so 8 KiB leaves ample room while a large body
	// cannot grow memory without bound.
	revealBodyLimit = 8 << 10
	// noStoreValue is the Cache-Control of every reveal answer: a revealed
	// value never lands in a cache.
	noStoreValue = "no-store"
)

// revealRequest is the body of the reveal call: the value path of one masked
// secret.
type revealRequest struct {
	Path string `json:"path"`
}

// revealResponse answers the reveal call: the value path and the plain JSON
// value at that path.
type revealResponse struct {
	Path  string `json:"path"`
	Value any    `json:"value"`
}

// revealRoute returns the reveal call (ADR 41 decisions 4, 5 and 7): it
// decrypts the secret at one value path of one checkpoint version with the
// data key of the stack. The path must be in the masked set of that version,
// else the call answers 404, so a caller cannot probe paths. A stack with
// another secrets provider answers 409 with an error that names the
// provider. The call needs unit:read and unit:read-secrets, answers
// Cache-Control: no-store, and writes one audit entry of the kind reveal
// before it answers. If the append fails, the call answers 500 and no value.
func (h *Handler) revealRoute() Route {
	return Route{
		Method:  http.MethodPost,
		Path:    "/api/stacks/{namespace}/{project}/{stack}/state/{version}/reveal",
		Scope:   ScopeAuthorized,
		Actions: []identity.Action{identity.ActionUnitRead, identity.ActionUnitReadSecrets},
		Resolve: resolveGetStack,
		Serve:   h.revealStateValue,
	}
}

// revealStateValue serves the reveal call. It sets Cache-Control: no-store
// before any other work, so every answer of the call, including the errors
// of the state and scope lookups below, carries it.
func (h *Handler) revealStateValue(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	w.Header().Set("Cache-Control", noStoreValue)

	path, ok := h.revealPath(w, r)
	if !ok {
		return
	}

	target, ok := h.stateTargetOf(w, r)
	if !ok {
		return
	}

	current, ok := h.stateCurrent(w, r, target)
	if !ok {
		return
	}

	version := r.PathValue("version")
	if target.latest {
		version = checkpointVersion(ctx, h, target, current)
	}

	cipher, _, err := stack.SecretCiphertextOf(current.Deployment, path)
	if err != nil {
		// A path outside the masked set answers 404 with no audit entry
		// and no value, so a caller cannot probe paths.
		h.writeNoStore(ctx, w, err)

		return
	}

	entry := revealAudit{
		request:   r,
		principal: p,
		object:    obj,
		version:   version,
		path:      path,
		outcome:   audit.OutcomeSuccess,
	}

	provider := stack.SecretsProvider(current.Deployment)
	if provider != serviceSecretsProviderName() {
		entry.outcome = audit.OutcomeFailure
		if !h.appendRevealEntry(ctx, entry) {
			h.writeNoStore(ctx, w, errAuditFailed)

			return
		}
		h.writeNoStore(ctx, w, fmt.Errorf("%w: %q", stack.ErrOtherSecretsProvider, provider))

		return
	}

	h.answerReveal(w, r, entry, cipher)
}

// answerReveal decrypts cipher with the data key of the stack, writes the
// audit entry and answers the plain value. If the append fails, the call
// answers 500 and no value.
func (h *Handler) answerReveal(
	w http.ResponseWriter, r *http.Request, entry revealAudit, cipher []byte,
) {
	ctx := r.Context()

	scope, ok := h.revealScope(w, r)
	if !ok {
		return
	}

	plain, err := h.secrets.Decrypt(ctx, scope, [][]byte{cipher})
	if err != nil {
		entry.outcome = audit.OutcomeFailure
		if !h.appendRevealEntry(ctx, entry) {
			h.writeNoStore(ctx, w, errAuditFailed)

			return
		}
		h.logger.WarnContext(ctx, "reveal decrypt failed", slog.String("path", entry.path))
		h.writeNoStore(ctx, w, fmt.Errorf("reveal %q: %w", entry.path, errRevealDecrypt))

		return
	}

	if !h.appendRevealEntry(ctx, entry) {
		h.writeNoStore(ctx, w, errAuditFailed)

		return
	}

	h.logger.InfoContext(ctx, "reveal completed", slog.String("path", entry.path))

	w.Header().Set("Cache-Control", noStoreValue)
	h.writeJSON(ctx, w, http.StatusOK, revealResponse{Path: entry.path, Value: stack.PlainValue(plain[0])})
}

// serviceSecretsProviderName returns the only provider the server decrypts.
func serviceSecretsProviderName() string {
	return "service"
}

// errRevealDecrypt reports a ciphertext of the stored state that does not
// open. It carries no value, no key and no scope.
var errRevealDecrypt = errors.New("cannot reveal the value")

// errAuditFailed reports a failed audit append before a reveal answer. It
// carries no value and no path detail beyond the status.
var errAuditFailed = errors.New("audit entry failed")

// revealPath reads the value path of one reveal body. An empty path and an
// unreadable body answer 400; an oversized body answers 413. Every answer
// carries Cache-Control: no-store, so no reveal answer lands in a cache.
func (h *Handler) revealPath(w http.ResponseWriter, r *http.Request) (string, bool) {
	ctx := r.Context()

	// Set no-store before the gate writes: hold, largeReader, writeTooLarge
	// and writeInvalidBody answer without it, and the header must already be
	// set when they write.
	w.Header().Set("Cache-Control", noStoreValue)

	releaseHold, ok := h.hold(w, r, revealBodyLimit)
	if !ok {
		return "", false
	}

	defer releaseHold()

	counted, release, ok := h.largeReader(w, r, revealBodyLimit)
	if !ok {
		return "", false
	}

	defer release()

	body, err := io.ReadAll(counted)
	if err != nil {
		if isMaxBytes(err) {
			h.writeTooLarge(w, r, revealBodyLimit)
		} else {
			h.writeInvalidBody(ctx, w)
		}

		return "", false
	}

	if counted.n > revealBodyLimit {
		h.writeTooLarge(w, r, revealBodyLimit)

		return "", false
	}

	var req revealRequest
	if err := json.Unmarshal(body, &req); err != nil {
		h.writeInvalidBody(ctx, w)

		return "", false
	}

	if req.Path == "" {
		h.writeInvalidBody(ctx, w)

		return "", false
	}

	return req.Path, true
}

// revealScope resolves the stack in the path to the scope its values are
// encrypted under: the namespace and the stack ID.
func (h *Handler) revealScope(w http.ResponseWriter, r *http.Request) (secret.Scope, bool) {
	return h.secretScope(w, r)
}

// checkpointVersion names the version of current for the audit entry: the
// explicit version, or the version of the update that saved the latest
// checkpoint. A lookup failure keeps the path value the request named.
func checkpointVersion(ctx context.Context, h *Handler, target stateTarget, current stack.Checkpoint) string {
	if !target.latest {
		return strconv.FormatInt(target.version, 10)
	}

	u, err := h.updates.Get(ctx, target.ref, current.Update)
	if err != nil {
		return latestVersionName
	}

	return strconv.FormatInt(u.Version, 10)
}

// revealAudit holds what the audit entry of a reveal call records: the
// request, the caller, the unit, the version, the value path and the
// outcome.
type revealAudit struct {
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
func (h *Handler) appendRevealEntry(ctx context.Context, a revealAudit) bool {
	if h.audit == nil {
		return true
	}

	ns, err := h.namespaces.Get(ctx, a.object.Namespace())
	if err != nil {
		h.failAudit(ctx)

		return false
	}

	entry := audit.Entry{
		At:         time.Now().UTC(),
		Namespace:  a.object.Namespace(),
		Subject:    a.principal.Subject,
		IDPSubject: "",
		TokenID:    "",
		Action:     string(identity.ActionUnitReadSecrets),
		Object:     a.object.String(),
		Version:    a.version,
		ValuePath:  a.path,
		ValueCount: nil,
		SourceIP:   sourceIP(a.request),
		UserAgent:  truncateUserAgent(a.request.UserAgent()),
		Channel:    audit.NormalizeChannel(a.request.Header.Get(channelHeader)),
		Kind:       audit.KindReveal,
		Outcome:    a.outcome,
		RequestID:  requestid.FromContext(ctx),
		PrevHash:   "",
		Hash:       "",
		Sequence:   0,
	}

	if _, err := h.audit.Append(ctx, ns.Fence(), entry); err != nil {
		h.logger.ErrorContext(ctx, "audit append failed", slog.String("path", a.path))
		h.failAudit(ctx)

		return false
	}

	return true
}

// writeNoStore answers err with Cache-Control: no-store, so no reveal
// answer, including an error, lands in a cache. It never writes a value: the
// error text carries no secret.
func (h *Handler) writeNoStore(ctx context.Context, w http.ResponseWriter, err error) {
	w.Header().Set("Cache-Control", noStoreValue)
	h.writeError(ctx, w, err)
}
