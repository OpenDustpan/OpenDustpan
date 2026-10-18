package secrettest

import (
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// ItemKeyStore is every persistence port that RunItemKeys exercises:
// namespaces for the fence, and secret.ItemKeyIndex under test.
type ItemKeyStore interface {
	namespace.Store
	secret.ItemKeyIndex
}

// ItemKeyFactory returns a fresh, empty store for one subtest.
type ItemKeyFactory func(t *testing.T) ItemKeyStore

// RunItemKeys runs the conformance suite for secret.ItemKeyIndex
// against the stores that newStore returns:
//
//	func TestItemKeyConformance(t *testing.T) {
//		t.Parallel()
//		secrettest.RunItemKeys(t, func(t *testing.T) secrettest.ItemKeyStore { return newMyStore(t) })
//	}
func RunItemKeys(t *testing.T, newStore ItemKeyFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s ItemKeyStore)
		name string
	}{
		{name: "create then get returns the same record", test: testItemKeyCreateGet},
		{name: "a second create returns the first record", test: testItemKeyCreateTwice},
		{name: "get of a missing record is ErrKeyNotFound", test: testItemKeyMissing},
		{name: "kinds and items of one scope have their own keys", test: testItemKeyDistinctRefs},
		{name: "shred marks the record and get answers ErrShredded", test: testItemKeyShred},
		{name: "a second shred keeps the first shred time", test: testItemKeyShredTwice},
		{name: "a create after a shred is ErrShredded", test: testItemKeyCreateAfterShred},
		{name: "shred of a missing record is ErrKeyNotFound", test: testItemKeyShredMissing},
		{name: "a stale fence refuses create and shred", test: testItemKeyFenced},
		{name: "list pages the records of one scope by ID", test: testItemKeyList},
		{name: "wrapper IDs leave out shredded records", test: testItemKeyWrapperIDs},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// itemKeyTime() returns the fixed creation time of the records of the suite.
func itemKeyTime() time.Time {
	return time.Date(2026, time.October, 17, 9, 0, 0, 0, time.UTC)
}

// itemKeyFenceVersion is the failover version of the namespace of the
// suite. A fence with a lower version is stale.
const itemKeyFenceVersion = 2

// itemKeyNamespace creates the namespace "acme" and returns its fence.
func itemKeyNamespace(t *testing.T, s ItemKeyStore) namespace.Fence {
	t.Helper()

	ns := namespace.Namespace{
		ID:              namespace.NewID(),
		CreatedAt:       itemKeyTime(),
		Name:            "acme",
		HomeRegion:      "local",
		FailoverVersion: itemKeyFenceVersion,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	return namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion}
}

// itemRef returns a valid reference in the namespace "acme".
func itemRef(t *testing.T, scopeID string, kind secret.ItemKind, item string) secret.ItemRef {
	t.Helper()

	scope, err := secret.NewScope("acme", scopeID)
	if err != nil {
		t.Fatalf("scope: %v", err)
	}

	ref, err := secret.NewItemRef(scope, kind, item)
	if err != nil {
		t.Fatalf("item ref: %v", err)
	}

	return ref
}

// newItemKey returns a record of ref that is not stored yet.
func newItemKey(ref secret.ItemRef, wrapperID string) secret.ItemKey {
	return secret.ItemKey{
		CreatedAt:  itemKeyTime(),
		ShreddedAt: time.Time{},
		Ref:        ref,
		ID:         secret.NewItemKeyID(),
		WrapperID:  wrapperID,
	}
}

// mustCreateItemKey stores a record of ref and returns it.
func mustCreateItemKey(
	t *testing.T, s ItemKeyStore, fence namespace.Fence, ref secret.ItemRef, wrapperID string,
) secret.ItemKey {
	t.Helper()

	got, err := s.CreateItemKey(t.Context(), newItemKey(ref, wrapperID), fence)
	if err != nil {
		t.Fatalf("create item key %s: %v", ref, err)
	}

	return got
}

// sameItemKey compares two records by their fields, with times compared
// as instants.
func sameItemKey(a, b secret.ItemKey) bool {
	return a.ID == b.ID && a.Ref == b.Ref && a.WrapperID == b.WrapperID &&
		a.CreatedAt.Equal(b.CreatedAt) && a.ShreddedAt.Equal(b.ShreddedAt)
}

func testItemKeyCreateGet(t *testing.T, s ItemKeyStore) {
	t.Helper()
	fence := itemKeyNamespace(t, s)
	ref := itemRef(t, "stk-one", secret.ItemKindState, "upd-1")

	created := mustCreateItemKey(t, s, fence, ref, "builtin-1")
	if created.Shredded() {
		t.Fatal("a new record is shredded")
	}

	got, err := s.GetItemKey(t.Context(), ref)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if !sameItemKey(got, created) {
		t.Errorf("get = %+v, want %+v", got, created)
	}
}

func testItemKeyCreateTwice(t *testing.T, s ItemKeyStore) {
	t.Helper()
	fence := itemKeyNamespace(t, s)
	ref := itemRef(t, "stk-one", secret.ItemKindState, "upd-1")

	first := mustCreateItemKey(t, s, fence, ref, "builtin-1")

	second, err := s.CreateItemKey(t.Context(), newItemKey(ref, "builtin-1"), fence)
	if err != nil {
		t.Fatalf("second create: %v", err)
	}

	if second.ID != first.ID {
		t.Errorf("second create ID = %s, want the first ID %s", second.ID, first.ID)
	}
}

func testItemKeyMissing(t *testing.T, s ItemKeyStore) {
	t.Helper()
	itemKeyNamespace(t, s)

	_, err := s.GetItemKey(t.Context(), itemRef(t, "stk-one", secret.ItemKindState, "upd-1"))
	if !errors.Is(err, secret.ErrKeyNotFound) {
		t.Errorf("get missing err = %v, want ErrKeyNotFound", err)
	}
}

func testItemKeyDistinctRefs(t *testing.T, s ItemKeyStore) {
	t.Helper()
	fence := itemKeyNamespace(t, s)

	refs := []secret.ItemRef{
		itemRef(t, "ws-one", secret.ItemKindState, "sv-1"),
		itemRef(t, "ws-one", secret.ItemKindState, "sv-2"),
		itemRef(t, "ws-one", secret.ItemKindPlan, "sv-1"),
		itemRef(t, "ws-one", secret.ItemKindJobLog, "sv-1"),
		itemRef(t, "ws-two", secret.ItemKindState, "sv-1"),
	}

	seen := map[secret.ItemKeyID]bool{}

	for _, ref := range refs {
		k := mustCreateItemKey(t, s, fence, ref, "builtin-1")
		if seen[k.ID] {
			t.Errorf("%s shares the key %s with another reference", ref, k.ID)
		}

		seen[k.ID] = true
	}
}

func testItemKeyShred(t *testing.T, s ItemKeyStore) {
	t.Helper()
	fence := itemKeyNamespace(t, s)
	ref := itemRef(t, "stk-one", secret.ItemKindState, "upd-1")
	created := mustCreateItemKey(t, s, fence, ref, "builtin-1")
	at := itemKeyTime().Add(time.Hour)

	shredded, err := s.ShredItemKey(t.Context(), ref, at, fence)
	if err != nil {
		t.Fatalf("shred: %v", err)
	}

	if shredded.ID != created.ID || !shredded.ShreddedAt.Equal(at) {
		t.Errorf("shred = %+v, want ID %s shredded at %v", shredded, created.ID, at)
	}

	got, err := s.GetItemKey(t.Context(), ref)
	if !errors.Is(err, secret.ErrShredded) {
		t.Fatalf("get after shred err = %v, want ErrShredded", err)
	}

	if got.ID != created.ID || !got.ShreddedAt.Equal(at) {
		t.Errorf("get after shred = %+v, want the shredded record", got)
	}
}

func testItemKeyShredTwice(t *testing.T, s ItemKeyStore) {
	t.Helper()
	fence := itemKeyNamespace(t, s)
	ref := itemRef(t, "stk-one", secret.ItemKindState, "upd-1")
	mustCreateItemKey(t, s, fence, ref, "builtin-1")
	first := itemKeyTime().Add(time.Hour)

	if _, err := s.ShredItemKey(t.Context(), ref, first, fence); err != nil {
		t.Fatalf("first shred: %v", err)
	}

	again, err := s.ShredItemKey(t.Context(), ref, first.Add(time.Hour), fence)
	if err != nil {
		t.Fatalf("second shred: %v", err)
	}

	if !again.ShreddedAt.Equal(first) {
		t.Errorf("second shred time = %v, want the first %v", again.ShreddedAt, first)
	}
}

func testItemKeyCreateAfterShred(t *testing.T, s ItemKeyStore) {
	t.Helper()
	fence := itemKeyNamespace(t, s)
	ref := itemRef(t, "stk-one", secret.ItemKindState, "upd-1")
	created := mustCreateItemKey(t, s, fence, ref, "builtin-1")

	if _, err := s.ShredItemKey(t.Context(), ref, itemKeyTime().Add(time.Hour), fence); err != nil {
		t.Fatalf("shred: %v", err)
	}

	got, err := s.CreateItemKey(t.Context(), newItemKey(ref, "builtin-1"), fence)
	if !errors.Is(err, secret.ErrShredded) {
		t.Fatalf("create after shred err = %v, want ErrShredded", err)
	}

	if got.ID != created.ID {
		t.Errorf("create after shred ID = %s, want the shredded record %s", got.ID, created.ID)
	}
}

func testItemKeyShredMissing(t *testing.T, s ItemKeyStore) {
	t.Helper()
	fence := itemKeyNamespace(t, s)

	_, err := s.ShredItemKey(
		t.Context(),
		itemRef(t, "stk-one", secret.ItemKindState, "upd-1"),
		itemKeyTime(),
		fence,
	)
	if !errors.Is(err, secret.ErrKeyNotFound) {
		t.Errorf("shred missing err = %v, want ErrKeyNotFound", err)
	}
}

func testItemKeyFenced(t *testing.T, s ItemKeyStore) {
	t.Helper()
	fence := itemKeyNamespace(t, s)
	stale := namespace.Fence{Namespace: fence.Namespace, FailoverVersion: fence.FailoverVersion - 1}
	ref := itemRef(t, "stk-one", secret.ItemKindState, "upd-1")

	_, err := s.CreateItemKey(
		t.Context(),
		newItemKey(ref, "builtin-1"),
		stale,
	)
	if !errors.Is(
		err,
		namespace.ErrFenced,
	) {
		t.Errorf("create with a stale fence err = %v, want ErrFenced", err)
	}

	if _, err := s.GetItemKey(t.Context(), ref); !errors.Is(err, secret.ErrKeyNotFound) {
		t.Errorf("a fenced create left a record: err = %v", err)
	}

	mustCreateItemKey(t, s, fence, ref, "builtin-1")

	if _, err := s.ShredItemKey(t.Context(), ref, itemKeyTime(), stale); !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("shred with a stale fence err = %v, want ErrFenced", err)
	}

	if _, err := s.GetItemKey(t.Context(), ref); err != nil {
		t.Errorf("a fenced shred changed the record: err = %v", err)
	}
}

func testItemKeyList(t *testing.T, s ItemKeyStore) {
	t.Helper()
	fence := itemKeyNamespace(t, s)

	want := make([]secret.ItemKeyID, 0, 3)

	for _, item := range []string{"sv-1", "sv-2", "sv-3"} {
		want = append(
			want,
			mustCreateItemKey(t, s, fence, itemRef(t, "ws-one", secret.ItemKindState, item), "builtin-1").ID,
		)
	}

	mustCreateItemKey(t, s, fence, itemRef(t, "ws-two", secret.ItemKindState, "sv-1"), "builtin-1")

	_, err := s.ShredItemKey(
		t.Context(),
		itemRef(t, "ws-one", secret.ItemKindState, "sv-2"),
		itemKeyTime(),
		fence,
	)
	if err != nil {
		t.Fatalf("shred: %v", err)
	}

	slices.Sort(want)

	scope, err := secret.NewScope("acme", "ws-one")
	if err != nil {
		t.Fatalf("scope: %v", err)
	}

	first, err := s.ListItemKeys(t.Context(), scope, "", 2)
	if err != nil {
		t.Fatalf("list first page: %v", err)
	}

	rest, err := s.ListItemKeys(t.Context(), scope, first[len(first)-1].ID, 2)
	if err != nil {
		t.Fatalf("list second page: %v", err)
	}

	items := slices.Concat(first, rest)
	got := make([]secret.ItemKeyID, 0, len(items))
	for i := range items {
		k := items[i]
		got = append(got, k.ID)
	}

	if !slices.Equal(got, want) {
		t.Errorf("listed IDs = %v, want %v (shredded records included, other scopes left out)", got, want)
	}
}

func testItemKeyWrapperIDs(t *testing.T, s ItemKeyStore) {
	t.Helper()
	fence := itemKeyNamespace(t, s)

	empty, err := s.ListItemKeyWrapperIDs(t.Context())
	if err != nil || len(empty) != 0 {
		t.Fatalf("wrapper IDs of an empty store = %v, %v, want none", empty, err)
	}

	mustCreateItemKey(t, s, fence, itemRef(t, "ws-one", secret.ItemKindState, "sv-1"), "builtin-b")
	mustCreateItemKey(t, s, fence, itemRef(t, "ws-one", secret.ItemKindState, "sv-2"), "builtin-a")
	mustCreateItemKey(t, s, fence, itemRef(t, "ws-one", secret.ItemKindPlan, "pln-1"), "builtin-a")
	gone := itemRef(t, "ws-one", secret.ItemKindJobLog, "job-1")
	mustCreateItemKey(t, s, fence, gone, "builtin-old")

	if _, shredErr := s.ShredItemKey(t.Context(), gone, itemKeyTime(), fence); shredErr != nil {
		t.Fatalf("shred: %v", shredErr)
	}

	got, err := s.ListItemKeyWrapperIDs(t.Context())
	if err != nil {
		t.Fatalf("wrapper IDs: %v", err)
	}

	if want := []string{"builtin-a", "builtin-b"}; !slices.Equal(got, want) {
		t.Errorf("wrapper IDs = %v, want %v", got, want)
	}
}
