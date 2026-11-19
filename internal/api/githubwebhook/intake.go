// Package githubwebhook is the HTTP adapter that takes the deliveries of a Git
// host webhook: POST /webhooks/{hookID}. The route has no sign-in. The
// signature of the delivery is its credential, and the service checks it. The
// handler holds no rule except the bound of the body and the answer: every
// delivery, accepted or refused, gets the same 204 with no body and the same
// headers, so no answer tells a sender why a delivery was dropped. The
// operator reads the reason in the counter and in the audit entry.
package githubwebhook

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/api/faultlog"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/identity"
)

// MaxBodyBytes is the largest body that the handler reads.
const MaxBodyBytes = 2 << 20

// Prefix is the path prefix that the root mux mounts the handler for.
const Prefix = "/webhooks/"

const (
	hookPath = Prefix + "{hookID}"

	// The fields of the limiter key and of the entry of a reject.
	auditSubject = "webhook"
	auditAction  = "git-host-event:receive"
	auditKind    = "git-host-event"

	// auditTimeout bounds the append of the entries of one reject. The append
	// runs after the request context is done too, so a sender that closes the
	// connection cannot make an entry get lost.
	auditTimeout = 5 * time.Second
)

// ErrMissingDependency reports a Config without a required part.
var ErrMissingDependency = errors.New("missing dependency")

// errNoServerAudit reports an audit store that cannot write the scope of the
// installation.
var errNoServerAudit = errors.New("audit store has no installation scope")

// Receiver is the service that checks and records one delivery. *forge.Service
// implements it. It returns an *forge.EventRefusalError for a delivery that it
// refused.
type Receiver interface {
	ReceiveEvent(ctx context.Context, hookID string, r forge.EventRequest) (forge.EventReceipt, error)
}

// Route is one row of the route table.
type Route = route.Route[identity.Object]

// Config holds the parts of a Handler.
type Config struct {
	// Receiver checks and records a delivery. Required.
	Receiver Receiver
	// Audit takes the entries of the rejects. It must implement
	// audit.ServerStore, because a reject has no namespace. Required.
	Audit audit.Store
	// Limiter bounds the entries of the rejects. Nil means a limiter with the
	// default bounds.
	Limiter *denylimit.Limiter
	// OnRejected counts one reject by its reason class. Nil means no counter.
	OnRejected func(reason string)
	// OnAuditFailure runs when an append of an entry fails. Nil means no
	// counter.
	OnAuditFailure func()
	// Logger is required.
	Logger *slog.Logger
	// Now is the clock of the entries. Nil means time.Now.
	Now func() time.Time
}

// Handler serves the deliveries.
type Handler struct {
	receiver  Receiver
	audit     audit.ServerStore
	limiter   *denylimit.Limiter
	onReject  func(string)
	onFailure func()
	logger    *slog.Logger
	now       func() time.Time
	mux       *route.Mux
	routes    []Route
}

// NewHandler returns a Handler.
func NewHandler(cfg Config) (*Handler, error) {
	if cfg.Receiver == nil {
		return nil, fmt.Errorf("%w: receiver", ErrMissingDependency)
	}

	if cfg.Audit == nil {
		return nil, fmt.Errorf("%w: audit store", ErrMissingDependency)
	}

	// A store that cannot write the installation scope would drop the entry of
	// every reject, so the handler refuses it at the start (fail closed).
	server, ok := cfg.Audit.(audit.ServerStore)
	if !ok {
		return nil, fmt.Errorf("%w: %w", ErrMissingDependency, errNoServerAudit)
	}

	if cfg.Logger == nil {
		return nil, fmt.Errorf("%w: logger", ErrMissingDependency)
	}

	h := &Handler{
		receiver: cfg.Receiver, audit: server, limiter: cfg.Limiter, onReject: cfg.OnRejected,
		onFailure: cfg.OnAuditFailure, logger: cfg.Logger, now: cfg.Now, mux: route.NewMux(), routes: nil,
	}

	if h.limiter == nil {
		h.limiter = denylimit.New(denylimit.Config{
			Window: 0, MaxKeys: 0, MaxFlush: 0, MaxKeysPerPrincipal: 0, Now: nil,
		})
	}

	if h.now == nil {
		h.now = time.Now
	}

	h.routes = []Route{{
		Method: http.MethodPost, Path: hookPath, Scope: route.ScopePublic, Actions: nil, Resolve: nil, Serve: h.intake,
	}}

	for _, rt := range h.routes {
		h.mux.HandleFunc(rt.Pattern(), func(w http.ResponseWriter, r *http.Request) {
			// The signature is the credential: the route has no principal.
			var (
				guest  identity.Principal
				target identity.Object
			)

			rt.Serve(w, r, guest, target)
		})
	}

	return h, nil
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// Routes returns a copy of the route table.
func (h *Handler) Routes() []Route {
	out := make([]Route, len(h.routes))
	for i, rt := range h.routes {
		out[i] = rt
		out[i].Actions = append([]identity.Action(nil), rt.Actions...)
	}

	return out
}

// RegisteredPatterns returns a copy of every ServeMux pattern that the handler
// serves. The route test compares it with the route table.
func (h *Handler) RegisteredPatterns() []string {
	return h.mux.Patterns()
}

// intake serves POST /webhooks/{hookID}. It reads the body under the bound,
// asks the service, and answers the same 204 whatever the outcome.
func (h *Handler) intake(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	reason := h.receive(w, r)
	if reason != "" {
		h.reject(r.Context(), reason)
	}

	answer(w)
}

// receive reads the delivery and passes it to the service. It returns the
// reason class of a reject, or an empty text for an accepted delivery.
func (h *Handler) receive(w http.ResponseWriter, r *http.Request) string {
	// The bound holds for a body with no length too: the reader stops at the
	// bound and asks the server to close the connection.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	if err != nil {
		if tooLarge, isLimit := errors.AsType[*http.MaxBytesError](err); isLimit && tooLarge != nil {
			return forge.EventReasonTooLarge
		}

		return forge.EventReasonMalformed
	}

	_, err = h.receiver.ReceiveEvent(r.Context(), r.PathValue("hookID"), forge.EventRequest{
		Header: r.Header.Clone(), Body: body,
	})
	if err == nil {
		return ""
	}

	if refused, isRefusal := errors.AsType[*forge.EventRefusalError](err); isRefusal && refused != nil {
		return normalize(refused.Reason)
	}

	// Fail closed: an error that is no refusal is a reject of the class other.
	return forge.EventReasonOther
}

// answer writes the one answer of the route: 204 with no body and one fixed
// header, for an accepted delivery and for every reject.
func answer(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// normalize returns reason when it is one of the reason classes, and other for
// any other text. The counter and the key of the limiter hold only these
// values, so a stranger cannot grow either of them.
func normalize(reason string) string {
	switch reason {
	case forge.EventReasonUnknownHook, forge.EventReasonNoRegistration, forge.EventReasonSignature,
		forge.EventReasonTooLarge, forge.EventReasonMalformed, forge.EventReasonUnsupported,
		forge.EventReasonDuplicate, forge.EventReasonNotAssigned, forge.EventReasonNotPrimary,
		forge.EventReasonSecretNotOpened, forge.EventReasonReceiptsFull, forge.EventReasonOther:
		return reason
	default:
		return forge.EventReasonOther
	}
}

// reject counts one refused delivery and writes its limited audit entry. A
// failed append logs and counts the failure. It never changes the answer.
func (h *Handler) reject(ctx context.Context, reason string) {
	if h.onReject != nil {
		h.onReject(reason)
	}

	// The append outlives the request: a closed connection must not lose the
	// entry. The timeout bounds it.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), auditTimeout)
	defer cancel()

	key := denylimit.Key{Principal: auditSubject, Action: auditAction, Kind: auditKind, Scope: reason}
	decision := h.limiter.Allow(key)

	// The counts of earlier windows go out after the entry of this reject.
	defer h.flush(ctx, decision.Flush)

	if !decision.Write {
		return
	}

	if decision.Overflow {
		if err := h.append(ctx, h.overflowEntry(decision.Suppressed)); err != nil {
			h.limiter.Release(key, decision)
			h.failed(ctx, err)
		}

		return
	}

	if err := h.append(ctx, h.rejectEntry(reason, decision.Suppressed)); err != nil {
		// Give the write back, so the next reject of this reason tries again.
		h.limiter.Release(key, decision)
		h.failed(ctx, err)
	}
}

// flush writes the counts that the limiter held back in a window that ended.
// The keys can come from other adapters that share the limiter.
func (h *Handler) flush(ctx context.Context, pending []denylimit.Pending) {
	for i := range pending {
		if ctx.Err() != nil {
			return
		}

		if err := h.append(ctx, h.pendingEntry(&pending[i])); err != nil {
			h.failed(ctx, err)
		}
	}
}

func (h *Handler) append(ctx context.Context, e audit.Entry) error {
	if _, err := h.audit.AppendServer(ctx, e); err != nil {
		return fmt.Errorf("append installation entry: %w", err)
	}

	return nil
}

// failed logs an append that failed, by the type of its cause, and counts it.
func (h *Handler) failed(ctx context.Context, err error) {
	faultlog.LogFault(ctx, h.logger, "audit append failed for a refused Git host event", faultlog.Cause(err),
		"githubwebhook.reject")

	if h.onFailure != nil {
		h.onFailure()
	}
}

// rejectEntry returns the entry of one reject. Its text holds the reason class
// and the count of the rejects that no entry carries. It holds no header, no
// body, no hook ID and no signature, and the entry has no address, so a sender
// cannot put text in it.
func (h *Handler) rejectEntry(reason string, suppressed int) audit.Entry {
	text := "the server refused a Git host event: " + reason
	if suppressed > 0 {
		text += fmt.Sprintf("; %d further refusals since the last entry", suppressed)
	}

	return h.baseEntry(auditSubject, auditAction, auditKind, text)
}

// pendingEntry returns the entry for the count of one key of the limiter.
func (h *Handler) pendingEntry(p *denylimit.Pending) audit.Entry {
	if p.Overflow {
		return h.overflowEntry(p.Suppressed)
	}

	return h.baseEntry(
		refusal.Clean(p.Key.Principal, audit.MaxReasonLen), refusal.Clean(p.Key.Action, audit.MaxReasonLen),
		refusal.Clean(p.Key.Kind, audit.MaxReasonLen),
		fmt.Sprintf("%d further refusals in the last window were not recorded one by one", p.Suppressed),
	)
}

// overflowEntry returns the entry that stands for the rejects of keys that the
// limiter had no room to track.
func (h *Handler) overflowEntry(suppressed int) audit.Entry {
	return h.baseEntry("denial-limiter", "denials:suppressed", "denials of untracked principals",
		fmt.Sprintf("%d refusals were not recorded one by one: too many principals in one window", suppressed))
}

func (h *Handler) baseEntry(subject, action, object, reason string) audit.Entry {
	return audit.Entry{
		At: h.now().UTC(), Namespace: audit.ServerScope, Subject: subject, IDPSubject: "", TokenID: "",
		Action: action, Object: object, Version: "", ValuePath: "", ValueCount: nil, Changes: nil,
		SourceIP: "", UserAgent: "", Channel: audit.NormalizeChannel(""), Kind: audit.KindSecurity,
		Outcome: audit.OutcomeDenied, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: refusal.Clean(reason, audit.MaxReasonLen), Origin: nil,
	}
}
