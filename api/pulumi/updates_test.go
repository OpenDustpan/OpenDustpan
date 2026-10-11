package pulumi_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/api/pulumi"
	"opendustpan.org/opendustpan/identity"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/namespace"
	"opendustpan.org/opendustpan/stack"
)

// updatePatterns lists every update route as "METHOD path". The enforcement
// tests use it to tell update routes, which need live update state, apart
// from the older routes.
func updatePatterns() []string {
	return []string{
		"POST /api/stacks/{namespace}/{project}/{stack}/update",
		"POST /api/stacks/{namespace}/{project}/{stack}/preview",
		"POST /api/stacks/{namespace}/{project}/{stack}/refresh",
		"POST /api/stacks/{namespace}/{project}/{stack}/destroy",
		"POST /api/stacks/{namespace}/{project}/{stack}/import",
		"POST /api/stacks/{namespace}/{project}/{stack}/update/{update}",
		"POST /api/stacks/{namespace}/{project}/{stack}/update/{update}/renew_lease",
		"POST /api/stacks/{namespace}/{project}/{stack}/update/{update}/events/batch",
		"PATCH /api/stacks/{namespace}/{project}/{stack}/update/{update}/checkpoint",
		"POST /api/stacks/{namespace}/{project}/{stack}/update/{update}/complete",
		"POST /api/stacks/{namespace}/{project}/{stack}/update/{update}/cancel",
		"GET /api/stacks/{namespace}/{project}/{stack}/update/{update}",
		"GET /api/stacks/{namespace}/{project}/{stack}/updates",
		"GET /api/stacks/{namespace}/{project}/{stack}/export",
		"GET /api/stacks/{namespace}/{project}/{stack}/export/{version}",
	}
}

// memUpdateStore implements the update, checkpoint and event ports in
// memory, following their documented contracts far enough for the HTTP
// tests: compare-and-swap on revision, one mutating update per stack,
// versions, and lease checks on event writes.
type memUpdateStore struct {
	updates      map[stack.UpdateID]stack.Update
	stackVersion map[stack.Ref]int64
	checkpoints  map[stack.UpdateID]stack.Checkpoint
	events       map[stack.UpdateID][]stack.Event
	mu           sync.Mutex
}

func newMemUpdateStore() *memUpdateStore {
	return &memUpdateStore{
		updates:      map[stack.UpdateID]stack.Update{},
		stackVersion: map[stack.Ref]int64{},
		checkpoints:  map[stack.UpdateID]stack.Checkpoint{},
		events:       map[stack.UpdateID][]stack.Event{},
		mu:           sync.Mutex{},
	}
}

func (m *memUpdateStore) CreateUpdate(_ context.Context, u stack.Update, _ namespace.Fence) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.updates[u.ID] = u

	return nil
}

func (m *memUpdateStore) GetUpdate(_ context.Context, ref stack.Ref, id stack.UpdateID) (stack.Update, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	u, ok := m.updates[id]
	if !ok || u.Ref != ref {
		return stack.Update{}, stack.ErrUpdateNotFound
	}

	return u, nil
}

func (m *memUpdateStore) StartUpdate(_ context.Context, u stack.Update, _ namespace.Fence) (stack.Update, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.updates[u.ID].Revision != u.Revision-1 {
		return stack.Update{}, stack.ErrConcurrentModification
	}

	var highest int64

	for id := range m.updates {
		if !m.updates[id].Mutates() || m.updates[id].Ref != u.Ref {
			continue
		}

		if m.updates[id].Status == stack.StatusRunning && u.Mutates() {
			return stack.Update{}, stack.ErrUpdateConflict
		}

		highest = max(highest, m.updates[id].Version)
	}

	u.Version = m.stackVersion[u.Ref]
	if u.Mutates() {
		u.Version = highest + 1
	}

	m.updates[u.ID] = u

	return u, nil
}

func (m *memUpdateStore) SaveUpdate(_ context.Context, u stack.Update, _ namespace.Fence) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.updates[u.ID].Revision != u.Revision-1 {
		return stack.ErrConcurrentModification
	}

	if u.Mutates() && u.Status.Terminal() && u.Version > 0 {
		m.stackVersion[u.Ref] = u.Version
	}

	m.updates[u.ID] = u

	return nil
}

func (m *memUpdateStore) ActiveUpdate(_ context.Context, ref stack.Ref) (stack.Update, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id := range m.updates {
		if m.updates[id].Ref == ref && m.updates[id].Mutates() && m.updates[id].Status == stack.StatusRunning {
			return m.updates[id], nil
		}
	}

	return stack.Update{}, stack.ErrUpdateNotFound
}

func (m *memUpdateStore) ListUpdates(_ context.Context, ref stack.Ref, _ stack.Page) ([]stack.Update, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var list []stack.Update

	for id := range m.updates {
		if m.updates[id].Ref == ref && m.updates[id].Mutates() && m.updates[id].Status != stack.StatusNotStarted {
			list = append(list, m.updates[id])
		}
	}

	slices.SortFunc(list, func(a, b stack.Update) int { return int(b.Version - a.Version) })

	return list, nil
}

func (m *memUpdateStore) PutCheckpoint(_ context.Context, cp stack.Checkpoint, _ namespace.Fence) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.checkpoints[cp.Update] = cp

	return nil
}

func (m *memUpdateStore) LatestCheckpoint(_ context.Context, ref stack.Ref) (stack.Checkpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var (
		best    stack.Checkpoint
		found   bool
		highest int64 = -1
	)

	for id := range m.checkpoints {
		if m.checkpoints[id].Ref != ref {
			continue
		}

		if v := m.updates[id].Version; v > highest {
			highest, best, found = v, m.checkpoints[id], true
		}
	}

	if !found {
		return stack.Checkpoint{}, stack.ErrNoState
	}

	return best, nil
}

func (m *memUpdateStore) CheckpointByVersion(
	_ context.Context, ref stack.Ref, version int64,
) (stack.Checkpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id := range m.checkpoints {
		if m.checkpoints[id].Ref == ref && m.updates[id].Version == version {
			return m.checkpoints[id], nil
		}
	}

	return stack.Checkpoint{}, stack.ErrNoState
}

func (m *memUpdateStore) AppendEvents(
	_ context.Context,
	batch stack.EventBatch,
	_ namespace.Fence,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	u := m.updates[batch.Update]
	if u.Status != stack.StatusRunning || u.Lease.Digest != batch.Lease {
		return stack.ErrWrongUpdateState
	}

	for _, e := range batch.Events {
		if !slices.ContainsFunc(
			m.events[batch.Update],
			func(have stack.Event) bool { return have.Sequence == e.Sequence },
		) {
			m.events[batch.Update] = append(m.events[batch.Update], e)
		}
	}

	return nil
}

func (m *memUpdateStore) ListEvents(
	_ context.Context, _ stack.Ref, id stack.UpdateID, after int64, limit int,
) ([]stack.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.updates[id]; !ok {
		return nil, stack.ErrUpdateNotFound
	}

	var out []stack.Event

	for _, e := range m.events[id] {
		if e.Sequence > after && len(out) < limit {
			out = append(out, e)
		}
	}

	return out, nil
}

// updateFixture is a Handler backed by real stack and update services over
// in-memory stores, plus the services to mint update state with.
type updateFixture struct {
	handler *pulumi.Handler
	updates *stack.Updates
	ref     stack.Ref
}

// newUpdateFixture builds a Handler whose update routes work end to end.
// authz decides the authorization policy under test.
func newUpdateFixture(t *testing.T, authz identity.Authorizer) updateFixture {
	t.Helper()

	return newUpdateFixtureWithLimit(t, authz, 0)
}

// newUpdateFixtureWithLimit builds a Handler as newUpdateFixture does, with
// checkpoints bounded by limit bytes (zero means the domain default). Tests
// for the checkpoint size limit use a small bound.
func newUpdateFixtureWithLimit(t *testing.T, authz identity.Authorizer, limit int) updateFixture {
	t.Helper()

	namespaces, err := namespace.NewService(
		namespace.Config{Store: homedNamespaceStore{}, Now: nil, Region: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	stores := &recordingStackStore{created: nil, renamed: nil, tags: nil, deleted: nil, deleteErr: nil}

	stacks, err := stack.NewService(stack.Config{Store: stores, Namespaces: namespaces, Now: nil})
	if err != nil {
		t.Fatalf("stack service: %v", err)
	}

	mem := newMemUpdateStore()

	updates, err := stack.NewUpdates(stack.UpdatesConfig{
		Updates:            mem,
		Checkpoints:        mem,
		Events:             mem,
		Namespaces:         namespaces,
		Now:                nil,
		MaxCheckpointBytes: limit,
	})
	if err != nil {
		t.Fatalf("updates service: %v", err)
	}

	handler, err := pulumi.NewHandler(pulumi.Config{
		Stacks:        stacks,
		Updates:       updates,
		Namespaces:    namespaces,
		Authenticator: tokenAuthenticator{},
		Authorizer:    authz,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	return updateFixture{handler: handler, updates: updates, ref: ref}
}

// allowAll authorizes every action.
func allowAll() selectiveAuthorizer {
	return selectiveAuthorizer{deny: map[identity.Action]bool{}}
}

// updateRequest describes one update-route request. Lease is sent as the
// update-token credential when not empty, access as the access token
// otherwise. Either may be empty for the unauthenticated cases.
type updateRequest struct {
	Method string
	Target string
	Access string
	Lease  string
	Body   string
}

// accessRequest builds a request that signs in with the test access token.
func accessRequest(method, target, body string) updateRequest {
	return updateRequest{Method: method, Target: target, Access: "test-token", Lease: "", Body: body}
}

// leaseRequest builds a request that presents the update lease token.
func leaseRequest(method, target, lease, body string) updateRequest {
	return updateRequest{Method: method, Target: target, Access: "", Lease: lease, Body: body}
}

// anonymousRequest builds a request with no credential.
func anonymousRequest(method, target, body string) updateRequest {
	return updateRequest{Method: method, Target: target, Access: "", Lease: "", Body: body}
}

// doUpdate sends one update-route request.
func (f updateFixture) doUpdate(t *testing.T, req updateRequest) (int, string) {
	t.Helper()

	var httpReq *http.Request
	if req.Body == "" {
		httpReq = httptest.NewRequestWithContext(t.Context(), req.Method, req.Target, http.NoBody)
	} else {
		httpReq = httptest.NewRequestWithContext(t.Context(), req.Method, req.Target, strings.NewReader(req.Body))
	}

	switch {
	case req.Lease != "":
		httpReq.Header.Set("Authorization", "update-token "+req.Lease)
	case req.Access != "":
		httpReq.Header.Set("Authorization", "token "+req.Access)
	default:
	}

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, httpReq)

	return rec.Code, rec.Body.String()
}

// mintStarted creates and starts a preview update and returns its ID and
// lease token.
func (f updateFixture) mintStarted(t *testing.T) (stack.UpdateID, string) {
	t.Helper()

	u, err := f.updates.Create(t.Context(), f.ref, stack.Plan{Kind: stack.KindPreview, DryRun: false})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	started, err := f.updates.Start(t.Context(), f.ref, u.ID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	return u.ID, started.Token.Reveal()
}

// mintStartedMutating creates and starts a mutating update and returns its
// ID and lease token. Only mutating updates save checkpoints.
func (f updateFixture) mintStartedMutating(t *testing.T) (stack.UpdateID, string) {
	t.Helper()

	u, err := f.updates.Create(t.Context(), f.ref, stack.Plan{Kind: stack.KindUpdate, DryRun: false})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	started, err := f.updates.Start(t.Context(), f.ref, u.ID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	return u.ID, started.Token.Reveal()
}

// updatePaths holds the lease, checkpoint, cancel and get paths for one update.
type updatePaths struct {
	Start      string
	Renew      string
	Events     string
	Checkpoint string
	Complete   string
	Cancel     string
	Get        string
}

// mintedPaths returns the start, lease, checkpoint, cancel and get paths for one update.
func mintedPaths(id stack.UpdateID) updatePaths {
	base := "/api/stacks/acme/web/prod/update/" + id.String()

	return updatePaths{
		Start:      base,
		Renew:      base + "/renew_lease",
		Events:     base + "/events/batch",
		Checkpoint: base + "/checkpoint",
		Complete:   base + "/complete",
		Cancel:     base + "/cancel",
		Get:        base,
	}
}

// isLeasePattern reports whether pattern is a lease route: the lease token
// stands in for sign-in on these routes only.
func isLeasePattern(pattern string) bool {
	for _, suffix := range []string{"/renew_lease", "/events/batch", "/checkpoint", "/complete"} {
		if strings.HasSuffix(pattern, suffix) {
			return true
		}
	}

	return false
}

// wantUpdateAction maps every update route to the action it must declare:
// reads need unit:read, writes need unit:write.
func wantUpdateAction() map[string]identity.Action {
	write := map[string]bool{}
	for _, pattern := range updatePatterns() {
		write[pattern] = true
	}

	for _, pattern := range []string{
		"GET /api/stacks/{namespace}/{project}/{stack}/update/{update}",
		"GET /api/stacks/{namespace}/{project}/{stack}/updates",
		"GET /api/stacks/{namespace}/{project}/{stack}/export",
		"GET /api/stacks/{namespace}/{project}/{stack}/export/{version}",
	} {
		write[pattern] = false
	}

	want := map[string]identity.Action{}

	for pattern, isWrite := range write {
		if isWrite {
			want[pattern] = identity.ActionUnitWrite
		} else {
			want[pattern] = identity.ActionUnitRead
		}
	}

	return want
}

// wantUpdateScope maps every update route to the scope it must declare.
func wantUpdateScope() map[string]pulumi.Scope {
	want := map[string]pulumi.Scope{}

	for _, pattern := range updatePatterns() {
		if isLeasePattern(pattern) {
			want[pattern] = pulumi.ScopeUpdateLease
		} else {
			want[pattern] = pulumi.ScopeAuthorized
		}
	}

	return want
}

// checkUpdateRoute proves one route declares its action with a resolver and
// the expected scope.
func checkUpdateRoute(
	t *testing.T, route pulumi.Route, wantScope map[string]pulumi.Scope, wantAction map[string]identity.Action,
) {
	t.Helper()

	pattern := route.Method + " " + route.Path
	scope, ok := wantScope[pattern]

	if !ok {
		return
	}

	if route.Scope != scope {
		t.Errorf("route %q scope = %v, want %v", pattern, route.Scope, scope)
	}

	if len(route.Actions) != 1 || route.Actions[0] != wantAction[pattern] {
		t.Errorf("route %q actions = %v, want exactly [%s]", pattern, route.Actions, wantAction[pattern])
	}

	if route.Resolve == nil {
		t.Errorf("route %q declares actions but no resolver", pattern)
	}
}

// TestUpdateRoutesDeclareActions proves every update route declares its
// action on the unit, with a resolver and the expected scope: reads declare
// unit:read, writes declare unit:write, and the lease token stands in for
// sign-in on the lease routes only.
func TestUpdateRoutesDeclareActions(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	wantScope := wantUpdateScope()
	wantAction := wantUpdateAction()
	seen := map[string]bool{}

	for _, route := range f.handler.Routes() {
		pattern := route.Method + " " + route.Path
		if _, ok := wantScope[pattern]; ok {
			seen[pattern] = true
		}

		checkUpdateRoute(t, route, wantScope, wantAction)
	}

	for _, pattern := range updatePatterns() {
		if !seen[pattern] {
			t.Errorf("route %q is not declared", pattern)
		}
	}
}

// createPreviewUpdate creates a preview through HTTP and returns its ID.
func createPreviewUpdate(t *testing.T, f updateFixture) stack.UpdateID {
	t.Helper()

	status, body := f.doUpdate(t, accessRequest(http.MethodPost, "/api/stacks/acme/web/prod/preview", `{}`))
	if status != http.StatusOK {
		t.Fatalf("create answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var created struct {
		UpdateID string `json:"updateID"`
	}

	if err := json.Unmarshal([]byte(body), &created); err != nil || created.UpdateID == "" {
		t.Fatalf("create answers %q, want an update ID: %v", body, err)
	}

	return stack.UpdateID(created.UpdateID)
}

// startPreviewUpdate starts a preview through HTTP and returns its lease.
// A preview takes no version: it carries the stack's current version.
func startPreviewUpdate(t *testing.T, f updateFixture, paths updatePaths) string {
	t.Helper()

	status, body := f.doUpdate(t, accessRequest(http.MethodPost, paths.Start, `{}`))
	if status != http.StatusOK {
		t.Fatalf("start answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var started struct {
		Token   string `json:"token"`
		Version int    `json:"version"`
	}

	if err := json.Unmarshal([]byte(body), &started); err != nil {
		t.Fatalf("decode start: %v (%s)", err, body)
	}

	if started.Token == "" {
		t.Fatalf("start answers no lease token (%s)", body)
	}

	if started.Version != 0 {
		t.Fatalf("preview version = %d, want the stack's current version 0", started.Version)
	}

	return started.Token
}

// finishPreviewUpdate posts one engine event batch and completes a preview.
func finishPreviewUpdate(t *testing.T, f updateFixture, paths updatePaths, lease string) {
	t.Helper()

	event := `{"sequence":0,"timestamp":0,"diagnosticEvent":{"message":"hi","severity":"info"}}`
	status, body := f.doUpdate(t, leaseRequest(http.MethodPost, paths.Events, lease, `{"events":[`+event+`]}`))
	if status != http.StatusOK {
		t.Fatalf("events answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	status, body = f.doUpdate(t, leaseRequest(http.MethodPost, paths.Complete, lease, `{"status":"succeeded"}`))
	if status != http.StatusOK {
		t.Fatalf("complete answers %d, want %d (%s)", status, http.StatusOK, body)
	}
}

// checkPreviewInvisible proves a finished preview is readable but leaves no
// history: a preview changes nothing.
func checkPreviewInvisible(t *testing.T, f updateFixture, paths updatePaths) {
	t.Helper()

	status, body := f.doUpdate(t, accessRequest(http.MethodGet, paths.Get, ""))
	if status != http.StatusOK || !strings.Contains(body, `"status":"succeeded"`) {
		t.Fatalf("get answers %d %q, want 200 with status succeeded", status, body)
	}

	status, body = f.doUpdate(t, accessRequest(http.MethodGet, "/api/stacks/acme/web/prod/updates", ""))
	if status != http.StatusOK || strings.TrimSpace(body) != `{"updates":[]}` {
		t.Fatalf("history answers %d %q, want 200 with no updates", status, body)
	}
}

// TestPreviewUpdateCycle drives one preview through HTTP: create, start,
// events, complete, get and history. History stays empty: a preview changes
// nothing.
func TestPreviewUpdateCycle(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	id := createPreviewUpdate(t, f)
	paths := mintedPaths(id)
	lease := startPreviewUpdate(t, f, paths)
	finishPreviewUpdate(t, f, paths, lease)
	checkPreviewInvisible(t, f, paths)
}

// leaseCase is one renewal or completion attempt against a lease route.
type leaseCase struct {
	name   string
	method string
	target string
	body   string
}

// leaseCases returns the renew and complete attempts for paths.
func leaseCases(prefix string, paths updatePaths) []leaseCase {
	return []leaseCase{
		{name: prefix + "renew", method: http.MethodPost, target: paths.Renew, body: `{"duration":60}`},
		{name: prefix + "complete", method: http.MethodPost, target: paths.Complete, body: `{"status":"succeeded"}`},
	}
}

// checkUnauthorizedError proves an error body carries the 401 code shape.
func checkUnauthorizedError(t *testing.T, body string) {
	t.Helper()

	var decoded struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}

	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode error: %v (%s)", err, body)
	}

	if decoded.Code != http.StatusUnauthorized || decoded.Message == "" {
		t.Fatalf("error = %+v, want code 401 with a message", decoded)
	}
}

// checkLeaseMissing proves the lease routes answer 401 without a credential.
func checkLeaseMissing(t *testing.T, f updateFixture, paths updatePaths) {
	t.Helper()

	for _, tc := range leaseCases("missing ", paths) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, body := f.doUpdate(t, anonymousRequest(tc.method, tc.target, tc.body))
			if status != http.StatusUnauthorized {
				t.Fatalf("status %d, want %d (%s)", status, http.StatusUnauthorized, body)
			}
		})
	}
}

// checkLeaseWrong proves the lease routes answer 401 for a foreign token.
func checkLeaseWrong(t *testing.T, f updateFixture, paths updatePaths, foreign string) {
	t.Helper()

	for _, tc := range leaseCases("wrong ", paths) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, body := f.doUpdate(t, leaseRequest(tc.method, tc.target, foreign, tc.body))
			if status != http.StatusUnauthorized {
				t.Fatalf("status %d, want %d (%s)", status, http.StatusUnauthorized, body)
			}

			checkUnauthorizedError(t, body)
		})
	}
}

// TestUpdateLeaseRejected proves renewal and completion with a wrong lease
// token are rejected, without touching any store.
func TestUpdateLeaseRejected(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	id, _ := f.mintStarted(t)
	paths := mintedPaths(id)

	foreign, err := token.New()
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	checkLeaseMissing(t, f, paths)
	checkLeaseWrong(t, f, paths, foreign.Reveal())
}

// TestUpdateRoutesEnforce proves every update route enforces its row.
// Access-authed routes answer 200 when allowed and 403 when their action is
// denied. Lease routes answer 200 with a valid lease even when the
// Authorizer denies: the lease stands in for the declared unit:write. Both
// answer 401 without a usable credential.
func TestUpdateRoutesEnforce(t *testing.T) {
	t.Parallel()

	for _, pattern := range updatePatterns() {
		t.Run(pattern, func(t *testing.T) {
			t.Parallel()
			checkUpdateRouteEnforcement(t, pattern)
		})
	}
}

// checkUpdateRouteEnforcement proves one update route enforces its row with
// a fresh handler and fresh update state per case.
func checkUpdateRouteEnforcement(t *testing.T, pattern string) {
	t.Helper()

	route := updateRoute(t, newUpdateFixture(t, allowAll()).handler, pattern)

	if route.Scope == pulumi.ScopeUpdateLease {
		checkLeaseRouteEnforcement(t, route, pattern)

		return
	}

	allowed := newUpdateFixture(t, allowAll())
	at := accessRouteTarget(t, allowed, pattern)

	status, resp := allowed.doUpdate(t, accessRequest(route.Method, at.Target, at.Body))
	if status != http.StatusOK {
		t.Fatalf("allowed request answers %d, want %d (%s)", status, http.StatusOK, resp)
	}

	denied := newUpdateFixture(t, selectiveAuthorizer{deny: map[identity.Action]bool{route.Actions[0]: true}})

	status, resp = denied.doUpdate(t, accessRequest(route.Method, at.Target, at.Body))
	if status != http.StatusForbidden {
		t.Fatalf("denied request answers %d, want %d (%s)", status, http.StatusForbidden, resp)
	}

	status, resp = allowed.doUpdate(t, anonymousRequest(route.Method, at.Target, at.Body))
	if status != http.StatusUnauthorized {
		t.Fatalf("unsigned request answers %d, want %d (%s)", status, http.StatusUnauthorized, resp)
	}
}

// updateRoute returns the declared route for pattern.
func updateRoute(t *testing.T, handler *pulumi.Handler, pattern string) pulumi.Route {
	t.Helper()

	for _, route := range handler.Routes() {
		if route.Method+" "+route.Path == pattern {
			return route
		}
	}

	t.Fatalf("route %q is not declared", pattern)

	return pulumi.Route{Method: "", Path: "", Scope: 0, Actions: nil, Resolve: nil, Serve: nil}
}

// accessTarget holds the target and body for an access-authed update route.
type accessTarget struct {
	Target string
	Body   string
}

// accessRouteTarget builds the target and body for an access-authed update
// route, minting the update state the route needs in f: a created update
// for start and get, a started update for cancel, a finished update with a
// checkpoint for versioned export, none for create, history and export, and a
// deployment body for import.
func accessRouteTarget(t *testing.T, f updateFixture, pattern string) accessTarget {
	t.Helper()

	switch {
	case strings.HasSuffix(pattern, "/export/{version}"):
		input := stack.CheckpointInput{
			Features:      nil,
			Deployment:    []byte(`{}`),
			SchemaVersion: 3,
			Sequence:      0,
			Invalid:       false,
		}

		_, importErr := f.updates.Import(t.Context(), f.ref, input)
		if importErr != nil {
			t.Fatalf("import: %v", importErr)
		}

		return accessTarget{Target: "/api/stacks/acme/web/prod/export/1", Body: ""}
	case strings.HasSuffix(pattern, "/updates"), strings.HasSuffix(pattern, "/export"):
		return accessTarget{
			Target: "/api/stacks/acme/web/prod" + strings.TrimPrefix(
				pattern, "GET /api/stacks/{namespace}/{project}/{stack}",
			),
			Body: "",
		}
	case strings.HasSuffix(pattern, "/import"):
		return accessTarget{Target: "/api/stacks/acme/web/prod/import", Body: `{"version":3,"deployment":{}}`}
	case strings.HasSuffix(pattern, "/cancel"):
		u, err := f.updates.Create(t.Context(), f.ref, stack.Plan{Kind: stack.KindUpdate, DryRun: false})
		if err != nil {
			t.Fatalf("create: %v", err)
		}

		if _, err := f.updates.Start(t.Context(), f.ref, u.ID); err != nil {
			t.Fatalf("start: %v", err)
		}

		return accessTarget{Target: "/api/stacks/acme/web/prod/update/" + u.ID.String() + "/cancel", Body: ""}
	case strings.HasSuffix(pattern, "/update/{update}"):
		u, err := f.updates.Create(t.Context(), f.ref, stack.Plan{Kind: stack.KindPreview, DryRun: false})
		if err != nil {
			t.Fatalf("create: %v", err)
		}

		return accessTarget{Target: "/api/stacks/acme/web/prod/update/" + u.ID.String(), Body: "{}"}
	default:
		kind := strings.TrimPrefix(pattern, "POST /api/stacks/{namespace}/{project}/{stack}/")

		return accessTarget{Target: "/api/stacks/acme/web/prod/" + kind, Body: "{}"}
	}
}

// leaseBodies holds one valid body per lease route.
func leaseBodies() map[string]string {
	return map[string]string{
		"POST /api/stacks/{namespace}/{project}/{stack}/update/{update}/renew_lease":  `{"duration":60}`,
		"POST /api/stacks/{namespace}/{project}/{stack}/update/{update}/events/batch": `{"events":[]}`,
		"PATCH /api/stacks/{namespace}/{project}/{stack}/update/{update}/checkpoint":  `{"version":3,"deployment":{}}`,
		"POST /api/stacks/{namespace}/{project}/{stack}/update/{update}/complete":     `{"status":"succeeded"}`,
	}
}

// leaseTargets returns the renew, event, checkpoint and complete paths for one update.
func leaseTargets(id stack.UpdateID) map[string]string {
	paths := mintedPaths(id)

	return map[string]string{
		"POST /api/stacks/{namespace}/{project}/{stack}/update/{update}/renew_lease":  paths.Renew,
		"POST /api/stacks/{namespace}/{project}/{stack}/update/{update}/events/batch": paths.Events,
		"PATCH /api/stacks/{namespace}/{project}/{stack}/update/{update}/checkpoint":  paths.Checkpoint,
		"POST /api/stacks/{namespace}/{project}/{stack}/update/{update}/complete":     paths.Complete,
	}
}

// mintForLease creates and starts the update that pattern needs: a mutating
// update for the checkpoint route, which a preview never saves, and a preview
// otherwise.
func mintForLease(t *testing.T, f updateFixture, pattern string) (stack.UpdateID, string) {
	t.Helper()

	if strings.HasSuffix(pattern, "/checkpoint") {
		return f.mintStartedMutating(t)
	}

	return f.mintStarted(t)
}

// checkLeaseRouteEnforcement proves one lease route answers 200 with a
// valid lease whatever the Authorizer says, and 401 otherwise.
func checkLeaseRouteEnforcement(t *testing.T, route pulumi.Route, pattern string) {
	t.Helper()

	body, ok := leaseBodies()[pattern]
	if !ok {
		t.Fatalf("no lease body for route %q", pattern)
	}

	// A valid lease answers 200, whether the Authorizer allows or denies.
	for _, authz := range []identity.Authorizer{
		allowAll(),
		selectiveAuthorizer{deny: map[identity.Action]bool{identity.ActionUnitWrite: true}},
	} {
		f := newUpdateFixture(t, authz)
		id, lease := mintForLease(t, f, pattern)
		target := leaseTargets(id)[pattern]
		status, resp := f.doUpdate(t, leaseRequest(route.Method, target, lease, body))

		if status != http.StatusOK {
			t.Fatalf("leased request answers %d, want %d (%s)", status, http.StatusOK, resp)
		}
	}

	// Without a usable lease the route answers 401.
	f := newUpdateFixture(t, allowAll())
	id, _ := mintForLease(t, f, pattern)
	target := leaseTargets(id)[pattern]

	foreign, err := token.New()
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	requests := []updateRequest{
		anonymousRequest(route.Method, target, body),
		accessRequest(route.Method, target, body),
		leaseRequest(route.Method, target, foreign.Reveal(), body),
	}

	for _, req := range requests {
		status, resp := f.doUpdate(t, req)
		if status != http.StatusUnauthorized {
			t.Fatalf("unleased request answers %d, want %d (%s)", status, http.StatusUnauthorized, resp)
		}
	}
}

// historyEntry is one entry of the stack history as the tests decode it.
type historyEntry struct {
	Result  string `json:"result"`
	Kind    string `json:"kind"`
	Version int    `json:"version"`
}

// historyPayload is the stack history as the tests decode it.
type historyPayload struct {
	Updates []historyEntry `json:"updates"`
}

// TestHistoryShowsFinishedUpdates proves the history lists a finished
// mutating update with its kind, result and version.
func TestHistoryShowsFinishedUpdates(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	u, err := f.updates.Create(t.Context(), f.ref, stack.Plan{Kind: stack.KindUpdate, DryRun: false})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	started, err := f.updates.Start(t.Context(), f.ref, u.ID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	if _, err := f.updates.Complete(t.Context(), f.ref, u.ID, started.Token, stack.StatusSucceeded); err != nil {
		t.Fatalf("complete: %v", err)
	}

	status, body := f.doUpdate(
		t,
		accessRequest(http.MethodGet, "/api/stacks/acme/web/prod/updates", ""),
	)
	if status != http.StatusOK {
		t.Fatalf("history answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var history historyPayload

	if err := json.Unmarshal([]byte(body), &history); err != nil {
		t.Fatalf("decode history: %v (%s)", err, body)
	}

	if len(history.Updates) != 1 {
		t.Fatalf("history holds %d updates, want 1 (%s)", len(history.Updates), body)
	}

	got := history.Updates[0]
	if got.Kind != "update" || got.Result != "succeeded" || got.Version != 1 {
		t.Fatalf("history entry = %+v, want kind update result succeeded version 1", got)
	}
}

// TestRenewLeaseEchoesToken proves a renewal keeps the token: the answer
// carries the presented token and its new expiry.
func TestRenewLeaseEchoesToken(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	id, lease := f.mintStarted(t)
	paths := mintedPaths(id)

	status, body := f.doUpdate(t, leaseRequest(http.MethodPost, paths.Renew, lease, `{"duration":60}`))
	if status != http.StatusOK {
		t.Fatalf("renew answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var renewed struct {
		Token           string `json:"token"`
		TokenExpiration int64  `json:"tokenExpiration"`
	}

	if err := json.Unmarshal([]byte(body), &renewed); err != nil {
		t.Fatalf("decode renew: %v (%s)", err, body)
	}

	if renewed.Token != lease {
		t.Fatal("renew answers a new token, want the presented one kept")
	}

	if renewed.TokenExpiration <= 0 {
		t.Fatalf("renew answers no expiry (%s)", body)
	}
}

// checkpointPaths starts a mutating update and returns its paths with its
// lease token.
func checkpointPaths(t *testing.T, f updateFixture) (updatePaths, string) {
	t.Helper()

	id, lease := f.mintStartedMutating(t)

	return mintedPaths(id), lease
}

// TestCheckpointCycle saves two checkpoints to a mutating update and proves
// the last write wins: export returns the second deployment.
func TestCheckpointCycle(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	paths, lease := checkpointPaths(t, f)

	first := `{"version":3,"deployment":{"resources":[{"urn":"urn:pulumi:dev::web::pulumi:pulumi:Stack::web-dev"}]}}`
	status, body := f.doUpdate(t, leaseRequest(http.MethodPatch, paths.Checkpoint, lease, first))
	if status != http.StatusOK {
		t.Fatalf("first checkpoint answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	const second = `{"resources":[]}`

	status, body = f.doUpdate(
		t,
		leaseRequest(http.MethodPatch, paths.Checkpoint, lease, `{"version":3,"deployment":`+second+`}`),
	)
	if status != http.StatusOK {
		t.Fatalf("second checkpoint answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	status, body = f.doUpdate(t, accessRequest(http.MethodGet, "/api/stacks/acme/web/prod/export", ""))
	if status != http.StatusOK {
		t.Fatalf("export answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var exported struct {
		Deployment json.RawMessage `json:"deployment"`
		Version    int             `json:"version"`
	}

	if err := json.Unmarshal([]byte(body), &exported); err != nil {
		t.Fatalf("decode export: %v (%s)", err, body)
	}

	if exported.Version != 3 || string(exported.Deployment) != second {
		t.Fatalf(
			"export = version %d %s, want version 3 with the second deployment",
			exported.Version,
			exported.Deployment,
		)
	}
}

// TestCheckpointPreviewRejected proves a preview saves no checkpoint.
func TestCheckpointPreviewRejected(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	id, lease := f.mintStarted(t)
	paths := mintedPaths(id)

	status, body := f.doUpdate(
		t,
		leaseRequest(http.MethodPatch, paths.Checkpoint, lease, `{"version":3,"deployment":{}}`),
	)
	if status != http.StatusConflict {
		t.Fatalf("preview checkpoint answers %d, want %d (%s)", status, http.StatusConflict, body)
	}
}

// TestCheckpointBodyLimit proves a checkpoint over the size limit answers
// 413 with the limit in the message.
func TestCheckpointBodyLimit(t *testing.T) {
	t.Parallel()

	const limit = 64

	f := newUpdateFixtureWithLimit(t, allowAll(), limit)
	paths, lease := checkpointPaths(t, f)

	big := strings.Repeat("a", limit+1)
	payload := `{"version":3,"deployment":"` + big + `"}`
	status, body := f.doUpdate(t, leaseRequest(http.MethodPatch, paths.Checkpoint, lease, payload))

	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want %d (%s)", status, http.StatusRequestEntityTooLarge, body)
	}

	var decoded struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}

	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode error: %v (%s)", err, body)
	}

	if decoded.Code != http.StatusRequestEntityTooLarge || !strings.Contains(decoded.Message, strconv.Itoa(limit)) {
		t.Fatalf("error = %+v, want code 413 with limit %d in the message", decoded, limit)
	}
}

// TestEventBatchBodyLimit proves an event batch over the size limit is
// rejected before it reaches any store.
func TestEventBatchBodyLimit(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	id, lease := f.mintStarted(t)
	paths := mintedPaths(id)

	big := strings.Repeat("a", 8<<20+1)
	payload := `{"events":[{"sequence":0,"payload":"` + big + `"}]}`
	status, body := f.doUpdate(t, leaseRequest(http.MethodPost, paths.Events, lease, payload))

	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want %d (%s)", status, http.StatusRequestEntityTooLarge, body)
	}

	var decoded struct {
		Code int `json:"code"`
	}

	if err := json.Unmarshal([]byte(body), &decoded); err != nil || decoded.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("error = %q, want code 413", body)
	}
}

// TestImportStackCycle imports a deployment through HTTP: the import answers
// the ID of its update, shows in history as kind import, and becomes the
// latest export.
func TestImportStackCycle(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	status, body := f.doUpdate(t, accessRequest(
		http.MethodPost, "/api/stacks/acme/web/prod/import", `{"version":3,"deployment":{"resources":[]}}`,
	))
	if status != http.StatusOK {
		t.Fatalf("import answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var imported struct {
		UpdateID string `json:"updateId"` //nolint:tagliatelle // The Pulumi CLI's wire name; it must not change.
	}

	if err := json.Unmarshal([]byte(body), &imported); err != nil || imported.UpdateID == "" {
		t.Fatalf("import answers %q, want an update id: %v", body, err)
	}

	status, body = f.doUpdate(t, accessRequest(http.MethodGet, "/api/stacks/acme/web/prod/updates", ""))
	if status != http.StatusOK {
		t.Fatalf("history answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var history historyPayload

	if err := json.Unmarshal([]byte(body), &history); err != nil {
		t.Fatalf("decode history: %v (%s)", err, body)
	}

	if len(history.Updates) != 1 {
		t.Fatalf("history holds %d updates, want 1 (%s)", len(history.Updates), body)
	}

	got := history.Updates[0]
	if got.Kind != "import" || got.Result != "succeeded" || got.Version != 1 {
		t.Fatalf("history entry = %+v, want kind import result succeeded version 1", got)
	}

	status, body = f.doUpdate(t, accessRequest(http.MethodGet, "/api/stacks/acme/web/prod/export", ""))
	if status != http.StatusOK || !strings.Contains(body, `"resources"`) {
		t.Fatalf("export answers %d %q, want 200 with the imported deployment", status, body)
	}
}

// TestImportStackConflict proves an import while another update runs is
// rejected, and the running update keeps the stack lock.
func TestImportStackConflict(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	id, _ := f.mintStartedMutating(t)

	status, body := f.doUpdate(t, accessRequest(
		http.MethodPost, "/api/stacks/acme/web/prod/import", `{"version":3,"deployment":{}}`,
	))
	if status != http.StatusConflict {
		t.Fatalf("import during a running update answers %d, want %d (%s)", status, http.StatusConflict, body)
	}

	active, err := f.updates.Active(t.Context(), f.ref)
	if err != nil || active.ID != id {
		t.Fatalf("active update = %q, %v; want the running one %q", active.ID, err, id)
	}
}

// TestImportStackInvalidBody proves an import with a bad body answers 400
// and records no update: validation runs before the import update exists.
func TestImportStackInvalidBody(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{name: "invalid JSON", body: `{`},
		{name: "no schema version", body: `{"version":0,"deployment":{}}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newUpdateFixture(t, allowAll())

			status, body := f.doUpdate(
				t,
				accessRequest(http.MethodPost, "/api/stacks/acme/web/prod/import", tc.body),
			)
			if status != http.StatusBadRequest {
				t.Fatalf("import answers %d, want %d (%s)", status, http.StatusBadRequest, body)
			}

			status, body = f.doUpdate(
				t,
				accessRequest(http.MethodGet, "/api/stacks/acme/web/prod/updates", ""),
			)
			if status != http.StatusOK || strings.TrimSpace(body) != `{"updates":[]}` {
				t.Fatalf("history answers %d %q, want 200 with no updates", status, body)
			}
		})
	}
}

// TestExportStackVersion proves versioned export returns only the named
// version: each version holds its own checkpoint, a missing or empty version
// answers 404, and a malformed version answers 400.
func TestExportStackVersion(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	for _, deployment := range []string{`{"v":1}`, `{"v":2}`} {
		status, body := f.doUpdate(t, accessRequest(
			http.MethodPost, "/api/stacks/acme/web/prod/import",
			`{"version":3,"deployment":`+deployment+`}`,
		))
		if status != http.StatusOK {
			t.Fatalf("import answers %d, want %d (%s)", status, http.StatusOK, body)
		}
	}

	// A finished update without a checkpoint leaves its version empty.
	finishMutatingUpdate(t, f, "", stack.StatusSucceeded)

	cases := []struct {
		name   string
		target string
		check  string
		want   int
	}{
		{name: "first version", target: "/api/stacks/acme/web/prod/export/1", want: http.StatusOK, check: `"v":1`},
		{name: "second version", target: "/api/stacks/acme/web/prod/export/2", want: http.StatusOK, check: `"v":2`},
		{name: "missing version", target: "/api/stacks/acme/web/prod/export/99", want: http.StatusNotFound, check: ""},
		{name: "empty version", target: "/api/stacks/acme/web/prod/export/3", want: http.StatusNotFound, check: ""},
		{name: "not a number", target: "/api/stacks/acme/web/prod/export/abc", want: http.StatusBadRequest, check: ""},
		{name: "negative", target: "/api/stacks/acme/web/prod/export/-1", want: http.StatusBadRequest, check: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, body := f.doUpdate(t, accessRequest(http.MethodGet, tc.target, ""))
			if status != tc.want {
				t.Fatalf("export answers %d, want %d (%s)", status, tc.want, body)
			}

			if tc.check != "" && !strings.Contains(body, tc.check) {
				t.Fatalf("export answers %q, want it to hold %s", body, tc.check)
			}
		})
	}
}

// TestExportStackVersionNoFallback proves versioned export never falls back:
// version 2 keeps no checkpoint of its own after version 1 saved one, so it
// answers 404 even though an older checkpoint exists.
func TestExportStackVersionNoFallback(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	finishMutatingUpdate(t, f, `{"v":1}`, stack.StatusSucceeded)
	finishMutatingUpdate(t, f, "", stack.StatusSucceeded)

	status, body := f.doUpdate(t, accessRequest(http.MethodGet, "/api/stacks/acme/web/prod/export/2", ""))
	if status != http.StatusNotFound {
		t.Fatalf("export of an empty version answers %d, want %d (%s)", status, http.StatusNotFound, body)
	}
}

// finishMutatingUpdate creates, starts, and completes a mutating update,
// saving deployment as its checkpoint first when not empty.
func finishMutatingUpdate(t *testing.T, f updateFixture, deployment string, status stack.UpdateStatus) {
	t.Helper()

	u, createErr := f.updates.Create(t.Context(), f.ref, stack.Plan{Kind: stack.KindUpdate, DryRun: false})
	if createErr != nil {
		t.Fatalf("create: %v", createErr)
	}

	started, startErr := f.updates.Start(t.Context(), f.ref, u.ID)
	if startErr != nil {
		t.Fatalf("start: %v", startErr)
	}

	if deployment != "" {
		input := stack.CheckpointInput{
			Features:      nil,
			Deployment:    []byte(deployment),
			SchemaVersion: 3,
			Sequence:      0,
			Invalid:       false,
		}

		saveErr := f.updates.SaveCheckpoint(t.Context(), f.ref, u.ID, started.Token, input)
		if saveErr != nil {
			t.Fatalf("save checkpoint: %v", saveErr)
		}
	}

	_, completeErr := f.updates.Complete(t.Context(), f.ref, u.ID, started.Token, status)
	if completeErr != nil {
		t.Fatalf("complete: %v", completeErr)
	}
}

// TestCancelUpdateCycle cancels a running mutating update through HTTP: the
// stack is locked while it runs, cancel frees it, and a new update starts.
func TestCancelUpdateCycle(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	id, _ := f.mintStartedMutating(t)
	paths := mintedPaths(id)

	status, body := f.doUpdate(t, accessRequest(http.MethodPost, paths.Cancel, ""))
	if status != http.StatusOK {
		t.Fatalf("cancel answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	// A canceled update keeps its number, so the next one does not reuse it.
	u, err := f.updates.Create(t.Context(), f.ref, stack.Plan{Kind: stack.KindUpdate, DryRun: false})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	started, err := f.updates.Start(t.Context(), f.ref, u.ID)
	if err != nil {
		t.Fatalf("start after cancel: %v", err)
	}

	if started.Update.Version != 2 {
		t.Fatalf("version after cancel = %d, want 2", started.Update.Version)
	}
}

// TestCancelUpdateIdempotent proves a retried cancel with the same final
// status succeeds: the CLI retries cancel when a response is lost.
func TestCancelUpdateIdempotent(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	id, _ := f.mintStartedMutating(t)
	paths := mintedPaths(id)

	for range 2 {
		status, body := f.doUpdate(t, accessRequest(http.MethodPost, paths.Cancel, ""))
		if status != http.StatusOK {
			t.Fatalf("cancel answers %d, want %d (%s)", status, http.StatusOK, body)
		}
	}
}

// TestCancelUpdateNeedsAccessToken proves the cancel route serves the access
// token, not the update lease: the CLI sends no lease on cancel.
func TestCancelUpdateNeedsAccessToken(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	id, lease := f.mintStartedMutating(t)
	paths := mintedPaths(id)

	status, _ := f.doUpdate(t, anonymousRequest(http.MethodPost, paths.Cancel, ""))
	if status != http.StatusUnauthorized {
		t.Fatalf("unsigned cancel answers %d, want %d", status, http.StatusUnauthorized)
	}

	status, body := f.doUpdate(t, leaseRequest(http.MethodPost, paths.Cancel, lease, ""))
	if status != http.StatusUnauthorized {
		t.Fatalf("leased cancel answers %d, want %d (%s)", status, http.StatusUnauthorized, body)
	}
}

// TestGetStackShowsActiveUpdate proves the stack response always holds
// activeUpdate as a string: the ID of the running mutating update, or ""
// when none runs. A preview never shows.
func TestGetStackShowsActiveUpdate(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	get := func() (int, string) {
		return f.doUpdate(t, accessRequest(http.MethodGet, "/api/stacks/acme/web/prod", ""))
	}

	check := func(want string) {
		t.Helper()

		status, body := get()
		if status != http.StatusOK {
			t.Fatalf("get stack answers %d, want %d (%s)", status, http.StatusOK, body)
		}

		var raw map[string]any
		if err := json.Unmarshal([]byte(body), &raw); err != nil {
			t.Fatalf("decode stack: %v (%s)", err, body)
		}

		active, ok := raw["activeUpdate"]
		if !ok {
			t.Fatalf("stack response lacks activeUpdate (%s)", body)
		}

		text, ok := active.(string)
		if !ok {
			t.Fatalf("activeUpdate = %v, want a string", active)
		}

		if text != want {
			t.Fatalf("activeUpdate = %q, want %q", text, want)
		}
	}

	check("")

	// A preview alone never shows.
	preview, err := f.updates.Create(t.Context(), f.ref, stack.Plan{Kind: stack.KindPreview, DryRun: false})
	if err != nil {
		t.Fatalf("create preview: %v", err)
	}

	if _, err := f.updates.Start(t.Context(), f.ref, preview.ID); err != nil {
		t.Fatalf("start preview: %v", err)
	}

	check("")

	id, _ := f.mintStartedMutating(t)

	// A preview takes no lock, so the mutating update still shows.
	check(id.String())

	paths := mintedPaths(id)

	status, body := f.doUpdate(t, accessRequest(http.MethodPost, paths.Cancel, ""))
	if status != http.StatusOK {
		t.Fatalf("cancel answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	// The preview still runs, so this stays the preview-only case.
	check("")
}
