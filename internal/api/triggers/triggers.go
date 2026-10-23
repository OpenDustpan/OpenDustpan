// Package triggers serves the tool-neutral run trigger API: a person saves a
// trigger on a unit, reads, lists and deletes it, and fires it now. A trigger
// starts a plan of its unit on its own (after another unit applies, on a
// schedule, or on a push to a review request), and a manual fire tests it.
//
// It is an HTTP adapter with its own route table. It never imports another
// API adapter: tools share concepts through the core alone.
package triggers

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
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trigger"
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

	// reservedKeyPrefix starts the idempotency keys that the server makes for
	// its own drift checks. A client key does not start with it.
	reservedKeyPrefix = "drift-"
)

// Config holds the dependencies of a Handler. Sessions checks the console
// session cookie of a request with no Authorization header; nil accepts access
// tokens only.
type Config struct {
	Namespaces *namespace.Service
	Units      coreinventory.Reader
	// Store reads the trigger of a path before the checks of the route.
	Store trigger.Store
	// Manager saves, reads, deletes and fires triggers, and checks the actions
	// of the caller at each call.
	Manager       *trigger.Manager
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

// Handler serves the trigger API under /api/dustpan/v1/.
type Handler struct {
	namespaces *namespace.Service
	units      coreinventory.Reader
	store      trigger.Store
	manager    *trigger.Manager
	authn      identity.Authenticator
	sessions   *sessionauth.Checker
	guard      *refusal.Recorder
	logger     *slog.Logger
	mux        *patternMux
	routes     []Route
}

// NewHandler returns a Handler. Every Config field except Sessions is
// required.
func NewHandler(cfg Config) (*Handler, error) {
	switch {
	case cfg.Audit == nil:
		return nil, fmt.Errorf("%w: audit store", ErrMissingDependency)
	case cfg.Namespaces == nil:
		return nil, fmt.Errorf("%w: namespace service", ErrMissingDependency)
	case cfg.Units == nil:
		return nil, fmt.Errorf("%w: inventory reader", ErrMissingDependency)
	case cfg.Store == nil:
		return nil, fmt.Errorf("%w: trigger store", ErrMissingDependency)
	case cfg.Manager == nil:
		return nil, fmt.Errorf("%w: trigger manager", ErrMissingDependency)
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
		store:      cfg.Store,
		manager:    cfg.Manager,
		authn:      cfg.Authenticator,
		sessions:   cfg.Sessions,
		guard:      recorder.WithLimiter(cfg.DenialLimiter).WithSurface("triggers"),
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

// errorResponse is the error body of the tool-neutral APIs. A refusal of a
// save that closes a loop of units lists the units of the loop in order.
type errorResponse struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
	// ErrorCode is a stable code for a program.
	ErrorCode string         `json:"errorCode,omitempty"`
	Units     []unitResponse `json:"units,omitempty"`
}

// errNotFound is the one answer for an unknown namespace, an unknown unit, an
// unknown trigger, and an object that the caller cannot read. The answer never
// tells that the object exists.
var errNotFound = errors.New("not found")

// errBody reports a request body that is not valid.
var errBody = errors.New("request body is not valid")

// errKeyUnsupported reports a write with an Idempotency-Key header to a route
// that takes none. Only the fire route takes the key.
var errKeyUnsupported = errors.New("this route does not take the Idempotency-Key header: send the request without it")

// Codes of the refusals of this API.
const (
	codeBodyInvalid  = "request.body-invalid"
	codeKeyRefused   = "request.idempotency-key-unsupported"
	codeKeyInvalid   = "request.idempotency-key-invalid"
	codeInvalid      = "trigger.invalid"
	codeCycle        = "trigger.cycle"
	codeDisabled     = "trigger.disabled"
	codeSaverDenied  = "trigger.saver-denied"
	codeFenced       = "namespace.fenced"
	codeFrozen       = "job.frozen"
	codeLimit        = "job.limit"
	codePolicyDenied = "job.policy-denied"
	codeUnsupported  = "job.unsupported"
	codeGateEnforced = "job.gate-enforced"
	codeAgentDenied  = "job.agent-denied"
	codeFireConflict = "trigger.fire-conflict"
	codeInvalidJob   = "job.invalid"
	maxMessageBytes  = 300
)

func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	body := h.classify(err)
	if body.Code == http.StatusInternalServerError {
		// The log names the status only, never the error text: the text may
		// hold driver detail, a token, or state.
		h.logger.ErrorContext(ctx, "request failed", slog.Int("status", body.Code))
	}

	h.writeJSON(ctx, w, body.Code, body)
}

// classify maps errors to an HTTP status, a stable code and a message safe to
// show callers.
func (h *Handler) classify(err error) errorResponse {
	switch {
	case errors.Is(err, identity.ErrRateLimited):
		return errorResponse{
			Code: http.StatusTooManyRequests, Message: "too many failed sign-ins from this address: try again later",
			ErrorCode: "", Units: nil,
		}
	case errors.Is(err, identity.ErrUnauthenticated):
		return errorResponse{Code: http.StatusUnauthorized, Message: "unauthenticated", ErrorCode: "", Units: nil}
	case errors.Is(err, sessionauth.ErrCSRF), errors.Is(err, sessionauth.ErrOtherSite):
		return errorResponse{Code: http.StatusForbidden, Message: err.Error(), ErrorCode: "", Units: nil}
	case errors.Is(err, trigger.ErrSaverDenied):
		return errorResponse{
			Code: http.StatusForbidden, ErrorCode: codeSaverDenied, Units: nil,
			Message: "the person who saves the trigger needs a stored principal that holds the actions to change " +
				"the unit and to start a plan on it, and to read the source unit of a follows trigger",
		}
	case errors.Is(err, identity.ErrDenied):
		return errorResponse{Code: http.StatusForbidden, Message: "forbidden", ErrorCode: "", Units: nil}
	case errors.Is(err, errNotFound), errors.Is(err, namespace.ErrNotFound), errors.Is(err, trigger.ErrNotFound):
		return errorResponse{
			Code: http.StatusNotFound, Message: errNotFound.Error(), ErrorCode: "", Units: nil,
		}
	case errors.Is(err, namespace.ErrNotHome):
		return errorResponse{
			Code: http.StatusMisdirectedRequest, Message: "namespace is not homed in this region", ErrorCode: "",
			Units: nil,
		}
	case errors.Is(err, maintenance.ErrInMaintenance):
		return errorResponse{
			Code: http.StatusServiceUnavailable, Message: maintenance.ErrInMaintenance.Error() + ": try again later",
			ErrorCode: "", Units: nil,
		}
	}

	return classifyRule(err)
}

// classifyRule maps the refusals of the trigger and job rules.
func classifyRule(err error) errorResponse {
	var cycle *trigger.CycleError

	switch {
	case errors.As(err, &cycle):
		return errorResponse{
			Code: http.StatusConflict, ErrorCode: codeCycle, Message: refusal.Clean(cycle.Error(), maxMessageBytes),
			Units: cycleUnits(cycle),
		}
	case errors.Is(err, errKeyUnsupported):
		return errorResponse{
			Code: http.StatusBadRequest, ErrorCode: codeKeyRefused, Message: errKeyUnsupported.Error(), Units: nil,
		}
	case errors.Is(err, idempotency.ErrInvalidKey):
		return errorResponse{
			Code:      http.StatusBadRequest,
			ErrorCode: codeKeyInvalid,
			Message:   refusal.Clean(err.Error(), maxMessageBytes),
			Units:     nil,
		}
	case errors.Is(err, errBody):
		return errorResponse{
			Code:      http.StatusBadRequest,
			ErrorCode: codeBodyInvalid,
			Message:   refusal.Clean(err.Error(), maxMessageBytes),
			Units:     nil,
		}
	case errors.Is(err, trigger.ErrInvalid):
		return errorResponse{
			Code: http.StatusBadRequest, ErrorCode: codeInvalid, Message: refusal.Clean(err.Error(), maxMessageBytes),
			Units: nil,
		}
	case errors.Is(err, trigger.ErrDisabled):
		return errorResponse{
			Code: http.StatusConflict, ErrorCode: codeDisabled, Units: nil,
			Message: "the trigger is off: switch it on before you fire it",
		}
	case errors.Is(err, namespace.ErrFenced):
		return errorResponse{
			Code: http.StatusConflict, ErrorCode: codeFenced, Message: namespace.ErrFenced.Error(), Units: nil,
		}
	}

	if status, code, ok := classifyJob(err); ok {
		return errorResponse{
			Code:      status,
			ErrorCode: code,
			Message:   refusal.Clean(err.Error(), maxMessageBytes),
			Units:     nil,
		}
	}

	return errorResponse{Code: http.StatusInternalServerError, Message: "internal error", ErrorCode: "", Units: nil}
}

// classifyJob maps the refusals of the job service to a manual fire.
func classifyJob(err error) (int, string, bool) {
	switch {
	case errors.Is(err, job.ErrInvalid):
		return http.StatusBadRequest, codeInvalidJob, true
	case errors.Is(err, job.ErrUnsupported):
		return http.StatusUnprocessableEntity, codeUnsupported, true
	case errors.Is(err, job.ErrPolicyDenied):
		return http.StatusForbidden, codePolicyDenied, true
	case errors.Is(err, job.ErrGateEnforced):
		return http.StatusForbidden, codeGateEnforced, true
	case errors.Is(err, job.ErrAgentDenied):
		return http.StatusForbidden, codeAgentDenied, true
	case errors.Is(err, job.ErrKeyConflict):
		return http.StatusConflict, codeFireConflict, true
	case errors.Is(err, job.ErrFrozen):
		return http.StatusConflict, codeFrozen, true
	case errors.Is(err, job.ErrLimit):
		return http.StatusTooManyRequests, codeLimit, true
	case errors.Is(err, job.ErrNotFound):
		return http.StatusNotFound, "", true
	default:
		return 0, "", false
	}
}

func (h *Handler) writeJSON(ctx context.Context, w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		h.logger.WarnContext(ctx, "write response failed", slog.String("error", err.Error()))
	}
}
