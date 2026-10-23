package inventory_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	apiinventory "opendustpan.org/opendustpan/internal/api/inventory"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// memNamespaceStore is an in-memory namespace.Store.
type memNamespaceStore struct {
	namespaces map[namespace.Name]namespace.Namespace
}

func (m *memNamespaceStore) CreateNamespace(_ context.Context, ns namespace.Namespace) error {
	if _, ok := m.namespaces[ns.Name]; ok {
		return namespace.ErrAlreadyExists
	}

	m.namespaces[ns.Name] = ns

	return nil
}

func (m *memNamespaceStore) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	ns, ok := m.namespaces[name]
	if !ok {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return ns, nil
}

func (m *memNamespaceStore) ListNamespaces(_ context.Context) ([]namespace.Namespace, error) {
	out := make([]namespace.Namespace, 0, len(m.namespaces))
	for _, ns := range m.namespaces {
		out = append(out, ns)
	}

	slices.SortFunc(out, func(a, b namespace.Namespace) int {
		return strings.Compare(a.Name.String(), b.Name.String())
	})

	return out, nil
}

func (m *memNamespaceStore) Ping(_ context.Context) error {
	return nil
}

// seedNamespace creates the acme namespace homed in the test region.
func seedNamespace(t *testing.T, nsStore *memNamespaceStore) {
	t.Helper()

	n, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("parse namespace: %v", err)
	}

	ns := namespace.Namespace{
		ID:              namespace.NewID(),
		CreatedAt:       time.Now(),
		Name:            n,
		HomeRegion:      "test-region",
		FailoverVersion: 1,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := nsStore.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
}

// memReader is an in-memory inventory.Reader. It sorts entries by tool, then
// name on read, as the port contract requires of implementations.
type memReader struct {
	entries map[namespace.Name][]coreinventory.Entry
	err     error
}

func (m *memReader) ListUnits(_ context.Context, ns namespace.Name) ([]coreinventory.Entry, error) {
	if m.err != nil {
		return nil, m.err
	}

	out := append([]coreinventory.Entry(nil), m.entries[ns]...)
	slices.SortFunc(out, func(a, b coreinventory.Entry) int {
		if a.Tool != b.Tool {
			return strings.Compare(a.Tool, b.Tool)
		}

		return strings.Compare(a.Name, b.Name)
	})

	return out, nil
}

// QueryUnits implements coreinventory.Reader over the entries of ListUnits.
func (m *memReader) QueryUnits(ctx context.Context, q coreinventory.UnitQuery) ([]coreinventory.Entry, error) {
	all, err := m.ListUnits(ctx, q.Namespace)
	if err != nil {
		return nil, err
	}

	return coreinventory.PageUnits(all, q), nil
}

func (m *memReader) GetUnit(_ context.Context, unitID string) (coreinventory.Entry, error) {
	if m.err != nil {
		return coreinventory.Entry{}, m.err
	}

	for _, entries := range m.entries {
		for i := range entries {
			if entries[i].UnitID == unitID {
				return entries[i], nil
			}
		}
	}

	return coreinventory.Entry{}, coreinventory.ErrUnitNotFound
}

// stubAuthenticator denies every token. Tests that send no token never reach it.
type stubAuthenticator struct{}

func (stubAuthenticator) AuthenticateToken(_ context.Context, _ string) (identity.Principal, error) {
	return identity.Principal{}, identity.ErrUnauthenticated
}

// tokenAuthenticator signs in exactly the test token.
type tokenAuthenticator struct{}

func (tokenAuthenticator) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	if token != "test-token" {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject:        "tester",
		DisplayName:    "Tester",
		Admin:          false,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
		Kind:           "",
		Credential:     "",
		JobID:          "",
	}, nil
}

// stubAuthorizer denies every action.
type stubAuthorizer struct{}

func (stubAuthorizer) Authorize(
	_ context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

// ListObjects gives an administrator every object and any other principal none.
func (stubAuthorizer) ListObjects(_ context.Context, p identity.Principal, _ identity.Action) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

// selectiveAuthorizer denies exactly the actions in deny and allows the rest.
type selectiveAuthorizer struct {
	deny map[identity.Action]bool
}

func (s selectiveAuthorizer) Authorize(
	_ context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	if s.deny[action] {
		return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
	}

	return nil
}

// ListObjects gives an administrator every object and any other principal none.
func (selectiveAuthorizer) ListObjects(
	_ context.Context,
	p identity.Principal,
	_ identity.Action,
) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

// denyUnits denies unit:read on the references in deny and allows the rest.
// It proves that the handler removes entries the caller may not read.
type denyUnits struct {
	deny map[string]bool
}

func (d denyUnits) Authorize(
	_ context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	if action == identity.ActionUnitRead && d.deny[obj.String()] {
		return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
	}

	return nil
}

// ListObjects gives an administrator every object and any other principal none.
func (denyUnits) ListObjects(_ context.Context, p identity.Principal, _ identity.Action) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

func newBehaviorHandler(
	t *testing.T, nsStore *memNamespaceStore, reader *memReader, authz identity.Authorizer,
) testHandler {
	t.Helper()

	return newBehaviorHandlerWithSessions(t, nsStore, reader, authz, nil)
}

// newBehaviorHandlerWithSessions is newBehaviorHandler with the session
// cookie check sessions; nil takes access tokens only.
func newBehaviorHandlerWithSessions(
	t *testing.T, nsStore *memNamespaceStore, reader *memReader, authz identity.Authorizer,
	sessions *sessionauth.Checker,
) testHandler {
	t.Helper()

	return newBehaviorHandlerWith(t, nsStore, reader, authz, behaviorWiring{
		sessions: sessions, lifecycles: testLifecycles(),
		publicURL: "",
	})
}

// behaviorWiring is the session cookie check and the lifecycle of each tool
// of a behavior handler.
type behaviorWiring struct {
	sessions   *sessionauth.Checker
	lifecycles map[string]coreinventory.Lifecycle
	publicURL  string
}

// newBehaviorHandlerWith is newBehaviorHandlerWithSessions with the
// session check and the lifecycles of wiring.
func newBehaviorHandlerWith(
	t *testing.T, nsStore *memNamespaceStore, reader coreinventory.Reader, authz identity.Authorizer,
	wiring behaviorWiring,
) testHandler {
	t.Helper()

	namespaces, err := namespace.NewService(
		namespace.Config{Store: nsStore, Now: nil, Region: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := newCheckedHandler(t, apiinventory.Config{
		OutputSharing: nil,
		Views:         nil,
		Audit:         newTestAudit(), OnAuditFailure: nil, Locks: nil, Moves: nil,
		Collections:   testCollectionService(t, nsStore),
		Namespaces:    namespaces,
		Units:         reader,
		Deleted:       seededDeleted(),
		History:       nil,
		Lifecycles:    wiring.lifecycles,
		ReadScope:     fixedScope{err: nil, ids: nil, all: true},
		Authenticator: tokenAuthenticator{},
		Authorizer:    authz,
		Sessions:      wiring.sessions,
		PublicURL:     wiring.publicURL,
		Logger:        slog.New(slog.DiscardHandler),
		Now:           nil,
		DenialLimiter: nil,
		StateGuard:    nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

func authorizedGet(
	t *testing.T, handler testHandler, target, token string,
) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

// unitBody is one decoded unit entry.
type unitBody struct {
	Tool          string      `json:"tool"`
	Reference     string      `json:"reference"`
	UnitID        string      `json:"unitID"`
	Namespace     string      `json:"namespace"`
	Name          string      `json:"name"`
	LastChange    string      `json:"lastChange"`
	Status        string      `json:"status"`
	ResourceCount *int64      `json:"resourceCount"`
	Lock          *lockBody   `json:"lock"`
	States        []stateBody `json:"states"`
}

// stateBody is one decoded state of a unit.
type stateBody struct {
	State string `json:"state"`
	Since string `json:"since"`
}

// lockBody is one decoded lock object.
type lockBody struct {
	Holder    string `json:"holder"`
	Since     string `json:"since"`
	Operation string `json:"operation"`
}

// unitsBody is the decoded GET /api/dustpan/v1/namespaces/{namespace}/units response.
type unitsBody struct {
	Units []unitBody `json:"units"`
}

// errorBody is the decoded error response.
type errorBody struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

func decodeUnits(t *testing.T, rec *httptest.ResponseRecorder) unitsBody {
	t.Helper()

	var got unitsBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode units: %v (%s)", err, rec.Body.String())
	}

	if got.Units == nil {
		t.Fatalf("units is null, want an array (%s)", rec.Body.String())
	}

	return got
}

// changeTime is the fixed last-change time of the seeded entries.
func changeTime() time.Time {
	return time.Date(2026, time.October, 9, 12, 0, 0, 0, time.UTC)
}

// seedEntries stores one namespace with a unit of each tool: an OpenTofu
// workspace without status or count, a Pulumi stack with both, and a plain
// Pulumi stack.
func seedEntries(nsStore *memNamespaceStore, reader *memReader) {
	ns := namespace.Name("acme")
	nsStore.namespaces[ns] = namespace.Namespace{
		ID:              namespace.NewID(),
		CreatedAt:       changeTime(),
		Name:            ns,
		HomeRegion:      "test-region",
		FailoverVersion: 1,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	}

	var count int64 = 3

	reader.entries[ns] = []coreinventory.Entry{
		{
			ManualLock:    nil,
			Collection:    "",
			LastChanged:   changeTime(),
			Namespace:     ns,
			Tool:          "pulumi",
			Name:          "web/prod",
			UnitID:        "stk-1",
			DisplayName:   "web/prod",
			Status:        "",
			ResourceCount: nil,
			Lock:          nil,
			Labels:        nil,
		},
		{
			ManualLock:    nil,
			Collection:    "",
			LastChanged:   changeTime(),
			Namespace:     ns,
			Tool:          "opentofu",
			Name:          "network",
			UnitID:        "ws-1",
			DisplayName:   "network",
			Status:        "",
			ResourceCount: nil,
			Lock:          nil,
			Labels:        nil,
		},
		{
			ManualLock:    nil,
			Collection:    "",
			LastChanged:   changeTime(),
			Namespace:     ns,
			Tool:          "pulumi",
			Name:          "api/dev",
			UnitID:        "stk-2",
			DisplayName:   "api/dev",
			Status:        "deployed",
			ResourceCount: &count,
			Lock:          nil,
			Labels:        nil,
		},
	}
}

func newSeededHandler(t *testing.T, authz identity.Authorizer) testHandler {
	t.Helper()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	reader := &memReader{entries: map[namespace.Name][]coreinventory.Entry{}, err: nil}
	seedEntries(nsStore, reader)

	return newBehaviorHandler(t, nsStore, reader, authz)
}

// TestRouteDeclaresNamespaceRead proves the unit list declares exactly
// namespace:read: the wrapper allows the namespace, and the handler filters
// units with unit:read itself.
func TestRouteDeclaresNamespaceRead(t *testing.T) {
	t.Parallel()

	handler := newSeededHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	for _, rt := range handler.Routes() {
		if rt.Method+" "+rt.Path != "GET /api/dustpan/v1/namespaces/{namespace}/units" {
			continue
		}

		if len(rt.Actions) != 1 || rt.Actions[0] != identity.ActionNamespaceRead {
			t.Fatalf("actions = %v, want [namespace:read]", rt.Actions)
		}

		return
	}

	t.Fatal("GET /api/dustpan/v1/namespaces/{namespace}/units is not declared")
}

// TestListUnitsBothTools proves one response holds both tools, ordered by
// unit ID, with the tool labels, references and change times the
// console reads.
func TestListUnitsBothTools(t *testing.T) {
	t.Parallel()

	handler := newSeededHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	rec := authorizedGet(t, handler, "/api/dustpan/v1/namespaces/acme/units", "test-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("list answers %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	got := decodeUnits(t, rec)
	if len(got.Units) != 3 {
		t.Fatalf("units = %+v, want 3", got.Units)
	}

	var count int64 = 3

	want := []unitBody{
		{
			Tool:          "Pulumi",
			Reference:     "pulumi:acme/web/prod",
			UnitID:        "stk-1",
			Namespace:     "acme",
			Name:          "web/prod",
			LastChange:    changeTime().UTC().Format(time.RFC3339Nano),
			Status:        "",
			ResourceCount: nil,
			Lock:          nil,
			States:        []stateBody{},
		},
		{
			Tool:          "Pulumi",
			Reference:     "pulumi:acme/api/dev",
			UnitID:        "stk-2",
			Namespace:     "acme",
			Name:          "api/dev",
			LastChange:    changeTime().UTC().Format(time.RFC3339Nano),
			Status:        "deployed",
			ResourceCount: &count,
			Lock:          nil,
			States:        []stateBody{},
		},
		{
			Tool:          "OpenTofu",
			Reference:     "opentofu:acme/network",
			UnitID:        "ws-1",
			Namespace:     "acme",
			Name:          "network",
			LastChange:    changeTime().UTC().Format(time.RFC3339Nano),
			Status:        "",
			ResourceCount: nil,
			Lock:          nil,
			States:        []stateBody{},
		},
	}

	for i := range want {
		checkUnit(t, got.Units[i], want[i], i)
	}
}

// checkUnit proves one listed unit matches its want: the tool label, the
// reference, the namespace, the name, the change time, the status, the
// resource count and the lock.
func checkUnit(t *testing.T, got, want unitBody, i int) {
	t.Helper()

	if got.Tool != want.Tool ||
		got.Reference != want.Reference ||
		got.UnitID != want.UnitID ||
		got.Namespace != want.Namespace ||
		got.Name != want.Name ||
		got.LastChange != want.LastChange ||
		got.Status != want.Status {
		t.Fatalf("units[%d] = %+v, want %+v", i, got, want)
	}

	checkCount(t, got.ResourceCount, want.ResourceCount, i)
	checkLock(t, got.Lock, want.Lock)
}

// checkLock proves a listed lock matches its want: omitted when the unit is
// not locked, the holder with the take time and the operation otherwise.
func checkLock(t *testing.T, got, want *lockBody) {
	t.Helper()

	switch {
	case want == nil && got != nil:
		t.Fatalf("lock = %+v, want omitted", got)
	case want != nil && got == nil:
		t.Fatalf("lock is omitted, want %+v", want)
	case want != nil && got != nil && *got != *want:
		t.Fatalf("lock = %+v, want %+v", got, want)
	default:
		return
	}
}

// checkCount proves a listed resource count matches its want: omitted when
// unknown, the value otherwise.
func checkCount(t *testing.T, got, want *int64, i int) {
	t.Helper()

	switch {
	case want == nil && got != nil:
		t.Fatalf("units[%d] resourceCount = %d, want omitted", i, *got)
	case want != nil && got == nil:
		t.Fatalf("units[%d] resourceCount is omitted, want %d", i, *want)
	case want != nil && got != nil && *got != *want:
		t.Fatalf("units[%d] resourceCount = %d, want %d", i, *got, *want)
	default:
		return
	}
}

// TestListUnitsOmitsUnknown proves status and resourceCount are absent from
// the JSON when unknown, not empty or null.
func TestListUnitsOmitsUnknown(t *testing.T) {
	t.Parallel()

	handler := newSeededHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	rec := authorizedGet(t, handler, "/api/dustpan/v1/namespaces/acme/units", "test-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("list answers %d, want %d", rec.Code, http.StatusOK)
	}

	var doc struct {
		Units []map[string]any `json:"units"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode units: %v", err)
	}

	byRef := map[string]map[string]any{}
	for _, u := range doc.Units {
		ref, ok := u["reference"].(string)
		if !ok {
			t.Fatalf("entry has no reference string: %v", u)
		}

		byRef[ref] = u
	}

	plain, ok := byRef["pulumi:acme/web/prod"]
	if !ok {
		t.Fatalf("no entry for pulumi:acme/web/prod in %s", rec.Body.String())
	}

	for _, key := range []string{"status", "resourceCount"} {
		if _, present := plain[key]; present {
			t.Fatalf("entry has %q = %v, want it omitted (%s)", key, plain[key], rec.Body.String())
		}
	}

	known, ok := byRef["pulumi:acme/api/dev"]
	if !ok {
		t.Fatalf("no entry for pulumi:acme/api/dev in %s", rec.Body.String())
	}

	if known["status"] != "deployed" {
		t.Fatalf("entry status = %v, want %q", known["status"], "deployed")
	}

	count, ok := known["resourceCount"].(float64)
	if !ok || count != 3 {
		t.Fatalf("entry resourceCount = %v, want 3", known["resourceCount"])
	}
}

// TestListUnitsLockField proves a locked unit answers its lock with the
// holder, the take time in RFC 3339 and the operation.
func TestListUnitsLockField(t *testing.T) {
	t.Parallel()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	seedNamespace(t, nsStore)

	ns := namespace.Name("acme")
	since := changeTime()

	reader := &memReader{
		entries: map[namespace.Name][]coreinventory.Entry{
			ns: {
				{
					ManualLock:    nil,
					Collection:    "",
					LastChanged:   changeTime(),
					Namespace:     ns,
					Tool:          "pulumi",
					Name:          "web/prod",
					UnitID:        "stk-1",
					DisplayName:   "web/prod",
					Status:        "",
					ResourceCount: nil,
					Lock: &coreinventory.Lock{
						Since:     since,
						Holder:    "admin",
						Operation: "update",
					},
					Labels: nil,
				},
			},
		},
		err: nil,
	}

	handler := newBehaviorHandler(t, nsStore, reader, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	rec := authorizedGet(t, handler, "/api/dustpan/v1/namespaces/acme/units", "test-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("list answers %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	got := decodeUnits(t, rec)
	if len(got.Units) != 1 {
		t.Fatalf("units = %+v, want 1", got.Units)
	}

	locked := got.Units[0]
	if locked.Reference != "pulumi:acme/web/prod" {
		t.Fatalf("unit reference = %q, want the locked stack", locked.Reference)
	}

	checkLock(t, locked.Lock, &lockBody{
		Holder: "admin", Since: since.UTC().Format(time.RFC3339Nano), Operation: "update",
	})
}

// TestListUnitsOmitsLock proves an unlocked unit omits the lock field from
// the JSON, instead of answering it empty or null.
func TestListUnitsOmitsLock(t *testing.T) {
	t.Parallel()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	seedNamespace(t, nsStore)

	ns := namespace.Name("acme")

	reader := &memReader{
		entries: map[namespace.Name][]coreinventory.Entry{
			ns: {
				{
					ManualLock:    nil,
					Collection:    "",
					LastChanged:   changeTime(),
					Namespace:     ns,
					Tool:          "opentofu",
					Name:          "network",
					UnitID:        "ws-1",
					DisplayName:   "network",
					Status:        "",
					ResourceCount: nil,
					Lock:          nil,
					Labels:        nil,
				},
			},
		},
		err: nil,
	}

	handler := newBehaviorHandler(t, nsStore, reader, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	rec := authorizedGet(t, handler, "/api/dustpan/v1/namespaces/acme/units", "test-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("list answers %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	var doc struct {
		Units []map[string]any `json:"units"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode units: %v", err)
	}

	if len(doc.Units) != 1 {
		t.Fatalf("units = %s, want one", rec.Body.String())
	}

	if _, present := doc.Units[0]["lock"]; present {
		t.Fatalf("unlocked entry holds a lock (%s)", rec.Body.String())
	}
}

// TestListUnitsLockHoldsNoToken proves the holder answers the principal
// subject, never the token that signed the request in.
func TestListUnitsLockHoldsNoToken(t *testing.T) {
	t.Parallel()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	seedNamespace(t, nsStore)

	ns := namespace.Name("acme")

	reader := &memReader{
		entries: map[namespace.Name][]coreinventory.Entry{
			ns: {
				{
					ManualLock:    nil,
					Collection:    "",
					LastChanged:   changeTime(),
					Namespace:     ns,
					Tool:          "opentofu",
					Name:          "network",
					UnitID:        "ws-1",
					DisplayName:   "network",
					Status:        "",
					ResourceCount: nil,
					Lock: &coreinventory.Lock{
						Since:     changeTime(),
						Holder:    "tester",
						Operation: "lock",
					},
					Labels: nil,
				},
			},
		},
		err: nil,
	}

	handler := newBehaviorHandler(t, nsStore, reader, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	rec := authorizedGet(t, handler, "/api/dustpan/v1/namespaces/acme/units", "test-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("list answers %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	got := decodeUnits(t, rec)
	if len(got.Units) != 1 || got.Units[0].Lock == nil {
		t.Fatalf("units = %+v, want one locked unit", got.Units)
	}

	if holder := got.Units[0].Lock.Holder; holder != "tester" {
		t.Fatalf("lock holder = %q, want the principal subject %q", holder, "tester")
	}

	if strings.Contains(rec.Body.String(), "test-token") {
		t.Fatalf("response holds the request token (%s)", rec.Body.String())
	}
}

// TestListUnitsEmptyNamespace proves a namespace with no units reads as an
// empty array, not null.
func TestListUnitsEmptyNamespace(t *testing.T) {
	t.Parallel()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	seedNamespace(t, nsStore)
	reader := &memReader{entries: map[namespace.Name][]coreinventory.Entry{}, err: nil}
	handler := newBehaviorHandler(t, nsStore, reader, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	rec := authorizedGet(t, handler, "/api/dustpan/v1/namespaces/acme/units", "test-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("list answers %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	if !strings.Contains(rec.Body.String(), `"units":[]`) {
		t.Fatalf("body = %s, want an empty units array", rec.Body.String())
	}
}

// TestListUnitsUnknownNamespace proves an unknown namespace answers 404.
func TestListUnitsUnknownNamespace(t *testing.T) {
	t.Parallel()

	handler := newSeededHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	rec := authorizedGet(t, handler, "/api/dustpan/v1/namespaces/ghost/units", "test-token")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("list answers %d, want %d (%s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}

	var got errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if got.Code != http.StatusNotFound {
		t.Fatalf("error code = %d, want %d", got.Code, http.StatusNotFound)
	}
}

// TestListUnitsInvalidNamespace proves a namespace name in the path that is not
// valid answers 400 with the error shape the console reads.
func TestListUnitsInvalidNamespace(t *testing.T) {
	t.Parallel()

	handler := newSeededHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	for _, name := range []string{"BAD%20NAME", "-x", "a%2Fb"} {
		target := "/api/dustpan/v1/namespaces/" + name + "/units"

		rec := authorizedGet(t, handler, target, "test-token")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s answers %d, want %d (%s)", target, rec.Code, http.StatusBadRequest, rec.Body.String())
		}

		var got errorBody
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode error: %v", err)
		}

		if got.Code != http.StatusBadRequest || got.Message == "" {
			t.Fatalf("error body = %+v, want code %d with a message", got, http.StatusBadRequest)
		}
	}
}

// TestListUnitsFiltersDeniedUnits proves the handler removes entries whose
// unit the caller may not read.
func TestListUnitsFiltersDeniedUnits(t *testing.T) {
	t.Parallel()

	handler := newSeededHandler(t, denyUnits{deny: map[string]bool{"pulumi:acme/web/prod": true}})

	rec := authorizedGet(t, handler, "/api/dustpan/v1/namespaces/acme/units", "test-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("list answers %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	got := decodeUnits(t, rec)
	if len(got.Units) != 2 {
		t.Fatalf("units = %+v, want 2", got.Units)
	}

	for _, u := range got.Units {
		if u.Reference == "pulumi:acme/web/prod" {
			t.Fatalf("units holds the denied reference %q", u.Reference)
		}
	}
}

// TestListUnitsDeniesAllUnits proves a caller that may read the namespace
// but no unit reads an empty list.
func TestListUnitsDeniesAllUnits(t *testing.T) {
	t.Parallel()

	handler := newSeededHandler(t, denyUnits{deny: map[string]bool{
		"opentofu:acme/network": true,
		"pulumi:acme/api/dev":   true,
		"pulumi:acme/web/prod":  true,
	}})

	rec := authorizedGet(t, handler, "/api/dustpan/v1/namespaces/acme/units", "test-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("list answers %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	got := decodeUnits(t, rec)
	if len(got.Units) != 0 {
		t.Fatalf("units = %+v, want none", got.Units)
	}
}

// TestListUnitsNeedsAuthentication proves a request without a token answers 401.
func TestListUnitsNeedsAuthentication(t *testing.T) {
	t.Parallel()

	handler := newSeededHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	rec := authorizedGet(t, handler, "/api/dustpan/v1/namespaces/acme/units", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("list answers %d, want %d (%s)", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

// TestListUnitsDeniedNamespace proves a caller without namespace:read on the
// namespace answers 403.
func TestListUnitsDeniedNamespace(t *testing.T) {
	t.Parallel()

	handler := newSeededHandler(
		t, selectiveAuthorizer{deny: map[identity.Action]bool{identity.ActionNamespaceRead: true}},
	)

	rec := authorizedGet(t, handler, "/api/dustpan/v1/namespaces/acme/units", "test-token")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("list answers %d, want %d (%s)", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

// TestNewHandlerRequiresDependencies proves NewHandler fails without each dependency.
func TestNewHandlerRequiresDependencies(t *testing.T) {
	t.Parallel()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	reader := &memReader{entries: map[namespace.Name][]coreinventory.Entry{}, err: nil}

	namespaces, err := namespace.NewService(
		namespace.Config{Store: nsStore, Now: nil, Region: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	full := apiinventory.Config{
		OutputSharing: nil,
		Views:         nil,
		Audit:         newTestAudit(), OnAuditFailure: nil, Locks: nil, Moves: nil,
		Collections:   testCollectionService(t, nsStore),
		Namespaces:    namespaces,
		Units:         reader,
		Deleted:       seededDeleted(),
		History:       nil,
		Lifecycles:    testLifecycles(),
		ReadScope:     fixedScope{err: nil, ids: nil, all: true},
		Authenticator: tokenAuthenticator{},
		Sessions:      nil,
		Authorizer:    selectiveAuthorizer{deny: map[identity.Action]bool{}},
		Logger:        slog.New(slog.DiscardHandler),
		Now:           nil,
		PublicURL:     "",
		DenialLimiter: nil,
		StateGuard:    nil,
	}

	cases := []struct {
		name string
		drop func(*apiinventory.Config)
	}{
		{name: "namespace service", drop: func(c *apiinventory.Config) { c.Namespaces = nil }},
		{name: "inventory reader", drop: func(c *apiinventory.Config) { c.Units = nil }},
		{name: "deleted unit reader", drop: func(c *apiinventory.Config) { c.Deleted = nil }},
		{name: "unit lifecycles", drop: func(c *apiinventory.Config) { c.Lifecycles = nil }},
		{name: "authenticator", drop: func(c *apiinventory.Config) { c.Authenticator = nil }},
		{name: "authorizer", drop: func(c *apiinventory.Config) { c.Authorizer = nil }},
		{name: "logger", drop: func(c *apiinventory.Config) { c.Logger = nil }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := full
			tc.drop(&cfg)

			if _, err := apiinventory.NewHandler(cfg); err == nil {
				t.Fatalf("NewHandler without %s succeeded", tc.name)
			}
		})
	}
}

// SetOverrides is not used by this fake.
func (*memNamespaceStore) SetOverrides(context.Context, namespace.Name, namespace.Overrides, namespace.Fence) error {
	return nil
}
