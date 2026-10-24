package jobs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Scope names how a route is protected. Every route declares one explicitly
// and the route test enforces the meaning of each value.
type Scope int

const (
	// ScopePublic serves without sign-in. Only routes on the public list may
	// use it. This API has none.
	ScopePublic Scope = iota + 1
	// ScopeAuthorized needs a signed-in principal and the Authorizer to allow
	// the checks of the route before the handler runs.
	ScopeAuthorized
)

// maxIDLength bounds the job ID of a path. A longer value is no job ID.
const maxIDLength = 64

// target is the object of a request, found before any authorization check:
// the namespace of the path, the job, and the unit reference of the job.
type target struct {
	ns  namespace.Namespace
	job job.Job
	obj identity.Object
}

// TargetResolver finds the object of a request. It answers errNotFound for
// an object that does not exist. It runs after the sign-in and before any
// authorization check.
type TargetResolver func(r *http.Request) (target, error)

// RouteHandler serves a route after the registration wrapper has
// authenticated and authorized it.
type RouteHandler func(w http.ResponseWriter, r *http.Request, p identity.Principal, t *target)

// Route declares one endpoint: its ServeMux pattern, how it is protected,
// the actions it needs, how to find its object, and how to serve it.
// NewHandler registers every route from the table that buildRoutes returns,
// so a route without a row is never served.
//
// The registration wrapper runs the checks in this order. A refusal of any
// of them answers the one 404 of an unknown job, so a caller learns nothing
// about a job that it cannot read:
//
//  1. identity.ActionNamespaceRead on the namespace of the path.
//  2. Reads on the unit of the job.
type Route struct {
	// Method is the HTTP method, for example GET.
	Method string
	// Path is the ServeMux path pattern.
	Path string
	// Scope names how the route is protected.
	Scope Scope
	// Reads lists the read actions that the route needs on the unit of the
	// job.
	Reads []identity.Action
	// Writes lists the actions that the job service checks on the unit that
	// the request body names, before it does anything. The wrapper cannot
	// check them: the body names the unit, and the action depends on the
	// operation and the executor. The service checks them for its caller,
	// and the handler writes the audit entry of a refusal.
	Writes []identity.Action
	// IdempotencyKey says that the route takes the Idempotency-Key header.
	// The wrapper does not check the header: the handler of a route with the
	// flag reads it. A read without the flag ignores the header, because a
	// read has no effect to repeat. A write without the flag must refuse the
	// header with 400 in its handler, so that the server never ignores a key
	// without an error. TestHeaderFlagsMatchTheHandler checks the write case.
	IdempotencyKey bool
	// ETag says that the answer holds the ETag header: the version of the
	// job, in quotes.
	ETag bool
	// Resolve finds the object of the request.
	Resolve TargetResolver
	// Serve handles the request after the wrapper has enforced the row.
	Serve RouteHandler
}

// buildRoutes returns the route table. It is the single source of the
// patterns that NewHandler registers and of what each pattern enforces. The
// path holds the namespace and the job ID, and no tool.
func (h *Handler) buildRoutes() []Route {
	routes := []Route{
		{
			Method: http.MethodGet, Path: jobPath,
			Scope: ScopeAuthorized, Reads: []identity.Action{identity.ActionJobRead}, Writes: nil,
			IdempotencyKey: false, ETag: true, Resolve: h.resolveJob, Serve: h.getJob,
		},
	}

	// Without a job service the server starts no job, so it serves no route
	// that would only answer an error.
	if h.starter != nil {
		routes = append(routes, Route{
			Method: http.MethodPost, Path: jobsPath, Scope: ScopeAuthorized, Reads: nil,
			Writes:         []identity.Action{identity.ActionUnitRunPlan},
			IdempotencyKey: true, ETag: true, Resolve: h.resolveNamespace, Serve: h.startJob,
		})
	}

	return routes
}

const (
	jobsPath = "/api/dustpan/v1/namespaces/{namespace}/jobs"
	jobPath  = jobsPath + "/{job_id}"
)

// Routes returns a copy of the route table. The action lists are copied too,
// so callers cannot mutate handler state through the result.
func (h *Handler) Routes() []Route {
	out := make([]Route, len(h.routes))
	for i, route := range h.routes {
		out[i] = route
		out[i].Reads = append([]identity.Action(nil), route.Reads...)
		out[i].Writes = append([]identity.Action(nil), route.Writes...)
	}

	return out
}

// RegisteredPatterns returns a copy of every ServeMux pattern the handler
// serves, in registration order. The route test compares this with the route
// table in both directions.
func (h *Handler) RegisteredPatterns() []string {
	return h.mux.Patterns()
}

// patternMux is an http.ServeMux that records every pattern registered on
// it, so the route test can list every served route.
type patternMux struct {
	servemux *http.ServeMux

	patterns []string
}

// newPatternMux returns a patternMux ready to register routes.
func newPatternMux() *patternMux {
	return &patternMux{servemux: http.NewServeMux(), patterns: nil}
}

// HandleFunc registers pattern and records it.
func (m *patternMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	m.patterns = append(m.patterns, pattern)
	m.servemux.HandleFunc(pattern, handler)
}

// ServeHTTP implements http.Handler.
func (m *patternMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.servemux.ServeHTTP(w, r)
}

// Patterns returns a copy of the recorded patterns.
func (m *patternMux) Patterns() []string {
	return append([]string(nil), m.patterns...)
}

// resolveNamespace finds the namespace of the path. The object of the request
// is the namespace until the handler finds the unit of the body.
func (h *Handler) resolveNamespace(r *http.Request) (target, error) {
	name, err := namespace.ParseName(r.PathValue("namespace"))
	if err != nil {
		return target{}, errNotFound
	}

	ns, err := h.namespaces.Get(r.Context(), name)
	if errors.Is(err, namespace.ErrNotFound) {
		return target{}, errNotFound
	}

	if err != nil {
		return target{}, fmt.Errorf("get namespace: %w", err)
	}

	var noJob job.Job

	return target{ns: ns, job: noJob, obj: identity.NamespaceObject(ns.Name)}, nil
}

// resolveJob finds the namespace of the path and the job of the path in it.
// An invalid name or ID, an unknown namespace, an unknown job and a job of
// another namespace answer errNotFound alike.
func (h *Handler) resolveJob(r *http.Request) (target, error) {
	name, err := namespace.ParseName(r.PathValue("namespace"))
	if err != nil {
		return target{}, errNotFound
	}

	ns, err := h.namespaces.Get(r.Context(), name)
	if errors.Is(err, namespace.ErrNotFound) {
		return target{}, errNotFound
	}

	if err != nil {
		return target{}, fmt.Errorf("get namespace: %w", err)
	}

	raw := r.PathValue("job_id")
	if !strings.HasPrefix(raw, job.IDPrefix) || !ident.Valid(raw, maxIDLength) {
		return target{}, errNotFound
	}

	j, err := h.jobs.Get(r.Context(), job.ID(raw))
	if errors.Is(err, job.ErrNotFound) {
		return target{}, errNotFound
	}

	if err != nil {
		return target{}, fmt.Errorf("get job: %w", err)
	}

	// Defense in depth: the store looks a job up by its ID alone. A job of
	// another namespace must not answer under this namespace, whatever the
	// caller may read there.
	if j.Spec.Unit.Namespace() != ns.Name || !j.Spec.Unit.IsUnit() || j.Spec.Unit.UnitID() == "" {
		return target{}, errNotFound
	}

	return target{ns: ns, job: j, obj: j.Spec.Unit}, nil
}

// enforce wraps one route table row: it authenticates, finds the object,
// runs the checks in the order of Route, and only then runs the handler.
func (h *Handler) enforce(route Route) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		p, session, err := h.authenticate(r)
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		if session {
			ctx = sessionauth.WithSession(ctx)
			r = r.WithContext(ctx)
		}

		if err = h.checkAddress(ctx, r, p); err != nil {
			h.writeError(ctx, w, err)

			return
		}

		t, err := route.Resolve(r)
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		if err := h.authorizeRoute(ctx, r, route, p, &t); err != nil {
			h.writeError(ctx, w, err)

			return
		}

		route.Serve(w, r, p, &t)
	}
}

// checkAddress refuses a client address that is outside the IP allowlist of
// the namespace in the path, before any lookup of the namespace or of the
// job. The answer is the same 403 for a name that exists and for a name that
// does not exist, so it does not tell whether the job exists. Every other
// result, also an invalid name, continues: the checks after the lookup give
// the answer, as before.
//
// Defense in depth: authorizeRoute checks the allowlist again on the found
// namespace. This check stays so that the order of the lookup never decides
// the answer.
func (h *Handler) checkAddress(
	ctx context.Context, r *http.Request, p identity.Principal,
) error {
	action := identity.ActionNamespaceRead

	// An invalid name names no namespace and has no allowlist: the lookup that
	// follows answers 404 for it.
	var err error
	if name, parseErr := namespace.ParseName(r.PathValue("namespace")); parseErr == nil {
		err = h.guard.Authorize(ctx, r, p, action, identity.NamespaceObject(name))
	}

	if errors.Is(err, ipallow.ErrAddressNotAllowed) {
		return fmt.Errorf("authorize %s: %w", action, err)
	}

	return nil
}

// authorizeRoute runs the checks of route for p on t. A refused check
// returns errNotFound, because the caller may not know that the job exists.
func (h *Handler) authorizeRoute(
	ctx context.Context, r *http.Request, route Route, p identity.Principal, t *target,
) error {
	nsRead := identity.ActionNamespaceRead

	nsErr := h.guard.Authorize(ctx, r, p, nsRead, identity.NamespaceObject(t.ns.Name))
	if err := readRefusal(nsRead, nsErr); err != nil {
		return err
	}

	for _, action := range route.Reads {
		if err := readRefusal(action, h.guard.Authorize(ctx, r, p, action, t.obj)); err != nil {
			return err
		}
	}

	return nil
}

// readRefusal maps the result of a read check: a refusal is the 404 of an
// unknown job.
func readRefusal(action identity.Action, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ipallow.ErrAddressNotAllowed):
		// An address outside the IP allowlist answers 403 with its own code,
		// as on the other routes of the namespace. The refusal names no
		// object, so it does not tell whether the object exists.
		return fmt.Errorf("authorize %s: %w", action, err)
	case errors.Is(err, identity.ErrDenied):
		return errNotFound
	default:
		return fmt.Errorf("authorize %s: %w", action, err)
	}
}
