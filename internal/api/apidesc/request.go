package apidesc

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
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
// A read of a name that the label does not declare is an error in the code of
// the handler. In a test it panics, so that the test of the route finds it,
// with the name, the route and the command that fixes the description. In the
// server it gives the empty value, as if the request did not hold the name,
// and logs the error once for each route and name, through the Reporter on
// the context of the request.
type Query struct {
	route  string
	values url.Values
	err    error
	// mode says what a read of an undeclared name does.
	mode misuseMode
	// reporter logs a read of an undeclared name in the server.
	reporter *Reporter
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
	return newQuery(r, labels, defaultMode())
}

// newQuery is QueryOf with the choice between the panic and the log.
func newQuery(r *http.Request, labels map[string]Label, mode misuseMode) Query {
	// The one place that reads the raw query of the tool-neutral REST API.
	values, err := url.ParseQuery(r.URL.RawQuery)
	q := Query{
		route: r.Pattern, values: values, err: err, mode: mode, reporter: reporterOf(r.Context()), declared: nil,
	}

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

// Get returns the first value of a declared name, or "". A name that the
// label does not declare gives "".
func (q Query) Get(name string) string {
	if !q.allow(name) {
		return ""
	}

	return q.values.Get(name)
}

// Has reports whether the query holds a declared name. A name that the label
// does not declare gives false.
func (q Query) Has(name string) bool {
	if !q.allow(name) {
		return false
	}

	return q.values.Has(name)
}

// All returns every value of a declared name, in the order of the query. A
// name that the label does not declare gives nil.
func (q Query) All(name string) []string {
	if !q.allow(name) {
		return nil
	}

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

// allow reports whether the handler may read the name. For a route in the
// description whose label does not declare the name, it panics in a test and
// logs the error in the server.
func (q Query) allow(name string) bool {
	if q.declared == nil || slices.Contains(q.declared, name) {
		return true
	}

	q.reporter.report(q.mode, "query parameter", q.route, name,
		"add it with WithQuery, then run "+FixCommand)

	return false
}

// misuseMode says what a read of a name that the description does not hold
// does: panic, in a test, or log, in the server.
type misuseMode int

const (
	modeLog misuseMode = iota
	modePanic
)

// defaultMode returns modePanic in a test and modeLog in the server.
func defaultMode() misuseMode {
	if testing.Testing() {
		return modePanic
	}

	return modeLog
}

// Reporter reports the reads of names that the description does not hold,
// for one server: it logs one error for each kind, route and name through the
// logger of that server. A server puts its Reporter on the context of each
// request with WithReporter, so two servers in one process never share the
// logger or the names already reported.
type Reporter struct {
	logger *slog.Logger
	seen   sync.Map
}

// NewReporter returns a Reporter that logs through l, or through the default
// logger of slog when l is nil.
func NewReporter(l *slog.Logger) *Reporter {
	if l == nil {
		l = slog.Default()
	}

	return &Reporter{logger: l, seen: sync.Map{}}
}

// reporterKey is the context key of the Reporter of a request.
type reporterKey struct{}

// WithReporter returns a copy of ctx that holds r.
func WithReporter(ctx context.Context, r *Reporter) context.Context {
	return context.WithValue(ctx, reporterKey{}, r)
}

// reporterOf returns the Reporter that ctx holds. A context with none gets a
// new Reporter on the default logger of slog, which keeps no names: it logs
// each read.
func reporterOf(ctx context.Context) *Reporter {
	if r, ok := ctx.Value(reporterKey{}).(*Reporter); ok && r != nil {
		return r
	}

	return NewReporter(nil)
}

// report panics in modePanic. Otherwise it logs one error for each
// kind, route and name.
func (m *Reporter) report(mode misuseMode, kind, route, name, fix string) {
	if mode == modePanic {
		panic(fmt.Sprintf(
			"apidesc: the handler of %q reads the %s %q, and the description does not hold it: %s",
			route, kind, name, fix,
		))
	}

	if _, dup := m.seen.LoadOrStore(kind+"\x00"+route+"\x00"+name, struct{}{}); dup {
		return
	}

	m.logger.Error("A handler reads a name that the API description does not hold. The server ignores the name.",
		slog.String("kind", kind), slog.String("route", route), slog.String("name", name), slog.String("fix", fix))
}

// SessionCookie is the name of the cookie of a console session.
const SessionCookie = "__Host-dustpan_session"

// CSRFHeader is the name of the header that carries the CSRF value of a
// console session on a request that can change state.
const CSRFHeader = "X-Dustpan-Csrf"

// requestHeaders lists the request headers that a handler, or a helper that a
// handler calls, may read, with the part of the description that holds each
// one. A header that is not on the list is not read in the server, and it
// panics in a test: a new header needs a place in the description first.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant maps.
var requestHeaders = map[string]string{
	"Authorization":   "the security scheme bearerAuth",
	"Content-Type":    "the media type of the request body",
	"Idempotency-Key": "the shared parameter IdempotencyKey",
	"If-Match":        "the shared parameters IfMatch and IfMatchRequired",
	CSRFHeader:        "the security scheme csrfHeader",
	// The headers below change no answer of a route, or only name where the
	// request comes from. The description has no place for them.
	"Dustpan-Channel": "not described: the audit record only",
	"User-Agent":      "not described: the audit record only",
	"Origin":          "not described: set by the browser; the server checks it against the allowed origins",
	"Sec-Fetch-Site":  "not described: set by the browser; the server refuses a cross-site request with a session",
	"Forwarded":       "not described: set by the proxy that the server trusts, for the address of the client",
	"X-Forwarded-For": "not described: set by the proxy that the server trusts, for the address of the client",
}

// requestCookies lists the cookies that a handler, or a helper that a handler
// calls, may read, with the part of the description that holds each one.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant maps.
var requestCookies = map[string]string{
	SessionCookie: "the security scheme sessionCookie",
}

// RequestHeaders returns a copy of the list of the request headers that a
// handler may read, with the part of the description that holds each one.
func RequestHeaders() map[string]string {
	return maps.Clone(requestHeaders)
}

// RequestCookies returns a copy of the list of the request cookies that a
// handler may read, with the part of the description that holds each one.
func RequestCookies() map[string]string {
	return maps.Clone(requestCookies)
}

// Header returns the first value of a request header on the list of
// RequestHeaders. A header that is not on the list gives "".
func Header(r *http.Request, name string) string {
	if !allowHeader(r, name) {
		return ""
	}

	// The one place that reads the headers of the tool-neutral REST API.
	return r.Header.Get(name)
}

// HeaderValues returns every value of a request header on the list of
// RequestHeaders. A header that is not on the list gives nil.
func HeaderValues(r *http.Request, name string) []string {
	if !allowHeader(r, name) {
		return nil
	}

	// The one place that reads the headers of the tool-neutral REST API.
	return slices.Clone(r.Header.Values(name))
}

// Cookie returns the value of a request cookie on the list of RequestCookies,
// and false when the request has no such cookie. A cookie that is not on the
// list gives false.
func Cookie(r *http.Request, name string) (string, bool) {
	if _, ok := requestCookies[name]; !ok {
		reporterOf(r.Context()).report(defaultMode(), "request cookie", r.Pattern, name,
			"give it a place in the description and add it to the list of request cookies, then run "+FixCommand)

		return "", false
	}

	// The one place that reads the cookies of the tool-neutral REST API.
	c, err := r.Cookie(name)
	if err != nil {
		return "", false
	}

	return c.Value, true
}

// allowHeader reports whether the header is on the list of RequestHeaders. A
// header that is not on it panics in a test and is logged in the server.
func allowHeader(r *http.Request, name string) bool {
	if _, ok := requestHeaders[http.CanonicalHeaderKey(name)]; ok {
		return true
	}

	reporterOf(r.Context()).report(defaultMode(), "request header", r.Pattern, name,
		"give it a place in the description and add it to the list of request headers, then run "+FixCommand)

	return false
}
