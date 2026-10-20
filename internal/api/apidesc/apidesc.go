// Package apidesc holds the types for the description of the tool-neutral
// REST API and the check that compares a description with a route table.
//
// The package describes the shape of the check only. It reads no route table
// and writes no document yet.
package apidesc

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Prefix is the path prefix of the tool-neutral REST API. The description
// covers a path under this prefix and no other path.
const Prefix = "/api/dustpan/v1/"

// ErrInvalidOperation means that an operation has no method or no path, or
// that its path lies outside Prefix.
var ErrInvalidOperation = errors.New("apidesc: operation is not valid")

// Operation is one endpoint of the description: a method and a path pattern,
// with the actions that the route declares. A route table and a description
// both reduce to a list of operations, so Diff can compare them.
type Operation struct {
	// Method is the HTTP method in capital letters, for example GET.
	Method string
	// Path is the path pattern with the wildcards of net/http, for example
	// /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}.
	Path string
	// Actions lists the actions that the route declares, for example
	// unit:read. A public route has none.
	Actions []string
	// Public is true for a route that needs no sign-in.
	Public bool
}

// Source gives the operations of one place: a route table of the server, or a
// description that a person or a tool wrote.
type Source interface {
	// Operations returns every operation of the source. It returns
	// ErrInvalidOperation, wrapped, for an operation that Validate refuses.
	Operations() ([]Operation, error)
}

// Validate returns ErrInvalidOperation, wrapped, when the operation has no
// method, when the path lies outside Prefix, or when the path holds a dot
// segment that could leave the prefix after a client cleans it.
func (o Operation) Validate() error {
	if o.Method == "" || o.Method != strings.ToUpper(o.Method) {
		return fmt.Errorf("%w: method %q", ErrInvalidOperation, o.Method)
	}

	if !strings.HasPrefix(o.Path, Prefix) {
		return fmt.Errorf("%w: path %q is outside %s", ErrInvalidOperation, o.Path, Prefix)
	}

	if slices.Contains(strings.Split(o.Path, "/"), "..") {
		return fmt.Errorf("%w: path %q holds a dot segment", ErrInvalidOperation, o.Path)
	}

	return nil
}

// Key names the operation for a comparison: the method and the path with the
// name of each wildcard removed, because two wildcards at the same place
// match the same requests whatever their names are.
func (o Operation) Key() string {
	segments := strings.Split(o.Path, "/")
	for i, s := range segments {
		segments[i] = wildcardKey(s)
	}

	return o.Method + " " + strings.Join(segments, "/")
}

// wildcardKey returns a path segment with the name of its wildcard removed.
// A segment that is not a wildcard stays as it is.
func wildcardKey(segment string) string {
	if !strings.HasPrefix(segment, "{") || !strings.HasSuffix(segment, "}") {
		return segment
	}

	switch {
	case segment == "{$}":
		return segment
	case strings.HasSuffix(segment, "...}"):
		return "{...}"
	default:
		return "{}"
	}
}

// Drift is the difference between a route table and a description.
type Drift struct {
	// Undescribed holds the keys of the routes that the description lacks.
	Undescribed []string
	// Unrouted holds the keys of the operations that no route serves.
	Unrouted []string
	// ActionMismatch holds the keys of the operations whose actions differ
	// between the route table and the description.
	ActionMismatch []string
}

// Empty reports whether the route table and the description agree.
func (d Drift) Empty() bool {
	return len(d.Undescribed) == 0 && len(d.Unrouted) == 0 && len(d.ActionMismatch) == 0
}

// Diff compares the routes with the described operations. It fails closed:
// it returns an error, and no Drift, when either list holds an operation that
// Validate refuses or a key twice, so a malformed input never reads as a
// match. Each list in the result is sorted.
func Diff(routes, described []Operation) (Drift, error) {
	routeByKey, err := index(routes)
	if err != nil {
		return Drift{}, fmt.Errorf("route table: %w", err)
	}

	describedByKey, err := index(described)
	if err != nil {
		return Drift{}, fmt.Errorf("description: %w", err)
	}

	var d Drift

	for key, route := range routeByKey {
		doc, ok := describedByKey[key]
		if !ok {
			d.Undescribed = append(d.Undescribed, key)

			continue
		}

		if route.Public != doc.Public || !sameActions(route.Actions, doc.Actions) {
			d.ActionMismatch = append(d.ActionMismatch, key)
		}
	}

	for key := range describedByKey {
		if _, ok := routeByKey[key]; !ok {
			d.Unrouted = append(d.Unrouted, key)
		}
	}

	slices.Sort(d.Undescribed)
	slices.Sort(d.Unrouted)
	slices.Sort(d.ActionMismatch)

	return d, nil
}

// index maps the key of each operation to the operation.
func index(ops []Operation) (map[string]Operation, error) {
	byKey := make(map[string]Operation, len(ops))

	for _, op := range ops {
		if err := op.Validate(); err != nil {
			return nil, err
		}

		key := op.Key()
		if _, dup := byKey[key]; dup {
			return nil, fmt.Errorf("%w: %s appears twice", ErrInvalidOperation, key)
		}

		byKey[key] = op
	}

	return byKey, nil
}

// sameActions reports whether two action lists hold the same actions, in any
// order.
func sameActions(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)

	return slices.Equal(x, y)
}
