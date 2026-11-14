package selfservice_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	selfserviceapi "opendustpan.org/opendustpan/internal/api/selfservice"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/selfservice"
	"opendustpan.org/opendustpan/internal/selfservicesvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// errExprUnused reports that the test fake does not evaluate expressions.
var errExprUnused = errors.New("expression evaluation is not used by the handler tests")

// draftJSON is a valid draft of blueprint-a in namespace-a.
const draftJSON = `{
	"schemaVersion": 1, "name": "blueprint-a", "title": "Database", "tool": "opentofu",
	"program": {"module": {"source": "registry.example.com/platform/database/aws", "version": "1.2.3"}},
	"unitName": "'databases/' + request.name",
	"inputs": [
		{"name": "size", "type": "integer", "maps": "size", "validate": [
			{"rule": "value >= 1", "message": "Give at least 1."},
			{"rule": "value <= 10", "message": "Give at most 10."}]},
		{"name": "label", "type": "string", "maps": "label", "validate": [
			{"rule": "value != ''", "message": "Give a label."}]}
	],
	"policy": [{"when": "inputs.size > 5", "effect": "deny"},
		{"when": "inputs.size == 5", "effect": "approve", "approvals": 1}],
	"quotas": [{"kind": "units", "scope": "namespace", "max": 20},
		{"kind": "expr", "rule": "usage['units'] < 20", "message": "The limit is 20."},
		{"kind": "expr", "rule": "usage['units'] >= 0", "message": "The count must not be negative."}],
	"delivery": {"homes": ["opendustpan"], "default": "opendustpan"}
}`

const (
	testBase         = "/api/dustpan/v1/self-service/blueprints/namespace-a"
	testDraftPath    = testBase + "/blueprint-a/draft"
	testVersionsPath = testBase + "/blueprint-a/versions"
	testVersionPath  = testVersionsPath + "/1"
)

// httpCase is one request of TestHandler and what it must answer.
type httpCase struct {
	name       string
	method     string
	path       string
	actions    []identity.Action
	body       string
	validDraft bool
	ifMatch    string
	status     int
	code       string
	audited    bool
	etag       string
}

// readActions are the actions of a caller who may read blueprints.
func readActions() []identity.Action {
	return []identity.Action{identity.ActionNamespaceRead, selfservice.ActionBlueprintRead}
}

// allActions are the actions of a caller who may read, publish and retire.
func allActions() []identity.Action {
	return slices.Concat(readActions(), []identity.Action{
		selfservice.ActionBlueprintPublish,
		selfservice.ActionBlueprintRetire,
	})
}

// httpCases returns the requests of TestHandler.
func httpCases() []httpCase {
	reads := readActions()
	all := allActions()

	return []httpCase{
		{
			name: "list with namespace grants", method: http.MethodGet, path: testBase,
			actions: reads, body: "", validDraft: false, ifMatch: "", status: http.StatusOK,
			code: "", audited: false, etag: "",
		},
		{
			name: "list without blueprint read", method: http.MethodGet, path: testBase,
			actions:    []identity.Action{identity.ActionNamespaceRead},
			body:       "",
			validDraft: false, ifMatch: "", status: http.StatusNotFound,
			code: errcode.SelfServiceBlueprintNotFound, audited: true, etag: "",
		},
		{
			name: "list without namespace read", method: http.MethodGet, path: testBase,
			actions:    []identity.Action{selfservice.ActionBlueprintRead},
			body:       "",
			validDraft: false, ifMatch: "", status: http.StatusNotFound,
			code: errcode.SelfServiceBlueprintNotFound, audited: true, etag: "",
		},
		{
			name: "read in another namespace", method: http.MethodGet,
			path:    "/api/dustpan/v1/self-service/blueprints/namespace-b/blueprint-a/draft",
			actions: reads, body: "", validDraft: false, ifMatch: "",
			status: http.StatusNotFound, code: errcode.SelfServiceBlueprintNotFound,
			audited: true, etag: "",
		},
		{
			name: "read draft", method: http.MethodGet, path: testDraftPath,
			actions: reads, body: "", validDraft: false, ifMatch: "", status: http.StatusOK,
			code: "", audited: false, etag: `"1"`,
		},
		{
			name: "read version", method: http.MethodGet, path: testVersionPath,
			actions: reads, body: "", validDraft: false, ifMatch: "", status: http.StatusOK,
			code: "", audited: false, etag: "",
		},
		{
			name: "read draft with invalid name", method: http.MethodGet,
			path:    testBase + "/BAD%20NAME/draft",
			actions: reads, body: "", validDraft: false, ifMatch: "",
			status: http.StatusBadRequest, code: errcode.SelfServiceBlueprintInvalid,
			audited: false, etag: "",
		},
		{
			name: "write draft without publish", method: http.MethodPut, path: testDraftPath,
			actions: reads, body: "", validDraft: true, ifMatch: `"1"`,
			status: http.StatusForbidden, code: "", audited: true, etag: "",
		},
		{
			name: "write draft without If-Match", method: http.MethodPut, path: testDraftPath,
			actions: all, body: "", validDraft: true, ifMatch: "",
			status: http.StatusPreconditionRequired, code: errcode.SelfServiceBlueprintStale,
			audited: false, etag: "",
		},
		{
			name: "write draft with malformed If-Match", method: http.MethodPut, path: testDraftPath,
			actions: all, body: "", validDraft: true, ifMatch: `"invalid"`,
			status: http.StatusPreconditionRequired, code: errcode.SelfServiceBlueprintStale,
			audited: false, etag: "",
		},
		{
			name: "write draft with oversized body", method: http.MethodPut, path: testDraftPath,
			actions: all, body: strings.Repeat("x", selfservice.MaxBlueprintBytes+1), validDraft: false,
			ifMatch: `"1"`, status: http.StatusRequestEntityTooLarge,
			code: errcode.SelfServiceBlueprintTooLarge, audited: false, etag: "",
		},
		{
			name: "publish without publish", method: http.MethodPost, path: testVersionsPath,
			actions: reads, body: `{"draftVersion":1}`, validDraft: false, ifMatch: "",
			status: http.StatusForbidden, code: "", audited: true, etag: "",
		},
		{
			name: "publish with unknown field", method: http.MethodPost, path: testVersionsPath,
			actions: all, body: `{"draftVersion":1,"x":1}`, validDraft: false, ifMatch: "",
			status: http.StatusBadRequest, code: "", audited: false, etag: "",
		},
		{
			name: "retire without retire", method: http.MethodPost, path: testVersionPath + "/retire",
			actions: reads, body: "", validDraft: false, ifMatch: "",
			status: http.StatusForbidden, code: "", audited: true, etag: "",
		},
		{
			name: "wrong method", method: http.MethodDelete, path: testBase,
			actions: all, body: "", validDraft: false, ifMatch: "",
			status: http.StatusMethodNotAllowed, code: "", audited: false, etag: "",
		},
	}
}

// TestHandler serves each request of httpCases and checks its answer.
func TestHandler(t *testing.T) {
	t.Parallel()

	cases := httpCases()
	for i := range cases {
		tc := &cases[i]

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runCase(t, tc)
		})
	}
}

// runCase sends the request of tc to a new handler and checks the answer.
func runCase(t *testing.T, tc *httpCase) {
	t.Helper()

	env := newEnv(t, tc.actions)

	body := tc.body
	if tc.validDraft {
		body = draftJSON
	}

	r := httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer token-a")

	if tc.ifMatch != "" {
		r.Header.Set("If-Match", tc.ifMatch)
	}

	w := httptest.NewRecorder()
	env.handler.ServeHTTP(w, r)

	checkStatus(t, tc, w)
	checkCode(t, tc, w)
	checkETag(t, tc, w)
	checkAudit(t, tc, env)
	checkNoStore(t, tc, env)
	checkList(t, tc, w)
}

// checkStatus compares the status of the answer.
func checkStatus(t *testing.T, tc *httpCase, w *httptest.ResponseRecorder) {
	t.Helper()

	if w.Code != tc.status {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, tc.status, w.Body.String())
	}
}

// checkCode compares the error code of a refused answer.
func checkCode(t *testing.T, tc *httpCase, w *httptest.ResponseRecorder) {
	t.Helper()

	if tc.code == "" {
		return
	}

	var result struct {
		ErrorCode string `json:"errorCode"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode error response: %v", err)
	}

	if result.ErrorCode != tc.code {
		t.Errorf("errorCode = %q, want %q", result.ErrorCode, tc.code)
	}
}

// checkETag compares the ETag of a read or a write answer.
func checkETag(t *testing.T, tc *httpCase, w *httptest.ResponseRecorder) {
	t.Helper()

	if tc.etag != "" && w.Header().Get("ETag") != tc.etag {
		t.Errorf("ETag = %q, want %q", w.Header().Get("ETag"), tc.etag)
	}
}

// checkAudit requires an audit entry for each refused request that must be
// audited.
func checkAudit(t *testing.T, tc *httpCase, env *testEnv) {
	t.Helper()

	if !tc.audited {
		return
	}

	entries := env.audit.snapshot()
	if len(entries) == 0 {
		t.Fatal("denial has no audit entry")
	}

	// The explanation of a refusal names its effective scope: the namespace of
	// the path, because grants apply at namespace level today.
	const namespaceSegment = 6

	want := strings.Split(tc.path, "/")[namespaceSegment]
	if got := entries[0].Object; got != want {
		t.Errorf("audit object = %q, want the namespace %q", got, want)
	}
}

// checkNoStore requires that a refused request never reached the store.
func checkNoStore(t *testing.T, tc *httpCase, env *testEnv) {
	t.Helper()

	if tc.status >= http.StatusBadRequest && env.store.callCount() != 0 {
		t.Error("rejected request reached the store")
	}
}

// listAnswer is the list answer as the tests read it.
type listAnswer struct {
	Versions []listedVersion `json:"versions"`
}

// listedVersion is one version of a list answer.
type listedVersion struct {
	Namespace   string `json:"namespace"`
	Blueprint   string `json:"blueprint"`
	ExprVersion string `json:"exprVersion"`
	PublishedAt string `json:"publishedAt"`
}

// checkList checks the metadata of the list answer.
func checkList(t *testing.T, tc *httpCase, w *httptest.ResponseRecorder) {
	t.Helper()

	if tc.path != testBase || tc.status != http.StatusOK {
		return
	}

	var result listAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode list response: %v", err)
	}

	if len(result.Versions) != 1 {
		t.Fatalf("list length = %d, want 1", len(result.Versions))
	}

	got := result.Versions[0]
	if got.Namespace != "namespace-a" || got.Blueprint != "blueprint-a" ||
		got.ExprVersion != "v1.0.0" || got.PublishedAt != "2026-11-07T12:00:00Z" {
		t.Errorf("list metadata = %+v", got)
	}
}

// TestNamespaceAuthorizer checks that a grant on one namespace does not allow
// a blueprint action in another namespace.
func TestNamespaceAuthorizer(t *testing.T) {
	t.Parallel()

	a := parseNamespace(t, "namespace-a")
	b := parseNamespace(t, "namespace-b")
	authz := &authorizer{grants: map[namespace.Name][]identity.Action{
		a: {selfservice.ActionBlueprintPublish},
	}}
	adapter := selfserviceapi.NewNamespaceAuthorizer(authz)

	cases := []struct {
		name   string
		ns     namespace.Name
		action identity.Action
		denied bool
	}{
		{name: "granted namespace", ns: a, action: selfservice.ActionBlueprintPublish, denied: false},
		{name: "other namespace", ns: b, action: selfservice.ActionBlueprintPublish, denied: true},
		{name: "other action", ns: a, action: selfservice.ActionBlueprintRetire, denied: true},
	}

	for i := range cases {
		tc := &cases[i]

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			object := selfservice.Object{
				Unit:      identity.Object{},
				Kind:      selfservice.ObjectBlueprint,
				Namespace: tc.ns,
				Blueprint: "blueprint-a",
			}
			err := adapter.Authorize(t.Context(), testPrincipal(), tc.action, object)

			if tc.denied && !errors.Is(err, identity.ErrDenied) {
				t.Fatalf("authorization error = %v, want a denial", err)
			}

			if !tc.denied && err != nil {
				t.Fatalf("authorize: %v", err)
			}
		})
	}
}

// testEnv is one handler with its fakes.
type testEnv struct {
	handler *selfserviceapi.Handler
	store   *blueprintStore
	audit   *auditStore
}

// newEnv builds a handler whose caller holds actions on namespace-a, with one
// published version of blueprint-a.
func newEnv(t *testing.T, actions []identity.Action) *testEnv {
	t.Helper()

	ns := parseNamespace(t, "namespace-a")
	ref := selfservice.BlueprintRef{Namespace: ns, Name: "blueprint-a"}

	bp, err := selfservicesvc.ParseDraft([]byte(draftJSON))
	if err != nil {
		t.Fatalf("parse test draft: %v", err)
	}

	bp.ExprVersion = "v1.0.0"

	clock := testClock{}
	store := &blueprintStore{
		mu:     sync.Mutex{},
		calls:  0,
		drafts: map[selfservice.BlueprintRef]draft{ref: {blueprint: bp, version: 1}},
		versions: map[versionKey]selfservice.Version{
			{ref: ref, number: 1}: {
				PublishedAt:  clock.Now(),
				Blueprint:    bp,
				Namespace:    ns,
				PublishedBy:  "user-a",
				Number:       1,
				DraftVersion: 1,
				Retired:      false,
			},
		},
	}
	authz := &authorizer{grants: map[namespace.Name][]identity.Action{ns: slices.Clone(actions)}}
	auditLog := &auditStore{mu: sync.Mutex{}, entries: nil}

	svc, err := selfservicesvc.New(selfservicesvc.Config{
		Store:      store,
		Authorizer: selfserviceapi.NewNamespaceAuthorizer(authz),
		Evaluator:  evaluator{},
		Clock:      clock,
		Audit:      auditLog,
		Fences:     fences{},
	})
	if err != nil {
		t.Fatalf("new blueprint service: %v", err)
	}

	handler, err := selfserviceapi.NewHandler(selfserviceapi.Config{
		Blueprints:     svc,
		Authenticator:  authenticator{},
		Authorizer:     authz,
		Audit:          auditLog,
		Namespaces:     namespaces{},
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		Logger:         slog.New(slog.DiscardHandler),
		Now:            clock.Now,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	return &testEnv{handler: handler, store: store, audit: auditLog}
}

// parseNamespace returns the namespace named s.
func parseNamespace(t *testing.T, s string) namespace.Name {
	t.Helper()

	ns, err := namespace.ParseName(s)
	if err != nil {
		t.Fatalf("parse test namespace: %v", err)
	}

	return ns
}

// testPrincipal returns the caller of the handler tests.
func testPrincipal() identity.Principal {
	var p identity.Principal

	p.Subject = "user-a"
	p.TokenID = "tok-a"

	return p
}

// authenticator accepts one token, for the caller of the tests.
type authenticator struct{}

func (authenticator) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	if token != "token-a" {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return testPrincipal(), nil
}

// authorizer grants actions on namespace objects.
type authorizer struct {
	grants map[namespace.Name][]identity.Action
}

func (a *authorizer) Authorize(
	_ context.Context, _ identity.Principal, action identity.Action, obj identity.Object,
) error {
	for ns, actions := range a.grants {
		if identity.NamespaceObject(ns) == obj && slices.Contains(actions, action) {
			return nil
		}
	}

	return identity.ErrDenied
}

func (*authorizer) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	var scope identity.Scope

	return scope, nil
}

// draft is the stored draft of one blueprint.
type draft struct {
	blueprint selfservice.Blueprint
	version   int64
}

// versionKey names one published version.
type versionKey struct {
	ref    selfservice.BlueprintRef
	number int
}

// blueprintStore keeps the drafts and versions in memory and counts the calls.
type blueprintStore struct {
	mu       sync.Mutex
	drafts   map[selfservice.BlueprintRef]draft
	versions map[versionKey]selfservice.Version
	calls    int
}

func (s *blueprintStore) PutDraft(
	_ context.Context, b selfservice.Blueprint, version int64, fence namespace.Fence, _ []audit.Entry,
) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls++

	ref := selfservice.BlueprintRef{Namespace: fence.Namespace, Name: b.Name}

	current := s.drafts[ref]
	if version != current.version {
		return 0, selfservice.ErrConflict
	}

	next := current.version + 1
	s.drafts[ref] = draft{blueprint: b, version: next}

	return next, nil
}

func (s *blueprintStore) Draft(_ context.Context, ref selfservice.BlueprintRef) (selfservice.Blueprint, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls++

	d, ok := s.drafts[ref]
	if !ok {
		return selfservice.Blueprint{}, 0, selfservice.ErrNotFound
	}

	return d.blueprint, d.version, nil
}

func (s *blueprintStore) Publish(
	_ context.Context, _ selfservice.Publication, _ namespace.Fence, _ []audit.Entry,
) (selfservice.Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls++

	return selfservice.Version{}, selfservice.ErrAlreadyPublished
}

func (s *blueprintStore) Version(_ context.Context, ref selfservice.BlueprintRef, n int) (selfservice.Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls++

	v, ok := s.versions[versionKey{ref: ref, number: n}]
	if !ok {
		return selfservice.Version{}, selfservice.ErrNotFound
	}

	return v, nil
}

func (s *blueprintStore) ListBlueprints(_ context.Context, ns namespace.Name) ([]selfservice.Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls++

	result := make([]selfservice.Version, 0)

	for key := range s.versions {
		if s.versions[key].Namespace == ns {
			result = append(result, s.versions[key])
		}
	}

	if len(result) == 0 {
		return nil, selfservice.ErrNotFound
	}

	return result, nil
}

func (s *blueprintStore) Retire(
	_ context.Context, ref selfservice.BlueprintRef, n int, _ namespace.Fence, _ []audit.Entry,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls++

	key := versionKey{ref: ref, number: n}
	if v, ok := s.versions[key]; ok {
		v.Retired = true
		s.versions[key] = v
	}

	return nil
}

func (s *blueprintStore) SetExprVersion(
	context.Context, selfservice.ExprVersionChange, namespace.Fence, []audit.Entry,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls++

	return nil
}

// callCount returns how many store calls were made.
func (s *blueprintStore) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.calls
}

// evaluator compiles every expression and evaluates none.
type evaluator struct{}

func (evaluator) Version() string { return "v1.0.0" }

func (evaluator) Compile(string, selfservice.ResultKind) error { return nil }

func (evaluator) Bool(context.Context, string, *selfservice.Env) (bool, error) {
	return false, errExprUnused
}

func (evaluator) String(context.Context, string, *selfservice.Env) (string, error) {
	return "", errExprUnused
}

// namespaces answers every namespace as existing, so that a denial is written
// in the namespace of its request.
type namespaces struct{}

func (namespaces) Get(context.Context, namespace.Name) (namespace.Namespace, error) {
	var ns namespace.Namespace

	return ns, nil
}

// fences returns the fence of the namespace.
type fences struct{}

func (fences) Fence(_ context.Context, ns namespace.Name) (namespace.Fence, error) {
	return namespace.Fence{Namespace: ns, FailoverVersion: 0}, nil
}

// testClock returns one fixed time.
type testClock struct{}

func (testClock) Now() time.Time {
	return time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
}

// auditStore keeps the appended entries.
type auditStore struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (s *auditStore) Append(_ context.Context, _ namespace.Fence, e audit.Entry) (audit.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.entries = append(s.entries, e)

	return e, nil
}

func (*auditStore) List(context.Context, namespace.Name, audit.Page) (audit.Listing, error) {
	var out audit.Listing

	return out, nil
}

func (*auditStore) Check(context.Context, namespace.Name) (audit.CheckResult, error) {
	var out audit.CheckResult

	return out, nil
}

// snapshot returns a copy of the appended entries.
func (s *auditStore) snapshot() []audit.Entry {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.entries)
}
