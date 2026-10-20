package apidesc

import (
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
	// IdempotencyKey is true when the route takes the Idempotency-Key header.
	IdempotencyKey bool
	// IfMatch is true when the route takes the If-Match header.
	IfMatch bool
	// ETag is true when the answer of the route holds the ETag header.
	ETag bool
}

// Name returns the label of an operation that sets no header: the operationId
// and the tags.
func Name(id string, tags ...string) Label {
	return Label{ID: id, Tags: tags, IdempotencyKey: false, IfMatch: false, ETag: false}
}

// WithKey returns the label for a route that takes the Idempotency-Key header.
func (l Label) WithKey() Label {
	l.IdempotencyKey = true

	return l
}

// WithIfMatch returns the label for a route that takes the If-Match header.
func (l Label) WithIfMatch() Label {
	l.IfMatch = true

	return l
}

// WithETag returns the label for a route whose answer holds the ETag header.
func (l Label) WithETag() Label {
	l.ETag = true

	return l
}

// Row is what an adapter knows of one route: the fields that every adapter
// has in its own Route type.
type Row struct {
	Method string
	Path   string
	// Actions lists the actions that the route can need, as text.
	Actions []string
	// Public is true for a route that needs no sign-in.
	Public bool
}

// Pattern returns the key of a label in a table: the method, one space and
// the path, as net/http writes a pattern.
func Pattern(method, path string) string {
	return method + " " + path
}

// Collect turns the rows of a route table into operations. It skips a row
// whose path is outside Prefix, because the description covers Prefix only.
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

		label := labels[Pattern(row.Method, row.Path)]

		out = append(out, Operation{
			ID:             label.ID,
			Method:         row.Method,
			Path:           row.Path,
			Tags:           append([]string(nil), label.Tags...),
			Actions:        uniqueActions(row.Actions),
			Public:         row.Public,
			IdempotencyKey: label.IdempotencyKey,
			IfMatch:        label.IfMatch,
			ETag:           label.ETag,
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
