package inventory_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	apiinventory "opendustpan.org/opendustpan/internal/api/inventory"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// lockUnitRef is the unit that the lock tests lock.
const lockUnitRef = "pulumi:acme/web/prod"

// lockCollectionPath is the lock route of the collection that the lock tests lock.
const lockCollectionPath = "/api/dustpan/v1/namespaces/acme/collections/payments/lock"

// lockNamespacePath is the lock route of the namespace that the lock tests lock.
const lockNamespacePath = "/api/dustpan/v1/namespaces/acme/lock"

// memLocks is an in-memory manuallock.Store for the route tests. It knows one
// unit and one collection, and keeps every audit entry that a write appends.
type memLocks struct {
	mu      sync.Mutex
	locks   map[string]manuallock.Lock
	entries []audit.Entry
	// removeErr, when set, is the error of every removal. Tests set it to reach
	// the answers of store errors that a fresh store does not give.
	removeErr error
}

func newMemLocks() *memLocks {
	return &memLocks{mu: sync.Mutex{}, locks: map[string]manuallock.Lock{}, entries: nil, removeErr: nil}
}

func (m *memLocks) PutLock(_ context.Context, l manuallock.Lock, _ namespace.Fence, e audit.Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.known(l.Target) {
		return manuallock.ErrTargetNotFound
	}

	if _, held := m.locks[l.Target.String()]; held {
		return manuallock.ErrAlreadyLocked
	}

	m.locks[l.Target.String()] = l
	m.entries = append(m.entries, e)

	return nil
}

func (m *memLocks) RemoveLock(
	_ context.Context, t manuallock.Target, holder string, _ namespace.Fence, e audit.Entry,
) (manuallock.Lock, error) {
	if holder == "" {
		return manuallock.Lock{}, manuallock.ErrNotHolder
	}

	return m.remove(t, holder, e)
}

func (m *memLocks) ForceRemoveLock(
	_ context.Context, t manuallock.Target, _ namespace.Fence, e audit.Entry,
) (manuallock.Lock, error) {
	return m.remove(t, "", e)
}

func (m *memLocks) GetLock(_ context.Context, t manuallock.Target) (manuallock.Lock, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.known(t) {
		return manuallock.Lock{}, manuallock.ErrTargetNotFound
	}

	held, ok := m.locks[t.String()]
	if !ok {
		return manuallock.Lock{}, manuallock.ErrNotLocked
	}

	return held, nil
}

func (m *memLocks) CoveringLock(_ context.Context, _ collection.UnitRef) (manuallock.Lock, error) {
	return manuallock.Lock{}, manuallock.ErrNotLocked
}

func (m *memLocks) CoveringLocks(_ context.Context, _ collection.UnitRef) ([]manuallock.Lock, error) {
	return nil, manuallock.ErrNotLocked
}

func (m *memLocks) remove(t manuallock.Target, holder string, e audit.Entry) (manuallock.Lock, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.removeErr != nil {
		return manuallock.Lock{}, m.removeErr
	}

	if !m.known(t) {
		return manuallock.Lock{}, manuallock.ErrTargetNotFound
	}

	held, ok := m.locks[t.String()]
	if !ok {
		return manuallock.Lock{}, manuallock.ErrNotLocked
	}

	if holder != "" && held.Holder != holder {
		return manuallock.Lock{}, manuallock.ErrNotHolder
	}

	delete(m.locks, t.String())
	m.entries = append(m.entries, e)

	return held, nil
}

// known reports whether the route tests have the object.
func (m *memLocks) known(t manuallock.Target) bool {
	return t.String() == lockUnitRef || t.String() == "collection acme/payments" || t.String() == "namespace acme"
}

// subjectAuthenticator maps the token "<subject>-token" to the subject.
type subjectAuthenticator struct{}

func (subjectAuthenticator) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	subject, ok := strings.CutSuffix(token, "-token")
	if !ok {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject: subject, DisplayName: subject, Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		Kind: "", Credential: "", JobID: "",
	}, nil
}

// memAudit is an audit.Store that keeps the appended entries of the namespace
// acme in memory. It does not chain them: the tests read the fields.
type memAudit struct {
	mu      sync.Mutex
	entries []audit.Entry
}

var _ audit.Store = (*memAudit)(nil)

func (m *memAudit) Append(_ context.Context, fence namespace.Fence, e audit.Entry) (audit.Entry, error) {
	if err := e.Validate(); err != nil {
		return audit.Entry{}, err
	}

	if e.Namespace != fence.Namespace {
		return audit.Entry{}, audit.ErrInvalidEntry
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.entries = append(m.entries, e)

	return e, nil
}

func (m *memAudit) List(_ context.Context, _ namespace.Name, _ audit.Page) (audit.Listing, error) {
	return audit.Listing{Entries: nil, Total: 0}, nil
}

func (m *memAudit) Check(_ context.Context, _ namespace.Name) (audit.CheckResult, error) {
	return audit.CheckResult{Checked: 0, BadSequence: 0, Unchained: 0, Valid: true}, nil
}

// denied returns the entries with the outcome denied.
func (m *memAudit) denied() []audit.Entry {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []audit.Entry

	for i := range m.entries {
		if m.entries[i].Outcome == audit.OutcomeDenied {
			out = append(out, m.entries[i])
		}
	}

	return out
}

// lockFixture is a handler with the lock routes over an in-memory store.
type lockFixture struct {
	handler *apiinventory.Handler
	store   *memLocks
	audit   *memAudit
}

func newLockFixture(t *testing.T, authz identity.Authorizer) lockFixture {
	t.Helper()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	seedNamespace(t, nsStore)

	nsSvc, err := namespace.NewService(namespace.Config{Store: nsStore, Now: nil, Region: "test-region"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	store := newMemLocks()
	auditStore := &memAudit{mu: sync.Mutex{}, entries: nil}

	locks, err := manuallock.NewService(
		manuallock.Config{Store: store, Namespaces: nsStore, Authorizer: authz, Now: nil, Phases: nil},
	)
	if err != nil {
		t.Fatalf("manual lock service: %v", err)
	}

	handler, err := apiinventory.NewHandler(apiinventory.Config{
		OutputSharing:  nil,
		Views:          nil,
		Namespaces:     nsSvc,
		Units:          &memReader{entries: map[namespace.Name][]coreinventory.Entry{}, err: nil},
		Deleted:        &memDeleted{entries: nil},
		History:        nil,
		Lifecycles:     testLifecycles(),
		ReadScope:      fixedScope{err: nil, ids: nil, all: true},
		Collections:    nil,
		Locks:          locks,
		Moves:          nil,
		Authenticator:  subjectAuthenticator{},
		Authorizer:     authz,
		Audit:          orTestAudit(auditStore),
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		PublicURL:      "",
		Logger:         slog.New(slog.DiscardHandler),
		Now:            nil,
		StateGuard:     nil,
	})
	if err != nil {
		t.Fatalf("inventory handler: %v", err)
	}

	return lockFixture{handler: handler, store: store, audit: auditStore}
}

// do sends one request as subject and returns the answer.
func (f lockFixture) do(t *testing.T, subject, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	req.Header.Set("Authorization", "token "+subject+"-token")

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	return rec
}

// lockObject is one object that the lock routes act on.
type lockObject struct {
	name string
	path string
}

func lockObjects() []lockObject {
	return []lockObject{
		{name: "unit", path: "/api/inventory/unit-lock?unit=" + url.QueryEscape(lockUnitRef)},
		{name: "collection", path: lockCollectionPath},
		{name: "namespace", path: lockNamespacePath},
	}
}

// forcePath returns the force-unlock route that belongs to the lock route path.
func forcePath(path string) string {
	if before, after, found := strings.Cut(path, "?"); found {
		return before + "/force-unlock?" + after
	}

	return path + "/force-unlock"
}

// TestLockRoutes drives the lock life cycle over the API for a unit and for a
// collection: the body checks, the lock, a second lock, the read, the removal
// by another principal, the removal by the holder, and the force-unlock.
func TestLockRoutes(t *testing.T) {
	t.Parallel()

	const reason = "freeze for the audit"

	long := `{"reason":"` + strings.Repeat("a", 501) + `"}`
	good := `{"reason":"` + reason + `"}`

	for _, obj := range lockObjects() {
		t.Run(obj.name, func(t *testing.T) {
			t.Parallel()

			f := newLockFixture(t, selectiveAuthorizer{deny: nil})

			steps := []struct {
				name    string
				subject string
				method  string
				path    string
				body    string
				want    int
				wantIn  string
			}{
				{"no body", "alice", http.MethodPut, obj.path, "", http.StatusBadRequest, "provide"},
				{"no reason field", "alice", http.MethodPut, obj.path, `{}`, http.StatusBadRequest, "provide"},
				{
					"unknown field",
					"alice",
					http.MethodPut,
					obj.path,
					`{"reason":"x","force":true}`,
					http.StatusBadRequest,
					"provide",
				},
				{
					"empty reason",
					"alice",
					http.MethodPut,
					obj.path,
					`{"reason":"  "}`,
					http.StatusBadRequest,
					"needs a reason",
				},
				{"reason of 501 characters", "alice", http.MethodPut, obj.path, long, http.StatusBadRequest, "500"},
				{
					"reason with a control character",
					"alice",
					http.MethodPut,
					obj.path,
					`{"reason":"a\u0000b"}`,
					http.StatusBadRequest,
					"control",
				},
				{"read before a lock", "bob", http.MethodGet, obj.path, "", http.StatusNotFound, "no manual lock"},
				{"lock", "alice", http.MethodPut, obj.path, good, http.StatusOK, `"holder":"alice"`},
				{"lock again", "bob", http.MethodPut, obj.path, `{"reason":"mine"}`, http.StatusConflict, "alice"},
				{"read", "bob", http.MethodGet, obj.path, "", http.StatusOK, `"reason":"` + reason + `"`},
				{
					"removal by another principal",
					"bob",
					http.MethodDelete,
					obj.path,
					"",
					http.StatusConflict,
					"force-unlock",
				},
				{
					"read after the refused removal",
					"bob",
					http.MethodGet,
					obj.path,
					"",
					http.StatusOK,
					`"holder":"alice"`,
				},
				{"removal by the holder", "alice", http.MethodDelete, obj.path, "", http.StatusNoContent, ""},
				{"read after the removal", "bob", http.MethodGet, obj.path, "", http.StatusNotFound, "no manual lock"},
				{
					"removal of a lock that does not exist",
					"alice",
					http.MethodDelete,
					obj.path,
					"",
					http.StatusNotFound,
					"no manual lock",
				},
				{"lock for the force-unlock", "alice", http.MethodPut, obj.path, good, http.StatusOK, ""},
				{
					"force-unlock by another principal",
					"bob",
					http.MethodPost,
					forcePath(obj.path),
					"",
					http.StatusNoContent,
					"",
				},
				{
					"read after the force-unlock",
					"bob",
					http.MethodGet,
					obj.path,
					"",
					http.StatusNotFound,
					"no manual lock",
				},
				{
					"force-unlock of a lock that does not exist",
					"bob",
					http.MethodPost,
					forcePath(obj.path),
					"",
					http.StatusNotFound,
					"no manual lock",
				},
			}

			for _, step := range steps {
				rec := f.do(t, step.subject, step.method, step.path, step.body)
				if rec.Code != step.want {
					t.Fatalf("%s: status = %d, want %d: %s", step.name, rec.Code, step.want, rec.Body.String())
				}

				if !strings.Contains(rec.Body.String(), step.wantIn) {
					t.Fatalf("%s: body = %s, want it to hold %q", step.name, rec.Body.String(), step.wantIn)
				}
			}
		})
	}
}

// TestLockRoutesWriteAuditEntries proves a lock, a removal by the holder and a
// force-unlock each append one audit entry that names the principal, the
// action and the object, and that the lock entry holds the reason.
func TestLockRoutesWriteAuditEntries(t *testing.T) {
	t.Parallel()

	f := newLockFixture(t, selectiveAuthorizer{deny: nil})
	path := lockObjects()[0].path

	for _, step := range []struct {
		subject, method, path, body string
	}{
		{"alice", http.MethodPut, path, `{"reason":"first"}`},
		{"alice", http.MethodDelete, path, ""},
		{"alice", http.MethodPut, path, `{"reason":"second"}`},
		{"bob", http.MethodPost, forcePath(path), ""},
	} {
		if rec := f.do(t, step.subject, step.method, step.path, step.body); rec.Code >= http.StatusBadRequest {
			t.Fatalf("%s %s: status = %d: %s", step.method, step.path, rec.Code, rec.Body.String())
		}
	}

	want := []struct{ subject, action, version, reason string }{
		{"alice", "unit:lock", "", "first"},
		{"alice", "unit:lock", "unlock", ""},
		{"alice", "unit:lock", "", "second"},
		{"bob", "unit:manage", "force-unlock", ""},
	}

	if len(f.store.entries) != len(want) {
		t.Fatalf("audit entries = %d, want %d", len(f.store.entries), len(want))
	}

	for i, w := range want {
		e := f.store.entries[i]
		if e.Subject != w.subject || e.Action != w.action || e.Version != w.version || e.Reason != w.reason ||
			e.Object != lockUnitRef {
			t.Errorf("entry %d = %s %s %q %q on %q, want %+v on %q",
				i, e.Subject, e.Action, e.Version, e.Reason, e.Object, w, lockUnitRef)
		}
	}
}

// deniedCase is one lock route and the action that the test denies on it.
type deniedCase struct {
	name   string
	deny   identity.Action
	method string
	force  bool
	seed   bool
}

// TestLockRoutesNeedTheirActions proves each route checks its own action: a
// denied check answers 403 and changes no lock.
func TestLockRoutesNeedTheirActions(t *testing.T) {
	t.Parallel()

	cases := []deniedCase{
		{
			name:   "lock needs unit:lock",
			deny:   identity.ActionUnitLock,
			method: http.MethodPut,
			force:  false,
			seed:   false,
		},
		{
			name: "removal by the holder needs unit:lock", deny: identity.ActionUnitLock,
			method: http.MethodDelete, force: false, seed: true,
		},
		{
			name: "force-unlock needs unit:manage", deny: identity.ActionUnitManage,
			method: http.MethodPost, force: true, seed: true,
		},
	}

	for _, obj := range lockObjects() {
		for _, tc := range cases {
			t.Run(obj.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				checkDenied(t, obj, tc)
			})
		}
	}
}

// checkDenied sends the request of tc with the action of tc denied, and checks
// that the answer is 403 and that the lock stays as it was.
func checkDenied(t *testing.T, obj lockObject, tc deniedCase) {
	t.Helper()

	f := newLockFixture(t, selectiveAuthorizer{deny: map[identity.Action]bool{tc.deny: true}})
	target := mustTarget(t, obj)

	if tc.seed {
		f.store.locks[target.String()] = manuallock.Lock{
			Since: time.Now(), Target: target, Holder: "alice", Reason: "seed",
		}
	}

	path := obj.path
	if tc.force {
		path = forcePath(path)
	}

	rec := f.do(t, "alice", tc.method, path, `{"reason":"x"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}

	if _, held := f.store.locks[target.String()]; held != tc.seed {
		t.Errorf("lock held = %v after a denied request, want %v", held, tc.seed)
	}

	got := f.audit.denied()
	if len(got) != 1 {
		t.Fatalf("denied entries = %d, want 1: %+v", len(got), got)
	}

	if got[0].Action != string(tc.deny) || got[0].Subject != "alice" {
		t.Errorf("entry action %q subject %q, want %q and alice", got[0].Action, got[0].Subject, tc.deny)
	}
}

// mustTarget returns the lock target that the route of obj acts on.
func mustTarget(t *testing.T, obj lockObject) manuallock.Target {
	t.Helper()

	if obj.name == "namespace" {
		return manuallock.NamespaceTarget("acme")
	}

	if obj.name == "collection" {
		name, err := collection.ParseName("payments")
		if err != nil {
			t.Fatalf("parse collection name: %v", err)
		}

		return manuallock.CollectionTarget(collection.Ref{Namespace: "acme", Name: name})
	}

	return manuallock.UnitTarget(collection.UnitRef{Namespace: "acme", Tool: "pulumi", Name: "web/prod"})
}

// TestLockRoutesOfAnUnknownObject proves a lock on a unit or a collection that
// does not exist answers 404 and holds no lock.
func TestLockRoutesOfAnUnknownObject(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		path string
	}{
		{name: "unit", path: "/api/inventory/unit-lock?unit=" + url.QueryEscape("pulumi:acme/web/absent")},
		{name: "collection", path: "/api/dustpan/v1/namespaces/acme/collections/absent/lock"},
		{name: "namespace", path: "/api/dustpan/v1/namespaces/absent/lock"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newLockFixture(t, selectiveAuthorizer{deny: nil})

			rec := f.do(t, "alice", http.MethodPut, tc.path, `{"reason":"x"}`)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusNotFound, rec.Body.String())
			}

			if len(f.store.locks) != 0 {
				t.Errorf("locks after the refused request = %d, want 0", len(f.store.locks))
			}
		})
	}
}

// TestUnitQueryNeedsAUnit proves the unit lock routes refuse a request without
// a unit reference, and a reference that names no unit.
func TestUnitQueryNeedsAUnit(t *testing.T) {
	t.Parallel()

	f := newLockFixture(t, selectiveAuthorizer{deny: nil})

	for _, target := range []string{
		"/api/inventory/unit-lock",
		"/api/inventory/unit-lock?unit=" + url.QueryEscape("acme"),
	} {
		rec := f.do(t, "alice", http.MethodGet, target, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s: status = %d, want %d: %s", target, rec.Code, http.StatusBadRequest, rec.Body.String())
		}
	}
}

// TestLockRoutesDeclareActions walks the routes of a handler with the lock
// service and proves the lock rows pass the same policy check as every other
// row: each served pattern has a row, each row declares a scope, an action and
// a resolver, and each lock route declares the action that the life cycle of a
// manual lock needs.
func TestLockRoutesDeclareActions(t *testing.T) {
	t.Parallel()

	f := newLockFixture(t, selectiveAuthorizer{deny: nil})

	byPattern := map[string]apiinventory.Route{}
	for _, route := range f.handler.Routes() {
		pattern := route.Method + " " + route.Path
		if _, dup := byPattern[pattern]; dup {
			t.Fatalf("duplicate route %q", pattern)
		}

		byPattern[pattern] = route

		checkRouteScope(t, route, map[string]bool{})
	}

	for _, pattern := range f.handler.RegisteredPatterns() {
		if _, ok := byPattern[pattern]; !ok {
			t.Fatalf("served route %q has no declared action", pattern)
		}
	}

	want := map[string]identity.Action{
		"PUT /api/inventory/unit-lock":                                                           identity.ActionUnitLock,
		"GET /api/inventory/unit-lock":                                                           identity.ActionUnitRead,
		"DELETE /api/inventory/unit-lock":                                                        identity.ActionUnitLock,
		"POST /api/inventory/unit-lock/force-unlock":                                             identity.ActionUnitManage,
		"PUT /api/dustpan/v1/namespaces/{namespace}/collections/{collection}/lock":               identity.ActionUnitLock,
		"GET /api/dustpan/v1/namespaces/{namespace}/collections/{collection}/lock":               identity.ActionCollectionRead,
		"DELETE /api/dustpan/v1/namespaces/{namespace}/collections/{collection}/lock":            identity.ActionUnitLock,
		"POST /api/dustpan/v1/namespaces/{namespace}/collections/{collection}/lock/force-unlock": identity.ActionUnitManage,
		"PUT /api/dustpan/v1/namespaces/{namespace}/lock":                                        identity.ActionUnitLock,
		"GET /api/dustpan/v1/namespaces/{namespace}/lock":                                        identity.ActionNamespaceRead,
		"DELETE /api/dustpan/v1/namespaces/{namespace}/lock":                                     identity.ActionUnitLock,
		"POST /api/dustpan/v1/namespaces/{namespace}/lock/force-unlock":                          identity.ActionUnitManage,
	}

	for pattern, action := range want {
		route, ok := byPattern[pattern]
		if !ok {
			t.Errorf("route %q is not served", pattern)

			continue
		}

		if len(route.Actions) != 1 || route.Actions[0] != action {
			t.Errorf("route %q actions = %v, want [%s]", pattern, route.Actions, action)
		}

		probeRoute(t, f.handler, route)
	}
}

// TestNoLockRoutesWithoutTheService proves a handler built without the lock
// service serves no lock route, so a store that holds no manual locks keeps the
// rest of the API unchanged.
func TestNoLockRoutesWithoutTheService(t *testing.T) {
	t.Parallel()

	for _, route := range newTestHandler(t).Routes() {
		if strings.Contains(route.Path, "unit-lock") || strings.HasSuffix(route.Path, "/lock") ||
			strings.Contains(route.Path, "/lock/") {
			t.Errorf("route %q is served without the lock service", route.Method+" "+route.Path)
		}
	}
}

// manualLockDoc is the manual lock flag of a unit in the unit list.
type manualLockDoc struct {
	Kind   string `json:"kind"`
	Object string `json:"object"`
	Holder string `json:"holder"`
	Reason string `json:"reason"`
	Since  string `json:"since"`
}

// unitDoc is one unit of the unit list, as far as the flag test reads it.
type unitDoc struct {
	Name       string         `json:"name"`
	Lock       any            `json:"lock"`
	ManualLock *manualLockDoc `json:"manualLock"`
}

// unitsDoc is the unit list, as far as the flag test reads it.
type unitsDoc struct {
	Units []unitDoc `json:"units"`
}

// TestListUnitsManualLockField proves a unit under a manual lock answers the
// flag with the holder, the reason, the object and the time, a unit under no
// manual lock omits the field, and the flag leaves the tool lock field alone.
func TestListUnitsManualLockField(t *testing.T) {
	t.Parallel()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	seedNamespace(t, nsStore)

	ns := namespace.Name("acme")
	since := changeTime()
	entry := func(name, id string, lock *coreinventory.ManualLock) coreinventory.Entry {
		return coreinventory.Entry{
			ManualLock: lock, Collection: "", LastChanged: since, Namespace: ns, Tool: "pulumi", Name: name,
			UnitID: id, DisplayName: name, Status: "", ResourceCount: nil, Lock: nil, Labels: nil,
		}
	}

	reader := &memReader{
		entries: map[namespace.Name][]coreinventory.Entry{
			ns: {
				entry("web/prod", "stk-1", &coreinventory.ManualLock{
					Since: since, Kind: "collection", Object: "acme/payments", Holder: "alice", Reason: "freeze",
				}),
				entry("web/test", "stk-2", nil),
			},
		},
		err: nil,
	}

	handler := newBehaviorHandler(t, nsStore, reader, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	rec := authorizedGet(t, handler, "/api/dustpan/v1/namespaces/acme/units", "test-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("list answers %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	var doc unitsDoc

	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode list: %v", err)
	}

	if len(doc.Units) != 2 {
		t.Fatalf("units = %d, want 2", len(doc.Units))
	}

	locked := doc.Units[0]
	if locked.ManualLock == nil {
		t.Fatalf("unit %s has no manual lock flag", locked.Name)
	}

	want := struct{ kind, object, holder, reason, since string }{
		"collection", "acme/payments", "alice", "freeze", since.UTC().Format(time.RFC3339Nano),
	}
	got := struct{ kind, object, holder, reason, since string }{
		locked.ManualLock.Kind, locked.ManualLock.Object, locked.ManualLock.Holder, locked.ManualLock.Reason,
		locked.ManualLock.Since,
	}

	if got != want {
		t.Errorf("flag = %+v, want %+v", got, want)
	}

	if locked.Lock != nil {
		t.Errorf("tool lock = %v, want none: the flags are separate", locked.Lock)
	}

	if doc.Units[1].ManualLock != nil {
		t.Errorf("unit %s flag = %+v, want none", doc.Units[1].Name, doc.Units[1].ManualLock)
	}

	if strings.Count(rec.Body.String(), `"manualLock"`) != 1 {
		t.Errorf("body = %s, want the manualLock field once, omitted on the unit with no lock", rec.Body.String())
	}
}

// TestLockReasonOfEscapedAstralCharacters proves a reason of 500 characters
// that the client writes as JSON surrogate-pair escapes (12 bytes for each
// character) fits in the body limit and is locked with all 500 characters. A
// reason of 501 such characters is refused for its length, not for its size.
func TestLockReasonOfEscapedAstralCharacters(t *testing.T) {
	t.Parallel()

	const (
		escaped = "\\ud83d\\ude00" // the JSON escape of U+1F600: 12 bytes for one character
		maxLen  = 500
	)

	body := func(n int) string {
		return `{"reason":"` + strings.Repeat(escaped, n) + `"}`
	}

	for _, obj := range lockObjects() {
		t.Run(obj.name, func(t *testing.T) {
			t.Parallel()

			f := newLockFixture(t, selectiveAuthorizer{deny: nil})

			rec := f.do(t, "alice", http.MethodPut, obj.path, body(maxLen+1))
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "500") {
				t.Fatalf("501 characters: status = %d, body = %s, want 400 that names the 500 limit",
					rec.Code, rec.Body.String())
			}

			rec = f.do(t, "alice", http.MethodPut, obj.path, body(maxLen))
			if rec.Code != http.StatusOK {
				t.Fatalf("500 characters: status = %d, body = %s, want %d", rec.Code, rec.Body.String(), http.StatusOK)
			}

			var doc struct {
				Reason string `json:"reason"`
			}

			if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
				t.Fatalf("decode %q: %v", rec.Body.String(), err)
			}

			if got := utf8.RuneCountInString(doc.Reason); got != maxLen {
				t.Errorf("reason has %d characters, want %d", got, maxLen)
			}
		})
	}
}

// TestLockRoutesAnswerLockChangedWith409 proves that a store which reports a
// lock that changed during the removal gives 409 on the remove route and on the
// force-unlock route, and that the text tells the caller to read the lock again.
func TestLockRoutesAnswerLockChangedWith409(t *testing.T) {
	t.Parallel()

	for _, obj := range lockObjects() {
		for _, force := range []bool{false, true} {
			name := obj.name + "/remove"
			path, method := obj.path, http.MethodDelete

			if force {
				name, path, method = obj.name+"/force-unlock", forcePath(obj.path), http.MethodPost
			}

			t.Run(name, func(t *testing.T) {
				t.Parallel()

				f := newLockFixture(t, selectiveAuthorizer{deny: nil})
				target := mustTarget(t, obj)
				f.store.locks[target.String()] = manuallock.Lock{
					Since: time.Now(), Target: target, Holder: "alice", Reason: "seed",
				}
				f.store.removeErr = manuallock.ErrLockChanged

				rec := f.do(t, "alice", method, path, "")
				if rec.Code != http.StatusConflict {
					t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusConflict, rec.Body.String())
				}

				if !strings.Contains(rec.Body.String(), "read the lock again") {
					t.Errorf("body = %s, want the text of ErrLockChanged", rec.Body.String())
				}
			})
		}
	}
}

// TestLockRefusalsOfTheServiceWriteDeniedEntries proves that a removal by a
// principal that does not hold the lock, and a removal that finds the lock
// changed, answer 409 and write one denied entry with their own reason and the
// action of the route.
func TestLockRefusalsOfTheServiceWriteDeniedEntries(t *testing.T) {
	t.Parallel()

	cases := []serviceRefusalCase{
		{
			name: "not holder", method: http.MethodDelete, force: false, action: identity.ActionUnitLock,
			reason: "lock held by another principal", removeErr: nil,
		},
		{
			name: "lock changed on removal", method: http.MethodDelete, force: false,
			action: identity.ActionUnitLock, reason: "lock changed during the removal",
			removeErr: manuallock.ErrLockChanged,
		},
		{
			name: "lock changed on force-unlock", method: http.MethodPost, force: true,
			action: identity.ActionUnitManage, reason: "lock changed during the removal",
			removeErr: manuallock.ErrLockChanged,
		},
	}

	for _, obj := range lockObjects() {
		for _, tc := range cases {
			t.Run(obj.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				checkServiceRefusal(t, obj, tc)
			})
		}
	}
}

// serviceRefusalCase is one refusal that the lock service decides.
type serviceRefusalCase struct {
	name      string
	method    string
	force     bool
	action    identity.Action
	reason    string
	removeErr error
}

// checkServiceRefusal sends the removal of tc on a lock that bob holds, as
// alice, and checks the 409 and the denied entry.
func checkServiceRefusal(t *testing.T, obj lockObject, tc serviceRefusalCase) {
	t.Helper()

	f := newLockFixture(t, selectiveAuthorizer{deny: nil})
	target := mustTarget(t, obj)
	f.store.locks[target.String()] = manuallock.Lock{
		Since: time.Now(), Target: target, Holder: "bob", Reason: "seed",
	}
	f.store.removeErr = tc.removeErr

	path := obj.path
	if tc.force {
		path = forcePath(path)
	}

	rec := f.do(t, "alice", tc.method, path, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusConflict, rec.Body.String())
	}

	got := f.audit.denied()
	if len(got) != 1 {
		t.Fatalf("denied entries = %d, want 1: %+v", len(got), got)
	}

	if got[0].Action != string(tc.action) || got[0].Subject != "alice" || !strings.HasPrefix(got[0].Reason, tc.reason) {
		t.Errorf("entry action %q subject %q reason %q, want %q and alice and %q",
			got[0].Action, got[0].Subject, got[0].Reason, tc.action, tc.reason)
	}
}

// TestLockStateAnswersWriteNoDeniedEntry proves that a lock that is already held
// and an object with no lock to remove are state and not refusals: they answer
// as before and write no denied entry.
func TestLockStateAnswersWriteNoDeniedEntry(t *testing.T) {
	t.Parallel()

	for _, obj := range lockObjects() {
		t.Run(obj.name, func(t *testing.T) {
			t.Parallel()

			f := newLockFixture(t, selectiveAuthorizer{deny: nil})
			target := mustTarget(t, obj)

			if rec := f.do(t, "alice", http.MethodDelete, obj.path, ""); rec.Code != http.StatusNotFound {
				t.Fatalf("remove of no lock: status = %d, want %d", rec.Code, http.StatusNotFound)
			}

			f.store.locks[target.String()] = manuallock.Lock{
				Since: time.Now(), Target: target, Holder: "bob", Reason: "seed",
			}

			if rec := f.do(t, "alice", http.MethodPut, obj.path, `{"reason":"x"}`); rec.Code != http.StatusConflict {
				t.Fatalf("lock of a held object: status = %d, want %d", rec.Code, http.StatusConflict)
			}

			if got := f.audit.denied(); len(got) != 0 {
				t.Errorf("denied entries = %d, want 0: %+v", len(got), got)
			}
		})
	}
}
