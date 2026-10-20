// Package objectcontext serves the tool-neutral context API: the summary,
// the runbook, the owners, the on-call text and the links of a namespace and
// of a unit. A person or a client sets them through the PATCH and DELETE
// routes. The GET routes answer the context read document, with the owners
// and the on-call text that a unit inherits from its collection and its
// namespace.
//
// It is an HTTP adapter with its own route table. It never imports another
// API adapter: tools share concepts through the core alone.
package objectcontext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/objectcontext"
)

// ErrMissingDependency reports a Config without a required dependency.
var ErrMissingDependency = errors.New("missing dependency")

const (
	tokenScheme = "token "
	// bearerScheme is the scheme of the TFE-compatible API and the
	// installation API. This API accepts it too, so one client can read
	// every API.
	bearerScheme = "Bearer "
	// channelHeader names the request header that says where a request came
	// from, for the audit entry.
	channelHeader = "Dustpan-Channel"
)

// Config holds the dependencies of a Handler. Collections is optional: with
// no collection service, a unit has no collection layer. Sessions checks the
// console session cookie of a request with no Authorization header; nil
// accepts access tokens only.
type Config struct {
	Namespaces    *namespace.Service
	Units         coreinventory.Reader
	Deleted       coreinventory.DeletedReader
	Collections   *collection.Service
	Entries       objectcontext.ContextReader
	Writer        *objectcontext.Writer
	Authenticator identity.Authenticator
	Authorizer    identity.Authorizer
	// Audit keeps the entries of the requests that the Authorizer refuses. Nil
	// writes none.
	Audit audit.Store
	// OnAuditFailure, when set, runs once for each refusal whose entry could not
	// be written, so the server can count it.
	OnAuditFailure func()
	Sessions       *sessionauth.Checker
	Logger         *slog.Logger
}

// Handler serves the context API under /api/dustpan/v1/.
type Handler struct {
	namespaces  *namespace.Service
	units       coreinventory.Reader
	deleted     coreinventory.DeletedReader
	collections *collection.Service
	entries     objectcontext.ContextReader
	writer      *objectcontext.Writer
	authn       identity.Authenticator
	sessions    *sessionauth.Checker
	authz       identity.Authorizer
	guard       *refusal.Recorder
	logger      *slog.Logger
	mux         *patternMux
	routes      []Route
}

// NewHandler returns a Handler. Every Config field except Collections and
// Sessions is required.
func NewHandler(cfg Config) (*Handler, error) {
	switch {
	case cfg.Namespaces == nil:
		return nil, fmt.Errorf("%w: namespace service", ErrMissingDependency)
	case cfg.Units == nil:
		return nil, fmt.Errorf("%w: inventory reader", ErrMissingDependency)
	case cfg.Deleted == nil:
		return nil, fmt.Errorf("%w: deleted unit reader", ErrMissingDependency)
	case cfg.Entries == nil:
		return nil, fmt.Errorf("%w: context entries", ErrMissingDependency)
	case cfg.Writer == nil:
		return nil, fmt.Errorf("%w: context writer", ErrMissingDependency)
	case cfg.Authenticator == nil:
		return nil, fmt.Errorf("%w: authenticator", ErrMissingDependency)
	case cfg.Authorizer == nil:
		return nil, fmt.Errorf("%w: authorizer", ErrMissingDependency)
	case cfg.Logger == nil:
		return nil, fmt.Errorf("%w: logger", ErrMissingDependency)
	}

	h := &Handler{
		namespaces:  cfg.Namespaces,
		units:       cfg.Units,
		deleted:     cfg.Deleted,
		collections: cfg.Collections,
		entries:     cfg.Entries,
		writer:      cfg.Writer,
		authn:       cfg.Authenticator,
		sessions:    cfg.Sessions,
		authz:       cfg.Authorizer,
		guard:       refusal.New(cfg.Authorizer, cfg.Audit, cfg.Namespaces, cfg.Logger, cfg.OnAuditFailure),
		logger:      cfg.Logger,
		mux:         newPatternMux(),
		routes:      nil,
	}

	h.routes = h.buildRoutes()
	for _, route := range h.routes {
		h.mux.HandleFunc(route.Method+" "+route.Path, h.enforce(route))
	}

	return h, nil
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// authenticate resolves the "Authorization: token <t>" or the
// "Authorization: Bearer <t>" header to a principal. A request with no
// Authorization header uses the console session cookie, and the bool
// reports that the cookie signed it in. A request with an Authorization
// header never uses the cookie.
func (h *Handler) authenticate(r *http.Request) (identity.Principal, bool, error) {
	header := r.Header.Get("Authorization")
	if header == "" {
		p, _, err := h.sessions.Authenticate(r)

		return p, err == nil, err
	}

	raw, ok := strings.CutPrefix(header, tokenScheme)
	if !ok {
		raw, ok = strings.CutPrefix(header, bearerScheme)
	}

	if !ok || raw == "" {
		return identity.Principal{}, false, identity.ErrUnauthenticated
	}

	p, err := h.authn.AuthenticateToken(r.Context(), raw)

	return p, false, err
}

// errorResponse is the error body of the tool-neutral APIs.
type errorResponse struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

// errNotFound is the one answer for an unknown namespace, an unknown unit, a
// unit of another namespace, a purged unit, and an object that the caller
// cannot read. The answer never tells that the object exists.
var errNotFound = errors.New("not found")

// errBody names the body that a context write needs.
var errBody = errors.New(
	`invalid request body: send a JSON object with at least one of "summary", "runbook", "owners", "onCall" and "links"`,
)

// errInclude names the include value that the read route needs.
var errInclude = errors.New("invalid include query parameter: send section names separated by commas")

func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	status, message := classify(err)
	if status == http.StatusInternalServerError {
		// The log names the status only, never the error text: the text may
		// hold driver detail, a token, or state.
		h.logger.ErrorContext(ctx, "request failed", slog.Int("status", status))
	}

	if errors.Is(err, namespace.ErrFenced) {
		h.logger.WarnContext(ctx, "write fenced", slog.String("error", err.Error()))
	}

	h.writeJSON(ctx, w, status, errorResponse{Code: status, Message: message})
}

// classifyAuth maps the errors of the sign-in and the authorization of a
// request.
func classifyAuth(err error) (int, string, bool) {
	switch {
	case errors.Is(err, identity.ErrRateLimited):
		return http.StatusTooManyRequests, "too many failed sign-ins from this address: try again later", true
	case errors.Is(err, identity.ErrUnauthenticated):
		return http.StatusUnauthorized, "unauthenticated", true
	case errors.Is(err, sessionauth.ErrCSRF), errors.Is(err, sessionauth.ErrOtherSite):
		return http.StatusForbidden, err.Error(), true
	case errors.Is(err, identity.ErrDenied):
		return http.StatusForbidden, "forbidden", true
	default:
		return 0, "", false
	}
}

// classify maps errors to an HTTP status and a message safe to show callers.
func classify(err error) (int, string) {
	if status, message, ok := classifyAuth(err); ok {
		return status, message
	}

	switch {
	case errors.Is(err, errNotFound), errors.Is(err, namespace.ErrNotFound):
		return http.StatusNotFound, errNotFound.Error()
	case errors.Is(err, objectcontext.ErrInvalidObject):
		// A purge removed the unit after the route resolved it.
		return http.StatusNotFound, errNotFound.Error()
	case errors.Is(err, objectcontext.ErrNotFound):
		return http.StatusNotFound, "no API context to clear"
	case errors.Is(err, errBody), errors.Is(err, errInclude):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, objectcontext.ErrInvalidContext), errors.Is(err, objectcontext.ErrUnknownSection):
		return http.StatusBadRequest, err.Error()
	default:
		return classifyConflict(err)
	}
}

// classifyConflict maps the errors of a refused write.
func classifyConflict(err error) (int, string) {
	switch {
	case errors.Is(err, objectcontext.ErrFieldFromCode):
		return http.StatusConflict, err.Error() + ": change it in the opendustpan output of the unit"
	case errors.Is(err, objectcontext.ErrUnitDeleted):
		return http.StatusConflict, "the unit is deleted: restore it before you change its context"
	case errors.Is(err, namespace.ErrFenced):
		return http.StatusConflict, namespace.ErrFenced.Error()
	case errors.Is(err, namespace.ErrNotHome):
		return http.StatusMisdirectedRequest, "namespace is not homed in this region"
	default:
		return http.StatusInternalServerError, "internal error"
	}
}

func (h *Handler) writeJSON(ctx context.Context, w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		h.logger.WarnContext(ctx, "write response failed", slog.String("error", err.Error()))
	}
}

// truncateUserAgent caps s at audit.MaxUserAgentLen bytes, at a rune
// boundary.
func truncateUserAgent(s string) string {
	if len(s) <= audit.MaxUserAgentLen {
		return s
	}

	cut := audit.MaxUserAgentLen
	for cut > 0 && !runeStart(s[cut]) {
		cut--
	}

	return s[:cut]
}

func runeStart(b byte) bool {
	const continuationMask, continuationBits = 0xC0, 0x80

	return b&continuationMask != continuationBits
}
