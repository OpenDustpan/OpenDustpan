// Package apidesc holds the types for the description of the tool-neutral
// REST API and the check that compares a description with a route table.
//
// The package holds the types and the check only. It reads no route table and
// writes no document.
package apidesc

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Prefix is the path prefix of the tool-neutral REST API. The description
// covers a path under this prefix and no other path.
const Prefix = "/api/dustpan/v1/"

// ErrInvalidOperation means that an operation breaks the allowlists of
// Validate, or that a list holds the same key twice.
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

// Validate returns ErrInvalidOperation, wrapped, when the operation breaks an
// allowlist: the method, the path or the actions. It refuses a value and does
// not change it to make it valid.
//
// Defense in depth: a handler and a route test check the same fields. This
// check stays so that a new caller of Diff or of the generator cannot skip it
// and read a hostile path as a match.
func (o Operation) Validate() error {
	if !allowedMethod(o.Method) {
		return fmt.Errorf("%w: method %q", ErrInvalidOperation, o.Method)
	}

	if err := validatePath(o.Path); err != nil {
		return err
	}

	return validateActions(o)
}

// allowedMethod reports whether the method is on the allowlist.
func allowedMethod(method string) bool {
	switch method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE":
		return true
	default:
		return false
	}
}

var (
	literalSegment = regexp.MustCompile(`^[a-z0-9-]+$`)
	wildcardName   = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)
	actionGrammar  = regexp.MustCompile(`^[a-z]+:[a-z]+(-[a-z]+)*$`)
)

// validatePath allows a path under Prefix whose segments are literals
// ([a-z0-9-]+) or wildcards. A wildcard is {name} with name [a-z_][a-z0-9_]*.
// {name...} and {$} are allowed in the last segment only.
func validatePath(path string) error {
	rest, ok := strings.CutPrefix(path, Prefix)
	if !ok || rest == "" {
		return fmt.Errorf("%w: path %q is outside %s", ErrInvalidOperation, path, Prefix)
	}

	segments := strings.Split(rest, "/")
	for i, seg := range segments {
		last := i == len(segments)-1
		if !validSegment(seg, last) {
			return fmt.Errorf("%w: path %q holds the segment %q that is not allowed", ErrInvalidOperation, path, seg)
		}
	}

	return nil
}

// validSegment reports whether one path segment is a literal or a wildcard
// that is allowed at its place. An empty segment, a dot segment, an encoded
// dot, a query, a fragment, a space and CR or LF all fail the literal rule.
func validSegment(seg string, last bool) bool {
	if literalSegment.MatchString(seg) {
		return true
	}

	name, ok := strings.CutPrefix(seg, "{")
	if !ok {
		return false
	}

	name, ok = strings.CutSuffix(name, "}")
	if !ok {
		return false
	}

	if name == "$" {
		return last
	}

	if base, rest := strings.CutSuffix(name, "..."); rest {
		return last && wildcardName.MatchString(base)
	}

	return wildcardName.MatchString(name)
}

// validateActions refuses an empty action, a repeated action and an action
// that breaks the grammar <area>:<verb>. A public route has no actions. A
// route that is not public has at least one.
func validateActions(o Operation) error {
	if o.Public && len(o.Actions) > 0 {
		return fmt.Errorf("%w: actions of %s %s are set on a public route", ErrInvalidOperation, o.Method, o.Path)
	}

	if !o.Public && len(o.Actions) == 0 {
		return fmt.Errorf(
			"%w: actions of %s %s are missing on a route that is not public",
			ErrInvalidOperation,
			o.Method,
			o.Path,
		)
	}

	seen := make(map[string]bool, len(o.Actions))

	for _, action := range o.Actions {
		if !actionGrammar.MatchString(action) {
			return fmt.Errorf("%w: action %q is not <area>:<verb>", ErrInvalidOperation, action)
		}

		if seen[action] {
			return fmt.Errorf("%w: action %q appears twice", ErrInvalidOperation, action)
		}

		seen[action] = true
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
	// Mismatch holds the keys of the operations that exist in both lists but
	// differ in their path, their actions or their public flag.
	Mismatch []string
}

// Empty reports whether the route table and the description agree.
func (d Drift) Empty() bool {
	return len(d.Undescribed) == 0 && len(d.Unrouted) == 0 && len(d.Mismatch) == 0
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

		// The key ignores the names of wildcards, but a generator turns them
		// into the names of SDK parameters, so the paths must match exactly.
		if route.Path != doc.Path || route.Public != doc.Public || !sameActions(route.Actions, doc.Actions) {
			d.Mismatch = append(d.Mismatch, key)
		}
	}

	for key := range describedByKey {
		if _, ok := routeByKey[key]; !ok {
			d.Unrouted = append(d.Unrouted, key)
		}
	}

	slices.Sort(d.Undescribed)
	slices.Sort(d.Unrouted)
	slices.Sort(d.Mismatch)

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
