package secrettest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// ItemKeyStore is every persistence port that RunItemKeys exercises:
// namespaces for the fence, and secret.ItemKeyStore under test.
type ItemKeyStore interface {
	namespace.Store
	secret.ItemKeyStore
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
		{name: "a valid fence of another namespace refuses create and shred", test: testItemKeyOtherNamespaceFence},
		{name: "a record with a bad blob description is refused", test: testItemKeyBadBlobInfo},
		{name: "a transaction keeps its record on commit and drops it on rollback", test: testItemKeyTx},
		{name: "a transaction past its attempt deadline does not commit", test: testItemKeyTxDeadline},
		{name: "the transaction reads a namespace, and a missing one is ErrNotFound", test: testItemKeyTxNamespace},
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

// errRollback is the error of a function that asks for a rollback.
var errRollback = errors.New("roll back the transaction")

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
		ID:                  namespace.NewID(),
		CreatedAt:           itemKeyTime(),
		Name:                "acme",
		PrimaryInstallation: "local",
		FailoverVersion:     itemKeyFenceVersion,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
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

// validAttempt is a valid attempt part of a blob key of the suite.
const validAttempt = "abcdefghij0123456789"

// newItemKey returns a record of ref that is not stored yet.
func newItemKey(ref secret.ItemRef, wrapperID string) secret.ItemKey {
	return secret.ItemKey{
		CreatedAt:  itemKeyTime(),
		ShreddedAt: time.Time{},
		Ref:        ref,
		ID:         secret.NewItemKeyID(),
		WrapperID:  wrapperID,
		Blob: secret.ItemKeyBlobInfo{
			Version: 1,
			Size:    60,
			MD5:     [16]byte{1, 2, 3},
			Attempt: "abcdefghij0123456789",
		},
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
		a.CreatedAt.Equal(b.CreatedAt) && a.ShreddedAt.Equal(b.ShreddedAt) && a.Blob == b.Blob
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

// otherItemKeyFence creates the namespace "other" and returns its current
// fence, which is valid for "other" and for no other namespace.
func otherItemKeyFence(t *testing.T, s ItemKeyStore) namespace.Fence {
	t.Helper()

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: itemKeyTime(), Name: "other", PrimaryInstallation: "local",
		FailoverVersion: itemKeyFenceVersion, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace other: %v", err)
	}

	return ns.Fence()
}

// testItemKeyOtherNamespaceFence sends the hostile call straight to the store:
// the fence is current, but it names another namespace than the record. The
// failover version check of the fence passes, so only the check of the
// namespace of the record refuses the call.
func testItemKeyOtherNamespaceFence(t *testing.T, s ItemKeyStore) {
	t.Helper()
	acme := itemKeyNamespace(t, s)
	other := otherItemKeyFence(t, s)
	ref := itemRef(t, "stk-one", secret.ItemKindState, "upd-1")

	_, err := s.CreateItemKey(t.Context(), newItemKey(ref, "builtin-1"), other)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("create under the fence of another namespace err = %v, want ErrFenced", err)
	}

	err = s.InItemKeyTx(t.Context(), other, func(tx secret.ItemKeyTx) error {
		_, createErr := tx.CreateItemKey(t.Context(), newItemKey(ref, "builtin-1"))

		return createErr
	})
	if !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("transaction create under the fence of another namespace err = %v, want ErrFenced", err)
	}

	if _, err = s.GetItemKey(t.Context(), ref); !errors.Is(err, secret.ErrKeyNotFound) {
		t.Errorf("a refused create left a record: err = %v", err)
	}

	mustCreateItemKey(t, s, acme, ref, "builtin-1")

	if _, err = s.ShredItemKey(t.Context(), ref, itemKeyTime(), other); !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("shred under the fence of another namespace err = %v, want ErrFenced", err)
	}

	if _, err = s.GetItemKey(t.Context(), ref); err != nil {
		t.Errorf("a refused shred changed the record: err = %v", err)
	}
}

func testItemKeyBadBlobInfo(t *testing.T, s ItemKeyStore) {
	t.Helper()
	fence := itemKeyNamespace(t, s)

	for name, info := range map[string]secret.ItemKeyBlobInfo{
		"no version":   {Version: 0, Size: 60, MD5: [16]byte{1}, Attempt: validAttempt},
		"huge version": {Version: secret.MaxItemKeyVersion + 1, Size: 60, MD5: [16]byte{1}, Attempt: validAttempt},
		"no size":      {Version: 1, Size: 0, MD5: [16]byte{1}, Attempt: validAttempt},
		"huge size":    {Version: 1, Size: secret.MaxWrappedItemKeyBytes + 1, MD5: [16]byte{1}, Attempt: validAttempt},
		// A new record always names a key with an attempt part.
		"no attempt part":            {Version: 1, Size: 60, MD5: [16]byte{1}, Attempt: ""},
		"a short attempt part":       {Version: 1, Size: 60, MD5: [16]byte{1}, Attempt: "abc"},
		"an attempt in upper case":   {Version: 1, Size: 60, MD5: [16]byte{1}, Attempt: "ABCDEFGHIJ0123456789"},
		"an attempt with a slash":    {Version: 1, Size: 60, MD5: [16]byte{1}, Attempt: "abcdefghij/123456789"},
		"an attempt with a line end": {Version: 1, Size: 60, MD5: [16]byte{1}, Attempt: "abcdefghij\n123456789"},
	} {
		ref := itemRef(t, "stk-one", secret.ItemKindState, "upd-"+strings.ReplaceAll(name, " ", "-"))
		k := newItemKey(ref, "builtin-1")
		k.Blob = info

		if _, err := s.CreateItemKey(t.Context(), k, fence); !errors.Is(err, secret.ErrInvalidItemRef) {
			t.Errorf("create with %s err = %v, want ErrInvalidItemRef", name, err)
		}
	}
}

func testItemKeyTx(t *testing.T, s ItemKeyStore) {
	t.Helper()
	fence := itemKeyNamespace(t, s)
	kept := itemRef(t, "stk-one", secret.ItemKindState, "upd-kept")
	dropped := itemRef(t, "stk-one", secret.ItemKindState, "upd-dropped")

	err := s.InItemKeyTx(t.Context(), fence, func(tx secret.ItemKeyTx) error {
		created, createErr := tx.CreateItemKey(t.Context(), newItemKey(kept, "builtin-1"))
		if createErr != nil {
			return createErr
		}

		got, getErr := tx.GetItemKey(t.Context(), kept)
		if getErr != nil || got.ID != created.ID {
			t.Errorf("get inside the transaction = %v, %v, want the created record", got.ID, getErr)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("committed transaction: %v", err)
	}

	if _, err = s.GetItemKey(t.Context(), kept); err != nil {
		t.Errorf("a committed transaction lost its record: %v", err)
	}

	err = s.InItemKeyTx(t.Context(), fence, func(tx secret.ItemKeyTx) error {
		if _, createErr := tx.CreateItemKey(t.Context(), newItemKey(dropped, "builtin-1")); createErr != nil {
			return createErr
		}

		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("rolled-back transaction err = %v, want the error of the function", err)
	}

	if _, err = s.GetItemKey(t.Context(), dropped); !errors.Is(err, secret.ErrKeyNotFound) {
		t.Errorf("a rolled-back transaction left a record: err = %v", err)
	}
}

func testItemKeyTxNamespace(t *testing.T, s ItemKeyStore) {
	t.Helper()
	fence := itemKeyNamespace(t, s)

	want, err := s.GetNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	err = s.InItemKeyTx(t.Context(), fence, func(tx secret.ItemKeyTx) error {
		got, getErr := tx.GetNamespace(t.Context(), "acme")
		if getErr != nil || got.ID != want.ID || got.Name != want.Name {
			t.Errorf("namespace inside the transaction = %+v, %v, want %+v", got, getErr, want)
		}

		if _, getErr = tx.GetNamespace(t.Context(), "absent"); !errors.Is(getErr, namespace.ErrNotFound) {
			t.Errorf("missing namespace inside the transaction err = %v, want namespace.ErrNotFound", getErr)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("transaction: %v", err)
	}
}

// attemptDeadlineForTest is the deadline that the deadline case gives the
// transaction through its context.
const attemptDeadlineForTest = 300 * time.Millisecond

// testItemKeyTxDeadline holds a transaction open past its attempt deadline,
// as a slow caller or a lock wait does. The commit fails with
// ErrItemKeyAttemptExpired, and no record stays, so no record can name a blob
// that the reaper deleted after the grace period. A transaction inside the
// deadline commits.
// Real time: it proves that a real context deadline ends a late commit.
func testItemKeyTxDeadline(t *testing.T, s ItemKeyStore) {
	t.Helper()
	fence := itemKeyNamespace(t, s)
	late := itemRef(t, "stk-one", secret.ItemKindState, "upd-late")
	inTime := itemRef(t, "stk-one", secret.ItemKindState, "upd-in-time")

	ctx, cancel := context.WithTimeout(t.Context(), attemptDeadlineForTest)
	defer cancel()

	err := s.InItemKeyTx(ctx, fence, func(tx secret.ItemKeyTx) error {
		if _, createErr := tx.CreateItemKey(ctx, newItemKey(late, "builtin-1")); createErr != nil {
			return createErr
		}

		<-ctx.Done()
		// The context of the call has ended, so the wait runs on a copy of it
		// that does not end with it.
		waitFor(context.WithoutCancel(ctx), attemptDeadlineForTest/3)

		return nil
	})
	if !errors.Is(err, secret.ErrItemKeyAttemptExpired) {
		t.Errorf("late commit err = %v, want ErrItemKeyAttemptExpired", err)
	}

	if _, getErr := s.GetItemKey(t.Context(), late); !errors.Is(getErr, secret.ErrKeyNotFound) {
		t.Errorf("a late transaction left a record: err = %v", getErr)
	}

	// The same deadline, with a commit just inside it.
	inTimeCtx, cancelInTime := context.WithTimeout(t.Context(), attemptDeadlineForTest)
	defer cancelInTime()

	err = s.InItemKeyTx(inTimeCtx, fence, func(tx secret.ItemKeyTx) error {
		_, createErr := tx.CreateItemKey(inTimeCtx, newItemKey(inTime, "builtin-1"))

		return createErr
	})
	if err != nil {
		t.Fatalf("transaction inside the deadline: %v", err)
	}

	if _, getErr := s.GetItemKey(t.Context(), inTime); getErr != nil {
		t.Errorf("a transaction inside the deadline left no record: err = %v", getErr)
	}
}

// waitFor waits for d, or until ctx ends.
func waitFor(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}
