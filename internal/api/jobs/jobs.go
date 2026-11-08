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
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/secret"
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
	// Units finds a live unit by its stable ID. The list reads it to check
	// whether the caller may list the unit of a damaged row. Nil counts no
	// damaged row: a row whose unit cannot be resolved is never counted.
	Units coreinventory.Reader
	// Artifacts opens the logs of jobs. Nil serves no log route: a server
	// that lacks a port of the artifact reader has no log to read.
	Artifacts job.ArtifactReader
	// Starter starts jobs. Nil serves the read route only: a server that lacks
	// a port of the job service starts no job.
	Starter job.Starter
	// Workers tells the start of a durable operation whether a worker runs.
	// Nil admits every start.
	Workers admit.Source
	// Pools names the runner pool of a job in the answer. Nil answers the pool
	// ID only: a server without runner stores has no pool to name.
	Pools         runner.PoolReader
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
	// Logs sets the limits of the log streams. Nil takes every default.
	Logs *LogOptions
	// Cursors seals the cursor of a page token, so a token names no job. With
	// no sealer, the job list reads every job to the end of the list, and the
	// token holds the ID of a job that the caller can read.
	Cursors secret.Wrapper
}

// Handler serves the jobs API under /api/dustpan/v1/.
type Handler struct {
	namespaces *namespace.Service
	jobs       job.Store
	units      coreinventory.Reader
	artifacts  job.ArtifactReader
	authz      identity.Authorizer
	logs       LogOptions
	streams    *streamLimiter
	cursors    secret.Wrapper
	starter    job.Starter
	workers    admit.Source
	pools      runner.PoolReader
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
		units:      cfg.Units,
		artifacts:  cfg.Artifacts,
		authz:      cfg.Authorizer,
		logs:       logLimits(cfg.Logs),
		streams:    nil,
		cursors:    cfg.Cursors,
		starter:    cfg.Starter,
		workers:    cfg.Workers,
		pools:      cfg.Pools,
		authn:      cfg.Authenticator,
		sessions:   cfg.Sessions,
		guard:      recorder.WithLimiter(cfg.DenialLimiter).WithSurface("jobs"),
		logger:     cfg.Logger,
		mux:        newPatternMux(),
		routes:     nil,
	}

	h.streams = newStreamLimiter(h.logs.PerPrincipal, h.logs.Total)
	h.routes = h.buildRoutes()
	for i := range h.routes {
		route := &h.routes[i]
		h.mux.HandleFunc(route.Method+" "+route.Path, h.enforce(*route))
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

// writeError answers err in the error shape of the tool-neutral APIs. The
// classifier of this API answers the errors of a job, and package problem
// answers the rest.
func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	problem.Writer{Logger: h.logger, KeepDenied: nil, Classify: classify}.Write(ctx, w, err)
}

// classify returns the answer to an error of the jobs API, and false for an
// error that it does not know. Package problem answers a denial before this
// classifier runs, and the other shared refusals in the same words for every
// API.
func classify(err error) (problem.Problem, bool) {
	switch {
	case errors.Is(err, errNotFound), errors.Is(err, namespace.ErrNotFound), errors.Is(err, job.ErrNotFound):
		return problem.New(http.StatusNotFound, errcode.JobNotFound, errNotFound.Error()), true
	case errors.Is(err, errTooManyStreams):
		return problem.New(http.StatusTooManyRequests, errcode.JobLimit, errTooManyStreams.Error()), true
	case errors.Is(err, errLogNotAvailable):
		return problem.New(http.StatusNotFound, errcode.JobLogNotAvailable, errLogNotAvailable.Error()), true
	case errors.Is(err, errPageToken):
		return problem.New(http.StatusBadRequest, errcode.RequestPageTokenInvalid, err.Error()), true
	case errors.Is(err, errInvalidQuery):
		return problem.New(http.StatusBadRequest, errcode.RequestInvalidQuery, err.Error()), true
	default:
		return classifyStart(err)
	}
}

// classifyStart maps the refusals of a request to start a job.
func classifyStart(err error) (problem.Problem, bool) {
	switch {
	case errors.Is(err, errBody):
		return problem.Refused(err, http.StatusBadRequest, errcode.RequestBodyInvalid), true
	case errors.Is(err, idempotency.ErrInvalidKey):
		return problem.Refused(err, http.StatusBadRequest, errcode.RequestIdempotencyKeyInvalid), true
	case errors.Is(err, job.ErrKeyConflict):
		return problem.Refused(err, http.StatusUnprocessableEntity, errcode.RequestIdempotencyKeyReused), true
	case errors.Is(err, namespace.ErrFenced):
		return problem.Refused(err, http.StatusConflict, errcode.NamespaceFenced), true
	default:
		return classifyJobRefusal(err)
	}
}

// classifyJobRefusal maps the refusals that the rules of a job give. An error
// that no case knows is answered by package problem.
func classifyJobRefusal(err error) (problem.Problem, bool) {
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
	case errors.Is(err, job.ErrFrozen):
		return problem.Refused(err, http.StatusConflict, errcode.JobFrozen), true
	case errors.Is(err, job.ErrLimit):
		return problem.Refused(err, http.StatusTooManyRequests, errcode.JobLimit), true
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
