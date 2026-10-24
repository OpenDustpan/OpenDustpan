package dustpan

import (
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"

	"opendustpan.org/opendustpan/internal/errcode"
)

// Component names a part of the server that a process runs.
type Component string

// The components. api runs each HTTP route and starts durable operations,
// but never claims one. worker claims and runs durable operations, the job
// schedules, the audit shipper and the audit chain check.
const (
	ComponentAPI    Component = "api"
	ComponentWorker Component = "worker"
)

// componentAll is the value of the components setting that selects every
// component.
const componentAll = "all"

// ErrUnknownComponent reports a component name that the server does not
// know.
var ErrUnknownComponent = errors.New("unknown component")

// Components is the set of components that a process runs. The zero value
// runs none. Use AllComponents or ParseComponents.
type Components struct {
	api    bool
	worker bool
}

// AllComponents returns the set with each component. It is the default.
func AllComponents() Components {
	return Components{api: true, worker: true}
}

// ParseComponents reads the value of --components: "all", or a
// comma-separated list of component names. An empty value means "all". A
// name that is not a component returns ErrUnknownComponent, and the error
// names the valid components.
func ParseComponents(raw string) (Components, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return AllComponents(), nil
	}

	var set Components

	for name := range strings.SplitSeq(raw, ",") {
		switch strings.TrimSpace(name) {
		case componentAll:
			set.api, set.worker = true, true
		case string(ComponentAPI):
			set.api = true
		case string(ComponentWorker):
			set.worker = true
		default:
			return Components{}, fmt.Errorf(
				"%w %q: the valid components are %s, %s and %s: set --components (DUSTPAN_COMPONENTS)",
				ErrUnknownComponent, strings.TrimSpace(name), componentAll, ComponentAPI, ComponentWorker,
			)
		}
	}

	return set, nil
}

// Has reports whether the set holds c.
func (c Components) Has(name Component) bool {
	switch name {
	case ComponentAPI:
		return c.api
	case ComponentWorker:
		return c.worker
	default:
		return false
	}
}

// Names returns the components of the set in a fixed order: api, then
// worker.
func (c Components) Names() []Component {
	var out []Component

	if c.api {
		out = append(out, ComponentAPI)
	}

	if c.worker {
		out = append(out, ComponentWorker)
	}

	return out
}

// Strings returns the names of the components in the order of Names.
func (c Components) Strings() []string {
	names := c.Names()
	out := make([]string, 0, len(names))

	for _, name := range names {
		out = append(out, string(name))
	}

	return out
}

// String returns the names, comma-separated, as --components reads them.
func (c Components) String() string {
	return strings.Join(c.Strings(), ",")
}

// WithComponents sets the components that this process runs. The default is
// AllComponents. A set with no component makes NewServer return an error.
func WithComponents(c Components) Option {
	return func(o *settings) { o.components = c }
}

// componentNotFound answers 404 on a route that a process without the api
// component does not serve.
func componentNotFound(w http.ResponseWriter, _ *http.Request) {
	writeRouteError(
		w, http.StatusNotFound, "not found: this process does not run the api component",
		routeAnswer(errcode.ServerComponentNotFound),
	)
}

// metricsPath is the route of the metrics. A process without the api
// component still serves it.
const metricsPath = "/api/server/metrics"

// workerOnlyPath reports whether a process without the api component still
// serves path: a probe or the metrics route. It accepts only a path that is
// already clean, so a path with a dot segment, a double slash or a trailing
// slash is never one of them. /health/ready/ takes one component name.
func workerOnlyPath(p string) bool {
	if p == "" || path.Clean(p) != p {
		return false
	}

	switch p {
	case "/health/live", "/health/ready", metricsPath:
		return true
	}

	name, found := strings.CutPrefix(p, "/health/ready/")

	return found && name != "" && !strings.Contains(name, "/")
}

// gateComponents answers 404 on each route except the probes and the metrics
// route when the set holds no api component. With api it returns next.
//
// Defense in depth: the mux of a process without the api component mounts no
// API handler either (see routes), so this gate is the first of two layers
// that each stop a request alone. The gate checks both the decoded path and
// the escaped path, each clean, so an encoded dot segment cannot slip past
// a check that reads only one form.
func gateComponents(next http.Handler, set Components) http.Handler {
	if set.api {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !workerOnlyPath(r.URL.Path) || !workerOnlyPath(r.URL.EscapedPath()) {
			componentNotFound(w, r)

			return
		}

		next.ServeHTTP(w, r)
	})
}
