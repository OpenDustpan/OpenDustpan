// Package drift serves the tool-neutral drift API: a person starts a drift
// check of a unit and reads the latest drift result of the unit. The API is an
// HTTP adapter with its own route table. It never imports another API
// adapter: tools share concepts through the core alone.
package drift

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
	"opendustpan.org/opendustpan/pkg/drift"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ErrMissingDependency reports a Config without a required dependency.
var ErrMissingDependency = errors.New("missing dependency")

const (
	tokenScheme = "token "
	// bearerScheme is the scheme of the TFE-compatible API and the
	// installation API. This API accepts it too, so one client can read
	// every API.
	bearerScheme = "Bearer "
)

// Config holds the dependencies of a Handler. Sessions checks the console
// session cookie of a request with no Authorization header; nil accepts
// access tokens only.
type Config struct {
	Namespaces *namespace.Service
	// Units finds a live unit by its unit ID.
	Units coreinventory.Reader
	// Results reads the latest drift result of a unit.
	Results drift.Store
	// Checker starts the drift checks. Nil serves the read route only: a
	// server that lacks a port of the job service starts no job.
	Checker       drift.Checker
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
	// gives every adapter the same limiter, so its bound on keys holds for all
	// of them. Nil means a limiter of this handler only.
	DenialLimiter *denylimit.Limiter
	Sessions      *sessionauth.Checker
	Logger        *slog.Logger
}

// Handler serves the drift API under /api/dustpan/v1/.
type Handler struct {
	namespaces *namespace.Service
	units      coreinventory.Reader
	results    drift.Store
	checker    drift.Checker
	authn      identity.Authenticator
	sessions   *sessionauth.Checker
	guard      *refusal.Recorder
	logger     *slog.Logger
	mux        *patternMux
	routes     []Route
}

// NewHandler returns a Handler. Every Config field except Checker and
// Sessions is required.
func NewHandler(cfg Config) (*Handler, error) {
	switch {
	case cfg.Audit == nil:
		return nil, fmt.Errorf("%w: audit store", ErrMissingDependency)
	case cfg.Namespaces == nil:
		return nil, fmt.Errorf("%w: namespace service", ErrMissingDependency)
	case cfg.Units == nil:
		return nil, fmt.Errorf("%w: inventory reader", ErrMissingDependency)
	case cfg.Results == nil:
		return nil, fmt.Errorf("%w: drift store", ErrMissingDependency)
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
		namespaces: cfg.Namespaces,
		units:      cfg.Units,
		results:    cfg.Results,
		checker:    cfg.Checker,
		authn:      cfg.Authenticator,
		sessions:   cfg.Sessions,
		guard:      recorder.WithLimiter(cfg.DenialLimiter).WithSurface("drift"),
		logger:     cfg.Logger,
		mux:        newPatternMux(),
		routes:     nil,
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
	// ErrorCode is the stable code of the catalog for a program, and ErrorArgs
	// holds the values that the message names.
	ErrorCode string         `json:"errorCode,omitempty"`
	ErrorArgs map[string]any `json:"errorArgs,omitempty"`
}

// failure is the answer to an error before the catalog checks it: the status,
// the message, the code and the values of the arguments of the code.
type failure struct {
	status  int
	message string
	code    string
	args    []errcode.Value
}

// errNotFound is the one answer for an unknown namespace, an unknown unit, a
// unit of another namespace, a purged unit, and a unit that the caller cannot
// read. The answer never tells that the unit exists.
var errNotFound = errors.New("not found")

// errBody reports a request body that is not valid.
var errBody = errors.New("request body is not valid")

const (
	maxMessageBytes = 300
	internalMessage = "internal error"
)

func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	f := classify(err)

	answer, codeErr := errcode.New(f.code, f.args...)
	if codeErr != nil {
		// A code outside the catalog or a wrong argument is a bug of this
		// adapter. The log names the code only, never the error text.
		h.logger.ErrorContext(ctx, "error code refused", slog.String("code", f.code))

		f = failure{
			status:  http.StatusInternalServerError,
			message: internalMessage,
			code:    errcode.ServerInternal,
			args:    nil,
		}
		answer = errcode.Internal()
	}

	if f.status == http.StatusInternalServerError {
		// The log names the status only, never the error text: the text may
		// hold driver detail, a token, or state.
		h.logger.ErrorContext(ctx, "request failed", slog.Int("status", f.status))
	}

	h.writeAnswer(ctx, w, f.status, f.message, answer)
}

// writeAnswer writes the error body of status with message, and with the code
// and the arguments of answer.
func (h *Handler) writeAnswer(
	ctx context.Context,
	w http.ResponseWriter,
	status int,
	message string,
	answer errcode.Answer,
) {
	h.writeJSON(ctx, w, status, errorResponse{
		Message: message, Code: status, ErrorCode: answer.Code(), ErrorArgs: answer.Args(),
	})
}

// classify maps errors to an HTTP status, a code of the catalog and a message
// safe to show callers. A refusal of the job service shows its own text, which
// the service and its ports write without detail of a driver or a secret. The
// text is cut and cleaned before it leaves.
func classify(err error) failure {
	switch {
	case errors.Is(err, identity.ErrRateLimited):
		return failure{
			status:  http.StatusTooManyRequests,
			message: "too many failed sign-ins from this address: try again later",
			code:    errcode.AuthRateLimited,
			args:    nil,
		}
	case errors.Is(err, identity.ErrUnauthenticated):
		return failure{
			status: http.StatusUnauthorized, message: "unauthenticated", code: errcode.AuthUnauthenticated, args: nil,
		}
	case errors.Is(err, sessionauth.ErrCSRF), errors.Is(err, sessionauth.ErrOtherSite):
		return failure{status: http.StatusForbidden, message: err.Error(), code: errcode.Forbidden(err), args: nil}
	case errors.Is(err, errNotFound), errors.Is(err, namespace.ErrNotFound), errors.Is(err, job.ErrNotFound):
		return failure{
			status: http.StatusNotFound, message: errNotFound.Error(), code: errcode.UnitNotFound, args: nil,
		}
	case errors.Is(err, namespace.ErrNotHome):
		return failure{
			status:  http.StatusMisdirectedRequest,
			message: "namespace is not homed in this region",
			code:    errcode.NamespaceNotHome,
			args:    nil,
		}
	case errors.Is(err, maintenance.ErrInMaintenance):
		return failure{
			status:  http.StatusServiceUnavailable,
			message: maintenance.ErrInMaintenance.Error() + ": try again later",
			code:    errcode.MaintenanceInMaintenance,
			args:    nil,
		}
	default:
		// The refusals of a start come before identity.ErrDenied: an error
		// that wraps both keeps the code of its cause.
		return classifyStart(err)
	}
}

// refused returns the answer to a refusal of the job service: its own text,
// cut and cleaned, with status and code.
func refused(err error, status int, code string) failure {
	return failure{status: status, message: refusal.Clean(err.Error(), maxMessageBytes), code: code, args: nil}
}

// classifyStart maps the refusals of a request to start a drift check.
func classifyStart(err error) failure {
	switch {
	case errors.Is(err, errBody):
		return refused(err, http.StatusBadRequest, errcode.RequestBodyInvalid)
	case errors.Is(err, namespace.ErrFenced):
		return refused(err, http.StatusConflict, errcode.NamespaceFenced)
	case errors.Is(err, job.ErrKeyConflict):
		// Two starts of one unit in one minute share a key. A second start with
		// another pool, location or reason is another request with that key.
		return refused(err, http.StatusConflict, errcode.DriftStartConflict)
	case errors.Is(err, job.ErrInvalid), errors.Is(err, drift.ErrInvalid):
		return refused(err, http.StatusBadRequest, errcode.JobInvalid)
	case errors.Is(err, job.ErrUnsupported):
		return refused(err, http.StatusUnprocessableEntity, errcode.JobUnsupported)
	case errors.Is(err, job.ErrPolicyDenied):
		return refused(err, http.StatusForbidden, errcode.JobPolicyDenied)
	case errors.Is(err, job.ErrGateEnforced):
		return refused(err, http.StatusForbidden, errcode.JobGateEnforced)
	case errors.Is(err, job.ErrAgentDenied):
		return refused(err, http.StatusForbidden, errcode.JobAgentDenied)
	case errors.Is(err, job.ErrFrozen):
		return refused(err, http.StatusConflict, errcode.JobFrozen)
	case errors.Is(err, job.ErrLimit):
		return refused(err, http.StatusTooManyRequests, errcode.JobLimit)
	default:
		return classifyOther(err)
	}
}

// classifyOther maps an error that is no refusal of a start: a denied action
// answers 403, and an error that no case knows 500 with no text of the error.
func classifyOther(err error) failure {
	if errors.Is(err, identity.ErrDenied) {
		return failure{status: http.StatusForbidden, message: "forbidden", code: errcode.Forbidden(err), args: nil}
	}

	return failure{
		status: http.StatusInternalServerError, message: internalMessage, code: errcode.ServerInternal, args: nil,
	}
}

func (h *Handler) writeJSON(ctx context.Context, w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		h.logger.WarnContext(ctx, "write response failed", slog.String("error", err.Error()))
	}
}
