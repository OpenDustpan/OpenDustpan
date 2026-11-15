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

	"opendustpan.org/opendustpan/internal/api/admit"
	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/manuallock"
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
	Manager *trigger.Manager
	// Workers tells the start of a durable operation whether a worker runs.
	// Nil admits every start.
	Workers       admit.Source
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
	workers    admit.Source
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
		workers:    cfg.Workers,
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

// errNotFound is the one answer for an unknown namespace, an unknown unit, an
// unknown trigger, and an object that the caller cannot read. The answer never
// tells that the object exists.
var errNotFound = errors.New("not found")

// errBrokenFrequency is the error of a stored schedule trigger whose frequency
// has no expression. The save checks the frequency, and the store refuses such
// a row when it reads it, so the record is broken. The text does not wrap
// trigger.ErrInvalid: the answer is a failure of the server, not a request that
// the caller must fix.
//
// Defense in depth: the store validates each row on load; this check stays so
// that a row written outside the store, or by another version, never answers
// as a valid trigger. It does not rely on the store.
var errBrokenFrequency = errors.New("stored schedule frequency has no expression")

// errBody reports a request body that is not valid.
var errBody = errors.New("request body is not valid")

// errKeyUnsupported reports a write with an Idempotency-Key header to a route
// that takes none. Only the fire route takes the key.
var errKeyUnsupported = errors.New("this route does not take the Idempotency-Key header: send the request without it")

// writeError answers err in the error shape of the tool-neutral APIs. The
// classifier of this API answers the errors of a trigger, and package problem
// answers the rest.
func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	problem.Writer{Logger: h.logger, KeepDenied: keepDenied, Classify: classify}.Write(ctx, w, err)
}

// keepDenied answers the one class of the triggers API that wraps a denial and
// keeps its own code: a save whose last saver lost access.
func keepDenied(err error) (problem.Problem, bool) {
	if errors.Is(err, trigger.ErrSaverDenied) {
		return problem.New(http.StatusForbidden, errcode.TriggerSaverDenied,
			"the person who saves the trigger needs a stored principal that holds the actions to change "+
				"the unit and to start a plan on it, and to read the source unit of a follows trigger"), true
	}

	return problem.Pass()
}

// classify returns the answer to an error of the triggers API, and false for an
// error that it does not know. Package problem answers a denial before this
// classifier runs, and the shared classes answer the rest.
func classify(err error) (problem.Problem, bool) {
	// The refusal of a cycle lists the units of the loop. A denial is answered
	// before this classifier, so a caller with no right on the loop gets the
	// same 403 as for any other refusal.
	if cycle, ok := errors.AsType[*trigger.CycleError](err); ok {
		p := problem.New(http.StatusConflict, errcode.TriggerCycle, cycleMessage(cycle))
		p.Units = cycleUnits(cycle)

		return p, true
	}

	switch {
	case errors.Is(err, errNotFound), errors.Is(err, namespace.ErrNotFound), errors.Is(err, trigger.ErrNotFound):
		return problem.New(http.StatusNotFound, errcode.TriggerNotFound, errNotFound.Error()), true
	}

	return classifyRule(err)
}

// classifyRule maps the refusals of the trigger and job rules.
func classifyRule(err error) (problem.Problem, bool) {
	if manual, ok := errors.AsType[*manuallock.LockedError](err); ok {
		return classifyLocked(manual), true
	}

	switch {
	case errors.Is(err, errKeyUnsupported):
		return problem.New(
			http.StatusBadRequest,
			errcode.RequestIdempotencyKeyUnsupported,
			errKeyUnsupported.Error(),
		), true
	case errors.Is(err, idempotency.ErrInvalidKey):
		return problem.Refused(err, http.StatusBadRequest, errcode.RequestIdempotencyKeyInvalid), true
	case errors.Is(err, errBody):
		return problem.Refused(err, http.StatusBadRequest, errcode.RequestBodyInvalid), true
	case errors.Is(err, trigger.ErrInvalid):
		return problem.Refused(err, http.StatusBadRequest, errcode.TriggerInvalid), true
	case errors.Is(err, trigger.ErrDisabled):
		return problem.New(
			http.StatusConflict,
			errcode.TriggerDisabled,
			"the trigger is off: switch it on before you fire it",
		), true
	}

	return classifyJob(err)
}

// classifyLocked maps the refusal of a manual lock. The arguments name the
// holder, the time and the locked object. They never hold the reason of the
// lock.
func classifyLocked(manual *manuallock.LockedError) problem.Problem {
	a, err := refusal.Locked(manual)

	return problem.Built(http.StatusConflict, refusal.Clean(refusal.LockedText(manual), refusal.MaxLockedTextBytes),
		errcode.LockLocked, a, err)
}

// classifyJob maps the refusals of the job service to a manual fire.
func classifyJob(err error) (problem.Problem, bool) {
	switch {
	case errors.Is(err, job.ErrInvalid):
		return problem.Refused(err, http.StatusBadRequest, errcode.JobInvalid), true
	case errors.Is(err, job.ErrUnsupported):
		return problem.RefusedText(problem.Detail(err, job.ErrUnsupported),
			http.StatusUnprocessableEntity, errcode.JobUnsupported), true
	case errors.Is(err, job.ErrPolicyDenied):
		return problem.Refused(err, http.StatusForbidden, errcode.JobPolicyDenied), true
	case errors.Is(err, job.ErrGateEnforced):
		return problem.Refused(err, http.StatusForbidden, errcode.JobGateEnforced), true
	case errors.Is(err, job.ErrAgentDenied):
		return problem.Refused(err, http.StatusForbidden, errcode.JobAgentDenied), true
	case errors.Is(err, job.ErrKeyConflict):
		return problem.Refused(err, http.StatusConflict, errcode.TriggerFireConflict), true
	case errors.Is(err, job.ErrFrozen):
		return problem.Refused(err, http.StatusConflict, errcode.JobFrozen), true
	case errors.Is(err, job.ErrLimit):
		return problem.Refused(err, http.StatusTooManyRequests, errcode.JobLimit), true
	case errors.Is(err, job.ErrNotFound):
		return problem.Refused(err, http.StatusNotFound, errcode.TriggerNotFound), true
	}

	return problem.Pass()
}

func (h *Handler) writeJSON(ctx context.Context, w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		h.logger.WarnContext(ctx, "write response failed", slog.String("error", err.Error()))
	}
}
