package dustpan_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/errcode"
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
//
// exchangeWorkloadToken is public because the JWT of the workload in its body
// is the credential: the exchange checks it against the trust rules.
// signInWithPassword runs before the person has a session: the user name and
// the password in its body are the credential. redeemLocalUserCode is public
// because the one-use code in its body is the credential. beginPasskeySignIn
// runs before the person has a session and names no person, and
// finishPasskeySignIn has the signed response of the passkey in its body as
// the credential.
func publicOperations() []string {
	return []string{
		"beginPasskeySignIn", "exchangeWorkloadToken", "finishPasskeySignIn", "getApiDescription", "getCapabilities",
		"redeemLocalUserCode", "signInWithPassword", "signInWithProviderPassword",
	}
}

// TestAPIDescriptionGolden proves that the checked-in description is what the
// writer makes from the operations of the server with every module on.
func TestAPIDescriptionGolden(t *testing.T) {
	t.Parallel()

	got, err := apidesc.Build(
		newEveryModuleServer(t).Operations(),
		apidesc.WithErrorCodes(catalogCodes()),
		apidesc.WithErrorVariants(catalogVariants()),
	)
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
		t.Fatalf("the routes and the description differ:\n%s", strings.Join(drift.Problems(), "\n"))
	}
}

// TestEveryRouteHasOneClass proves that each route that the adapters serve is
// in exactly one of four classes: described, which means under the prefix and
// in the operations of the server and in the checked-in description; on the
// list of routes that no adapter describes, with a reason; on the list of
// paths that stay outside by design; or on the not-moved list. A path under
// the prefix is not enough: a route that is served and is in neither the
// operations nor the description fails here. The check runs on a server of the
// installation kind dev too, which serves the route of the one-time code.
func TestEveryRouteHasOneClass(t *testing.T) {
	t.Parallel()

	in, patterns := servedRoutes(t, newEveryModuleServer(t), newDevModuleServer(t))

	if problems := checkClasses(patterns, in); len(problems) > 0 {
		t.Fatalf("routes that are not in exactly one class:\n%s", strings.Join(problems, "\n"))
	}
}

// TestEveryRouteCheckSeesAWrappedAdapter proves that the class check of an
// assembled server catches a route that a mounted adapter serves and the
// description lacks, and passes a route that is in the operations and in the
// description. The server is the one of the class check, with one adapter
// wrapped in test code only.
func TestEveryRouteCheckSeesAWrappedAdapter(t *testing.T) {
	t.Parallel()

	base := newEveryModuleServer(t)
	dev := newDevModuleServer(t)

	describedOp, described := describedCapabilities(t, base)
	if !described {
		t.Fatal("the operations and the description lack GET /api/dustpan/v1/capabilities")
	}

	const undescribed = "GET /api/dustpan/v1/things/extra"

	tests := []struct {
		name    string
		pattern string
		op      *apidesc.Operation
		want    string
	}{
		{
			name:    "an undescribed route under the prefix",
			pattern: undescribed,
			op:      nil,
			want:    undescribed,
		},
		{
			name:    "a route that is in the operations and in the description",
			pattern: describedOp.Method + " " + describedOp.Path,
			op:      &describedOp,
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			checkWrappedRoute(t, base.WrappedRoute("server", tt.pattern, tt.op), dev, tt.want)
		})
	}
}

// describedCapabilities returns the capabilities operation of the server and
// reports whether the checked-in description holds it.
func describedCapabilities(t *testing.T, srv *dustpan.Server) (apidesc.Operation, bool) {
	t.Helper()

	described, err := apidesc.Read(apidesc.Document())
	if err != nil {
		t.Fatalf("read the checked-in description: %v", err)
	}

	inDocument := map[string]bool{}
	for i := range described {
		inDocument[described[i].Key()] = true
	}

	ops := srv.Operations()
	for i := range ops {
		if ops[i].Key() == keyOfPattern("GET /api/dustpan/v1/capabilities") {
			return ops[i], inDocument[ops[i].Key()]
		}
	}

	var none apidesc.Operation

	return none, false
}

// checkWrappedRoute runs the checks of the class test on srv and dev and
// reports a problem that does not hold want.
func checkWrappedRoute(t *testing.T, srv, dev *dustpan.Server, want string) {
	t.Helper()

	if _, err := srv.DescribeOperations(); err != nil {
		t.Fatalf("describe the operations: %v", err)
	}

	in, patterns := servedRoutes(t, srv, dev)

	problems := strings.Join(checkClasses(patterns, in), "\n")
	if want == "" {
		if problems != "" {
			t.Fatalf("problems = %q, want none", problems)
		}

		return
	}

	if !strings.Contains(problems, want) {
		t.Fatalf("problems = %q, want it to hold %q", problems, want)
	}
}

// TestEveryRootRouteIsClassified proves that each pattern registered on the
// root mux is the catch-all of the prefix or is on the list rootMounts, and
// that each path the server adds from an adapter is a path the adapter lists.
// The check runs on a server of the installation kind dev too.
func TestEveryRootRouteIsClassified(t *testing.T) {
	t.Parallel()

	for _, srv := range []*dustpan.Server{newEveryModuleServer(t), newDevModuleServer(t)} {
		root := srv.RootPatterns()
		if len(root.Direct) == 0 {
			t.Fatal("the root mux records no pattern")
		}

		var adapterPatterns []string
		for _, source := range srv.RouteSources() {
			adapterPatterns = append(adapterPatterns, source.Patterns...)
		}

		if problems := checkRootPatterns(root.Direct, root.Mounted, adapterPatterns); len(problems) > 0 {
			t.Fatalf("root mux routes that are in no class:\n%s", strings.Join(problems, "\n"))
		}
	}
}

// TestEveryModuleServerMountsEveryAdapter proves that the server of the class
// check has every adapter on. A module that the binary can set and that this
// server leaves off hides its routes from the description, the golden file and
// the class check, so each new module option must be set in newModuleServer.
func TestEveryModuleServerMountsEveryAdapter(t *testing.T) {
	t.Parallel()

	srv := newEveryModuleServer(t)

	mounted := srv.HandlerNames()
	for _, name := range srv.AllHandlerNames() {
		if !slices.Contains(mounted, name) {
			t.Errorf("the adapter %s is off in the server of the class check: set its option in newModuleServer", name)
		}
	}
}

// TestRootCheckFailsClosed proves that a route on the root mux beside the
// catch-all of the prefix fails the check, with the shape of the break that
// the class check of the adapters could not see.
func TestRootCheckFailsClosed(t *testing.T) {
	t.Parallel()

	adapter := []string{"GET /api/dustpan/v1/capabilities"}

	tests := []struct {
		name    string
		direct  []string
		mounted []string
		want    string
	}{
		{"catch-all only", []string{"/api/dustpan/v1/", "GET /health/live"}, nil, ""},
		{
			"a route under the prefix beside the catch-all",
			[]string{"/api/dustpan/v1/", "GET /api/dustpan/v1/rootonly"},
			nil, "GET /api/dustpan/v1/rootonly is on the root mux under the prefix",
		},
		{
			"a method-less route under the prefix",
			[]string{"/api/dustpan/v1/rootonly"},
			nil, "/api/dustpan/v1/rootonly is on the root mux under the prefix",
		},
		{
			"a route under the prefix with a host part",
			[]string{"/api/dustpan/v1/", "GET localhost/api/dustpan/v1/hidden"},
			nil, "GET localhost/api/dustpan/v1/hidden is on the root mux with a host part",
		},
		{
			"a method-less route with a host part",
			[]string{"example.org/api/dustpan/v1/hidden"},
			nil, "example.org/api/dustpan/v1/hidden is on the root mux with a host part",
		},
		{"a root route outside the list", []string{"GET /extra"}, nil, "GET /extra is on the root mux"},
		{"a mounted path no adapter lists", nil, []string{"/api/dustpan/v1/ghost"}, "/api/dustpan/v1/ghost is mounted"},
		{"a mounted path an adapter lists", nil, []string{"/api/dustpan/v1/capabilities"}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			problems := strings.Join(checkRootPatterns(tt.direct, tt.mounted, adapter), "\n")
			if tt.want == "" {
				if problems != "" {
					t.Fatalf("unexpected problems: %s", problems)
				}

				return
			}

			if !strings.Contains(problems, tt.want) {
				t.Fatalf("problems %q do not contain %q", problems, tt.want)
			}
		})
	}
}

// appendNew appends the items of more that list does not hold.
func appendNew(list, more []string) []string {
	for _, item := range more {
		if !slices.Contains(list, item) {
			list = append(list, item)
		}
	}

	return list
}

// servedRoutes returns the patterns that the servers serve, each once, and the
// input of the class check: the keys of the operations of the servers that the
// checked-in description holds too. It fails the test when a mounted handler
// lists no patterns, because the check would not see its routes.
func servedRoutes(t *testing.T, servers ...*dustpan.Server) (classInput, []string) {
	t.Helper()

	described, err := apidesc.Read(apidesc.Document())
	if err != nil {
		t.Fatalf("read the checked-in description: %v", err)
	}

	inDocument := map[string]bool{}
	for i := range described {
		op := &described[i]

		inDocument[op.Key()] = true
	}

	in := newClassInput(notMoved, map[string]bool{}, apidesc.Undescribed())

	var patterns []string

	for _, srv := range servers {
		ops := srv.Operations()
		for i := range ops {
			in.described[ops[i].Key()] = inDocument[ops[i].Key()]
		}

		var listed []string

		for _, source := range srv.RouteSources() {
			listed = append(listed, source.Name)
			patterns = appendNew(patterns, source.Patterns)
		}

		if names := srv.HandlerNames(); !slices.Equal(names, listed) {
			t.Fatalf("the mounted handlers %v differ from the handlers that list their routes %v", names, listed)
		}
	}

	return in, patterns
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
	for i := range described {
		op := &described[i]

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
// a route in no class, a route in two classes, a route under the prefix that
// the description lacks, and an entry of a list that nothing serves each give a
// problem.
func TestClassCheckFailsClosed(t *testing.T) {
	t.Parallel()

	const (
		added  = "GET /api/dustpan/v1/namespaces/-/units/{unit_id}/extra"
		listed = "GET /api/dustpan/v1/namespaces/{namespace}/units"
	)

	tests := []struct {
		name     string
		patterns []string
		in       classInput
		want     string
	}{
		{"route in no class", []string{"GET /api/other/x"}, newClassInput(nil, nil, nil), "is in no class"},
		{
			"route outside the prefix and not listed",
			[]string{"GET /api/server/x"},
			newClassInput(nil, nil, nil),
			"is in no class",
		},
		{
			// A new route under the prefix that no adapter lists and that
			// the description lacks.
			"route under the prefix that is not in the operations",
			[]string{listed, added},
			newClassInput(nil, map[string]bool{keyOfPattern(listed): true}, nil),
			added + " is in no class",
		},
		{
			"route under the prefix that is in the operations and not in the description",
			[]string{listed},
			newClassInput(nil, map[string]bool{keyOfPattern(listed): false}, nil),
			listed + " is in no class",
		},
		{
			"route under the prefix with a part of a listed path",
			[]string{added},
			newClassInput(nil, nil, map[string]string{"GET /api/dustpan/v1/namespaces/-/units/{unitID}": "reason"}),
			"is in no class",
		},
		{
			"route under the prefix that is described",
			[]string{listed},
			newClassInput(nil, map[string]bool{keyOfPattern(listed): true}, nil),
			"",
		},
		{
			"route under the prefix on the undescribed list",
			[]string{listed},
			newClassInput(nil, nil, map[string]string{listed: "reason"}),
			"",
		},
		{
			"route under the prefix that is described and on the undescribed list",
			[]string{listed},
			newClassInput(nil, map[string]bool{keyOfPattern(listed): true}, map[string]string{listed: "reason"}),
			"is in 2 classes",
		},
		{
			"route that is outside by design and on the not-moved list",
			[]string{"GET /runner/v1/poll"},
			newClassInput(map[string]string{"GET /runner/v1/poll": "/api/dustpan/v1/poll"}, nil, nil),
			"is in 2 classes",
		},
		{
			"route below an exact path that stays outside",
			[]string{"GET /sign-in/{id}/other"},
			newClassInput(nil, nil, nil),
			"is in no class",
		},
		{
			"entry of the not-moved list that no adapter serves",
			[]string{"GET /runner/v1/x"},
			newClassInput(map[string]string{"GET /api/server/gone": "/api/dustpan/v1/gone"}, nil, nil),
			"no adapter serves it",
		},
		{
			"entry of the undescribed list that no adapter serves",
			[]string{"GET /runner/v1/x"},
			newClassInput(nil, nil, map[string]string{"GET /api/dustpan/v1/gone": "reason"}),
			"on the undescribed list but no adapter serves it",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			problems := strings.Join(checkClasses(tt.patterns, tt.in), "\n")

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

// TestUndescribedRoutesAreNotInTheDescription proves that a route on the
// undescribed list is in neither the operations nor the checked-in
// description, and that each entry has a reason.
func TestUndescribedRoutesAreNotInTheDescription(t *testing.T) {
	t.Parallel()

	described, err := apidesc.Read(apidesc.Document())
	if err != nil {
		t.Fatalf("read the checked-in description: %v", err)
	}

	keys := map[string]bool{}
	for i := range described {
		op := &described[i]

		keys[op.Key()] = true
	}

	for _, srv := range []*dustpan.Server{newEveryModuleServer(t), newDevModuleServer(t)} {
		for _, op := range srv.Operations() {
			keys[op.Key()] = true
		}
	}

	for pattern, reason := range apidesc.Undescribed() {
		if reason == "" {
			t.Errorf("%s is on the undescribed list with no reason", pattern)
		}

		if keys[keyOfPattern(pattern)] {
			t.Errorf("%s is on the undescribed list and in the description", pattern)
		}
	}
}

// TestClassesOfKnownRoutes pins the class of one route of each kind.
func TestClassesOfKnownRoutes(t *testing.T) {
	t.Parallel()

	in := newClassInput(
		notMoved, map[string]bool{keyOfPattern("GET /api/dustpan/v1/capabilities"): true}, apidesc.Undescribed(),
	)

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
		{"GET /api/server/token-policy", classNotMoved},
		{"GET /api/audit/entries", classNotMoved},
		{"GET /api/dustpan/v1/namespaces/-/units/{unitID}", classUndescribed},
		{"POST /api/dustpan/v1/sign-in/dev-code", classUndescribed},
	}

	for _, tt := range tests {
		got := classesOf(tt.pattern, in)
		if len(got) != 1 || got[0] != tt.want {
			t.Errorf("classesOf(%q) = %v, want [%s]", tt.pattern, got, tt.want)
		}
	}
}

// TestNotMovedSettingsPolicies pins the paths that the decisions of the move
// table give to the policies of the installation.
//
//nolint:gosec // The paths name policies and hold no secret.
func TestNotMovedSettingsPolicies(t *testing.T) {
	t.Parallel()

	want := map[string]string{
		"GET /api/server/token-policy":        "/api/dustpan/v1/settings/token-policy",
		"PATCH /api/server/token-policy":      "/api/dustpan/v1/settings/token-policy",
		"POST /api/server/token-policy/sweep": "/api/dustpan/v1/settings/token-policy/sweep",
		"GET /api/server/session-policy":      "/api/dustpan/v1/settings/session-policy",
		"PATCH /api/server/session-policy":    "/api/dustpan/v1/settings/session-policy",
	}

	for old, newPath := range want {
		if got := notMoved[old]; got != newPath {
			t.Errorf("notMoved[%q] = %q, want %q", old, got, newPath)
		}
	}
}

// TestOpenAPIRouteServesTheCheckedInBytes proves that the route answers the
// bytes of the checked-in file, on every build and for every request.
func TestOpenAPIRouteServesTheCheckedInBytes(t *testing.T) {
	t.Parallel()

	handler := startedHandler(t, newEveryModuleServer(t))

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

	full := serveDescription(t, startedHandler(t, newEveryModuleServer(t)), http.MethodGet, openAPIPath, nil)

	p := newPorts(t)

	bare, err := dustpan.NewServer(fullServerOptions(t, p.st, p.blobs, p.authn)...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	got := serveDescription(t, startedHandler(t, bare), http.MethodGet, openAPIPath, nil)

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
		ID:     "getThing",
		Method: http.MethodGet,
		Path:   "/api/dustpan/v1/things",
		Tags:   []string{"things"},
		Actions: []string{
			"thing:read",
		},
		ActionObjects:   map[string]string{"thing:read": apidesc.KindNamespace},
		Public:          false,
		IdempotencyKey:  false,
		IfMatch:         false,
		IfMatchRequired: false,
		ETag:            false,
		Query:           nil,
		Statuses:        []int{200},
		Bodies:          &apidesc.Bodies{Request: apidesc.NoBody, Response: apidesc.NoBody},
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

	handler := startedHandler(t, newEveryModuleServer(t))
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

	handler := startedHandler(t, newEveryModuleServer(t))

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

	for i := range described {
		op := &described[i]

		if op.Public {
			public = append(public, op.ID)
		}
	}

	slices.Sort(public)

	if !slices.Equal(public, publicOperations()) {
		t.Fatalf("public operations = %v, want %v", public, publicOperations())
	}

	handler := startedHandler(t, newEveryModuleServer(t))

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

// catalogCodes returns the codes of the catalog. The description takes its
// list of error codes from here and from nowhere else.
func catalogCodes() []string {
	specs := errcode.Catalog()
	codes := make([]string, len(specs))

	for i, spec := range specs {
		codes[i] = spec.Code
	}

	return codes
}

// catalogVariants returns the codes of the catalog that have arguments, with
// the type of each argument. The description takes its variants from here and
// from nowhere else.
func catalogVariants() []apidesc.ErrorVariant {
	var variants []apidesc.ErrorVariant

	for _, spec := range errcode.Catalog() {
		if len(spec.Args) == 0 {
			continue
		}

		args := make([]apidesc.ErrorArg, len(spec.Args))
		for i, arg := range spec.Args {
			args[i] = apidesc.ErrorArg{Name: arg.Name, Type: argType(arg.Kind)}
		}

		variants = append(variants, apidesc.ErrorVariant{Code: spec.Code, Args: args})
	}

	return variants
}

// argType returns the type that the description gives to an argument kind. A
// kind that is not known gives the zero type, which the writer refuses.
func argType(kind errcode.Kind) apidesc.ArgType {
	switch kind {
	case errcode.KindIdentifier:
		return apidesc.ArgString
	case errcode.KindQuantity:
		return apidesc.ArgInteger
	case errcode.KindTime:
		return apidesc.ArgTime
	default:
		return 0
	}
}

// TestAPIDescriptionErrorCodesMatchTheCatalog proves that the schema ErrorCode
// of the checked-in description lists the codes of the catalog and no other
// code, and that errorCode is an open enum of that schema.
func TestAPIDescriptionErrorCodesMatchTheCatalog(t *testing.T) {
	t.Parallel()

	var doc descriptionShape

	if err := json.Unmarshal(apidesc.Document(), &doc); err != nil {
		t.Fatalf("read the description: %v", err)
	}

	described := map[string]bool{}
	for _, code := range doc.Components.Schemas["ErrorCode"].Enum {
		described[code] = true
	}

	for _, code := range catalogCodes() {
		if !described[code] {
			t.Errorf(
				"code %q is in the catalog and not in the description: run the golden test with the update flag",
				code,
			)
		}

		delete(described, code)
	}

	for code := range described {
		t.Errorf("code %q is in the description and not in the catalog", code)
	}

	anyOf := doc.Components.Schemas["ErrorResponse"].Properties["errorCode"].AnyOf
	want := []map[string]string{{"$ref": "#/components/schemas/ErrorCode"}, {"type": "string"}}

	if !reflect.DeepEqual(anyOf, want) {
		t.Errorf("errorCode = anyOf %v, want %v (an open enum)", anyOf, want)
	}
}

// TestBuildRefusesBadErrorCodes proves that the writer refuses a list of codes
// that is not valid, and writes an open enum for a valid list.
func TestBuildRefusesBadErrorCodes(t *testing.T) {
	t.Parallel()

	for name, codes := range map[string][]string{
		"twice":    {"a.b", "a.b"},
		"no form":  {"A b"},
		"empty":    {""},
		"newline":  {"a.b\n"},
		"slash":    {"a/b"},
		"space":    {"a.b "},
		"upper":    {"Access.denied"},
		"trailing": {"a."},
	} {
		if _, err := apidesc.Build(nil, apidesc.WithErrorCodes(codes)); !errors.Is(err, apidesc.ErrInvalidBody) {
			t.Errorf("%s: Build() = %v, want ErrInvalidBody", name, err)
		}
	}
}

// descriptionShape is the part of the description that the error code test reads.
type descriptionShape struct {
	Components componentsShape `json:"components"`
}

type componentsShape struct {
	Schemas map[string]schemaShape `json:"schemas"`
}

type schemaShape struct {
	Enum          []string               `json:"enum"`
	Properties    map[string]memberShape `json:"properties"`
	Required      []string               `json:"required"`
	OneOf         []map[string]string    `json:"oneOf"`
	Discriminator discriminatorShape     `json:"discriminator"`
}

type discriminatorShape struct {
	PropertyName string            `json:"propertyName"`
	Mapping      map[string]string `json:"mapping"`
}

type memberShape struct {
	AnyOf      []map[string]string    `json:"anyOf"`
	Ref        string                 `json:"$ref"`
	Type       any                    `json:"type"`
	Format     string                 `json:"format"`
	Enum       []string               `json:"enum"`
	Required   []string               `json:"required"`
	Properties map[string]memberShape `json:"properties"`
}

// wantArgType returns the type and the format that the description gives to an
// argument kind. It is written out here, and not taken from the writer, so
// that a change of the writer fails this test.
func wantArgType(t *testing.T, kind errcode.Kind) argTypeShape {
	t.Helper()

	switch kind {
	case errcode.KindIdentifier:
		return argTypeShape{Type: "string", Format: ""}
	case errcode.KindQuantity:
		return argTypeShape{Type: "integer", Format: ""}
	case errcode.KindTime:
		return argTypeShape{Type: "string", Format: "date-time"}
	default:
		t.Fatalf("kind %d has no type", kind)

		return argTypeShape{Type: "", Format: ""}
	}
}

// argTypeShape is the type and the format of an argument in the description.
type argTypeShape struct{ Type, Format string }

// TestAPIDescriptionErrorVariantsMatchTheCatalog proves that each code of the
// catalog that has arguments has a variant in the checked-in description with
// the same arguments, that a code with no arguments has none, and that
// ErrorAnswer selects the variants by errorCode.
func TestAPIDescriptionErrorVariantsMatchTheCatalog(t *testing.T) {
	t.Parallel()

	var doc descriptionShape

	if err := json.Unmarshal(apidesc.Document(), &doc); err != nil {
		t.Fatalf("read the description: %v", err)
	}

	answer := doc.Components.Schemas[apidesc.ErrorAnswerSchema]
	wantMapping := map[string]string{}

	for _, spec := range errcode.Catalog() {
		name := variantName(spec.Code)
		variant, described := doc.Components.Schemas[name]

		if len(spec.Args) == 0 {
			if described {
				t.Errorf("code %q has no arguments and has the schema %q", spec.Code, name)
			}

			continue
		}

		if !described {
			t.Errorf(
				"code %q has arguments and no schema %q: run the golden test with the update flag",
				spec.Code,
				name,
			)

			continue
		}

		wantMapping[spec.Code] = "#/components/schemas/" + name

		checkVariant(t, spec, variant, doc.Components.Schemas[name+"Args"])
	}

	if !reflect.DeepEqual(answer.Discriminator.Mapping, wantMapping) {
		t.Errorf("ErrorAnswer mapping = %v, want %v", answer.Discriminator.Mapping, wantMapping)
	}

	if answer.Discriminator.PropertyName != "errorCode" {
		t.Errorf("ErrorAnswer discriminator = %q, want errorCode", answer.Discriminator.PropertyName)
	}

	last := len(answer.OneOf) - 1
	if last < 0 || answer.OneOf[last]["$ref"] != "#/components/schemas/ErrorResponse" {
		t.Errorf("ErrorAnswer oneOf = %v, want the base ErrorResponse as the last branch", answer.OneOf)
	}

	if len(answer.OneOf) != len(wantMapping)+1 {
		t.Errorf("ErrorAnswer has %d branches, want %d", len(answer.OneOf), len(wantMapping)+1)
	}
}

// variantName returns the schema name of a code: the parts of the code in
// UpperCamelCase, then Error. It is written out here, and not taken from the
// writer, so that a rename of the writer fails this test.
func variantName(code string) string {
	var name strings.Builder

	for _, part := range strings.FieldsFunc(code, func(r rune) bool { return r == '.' || r == '-' }) {
		name.WriteString(strings.ToUpper(part[:1]) + part[1:])
	}

	return name.String() + "Error"
}

// checkVariant proves that the schema of a code has the arguments of its spec.
func checkVariant(t *testing.T, spec errcode.Spec, variant, args schemaShape) {
	t.Helper()

	if got := variant.Properties["errorCode"].Enum; !reflect.DeepEqual(got, []string{spec.Code}) {
		t.Errorf("%s: errorCode enum = %v, want only the code", spec.Code, got)
	}

	if got := variant.Properties["errorArgs"].Ref; got != "#/components/schemas/"+variantName(spec.Code)+"Args" {
		t.Errorf("%s: errorArgs refers to %q", spec.Code, got)
	}

	if len(args.Properties) != len(spec.Args) {
		t.Errorf(
			"%s: the description has %d arguments, the catalog has %d",
			spec.Code,
			len(args.Properties),
			len(spec.Args),
		)
	}

	want := make([]string, 0, len(spec.Args))

	for _, arg := range spec.Args {
		want = append(want, arg.Name)

		shape := wantArgType(t, arg.Kind)

		got, ok := args.Properties[arg.Name]
		if !ok {
			t.Errorf("%s: argument %q is in the catalog and not in the description", spec.Code, arg.Name)

			continue
		}

		if got.Type != shape.Type || got.Format != shape.Format {
			t.Errorf(
				"%s: argument %q = %v %q, want %s %q",
				spec.Code,
				arg.Name,
				got.Type,
				got.Format,
				shape.Type,
				shape.Format,
			)
		}
	}

	slices.Sort(want)

	gotRequired := slices.Clone(args.Required)
	slices.Sort(gotRequired)

	if !reflect.DeepEqual(gotRequired, want) {
		t.Errorf("%s: required arguments = %v, want %v", spec.Code, gotRequired, want)
	}
}
