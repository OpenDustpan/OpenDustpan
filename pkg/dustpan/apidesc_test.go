package dustpan_test

import (
	"bytes"
	"errors"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/pkg/dustpan"
)

// updateDescription rewrites the checked-in description from the routes:
//
//	go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description
//
//nolint:gochecknoglobals // A test flag is read once; passing it as a parameter adds nothing.
var updateDescription = flag.Bool(
	"update-api-description", false, "write internal/api/apidesc/openapi.json from the route tables",
)

// descriptionFile is the checked-in description, from this package.
const descriptionFile = "../../internal/api/apidesc/openapi.json"

// openAPIPath is the route that serves the description.
const openAPIPath = "/api/dustpan/v1/openapi.json"

// publicOperations returns the operations of the description that need no
// sign-in. A new public route needs a reason, so it needs this list to change.
func publicOperations() []string {
	return []string{"getApiDescription", "getCapabilities"}
}

// TestAPIDescriptionGolden proves that the checked-in description is what the
// writer makes from the operations of the server with every module on.
func TestAPIDescriptionGolden(t *testing.T) {
	t.Parallel()

	got, err := apidesc.Build(newEveryModuleServer(t).Operations())
	if err != nil {
		t.Fatalf("build the description: %v", err)
	}

	if *updateDescription {
		if writeErr := os.WriteFile(descriptionFile, got, 0o600); writeErr != nil {
			t.Fatalf("write %s: %v", descriptionFile, writeErr)
		}

		return
	}

	if !bytes.Equal(got, apidesc.Document()) {
		t.Fatal("the checked-in description differs from the routes: " +
			"run go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description")
	}
}

// TestAPIDescriptionDrift proves that the operations of the server and the
// operations of the checked-in description agree in every field: the
// operationId, the path with the names of its wildcards, the tags, the
// actions, the public flag and the header flags. A rename of an operationId
// fails here.
func TestAPIDescriptionDrift(t *testing.T) {
	t.Parallel()

	described, err := apidesc.Read(apidesc.Document())
	if err != nil {
		t.Fatalf("read the checked-in description: %v", err)
	}

	drift, err := apidesc.Diff(newEveryModuleServer(t).Operations(), described)
	if err != nil {
		t.Fatalf("compare: %v", err)
	}

	if !drift.Empty() {
		t.Fatalf("the routes and the description differ:\nno entry: %v\nno route: %v\ndiffer: %v",
			drift.Undescribed, drift.Unrouted, drift.Mismatch)
	}
}

// TestEveryRouteHasOneClass proves that each route that the adapters serve is
// in exactly one of three classes: under the prefix and in the description, on
// the list of paths that stay outside by design, or on the not-moved list.
func TestEveryRouteHasOneClass(t *testing.T) {
	t.Parallel()

	var patterns []string

	for _, source := range newEveryModuleServer(t).RouteSources() {
		patterns = append(patterns, source.Patterns...)
	}

	if problems := checkClasses(patterns, notMoved); len(problems) > 0 {
		t.Fatalf("routes that are not in exactly one class:\n%s", strings.Join(problems, "\n"))
	}
}

// TestNotMovedListIsConsistent proves that a route on the not-moved list
// moves to a path that no served route has, that it changes its path, and that
// no two routes move to one path.
func TestNotMovedListIsConsistent(t *testing.T) {
	t.Parallel()

	described, err := apidesc.Read(apidesc.Document())
	if err != nil {
		t.Fatalf("read the checked-in description: %v", err)
	}

	taken := map[string]string{}
	for _, op := range described {
		taken[op.Key()] = "the description"
	}

	for old, newPath := range notMoved {
		method, oldPath, _ := strings.Cut(old, " ")

		if oldPath == newPath {
			t.Errorf("%s moves to its own path", old)
		}

		if !strings.HasPrefix(newPath, apidesc.Prefix) && !isOutsideByDesign(newPath) {
			t.Errorf("%s moves to %s, which is neither under the prefix nor outside by design", old, newPath)
		}

		key := hostileOperation(func(o *apidesc.Operation) { o.Method, o.Path = method, newPath }).Key()
		if other, dup := taken[key]; dup {
			t.Errorf("%s moves to %s, which %s has already", old, newPath, other)
		}

		taken[key] = old
	}
}

// TestClassCheckFailsClosed proves the check of the classes for hostile input:
// a route in no class, a route in two classes, and an entry of the not-moved
// list that nothing serves each give a problem.
func TestClassCheckFailsClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		patterns []string
		moved    map[string]string
		want     string
	}{
		{"route in no class", []string{"GET /api/other/x"}, nil, "is in no class"},
		{"route outside the prefix and not listed", []string{"GET /api/server/x"}, nil, "is in no class"},
		{
			"route under the prefix on the not-moved list is in that class only",
			[]string{"GET /api/dustpan/v1/x/{id}"},
			map[string]string{"GET /api/dustpan/v1/x/{id}": "/api/dustpan/v1/x/{x_id}"},
			"",
		},
		{
			"route that is outside by design and on the not-moved list",
			[]string{"GET /runner/v1/poll"},
			map[string]string{"GET /runner/v1/poll": "/api/dustpan/v1/poll"},
			"is in 2 classes",
		},
		{
			"entry that no adapter serves",
			[]string{"GET /api/dustpan/v1/x"},
			map[string]string{"GET /api/server/gone": "/api/dustpan/v1/gone"},
			"no adapter serves it",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			problems := strings.Join(checkClasses(tt.patterns, tt.moved), "\n")

			if tt.want == "" {
				if problems != "" {
					t.Fatalf("problems = %q, want none", problems)
				}

				return
			}

			if !strings.Contains(problems, tt.want) {
				t.Fatalf("problems = %q, want it to hold %q", problems, tt.want)
			}
		})
	}
}

// TestClassesOfKnownRoutes pins the class of one route of each kind.
func TestClassesOfKnownRoutes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		pattern string
		want    routeClass
	}{
		{"GET /api/dustpan/v1/capabilities", classDescribed},
		{"GET /api/stacks/{namespace}/{project}/{stack}", classOutside},
		{"GET /api/user", classOutside},
		{"GET /api/v2/ping", classOutside},
		{"GET /tofu/state/{namespace}/{workspace}", classOutside},
		{"POST /runner/v1/poll", classOutside},
		{"GET /api/server/backups", classNotMoved},
		{"GET /api/server/sign-in/{provider}/callback", classNotMoved},
		{"GET /api/audit/entries", classNotMoved},
		{"GET /api/dustpan/v1/namespaces/-/units/{unitID}", classNotMoved},
	}

	for _, tt := range tests {
		got := classesOf(tt.pattern, notMoved)
		if len(got) != 1 || got[0] != tt.want {
			t.Errorf("classesOf(%q) = %v, want [%s]", tt.pattern, got, tt.want)
		}
	}
}

// TestOpenAPIRouteServesTheCheckedInBytes proves that the route answers the
// bytes of the checked-in file, on every build and for every request.
func TestOpenAPIRouteServesTheCheckedInBytes(t *testing.T) {
	t.Parallel()

	handler := newEveryModuleServer(t).Handler()

	for _, target := range []string{openAPIPath, openAPIPath + "?format=yaml&x=%3Cscript%3E"} {
		rec := serveDescription(t, handler, http.MethodGet, target, nil)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", target, rec.Code)
		}

		if !bytes.Equal(rec.Body.Bytes(), apidesc.Document()) {
			t.Fatalf("GET %s does not answer the bytes of the checked-in description", target)
		}

		if got := rec.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
	}
}

// TestOpenAPIRouteIsTheSameForEveryBuild proves that the mount state of a
// module does not change the document: a server without the optional modules
// serves the same bytes as a server with all of them.
func TestOpenAPIRouteIsTheSameForEveryBuild(t *testing.T) {
	t.Parallel()

	full := serveDescription(t, newEveryModuleServer(t).Handler(), http.MethodGet, openAPIPath, nil)

	p := newPorts(t)

	bare, err := dustpan.NewServer(fullServerOptions(t, p.st, p.blobs, p.authn)...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	got := serveDescription(t, bare.Handler(), http.MethodGet, openAPIPath, nil)

	if got.Code != http.StatusOK || !bytes.Equal(got.Body.Bytes(), full.Body.Bytes()) {
		t.Fatalf("a server without the optional modules serves another document (status %d)", got.Code)
	}
}

// serveDescription sends one request to the handler, with no credential unless
// header says otherwise, and returns the recorded answer.
func serveDescription(
	t *testing.T, handler http.Handler, method, target string, header map[string]string,
) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, target, http.NoBody)
	for key, value := range header {
		req.Header.Set(key, value)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

// hostileOperation returns a valid operation with one change, which the
// writer must refuse.
func hostileOperation(change func(o *apidesc.Operation)) apidesc.Operation {
	o := apidesc.Operation{
		ID: "getThing", Method: http.MethodGet, Path: "/api/dustpan/v1/things", Tags: []string{"things"},
		Actions: []string{"thing:read"}, Public: false, IdempotencyKey: false, IfMatch: false, ETag: false,
	}
	change(&o)

	return o
}

// TestThreatStep1CheckTheInput proves that the writer refuses a hostile
// operation, as if the server had not checked it before.
func TestThreatStep1CheckTheInput(t *testing.T) {
	t.Parallel()

	hostile := map[string]apidesc.Operation{
		"method TRACE": hostileOperation(func(o *apidesc.Operation) { o.Method = "TRACE" }),
		"dot segments": hostileOperation(
			func(o *apidesc.Operation) { o.Path = "/api/dustpan/v1/x/../../server" },
		),
		"line feed in a path": hostileOperation(
			func(o *apidesc.Operation) { o.Path = "/api/dustpan/v1/x\r\nX-Evil: 1" },
		),
		"quote in the ID":     hostileOperation(func(o *apidesc.Operation) { o.ID = "get\"X" }),
		"quote in a tag":      hostileOperation(func(o *apidesc.Operation) { o.Tags = []string{"x\"]}"} }),
		"line feed in action": hostileOperation(func(o *apidesc.Operation) { o.Actions = []string{"a:b\n"} }),
		"rest wildcard":       hostileOperation(func(o *apidesc.Operation) { o.Path = "/api/dustpan/v1/{rest...}" }),
		"path outside":        hostileOperation(func(o *apidesc.Operation) { o.Path = "/api/server/things" }),
	}

	for name, op := range hostile {
		doc, err := apidesc.Build([]apidesc.Operation{op})
		if !errors.Is(err, apidesc.ErrInvalidOperation) || doc != nil {
			t.Errorf("%s: Build() = %d bytes, %v; want ErrInvalidOperation and no bytes", name, len(doc), err)
		}
	}
}

// TestThreatStep2EncodeOrContain proves that the route builds nothing from the
// request: hostile headers and a hostile query change neither the body nor the
// headers of the answer.
func TestThreatStep2EncodeOrContain(t *testing.T) {
	t.Parallel()

	handler := newEveryModuleServer(t).Handler()
	hostile := map[string]string{
		"X-Forwarded-Host": "evil.example",
		"Accept":           "text/html",
		"Accept-Language":  "<script>",
		"Origin":           "https://evil.example",
		"X-Request-Id":     "x-evil-id",
		"Content-Type":     "text/html",
		"If-None-Match":    "*",
	}

	rec := serveDescription(t, handler, http.MethodGet, openAPIPath+"?callback=alert(1)%0d%0aX-Evil:1", hostile)

	if !bytes.Equal(rec.Body.Bytes(), apidesc.Document()) {
		t.Fatal("the answer depends on the request")
	}

	for name, values := range rec.Header() {
		for _, value := range values {
			if strings.Contains(value, "evil") || strings.Contains(value, "alert(1)") ||
				strings.ContainsAny(value, "\r\n") {
				t.Errorf("header %s holds a value from the request: %q", name, value)
			}
		}
	}

	if rec.Header().Get("X-Evil") != "" {
		t.Error("the answer sets a header that the request chose")
	}
}

// TestThreatStep3LimitTheEffect proves that the route is read only, that the
// size is bounded, and that the document holds no secret of the server.
func TestThreatStep3LimitTheEffect(t *testing.T) {
	t.Parallel()

	handler := newEveryModuleServer(t).Handler()

	if size := len(apidesc.Document()); size == 0 || size > 1<<20 {
		t.Errorf("the document has %d bytes, want 1 to %d", size, 1<<20)
	}

	admin := map[string]string{"Authorization": "Bearer " + adminToken}

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		rec := serveDescription(t, handler, method, openAPIPath, admin)

		if rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 405 or 404", method, openAPIPath, rec.Code)
		}
	}

	for _, secret := range []string{adminToken, "0123456789abcdef0123456789abcdef"} {
		if bytes.Contains(apidesc.Document(), []byte(secret)) {
			t.Errorf("the document holds the secret %q", secret)
		}
	}
}

// TestThreatStep4CheckAuthorization proves that the public list of the
// description is explicit and short, and that a closed route still asks for a
// credential.
func TestThreatStep4CheckAuthorization(t *testing.T) {
	t.Parallel()

	described, err := apidesc.Read(apidesc.Document())
	if err != nil {
		t.Fatalf("read the checked-in description: %v", err)
	}

	var public []string

	for _, op := range described {
		if op.Public {
			public = append(public, op.ID)
		}
	}

	slices.Sort(public)

	if !slices.Equal(public, publicOperations()) {
		t.Fatalf("public operations = %v, want %v", public, publicOperations())
	}

	handler := newEveryModuleServer(t).Handler()

	// A closed route still asks for a credential, with the description on.
	closed := serveDescription(t, handler, http.MethodGet, "/api/dustpan/v1/me/preferences", nil)
	if closed.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/dustpan/v1/me/preferences with no credential = %d, want 401", closed.Code)
	}

	// A wrong credential does not close the public route.
	wrong := map[string]string{"Authorization": "Bearer wrong"}
	if rec := serveDescription(t, handler, http.MethodGet, openAPIPath, wrong); rec.Code != http.StatusOK {
		t.Errorf("GET %s with a wrong credential = %d, want 200", openAPIPath, rec.Code)
	}
}

// TestThreatStep5FailClosed proves that a document that Read does not know
// gives no operations.
func TestThreatStep5FailClosed(t *testing.T) {
	t.Parallel()

	const head = `{"openapi":"3.1.0","info":{"version":"0.1.0"},"paths":`

	for name, doc := range map[string]string{
		"empty":         "",
		"not JSON":      "openapi: 3.1.0",
		"other version": `{"openapi":"3.0.3","info":{"version":"0.1.0"},"paths":{}}`,
		"other release": `{"openapi":"3.1.0","info":{"version":"9.9.9"},"paths":{}}`,
		"unknown method": head +
			`{"/api/dustpan/v1/x":{"trace":{"operationId":"traceX"}}}}`,
		"unknown security": head +
			`{"/api/dustpan/v1/x":{"get":{"operationId":"getX","security":[{"other":[]}]}}}}`,
		"unknown parameter": head +
			`{"/api/dustpan/v1/x":{"get":{"operationId":"getX","parameters":[{"$ref":"#/components/parameters/Evil"}]}}}}`,
	} {
		ops, err := apidesc.Read([]byte(doc))
		if err == nil || ops != nil {
			t.Errorf("Read(%s) = %d operations, %v; want an error and none", name, len(ops), err)
		}
	}
}
