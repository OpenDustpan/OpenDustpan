package apidesc

import (
	"maps"
	"net/http"
	"slices"
	"strings"
)

// Label names one operation and states what its route does with the headers
// of the description. An adapter keeps a table of labels, keyed by Pattern,
// next to its route table. The name is explicit, so a change of a path never
// renames an SDK method.
type Label struct {
	// ID is the operationId.
	ID string
	// Tags are the tags of the operation.
	Tags []string
	// IdempotencyKey is true when the route takes the Idempotency-Key header
	// and its route table has no field that says so. A route table that has
	// such a field gives it in Row, and its label leaves this flag off.
	IdempotencyKey bool
	// IfMatch is true when the route takes the If-Match header and works
	// without it.
	IfMatch bool
	// IfMatchRequired is true when the route takes the If-Match header and
	// answers 428 without it.
	IfMatchRequired bool
	// ETag is true when the answer of the route holds the ETag header.
	ETag bool
	// Object is the kind of the object that the actions of the route apply
	// to, except the actions that the row lists as namespace actions. It is
	// one of the constants Kind...
	Object string
	// Query lists the query parameters that the route reads.
	Query []QueryParam
	// Statuses are the status codes of the success answer. An empty list means
	// the default of the body: 200 for a typed answer and 204 for an answer
	// with no body.
	Statuses []int
	// Bodies names the body of the request and the body of the success
	// answer.
	Bodies *Bodies
}

// Name returns the label of an operation that sets no header: the operationId
// and the tags.
func Name(id string, tags ...string) Label {
	return Label{
		ID: id, Tags: tags, IdempotencyKey: false, IfMatch: false, IfMatchRequired: false, ETag: false,
		Object: "", Query: nil, Statuses: nil, Bodies: nil,
	}
}

// WithStatus returns the label for a route whose success answer has the given
// status codes, in place of the default of its body (200 with a body, 204
// without one). Give one code for a route that always answers the same, for
// example 201 for a route that creates an object. Give two for a route that
// answers 201 for the first write of an object and 200 for a replacement. Each
// call adds codes. Build refuses a code that is not 2xx and a code that the
// label gives twice.
func (l Label) WithStatus(statuses ...int) Label {
	l.Statuses = append(slices.Clone(l.Statuses), statuses...)

	return l
}

// On returns the label for a route whose actions apply to an object of the
// kind. The kind is one of the constants Kind...
func (l Label) On(kind string) Label {
	l.Object = kind

	return l
}

// WithBodies returns the label for a route with the body of its request and
// the body of its success answer. A body is a Go type or NoBody. A GET, a HEAD
// and a DELETE send no request, and take NoBody.
func (l Label) WithBodies(request, response Body) Label {
	l.Bodies = &Bodies{Request: request, Response: response}

	return l
}

// WithQuery returns the label for a route that reads the query parameters.
// A handler reads a parameter through QueryOf, which gives the value of a
// declared name only, so the description lists each parameter that the
// handler reads.
func (l Label) WithQuery(params ...QueryParam) Label {
	l.Query = append(slices.Clone(l.Query), params...)

	return l
}

// WithKey returns the label for a route that takes the Idempotency-Key header
// and whose route table has no field for it.
func (l Label) WithKey() Label {
	l.IdempotencyKey = true

	return l
}

// WithIfMatch returns the label for a route that takes the If-Match header
// and works without it.
func (l Label) WithIfMatch() Label {
	l.IfMatch = true

	return l
}

// WithIfMatchRequired returns the label for a route that takes the If-Match
// header and answers 428 without it.
func (l Label) WithIfMatchRequired() Label {
	l.IfMatchRequired = true

	return l
}

// WithETag returns the label for a route whose answer holds the ETag header.
func (l Label) WithETag() Label {
	l.ETag = true

	return l
}

// statuses returns the success statuses of the label in ascending order: the
// explicit statuses, or the default of the body. A repeated status stays in the
// list, so that Validate refuses it.
func (l Label) statuses() []int {
	switch {
	case len(l.Statuses) > 0:
		return slices.Sorted(slices.Values(l.Statuses))
	case l.Bodies != nil && l.Bodies.Response.kind == bodyNone:
		return []int{http.StatusNoContent}
	default:
		return []int{http.StatusOK}
	}
}

// Row is what an adapter knows of one route: the fields that every adapter
// has in its own Route type.
type Row struct {
	Method string
	Path   string
	// Actions lists the actions that the route can need, as text.
	Actions []string
	// NamespaceActions lists the actions of Actions that apply to the
	// namespace of the object and not to the object that the label names.
	NamespaceActions []string
	// Public is true for a route that needs no sign-in.
	Public bool
	// IdempotencyKey is true when the route table says that the route takes
	// the Idempotency-Key header.
	IdempotencyKey bool
}

// undescribed lists the routes under Prefix that no adapter describes, keyed
// by their exact pattern, with the reason for each. It is the one list: Collect
// skips these routes, and the class test of the server reads the list, so a
// route under Prefix that is not on it and not described fails the test.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant maps.
var undescribed = map[string]string{
	Pattern("GET", Prefix+"namespaces/-/units/{unitID}"): "The wildcard unitID breaks the grammar of " +
		"wildcard names. The route joins the description when it is renamed to unit_id.",
	Pattern("POST", Prefix+"sign-in/dev-code"): "Only the installation kind dev serves it, " +
		"and a client never calls it.",
}

// Undescribed returns a copy of the list of routes under Prefix that no
// adapter describes: the exact pattern and the reason.
func Undescribed() map[string]string { return maps.Clone(undescribed) }

// Pattern returns the key of a label in a table: the method, one space and
// the path, as net/http writes a pattern.
func Pattern(method, path string) string {
	return method + " " + path
}

// Collect turns the rows of a route table into operations. It skips a row
// whose path is outside Prefix, because the description covers Prefix only,
// and a row whose exact pattern is on the list Undescribed.
//
// A row under Prefix that has no label still gives an operation, with no ID.
// Validate refuses such an operation, so a new route cannot enter the server
// without a name.
func Collect(rows []Row, labels map[string]Label) []Operation {
	var out []Operation

	for _, row := range rows {
		if !strings.HasPrefix(row.Path, Prefix) {
			continue
		}

		if _, skip := undescribed[Pattern(row.Method, row.Path)]; skip {
			continue
		}

		label := labels[Pattern(row.Method, row.Path)]
		actions := uniqueActions(row.Actions)

		out = append(out, Operation{
			ID:              label.ID,
			Method:          row.Method,
			Path:            row.Path,
			Tags:            append([]string(nil), label.Tags...),
			Actions:         actions,
			ActionObjects:   actionObjects(actions, row.NamespaceActions, label.Object),
			Public:          row.Public,
			IdempotencyKey:  row.IdempotencyKey || label.IdempotencyKey,
			IfMatch:         label.IfMatch,
			IfMatchRequired: label.IfMatchRequired,
			ETag:            label.ETag,
			Statuses:        label.statuses(),
			Query:           sortedQuery(label.Query),
			Bodies:          copyBodies(label.Bodies),
		})
	}

	return out
}

// uniqueActions returns the actions in their order, with each action once. A
// route can list an action in two of its lists, and Validate refuses a
// repeated action.
func uniqueActions(actions []string) []string {
	out := make([]string, 0, len(actions))

	for _, action := range actions {
		if !slices.Contains(out, action) {
			out = append(out, action)
		}
	}

	return out
}

// copyBodies returns a copy of the bodies, so that two operations of one label
// never share them.
func copyBodies(b *Bodies) *Bodies {
	if b == nil {
		return nil
	}

	out := *b

	return &out
}

// actionObjects gives each action the kind of its object: the namespace for a
// namespace action and the kind of the label for the rest. A label with no
// kind gives the empty kind, which Validate refuses.
func actionObjects(actions, namespaceActions []string, kind string) map[string]string {
	out := make(map[string]string, len(actions))

	for _, action := range actions {
		if slices.Contains(namespaceActions, action) {
			out[action] = KindNamespace

			continue
		}

		out[action] = kind
	}

	return out
}
