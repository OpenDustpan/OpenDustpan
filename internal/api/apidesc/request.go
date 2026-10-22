package apidesc

import (
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// FixCommand is the one command that writes the description again after a
// change of a label.
const FixCommand = "go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description"

// Query is the query of one request, as the label of its route allows a
// handler to read it. For a route that the description holds, it gives the
// values of the declared names only. A route outside Prefix, and a route on
// the list Undescribed, is not in the description, and its handler reads every
// name.
//
// A read of a name that the label does not declare panics: it is an error in
// the code of the handler, and the test of the route finds it, with the name,
// the route and the command that fixes the description.
type Query struct {
	route  string
	values url.Values
	err    error
	// declared holds the names that the handler may read. It is nil for a
	// route that the description does not hold.
	declared []string
}

// QueryOf returns the query of the request. The labels are the table of the
// adapter, keyed by Pattern. The route is the pattern that the ServeMux
// matched: a label for it gives its declared names. A request under Prefix
// with no label and no entry on the list Undescribed gets no name: it fails
// closed.
func QueryOf(r *http.Request, labels map[string]Label) Query {
	// The one place that reads the raw query of the tool-neutral REST API.
	values, err := url.ParseQuery(r.URL.RawQuery)
	q := Query{route: r.Pattern, values: values, err: err, declared: nil}

	label, described := labels[r.Pattern]

	switch {
	case described:
		q.declared = make([]string, 0, len(label.Query))
		for _, p := range label.Query {
			q.declared = append(q.declared, p.Name)
		}
	case outsideDescription(r):
	default:
		// Defense in depth: Collect refuses a route under Prefix with no
		// label, so the server does not start with one. This check stays
		// so that a handler that a request reaches with no matched pattern
		// cannot read a name that no label declares.
		q.declared = []string{}
	}

	return q
}

// outsideDescription reports whether the route of the request is not in the
// description: a path outside Prefix, or a pattern on the list Undescribed.
func outsideDescription(r *http.Request) bool {
	if _, skip := undescribed[r.Pattern]; skip {
		return true
	}

	path := patternPath(r.Pattern)
	if r.Pattern == "" {
		path = r.URL.Path
	}

	return !strings.HasPrefix(path, Prefix)
}

// patternPath returns the path of a pattern "METHOD /path".
func patternPath(pattern string) string {
	if _, path, found := strings.Cut(pattern, " "); found {
		return path
	}

	return pattern
}

// Err returns the error of the parse of the query: the first pair that is
// not valid. The values of the pairs before and after it stay readable, as
// url.URL.Query gives them.
func (q Query) Err() error { return q.err }

// Get returns the first value of a declared name, or "".
func (q Query) Get(name string) string {
	q.allow(name)

	return q.values.Get(name)
}

// Has reports whether the query holds a declared name.
func (q Query) Has(name string) bool {
	q.allow(name)

	return q.values.Has(name)
}

// All returns every value of a declared name, in the order of the query.
func (q Query) All(name string) []string {
	q.allow(name)

	return slices.Clone(q.values[name])
}

// Undeclared returns the names of the query that the label does not declare,
// sorted, so that a handler can refuse them. It returns nil for a route that
// the description does not hold.
func (q Query) Undeclared() []string {
	if q.declared == nil {
		return nil
	}

	var out []string

	for name := range q.values {
		if !slices.Contains(q.declared, name) {
			out = append(out, name)
		}
	}

	slices.Sort(out)

	return out
}

// allow panics when the route is in the description and its label does not
// declare the name.
func (q Query) allow(name string) {
	if q.declared == nil || slices.Contains(q.declared, name) {
		return
	}

	panic(fmt.Sprintf(
		"apidesc: the handler of %q reads the query parameter %q, and the label of the route does not declare it: "+
			"add it with WithQuery, then run %s",
		q.route, name, FixCommand,
	))
}

// requestHeaders lists the request headers that a handler of the
// tool-neutral REST API may read, with the part of the description that holds
// each one. A header that is not on the list panics in Header: a new header
// needs a place in the description first.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant maps.
var requestHeaders = map[string]string{
	"Authorization":   "the bearer security scheme",
	"Content-Type":    "the media type of the request body",
	"Idempotency-Key": "the shared parameter IdempotencyKey",
	"If-Match":        "the shared parameters IfMatch and IfMatchRequired",
	// The two headers below go to the audit record only. They change no
	// answer, and the format of the description has no place for them.
	"Dustpan-Channel": "not described: the audit record only",
	"User-Agent":      "not described: the audit record only",
}

// RequestHeaders returns a copy of the list of the request headers that a
// handler may read, with the part of the description that holds each one.
func RequestHeaders() map[string]string {
	return maps.Clone(requestHeaders)
}

// Header returns the first value of a request header on the list of
// RequestHeaders.
func Header(r *http.Request, name string) string {
	allowHeader(name)

	// The one place that reads the headers of the tool-neutral REST API.
	return r.Header.Get(name)
}

// HeaderValues returns every value of a request header on the list of
// RequestHeaders.
func HeaderValues(r *http.Request, name string) []string {
	allowHeader(name)

	// The one place that reads the headers of the tool-neutral REST API.
	return slices.Clone(r.Header.Values(name))
}

// allowHeader panics for a header that is not on the list of RequestHeaders.
func allowHeader(name string) {
	if _, ok := requestHeaders[http.CanonicalHeaderKey(name)]; ok {
		return
	}

	panic(fmt.Sprintf(
		"apidesc: a handler reads the request header %q, which the description does not hold: "+
			"give it a place in the description and add it to the list of request headers, then run %s",
		name, FixCommand,
	))
}
