package secret_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/scopeaudit"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

var errNamerDown = errors.New("records cannot be read")

// TestItemKeyBlobsNewKeyIsUniqueForEachAttempt shows that two writes never
// share a blob key: each call of NewKey gives a new attempt part, for the first
// version and for a rewrap to a later version.
func TestItemKeyBlobsNewKeyIsUniqueForEachAttempt(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		version int
	}{
		{name: "the first write", version: 1},
		{name: "a rewrap", version: 2},
		{name: "a late rewrap", version: secret.MaxItemKeyVersion},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			b, store, _ := newItemKeyBlobs(t)
			id := secret.NewItemKeyID()

			const attempts = 1000

			seen := make(map[blob.Key]struct{}, attempts)

			for range attempts {
				key := checkNewKey(t, b, id, tc.version)

				if _, dup := seen[key]; dup {
					t.Fatalf("two attempts share the key %s", key)
				}

				seen[key] = struct{}{}
			}

			if n := store.callCount(); n != 0 {
				t.Errorf("NewKey reached the store %d times, want 0", n)
			}
		})
	}
}

// checkNewKey makes a new key for version of id in nsA, checks that it names
// its parts and a valid attempt part, and returns it.
func checkNewKey(t *testing.T, b *secret.ItemKeyBlobs, id secret.ItemKeyID, version int) blob.Key {
	t.Helper()

	key, attempt, err := b.NewKey(nsA, id, version)
	if err != nil {
		t.Fatalf("new key: %v", err)
	}

	parsed, err := secret.ParseItemKeyBlobKey(key)
	if err != nil {
		t.Fatalf("parse %s: %v", key, err)
	}

	want := secret.ItemKeyBlob{Namespace: nsA, ID: id, Version: version, Attempt: attempt}
	if parsed != want || !secret.ValidItemKeyAttempt(attempt) {
		t.Fatalf("parse %s = %+v, want %+v with a valid attempt part", key, parsed, want)
	}

	return key
}

// TestItemKeyBlobsPutRefusesAKeyWithNoAttempt sends a key with no attempt
// part straight to Put, as if a writer had skipped NewKey. A second write to
// such a key could replace a blob that a record names, so Put refuses it
// before the store sees it.
func TestItemKeyBlobsPutRefusesAKeyWithNoAttempt(t *testing.T) {
	t.Parallel()

	b, store, rec := newItemKeyBlobs(t)
	id := secret.NewItemKeyID()

	for _, version := range []string{"v1", "v2", "v1-"} {
		key := blob.Key("namespaces/" + string(nsA) + "/item-keys/" + string(id) + "/" + version)

		if putErr := b.Put(t.Context(), nsA, key, []byte("wrapped")); !errors.Is(putErr, secret.ErrInvalidItemRef) {
			t.Errorf("put of %s err = %v, want ErrInvalidItemRef", key, putErr)
		}

		if _, keyErr := b.Key(nsA, id, 1, ""); !errors.Is(keyErr, secret.ErrInvalidItemRef) {
			t.Errorf("key with no attempt part err = %v, want ErrInvalidItemRef", keyErr)
		}
	}

	if n := store.callCount(); n != 0 {
		t.Errorf("a refused put reached the store %d times, want 0", n)
	}

	if n := len(rec.refusals()); n != 0 {
		t.Errorf("a key of the namespace is not a scope refusal, recorder called %d times", n)
	}
}

// TestItemKeyBlobsGetRefusesASubstitutedBlob is the test of the digest layer:
// the store replaced the blob that the record names, as a store that ignores a
// conditional write does on a second write. Get compares the bytes with the
// record before it returns them, so the caller never unwraps them, and it
// writes one mismatch record that holds no key.
func TestItemKeyBlobsGetRefusesASubstitutedBlob(t *testing.T) {
	t.Parallel()

	original := bytes.Repeat([]byte{0x5a}, 60)

	cases := []struct {
		name        string
		replacement []byte
	}{
		{name: "bytes of the same size", replacement: bytes.Repeat([]byte{0xa5}, 60)},
		{name: "one flipped byte", replacement: append(bytes.Repeat([]byte{0x5a}, 59), 0x5b)},
		{name: "a longer blob", replacement: bytes.Repeat([]byte{0x5a}, 61)},
		{name: "a shorter blob", replacement: original[:59]},
		{name: "an empty blob", replacement: []byte{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			b, store, rec := newItemKeyBlobs(t)
			key := mustKey(t, b, nsA, secret.NewItemKeyID(), 1)
			info := infoOf(t, key, original)

			if err := b.Put(t.Context(), nsA, key, original); err != nil {
				t.Fatalf("put: %v", err)
			}

			store.replace(key, tc.replacement)

			got, err := b.Get(t.Context(), nsA, key, info)
			checkMismatch(t, got, err, key)

			if calls := rec.mismatchCalls(); !slices.Equal(calls, []refusal{{ns: nsA, op: "get", where: ""}}) {
				t.Errorf("mismatch records = %v, want one get in %s", calls, nsA)
			}
		})
	}
}

// checkMismatch checks that a read of key returned ErrItemKeyBlobMismatch, no
// bytes, and an error that does not hold the key.
func checkMismatch(t *testing.T, got []byte, err error, key blob.Key) {
	t.Helper()

	if !errors.Is(err, secret.ErrItemKeyBlobMismatch) {
		t.Fatalf("get of a substituted blob = %q, %v, want ErrItemKeyBlobMismatch", got, err)
	}

	if got != nil {
		t.Errorf("get returned %d bytes with the mismatch", len(got))
	}

	if strings.Contains(err.Error(), key.String()) {
		t.Errorf("error %q holds the blob key", err)
	}
}

// TestItemKeyBlobsMismatchFailsClosedWhenTheRecorderFails shows that a
// mismatch is still a refusal when its record fails.
func TestItemKeyBlobsMismatchFailsClosedWhenTheRecorderFails(t *testing.T) {
	t.Parallel()

	store, rec := newMemBlobs(), newFakeRecorder(errAuditDown)

	b, err := secret.NewItemKeyBlobs(store, rec, rec)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	key := mustKey(t, b, nsA, secret.NewItemKeyID(), 1)
	info := infoOf(t, key, []byte("original"))

	if putErr := b.Put(t.Context(), nsA, key, []byte("original")); putErr != nil {
		t.Fatalf("put: %v", putErr)
	}

	store.replace(key, []byte("replaced"))

	got, err := b.Get(t.Context(), nsA, key, info)
	if !errors.Is(err, secret.ErrItemKeyBlobMismatch) || !errors.Is(err, errAuditDown) || got != nil {
		t.Errorf("get = %q, %v, want ErrItemKeyBlobMismatch and the recorder error", got, err)
	}
}

// TestItemKeyBlobsGetRefusesBlobInfoOfAnotherKey sends Get a record that does
// not name the key of the call. The size and the MD5 of one blob must never
// check the bytes of another blob, so Get refuses the call before the store.
func TestItemKeyBlobsGetRefusesBlobInfoOfAnotherKey(t *testing.T) {
	t.Parallel()

	b, store, rec := newItemKeyBlobs(t)
	wrapped := []byte("wrapped")
	key := mustKey(t, b, nsA, secret.NewItemKeyID(), 2)
	good := infoOf(t, key, wrapped)

	if err := b.Put(t.Context(), nsA, key, wrapped); err != nil {
		t.Fatalf("put: %v", err)
	}

	edit := func(f func(*secret.ItemKeyBlobInfo)) secret.ItemKeyBlobInfo {
		i := good
		f(&i)

		return i
	}

	cases := map[string]secret.ItemKeyBlobInfo{
		"another version":       edit(func(i *secret.ItemKeyBlobInfo) { i.Version = 1 }),
		"another attempt":       edit(func(i *secret.ItemKeyBlobInfo) { i.Attempt = testAttempt }),
		"no attempt":            edit(func(i *secret.ItemKeyBlobInfo) { i.Attempt = "" }),
		"a size of zero":        edit(func(i *secret.ItemKeyBlobInfo) { i.Size = 0 }),
		"a negative size":       edit(func(i *secret.ItemKeyBlobInfo) { i.Size = -1 }),
		"a size over the limit": edit(func(i *secret.ItemKeyBlobInfo) { i.Size = secret.MaxWrappedItemKeyBytes + 1 }),
		"no info at all":        {Version: 0, Attempt: "", Size: 0, MD5: [16]byte{}},
	}

	before := store.callCount()

	for name, info := range cases {
		if got, err := b.Get(t.Context(), nsA, key, info); !errors.Is(err, secret.ErrInvalidItemRef) || got != nil {
			t.Errorf("get with %s = %q, %v, want ErrInvalidItemRef", name, got, err)
		}
	}

	if n := store.callCount(); n != before {
		t.Errorf("refused reads reached the store %d times, want 0", n-before)
	}

	if n := len(rec.mismatchCalls()) + len(rec.refusals()); n != 0 {
		t.Errorf("refused reads wrote %d records, want 0", n)
	}
}

// TestItemKeyBlobsMismatchAuditEndToEnd checks that a mismatch reaches the
// audit store through the recorder of the package.
func TestItemKeyBlobsMismatchAuditEndToEnd(t *testing.T) {
	t.Parallel()

	entries := &fakeServerStore{err: nil, entries: nil}

	rec, err := scopeaudit.New(entries, secret.ItemKeyFamily(), nil)
	if err != nil {
		t.Fatalf("recorder: %v", err)
	}

	store := newMemBlobs()

	b, err := secret.NewItemKeyBlobs(store, rec, rec)
	if err != nil {
		t.Fatalf("blobs: %v", err)
	}

	key := mustKey(t, b, nsA, secret.NewItemKeyID(), 1)
	info := infoOf(t, key, []byte("original"))

	if putErr := b.Put(t.Context(), nsA, key, []byte("original")); putErr != nil {
		t.Fatalf("put: %v", putErr)
	}

	store.replace(key, []byte("replaced"))

	if _, getErr := b.Get(t.Context(), nsA, key, info); !errors.Is(getErr, secret.ErrItemKeyBlobMismatch) {
		t.Fatalf("get err = %v, want ErrItemKeyBlobMismatch", getErr)
	}

	if len(entries.entries) != 1 {
		t.Fatalf("entries = %+v, want 1", entries.entries)
	}

	e := entries.entries[0]
	if e.Action != scopeaudit.ActionMismatch || e.Kind != audit.KindSecurity || e.Outcome != audit.OutcomeDenied ||
		e.Object != string(nsA) || strings.Contains(e.Reason, key.String()) {
		t.Errorf("entry = %+v", e)
	}
}

// namerOf returns an ItemKeyBlobNamer that names the keys in named.
func namerOf(named ...blob.Key) secret.ItemKeyBlobNamer {
	return func(_ context.Context, part secret.ItemKeyBlob) (bool, error) {
		for _, key := range named {
			parsed, err := secret.ParseItemKeyBlobKey(key)
			if err == nil && parsed == part {
				return true, nil
			}
		}

		return false, nil
	}
}

// TestItemKeyBlobsGarbageWaitsForTheCommit is the test of the reaper rule: a
// blob that no committed record names is garbage only after the grace period,
// because the writer puts the blob before the record commits.
func TestItemKeyBlobsGarbageWaitsForTheCommit(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 29, 12, 0, 0, 0, time.UTC)
	old := now.Add(-secret.ItemKeyBlobGracePeriod - time.Second)

	cases := []struct {
		modified time.Time
		name     string
		named    bool
		garbage  bool
	}{
		{name: "a new blob of a write that has not committed", modified: now, named: false, garbage: false},
		{
			name:     "a blob just inside the grace period",
			modified: now.Add(-secret.ItemKeyBlobGracePeriod + time.Second),
			named:    false,
			garbage:  false,
		},
		{name: "an old blob that no record names", modified: old, named: false, garbage: true},
		{name: "an old blob that a record names", modified: old, named: true, garbage: false},
		{name: "a blob with no time", modified: time.Time{}, named: false, garbage: false},
		{name: "a blob from the future", modified: now.Add(time.Hour), named: false, garbage: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			b, store, _ := newItemKeyBlobs(t)
			key := mustKey(t, b, nsA, secret.NewItemKeyID(), 1)

			if err := b.Put(t.Context(), nsA, key, []byte("wrapped")); err != nil {
				t.Fatalf("put: %v", err)
			}

			store.setModified(key, tc.modified)

			namer := namerOf()
			if tc.named {
				namer = namerOf(key)
			}

			got, err := b.Garbage(t.Context(), nsA, namer, now)
			if err != nil {
				t.Fatalf("garbage: %v", err)
			}

			if want := tc.garbage; (len(got) == 1 && got[0] == key) != want || len(got) > 1 {
				t.Errorf("garbage = %v, want the blob: %v", got, want)
			}
		})
	}
}

// garbageNow is the time of the reaper in the tests.
func garbageNow() time.Time {
	return time.Date(2026, time.October, 29, 12, 0, 0, 0, time.UTC)
}

// TestItemKeyBlobsGarbageFailsOnARecordError shows that the reaper rule returns
// no key when it cannot read the records.
func TestItemKeyBlobsGarbageFailsOnARecordError(t *testing.T) {
	t.Parallel()

	b, store, _ := newItemKeyBlobs(t)
	key := mustKey(t, b, nsA, secret.NewItemKeyID(), 1)

	if err := b.Put(t.Context(), nsA, key, []byte("wrapped")); err != nil {
		t.Fatalf("put: %v", err)
	}

	store.setModified(key, garbageNow().Add(-2*secret.ItemKeyBlobGracePeriod))

	failing := func(context.Context, secret.ItemKeyBlob) (bool, error) { return false, errNamerDown }

	got, err := b.Garbage(t.Context(), nsA, failing, garbageNow())
	if !errors.Is(err, errNamerDown) || got != nil {
		t.Errorf("garbage = %v, %v, want no key and the error", got, err)
	}
}

// TestItemKeyBlobsGarbageRefusesBadArguments shows that the reaper rule
// refuses a call with no namer, no time or a bad namespace ID, before the
// store.
func TestItemKeyBlobsGarbageRefusesBadArguments(t *testing.T) {
	t.Parallel()

	cases := []struct {
		wantErr error
		namer   secret.ItemKeyBlobNamer
		now     time.Time
		name    string
		ns      namespace.ID
	}{
		{name: "no namer", ns: nsA, namer: nil, now: garbageNow(), wantErr: secret.ErrMissingDependency},
		{name: "no time", ns: nsA, namer: namerOf(), now: time.Time{}, wantErr: secret.ErrInvalidItemRef},
		{name: "an empty namespace ID", ns: "", namer: namerOf(), now: garbageNow(), wantErr: blob.ErrInvalidKey},
		{
			name:    "a namespace ID in upper case",
			ns:      "ns-A",
			namer:   namerOf(),
			now:     garbageNow(),
			wantErr: blob.ErrInvalidKey,
		},
		{
			name:    "a namespace ID with a slash",
			ns:      "ns-a/b",
			namer:   namerOf(),
			now:     garbageNow(),
			wantErr: blob.ErrInvalidKey,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			b, store, _ := newItemKeyBlobs(t)

			if got, err := b.Garbage(t.Context(), tc.ns, tc.namer, tc.now); !errors.Is(err, tc.wantErr) || got != nil {
				t.Errorf("garbage = %v, %v, want %v", got, err, tc.wantErr)
			}

			if n := store.callCount(); n != 0 {
				t.Errorf("a refused call reached the store %d times, want 0", n)
			}
		})
	}
}

// TestItemKeyBlobsGarbageSkipsForeignKeys shows that the reaper rule never
// returns a key that is not an item key blob key of the namespace. The listing
// of the view holds such keys only after an adapter fault.
func TestItemKeyBlobsGarbageSkipsForeignKeys(t *testing.T) {
	t.Parallel()

	b, store, _ := newItemKeyBlobs(t)
	dir := "namespaces/" + string(nsA) + "/item-keys/"

	for _, bad := range []string{
		dir + "notes",
		dir + "ik-1/v01",
		dir + "ik-1/v1-short",
		dir + "ik-1/v1/" + testAttempt,
	} {
		if err := store.Put(t.Context(), blob.Key(bad), strings.NewReader("x")); err != nil {
			t.Fatalf("seed %s: %v", bad, err)
		}

		store.setModified(blob.Key(bad), garbageNow().Add(-2*secret.ItemKeyBlobGracePeriod))
	}

	got, err := b.Garbage(t.Context(), nsA, namerOf(), garbageNow())
	if err != nil || len(got) != 0 {
		t.Errorf("garbage = %v, %v, want none", got, err)
	}
}
