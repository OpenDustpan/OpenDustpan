package apidesc_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// listPath is the path of the routes of the tests of the query.
const listPath = "/api/dustpan/v1/namespaces/{namespace}/things"

// pageQuery returns the parameters of a page.
func pageQuery() []apidesc.QueryParam {
	return []apidesc.QueryParam{
		apidesc.IntegerParam("pageSize", "The largest number of things on the page."),
		apidesc.StringParam("pageToken", "The nextPageToken of the page before."),
	}
}

// withQuery returns a GET operation on listPath with the query parameters.
func withQuery(params ...apidesc.QueryParam) apidesc.Operation {
	o := op(http.MethodGet, listPath, "thing:read")
	o.Query = params

	return o
}

// TestQueryRoundTrip proves that the writer puts each query parameter in the
// document, and that Read gives the same parameters back.
func TestQueryRoundTrip(t *testing.T) {
	t.Parallel()

	route := withQuery(append(pageQuery(),
		apidesc.StringParam("label", "Keeps the things with this label.").Repeat(),
		apidesc.BooleanParam("showAll", "Shows every thing.").Require(),
	)...)

	doc, err := apidesc.Build([]apidesc.Operation{route})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	for _, want := range []string{
		`"name": "pageSize"`, `"format": "int64"`, `"in": "query"`, `"type": "array"`, `"required": true`,
	} {
		if !strings.Contains(string(doc), want) {
			t.Errorf("the document does not hold %s", want)
		}
	}

	read, err := apidesc.Read(doc)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	d, err := apidesc.Diff([]apidesc.Operation{route}, read)
	if err != nil || !d.Empty() {
		t.Fatalf("Diff() = %v, %v; want no drift", d.Problems(), err)
	}
}

// TestDiffFindsQueryDrift proves that a query parameter that only one side
// declares, or that the two sides declare in another way, is drift, and that
// the problem names the route and the command that fixes it.
func TestDiffFindsQueryDrift(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		route, desc apidesc.Operation
	}{
		{"the route reads a parameter that the description lacks", withQuery(pageQuery()...), withQuery()},
		{"the description has a parameter that the route does not read", withQuery(), withQuery(pageQuery()...)},
		{
			"the type differs",
			withQuery(apidesc.StringParam("pageSize", "The size.")),
			withQuery(apidesc.IntegerParam("pageSize", "The size.")),
		},
		{
			"one side repeats the parameter",
			withQuery(apidesc.StringParam("label", "A label.").Repeat()),
			withQuery(apidesc.StringParam("label", "A label.")),
		},
		{
			"one side requires the parameter",
			withQuery(apidesc.StringParam("label", "A label.").Require()),
			withQuery(apidesc.StringParam("label", "A label.")),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d, err := apidesc.Diff([]apidesc.Operation{tt.route}, []apidesc.Operation{tt.desc})
			if err != nil || len(d.Mismatch) != 1 {
				t.Fatalf("Diff() = %+v, %v; want one mismatch", d, err)
			}

			problems := strings.Join(d.Problems(), "\n")
			if !strings.Contains(problems, "GET "+listPath) || !strings.Contains(problems, apidesc.FixCommand) {
				t.Fatalf("Problems() = %q, want the route and %q", problems, apidesc.FixCommand)
			}
		})
	}
}

// TestDiffNamesTheRouteAndTheCommand proves that each kind of drift gives a
// problem that names the exact pattern, with the names of its wildcards, and
// the one command that fixes the description.
func TestDiffNamesTheRouteAndTheCommand(t *testing.T) {
	t.Parallel()

	fake := op(http.MethodGet, "/api/dustpan/v1/namespaces/{namespace}/fakes/{fake_id}", "fake:read")

	tests := []struct {
		name              string
		routes, described []apidesc.Operation
		want              string
	}{
		{"a served route with no operation", append(backupRoutes(), fake), backupRoutes(), "is served"},
		{"an operation with no route", backupRoutes(), append(backupRoutes(), fake), "no route serves it"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d, err := apidesc.Diff(tt.routes, tt.described)
			if err != nil {
				t.Fatalf("Diff() error = %v", err)
			}

			checkOneProblem(t, d.Problems(), tt.want)
		})
	}
}

// checkOneProblem fails the test unless there is one problem, that it names
// the fake route and holds want, and that it ends with the command that fixes
// the description.
func checkOneProblem(t *testing.T, got []string, want string) {
	t.Helper()

	if len(got) != 1 {
		t.Fatalf("Problems() = %q, want one problem", got)
	}

	for _, part := range []string{"GET /api/dustpan/v1/namespaces/{namespace}/fakes/{fake_id}", want} {
		if !strings.Contains(got[0], part) {
			t.Errorf("Problems() = %q, want it to hold %q", got[0], part)
		}
	}

	if !strings.HasSuffix(got[0], "run "+apidesc.FixCommand) {
		t.Errorf("Problems() = %q, want it to end with the command %q", got[0], apidesc.FixCommand)
	}
}

// TestValidateRefusesHostileQuery proves that the check of the input refuses
// a hostile query parameter, as if the label had not been reviewed.
func TestValidateRefusesHostileQuery(t *testing.T) {
	t.Parallel()

	str := apidesc.StringParam

	hostile := map[string]apidesc.QueryParam{
		"snake_case name":       str("page_size", "A size."),
		"bracket in the name":   str("page[size]", "A size."),
		"line feed in the name": str("page\nSize", "A size."),
		"empty name":            str("", "A size."),
		"name too long":         str("a"+strings.Repeat("b", 64), "A size."),
		"type outside the list": {
			Name:        "page",
			Type:        "object",
			Repeated:    false,
			Required:    false,
			Description: "A page.",
		},
		"no description":          str("pageSize", ""),
		"line feed in the text":   str("pageSize", "A size.\r\nX-Evil: 1"),
		"description too long":    str("pageSize", strings.Repeat("a", 301)),
		"control character":       str("pageSize", "A size.\x00"),
		"delete character":        str("pageSize", "A size.\x7f"),
		"name that appears twice": str("pageToken", "Again."),
	}

	for name, p := range hostile {
		params := []apidesc.QueryParam{p}
		if name == "name that appears twice" {
			params = append(pageQuery(), p)
		}

		if err := withQuery(params...).Validate(); !errors.Is(err, apidesc.ErrInvalidOperation) {
			t.Errorf("%s: Validate() = %v, want ErrInvalidOperation", name, err)
		}

		if doc, err := apidesc.Build([]apidesc.Operation{withQuery(params...)}); err == nil || doc != nil {
			t.Errorf("%s: Build() = %d bytes, %v; want an error and no bytes", name, len(doc), err)
		}
	}
}

// TestReadRefusesAQueryThatBuildNeverWrites proves that Read fails closed on a
// query parameter or a header parameter that the writer never writes.
func TestReadRefusesAQueryThatBuildNeverWrites(t *testing.T) {
	t.Parallel()

	const head = `{"openapi":"3.1.0","info":{"version":"0.1.0"},"paths":{"/api/dustpan/v1/x":{"get":` +
		`{"operationId":"getX","parameters":[`

	for name, param := range map[string]string{
		"no schema":                  `{"name":"a","in":"query","description":"A."}`,
		"object":                     `{"name":"a","in":"query","description":"A.","schema":{"type":"array"}}`,
		"array of an array":          `{"name":"a","in":"query","schema":{"type":"array","items":{"type":"array","items":{"type":"string"}}}}`,
		"integer without the format": `{"name":"a","in":"query","schema":{"type":"integer"}}`,
		"string with a format":       `{"name":"a","in":"query","schema":{"type":"string","format":"int64"}}`,
		"array with a format":        `{"name":"a","in":"query","schema":{"type":"array","format":"x","items":{"type":"string"}}}`,
		"header parameter":           `{"name":"X-Evil","in":"header","schema":{"type":"string"}}`,
		"cookie parameter":           `{"name":"session","in":"cookie","schema":{"type":"string"}}`,
	} {
		ops, err := apidesc.Read([]byte(head + param + `]}}}}`))
		if !errors.Is(err, apidesc.ErrInvalidDocument) || ops != nil {
			t.Errorf("Read(%s) = %d operations, %v; want ErrInvalidDocument and none", name, len(ops), err)
		}
	}
}

// served is what one request to a test route gives: the answer, or the text
// of the panic of the handler.
type served struct {
	body  string
	panic string
}

// serve registers the handler on a ServeMux at the pattern, sends the target
// and returns the answer or the panic.
func serve(t *testing.T, pattern, target string, handler func(r *http.Request) string) served {
	t.Helper()

	var out served

	mux := http.NewServeMux()
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				out.panic = fmt.Sprint(v)
			}
		}()

		out.body = handler(r)

		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	mux.ServeHTTP(httptest.NewRecorder(), req)

	return out
}

// thingLabels is the table of labels of the test routes.
func thingLabels() map[string]apidesc.Label {
	return map[string]apidesc.Label{
		apidesc.Pattern(http.MethodGet, listPath): apidesc.Name("listThings", "things").
			WithQuery(pageQuery()...),
		apidesc.Pattern(http.MethodGet, listPath+"/{id}"): apidesc.Name("getThing", "things"),
	}
}

// TestQueryOfContainsTheQuery proves that a handler gets the values of the
// declared names, and that a read of another name panics with a message that
// names the route, the parameter and the command that fixes the description.
func TestQueryOfContainsTheQuery(t *testing.T) {
	t.Parallel()

	labels := thingLabels()
	target := "/api/dustpan/v1/namespaces/acme/things?pageSize=5&pageToken=t1&evil=%3Cscript%3E"

	got := serve(t, "GET "+listPath, target, func(r *http.Request) string {
		q := apidesc.QueryOf(r, labels)

		return q.Get("pageSize") + "," + q.Get("pageToken") + "," + strings.Join(q.Undeclared(), ",")
	})
	if got.panic != "" || got.body != "5,t1,evil" {
		t.Fatalf("declared reads = %+v, want 5,t1,evil and no panic", got)
	}

	for name, read := range map[string]func(q apidesc.Query){
		"Get": func(q apidesc.Query) { q.Get("evil") },
		"Has": func(q apidesc.Query) { q.Has("evil") },
		"All": func(q apidesc.Query) { q.All("evil") },
	} {
		got = serve(t, "GET "+listPath, target, func(r *http.Request) string {
			read(apidesc.QueryOf(r, labels))

			return "read"
		})

		for _, want := range []string{"GET " + listPath, `"evil"`, apidesc.FixCommand} {
			if !strings.Contains(got.panic, want) {
				t.Errorf("%s of an undeclared name: panic = %q, want it to hold %q", name, got.panic, want)
			}
		}
	}

	// A route with a label and no query parameter reads no name.
	got = serve(t, "GET "+listPath+"/{id}", "/api/dustpan/v1/namespaces/acme/things/1?pageSize=5",
		func(r *http.Request) string { return apidesc.QueryOf(r, labels).Get("pageSize") })
	if !strings.Contains(got.panic, "pageSize") {
		t.Fatalf("a read on a route that declares no parameter: panic = %q, want one", got.panic)
	}
}

// TestQueryOfOutsideTheDescription proves that a route that the description
// does not hold reads every name: a path outside the prefix and a pattern on
// the list Undescribed.
func TestQueryOfOutsideTheDescription(t *testing.T) {
	t.Parallel()

	undescribedPattern := ""

	for pattern := range apidesc.Undescribed() {
		if strings.HasPrefix(pattern, http.MethodGet+" ") {
			undescribedPattern = pattern
		}
	}

	if undescribedPattern == "" {
		t.Fatal("the list Undescribed holds no GET route")
	}

	tests := []struct{ pattern, target string }{
		{"GET /api/server/things", "/api/server/things?any=1"},
		{
			undescribedPattern,
			strings.ReplaceAll(strings.TrimPrefix(undescribedPattern, "GET "), "{unitID}", "u1") + "?any=1",
		},
	}

	for _, tt := range tests {
		got := serve(t, tt.pattern, tt.target, func(r *http.Request) string {
			q := apidesc.QueryOf(r, thingLabels())
			if q.Undeclared() != nil {
				return "undeclared"
			}

			return q.Get("any")
		})
		if got.panic != "" || got.body != "1" {
			t.Errorf("%s: %+v, want 1 and no panic", tt.pattern, got)
		}
	}
}

// TestQueryOfFailsClosed proves that a request under the prefix whose route
// has no label reads no name, also when no ServeMux matched a pattern.
func TestQueryOfFailsClosed(t *testing.T) {
	t.Parallel()

	got := serve(t, "GET /api/dustpan/v1/unlabeled", "/api/dustpan/v1/unlabeled?pageSize=5",
		func(r *http.Request) string { return apidesc.QueryOf(r, thingLabels()).Get("pageSize") })
	if !strings.Contains(got.panic, "pageSize") {
		t.Errorf("a route with no label: panic = %q, want one", got.panic)
	}

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/api/dustpan/v1/namespaces/acme/things?pageSize=5", http.NoBody,
	)

	func() {
		defer func() {
			if recover() == nil {
				t.Error("a request with no matched pattern under the prefix read a name")
			}
		}()

		apidesc.QueryOf(req, thingLabels()).Get("pageSize")
	}()

	if names := apidesc.QueryOf(req, thingLabels()).Undeclared(); !slices.Equal(names, []string{"pageSize"}) {
		t.Errorf("Undeclared() = %v, want [pageSize]", names)
	}
}

// TestQueryOfKeepsTheParseError proves that a query that is not valid gives
// its error, and that the valid pairs stay readable, as url.URL.Query gives
// them.
func TestQueryOfKeepsTheParseError(t *testing.T) {
	t.Parallel()

	got := serve(t, "GET "+listPath, "/api/dustpan/v1/namespaces/acme/things?pageToken=%zz&pageSize=3",
		func(r *http.Request) string {
			q := apidesc.QueryOf(r, thingLabels())

			return fmt.Sprintf("%t,%s,%s", q.Err() != nil, q.Get("pageSize"), q.Get("pageToken"))
		})
	if got.panic != "" || got.body != "true,3," {
		t.Fatalf("got %+v, want true,3, and no panic", got)
	}
}

// TestHeaderAllowsTheListOnly proves that a handler reads a header on the list
// RequestHeaders, in any case, and that another header panics with its name
// and the command that fixes the description.
func TestHeaderAllowsTheListOnly(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/dustpan/v1/x", http.NoBody)
	req.Header.Set("If-Match", `"3"`)
	req.Header.Add("Idempotency-Key", "k1")
	req.Header.Set("X-Evil", "1")

	if got := apidesc.Header(req, "if-match"); got != `"3"` {
		t.Errorf("Header(if-match) = %q, want \"3\"", got)
	}

	if got := apidesc.HeaderValues(req, "Idempotency-Key"); !slices.Equal(got, []string{"k1"}) {
		t.Errorf("HeaderValues(Idempotency-Key) = %v, want [k1]", got)
	}

	for name, read := range map[string]func(){
		"Header":       func() { apidesc.Header(req, "X-Evil") },
		"HeaderValues": func() { apidesc.HeaderValues(req, "X-Evil") },
	} {
		func() {
			defer func() {
				msg := fmt.Sprint(recover())
				if !strings.Contains(msg, "X-Evil") || !strings.Contains(msg, apidesc.FixCommand) {
					t.Errorf("%s(X-Evil): panic = %q, want the header and the command", name, msg)
				}
			}()

			read()
		}()
	}

	for name, place := range apidesc.RequestHeaders() {
		if place == "" || name != http.CanonicalHeaderKey(name) {
			t.Errorf("request header %q has the place %q, want a place and the canonical name", name, place)
		}
	}
}
