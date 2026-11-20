package jobs

import (
	"context"
	"log/slog"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// CheckAddress runs the address check of h on r for p.
func CheckAddress(ctx context.Context, h *Handler, r *http.Request, p identity.Principal) error {
	return h.checkAddress(ctx, r, p)
}

// WriteError answers err as a handler does, with a logger that discards
// every line.
func WriteError(ctx context.Context, w http.ResponseWriter, err error) {
	WriteErrorTo(ctx, w, slog.New(slog.DiscardHandler), nil, err)
}

// WriteErrorTo answers err as a handler does, with logger. A non-nil classify
// stands in for the classifier of the handler, so a test can answer an error
// with a code that the catalog refuses.
func WriteErrorTo(
	ctx context.Context,
	w http.ResponseWriter,
	logger *slog.Logger,
	classify problem.Classifier,
	err error,
) {
	var h Handler

	h.logger = logger
	if classify != nil {
		problem.Writer{Logger: logger, KeepDenied: nil, Classify: classify}.Write(ctx, w, err)

		return
	}

	h.writeError(ctx, w, err)
}

// ErrNotFound is the not-found answer of the jobs API, for a test of its
// precedence.
var ErrNotFound = errNotFound

// ErrBody is the error of a request body that is not valid, for a test of its
// precedence.
var ErrBody = errBody

// MaxScanned is the largest number of jobs that one request reads from the
// store, for a test of the scan limit.
const MaxScanned = maxScanned

// ScanBatch is the number of jobs that one read of the store gives, for a
// test of the scan limit.
const ScanBatch = scanBatch

// ServeHandlerOnly serves r with the handler of the route that has the method
// and the path pattern of r, for a damaged job of the unit unit. It skips the
// wrapper: no sign-in, no check, no resolver. So a test of it proves that the
// handler refuses a damaged job when the wrapper did not. It reports whether
// the route exists.
func ServeHandlerOnly(h *Handler, w http.ResponseWriter, r *http.Request, pattern string, unit identity.Object) bool {
	routes := h.buildRoutes()

	for i := range routes {
		rt := &routes[i]
		if rt.Method != r.Method || rt.Path != pattern {
			continue
		}

		var (
			stub job.Job
			ns   namespace.Namespace
			p    identity.Principal
		)

		stub.ID, stub.Spec.Unit = "job-abcdefghijklmnopqrst", unit

		t := target{ns: ns, job: stub, obj: unit, damaged: true}

		rt.Serve(w, r, p, &t)

		return true
	}

	return false
}

// DamagedRoutesAnswer is the answer of DamagedRoutes.
type DamagedRoutesAnswer struct {
	// Refusing holds the method and the path of each route that resolves a job
	// by its ID and refuses a damaged job.
	Refusing [][2]string
	// Serving holds the same for each route that serves a damaged job.
	Serving [][2]string
}

// DamagedRoutes lists the routes that resolve a job by its ID, split by what
// they do with a damaged job.
func DamagedRoutes(h *Handler) DamagedRoutesAnswer {
	var out DamagedRoutesAnswer

	routes := h.buildRoutes()

	for i := range routes {
		rt := &routes[i]
		if rt.Path == jobsPath {
			continue
		}

		row := [2]string{rt.Method, rt.Path}
		if rt.ServesDamaged {
			out.Serving = append(out.Serving, row)
		} else {
			out.Refusing = append(out.Refusing, row)
		}
	}

	return out
}

// ProbeWrapper returns the wrapper of a route that resolves a job with the
// resolver of the job routes and whose handler writes 200 with "served". The
// route serves a damaged job when servesDamaged is true. A test of it proves
// the order of the wrapper: the checks first, then the 409, and never the
// handler.
func ProbeWrapper(h *Handler, servesDamaged bool) http.HandlerFunc {
	return h.enforce(Route{
		Method: http.MethodGet, Path: jobPath, Scope: ScopeAuthorized,
		Reads:   []identity.Action{identity.ActionJobRead},
		Actions: nil, Filters: nil, Writes: nil, IdempotencyKey: false, ETag: false,
		ServesDamaged: servesDamaged, Resolve: h.resolveJob,
		Serve: func(w http.ResponseWriter, _ *http.Request, _ identity.Principal, _ *target) {
			w.WriteHeader(http.StatusOK)

			if _, err := w.Write([]byte("served")); err != nil {
				return
			}
		},
	})
}

// ProbePattern is the ServeMux pattern for ProbeWrapper.
const ProbePattern = "GET " + jobPath
