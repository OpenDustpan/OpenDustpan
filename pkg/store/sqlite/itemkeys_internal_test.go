package sqlite

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// seedItemKey makes the namespace acme and one item key in it, and returns the
// reference of the key.
func seedItemKey(t *testing.T, s *Store) secret.ItemRef {
	t.Helper()

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.UnixMilli(1_700_000_000_000).UTC(),
		Name:                "acme",
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	scope, err := secret.NewScope("acme", "unit-1")
	if err != nil {
		t.Fatalf("scope: %v", err)
	}

	ref, err := secret.NewItemRef(scope, secret.ItemKindPlan, "pln-1")
	if err != nil {
		t.Fatalf("item ref: %v", err)
	}

	k := secret.ItemKey{
		CreatedAt: time.UnixMilli(1_700_000_000_000).UTC(), ShreddedAt: time.Time{}, Ref: ref,
		ID: secret.NewItemKeyID(), WrapperID: "builtin-1",
		Blob: secret.ItemKeyBlobInfo{Version: 1, Size: 60, MD5: [16]byte{1, 2, 3}, Attempt: "abcdefghij0123456789"},
	}
	if _, err = s.CreateItemKey(t.Context(), k, ns.Fence()); err != nil {
		t.Fatalf("create item key: %v", err)
	}

	return ref
}

// tamperedItemKeyRows lists changes of the only item key row that a record of
// the store never has.
func tamperedItemKeyRows() []struct{ name, update string } {
	return []struct{ name, update string }{
		{"an ID that is not an item key ID", `UPDATE item_keys SET id = 'x-1'`},
		{"an ID with a slash", `UPDATE item_keys SET id = 'ik-a/b'`},
		{"an empty scope", `UPDATE item_keys SET scope_id = ''`},
		{"a scope with a slash", `UPDATE item_keys SET scope_id = 'a/b'`},
		{"a scope with a space", `UPDATE item_keys SET scope_id = 'a b'`},
		{"an empty item", `UPDATE item_keys SET item = ''`},
		{"an item with a slash", `UPDATE item_keys SET item = 'a/b'`},
		{"an item with a parent segment", `UPDATE item_keys SET item = '..'`},
		{"an empty wrapper ID", `UPDATE item_keys SET wrapper_id = ''`},
	}
}

// TestCheckStagedItemKeys proves the restore check: it accepts the rows that
// the store wrote, and it refuses each row that a new record would not pass.
func TestCheckStagedItemKeys(t *testing.T) {
	t.Parallel()

	t.Run("a row that the store wrote passes", func(t *testing.T) {
		t.Parallel()

		s := openInternalTemplated(t)
		seedItemKey(t, s)

		if err := checkStagedItemKeys(t.Context(), s.conn); err != nil {
			t.Fatalf("check = %v, want nil", err)
		}
	})

	for _, tc := range tamperedItemKeyRows() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := openInternalTemplated(t)
			ref := seedItemKey(t, s)

			if _, err := s.conn.ExecContext(t.Context(), tc.update); err != nil {
				t.Fatalf("change the row: %v", err)
			}

			err := checkStagedItemKeys(t.Context(), s.conn)
			if !errors.Is(err, backup.ErrCorrupt) {
				t.Fatalf("check = %v, want ErrCorrupt", err)
			}

			// The read path checks the row again, so a bad row is not served.
			if k, getErr := s.GetItemKey(t.Context(), ref); getErr == nil {
				t.Fatalf("get of a bad row = %+v, want an error", k)
			}
		})
	}
}

// TestCheckStagedItemKeysRefusesAMissingNamespace proves that a record whose
// namespace the backup does not hold is corrupt. An inner join would skip such
// a row, and the restore would bring it in.
func TestCheckStagedItemKeysRefusesAMissingNamespace(t *testing.T) {
	t.Parallel()

	s := openInternalTemplated(t)
	seedItemKey(t, s)

	// The foreign key refuses this change, so the connection that makes it
	// switches the key check off. It is closed before the check runs.
	pinned, err := s.conn.Conn(t.Context())
	if err != nil {
		t.Fatalf("take a connection: %v", err)
	}

	for _, stmt := range []string{`PRAGMA foreign_keys = OFF`, `UPDATE item_keys SET namespace_id = 'ns-gone'`} {
		if _, execErr := pinned.ExecContext(t.Context(), stmt); execErr != nil {
			t.Fatalf("%s: %v", stmt, execErr)
		}
	}

	if closeErr := pinned.Close(); closeErr != nil {
		t.Fatalf("release the connection: %v", closeErr)
	}

	err = checkStagedItemKeys(t.Context(), s.conn)
	if !errors.Is(err, backup.ErrCorrupt) || !strings.Contains(fmt.Sprint(err), "has no namespace") {
		t.Fatalf("check = %v, want the refusal of a record with no namespace", err)
	}
}

// seedTwoNamespaces makes the namespace acme with one item key, and the
// namespace other. It returns the key of acme and a reference in other.
func seedTwoNamespaces(t *testing.T, s *Store) (secret.ItemKey, secret.ItemRef) {
	t.Helper()

	ref := seedItemKey(t, s)

	other := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.UnixMilli(1_700_000_000_000).UTC(),
		Name:                "other",
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.CreateNamespace(t.Context(), other); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	scope, err := secret.NewScope("other", "unit-1")
	if err != nil {
		t.Fatalf("scope: %v", err)
	}

	otherRef, err := secret.NewItemRef(scope, secret.ItemKindPlan, "pln-9")
	if err != nil {
		t.Fatalf("item ref: %v", err)
	}

	stored, err := s.GetItemKey(t.Context(), ref)
	if err != nil {
		t.Fatalf("get item key: %v", err)
	}

	return stored, otherRef
}

// TestItemKeyStoreRefusesHostileCreate proves the checks of the store, which
// hold when a caller skipped its own: a record of another namespace than the
// fence, an invalid record, and a record created shredded.
func TestItemKeyStoreRefusesHostileCreate(t *testing.T) {
	t.Parallel()

	s := openInternalTemplated(t)
	stored, otherRef := seedTwoNamespaces(t, s)

	acme := namespace.Fence{Namespace: "acme", FailoverVersion: 1}
	good := secret.ItemKey{
		CreatedAt: stored.CreatedAt, ShreddedAt: time.Time{}, Ref: stored.Ref, ID: secret.NewItemKeyID(),
		WrapperID: "builtin-2", Blob: stored.Blob,
	}

	for _, tc := range refusedItemKeys(otherRef, acme) {
		_, err := s.CreateItemKey(t.Context(), tc.key(good), tc.fence)
		if !errors.Is(err, tc.wanted) {
			t.Errorf("%s: create = %v, want %v", tc.name, err, tc.wanted)
		}
	}

	// None of the refused calls changed the record that the store wrote.
	got, err := s.GetItemKey(t.Context(), stored.Ref)
	if err != nil || got.Shredded() || got.ID != stored.ID || got.WrapperID != stored.WrapperID {
		t.Fatalf("record after the refused creates = %+v, %v; want the first record, live", got, err)
	}
}

// TestItemKeyStoreRefusesHostileShredAndList proves that a shred names the
// namespace of its fence, needs a time, and that a page of a list is bounded.
func TestItemKeyStoreRefusesHostileShredAndList(t *testing.T) {
	t.Parallel()

	s := openInternalTemplated(t)
	stored, otherRef := seedTwoNamespaces(t, s)
	acme := namespace.Fence{Namespace: "acme", FailoverVersion: 1}

	_, err := s.ShredItemKey(t.Context(), otherRef, time.UnixMilli(5), acme)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("shred of a record of another namespace under the fence of acme = %v, want ErrFenced", err)
	}

	_, err = s.ShredItemKey(t.Context(), stored.Ref, time.Time{}, acme)
	if !errors.Is(err, secret.ErrInvalidItemRef) {
		t.Errorf("shred with no time = %v, want ErrInvalidItemRef", err)
	}

	for _, limit := range []int{0, -1, 1001} {
		_, err = s.ListItemKeys(t.Context(), stored.Ref.Scope, "", limit)
		if !errors.Is(err, secret.ErrInvalidItemRef) {
			t.Errorf("list with limit %d = %v, want ErrInvalidItemRef", limit, err)
		}
	}

	got, err := s.GetItemKey(t.Context(), stored.Ref)
	if err != nil || got.Shredded() {
		t.Fatalf("record after the refused calls = %+v, %v; want it live", got, err)
	}
}

// refusedItemKey is a create of an item key that the store refuses.
type refusedItemKey struct {
	name   string
	key    func(k secret.ItemKey) secret.ItemKey
	fence  namespace.Fence
	wanted error
}

// refusedItemKeys lists the creates that the store refuses: a record of other
// as the reference, and the records that are not valid, under the fence acme.
func refusedItemKeys(other secret.ItemRef, acme namespace.Fence) []refusedItemKey {
	change := func(fn func(k *secret.ItemKey)) func(k secret.ItemKey) secret.ItemKey {
		return func(k secret.ItemKey) secret.ItemKey {
			fn(&k)

			return k
		}
	}

	return []refusedItemKey{
		{
			"a record of another namespace than the fence", change(func(k *secret.ItemKey) { k.Ref = other }), acme,
			namespace.ErrFenced,
		},
		{
			"a record with an ID that is not a key ID", change(func(k *secret.ItemKey) { k.ID = "x" }), acme,
			secret.ErrInvalidItemRef,
		},
		{
			"a record with no wrapper", change(func(k *secret.ItemKey) { k.WrapperID = "" }), acme,
			secret.ErrInvalidItemRef,
		},
		{
			"a record created shredded", change(func(k *secret.ItemKey) { k.ShreddedAt = time.UnixMilli(1) }), acme,
			secret.ErrInvalidItemRef,
		},
		{
			"a record with no creation time", change(func(k *secret.ItemKey) { k.CreatedAt = time.Time{} }), acme,
			secret.ErrInvalidItemRef,
		},
		{
			"a record of an unknown kind", change(func(k *secret.ItemKey) { k.Ref.Kind = "other" }), acme,
			secret.ErrInvalidItemRef,
		},
	}
}

// TestItemKeyBackupNamesTheAttemptKey proves that the backup names the blob
// key of a record with its attempt part, so a backup copies the blob that the
// record names and no other attempt.
func TestItemKeyBackupNamesTheAttemptKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		update string
		suffix func(k secret.ItemKey) string
	}{
		{
			name: "a record of the first version", update: "",
			suffix: func(k secret.ItemKey) string { return "/v1-" + k.Blob.Attempt },
		},
		{
			name: "a record of a rewrap", update: `UPDATE item_keys SET blob_version = 3`,
			suffix: func(k secret.ItemKey) string { return "/v3-" + k.Blob.Attempt },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := openInternalTemplated(t)
			k := seedRowOfLayout(t, s, tc.update)

			if err := checkStagedItemKeys(t.Context(), s.conn); err != nil {
				t.Fatalf("check = %v, want nil", err)
			}

			refs, _, err := blobRefs(t.Context(), s.conn)
			if err != nil {
				t.Fatalf("blob refs: %v", err)
			}

			want := "/item-keys/" + string(k.ID) + tc.suffix(k)
			if hits := countKeySuffix(refs, want); hits != 1 {
				t.Errorf("blob key %q: %d references, want 1", want, hits)
			}
		})
	}
}

// seedRowOfLayout seeds one item key, runs update on its row, and returns
// the record that the store reads back.
func seedRowOfLayout(t *testing.T, s *Store, update string) secret.ItemKey {
	t.Helper()

	ref := seedItemKey(t, s)

	if update != "" {
		if _, err := s.conn.ExecContext(t.Context(), update); err != nil {
			t.Fatalf("change the row: %v", err)
		}
	}

	k, err := s.GetItemKey(t.Context(), ref)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	return k
}

// countKeySuffix counts the references whose key ends with suffix.
func countKeySuffix(refs []backup.BlobRef, suffix string) int {
	n := 0

	for i := range refs {
		if strings.HasSuffix(refs[i].Key, suffix) {
			n++
		}
	}

	return n
}

// TestItemKeyAttemptColumnRefusesABadLength proves the check of the column:
// the database refuses an attempt part that is not 20 lowercase ASCII letters
// and digits, also an empty one and one that names another path, when a write
// skips every check of the store.
func TestItemKeyAttemptColumnRefusesABadLength(t *testing.T) {
	t.Parallel()

	for _, update := range []string{
		`UPDATE item_keys SET blob_attempt = ''`,
		`UPDATE item_keys SET blob_attempt = 'abc'`,
		`UPDATE item_keys SET blob_attempt = 'abcdefghij01234567890'`,
		`UPDATE item_keys SET blob_attempt = 'ABCDEFGHIJ0123456789'`,
		`UPDATE item_keys SET blob_attempt = 'aaaaaaaaaaaaaaaaa/x1'`,
		`UPDATE item_keys SET blob_attempt = 'aaaaaaaaaaaaaaaaa..1'`,
	} {
		s := openInternalTemplated(t)
		seedItemKey(t, s)

		if _, err := s.conn.ExecContext(t.Context(), update); err == nil {
			t.Errorf("%s succeeded, want a refusal of the column check", update)
		}
	}
}

// TestInItemKeyTxEndsALateWrite proves that InItemKeyTx gives the
// transaction its deadline: database/sql rolls the transaction back when the
// deadline passes, so a write through the transaction after the deadline fails, so
// fn never returns nil and the check before the commit is not what stops it.
// The write uses a context with no deadline.
func TestInItemKeyTxEndsALateWrite(t *testing.T) {
	t.Parallel()

	s := openInternalTemplated(t)
	ref := seedItemKey(t, s)
	late := lateItemKey(t, ref)
	fence := namespace.Fence{Namespace: ref.Scope.Namespace, FailoverVersion: 1}

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	var writeErr error

	err := s.InItemKeyTx(ctx, fence, func(tx secret.ItemKeyTx) error {
		waitPast(600 * time.Millisecond)

		_, writeErr = tx.CreateItemKey(context.WithoutCancel(ctx), late)

		return writeErr
	})
	if writeErr == nil || !errors.Is(err, secret.ErrItemKeyAttemptExpired) {
		t.Fatalf("a write after the deadline = %v, transaction = %v, want both to fail, the transaction "+
			"with ErrItemKeyAttemptExpired", writeErr, err)
	}

	if _, getErr := s.GetItemKey(t.Context(), late.Ref); !errors.Is(getErr, secret.ErrKeyNotFound) {
		t.Errorf("a late write left a record: err = %v", getErr)
	}
}

// lateItemKey returns a new record in the namespace of ref, for another item.
func lateItemKey(t *testing.T, ref secret.ItemRef) secret.ItemKey {
	t.Helper()

	other, err := secret.NewItemRef(ref.Scope, ref.Kind, "pln-late")
	if err != nil {
		t.Fatalf("item ref: %v", err)
	}

	return secret.ItemKey{
		CreatedAt: time.UnixMilli(1_700_000_000_000).UTC(), ShreddedAt: time.Time{}, Ref: other,
		ID: secret.NewItemKeyID(), WrapperID: "builtin-1",
		Blob: secret.ItemKeyBlobInfo{Version: 1, Size: 60, MD5: [16]byte{1, 2, 3}, Attempt: "abcdefghij0123456789"},
	}
}

// waitPast waits for d.
func waitPast(d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()

	<-timer.C
}

// TestInItemKeyTxGivesItsOwnDeadline proves that InItemKeyTx ends the
// transaction at its attempt deadline when the caller gives no deadline. The
// store has a short attempt deadline, the context of the call has none, and a
// write after the deadline fails, so fn never returns nil and the check before
// the commit is not what stops it.
func TestInItemKeyTxGivesItsOwnDeadline(t *testing.T) {
	t.Parallel()

	s := openInternalTemplated(t)
	s.attemptDeadline = func(_ context.Context, start time.Time) time.Time {
		return start.Add(200 * time.Millisecond)
	}

	ref := seedItemKey(t, s)
	late := lateItemKey(t, ref)
	fence := namespace.Fence{Namespace: ref.Scope.Namespace, FailoverVersion: 1}

	var writeErr error

	err := s.InItemKeyTx(t.Context(), fence, func(tx secret.ItemKeyTx) error {
		waitPast(600 * time.Millisecond)

		_, writeErr = tx.CreateItemKey(t.Context(), late)

		return writeErr
	})
	if writeErr == nil || !errors.Is(err, secret.ErrItemKeyAttemptExpired) {
		t.Fatalf("a write after the deadline = %v, transaction = %v, want both to fail, the transaction "+
			"with ErrItemKeyAttemptExpired", writeErr, err)
	}

	if _, getErr := s.GetItemKey(t.Context(), late.Ref); !errors.Is(getErr, secret.ErrKeyNotFound) {
		t.Errorf("a late write left a record: err = %v", getErr)
	}
}
