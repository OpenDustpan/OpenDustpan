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

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/maintenance"
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
	// DenialLimiter bounds the audit entries of refused requests. The server
	// gives every adapter the same limiter, so its bound on keys holds for all
	// of them. Nil means a limiter of this handler only.
	DenialLimiter *denylimit.Limiter
	Sessions      *sessionauth.Checker
	Logger        *slog.Logger
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
	case cfg.Audit == nil:
		return nil, fmt.Errorf("%w: audit store", ErrMissingDependency)
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

	recorder, err := refusal.New(cfg.Authorizer, cfg.Audit, cfg.Namespaces, cfg.Logger, cfg.OnAuditFailure)
	if err != nil {
		return nil, fmt.Errorf("denial recorder: %w", err)
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
		guard:       recorder.WithLimiter(cfg.DenialLimiter).WithSurface("context"),
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
	header := apidesc.Header(r, "Authorization")
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
	// ErrorCode is a stable machine code of the catalog.
	ErrorCode string `json:"errorCode,omitempty"`
	// ErrorArgs are the arguments of the code.
	ErrorArgs map[string]any `json:"errorArgs,omitempty"`
}

// errNotFound is the one answer for an unknown namespace, an unknown unit, a
// unit of another namespace, a purged unit, and an object that the caller
// cannot read. The answer never tells that the object exists, so it names no
// value of the path. It names the mistake that people make most: a unit name
// in place of the unit ID.
var errNotFound = errors.New("not found: no namespace or unit that you can read has this path. " +
	"A unit path takes the unit ID (stk-... or ws-...), not the name. " +
	"To list the unit IDs, send GET /api/dustpan/v1/namespaces/{namespace}/units")

// errBody names the body that a context write needs.
var errBody = errors.New(
	`invalid request body: send a JSON object with at least one of "summary", "runbook", "owners", "onCall" and "links"`,
)

// errBodyText is the answer for a body that is not valid UTF-8 or that holds
// a lone surrogate escape. The server refuses the text and does not change it.
var errBodyText = errors.New(
	`invalid request body: send valid UTF-8 text, with no invalid byte and no lone surrogate escape such as \ud800`,
)

// errInclude names the include value that the read route needs.
var errInclude = errors.New("invalid include query parameter: send section names separated by commas")

func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	if errors.Is(err, namespace.ErrFenced) {
		h.logger.WarnContext(ctx, "write fenced", slog.String("error", err.Error()))
	}

	h.writeFailure(ctx, w, classify(err))
}

// classifyAuth maps the errors of the sign-in and the authorization of a
// request.
func classifyAuth(err error) (failure, bool) {
	switch {
	case errors.Is(err, identity.ErrRateLimited):
		return fail(http.StatusTooManyRequests, "too many failed sign-ins from this address: try again later",
			errcode.AuthRateLimited), true
	case errors.Is(err, identity.ErrUnauthenticated):
		return fail(http.StatusUnauthorized, "unauthenticated", errcode.AuthUnauthenticated), true
	case errors.Is(err, sessionauth.ErrCSRF), errors.Is(err, sessionauth.ErrOtherSite):
		return fail(http.StatusForbidden, err.Error(), errcode.Forbidden(err)), true
	case errors.Is(err, identity.ErrDenied):
		return fail(http.StatusForbidden, "forbidden", errcode.Forbidden(err)), true
	}

	var none failure

	return none, false
}

// classify maps errors to an HTTP status, a message safe to show callers and
// a code of the catalog.
func classify(err error) failure {
	if f, ok := classifyAuth(err); ok {
		return f
	}

	switch {
	case errors.Is(err, errNotFound), errors.Is(err, namespace.ErrNotFound):
		return fail(http.StatusNotFound, errNotFound.Error(), errcode.ContextObjectNotFound)
	case errors.Is(err, objectcontext.ErrInvalidObject):
		// A purge removed the unit after the route resolved it.
		return fail(http.StatusNotFound, errNotFound.Error(), errcode.ContextObjectNotFound)
	case errors.Is(err, objectcontext.ErrNotFound):
		return fail(http.StatusNotFound, "no API context to clear", errcode.ContextNotSet)
	case errors.Is(err, errBody), errors.Is(err, errBodyText):
		return fail(http.StatusBadRequest, err.Error(), errcode.RequestBodyInvalid)
	case errors.Is(err, errInclude):
		return fail(http.StatusBadRequest, err.Error(), errcode.RequestInvalidQuery)
	case errors.Is(err, objectcontext.ErrInvalidContext):
		return fail(http.StatusBadRequest, invalidMessage(err), errcode.ContextInvalid)
	case errors.Is(err, objectcontext.ErrUnknownSection):
		return fail(http.StatusBadRequest, invalidMessage(err), errcode.ContextUnknownSection)
	default:
		return classifyConflict(err)
	}
}

// invalidMessage returns the text of a 400 for an invalid context or section.
// For a *RuleError it is the text of the rule only, so the call chain of the
// service and the store, which differs between the store adapters, never
// reaches the client.
func invalidMessage(err error) string {
	if rule, ok := errors.AsType[*objectcontext.RuleError](err); ok {
		return rule.Error()
	}

	return err.Error()
}

// classifyConflict maps the errors of a refused write.
func classifyConflict(err error) failure {
	switch {
	case errors.Is(err, objectcontext.ErrFieldFromCode):
		return classifyFieldFromCode(err)
	case errors.Is(err, objectcontext.ErrUnitDeleted):
		return fail(http.StatusConflict, "the unit is deleted: restore it before you change its context",
			errcode.ContextUnitDeleted)
	case errors.Is(err, namespace.ErrFenced):
		return fail(http.StatusConflict, namespace.ErrFenced.Error(), errcode.NamespaceFenced)
	case errors.Is(err, namespace.ErrNotHome):
		return fail(http.StatusMisdirectedRequest, "namespace is not homed in this region", errcode.NamespaceNotHome)
	case errors.Is(err, maintenance.ErrInMaintenance):
		return fail(http.StatusServiceUnavailable, maintenance.ErrInMaintenance.Error()+": try again later",
			errcode.MaintenanceInMaintenance)
	default:
		return fail(http.StatusInternalServerError, "internal error", errcode.ServerInternal)
	}
}

// classifyFieldFromCode maps the write of a field that the output of the code
// sets to 409 with the code context.field-from-code and the field. A refusal
// without the field has no value for the argument, so it answers 500: the
// service always names the field.
func classifyFieldFromCode(err error) failure {
	field, ok := errors.AsType[*objectcontext.FieldFromCodeError](err)
	if !ok || field == nil {
		return fail(http.StatusInternalServerError, "internal error", errcode.ServerInternal)
	}

	return fail(http.StatusConflict, err.Error()+": change it in the opendustpan output of the unit",
		errcode.ContextFieldFromCode, errcode.Identifier("field", string(field.Field)))
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
