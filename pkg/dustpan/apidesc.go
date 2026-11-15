package dustpan

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

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

// allHandlers lists every adapter field by name, nil ones included. A new
// http.Handler field of adapters needs a row here: TestEveryHandlerFieldIsListed
// fails without it.
func (a adapters) allHandlers() []namedHandler {
	return []namedHandler{
		{"server", a.server},
		{"inventory", a.inventory},
		{"audit", a.audit},
		{"context", a.context},
		{"runners", a.runners},
		{"profiles", a.profiles},
		{"issuer", a.issuer},
		{"sinks", a.sinks},
		{"jobs", a.jobs},
		{"triggers", a.triggers},
		{"unitsettings", a.unitSettings},
		{"drift", a.drift},
		{"registry", a.registry},
		{"sourcefilter", a.sourceFilter},
		{"trustrules", a.trustRules},
		{"selfservice", a.selfService},
		{"pulumi", a.pulumi},
		{"tfe", a.tfe},
		{"tofuhttp", a.tofuHTTP},
	}
}

// handlers lists the mounted adapters by name. A module that is off has a nil
// handler and is left out.
func (a adapters) handlers() []namedHandler {
	var out []namedHandler

	for _, h := range a.allHandlers() {
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
// the first operation that leaves a body unmarked, or the first handler that hides its routes, so that a server never
// starts
// with a description that the writer would refuse or that misses a route.
func (a adapters) describeOperations() ([]apidesc.Operation, error) {
	return describeHandlers(a.handlers())
}

// errUnlisted means that a mounted handler hides its routes from the
// description: it lists no patterns, or it serves a route under the prefix and
// lists no operations.
var errUnlisted = errors.New("api description: a mounted handler does not list its routes")

// patternPath returns the path of a ServeMux pattern. A pattern with no
// method is all path. A host part, as in "example.org/api/dustpan/v1/x", is
// not part of the path: the path starts at the first slash, so a pattern with
// a host is classified by its path, never skipped.
func patternPath(pattern string) string {
	path := pattern
	if _, rest, found := strings.Cut(pattern, " "); found {
		path = rest
	}

	if i := strings.Index(path, "/"); i > 0 {
		return path[i:]
	}

	return path
}

// checkListers returns errUnlisted, wrapped, for the first handler that
// serves a route under apidesc.Prefix and does not implement both listers, or
// that does not implement patternLister. A handler like that, for example one
// that a later change wraps in a middleware, would drop out of the
// description and out of the class check with no error.
func checkListers(handlers []namedHandler) error {
	for _, h := range handlers {
		patterns, ok := h.handler.(patternLister)
		if !ok {
			return fmt.Errorf("%w: %s lists no patterns", errUnlisted, h.name)
		}

		if _, ok = h.handler.(operationLister); ok {
			continue
		}

		for _, pattern := range patterns.RegisteredPatterns() {
			if strings.HasPrefix(patternPath(pattern), apidesc.Prefix) {
				return fmt.Errorf("%w: %s serves %s and lists no operations", errUnlisted, h.name, pattern)
			}
		}
	}

	return nil
}

// describeHandlers joins the operations of the handlers and checks each one.
func describeHandlers(handlers []namedHandler) ([]apidesc.Operation, error) {
	if err := checkListers(handlers); err != nil {
		return nil, err
	}

	ops := joinOperations(handlers)

	for i := range ops {
		op := &ops[i]

		if err := op.Validate(); err != nil {
			return nil, fmt.Errorf("api description: %w", err)
		}

		if err := op.ValidateBodies(); err != nil {
			return nil, fmt.Errorf("api description: %w", err)
		}
	}

	return ops, nil
}
