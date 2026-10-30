// Package settings serves the tool-neutral API of the settings of a unit: the
// code location, the executor, the runner pool, the write-back options and
// the custom steps that a job reads at its start. A person reads the settings
// with the effective value of each field and its origin, replaces them with
// the version that the person read, resets them, and links many units at once.
// A read-only route answers a suggestion for the code location. It never sets
// a field.
//
// It is an HTTP adapter with its own route table. It never imports another
// API adapter: tools share concepts through the core alone.
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/internal/textbody"
	"opendustpan.org/opendustpan/internal/unitsettingssvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/unitsettings"
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
	// idempotencyHeader is the header of the key of an idempotent write.
	idempotencyHeader = "Idempotency-Key"
	// ifMatchHeader is the header that carries the version that a write
	// replaces.
	ifMatchHeader = "If-Match"
)

// MaxBulkUnits is the largest number of units in one bulk call.
const MaxBulkUnits = 100

// Config holds the dependencies of a Handler. Sessions checks the console
// session cookie of a request with no Authorization header; nil accepts access
// tokens only. Suggestions may be nil: the suggestion route then answers an
// empty list.
type Config struct {
	Namespaces *namespace.Service
	Units      coreinventory.Reader
	Service    *unitsettingssvc.Service
	// Suggestions gives the suggestions for the code location of a unit.
	Suggestions   unitsettingssvc.Suggester
	Authenticator identity.Authenticator
	Authorizer    identity.Authorizer
	// Audit keeps the entries of the requests that the Authorizer refuses.
	// Required: a handler that cannot write the entry of a refusal does not
	// start.
	Audit audit.Store
	// OnAuditFailure, when set, runs once for each refusal whose entry could not
	// be written, so the server can count it.
	OnAuditFailure func()
	// DenialLimiter bounds the audit entries of refused requests. The server
	// gives every adapter the same limiter. Nil means a limiter of this
	// handler only.
	DenialLimiter *denylimit.Limiter
	Sessions      *sessionauth.Checker
	Logger        *slog.Logger
	// Idempotency serves the Idempotency-Key header of a single write. Nil
	// makes every request with the header answer 400.
	Idempotency *idempotent.Middleware
	// Records keeps the replay records of the items of a bulk call. Nil makes
	// a bulk call with the header answer 400.
	Records idempotency.Store
}

// Handler serves the unit settings API under /api/dustpan/v1/.
type Handler struct {
	namespaces  *namespace.Service
	units       coreinventory.Reader
	service     *unitsettingssvc.Service
	suggestions unitsettingssvc.Suggester
	authn       identity.Authenticator
	sessions    *sessionauth.Checker
	guard       *refusal.Recorder
	logger      *slog.Logger
	mux         *patternMux
	routes      []Route
	idem        *idempotent.Middleware
	records     idempotency.Store
}

// NewHandler returns a Handler. Every Config field except Sessions and
// Suggestions is required.
func NewHandler(cfg Config) (*Handler, error) {
	switch {
	case cfg.Audit == nil:
		return nil, fmt.Errorf("%w: audit store", ErrMissingDependency)
	case cfg.Namespaces == nil:
		return nil, fmt.Errorf("%w: namespace service", ErrMissingDependency)
	case cfg.Units == nil:
		return nil, fmt.Errorf("%w: inventory reader", ErrMissingDependency)
	case cfg.Service == nil:
		return nil, fmt.Errorf("%w: unit settings service", ErrMissingDependency)
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
		service:     cfg.Service,
		suggestions: cfg.Suggestions,
		authn:       cfg.Authenticator,
		sessions:    cfg.Sessions,
		guard:       recorder.WithLimiter(cfg.DenialLimiter).WithSurface("unit-settings"),
		logger:      cfg.Logger,
		mux:         newPatternMux(),
		routes:      nil,
		idem:        cfg.Idempotency,
		records:     cfg.Records,
	}

	h.routes = h.buildRoutes()
	for i := range h.routes {
		route := h.routes[i]
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
// Authorization header uses the console session cookie, and the bool reports
// that the cookie signed it in. A request with an Authorization header never
// uses the cookie.
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

// errNotFound is the one answer for an unknown namespace, an unknown unit, and
// an object that the caller cannot read. The answer never tells that the
// object exists.
var errNotFound = errors.New("not found")

// errNoRecord reports a reset of a unit that has no settings record.
var errNoRecord = errors.New("the unit has no settings record")

// errBody reports a request body that is not valid.
var errBody = errors.New("request body is not valid")

// errPreconditionRequired reports a write with no If-Match header.
var errPreconditionRequired = errors.New("send the If-Match header with the version that you read")

// errIfMatchInvalid reports an If-Match header that is not one version.
var errIfMatchInvalid = errors.New(`If-Match must be one stored version in quotes, for example "1"`)

// errBulkSize reports a bulk call with no unit or with too many.
var errBulkSize = fmt.Errorf("a bulk call needs 1 to %d units", MaxBulkUnits)

// errKeyUnsupported reports a request with an Idempotency-Key header to a route
// that takes none, or to a server that keeps no replay records.
var errKeyUnsupported = errors.New("this route does not take the Idempotency-Key header: send the request without it")

// writeError answers err in the error shape of the tool-neutral APIs.
func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	h.writer().Write(ctx, w, err)
}

func (h *Handler) writer() problem.Writer {
	return problem.Writer{Logger: h.logger, KeepDenied: nil, Classify: classify}
}

// classify returns the answer to an error of the unit settings API, and false
// for an error that it does not know. Package problem answers a denial before
// this classifier runs, and the shared classes answer the rest.
func classify(err error) (problem.Problem, bool) {
	switch {
	case errors.Is(err, errNotFound), errors.Is(err, namespace.ErrNotFound),
		errors.Is(err, unitsettings.ErrUnknownUnit):
		return problem.New(http.StatusNotFound, errcode.UnitSettingsNotFound, errNotFound.Error()), true
	case errors.Is(err, errNoRecord), errors.Is(err, unitsettings.ErrNotFound):
		return problem.New(http.StatusNotFound, errcode.UnitSettingsNotFound, errNoRecord.Error()), true
	case errors.Is(err, errKeyUnsupported):
		return problem.New(http.StatusBadRequest, errcode.RequestIdempotencyKeyUnsupported,
			errKeyUnsupported.Error()), true
	case errors.Is(err, errPreconditionRequired):
		return problem.New(http.StatusPreconditionRequired, errcode.RequestPreconditionRequired,
			errPreconditionRequired.Error()), true
	case errors.Is(err, errIfMatchInvalid):
		return problem.New(http.StatusBadRequest, errcode.RequestIfMatchInvalid, errIfMatchInvalid.Error()), true
	case errors.Is(err, unitsettings.ErrConflict):
		return problem.New(http.StatusPreconditionFailed, errcode.RequestPreconditionFailed,
			"the settings changed since the version that you read: read them again"), true
	case errors.Is(err, errBulkSize):
		return problem.New(http.StatusBadRequest, errcode.UnitSettingsBulkSize, errBulkSize.Error()), true
	}

	return classifyBody(err)
}

// classifyBody maps the errors of the body and the rules of the settings.
func classifyBody(err error) (problem.Problem, bool) {
	if tooLarge, ok := errors.AsType[*textbody.TooLargeError](err); ok {
		return problem.New(http.StatusRequestEntityTooLarge, errcode.RequestBodyTooLarge, tooLarge.Error(),
			errcode.Quantity("maxBytes", tooLarge.Limit)), true
	}

	switch {
	case errors.Is(err, errBody), errors.Is(err, textbody.ErrEmpty), errors.Is(err, textbody.ErrText),
		errors.Is(err, textbody.ErrJSON), errors.Is(err, textbody.ErrUnreadable):
		return problem.New(http.StatusBadRequest, errcode.RequestBodyInvalid,
			"send one JSON object with a known field set"), true
	case errors.Is(err, unitsettings.ErrInvalid):
		return problem.Refused(err, http.StatusBadRequest, errcode.UnitSettingsInvalid), true
	case errors.Is(err, unitsettingssvc.ErrConnectionUnknown):
		return problem.New(http.StatusUnprocessableEntity, errcode.UnitSettingsConnectionUnknown,
			"the namespace has no Git host connection with this ID"), true
	case errors.Is(err, unitsettingssvc.ErrConnectionLacksCapability):
		return problem.Refused(err, http.StatusUnprocessableEntity, errcode.UnitSettingsConnectionLacksCapability), true
	case errors.Is(err, unitsettingssvc.ErrPoolUnknown):
		return problem.New(http.StatusUnprocessableEntity, errcode.UnitSettingsPoolUnknown,
			"the namespace has no runner pool for jobs with this ID"), true
	}

	return problem.Pass()
}

func (h *Handler) writeJSON(ctx context.Context, w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		h.logger.WarnContext(ctx, "write response failed", slog.String("error", err.Error()))
	}
}
