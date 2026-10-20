package dustpan

import (
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// operationLister is implemented by an adapter that describes its routes under
// apidesc.Prefix.
type operationLister interface {
	Operations() []apidesc.Operation
}

// routeSource is the list of the route patterns ("METHOD /path") that one
// mounted adapter serves.
type routeSource struct {
	// name is the adapter, for example "inventory".
	name     string
	patterns []string
}

// handlers lists the mounted adapters by name. A module that is off has a nil
// handler and is left out.
func (a adapters) handlers() []namedHandler {
	all := []namedHandler{
		{"server", a.server},
		{"inventory", a.inventory},
		{"audit", a.audit},
		{"context", a.context},
		{"runners", a.runners},
		{"profiles", a.profiles},
		{"jobs", a.jobs},
		{"pulumi", a.pulumi},
		{"tfe", a.tfe},
		{"tofuhttp", a.tofuHTTP},
	}

	var out []namedHandler

	for _, h := range all {
		if !isNilHandler(h.handler) {
			out = append(out, h)
		}
	}

	return out
}

// namedHandler is an adapter with the name that the tests print.
type namedHandler struct {
	name    string
	handler http.Handler
}

// isNilHandler reports whether h is a nil interface. A module that is off
// leaves its handler nil.
func isNilHandler(h http.Handler) bool { return h == nil }

// operations joins the operations of the adapters that the server mounts.
// The result is not sorted: the writer of the document sorts it.
func (a adapters) operations() []apidesc.Operation {
	return joinOperations(a.handlers())
}

// joinOperations joins the operations of the handlers that list them.
func joinOperations(handlers []namedHandler) []apidesc.Operation {
	var out []apidesc.Operation

	for _, h := range handlers {
		lister, ok := h.handler.(operationLister)
		if !ok {
			continue
		}

		out = append(out, lister.Operations()...)
	}

	return out
}

// routeSources lists the route patterns of each mounted adapter that has
// them.
func (a adapters) routeSources() []routeSource {
	var out []routeSource

	for _, h := range a.handlers() {
		lister, ok := h.handler.(patternLister)
		if !ok {
			continue
		}

		out = append(out, routeSource{name: h.name, patterns: lister.RegisteredPatterns()})
	}

	return out
}

// describeOperations returns the joined operations of the adapters, or an
// error that names the first operation that breaks the allowlists of apidesc,
// so that a server never starts with a description that the writer would
// refuse.
func (a adapters) describeOperations() ([]apidesc.Operation, error) {
	return describeHandlers(a.handlers())
}

// describeHandlers joins the operations of the handlers and checks each one.
func describeHandlers(handlers []namedHandler) ([]apidesc.Operation, error) {
	ops := joinOperations(handlers)

	for _, op := range ops {
		if err := op.Validate(); err != nil {
			return nil, fmt.Errorf("api description: %w", err)
		}
	}

	return ops, nil
}
