package inventory_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apiinventory "opendustpan.org/opendustpan/internal/api/inventory"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// idMoves is a MoveChecker that answers the stable ID of the target collection.
type idMoves map[string]string

func (m idMoves) CheckMove(
	_ context.Context, _ identity.Principal, _, target identity.Object,
) (string, error) {
	return m[target.CollectionName()], nil
}

// storeMoveFixture is a real store with the unit "web" in the collection "old",
// a second collection "new", and a manual lock on the unit and on "old".
type storeMoveFixture struct {
	store *sqlite.Store
	ns    namespace.Namespace
	unit  collection.UnitRef
	ids   idMoves
}

// TestMoveOutOfLockedCollectionWithUnitLock proves, over a real store, that a
// unit with its own manual lock inside a locked collection still needs
// unit:manage to leave the collection: the unit lock must not hide the lock of
// the collection that the move ends.
func TestMoveOutOfLockedCollectionWithUnitLock(t *testing.T) {
	t.Parallel()

	t.Run("caller without unit:manage", func(t *testing.T) {
		t.Parallel()

		f := newStoreMoveFixture(t)
		rec := f.move(t, objectDenier{action: identity.ActionUnitManage, object: "opentofu:acme/web"})

		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "collection") {
			t.Fatalf("status = %d, body = %s; want 409 that names the collection lock", rec.Code, rec.Body.String())
		}

		if got := f.collectionOfUnit(t); got != "old" {
			t.Errorf("unit collection = %q after a refused move, want old", got)
		}

		f.wantOneMoveOutRefusal(t)
	})

	t.Run("caller with unit:manage", func(t *testing.T) {
		t.Parallel()

		f := newStoreMoveFixture(t)
		rec := f.move(t, objectDenier{action: "", object: ""})

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s; want 200", rec.Code, rec.Body.String())
		}

		if got := f.collectionOfUnit(t); got != "new" {
			t.Errorf("unit collection = %q after the move, want new", got)
		}
	})
}

// wantOneMoveOutRefusal checks that the namespace audit log holds one denied
// entry of unit:manage on the unit, with the reason of the move-out refusal and
// not the generic policy reason.
func (f storeMoveFixture) wantOneMoveOutRefusal(t *testing.T) {
	t.Helper()

	l, err := f.store.List(t.Context(), f.ns.Name, audit.Page{Number: 1, Size: 100})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}

	var reasons []string

	for i := range l.Entries {
		if e := &l.Entries[i]; e.Outcome == audit.OutcomeDenied {
			reasons = append(reasons, e.Action+": "+e.Reason)
		}
	}

	const want = "unit:manage: move out of a locked collection needs unit:manage"
	if len(reasons) != 1 || !strings.HasPrefix(reasons[0], want) {
		t.Errorf("denied audit entries = %q, want one that starts with %q", reasons, want)
	}
}

// collectionOfUnit returns the name of the collection that holds the unit.
func (f storeMoveFixture) collectionOfUnit(t *testing.T) collection.Name {
	t.Helper()

	ref, err := f.store.UnitCollection(t.Context(), f.unit)
	if err != nil {
		t.Fatalf("unit collection: %v", err)
	}

	return ref.Name
}

func newStoreMoveFixture(t *testing.T) storeMoveFixture {
	t.Helper()

	ctx := t.Context()

	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.Now(), Name: "acme", PrimaryInstallation: "test-region",
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if createErr := store.CreateNamespace(ctx, ns); createErr != nil {
		t.Fatalf("create namespace: %v", createErr)
	}

	f := storeMoveFixture{
		store: store, ns: ns, ids: idMoves{},
		unit: collection.UnitRef{Namespace: ns.Name, Tool: workspace.InventoryTool, Name: "web"},
	}
	f.createWorkspace(t)

	refs := f.createCollections(t, "old", "new")

	put := collection.Move{Unit: f.unit, From: "", To: refs[0], ToID: f.ids["old"], EndsLock: true}
	if putErr := store.SetUnitCollection(ctx, put, ns.Fence(), storeAudit(ns)); putErr != nil {
		t.Fatalf("put unit in old: %v", putErr)
	}

	for _, target := range []manuallock.Target{manuallock.CollectionTarget(refs[0]), manuallock.UnitTarget(f.unit)} {
		l := manuallock.Lock{Since: time.Now(), Target: target, Holder: "alice", Reason: "freeze"}
		if putErr := store.PutLock(ctx, l, ns.Fence(), storeAudit(ns)); putErr != nil {
			t.Fatalf("lock %s: %v", target, putErr)
		}
	}

	return f
}

// createWorkspace makes the workspace of the fixture, which is its unit.
func (f storeMoveFixture) createWorkspace(t *testing.T) {
	t.Helper()

	name, err := workspace.ParseName(f.unit.Name)
	if err != nil {
		t.Fatalf("parse workspace name: %v", err)
	}

	w := workspace.Workspace{
		NamespaceID: "",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Lock:        workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease: workspace.NoRelease(),
		ID:          workspace.ID(ident.NewID("ws-")),
		Ref:         workspace.Ref{Namespace: f.ns.Name, Name: name},
		Revision:    1,
		Overrides:   namespace.Overrides{Forced: false, Unlocked: false},
		Origin:      identity.StateOrigin{Job: "", Commit: ""},
	}
	if createErr := f.store.CreateWorkspace(t.Context(), w, f.ns.Fence()); createErr != nil {
		t.Fatalf("create workspace: %v", createErr)
	}
}

// createCollections makes the collections called names and records their IDs.
func (f storeMoveFixture) createCollections(t *testing.T, names ...string) []collection.Ref {
	t.Helper()

	refs := make([]collection.Ref, 0, len(names))

	for _, n := range names {
		cn, err := collection.ParseName(n)
		if err != nil {
			t.Fatalf("parse collection name: %v", err)
		}

		c := collection.Collection{
			CreatedAt: time.Now(), ID: ident.NewID("col-"), Ref: collection.Ref{Namespace: f.ns.Name, Name: cn},
		}
		if createErr := f.store.CreateCollection(t.Context(), c, f.ns.Fence(), storeAudit(f.ns)); createErr != nil {
			t.Fatalf("create collection %s: %v", n, createErr)
		}

		f.ids[cn.String()] = c.ID
		refs = append(refs, c.Ref)
	}

	return refs
}

// storeAudit returns a valid audit entry for a write in ns.
func storeAudit(ns namespace.Namespace) audit.Entry {
	return audit.Entry{
		At: time.Now(), Namespace: ns.Name, Subject: "tester", IDPSubject: "", TokenID: "",
		Action: "test:write", Object: ns.Name.String(), Version: "", ValuePath: "", ValueCount: nil,
		SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", PrevHash: "", Hash: "", Sequence: 0, Changes: nil,
		ObjectID: "", FenceVersion: ns.FailoverVersion, Reason: "", PrincipalKind: "", JobID: "",
	}
}

// move sends the move of the unit to the collection new over a handler that
// reads its collections, namespaces and manual locks from the store.
func (f storeMoveFixture) move(t *testing.T, authz identity.Authorizer) *httptest.ResponseRecorder {
	t.Helper()

	nsSvc, err := namespace.NewService(namespace.Config{Store: f.store, Now: nil, Installation: "test-region"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	colSvc, err := collection.NewService(collection.Config{Store: f.store, Namespaces: f.store, Now: nil})
	if err != nil {
		t.Fatalf("collection service: %v", err)
	}

	locks, err := manuallock.NewService(manuallock.Config{
		Store: f.store, Namespaces: f.store, Authorizer: authz, Phases: nil, Now: nil,
	})
	if err != nil {
		t.Fatalf("manual lock service: %v", err)
	}

	handler, err := newCheckedHandler(t, apiinventory.Config{
		Placements:    nil,
		OutputSharing: nil,
		Outputs:       nil,
		Views:         nil,
		Locks:         locks, PublicURL: "", Namespaces: nsSvc,
		Units:       &memReader{entries: map[namespace.Name][]coreinventory.Entry{}, err: nil},
		Collections: colSvc, Moves: f.ids, Deleted: &memDeleted{entries: nil}, History: nil,
		Lifecycles: testLifecycles(), ReadScope: fixedScope{err: nil, ids: nil, all: true}, Now: nil,
		Authenticator: tokenAuthenticator{}, Authorizer: authz, Audit: f.store, OnAuditFailure: nil,
		DenialLimiter: nil, Sessions: nil,
		Logger:     slog.New(slog.DiscardHandler),
		StateGuard: stategate.Off("the test keeps no gates"),
	})
	if err != nil {
		t.Fatalf("inventory handler: %v", err)
	}

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPut, "/api/inventory/unit-collection?unit="+f.unit.String(),
		strings.NewReader(`{"collection":"new"}`),
	)
	req.Header.Set("Authorization", "token test-token")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}
