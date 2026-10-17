package secrettest

import (
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// ContentKeyStore is every persistence port that RunContentKeys exercises:
// namespaces for the fence, and secret.ContentKeyIndex under test.
type ContentKeyStore interface {
	namespace.Store
	secret.ContentKeyIndex
}

// ContentKeyFactory returns a fresh, empty store for one subtest.
type ContentKeyFactory func(t *testing.T) ContentKeyStore

// RunContentKeys runs the conformance suite for secret.ContentKeyIndex
// against the stores that newStore returns:
//
//	func TestContentKeyConformance(t *testing.T) {
//		t.Parallel()
//		secrettest.RunContentKeys(t, func(t *testing.T) secrettest.ContentKeyStore { return newMyStore(t) })
//	}
func RunContentKeys(t *testing.T, newStore ContentKeyFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s ContentKeyStore)
		name string
	}{
		{name: "create then get returns the same record", test: testContentCreateGet},
		{name: "a second create returns the first record", test: testContentCreateTwice},
		{name: "get of a missing record is ErrKeyNotFound", test: testContentMissing},
		{name: "kinds and items of one scope have their own keys", test: testContentDistinctRefs},
		{name: "shred marks the record and get answers ErrShredded", test: testContentShred},
		{name: "a second shred keeps the first shred time", test: testContentShredTwice},
		{name: "a create after a shred is ErrShredded", test: testContentCreateAfterShred},
		{name: "shred of a missing record is ErrKeyNotFound", test: testContentShredMissing},
		{name: "a stale fence refuses create and shred", test: testContentFenced},
		{name: "list pages the records of one scope by ID", test: testContentList},
		{name: "wrapper IDs leave out shredded records", test: testContentWrapperIDs},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// contentTime() returns the fixed creation time of the records of the suite.
func contentTime() time.Time {
	return time.Date(2026, time.October, 17, 9, 0, 0, 0, time.UTC)
}

// contentFenceVersion is the failover version of the namespace of the
// suite. A fence with a lower version is stale.
const contentFenceVersion = 2

// contentNamespace creates the namespace "acme" and returns its fence.
func contentNamespace(t *testing.T, s ContentKeyStore) namespace.Fence {
	t.Helper()

	ns := namespace.Namespace{
		CreatedAt:       contentTime(),
		Name:            "acme",
		HomeRegion:      "local",
		FailoverVersion: contentFenceVersion,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	return namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion}
}

// contentRef returns a valid reference in the namespace "acme".
func contentRef(t *testing.T, scopeID string, kind secret.ContentKind, item string) secret.ContentRef {
	t.Helper()

	scope, err := secret.NewScope("acme", scopeID)
	if err != nil {
		t.Fatalf("scope: %v", err)
	}

	ref, err := secret.NewContentRef(scope, kind, item)
	if err != nil {
		t.Fatalf("content ref: %v", err)
	}

	return ref
}

// newContentKey returns a record of ref that is not stored yet.
func newContentKey(ref secret.ContentRef, wrapperID string) secret.ContentKey {
	return secret.ContentKey{
		CreatedAt:  contentTime(),
		ShreddedAt: time.Time{},
		Ref:        ref,
		ID:         secret.NewContentKeyID(),
		WrapperID:  wrapperID,
	}
}

// mustCreateContentKey stores a record of ref and returns it.
func mustCreateContentKey(
	t *testing.T, s ContentKeyStore, fence namespace.Fence, ref secret.ContentRef, wrapperID string,
) secret.ContentKey {
	t.Helper()

	got, err := s.CreateContentKey(t.Context(), newContentKey(ref, wrapperID), fence)
	if err != nil {
		t.Fatalf("create content key %s: %v", ref, err)
	}

	return got
}

// sameContentKey compares two records by their fields, with times compared
// as instants.
func sameContentKey(a, b secret.ContentKey) bool {
	return a.ID == b.ID && a.Ref == b.Ref && a.WrapperID == b.WrapperID &&
		a.CreatedAt.Equal(b.CreatedAt) && a.ShreddedAt.Equal(b.ShreddedAt)
}

func testContentCreateGet(t *testing.T, s ContentKeyStore) {
	t.Helper()
	fence := contentNamespace(t, s)
	ref := contentRef(t, "stk-one", secret.ContentKindState, "upd-1")

	created := mustCreateContentKey(t, s, fence, ref, "builtin-1")
	if created.Shredded() {
		t.Fatal("a new record is shredded")
	}

	got, err := s.GetContentKey(t.Context(), ref)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if !sameContentKey(got, created) {
		t.Errorf("get = %+v, want %+v", got, created)
	}
}

func testContentCreateTwice(t *testing.T, s ContentKeyStore) {
	t.Helper()
	fence := contentNamespace(t, s)
	ref := contentRef(t, "stk-one", secret.ContentKindState, "upd-1")

	first := mustCreateContentKey(t, s, fence, ref, "builtin-1")

	second, err := s.CreateContentKey(t.Context(), newContentKey(ref, "builtin-1"), fence)
	if err != nil {
		t.Fatalf("second create: %v", err)
	}

	if second.ID != first.ID {
		t.Errorf("second create ID = %s, want the first ID %s", second.ID, first.ID)
	}
}

func testContentMissing(t *testing.T, s ContentKeyStore) {
	t.Helper()
	contentNamespace(t, s)

	_, err := s.GetContentKey(t.Context(), contentRef(t, "stk-one", secret.ContentKindState, "upd-1"))
	if !errors.Is(err, secret.ErrKeyNotFound) {
		t.Errorf("get missing err = %v, want ErrKeyNotFound", err)
	}
}

func testContentDistinctRefs(t *testing.T, s ContentKeyStore) {
	t.Helper()
	fence := contentNamespace(t, s)

	refs := []secret.ContentRef{
		contentRef(t, "ws-one", secret.ContentKindState, "sv-1"),
		contentRef(t, "ws-one", secret.ContentKindState, "sv-2"),
		contentRef(t, "ws-one", secret.ContentKindPlan, "sv-1"),
		contentRef(t, "ws-one", secret.ContentKindJobLog, "sv-1"),
		contentRef(t, "ws-two", secret.ContentKindState, "sv-1"),
	}

	seen := map[secret.ContentKeyID]bool{}

	for _, ref := range refs {
		k := mustCreateContentKey(t, s, fence, ref, "builtin-1")
		if seen[k.ID] {
			t.Errorf("%s shares the key %s with another reference", ref, k.ID)
		}

		seen[k.ID] = true
	}
}

func testContentShred(t *testing.T, s ContentKeyStore) {
	t.Helper()
	fence := contentNamespace(t, s)
	ref := contentRef(t, "stk-one", secret.ContentKindState, "upd-1")
	created := mustCreateContentKey(t, s, fence, ref, "builtin-1")
	at := contentTime().Add(time.Hour)

	shredded, err := s.ShredContentKey(t.Context(), ref, at, fence)
	if err != nil {
		t.Fatalf("shred: %v", err)
	}

	if shredded.ID != created.ID || !shredded.ShreddedAt.Equal(at) {
		t.Errorf("shred = %+v, want ID %s shredded at %v", shredded, created.ID, at)
	}

	got, err := s.GetContentKey(t.Context(), ref)
	if !errors.Is(err, secret.ErrShredded) {
		t.Fatalf("get after shred err = %v, want ErrShredded", err)
	}

	if got.ID != created.ID || !got.ShreddedAt.Equal(at) {
		t.Errorf("get after shred = %+v, want the shredded record", got)
	}
}

func testContentShredTwice(t *testing.T, s ContentKeyStore) {
	t.Helper()
	fence := contentNamespace(t, s)
	ref := contentRef(t, "stk-one", secret.ContentKindState, "upd-1")
	mustCreateContentKey(t, s, fence, ref, "builtin-1")
	first := contentTime().Add(time.Hour)

	if _, err := s.ShredContentKey(t.Context(), ref, first, fence); err != nil {
		t.Fatalf("first shred: %v", err)
	}

	again, err := s.ShredContentKey(t.Context(), ref, first.Add(time.Hour), fence)
	if err != nil {
		t.Fatalf("second shred: %v", err)
	}

	if !again.ShreddedAt.Equal(first) {
		t.Errorf("second shred time = %v, want the first %v", again.ShreddedAt, first)
	}
}

func testContentCreateAfterShred(t *testing.T, s ContentKeyStore) {
	t.Helper()
	fence := contentNamespace(t, s)
	ref := contentRef(t, "stk-one", secret.ContentKindState, "upd-1")
	created := mustCreateContentKey(t, s, fence, ref, "builtin-1")

	if _, err := s.ShredContentKey(t.Context(), ref, contentTime().Add(time.Hour), fence); err != nil {
		t.Fatalf("shred: %v", err)
	}

	got, err := s.CreateContentKey(t.Context(), newContentKey(ref, "builtin-1"), fence)
	if !errors.Is(err, secret.ErrShredded) {
		t.Fatalf("create after shred err = %v, want ErrShredded", err)
	}

	if got.ID != created.ID {
		t.Errorf("create after shred ID = %s, want the shredded record %s", got.ID, created.ID)
	}
}

func testContentShredMissing(t *testing.T, s ContentKeyStore) {
	t.Helper()
	fence := contentNamespace(t, s)

	_, err := s.ShredContentKey(
		t.Context(),
		contentRef(t, "stk-one", secret.ContentKindState, "upd-1"),
		contentTime(),
		fence,
	)
	if !errors.Is(err, secret.ErrKeyNotFound) {
		t.Errorf("shred missing err = %v, want ErrKeyNotFound", err)
	}
}

func testContentFenced(t *testing.T, s ContentKeyStore) {
	t.Helper()
	fence := contentNamespace(t, s)
	stale := namespace.Fence{Namespace: fence.Namespace, FailoverVersion: fence.FailoverVersion - 1}
	ref := contentRef(t, "stk-one", secret.ContentKindState, "upd-1")

	_, err := s.CreateContentKey(
		t.Context(),
		newContentKey(ref, "builtin-1"),
		stale,
	)
	if !errors.Is(
		err,
		namespace.ErrFenced,
	) {
		t.Errorf("create with a stale fence err = %v, want ErrFenced", err)
	}

	if _, err := s.GetContentKey(t.Context(), ref); !errors.Is(err, secret.ErrKeyNotFound) {
		t.Errorf("a fenced create left a record: err = %v", err)
	}

	mustCreateContentKey(t, s, fence, ref, "builtin-1")

	if _, err := s.ShredContentKey(t.Context(), ref, contentTime(), stale); !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("shred with a stale fence err = %v, want ErrFenced", err)
	}

	if _, err := s.GetContentKey(t.Context(), ref); err != nil {
		t.Errorf("a fenced shred changed the record: err = %v", err)
	}
}

func testContentList(t *testing.T, s ContentKeyStore) {
	t.Helper()
	fence := contentNamespace(t, s)

	want := make([]secret.ContentKeyID, 0, 3)

	for _, item := range []string{"sv-1", "sv-2", "sv-3"} {
		want = append(
			want,
			mustCreateContentKey(t, s, fence, contentRef(t, "ws-one", secret.ContentKindState, item), "builtin-1").ID,
		)
	}

	mustCreateContentKey(t, s, fence, contentRef(t, "ws-two", secret.ContentKindState, "sv-1"), "builtin-1")

	_, err := s.ShredContentKey(
		t.Context(),
		contentRef(t, "ws-one", secret.ContentKindState, "sv-2"),
		contentTime(),
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

	first, err := s.ListContentKeys(t.Context(), scope, "", 2)
	if err != nil {
		t.Fatalf("list first page: %v", err)
	}

	rest, err := s.ListContentKeys(t.Context(), scope, first[len(first)-1].ID, 2)
	if err != nil {
		t.Fatalf("list second page: %v", err)
	}

	items := slices.Concat(first, rest)
	got := make([]secret.ContentKeyID, 0, len(items))
	for i := range items {
		k := items[i]
		got = append(got, k.ID)
	}

	if !slices.Equal(got, want) {
		t.Errorf("listed IDs = %v, want %v (shredded records included, other scopes left out)", got, want)
	}
}

func testContentWrapperIDs(t *testing.T, s ContentKeyStore) {
	t.Helper()
	fence := contentNamespace(t, s)

	empty, err := s.ListContentWrapperIDs(t.Context())
	if err != nil || len(empty) != 0 {
		t.Fatalf("wrapper IDs of an empty store = %v, %v, want none", empty, err)
	}

	mustCreateContentKey(t, s, fence, contentRef(t, "ws-one", secret.ContentKindState, "sv-1"), "builtin-b")
	mustCreateContentKey(t, s, fence, contentRef(t, "ws-one", secret.ContentKindState, "sv-2"), "builtin-a")
	mustCreateContentKey(t, s, fence, contentRef(t, "ws-one", secret.ContentKindPlan, "pln-1"), "builtin-a")
	gone := contentRef(t, "ws-one", secret.ContentKindJobLog, "job-1")
	mustCreateContentKey(t, s, fence, gone, "builtin-old")

	if _, shredErr := s.ShredContentKey(t.Context(), gone, contentTime(), fence); shredErr != nil {
		t.Fatalf("shred: %v", shredErr)
	}

	got, err := s.ListContentWrapperIDs(t.Context())
	if err != nil {
		t.Fatalf("wrapper IDs: %v", err)
	}

	if want := []string{"builtin-a", "builtin-b"}; !slices.Equal(got, want) {
		t.Errorf("wrapper IDs = %v, want %v", got, want)
	}
}
