// Package jobs serves the tool-neutral jobs API: a person reads a job, its
// phases and their records. The API is an HTTP adapter with its own route
// table. It never imports another API adapter: tools share concepts through
// the core alone.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
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
	Jobs       job.Store
	// Starter starts jobs. Nil serves the read route only: a server that lacks
	// a port of the job service starts no job.
	Starter       job.Starter
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

// Handler serves the jobs API under /api/dustpan/v1/.
type Handler struct {
	namespaces *namespace.Service
	jobs       job.Store
	starter    job.Starter
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
	case cfg.Jobs == nil:
		return nil, fmt.Errorf("%w: job store", ErrMissingDependency)
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
		jobs:       cfg.Jobs,
		starter:    cfg.Starter,
		authn:      cfg.Authenticator,
		sessions:   cfg.Sessions,
		guard:      recorder.WithLimiter(cfg.DenialLimiter).WithSurface("jobs"),
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

// errNotFound is the one answer for an unknown namespace, an unknown job, a
// job of another namespace, and a job that the caller cannot read. The answer
// never tells that the job exists.
var errNotFound = errors.New("not found")

// maxMessageBytes is the longest refusal text that an answer sends.
const maxMessageBytes = 300

// writeError answers err in the error shape of the tool-neutral APIs. The
// classifier of this API answers the errors of a job, and package problem
// answers the rest.
func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	problem.Writer{Logger: h.logger, Classify: classify}.Write(ctx, w, err)
}

// classify returns the answer to an error of the jobs API, and false for an
// error that it does not know. A denied action is answered before the refusals
// of a job, so an error that wraps both keeps the answer of the denial. Package
// problem answers the other shared refusals in the same words for every API.
func classify(err error) (problem.Problem, bool) {
	switch {
	// A denial wins over every refusal and conflict below, so a mixed error never
	// tells the state of an object the caller has no right on.
	case errors.Is(err, identity.ErrDenied):
		return problem.Problem{
			Status: http.StatusForbidden, Code: errcode.Forbidden(err), Values: nil, Message: "forbidden",
			RetryAfter: 0, Reauthenticate: "",
		}, true
	case errors.Is(err, errNotFound), errors.Is(err, namespace.ErrNotFound), errors.Is(err, job.ErrNotFound):
		return problem.Problem{
			Status: http.StatusNotFound, Code: errcode.JobNotFound, Values: nil, Message: errNotFound.Error(),
			RetryAfter: 0, Reauthenticate: "",
		}, true
	default:
		return classifyStart(err)
	}
}

// refused returns the answer to a refusal of the job service: its own text,
// cut and cleaned, with status and code.
func refused(err error, status int, code string) (problem.Problem, bool) {
	return problem.Problem{
		Status: status, Code: code, Values: nil, Message: refusal.Clean(err.Error(), maxMessageBytes),
		RetryAfter: 0, Reauthenticate: "",
	}, true
}

// classifyStart maps the refusals of a request to start a job.
func classifyStart(err error) (problem.Problem, bool) {
	switch {
	case errors.Is(err, errBody):
		return refused(err, http.StatusBadRequest, errcode.RequestBodyInvalid)
	case errors.Is(err, idempotency.ErrInvalidKey):
		return refused(err, http.StatusBadRequest, errcode.RequestIdempotencyKeyInvalid)
	case errors.Is(err, job.ErrKeyConflict):
		return refused(err, http.StatusUnprocessableEntity, errcode.RequestIdempotencyKeyReused)
	case errors.Is(err, namespace.ErrFenced):
		return refused(err, http.StatusConflict, errcode.NamespaceFenced)
	default:
		return classifyJobRefusal(err)
	}
}

// classifyJobRefusal maps the refusals that the rules of a job give. An error
// that no case knows is answered by package problem.
func classifyJobRefusal(err error) (problem.Problem, bool) {
	switch {
	case errors.Is(err, job.ErrInvalid):
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
	}

	// Not a refusal of a job: package problem answers the error.
	return problem.Pass()
}

func (h *Handler) writeJSON(ctx context.Context, w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		h.logger.WarnContext(ctx, "write response failed", slog.String("error", err.Error()))
	}
}
