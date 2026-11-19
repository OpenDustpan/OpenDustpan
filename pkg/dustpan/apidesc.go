package dustpan

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/metrics"
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
		{name: "server", handler: a.server, label: metrics.AdapterServer},
		{name: "inventory", handler: a.inventory, label: metrics.AdapterInventory},
		{name: "audit", handler: a.audit, label: metrics.AdapterServer},
		{name: "context", handler: a.context, label: metrics.AdapterServer},
		{name: "decisionrecords", handler: a.decisionRecords, label: metrics.AdapterServer},
		{name: "runners", handler: a.runners, label: metrics.AdapterServer},
		{name: "runnerpools", handler: a.runnerPools, label: metrics.AdapterServer},
		{name: "profiles", handler: a.profiles, label: metrics.AdapterServer},
		{name: "issuer", handler: a.issuer, label: metrics.AdapterServer},
		{name: "sinks", handler: a.sinks, label: metrics.AdapterServer},
		{name: "jobs", handler: a.jobs, label: metrics.AdapterServer},
		{name: "triggers", handler: a.triggers, label: metrics.AdapterServer},
		{name: "unitsettings", handler: a.unitSettings, label: metrics.AdapterServer},
		{name: "drift", handler: a.drift, label: metrics.AdapterServer},
		{name: "registry", handler: a.registry, label: metrics.AdapterServer},
		{name: "sourcefilter", handler: a.sourceFilter, label: metrics.AdapterServer},
		{name: "trustrules", handler: a.trustRules, label: metrics.AdapterServer},
		{name: "selfservice", handler: a.selfService, label: metrics.AdapterServer},
		{name: "state", handler: a.state, label: metrics.AdapterServer},
		{name: "pulumi", handler: a.pulumi, label: metrics.AdapterPulumi},
		{name: "tfe", handler: a.tfe, label: metrics.AdapterOpenTofu},
		{name: "tofuhttp", handler: a.tofuHTTP, label: metrics.AdapterOpenTofu},
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

// namedHandler is an adapter with the name that the tests print and the
// adapter label that the request metrics and the request log use.
type namedHandler struct {
	name    string
	handler http.Handler
	label   string
}

// errLabelConflict means that two adapters register one pattern, or one path,
// with different adapter labels.
var errLabelConflict = errors.New("adapter label: one route has two labels")

// errLabelNoHandler means that a route source has no handler row, so it has no
// label. The server does not start: no rule picks a label for it.
var errLabelNoHandler = errors.New("adapter has no handler row")

// adapterLabels maps each registered pattern of every mounted adapter, and the
// path of that pattern, to the label of the adapter. The path key is for the
// root mux, which knows a mounted route by its path only. It returns
// errLabelConflict, with the key and both adapters, when one key would get two
// labels, so that a server never starts with an ambiguous label.
func (a adapters) adapterLabels(root labelSet) (map[string]string, error) {
	return buildAdapterLabels(a.routeSources(), a.allHandlers(), root)
}

// labelSet collects the label of each key and the adapter that set it first.
type labelSet struct {
	label map[string]string
	owner map[string]string
	// err is the first conflict that a registration found. Only the root mux
	// uses it.
	err error
}

// newLabelSet returns an empty labelSet.
func newLabelSet() labelSet {
	return labelSet{label: map[string]string{}, owner: map[string]string{}, err: nil}
}

// add sets the label of key for adapter, or returns errLabelConflict when key
// already has another label.
func (l labelSet) add(key, label, adapter string) error {
	if prev, seen := l.label[key]; seen && prev != label {
		return fmt.Errorf("%w: %q is %q for adapter %q and %q for adapter %q",
			errLabelConflict, key, prev, l.owner[key], label, adapter)
	}

	l.label[key] = label

	if _, seen := l.owner[key]; !seen {
		l.owner[key] = adapter
	}

	return nil
}

// buildAdapterLabels adds the patterns of sources to the labels that the root
// mux holds, and returns the joined map.
func buildAdapterLabels(sources []routeSource, handlers []namedHandler, root labelSet) (map[string]string, error) {
	if root.err != nil {
		return nil, root.err
	}

	labelOf := map[string]string{}
	for _, h := range handlers {
		labelOf[h.name] = h.label
	}

	set := newLabelSet()
	for key, label := range root.label {
		set.label[key] = label
		set.owner[key] = root.owner[key]
	}

	for _, source := range sources {
		label, ok := labelOf[source.name]
		if !ok {
			return nil, fmt.Errorf(
				"adapter %q lists routes but has no row in allHandlers: %w",
				source.name, errLabelNoHandler,
			)
		}

		for _, pattern := range source.patterns {
			for _, key := range []string{pattern, patternPath(pattern)} {
				if err := set.add(key, label, source.name); err != nil {
					return nil, err
				}
			}
		}
	}

	return set.label, nil
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
