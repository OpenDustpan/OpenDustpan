package drift_test

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc/apidesctest"
	driftapi "opendustpan.org/opendustpan/internal/api/drift"
	"opendustpan.org/opendustpan/pkg/drift"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// packageSources holds every Go file in this package, so TestNoMuxBypass can
// prove that no production file reaches the mux without recording.
//
//go:embed *.go
var packageSources embed.FS

const (
	testToken  = "drift-test-token"
	testUnitID = "ws-1"
	driftURL   = "/api/dustpan/v1/namespaces/acme/units/" + testUnitID + "/drift"
	checksURL  = "/api/dustpan/v1/namespaces/acme/units/" + testUnitID + "/drift-checks"
)

// fakeNamespaces answers every namespace except "absent", homed in the test
// region.
type fakeNamespaces struct{}

func (fakeNamespaces) CreateNamespace(context.Context, namespace.Namespace) error { return nil }

func (fakeNamespaces) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	if name == "absent" {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.Unix(0, 0), Name: name, HomeRegion: "test-region", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}, nil
}

func (fakeNamespaces) SetOverrides(context.Context, namespace.Name, namespace.Overrides, namespace.Fence) error {
	return nil
}

func (fakeNamespaces) ListNamespaces(context.Context) ([]namespace.Namespace, error) { return nil, nil }

func (fakeNamespaces) Ping(context.Context) error { return nil }

// fakeUnits holds the workspace ws-1 in acme, and a unit ws-2 in the namespace
// other. A reader that answers for another namespace is a hostile reader:
// "liar" gives the unit of acme for any ID.
type fakeUnits struct {
	liar bool
}

func entryOf(ns namespace.Name, unitID, name string) coreinventory.Entry {
	return coreinventory.Entry{
		ManualLock:  nil,
		LastChanged: time.Unix(0, 0), Namespace: ns, Tool: "opentofu", Name: name, UnitID: unitID,
		DisplayName: "", Status: "", ResourceCount: nil, Lock: nil, Collection: "", Labels: nil,
	}
}

func (f fakeUnits) ListUnits(_ context.Context, ns namespace.Name) ([]coreinventory.Entry, error) {
	switch ns {
	case "acme":
		return []coreinventory.Entry{entryOf("acme", testUnitID, "net")}, nil
	case "other":
		return []coreinventory.Entry{entryOf("other", "ws-2", "db")}, nil
	default:
		return nil, nil
	}
}

// QueryUnits implements coreinventory.Reader over the entries of ListUnits.
func (f fakeUnits) QueryUnits(ctx context.Context, q coreinventory.UnitQuery) ([]coreinventory.Entry, error) {
	all, err := f.ListUnits(ctx, q.Namespace)
	if err != nil {
		return nil, err
	}

	return coreinventory.PageUnits(all, q), nil
}

func (f fakeUnits) GetUnit(ctx context.Context, unitID string) (coreinventory.Entry, error) {
	if f.liar {
		return entryOf("other", unitID, "db"), nil
	}

	for _, ns := range []namespace.Name{"acme", "other"} {
		entries, _ := f.ListUnits(ctx, ns) //nolint:errcheck // The fake never fails.
		for i := range entries {
			if entries[i].UnitID == unitID {
				return entries[i], nil
			}
		}
	}

	return coreinventory.Entry{}, coreinventory.ErrUnitNotFound
}

// fakeResults is a drift.Store that reads the results in latest and counts the
// calls. The handler never writes.
type fakeResults struct {
	latest map[graph.Unit]drift.Latest
	err    error
	reads  int
	mu     sync.Mutex
}

func newResults() *fakeResults {
	return &fakeResults{latest: map[graph.Unit]drift.Latest{}, err: nil, reads: 0, mu: sync.Mutex{}}
}

func (*fakeResults) Record(context.Context, drift.Result, namespace.Fence) (drift.Transition, error) {
	panic("a drift route never records a result")
}

func (f *fakeResults) GetLatest(_ context.Context, u graph.Unit) (drift.Latest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.reads++

	if f.err != nil {
		return drift.Latest{}, f.err
	}

	l, ok := f.latest[u]
	if !ok {
		return drift.Latest{}, drift.ErrNotFound
	}

	return l, nil
}

func (*fakeResults) DeleteLatest(context.Context, graph.Unit, namespace.Fence) error {
	panic("a drift route never deletes a result")
}

// fakeChecker is a drift.Checker that records its calls.
type fakeChecker struct {
	id     job.ID
	err    error
	actors []job.Actor
	reqs   []drift.CheckRequest
	mu     sync.Mutex
}

func newChecker() *fakeChecker {
	return &fakeChecker{id: "job-fake1", err: nil, actors: nil, reqs: nil, mu: sync.Mutex{}}
}

func (f *fakeChecker) Check(_ context.Context, a job.Actor, req drift.CheckRequest) (job.ID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.actors, f.reqs = append(f.actors, a), append(f.reqs, req)

	if f.err != nil {
		return "", f.err
	}

	return f.id, nil
}

func (f *fakeChecker) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.reqs)
}

// tokenAuthenticator signs in the principal ada for testToken.
type tokenAuthenticator struct{}

func (tokenAuthenticator) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	if token != testToken {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return identity.Principal{
		Subject: "ada", DisplayName: "ada", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
	}, nil
}

var (
	// errNoListing reports a call of ListObjects, which no route of this API makes.
	errNoListing = errors.New("no route lists objects")
	// errDriver is a failure of a driver, with a secret in its text.
	errDriver = errors.New("driver: password=hunter2 refused")
)

// recordingAuthorizer allows the checks that holds names, as "<action> <object>",
// and records every check in order.
type recordingAuthorizer struct {
	holds  map[string]bool
	checks []string
	mu     sync.Mutex
}

func newAuthorizer(holds ...string) *recordingAuthorizer {
	a := &recordingAuthorizer{holds: map[string]bool{}, checks: nil, mu: sync.Mutex{}}
	for _, h := range holds {
		a.holds[h] = true
	}

	return a
}

func (a *recordingAuthorizer) Authorize(
	_ context.Context, _ identity.Principal, action identity.Action, obj identity.Object,
) error {
	key := string(action) + " " + obj.String()

	a.mu.Lock()
	defer a.mu.Unlock()

	a.checks = append(a.checks, key)

	if !a.holds[key] {
		return fmt.Errorf("%w: %s", identity.ErrDenied, key)
	}

	return nil
}

// ListObjects implements identity.Authorizer. No route of this API lists
// objects.
func (*recordingAuthorizer) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{}, errNoListing
}

func (a *recordingAuthorizer) seen() []string {
	a.mu.Lock()
	defer a.mu.Unlock()

	return slices.Clone(a.checks)
}

// rig holds the handler and the fakes behind it.
type rig struct {
	handler *driftapi.Handler
	// served is the handler that the tests call: it fails a test when a
	// handler answers a success status that the description does not give.
	served  http.Handler
	authz   *recordingAuthorizer
	results *fakeResults
	checker *fakeChecker
}

type rigOption func(*driftapi.Config)

func newRig(t *testing.T, authz *recordingAuthorizer, opts ...rigOption) *rig {
	t.Helper()

	namespaces, err := namespace.NewService(namespace.Config{Store: fakeNamespaces{}, Now: nil, Region: "test-region"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	results, checker := newResults(), newChecker()

	cfg := driftapi.Config{
		Namespaces: namespaces, Units: fakeUnits{liar: false}, Results: results, Checker: checker,
		Authenticator: tokenAuthenticator{}, Authorizer: authz,
		Audit:          &recordingAudit{Store: nil, mu: sync.Mutex{}, entries: nil},
		OnAuditFailure: nil, DenialLimiter: nil,
		Sessions: nil, Logger: slog.New(slog.DiscardHandler),
	}

	for _, o := range opts {
		o(&cfg)
	}

	handler, err := driftapi.NewHandler(cfg)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return &rig{
		handler: handler,
		served:  apidesctest.CheckStatus(t, handler),
		authz:   authz,
		results: results,
		checker: checker,
	}
}

// request is one call of a test: the method, the path, the token and the body.
type request struct {
	method, path, token, body string
	headers                   map[string]string
}

func (r *rig) serve(t *testing.T, q request) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), q.method, q.path, strings.NewReader(q.body))
	if q.token != "" {
		req.Header.Set("Authorization", "token "+q.token)
	}

	for k, v := range q.headers {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	r.served.ServeHTTP(rec, req)

	return rec
}

// holdsAll returns the checks that a person who may read and start holds.
func holdsAll() []string {
	return []string{
		string(identity.ActionNamespaceRead) + " acme",
		string(identity.ActionUnitRead) + " opentofu:acme/net",
		string(identity.ActionUnitRunPlan) + " opentofu:acme/net",
	}
}

// routeCase is a route with the request path and the checks that the route
// makes, in order.
type routeCase struct {
	route  driftapi.Route
	path   string
	checks []string
	reads  int
}

// casesOf returns a routeCase for each route of the table.
func casesOf(t *testing.T) []routeCase {
	t.Helper()

	routes := newRig(t, newAuthorizer()).handler.Routes()
	out := make([]routeCase, 0, len(routes))

	for _, route := range routes {
		path := strings.NewReplacer("{namespace}", "acme", "{unit_id}", testUnitID).Replace(route.Path)
		checks := make([]string, 0, 1+len(route.Reads)+len(route.Actions))
		checks = append(checks, string(identity.ActionNamespaceRead)+" acme")

		for _, a := range route.Reads {
			checks = append(checks, string(a)+" opentofu:acme/net")
		}

		reads := len(checks)

		for _, a := range route.Actions {
			checks = append(checks, string(a)+" opentofu:acme/net")
		}

		out = append(out, routeCase{route: route, path: path, checks: checks, reads: reads})
	}

	return out
}

// wantStatus returns the status that a caller gets when it holds the first
// held checks of the route.
func (rc *routeCase) wantStatus(held int) int {
	switch {
	case held < rc.reads:
		return http.StatusNotFound
	case held < len(rc.checks):
		return http.StatusForbidden
	case rc.route.Method == http.MethodPost:
		return http.StatusAccepted
	default:
		return http.StatusOK
	}
}

// TestRoutesDeclareActions walks every route and fails for a route with no
// scope, no read action or no handler, and for a start route with no start
// action. It checks both directions: every served pattern has a table row, and
// every table row is served.
func TestRoutesDeclareActions(t *testing.T) {
	t.Parallel()

	r := newRig(t, newAuthorizer())
	byPattern := map[string]driftapi.Route{}

	for _, route := range r.handler.Routes() {
		pattern := route.Method + " " + route.Path
		if _, dup := byPattern[pattern]; dup {
			t.Fatalf("duplicate route %q", pattern)
		}

		byPattern[pattern] = route

		checkRouteRow(t, pattern, &route)
	}

	if len(byPattern) != 2 {
		t.Errorf("routes = %d, want the read route and the start route", len(byPattern))
	}

	start := byPattern["POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/drift-checks"]
	if !slices.Equal(start.Actions, []identity.Action{identity.ActionUnitRunPlan}) {
		t.Errorf("start actions = %v, want unit:run-plan alone", start.Actions)
	}

	for _, pattern := range r.handler.RegisteredPatterns() {
		if _, ok := byPattern[pattern]; !ok {
			t.Errorf("served route %q has no table row", pattern)
		}
	}

	checkSignedOut(t, r)
}

// checkRouteRow checks the declarations of one row of the route table.
func checkRouteRow(t *testing.T, pattern string, route *driftapi.Route) {
	t.Helper()

	switch {
	case route.Scope != driftapi.ScopeAuthorized:
		t.Errorf("route %q is not authorized: this API serves no public route", pattern)
	case len(route.Reads) == 0:
		t.Errorf("route %q declares no read action", pattern)
	case route.Serve == nil:
		t.Errorf("route %q declares no handler", pattern)
	case (route.Method == http.MethodGet) != (len(route.Actions) == 0):
		t.Errorf("route %q: only a read route may declare no action", pattern)
	default:
	}
}

// checkSignedOut proves that each route fails closed for a request with no
// token and for a wrong token: it answers 401, never 404.
func checkSignedOut(t *testing.T, r *rig) {
	t.Helper()

	cases := casesOf(t)

	for i := range cases {
		rc := &cases[i]

		for _, token := range []string{"", "wrong"} {
			rec := r.serve(t, request{method: rc.route.Method, path: rc.path, token: token, body: "", headers: nil})
			if rec.Code != http.StatusUnauthorized {
				t.Errorf(
					"route %q with the token %q = %d, want 401",
					rc.route.Method+" "+rc.route.Path,
					token,
					rec.Code,
				)
			}
		}
	}
}

// TestReadOnlyHandlerServesNoStartRoute proves that a server with no job
// service serves the read route alone: the start route is not in the table and
// the mux answers 404 or 405 for it.
func TestReadOnlyHandlerServesNoStartRoute(t *testing.T) {
	t.Parallel()

	r := newRig(t, newAuthorizer(holdsAll()...), func(cfg *driftapi.Config) { cfg.Checker = nil })

	if got := len(r.handler.Routes()); got != 1 {
		t.Fatalf("routes = %d, want the read route alone", got)
	}

	rec := r.serve(t, request{method: http.MethodPost, path: checksURL, token: testToken, body: "", headers: nil})
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST drift-checks = %d, want 404 or 405", rec.Code)
	}
}

// TestNoMuxBypass proves every served route goes through the recorder: it
// fails if any production file in this package builds a ServeMux or touches
// the inner mux of patternMux outside routes.go.
func TestNoMuxBypass(t *testing.T) {
	t.Parallel()

	entries, err := packageSources.ReadDir(".")
	if err != nil {
		t.Fatalf("read package sources: %v", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if name == "routes.go" || strings.HasSuffix(name, "_test.go") {
			continue
		}

		content, err := packageSources.ReadFile(name)
		if err != nil {
			t.Fatalf("read file %s: %v", name, err)
		}

		for _, needle := range []string{"NewServeMux", "servemux"} {
			if strings.Contains(string(content), needle) {
				t.Fatalf("file %s reaches the mux without recording (contains %q)", name, needle)
			}
		}
	}
}

// TestRoutesEnforceTheChecks proves, for every route, the order and the answer
// of the checks: a refused namespace read or unit read answers the one 404, a
// refused action answers 403, and a caller who holds every check passes. A
// refused start never reaches the checker.
func TestRoutesEnforceTheChecks(t *testing.T) {
	t.Parallel()

	cases := casesOf(t)

	for i := range cases {
		rc := &cases[i]

		for held := range len(rc.checks) + 1 {
			t.Run(fmt.Sprintf("%s %s holding %d of %d checks", rc.route.Method, rc.route.Path, held, len(rc.checks)),
				func(t *testing.T) {
					t.Parallel()
					checkRoute(t, rc, held)
				})
		}
	}
}

// checkRoute runs rc with a caller that holds the first held checks.
func checkRoute(t *testing.T, rc *routeCase, held int) {
	t.Helper()

	r := newRig(t, newAuthorizer(rc.checks[:held]...))
	rec := r.serve(t, request{method: rc.route.Method, path: rc.path, token: testToken, body: "", headers: nil})

	if want := rc.wantStatus(held); rec.Code != want {
		t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body.String(), want)
	}

	wantChecks := rc.checks[:min(held+1, len(rc.checks))]
	if got := r.authz.seen(); !slices.Equal(got, wantChecks) {
		t.Errorf("checks = %q, want %q, in order", got, wantChecks)
	}

	complete := held == len(rc.checks)
	if rc.route.Method == http.MethodPost && (r.checker.calls() == 1) != complete {
		t.Errorf("checker calls = %d for status %d", r.checker.calls(), rec.Code)
	}

	if rc.route.Method == http.MethodGet && (r.results.reads == 1) != (held >= rc.reads) {
		t.Errorf("store reads = %d for status %d: a refused reader never reaches the store", r.results.reads, rec.Code)
	}
}

// TestUnknownObjectsAnswerAlike proves an unknown namespace, an unknown unit,
// a unit of another namespace and a unit ID that is no ID answer the same 404
// before any check of the caller runs for the unit, on both routes.
func TestUnknownObjectsAnswerAlike(t *testing.T) {
	t.Parallel()

	paths := []string{
		"/api/dustpan/v1/namespaces/absent/units/" + testUnitID,
		"/api/dustpan/v1/namespaces/acme/units/ws-absent",
		"/api/dustpan/v1/namespaces/other/units/" + testUnitID,
		"/api/dustpan/v1/namespaces/BAD%20NAME/units/" + testUnitID,
		"/api/dustpan/v1/namespaces/acme/units/ws%0A1",
		"/api/dustpan/v1/namespaces/acme/units/" + strings.Repeat("a", 129),
		"/api/dustpan/v1/namespaces/acme/units/.hidden",
		"/api/dustpan/v1/namespaces/acme/units/-lead",
	}

	for _, suffix := range []struct{ method, path string }{{http.MethodGet, "/drift"}, {http.MethodPost, "/drift-checks"}} {
		for _, path := range paths {
			r := newRig(t, newAuthorizer())
			rec := r.serve(
				t,
				request{method: suffix.method, path: path + suffix.path, token: testToken, body: "", headers: nil},
			)

			if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"message":"not found"`) {
				t.Errorf("%s %s = %d %s, want the 404 of an unknown object", suffix.method, path+suffix.path, rec.Code,
					rec.Body.String())
			}

			// The namespace read is the one check that may run, for a namespace that exists.
			if got := r.authz.seen(); len(got) != 0 {
				t.Errorf("%s %s: checks = %q, want none for an object that does not exist", suffix.method, path, got)
			}

			if r.checker.calls() != 0 || r.results.reads != 0 {
				t.Errorf("%s %s reached the checker or the store", suffix.method, path)
			}
		}
	}
}

// TestAHostileInventoryReaderCannotMoveAUnit proves that a unit of another
// namespace never answers under this one, also when the reader gives it.
func TestAHostileInventoryReaderCannotMoveAUnit(t *testing.T) {
	t.Parallel()

	r := newRig(t, newAuthorizer(holdsAll()...), func(cfg *driftapi.Config) { cfg.Units = fakeUnits{liar: true} })

	for _, q := range []request{
		{method: http.MethodGet, path: driftURL, token: testToken, body: "", headers: nil},
		{method: http.MethodPost, path: checksURL, token: testToken, body: "", headers: nil},
	} {
		if rec := r.serve(t, q); rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d %s, want 404", q.method, rec.Code, rec.Body.String())
		}
	}

	if r.checker.calls() != 0 {
		t.Error("a check started on a unit of another namespace")
	}
}

type driftUnitDoc struct {
	ID   string `json:"id"`
	Tool string `json:"tool"`
	Name string `json:"name"`
}

type driftResourceDoc struct {
	Address string `json:"address"`
	Kind    string `json:"kind"`
}

type driftDoc struct {
	Unit      driftUnitDoc       `json:"unit"`
	Status    string             `json:"status"`
	CheckedAt *time.Time         `json:"checkedAt"`
	JobID     string             `json:"jobId"` //nolint:tagliatelle // The description of the API demands camelCase: jobId.
	Resources []driftResourceDoc `json:"resources"`
	Truncated bool               `json:"truncated"`
}

func readDoc(t *testing.T, r *rig) (driftDoc, string) {
	t.Helper()

	rec := r.serve(t, request{method: http.MethodGet, path: driftURL, token: testToken, body: "", headers: nil})
	if rec.Code != http.StatusOK {
		t.Fatalf("GET drift = %d %s, want 200", rec.Code, rec.Body.String())
	}

	var doc driftDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}

	return doc, rec.Body.String()
}

func unitOfTests() graph.Unit {
	return graph.Unit{Namespace: "acme", Tool: "opentofu", ID: testUnitID}
}

// readCase is one case of TestReadDrift.
type readCase struct {
	name      string
	setup     func(f *fakeResults)
	status    string
	resources int
	hasTime   bool
}

// TestReadDrift proves the document of the read: a unit that no check ran on
// is unknown and never in-sync, and a result gives its status, its time, its
// job and its addresses and kinds, and nothing else.
func TestReadDrift(t *testing.T) {
	t.Parallel()

	for _, tt := range readCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t, newAuthorizer(holdsAll()[:2]...))
			tt.setup(r.results)

			doc, raw := readDoc(t, r)
			checkReadDoc(t, &tt, &doc, raw)
		})
	}
}

// checkReadDoc checks the document that a read answered for the case tt.
func checkReadDoc(t *testing.T, tt *readCase, doc *driftDoc, raw string) {
	t.Helper()

	if doc.Status != tt.status || len(doc.Resources) != tt.resources || (doc.CheckedAt != nil) != tt.hasTime {
		t.Fatalf("document = %s, want status %q with %d resources", raw, tt.status, tt.resources)
	}

	if doc.Unit != (driftUnitDoc{ID: testUnitID, Tool: "OpenTofu", Name: "net"}) {
		t.Errorf("unit = %+v, want ws-1, OpenTofu and net", doc.Unit)
	}

	// A resource that no check listed is an empty list, never null.
	if !strings.Contains(raw, `"resources":[`) {
		t.Errorf("document = %s, want a list of resources", raw)
	}

	if tt.hasTime && (!doc.CheckedAt.Equal(checkedAtOfTests()) || doc.JobID != "job-9") {
		t.Errorf("document = %s, want the time and the job of the result", raw)
	}
}

func checkedAtOfTests() time.Time { return time.UnixMilli(1_800_000_000_000).UTC() }

// latestOf returns the latest result of the unit of the tests.
func latestOf(status drift.Status, resources ...drift.Resource) drift.Latest {
	return drift.Latest{
		CheckedAt: checkedAtOfTests(), Unit: unitOfTests(), JobID: "job-9", Status: status, Resources: resources,
		Truncated: false,
	}
}

func readCases() []readCase {
	changed := drift.Resource{Address: "a", Kind: drift.KindChanged}

	return []readCase{
		{"no result is unknown", func(*fakeResults) {}, "unknown", 0, false},
		{
			"a clean result is in-sync",
			func(f *fakeResults) { f.latest[unitOfTests()] = latestOf(drift.StatusInSync) }, "in-sync", 0, true,
		},
		{
			"a drifted result lists its addresses",
			func(f *fakeResults) {
				f.latest[unitOfTests()] = latestOf(drift.StatusDrifted,
					drift.Resource{Address: "aws_s3_bucket.logs", Kind: drift.KindChanged},
					drift.Resource{Address: "aws_instance.web", Kind: drift.KindDeleted})
			},
			"drifted", 2, true,
		},
		{
			"a result of another unit is not served",
			func(f *fakeResults) {
				other := latestOf(drift.StatusDrifted, changed)
				other.Unit.ID = "ws-9"
				f.latest[unitOfTests()] = other
			},
			"unknown", 0, false,
		},
		{
			"a status that no one knows is not served",
			func(f *fakeResults) { f.latest[unitOfTests()] = latestOf("danger", changed) }, "unknown", 0, false,
		},
	}
}

func TestReadDriftTruncated(t *testing.T) {
	t.Parallel()

	r := newRig(t, newAuthorizer(holdsAll()[:2]...))

	l := drift.Latest{
		CheckedAt: time.UnixMilli(1_800_000_000_000).UTC(), Unit: unitOfTests(), JobID: "job-9",
		Status: drift.StatusDrifted, Resources: nil, Truncated: true,
	}
	for i := range drift.MaxResources {
		l.Resources = append(l.Resources, drift.Resource{Address: fmt.Sprintf("a.b%d", i), Kind: drift.KindChanged})
	}

	r.results.latest[unitOfTests()] = l

	doc, _ := readDoc(t, r)
	if !doc.Truncated || len(doc.Resources) != drift.MaxResources {
		t.Fatalf("document = truncated %t with %d resources, want the truncated list of %d", doc.Truncated,
			len(doc.Resources), drift.MaxResources)
	}
}

func TestReadDriftStoreFailure(t *testing.T) {
	t.Parallel()

	r := newRig(t, newAuthorizer(holdsAll()[:2]...))
	r.results.err = errDriver

	rec := r.serve(t, request{method: http.MethodGet, path: driftURL, token: testToken, body: "", headers: nil})
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "hunter2") {
		t.Fatalf("GET drift = %d %s, want 500 with no detail of the driver", rec.Code, rec.Body.String())
	}
}
