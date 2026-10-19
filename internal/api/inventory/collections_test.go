package inventory_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiinventory "opendustpan.org/opendustpan/internal/api/inventory"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// memCollections is an in-memory collection.Store. It holds the units that
// the test declares, so a move can name them.
type memCollections struct {
	collections map[string]collection.Name // key: namespace/name key
	members     map[collection.UnitRef]collection.Name
	units       map[collection.UnitRef]bool
	entries     []audit.Entry
}

func newMemCollections() *memCollections {
	return &memCollections{
		collections: map[string]collection.Name{},
		members:     map[collection.UnitRef]collection.Name{},
		units:       map[collection.UnitRef]bool{},
		entries:     nil,
	}
}

func collectionKey(ns namespace.Name, name collection.Name) string {
	return ns.String() + "/" + name.Key()
}

func (m *memCollections) CreateCollection(
	_ context.Context, c collection.Collection, _ namespace.Fence, e audit.Entry,
) error {
	m.entries = append(m.entries, e)

	key := collectionKey(c.Ref.Namespace, c.Ref.Name)
	if _, ok := m.collections[key]; ok {
		return collection.ErrAlreadyExists
	}

	m.collections[key] = c.Ref.Name

	return nil
}

func (m *memCollections) GetCollection(_ context.Context, ref collection.Ref) (collection.Collection, error) {
	name, ok := m.collections[collectionKey(ref.Namespace, ref.Name)]
	if !ok {
		return collection.Collection{}, collection.ErrNotFound
	}

	return collection.Collection{
		CreatedAt: changeTime(),
		ID:        "col-" + name.Key(),
		Ref:       collection.Ref{Namespace: ref.Namespace, Name: name},
	}, nil
}

func (m *memCollections) ListCollections(_ context.Context, _ namespace.Name) ([]collection.Collection, error) {
	return nil, nil
}

func (m *memCollections) DeleteCollection(
	_ context.Context, ref collection.Ref, _ namespace.Fence, e audit.Entry,
) error {
	m.entries = append(m.entries, e)

	key := collectionKey(ref.Namespace, ref.Name)
	if _, ok := m.collections[key]; !ok {
		return collection.ErrNotFound
	}

	for _, name := range m.members {
		if name.Key() == ref.Name.Key() {
			return collection.ErrNotEmpty
		}
	}

	delete(m.collections, key)

	return nil
}

func (m *memCollections) SetUnitCollection(
	_ context.Context, mv collection.Move, _ namespace.Fence, e audit.Entry,
) error {
	m.entries = append(m.entries, e)

	if !m.units[mv.Unit] {
		return collection.ErrUnitNotFound
	}

	if _, ok := m.collections[collectionKey(mv.To.Namespace, mv.To.Name)]; !ok {
		return collection.ErrNotFound
	}

	if mv.ToID != "col-"+mv.To.Name.Key() {
		return collection.ErrCollectionChanged
	}

	if m.members[mv.Unit].Key() != mv.From.Key() {
		return collection.ErrCollectionChanged
	}

	m.members[mv.Unit] = mv.To.Name

	return nil
}

func (m *memCollections) UnitCollection(_ context.Context, unit collection.UnitRef) (collection.Ref, error) {
	if !m.units[unit] {
		return collection.Ref{}, collection.ErrUnitNotFound
	}

	name, ok := m.members[unit]
	if !ok {
		return collection.Ref{}, collection.ErrNoCollection
	}

	return collection.Ref{Namespace: unit.Namespace, Name: name}, nil
}

// testCollectionService returns a collection service over an in-memory store.
// It serves the collection routes of a handler under test.
func testCollectionService(t *testing.T, ns namespace.Store) *collection.Service {
	t.Helper()

	svc, err := collection.NewService(collection.Config{Store: newMemCollections(), Namespaces: ns, Now: nil})
	if err != nil {
		t.Fatalf("collection service: %v", err)
	}

	return svc
}

// objectDenier denies one action on one object and allows the rest.
type objectDenier struct {
	action identity.Action
	object string
}

func (d objectDenier) Authorize(
	_ context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	if action == d.action && obj.String() == d.object {
		return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
	}

	return nil
}

// ListObjects gives an administrator every object and any other principal none.
func (objectDenier) ListObjects(_ context.Context, p identity.Principal, _ identity.Action) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

// moveFixture is a namespace with two collections and one unit in "old".
type moveFixture struct {
	handler  http.Handler
	store    *memCollections
	unitRef  collection.UnitRef
	unitText string
}

func newMoveFixture(t *testing.T, authz identity.Authorizer) moveFixture {
	t.Helper()
	return newMoveFixtureWith(t, authz, allowMoves{})
}

// newMoveFixtureWith is newMoveFixture with the move checker moves.
func newMoveFixtureWith(t *testing.T, authz identity.Authorizer, moves apiinventory.MoveChecker) moveFixture {
	t.Helper()

	return newMoveFixtureWithSessions(t, authz, moves, nil)
}

// newMoveFixtureWithSessions is newMoveFixtureWith with the session cookie
// check sessions.
func newMoveFixtureWithSessions(
	t *testing.T, authz identity.Authorizer, moves apiinventory.MoveChecker, sessions *sessionauth.Checker,
) moveFixture {
	t.Helper()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	seedNamespace(t, nsStore)

	store := newMemCollections()
	unit := collection.UnitRef{Namespace: "acme", Tool: "pulumi", Name: "web/prod"}
	store.units[unit] = true

	for _, name := range []string{"old", "new"} {
		store.collections[collectionKey("acme", collection.Name(name))] = collection.Name(name)
	}

	store.members[unit] = "old"

	svc, err := collection.NewService(collection.Config{Store: store, Namespaces: nsStore, Now: nil})
	if err != nil {
		t.Fatalf("collection service: %v", err)
	}

	nsSvc, err := namespace.NewService(namespace.Config{Store: nsStore, Now: nil, Region: "test-region"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := apiinventory.NewHandler(apiinventory.Config{
		PublicURL:     "",
		Namespaces:    nsSvc,
		Units:         &memReader{entries: map[namespace.Name][]coreinventory.Entry{}, err: nil},
		Collections:   svc,
		Moves:         moves,
		Deleted:       &memDeleted{entries: nil},
		History:       nil,
		Lifecycles:    testLifecycles(),
		ReadScope:     fixedScope{err: nil, ids: nil, all: true},
		Now:           nil,
		Authenticator: tokenAuthenticator{},
		Authorizer:    authz,
		Sessions:      sessions,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("inventory handler: %v", err)
	}

	return moveFixture{handler: handler, store: store, unitRef: unit, unitText: unit.String()}
}

// move sends the unit move of the fixture to the collection target.
func (f moveFixture) move(t *testing.T, target string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPut,
		"/api/inventory/unit-collection?unit="+f.unitText,
		strings.NewReader(`{"collection":"`+target+`"}`),
	)
	req.Header.Set("Authorization", "token test-token")

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	return rec
}

// TestUnitMoveNeedsTheUnitAndBothCollections proves a move checks the unit
// (unit:move, unit:manage-access), the collection it enters (collection:create-unit,
// collection:manage-access) and the collection it leaves (collection:manage-access). A denied check stops the move, and
// the unit
// keeps its collection (ADR 61, decision 4).
func TestUnitMoveNeedsTheUnitAndBothCollections(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		authz  identity.Authorizer
		want   int
		wantIn string
	}{
		{
			name:   "allowed on every check",
			authz:  selectiveAuthorizer{deny: nil},
			want:   http.StatusOK,
			wantIn: "new",
		},
		{
			name:   "denied create-unit on the target collection",
			authz:  objectDenier{action: identity.ActionCollectionCreateUnit, object: "acme/new"},
			want:   http.StatusForbidden,
			wantIn: "old",
		},
		{
			name:   "denied manage-access on the target collection",
			authz:  objectDenier{action: identity.ActionCollectionManageAccess, object: "acme/new"},
			want:   http.StatusForbidden,
			wantIn: "old",
		},
		{
			name:   "denied manage-access on the collection the unit leaves",
			authz:  objectDenier{action: identity.ActionCollectionManageAccess, object: "acme/old"},
			want:   http.StatusForbidden,
			wantIn: "old",
		},
		{
			name:   "denied manage-access on the unit",
			authz:  objectDenier{action: identity.ActionUnitManageAccess, object: "pulumi:acme/web/prod"},
			want:   http.StatusForbidden,
			wantIn: "old",
		},
		{
			name:   "denied unit:move on the unit",
			authz:  objectDenier{action: identity.ActionUnitMove, object: "pulumi:acme/web/prod"},
			want:   http.StatusForbidden,
			wantIn: "old",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newMoveFixture(t, tc.authz)

			rec := f.move(t, "new")
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}

			got, err := f.store.UnitCollection(t.Context(), f.unitRef)
			if err != nil {
				t.Fatalf("UnitCollection: %v", err)
			}

			if got.Name.String() != tc.wantIn {
				t.Errorf("unit is in %q, want %q", got.Name, tc.wantIn)
			}
		})
	}
}

// TestUnitMoveWithoutCollectionChecksOnlyTheTarget proves a unit with no
// collection moves in after the check of the target only: it has no old
// collection to check.
func TestUnitMoveWithoutCollectionChecksOnlyTheTarget(t *testing.T) {
	t.Parallel()

	f := newMoveFixture(t, objectDenier{action: identity.ActionCollectionManageAccess, object: "acme/old"})
	f.store.members = map[collection.UnitRef]collection.Name{}

	rec := f.move(t, "new")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	if !strings.Contains(rec.Body.String(), `"previousCollection":""`) {
		t.Errorf("answer lacks the empty previous collection: %s", rec.Body.String())
	}

	got, err := f.store.UnitCollection(t.Context(), f.unitRef)
	if err != nil {
		t.Fatalf("UnitCollection: %v", err)
	}

	if got.Name != "new" {
		t.Errorf("unit is in %q, want %q", got.Name, "new")
	}
}

// allowMoves is a move checker that allows every move. The escalation check
// of a move is tested with the access service, so these tests check the
// handler alone.
type allowMoves struct{}

func (allowMoves) CheckMove(
	_ context.Context, _ identity.Principal, _, target identity.Object,
) (string, error) {
	return "col-" + strings.ToLower(target.CollectionName()), nil
}

// denyMoves refuses every move, as the escalation check does when the caller
// holds less on the unit than the target collection gives.
type denyMoves struct{}

func (denyMoves) CheckMove(
	_ context.Context, _ identity.Principal, unit, _ identity.Object,
) (string, error) {
	return "", fmt.Errorf("move %s: %w", unit.String(), identity.ErrDenied)
}

// TestMoveRefusedByTheEscalationCheckAnswersForbidden checks that a move the
// move checker refuses is not made, and answers 403.
func TestMoveRefusedByTheEscalationCheckAnswersForbidden(t *testing.T) {
	t.Parallel()

	f := newMoveFixtureWith(t, objectDenier{action: "", object: ""}, denyMoves{})
	f.store.members = map[collection.UnitRef]collection.Name{}

	rec := f.move(t, "new")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}

	got, err := f.store.UnitCollection(t.Context(), f.unitRef)
	if !errors.Is(err, collection.ErrNoCollection) {
		t.Fatalf("UnitCollection = %q, %v; want no collection: a refused move makes no change", got.Name, err)
	}
}

// TestMoveIntoMissingCollectionIsForbiddenBeforeNotFound checks that a caller
// who may not create units in a collection gets 403 for a move into a name
// that does not exist, so the answer does not tell which names exist.
func TestMoveIntoMissingCollectionIsForbiddenBeforeNotFound(t *testing.T) {
	t.Parallel()

	f := newMoveFixtureWith(t,
		objectDenier{action: identity.ActionCollectionCreateUnit, object: "acme/missing"}, allowMoves{})
	f.store.members = map[collection.UnitRef]collection.Name{}

	rec := f.move(t, "missing")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 before any existence check: %s", rec.Code, rec.Body.String())
	}
}
