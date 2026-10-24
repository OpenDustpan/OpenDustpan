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
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/errcode"
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
	mux         *route.Mux
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
		mux:         route.NewMux(),
		routes:      nil,
	}

	h.routes = h.buildRoutes()
	for _, rt := range h.routes {
		h.mux.HandleFunc(rt.Method+" "+rt.Path, h.enforce(rt))
	}

	return h, nil
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
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
	problem.Writer{Logger: h.logger, Classify: contextErrors}.Write(ctx, w, err)
}

// contextErrors maps the errors of the context routes to an answer that is
// safe to show callers. The shared module answers the rest.
func contextErrors(err error) (problem.Problem, bool) {
	switch {
	case errors.Is(err, errNotFound), errors.Is(err, namespace.ErrNotFound):
		// One answer for every object that the caller may not see, so the
		// answer never tells that it exists.
		return fail(http.StatusNotFound, errNotFound.Error(), errcode.ContextObjectNotFound), true
	case errors.Is(err, objectcontext.ErrInvalidObject):
		// A purge removed the unit after the route resolved it.
		return fail(http.StatusNotFound, errNotFound.Error(), errcode.ContextObjectNotFound), true
	case errors.Is(err, objectcontext.ErrNotFound):
		return fail(http.StatusNotFound, "no API context to clear", errcode.ContextNotSet), true
	case errors.Is(err, errBody), errors.Is(err, errBodyText):
		return fail(http.StatusBadRequest, err.Error(), errcode.RequestBodyInvalid), true
	case errors.Is(err, errInclude):
		return fail(http.StatusBadRequest, err.Error(), errcode.RequestInvalidQuery), true
	case errors.Is(err, objectcontext.ErrInvalidContext):
		return fail(http.StatusBadRequest, invalidMessage(err), errcode.ContextInvalid), true
	case errors.Is(err, objectcontext.ErrUnknownSection):
		return fail(http.StatusBadRequest, invalidMessage(err), errcode.ContextUnknownSection), true
	case errors.Is(err, objectcontext.ErrFieldFromCode):
		return fieldFromCodeError(err), true
	case errors.Is(err, objectcontext.ErrUnitDeleted):
		return fail(http.StatusConflict, "the unit is deleted: restore it before you change its context",
			errcode.ContextUnitDeleted), true
	default:
		return problem.Pass()
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

// fieldFromCodeError maps the write of a field that the output of the code
// sets to 409 with the code context.field-from-code and the field. A refusal
// without the field has no value for the argument, so it answers 500: the
// service always names the field.
func fieldFromCodeError(err error) problem.Problem {
	field, ok := errors.AsType[*objectcontext.FieldFromCodeError](err)
	if !ok || field == nil {
		return problem.Internal()
	}

	return fail(http.StatusConflict, err.Error()+": change it in the opendustpan output of the unit",
		errcode.ContextFieldFromCode, errcode.Identifier("field", string(field.Field)))
}

// fail returns the answer of status and message with the catalog code and its
// values. When the catalog refuses the code or the values, the writer answers
// 500.
func fail(status int, message, code string, values ...errcode.Value) problem.Problem {
	return problem.Problem{
		Status: status, Code: code, Values: values, Message: message, RetryAfter: 0, Reauthenticate: "",
	}
}

func (h *Handler) writeJSON(ctx context.Context, w http.ResponseWriter, status int, body any) {
	problem.WriteJSON(ctx, h.logger, w, status, body)
}
