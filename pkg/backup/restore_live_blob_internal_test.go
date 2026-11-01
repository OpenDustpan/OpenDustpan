package backup

import (
	"bytes"
	"context"
	"crypto"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
)

// mapStore is a blob store in memory. With conditional set it refuses a Put to
// a key that exists with ErrExists, as a store that enforces If-None-Match
// does. Without it, a Put replaces the object, as a store that ignores the
// header does. It counts the Puts to each key.
type mapStore struct {
	objects     map[string][]byte
	puts        map[string]int
	log         *[]string
	mu          sync.Mutex
	conditional bool
}

func newMapStore(conditional bool, log *[]string) *mapStore {
	return &mapStore{
		objects: map[string][]byte{}, puts: map[string]int{}, log: log, mu: sync.Mutex{}, conditional: conditional,
	}
}

func (m *mapStore) Put(_ context.Context, key blob.Key, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read the object: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.puts[key.String()]++

	if m.log != nil && !strings.HasPrefix(key.String(), tempKeyPrefix) {
		*m.log = append(*m.log, "put "+key.String())
	}

	if _, ok := m.objects[key.String()]; ok && m.conditional {
		return blob.ErrExists
	}

	m.objects[key.String()] = data

	return nil
}

func (m *mapStore) Get(_ context.Context, key blob.Key) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, ok := m.objects[key.String()]
	if !ok {
		return nil, blob.ErrNotFound
	}

	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *mapStore) Delete(_ context.Context, key blob.Key) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.objects, key.String())

	return nil
}

func (*mapStore) ListPage(context.Context, blob.PageRequest) (blob.Page, error) {
	return blob.Page{Objects: nil, Next: ""}, nil
}

func (*mapStore) Ping(context.Context) error { return nil }

func (m *mapStore) liveBytes() ([]byte, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, ok := m.objects[liveKey]

	return data, ok
}

func (m *mapStore) liveWrites() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.puts[liveKey]
}

// fenceLive is a RestoreLive that records the audit events and the checks of
// the fence, and nothing else.
type fenceLive struct {
	RestoreLive

	log      *[]string
	events   []string
	eventErr error
	fenceErr error
	// onFence runs at each check of the claim, for a test that changes the
	// store at that moment.
	onFence func()
}

func (l *fenceLive) RecordRestoreEvent(_ context.Context, _ durable.StepFence, _ RestoreID, object string) error {
	if l.eventErr != nil {
		return l.eventErr
	}

	l.events = append(l.events, object)

	return nil
}

func (l *fenceLive) CheckRestoreFence(context.Context, durable.StepFence, RestoreID) error {
	*l.log = append(*l.log, "fence")

	if l.onFence != nil {
		l.onFence()
	}

	return l.fenceErr
}

const liveKey = "namespaces/ns-aaaaaaaaaaaaaaaaaaaa/tofu/ws/sv/a1"

// wantBytes returns the bytes of the blob in the backup.
func wantBytes() []byte { return []byte("the bytes of the backup") }

// wantRef returns the manifest entry of wantBytes. The package registers the
// MD5 function for the manifest.
func wantRef() ManifestBlob {
	h := crypto.MD5.New()
	h.Write(wantBytes())

	return ManifestBlob{
		Key: liveKey, Size: int64(len(wantBytes())), MD5: hex.EncodeToString(h.Sum(nil)), Sealed: false,
	}
}

type liveBlobSetup struct {
	svc    *RestoreService
	live   *fenceLive
	blobs  *mapStore
	target *mapStore
	log    *[]string
}

// newLiveBlobSetup returns a service whose live store enforces conditional
// writes or not, and whose backup target holds the blob of the manifest.
func newLiveBlobSetup(conditional bool) liveBlobSetup {
	log := []string{}
	blobs := newMapStore(conditional, &log)
	target := newMapStore(false, nil)
	target.objects["blobs/"+liveKey] = wantBytes()
	live := &fenceLive{RestoreLive: nil, log: &log, events: nil, eventErr: nil, fenceErr: nil, onFence: nil}

	svc := keyService(live)
	svc.blobs, svc.target, svc.logger = blobs, target, slog.New(slog.DiscardHandler)

	return liveBlobSetup{svc: svc, live: live, blobs: blobs, target: target, log: &log}
}

func (u liveBlobSetup) copyKey(t *testing.T) error {
	t.Helper()

	return u.svc.copyCheckedKey(
		t.Context(),
		keyInput(),
		durable.StepFence{Operation: "op", Token: 1, Step: "copy"},
		wantRef(),
	)
}

// TestCopyCheckedKeyRefusesALiveBlobThatDiffers proves that a live blob with
// other bytes stops the restore with ErrCorrupt on a store that enforces
// conditional writes and on one that ignores them. Nothing is written to the
// live key, and the audit event of the refusal is stored.
func TestCopyCheckedKeyRefusesALiveBlobThatDiffers(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		conditional bool
		stored      []byte
	}{
		{"store that enforces conditional writes, other bytes", true, []byte("other bytes of another size")},
		{"store that ignores conditional writes, other bytes", false, []byte("other bytes of another size")},
		{"store that enforces conditional writes, same size", true, bytes.Repeat([]byte("x"), len(wantBytes()))},
		{"store that ignores conditional writes, same size", false, bytes.Repeat([]byte("x"), len(wantBytes()))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			u := newLiveBlobSetup(tc.conditional)
			u.blobs.objects[liveKey] = tc.stored

			err := u.copyKey(t)
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("copyCheckedKey: err = %v, want ErrCorrupt", err)
			}

			if got, _ := u.blobs.liveBytes(); !bytes.Equal(got, tc.stored) {
				t.Errorf("live bytes = %q, want them unchanged", got)
			}

			if n := u.blobs.liveWrites(); n != 0 {
				t.Errorf("writes to the live key = %d, want 0", n)
			}

			if len(u.live.events) != 1 || !strings.Contains(u.live.events[0], "refused to overwrite live blob") {
				t.Errorf("events = %v, want one refusal", u.live.events)
			}
		})
	}
}

// TestCopyCheckedKeyRefusesALiveBlobThatAppearsDuringTheCopy proves that a
// store that refuses the write because the key exists is not trusted: the
// live key is read again, and other bytes there stop the restore.
func TestCopyCheckedKeyRefusesALiveBlobThatAppearsDuringTheCopy(t *testing.T) {
	t.Parallel()

	u := newLiveBlobSetup(true)
	racer := newAppearing(u, []byte("a writer got there first"), false)
	u.svc.blobs = racer

	err := u.copyKey(t)
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("copyCheckedKey: err = %v, want ErrCorrupt", err)
	}

	if got, _ := u.blobs.liveBytes(); !bytes.Equal(got, racer.data) {
		t.Errorf("live bytes = %q, want the bytes of the other writer", got)
	}

	if len(u.live.events) != 1 {
		t.Errorf("events = %v, want one refusal", u.live.events)
	}
}

// appearingStore stores data under key when the first write to key arrives,
// so the key exists when the copy writes it. With throttle set, a read of key
// after that answers ErrThrottled, as a store does that asks the client to slow
// down.
type appearingStore struct {
	*mapStore

	key      string
	data     []byte
	once     sync.Once
	appeared atomic.Bool
	throttle bool
}

func (a *appearingStore) Put(ctx context.Context, key blob.Key, r io.Reader) error {
	if key.String() == a.key {
		a.once.Do(func() {
			a.mu.Lock()
			a.objects[a.key] = a.data
			a.mu.Unlock()
			a.appeared.Store(true)
		})
	}

	return a.mapStore.Put(ctx, key, r)
}

func (a *appearingStore) Get(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	if a.throttle && a.appeared.Load() && key.String() == a.key {
		return nil, blob.ErrThrottled
	}

	return a.mapStore.Get(ctx, key)
}

func newAppearing(u liveBlobSetup, data []byte, throttle bool) *appearingStore {
	return &appearingStore{
		mapStore: u.blobs, key: liveKey, data: data, once: sync.Once{}, appeared: atomic.Bool{}, throttle: throttle,
	}
}

// flipStore answers other bytes for the reads of a temporary key from the
// read number from on (1 is the first read).
type flipStore struct {
	*mapStore

	reads atomic.Int64
	from  int64
}

func (f *flipStore) Get(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	rc, err := f.mapStore.Get(ctx, key)
	if err != nil || !strings.HasPrefix(key.String(), tempKeyPrefix) {
		return rc, err
	}

	if f.reads.Add(1) < f.from {
		return rc, nil
	}

	if closeErr := rc.Close(); closeErr != nil {
		return nil, fmt.Errorf("close the object: %w", closeErr)
	}

	return io.NopCloser(strings.NewReader("flipped bytes")), nil
}

func (f *flipStore) tempKeys() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	n := 0

	for k := range f.objects {
		if strings.HasPrefix(k, tempKeyPrefix) {
			n++
		}
	}

	return n
}

// TestCopyCheckedKeyChecksTheClaimBeforeTheWrite proves that the claim is
// checked after the bytes are staged and before the write to the live key, on
// a store that enforces conditional writes and on one that ignores them.
func TestCopyCheckedKeyChecksTheClaimBeforeTheWrite(t *testing.T) {
	t.Parallel()

	for _, conditional := range []bool{true, false} {
		t.Run(map[bool]string{true: "enforcing store", false: "ignoring store"}[conditional], func(t *testing.T) {
			t.Parallel()

			u := newLiveBlobSetup(conditional)

			if err := u.copyKey(t); err != nil {
				t.Fatalf("copyCheckedKey: %v", err)
			}

			if got, _ := u.blobs.liveBytes(); !bytes.Equal(got, wantBytes()) {
				t.Errorf("live bytes = %q, want the bytes of the backup", got)
			}

			if want := []string{"fence", "put " + liveKey}; strings.Join(*u.log, ",") != strings.Join(want, ",") {
				t.Errorf("order = %v, want the claim check and then the write of the live key", *u.log)
			}
		})
	}
}

// TestCopyCheckedKeyStopsWhenTheClaimIsStale proves that a stale fence stops
// the copy before it writes the live key, and removes the temporary key.
func TestCopyCheckedKeyStopsWhenTheClaimIsStale(t *testing.T) {
	t.Parallel()

	for _, conditional := range []bool{true, false} {
		t.Run(map[bool]string{true: "enforcing store", false: "ignoring store"}[conditional], func(t *testing.T) {
			t.Parallel()

			u := newLiveBlobSetup(conditional)
			u.live.fenceErr = durable.ErrClaimLost

			err := u.copyKey(t)
			if !errors.Is(err, durable.ErrClaimLost) {
				t.Fatalf("copyCheckedKey: err = %v, want ErrClaimLost", err)
			}

			if _, ok := u.blobs.liveBytes(); ok || u.blobs.liveWrites() != 0 {
				t.Error("the live key was written, want no write")
			}

			for k := range u.blobs.objects {
				if strings.HasPrefix(k, tempKeyPrefix) {
					t.Errorf("temporary key %s stays", k)
				}
			}
		})
	}
}

// TestCopyCheckedKeyKeepsAnEqualLiveBlob proves that a live blob with the
// bytes of the manifest stays: no write and no claim check.
func TestCopyCheckedKeyKeepsAnEqualLiveBlob(t *testing.T) {
	t.Parallel()

	u := newLiveBlobSetup(true)
	u.blobs.objects[liveKey] = wantBytes()

	if err := u.copyKey(t); err != nil {
		t.Fatalf("copyCheckedKey: %v", err)
	}

	if len(*u.log) != 0 || len(u.live.events) != 0 {
		t.Errorf("log %v and events %v, want none", *u.log, u.live.events)
	}
}

// TestRefuseDifferingBlobFailsClosed proves that the refusal stands when its
// audit event is lost, and that a lost claim stops the step instead.
func TestRefuseDifferingBlobFailsClosed(t *testing.T) {
	t.Parallel()

	u := newLiveBlobSetup(false)
	u.blobs.objects[liveKey] = []byte("other")
	u.live.eventErr = errEventStore

	err := u.copyKey(t)
	if !errors.Is(err, ErrCorrupt) || !errors.Is(err, errEventStore) {
		t.Fatalf("lost event: err = %v, want ErrCorrupt joined to the store error", err)
	}

	u.live.eventErr = durable.ErrClaimLost

	err = u.copyKey(t)
	if !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("lost claim: err = %v, want ErrClaimLost", err)
	}

	if got, _ := u.blobs.liveBytes(); string(got) != "other" {
		t.Errorf("live bytes = %q, want them unchanged", got)
	}
}

// TestCopyCheckedKeyChecksTheStagedBytesBeforeTheWrite proves that staged bytes
// that differ from the manifest stop the restore before the live key is
// written: the live key stays absent, only the temporary key is deleted, and
// the audit event says what happened.
func TestCopyCheckedKeyChecksTheStagedBytesBeforeTheWrite(t *testing.T) {
	t.Parallel()

	for _, conditional := range []bool{true, false} {
		t.Run(map[bool]string{true: "enforcing store", false: "ignoring store"}[conditional], func(t *testing.T) {
			t.Parallel()

			u := newLiveBlobSetup(conditional)
			flip := &flipStore{mapStore: u.blobs, reads: atomic.Int64{}, from: 1}
			u.svc.blobs = flip

			err := u.copyKey(t)
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("copyCheckedKey: err = %v, want ErrCorrupt", err)
			}

			if _, ok := u.blobs.liveBytes(); ok || u.blobs.liveWrites() != 0 {
				t.Error("the live key was written, want no write")
			}

			if n := flip.tempKeys(); n != 0 {
				t.Errorf("temporary keys = %d, want 0", n)
			}

			if len(u.live.events) != 1 || !strings.Contains(u.live.events[0], "the staged copy differs") ||
				strings.Contains(u.live.events[0], "refused to overwrite") {
				t.Errorf("events = %v, want one event that names the staged copy", u.live.events)
			}
		})
	}
}

// TestCopyCheckedKeyRecordsBytesThatFailTheCheckAfterTheWrite proves that bytes
// that pass the check before the write and fail it after the write fail the
// restore with an event that says so. The restore wrote the live key, and the
// event does not claim that it refused to overwrite it.
func TestCopyCheckedKeyRecordsBytesThatFailTheCheckAfterTheWrite(t *testing.T) {
	t.Parallel()

	u := newLiveBlobSetup(false)
	u.svc.blobs = &flipStore{mapStore: u.blobs, reads: atomic.Int64{}, from: 2}

	err := u.copyKey(t)
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("copyCheckedKey: err = %v, want ErrCorrupt", err)
	}

	if len(u.live.events) != 1 || !strings.Contains(u.live.events[0], "failed the check after the write") {
		t.Errorf("events = %v, want one event that names the check after the write", u.live.events)
	}
}

// TestCopyCheckedKeyDoesNotCallAThrottledReadCorrupt proves that a read error
// after a refused write is not corruption: the error returns as it is, so the
// step retries, and no event says that bytes differ.
func TestCopyCheckedKeyDoesNotCallAThrottledReadCorrupt(t *testing.T) {
	t.Parallel()

	u := newLiveBlobSetup(true)
	u.svc.blobs = newAppearing(u, []byte("a writer got there first"), true)

	err := u.copyKey(t)
	if !errors.Is(err, blob.ErrThrottled) || errors.Is(err, ErrCorrupt) {
		t.Fatalf("copyCheckedKey: err = %v, want ErrThrottled and not ErrCorrupt", err)
	}

	if len(u.live.events) != 0 {
		t.Errorf("events = %v, want none", u.live.events)
	}
}

// TestCopyCheckedKeyReadsTheLiveKeyAgainAfterTheClaimCheck proves that, on a
// store that ignores conditional writes, a key that appears after the first
// read and before the claim check is seen: the restore stops and writes nothing
// over it.
func TestCopyCheckedKeyReadsTheLiveKeyAgainAfterTheClaimCheck(t *testing.T) {
	t.Parallel()

	u := newLiveBlobSetup(false)
	other := []byte("a writer got there first")
	u.live.onFence = func() {
		u.blobs.mu.Lock()
		u.blobs.objects[liveKey] = other
		u.blobs.mu.Unlock()
	}

	err := u.copyKey(t)
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("copyCheckedKey: err = %v, want ErrCorrupt", err)
	}

	if got, _ := u.blobs.liveBytes(); !bytes.Equal(got, other) || u.blobs.liveWrites() != 0 {
		t.Errorf("live bytes = %q with %d writes, want the other bytes and no write", got, u.blobs.liveWrites())
	}

	if len(u.live.events) != 1 || !strings.Contains(u.live.events[0], "refused to overwrite live blob") {
		t.Errorf("events = %v, want one refusal", u.live.events)
	}
}

// TestCopyCheckedKeyKeepsTheGapThatMaintenanceModeCloses pins the behavior that
// remains: on a store that ignores conditional writes, a key that appears after
// the last read and before the write is replaced with no error. Maintenance
// mode stops other writers while the copy steps run, and the guide says so.
func TestCopyCheckedKeyKeepsTheGapThatMaintenanceModeCloses(t *testing.T) {
	t.Parallel()

	u := newLiveBlobSetup(false)
	u.svc.blobs = newAppearing(u, []byte("a writer got there first"), false)

	if err := u.copyKey(t); err != nil {
		t.Fatalf("copyCheckedKey: %v", err)
	}

	if got, _ := u.blobs.liveBytes(); !bytes.Equal(got, wantBytes()) {
		t.Errorf("live bytes = %q, want the bytes of the backup", got)
	}
}
