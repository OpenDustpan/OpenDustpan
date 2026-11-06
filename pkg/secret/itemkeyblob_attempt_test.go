package secret_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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
		{
			name: "a blob from the future inside the clock skew", modified: now.Add(secret.ItemKeyBlobClockSkew),
			named: false, garbage: false,
		},
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

// cancelOnGetBlobs cancels the context of the request when a read returns,
// as a client that disconnects just after the read does.
type cancelOnGetBlobs struct {
	*memBlobs

	cancel context.CancelFunc
}

func (c cancelOnGetBlobs) Get(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	rc, err := c.memBlobs.Get(ctx, key)
	c.cancel()

	return rc, err
}

// liveContextRecorder is a recorder that refuses to record with a context
// that has ended, as an audit store does.
type liveContextRecorder struct {
	*fakeRecorder
}

func (l liveContextRecorder) RecordMismatch(ctx context.Context, ns namespace.ID, op string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("record with an ended context: %w", err)
	}

	return l.fakeRecorder.RecordMismatch(ctx, ns, op)
}

// TestItemKeyBlobsMismatchIsRecordedAfterACancel shows that a client that
// cancels its request just after the read does not suppress the record of a
// mismatch.
func TestItemKeyBlobsMismatchIsRecordedAfterACancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	mem, rec := newMemBlobs(), newFakeRecorder(nil)
	live := liveContextRecorder{fakeRecorder: rec}

	b, err := secret.NewItemKeyBlobs(cancelOnGetBlobs{memBlobs: mem, cancel: cancel}, rec, live)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	key := mustKey(t, b, nsA, secret.NewItemKeyID(), 1)
	info := infoOf(t, key, []byte("original"))

	if putErr := b.Put(t.Context(), nsA, key, []byte("original")); putErr != nil {
		t.Fatalf("put: %v", putErr)
	}

	mem.replace(key, []byte("replaced"))

	got, err := b.Get(ctx, nsA, key, info)
	checkMismatch(t, got, err, key)

	if ctx.Err() == nil {
		t.Fatal("the store did not cancel the request")
	}

	if calls := rec.mismatchCalls(); len(calls) != 1 {
		t.Errorf("mismatch records after a cancel = %d, want 1 (err %v)", len(calls), err)
	}
}

// TestAttemptDeadline shows the deadline of an item key transaction: the
// fixed attempt deadline, or an earlier deadline of the context, and a check
// that refuses a commit at or after it.
func TestAttemptDeadline(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.October, 30, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		want     time.Time
		name     string
		ctxAfter time.Duration
	}{
		{name: "no deadline of the context", ctxAfter: 0, want: start.Add(secret.ItemKeyAttemptDeadline)},
		{name: "an earlier deadline of the context", ctxAfter: time.Minute, want: start.Add(time.Minute)},
		{
			name: "a later deadline of the context", ctxAfter: 2 * secret.ItemKeyAttemptDeadline,
			want: start.Add(secret.ItemKeyAttemptDeadline),
		},
	}

	for _, tc := range cases {
		ctx := t.Context()

		if tc.ctxAfter > 0 {
			var cancel context.CancelFunc

			ctx, cancel = context.WithDeadline(ctx, start.Add(tc.ctxAfter))
			t.Cleanup(cancel)
		}

		if got := secret.AttemptDeadline(ctx, start); !got.Equal(tc.want) {
			t.Errorf("%s: deadline = %v, want %v", tc.name, got, tc.want)
		}
	}

	deadline := start.Add(secret.ItemKeyAttemptDeadline)

	if err := secret.CheckAttemptDeadline(deadline, deadline.Add(-time.Millisecond)); err != nil {
		t.Errorf("a commit before the deadline err = %v, want nil", err)
	}

	for _, at := range []time.Time{deadline, deadline.Add(time.Hour)} {
		if err := secret.CheckAttemptDeadline(deadline, at); !errors.Is(err, secret.ErrItemKeyAttemptExpired) {
			t.Errorf("a commit at %v err = %v, want ErrItemKeyAttemptExpired", at, err)
		}
	}
}

// TestItemKeyBlobsGarbageFailsOnAStoreClockAhead shows that a store clock that
// runs ahead of the server by more than the allowed skew fails the whole run:
// no key is garbage, also an old blob that no record names.
func TestItemKeyBlobsGarbageFailsOnAStoreClockAhead(t *testing.T) {
	t.Parallel()

	b, store, _ := newItemKeyBlobs(t)
	orphan := mustKey(t, b, nsA, secret.NewItemKeyID(), 1)
	ahead := mustKey(t, b, nsA, secret.NewItemKeyID(), 1)

	for key, at := range map[blob.Key]time.Time{
		orphan: garbageNow().Add(-2 * secret.ItemKeyBlobGracePeriod),
		ahead:  garbageNow().Add(secret.ItemKeyBlobClockSkew + time.Second),
	} {
		if err := b.Put(t.Context(), nsA, key, []byte("wrapped")); err != nil {
			t.Fatalf("put: %v", err)
		}

		store.setModified(key, at)
	}

	got, err := b.Garbage(t.Context(), nsA, namerOf(), garbageNow())
	if !errors.Is(err, secret.ErrItemKeyBlobClockSkew) || got != nil {
		t.Errorf("garbage with a store clock ahead = %v, %v, want no key and ErrItemKeyBlobClockSkew", got, err)
	}
}

// TestItemKeyBlobPeriodsLeaveRoomForTheSkew shows the order of the periods of
// the reaper rule: the attempt deadline, plus the lease of an instance, plus the
// allowed clock skew, is shorter than the grace period.
func TestItemKeyBlobPeriodsLeaveRoomForTheSkew(t *testing.T) {
	t.Parallel()

	const lease = 30 * time.Second

	if sum := secret.ItemKeyAttemptDeadline + lease + secret.ItemKeyBlobClockSkew; sum >= secret.ItemKeyBlobGracePeriod {
		t.Errorf("deadline + lease + skew = %v, want less than the grace period %v", sum, secret.ItemKeyBlobGracePeriod)
	}
}

// TestItemKeyBlobsGarbageMeasuresTheStoreClock is the test of the clock
// probe. The store clock runs behind or ahead of the server by the offset. A
// store behind the server makes every blob look older, so without the probe a
// blob of a write that can still commit would look like garbage. The case
// from the review: the blob is put at server time T and stamped T - 40 min,
// the reaper runs at T + 21 min, and no record exists yet.
func TestItemKeyBlobsGarbageMeasuresTheStoreClock(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		offset  time.Duration
		wantErr bool
	}{
		{name: "the store 40 minutes behind", offset: -40 * time.Minute, wantErr: true},
		{
			name:    "the store just past the skew behind",
			offset:  -secret.ItemKeyBlobClockSkew - time.Second,
			wantErr: true,
		},
		{name: "the store inside the skew behind", offset: -secret.ItemKeyBlobClockSkew + time.Second, wantErr: false},
		{name: "the store in step", offset: 0, wantErr: false},
		{name: "the store inside the skew ahead", offset: secret.ItemKeyBlobClockSkew - time.Second, wantErr: false},
		{name: "the store just past the skew ahead", offset: secret.ItemKeyBlobClockSkew + time.Second, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			put := garbageNow()
			reap := put.Add(21 * time.Minute)

			b, store, _ := newItemKeyBlobs(t)
			store.setClock(put.Add(tc.offset))

			key := mustKey(t, b, nsA, secret.NewItemKeyID(), 1)
			if err := b.Put(t.Context(), nsA, key, []byte("wrapped")); err != nil {
				t.Fatalf("put: %v", err)
			}

			store.setClock(reap.Add(tc.offset))

			got, err := b.Garbage(t.Context(), nsA, namerOf(), reap)

			switch {
			case tc.wantErr && (!errors.Is(err, secret.ErrItemKeyBlobClockSkew) || got != nil):
				t.Errorf("garbage = %v, %v, want no key and ErrItemKeyBlobClockSkew", got, err)
			case !tc.wantErr && (err != nil || len(got) != 0):
				t.Errorf("garbage = %v, %v, want no key and no error: the blob is younger than the grace period",
					got, err)
			default:
			}

			if probes := clockProbesIn(store); len(probes) != 0 {
				t.Errorf("the run left its probe: %v", probes)
			}
		})
	}
}

// clockProbesIn returns the probe blobs that the store holds.
func clockProbesIn(store *memBlobs) []blob.Key {
	var probes []blob.Key

	for _, k := range store.keys() {
		if strings.Contains(k.String(), "/item-keys/clock-probe-") {
			probes = append(probes, k)
		}
	}

	return probes
}

// TestItemKeyBlobsGarbageCleansAStaleProbe shows that a probe that an earlier
// run left is garbage once it is older than the grace period, and not before.
func TestItemKeyBlobsGarbageCleansAStaleProbe(t *testing.T) {
	t.Parallel()

	b, store, _ := newItemKeyBlobs(t)
	dir := "namespaces/" + string(nsA) + "/item-keys/"
	old := blob.Key(dir + "clock-probe-" + testAttempt)
	young := blob.Key(dir + "clock-probe-0123456789abcdefghij")

	for key, at := range map[blob.Key]time.Time{
		old:   garbageNow().Add(-2 * secret.ItemKeyBlobGracePeriod),
		young: garbageNow().Add(-time.Minute),
	} {
		if err := store.Put(t.Context(), key, strings.NewReader("")); err != nil {
			t.Fatalf("seed: %v", err)
		}

		store.setModified(key, at)
	}

	got, err := b.Garbage(t.Context(), nsA, namerOf(), garbageNow())
	if err != nil || !slices.Equal(got, []blob.Key{old}) {
		t.Errorf("garbage = %v, %v, want only the old probe %s", got, err, old)
	}
}

// failingPutBlobs refuses every write.
type failingPutBlobs struct {
	*memBlobs
}

func (failingPutBlobs) Put(context.Context, blob.Key, io.Reader) error {
	return errAuditDown
}

// TestItemKeyBlobsGarbageFailsWithoutAProbe shows that a run that cannot put
// its probe returns the error and no key: it cannot know the store clock.
func TestItemKeyBlobsGarbageFailsWithoutAProbe(t *testing.T) {
	t.Parallel()

	mem, rec := newMemBlobs(), newFakeRecorder(nil)

	b, err := secret.NewItemKeyBlobs(failingPutBlobs{memBlobs: mem}, rec, rec)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	orphan := blob.Key("namespaces/" + string(nsA) + "/item-keys/ik-1/v1-" + testAttempt)
	if putErr := mem.Put(t.Context(), orphan, strings.NewReader("x")); putErr != nil {
		t.Fatalf("seed: %v", putErr)
	}

	mem.setModified(orphan, garbageNow().Add(-2*secret.ItemKeyBlobGracePeriod))

	got, err := b.Garbage(t.Context(), nsA, namerOf(), garbageNow())
	if !errors.Is(err, errAuditDown) || got != nil {
		t.Errorf("garbage with no probe = %v, %v, want no key and the error of the put", got, err)
	}
}

// probeHidingBlobs accepts every write, and lists without the probe of a
// reaper run, as a listing that lags a write does.
type probeHidingBlobs struct {
	*memBlobs
}

func (p probeHidingBlobs) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	page, err := p.memBlobs.ListPage(ctx, req)
	if err != nil {
		return blob.Page{}, err
	}

	page.Objects = slices.DeleteFunc(page.Objects, func(o blob.Object) bool {
		return strings.Contains(o.Key.String(), "/item-keys/clock-probe-")
	})

	return page, nil
}

// failingDeleteBlobs refuses every delete.
type failingDeleteBlobs struct {
	*memBlobs
}

func (failingDeleteBlobs) Delete(context.Context, blob.Key) error {
	return errAuditDown
}

// TestItemKeyBlobsGarbageNeedsItsProbe shows that a run fails closed when the
// listing does not hold its probe, so it cannot know the store clock, and when
// it cannot delete its probe. In both cases an old blob that no record names,
// stamped by a store clock 40 minutes behind, is not garbage.
func TestItemKeyBlobsGarbageNeedsItsProbe(t *testing.T) {
	t.Parallel()

	cases := []struct {
		wrap    func(*memBlobs) blob.Store
		wantErr error
		name    string
	}{
		{
			name:    "a listing without the probe",
			wrap:    func(m *memBlobs) blob.Store { return probeHidingBlobs{memBlobs: m} },
			wantErr: secret.ErrItemKeyBlobClockSkew,
		},
		{
			name:    "a probe that does not delete",
			wrap:    func(m *memBlobs) blob.Store { return failingDeleteBlobs{memBlobs: m} },
			wantErr: errAuditDown,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mem, rec := newMemBlobs(), newFakeRecorder(nil)
			mem.setClock(garbageNow().Add(-40 * time.Minute))

			b, err := secret.NewItemKeyBlobs(tc.wrap(mem), rec, rec)
			if err != nil {
				t.Fatalf("new: %v", err)
			}

			key := mustKey(t, b, nsA, secret.NewItemKeyID(), 1)
			if putErr := b.Put(t.Context(), nsA, key, []byte("wrapped")); putErr != nil {
				t.Fatalf("put: %v", putErr)
			}

			mem.setModified(key, garbageNow().Add(-2*secret.ItemKeyBlobGracePeriod))

			got, err := b.Garbage(t.Context(), nsA, namerOf(), garbageNow())
			if !errors.Is(err, tc.wantErr) || got != nil {
				t.Errorf("garbage = %v, %v, want no key and %v", got, err, tc.wantErr)
			}
		})
	}
}
