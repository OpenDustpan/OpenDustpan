package secret_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/scopeaudit"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

const (
	nsA = namespace.ID("ns-aaaaaaaaaaaaaaaaaaaa")
	nsB = namespace.ID("ns-bbbbbbbbbbbbbbbbbbbb")
)

// memBlobs is a blob.Store in memory that counts the calls that reach it.
// Put stamps each object with the time of the clock of the store, which
// setClock changes.
type memBlobs struct {
	objects  map[blob.Key][]byte
	modified map[blob.Key]time.Time
	clock    time.Time
	calls    int
	mu       sync.Mutex
}

func newMemBlobs() *memBlobs {
	return &memBlobs{
		objects: map[blob.Key][]byte{}, modified: map[blob.Key]time.Time{}, clock: garbageNow(), calls: 0,
		mu: sync.Mutex{},
	}
}

func (m *memBlobs) Put(_ context.Context, key blob.Key, r io.Reader) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls++

	if _, ok := m.objects[key]; ok {
		return blob.ErrExists
	}

	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}

	m.objects[key] = data
	m.modified[key] = m.clock

	return nil
}

func (m *memBlobs) Get(_ context.Context, key blob.Key) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls++

	data, ok := m.objects[key]
	if !ok {
		return nil, blob.ErrNotFound
	}

	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *memBlobs) Delete(_ context.Context, key blob.Key) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls++
	delete(m.objects, key)

	return nil
}

// ListPage implements blob.Store: it cuts the page that req selects from the
// objects of list.
func (m *memBlobs) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	if err := req.Validate(); err != nil {
		return blob.Page{}, err
	}

	objects, err := m.list(ctx, req.Prefix)
	if err != nil {
		return blob.Page{}, err
	}

	slices.SortFunc(objects, func(a, b blob.Object) int { return strings.Compare(a.Key.String(), b.Key.String()) })

	return blob.PageOf(objects, req), nil
}

func (m *memBlobs) Ping(context.Context) error { return nil }

func (m *memBlobs) list(_ context.Context, prefix string) ([]blob.Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls++

	prefix = strings.TrimSuffix(prefix, "/") + "/"

	var out []blob.Object

	for k := range m.objects {
		if strings.HasPrefix(k.String(), prefix) {
			out = append(out, blob.Object{Key: k, ModifiedAt: m.modified[k]})
		}
	}

	slices.SortFunc(out, func(a, b blob.Object) int { return strings.Compare(a.Key.String(), b.Key.String()) })

	return out, nil
}

// setModified sets the time that List gives for key.
func (m *memBlobs) setModified(key blob.Key, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.modified[key] = at
}

// setClock sets the time that the store stamps on the next writes.
func (m *memBlobs) setClock(at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.clock = at
}

// keys returns the keys that the store holds.
func (m *memBlobs) keys() []blob.Key {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]blob.Key, 0, len(m.objects))
	for k := range m.objects {
		out = append(out, k)
	}

	return out
}

// replace overwrites the object under key, as a store that ignores a
// conditional write does.
func (m *memBlobs) replace(key blob.Key, data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.objects[key] = bytes.Clone(data)
}

func (m *memBlobs) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.calls
}

var errAuditDown = errors.New("audit store is down")

// refusal is one call of a recorder.
type refusal struct {
	op    string
	where scopeaudit.Where
	ns    namespace.ID
}

// fakeRecorder is a scopeaudit.Recorder and a scopeaudit.IntegrityRecorder
// that keeps its calls.
type fakeRecorder struct {
	err        error
	calls      []refusal
	mismatches []refusal
	mu         sync.Mutex
}

func (f *fakeRecorder) RecordOutOfScope(
	_ context.Context, ns namespace.ID, op string, where scopeaudit.Where,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, refusal{ns: ns, op: op, where: where})

	return f.err
}

func (f *fakeRecorder) RecordMismatch(_ context.Context, ns namespace.ID, op string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.mismatches = append(f.mismatches, refusal{ns: ns, op: op, where: ""})

	return f.err
}

func (f *fakeRecorder) refusals() []refusal {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.calls)
}

func (f *fakeRecorder) mismatchCalls() []refusal {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.mismatches)
}

func newFakeRecorder(err error) *fakeRecorder {
	return &fakeRecorder{err: err, calls: nil, mismatches: nil, mu: sync.Mutex{}}
}

func newItemKeyBlobs(t *testing.T) (*secret.ItemKeyBlobs, *memBlobs, *fakeRecorder) {
	t.Helper()

	store, rec := newMemBlobs(), newFakeRecorder(nil)

	b, err := secret.NewItemKeyBlobs(store, rec, rec)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	return b, store, rec
}

// mustKey returns a new blob key for a write of version of id in ns, with a
// new attempt part.
func mustKey(t *testing.T, b *secret.ItemKeyBlobs, ns namespace.ID, id secret.ItemKeyID, version int) blob.Key {
	t.Helper()

	key, _, err := b.NewKey(ns, id, version)
	if err != nil {
		t.Fatalf("key v%d: %v", version, err)
	}

	return key
}

// infoOf returns the blob info that a record of key with the bytes data holds.
func infoOf(t *testing.T, key blob.Key, data []byte) secret.ItemKeyBlobInfo {
	t.Helper()

	parsed, err := secret.ParseItemKeyBlobKey(key)
	if err != nil {
		t.Fatalf("parse %s: %v", key, err)
	}

	d := blob.DigestBytes(data)

	return secret.ItemKeyBlobInfo{Version: parsed.Version, Attempt: parsed.Attempt, Size: d.Size, MD5: d.MD5}
}

// testAttempt is a valid attempt part of the tests.
const testAttempt = "abcdefghij0123456789"

func TestNewItemKeyBlobsNeedsItsPorts(t *testing.T) {
	t.Parallel()

	rec := newFakeRecorder(nil)

	if _, err := secret.NewItemKeyBlobs(nil, rec, rec); !errors.Is(err, secret.ErrMissingDependency) {
		t.Errorf("nil store err = %v, want ErrMissingDependency", err)
	}

	if _, err := secret.NewItemKeyBlobs(newMemBlobs(), nil, rec); !errors.Is(err, secret.ErrMissingDependency) {
		t.Errorf("nil recorder err = %v, want ErrMissingDependency", err)
	}

	if _, err := secret.NewItemKeyBlobs(newMemBlobs(), rec, nil); !errors.Is(err, secret.ErrMissingDependency) {
		t.Errorf("nil mismatch recorder err = %v, want ErrMissingDependency", err)
	}
}

func TestItemKeyBlobsKey(t *testing.T) {
	t.Parallel()

	b, store, _ := newItemKeyBlobs(t)

	cases := []struct {
		wantErr error
		name    string
		ns      namespace.ID
		id      secret.ItemKeyID
		attempt string
		want    string
		version int
	}{
		{
			name: "version one", ns: nsA, id: "ik-abc", version: 1, attempt: testAttempt, wantErr: nil,
			want: "namespaces/" + string(nsA) + "/item-keys/ik-abc/v1-" + testAttempt,
		},
		{
			name: "a later version", ns: nsB, id: "ik-abc", version: 12, attempt: testAttempt, wantErr: nil,
			want: "namespaces/" + string(nsB) + "/item-keys/ik-abc/v12-" + testAttempt,
		},
		{
			name: "no attempt part", ns: nsA, id: "ik-abc", version: 1, attempt: "",
			want: "", wantErr: secret.ErrInvalidItemRef,
		},
		{
			name: "a short attempt part", ns: nsA, id: "ik-abc", version: 1, attempt: "abc", want: "",
			wantErr: secret.ErrInvalidItemRef,
		},
		{
			name:    "an attempt part in upper case",
			ns:      nsA,
			id:      "ik-abc",
			version: 1,
			attempt: strings.ToUpper(testAttempt),
			want:    "",
			wantErr: secret.ErrInvalidItemRef,
		},
		{
			name: "an attempt part with a slash", ns: nsA, id: "ik-abc", version: 1, attempt: "abcdefghij/123456789",
			want: "", wantErr: secret.ErrInvalidItemRef,
		},
		{
			name: "an attempt part with a hyphen", ns: nsA, id: "ik-abc", version: 1, attempt: "abcdefghij-123456789",
			want: "", wantErr: secret.ErrInvalidItemRef,
		},
		{
			name:    "version zero",
			ns:      nsA,
			id:      "ik-abc",
			attempt: "",
			version: 0,
			want:    "",
			wantErr: secret.ErrInvalidItemRef,
		},
		{
			name:    "a negative version",
			ns:      nsA,
			id:      "ik-abc",
			attempt: "",
			version: -1,
			want:    "",
			wantErr: secret.ErrInvalidItemRef,
		},
		{
			name:    "a huge version",
			ns:      nsA,
			id:      "ik-abc",
			attempt: "",
			version: 1 << 40,
			want:    "",
			wantErr: secret.ErrInvalidItemRef,
		},
		{
			name:    "an ID without the prefix",
			ns:      nsA,
			id:      "key-1",
			attempt: "",
			version: 1,
			want:    "",
			wantErr: secret.ErrInvalidItemRef,
		},
		{
			name:    "an ID with a slash",
			ns:      nsA,
			id:      "ik-a/b",
			attempt: "",
			version: 1,
			want:    "",
			wantErr: secret.ErrInvalidItemRef,
		},
		{name: "an empty ID", ns: nsA, id: "", attempt: "", version: 1, want: "", wantErr: secret.ErrInvalidItemRef},
		{
			name:    "an empty namespace ID",
			ns:      "",
			id:      "ik-abc",
			attempt: testAttempt,
			version: 1,
			want:    "",
			wantErr: blob.ErrInvalidKey,
		},
		{
			name:    "a namespace ID with a slash",
			ns:      "ns-a/../ns-b",
			id:      "ik-abc",
			attempt: testAttempt,
			version: 1,
			want:    "",
			wantErr: blob.ErrInvalidKey,
		},
		{
			name:    "a namespace ID with upper case",
			ns:      "ns-AAAA",
			id:      "ik-abc",
			attempt: testAttempt,
			version: 1,
			want:    "",
			wantErr: blob.ErrInvalidKey,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			key, err := b.Key(tc.ns, tc.id, tc.version, tc.attempt)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Key err = %v, want %v", err, tc.wantErr)
			}

			if key.String() != tc.want {
				t.Errorf("Key = %q, want %q", key, tc.want)
			}
		})
	}

	if n := store.callCount(); n != 0 {
		t.Errorf("Key reached the store %d times, want 0", n)
	}
}

func TestItemKeyBlobsPutGetDelete(t *testing.T) {
	t.Parallel()

	b, _, rec := newItemKeyBlobs(t)
	id := secret.NewItemKeyID()
	keyV1 := mustKey(t, b, nsA, id, 1)
	wrapped := []byte("wrapped-key-bytes")
	info := infoOf(t, keyV1, wrapped)

	if _, err := b.Get(t.Context(), nsA, keyV1, info); !errors.Is(err, secret.ErrKeyNotFound) {
		t.Fatalf("get before put err = %v, want ErrKeyNotFound", err)
	}

	if err := b.Put(t.Context(), nsA, keyV1, wrapped); err != nil {
		t.Fatalf("put: %v", err)
	}

	if err := b.Put(t.Context(), nsA, keyV1, []byte("other")); !errors.Is(err, blob.ErrExists) {
		t.Errorf("second put err = %v, want ErrExists", err)
	}

	if got, err := b.Get(t.Context(), nsA, keyV1, info); err != nil || !bytes.Equal(got, wrapped) {
		t.Fatalf("get = %q, %v, want %q", got, err, wrapped)
	}

	if err := b.Delete(t.Context(), nsA, keyV1); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if err := b.Delete(t.Context(), nsA, keyV1); err != nil {
		t.Errorf("second delete: %v", err)
	}

	if _, err := b.Get(t.Context(), nsA, keyV1, info); !errors.Is(err, secret.ErrKeyNotFound) {
		t.Errorf("get after delete err = %v, want ErrKeyNotFound", err)
	}

	if n := len(rec.refusals()); n != 0 {
		t.Errorf("recorder called %d times for valid calls, want 0", n)
	}
}

func TestItemKeyBlobsVersions(t *testing.T) {
	t.Parallel()

	b, _, _ := newItemKeyBlobs(t)
	id := secret.NewItemKeyID()
	keyV1, keyV2 := mustKey(t, b, nsA, id, 1), mustKey(t, b, nsA, id, 2)

	for _, key := range []blob.Key{keyV1, keyV2} {
		if err := b.Put(t.Context(), nsA, key, []byte("k")); err != nil {
			t.Fatalf("put %s: %v", key, err)
		}
	}

	got, err := b.Versions(t.Context(), nsA, id)
	if err != nil || !slices.Equal(got, []blob.Key{keyV1, keyV2}) {
		t.Fatalf("versions = %v, %v, want [%s %s]", got, err, keyV1, keyV2)
	}

	// Another namespace lists none of them.
	if other, otherErr := b.Versions(t.Context(), nsB, id); otherErr != nil || len(other) != 0 {
		t.Errorf("versions in the other namespace = %v, %v, want none", other, otherErr)
	}
}

// TestItemKeyBlobsVersionsOrder proves that the versions come in the order of
// the version number. The order of the key text puts v10 before v2.
func TestItemKeyBlobsVersionsOrder(t *testing.T) {
	t.Parallel()

	b, _, _ := newItemKeyBlobs(t)
	id := secret.NewItemKeyID()

	versions := []int{1, 2, 3, 9, 10, 11, 100}
	want := make([]blob.Key, 0, len(versions))

	for _, version := range versions {
		key := mustKey(t, b, nsA, id, version)
		want = append(want, key)
	}

	// Write them in a mixed order, so the order of the writes does not help.
	for _, i := range []int{4, 0, 6, 2, 5, 1, 3} {
		if err := b.Put(t.Context(), nsA, want[i], []byte("k")); err != nil {
			t.Fatalf("put %s: %v", want[i], err)
		}
	}

	got, err := b.Versions(t.Context(), nsA, id)
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("versions = %v, %v, want %v", got, err, want)
	}
}

// TestItemKeyBlobsVersionsRefuseABadListing proves that a listing which holds a
// key that is not a version of the item key is an error, and not a key for the
// purge. The fault is a key that sits under the prefix of the item key in the
// store.
func TestItemKeyBlobsVersionsRefuseABadListing(t *testing.T) {
	t.Parallel()

	id := secret.NewItemKeyID()
	dir := "namespaces/" + string(nsA) + "/item-keys/" + string(id) + "/"

	for name, bad := range map[string]string{
		"a version with a leading zero":   dir + "v01",
		"a version with no number":        dir + "v",
		"a name that is not a version":    dir + "notes",
		"a deeper key":                    dir + "v1/extra",
		"version zero":                    dir + "v0",
		"a short attempt part":            dir + "v1-abc",
		"an empty attempt part":           dir + "v1-",
		"an attempt part with no version": dir + "v-" + testAttempt,
		"no attempt part":                 dir + "v1",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			b, store, _ := newItemKeyBlobs(t)

			if err := b.Put(t.Context(), nsA, mustKey(t, b, nsA, id, 1), []byte("k")); err != nil {
				t.Fatalf("put: %v", err)
			}

			if err := store.Put(t.Context(), blob.Key(bad), strings.NewReader("x")); err != nil {
				t.Fatalf("seed: %v", err)
			}

			keys, err := b.Versions(t.Context(), nsA, id)
			if !errors.Is(err, secret.ErrInvalidItemRef) {
				t.Fatalf("versions = %v, %v, want ErrInvalidItemRef", keys, err)
			}

			if len(keys) != 0 {
				t.Errorf("versions returned %v with the error, want none", keys)
			}
		})
	}
}

func TestItemKeyBlobsSizeLimits(t *testing.T) {
	t.Parallel()

	b, store, _ := newItemKeyBlobs(t)
	id := secret.NewItemKeyID()
	key := mustKey(t, b, nsA, id, 1)
	big := make([]byte, secret.MaxWrappedItemKeyBytes+1)

	if err := b.Put(t.Context(), nsA, key, big); !errors.Is(err, secret.ErrTooLarge) {
		t.Errorf("put of a large value err = %v, want ErrTooLarge", err)
	}

	if n := store.callCount(); n != 0 {
		t.Errorf("a refused put reached the store %d times, want 0", n)
	}

	if err := b.Put(t.Context(), nsA, key, big[:secret.MaxWrappedItemKeyBytes]); err != nil {
		t.Errorf("put at the limit: %v", err)
	}

	// A blob that grew past the limit in the store, for example by a fault.
	key2 := mustKey(t, b, nsA, id, 2)

	if err := store.Put(t.Context(), key2, bytes.NewReader(big)); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := b.Get(t.Context(), nsA, key2, infoOf(t, key2, big[:1])); !errors.Is(err, secret.ErrTooLarge) {
		t.Errorf("get of a large blob err = %v, want ErrTooLarge", err)
	}
}

// foreignKey is a key that a call in nsA must refuse, and the place that the
// refusal names: outside the namespace, or inside it and outside its item-keys
// directory.
type foreignKey struct {
	key   blob.Key
	where scopeaudit.Where
}

// foreignKeys returns keys that a call in nsA must refuse, with a name for
// each. Each key is a plain conversion, so no check of ParseKey ran on it.
func foreignKeys(id secret.ItemKeyID) map[string]foreignKey {
	a, other, tail := string(nsA), string(nsB), "/item-keys/"+string(id)+"/v1"
	outside, inside := scopeaudit.OutsideNamespace, scopeaudit.OutsideDirectory

	return map[string]foreignKey{
		"the item key of another namespace": {blob.Key("namespaces/" + other + tail), outside},
		"a later version in another namespace": {
			blob.Key("namespaces/" + other + "/item-keys/" + string(id) + "/v9"),
			outside,
		},
		"a string prefix of the namespace":        {blob.Key("namespaces/" + a + "x" + tail), outside},
		"the parent of the namespace":             {blob.Key("namespaces" + tail), outside},
		"the old layout":                          {blob.Key("item-keys/" + string(id)), outside},
		"the old layout with a version":           {blob.Key("item-keys/" + string(id) + "/v1"), outside},
		"another directory of the namespace":      {blob.Key("namespaces/" + a + "/tofu/ws-1/sv-1/1"), inside},
		"a parent segment out of the namespace":   {blob.Key("namespaces/" + a + "/../" + other + tail), outside},
		"the item-keys directory without a slash": {blob.Key("namespaces/" + a + "/item-keys"), inside},
		"an empty key":                            {"", outside},
		"a key with a line break":                 {blob.Key("namespaces/" + other + "/item-keys/ik-x\n/v1"), outside},
		"a key in upper case": {
			blob.Key("NAMESPACES/" + other + "/ITEM-KEYS/" + string(id) + "/v1"),
			outside,
		},
	}
}

// itemKeyOps are the blob calls of the service that take a key.
func itemKeyOps() map[string]func(ctx context.Context, b *secret.ItemKeyBlobs, key blob.Key) error {
	return map[string]func(ctx context.Context, b *secret.ItemKeyBlobs, key blob.Key) error{
		"get": func(ctx context.Context, b *secret.ItemKeyBlobs, key blob.Key) error {
			_, err := b.Get(ctx, nsA, key, secret.ItemKeyBlobInfo{Version: 1, Attempt: "", Size: 1, MD5: [16]byte{}})

			return err
		},
		"put": func(ctx context.Context, b *secret.ItemKeyBlobs, key blob.Key) error {
			return b.Put(ctx, nsA, key, []byte("x"))
		},
		"delete": func(ctx context.Context, b *secret.ItemKeyBlobs, key blob.Key) error {
			return b.Delete(ctx, nsA, key)
		},
	}
}

// TestItemKeyBlobsRefuseAKeyOfAnotherNamespace is the blob scope test of the
// isolation decision: each blob read, write and delete of the item key service
// with a key of another namespace returns blob.ErrOutOfScope, before any call
// to the store, and writes one refusal that holds no key.
func TestItemKeyBlobsRefuseAKeyOfAnotherNamespace(t *testing.T) {
	t.Parallel()

	id := secret.NewItemKeyID()

	for opName, call := range itemKeyOps() {
		for keyName, foreign := range foreignKeys(id) {
			t.Run(opName+" with "+keyName, func(t *testing.T) {
				t.Parallel()

				b, store, rec := newItemKeyBlobs(t)

				// The blob of the other namespace exists, so a leak would show.
				other := mustKey(t, b, nsB, id, 1)
				if err := store.Put(t.Context(), other, strings.NewReader("secret")); err != nil {
					t.Fatalf("seed: %v", err)
				}

				before := store.callCount()
				err := call(t.Context(), b, foreign.key)

				assertRefusal(t, err, rec, refusal{ns: nsA, op: opName, where: foreign.where})

				if n := store.callCount(); n != before {
					t.Errorf("%s reached the store %d times, want 0", opName, n-before)
				}
			})
		}
	}
}

// assertRefusal checks that err is blob.ErrOutOfScope and that the recorder
// holds exactly the one refusal want. There is no other way out: a foreign key
// that gave ErrInvalidItemRef or no record would fail here.
func assertRefusal(t *testing.T, err error, rec *fakeRecorder, want refusal) {
	t.Helper()

	if !errors.Is(err, blob.ErrOutOfScope) {
		t.Fatalf("%s err = %v, want ErrOutOfScope", want.op, err)
	}

	if got := rec.refusals(); !slices.Equal(got, []refusal{want}) {
		t.Errorf("refusals = %v, want exactly %v", got, want)
	}

	if strings.Contains(err.Error(), string(nsB)) {
		t.Errorf("error %q names the other namespace", err)
	}
}

func TestItemKeyBlobsOtherNamespaceKeyLeavesTheBlobAlone(t *testing.T) {
	t.Parallel()

	b, store, rec := newItemKeyBlobs(t)
	other := mustKey(t, b, nsB, secret.NewItemKeyID(), 1)
	otherInfo := infoOf(t, other, []byte("b-secret"))

	if err := b.Put(t.Context(), nsB, other, []byte("b-secret")); err != nil {
		t.Fatalf("put in B: %v", err)
	}

	before := store.callCount()

	for name, call := range itemKeyOps() {
		if err := call(t.Context(), b, other); !errors.Is(err, blob.ErrOutOfScope) {
			t.Errorf("%s err = %v, want ErrOutOfScope", name, err)
		}
	}

	if n := store.callCount(); n != before {
		t.Errorf("refused calls reached the store %d times", n-before)
	}

	if n := len(rec.refusals()); n != len(itemKeyOps()) {
		t.Errorf("refusals = %d, want %d", n, len(itemKeyOps()))
	}

	if got, err := b.Get(t.Context(), nsB, other, otherInfo); err != nil || string(got) != "b-secret" {
		t.Errorf("blob of B = %q, %v", got, err)
	}
}

func TestItemKeyBlobsRefuseWhenTheRecorderFails(t *testing.T) {
	t.Parallel()

	store, rec := newMemBlobs(), newFakeRecorder(errAuditDown)

	b, err := secret.NewItemKeyBlobs(store, rec, rec)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	key := mustKey(t, b, nsB, secret.NewItemKeyID(), 1)

	_, getErr := b.Get(t.Context(), nsA, key, infoOf(t, key, []byte("x")))
	if !errors.Is(getErr, blob.ErrOutOfScope) || !errors.Is(getErr, errAuditDown) {
		t.Errorf("get err = %v, want ErrOutOfScope and the recorder error", getErr)
	}

	if n := store.callCount(); n != 0 {
		t.Errorf("store calls = %d, want 0", n)
	}
}

func TestItemKeyBlobsRefuseAMalformedKeyOfTheNamespace(t *testing.T) {
	t.Parallel()

	prefix := "namespaces/" + string(nsA) + "/item-keys/"

	cases := map[string]string{
		"no version":               prefix + "ik-abc",
		"no attempt part":          prefix + "ik-abc/v1",
		"a version of zero":        prefix + "ik-abc/v0",
		"a version with a +":       prefix + "ik-abc/v+1",
		"a padded version":         prefix + "ik-abc/v01",
		"no v in the version":      prefix + "ik-abc/1",
		"a deep key":               prefix + "ik-abc/v1/x",
		"an ID without prefix":     prefix + "key-1/v1",
		"a huge version":           prefix + "ik-abc/v" + strconv.Itoa(1<<30),
		"a short attempt part":     prefix + "ik-abc/v1-abc",
		"an attempt in upper case": prefix + "ik-abc/v1-" + strings.ToUpper(testAttempt),
		"two attempt parts":        prefix + "ik-abc/v1-" + testAttempt + "-" + testAttempt,
	}

	for name, key := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			b, store, rec := newItemKeyBlobs(t)

			for opName, call := range itemKeyOps() {
				if err := call(t.Context(), b, blob.Key(key)); !errors.Is(err, secret.ErrInvalidItemRef) {
					t.Errorf("%s err = %v, want ErrInvalidItemRef", opName, err)
				}
			}

			if n := store.callCount(); n != 0 {
				t.Errorf("store calls = %d, want 0", n)
			}

			if n := len(rec.refusals()); n != 0 {
				t.Errorf("a key of the namespace is not a refusal, recorder called %d times", n)
			}
		})
	}
}

func TestItemKeyBlobsRefuseABadNamespaceID(t *testing.T) {
	t.Parallel()

	b, store, _ := newItemKeyBlobs(t)
	key := blob.Key("namespaces/" + string(nsA) + "/item-keys/ik-abc/v1-" + testAttempt)

	for _, ns := range []namespace.ID{"", "ns-", "ns-A", "ns-a/b", "ns-a b"} {
		if _, err := b.Get(t.Context(), ns, key, infoOf(t, key, []byte("x"))); !errors.Is(err, blob.ErrInvalidKey) {
			t.Errorf("get with namespace %q err = %v, want ErrInvalidKey", ns, err)
		}

		if _, err := b.Versions(t.Context(), ns, "ik-abc"); !errors.Is(err, blob.ErrInvalidKey) {
			t.Errorf("versions with namespace %q err = %v, want ErrInvalidKey", ns, err)
		}
	}

	if _, err := b.Versions(t.Context(), nsA, "bad"); !errors.Is(err, secret.ErrInvalidItemRef) {
		t.Errorf("versions of a bad ID err = %v, want ErrInvalidItemRef", err)
	}

	if n := store.callCount(); n != 0 {
		t.Errorf("store calls = %d, want 0", n)
	}
}

func TestParseItemKeyBlobKey(t *testing.T) {
	t.Parallel()

	attempt := func(ns namespace.ID, id secret.ItemKeyID, v int) *secret.ItemKeyBlob {
		return &secret.ItemKeyBlob{Namespace: ns, ID: id, Version: v, Attempt: testAttempt}
	}

	cases := []struct {
		want *secret.ItemKeyBlob
		name string
		key  string
	}{
		{name: "no attempt part", key: "namespaces/ns-abc/item-keys/ik-1/v1", want: nil},
		{
			name: "an attempt part", key: "namespaces/ns-abc/item-keys/ik-1/v1-" + testAttempt,
			want: attempt("ns-abc", "ik-1", 1),
		},
		{
			name: "a rewrap with an attempt part", key: "namespaces/ns-abc/item-keys/ik-1/v7-" + testAttempt,
			want: attempt("ns-abc", "ik-1", 7),
		},
		{name: "a short attempt part", key: "namespaces/ns-abc/item-keys/ik-1/v1-abc", want: nil},
		{name: "an empty attempt part", key: "namespaces/ns-abc/item-keys/ik-1/v1-", want: nil},
		{name: "no version before the attempt", key: "namespaces/ns-abc/item-keys/ik-1/v-" + testAttempt, want: nil},
		{name: "the old layout", key: "item-keys/ik-1", want: nil},
		{name: "no version", key: "namespaces/ns-abc/item-keys/ik-1", want: nil},
		{name: "version zero", key: "namespaces/ns-abc/item-keys/ik-1/v0", want: nil},
		{name: "another directory", key: "namespaces/ns-abc/tofu/ik-1/v1", want: nil},
		{name: "a bad namespace ID", key: "namespaces/abc/item-keys/ik-1/v1", want: nil},
		{name: "a bad item key ID", key: "namespaces/ns-abc/item-keys/xx-1/v1", want: nil},
		{name: "an extra segment", key: "namespaces/ns-abc/item-keys/ik-1/v1/x", want: nil},
		{name: "an empty key", key: "", want: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := secret.ParseItemKeyBlobKey(blob.Key(tc.key))
			if tc.want == nil {
				if !errors.Is(err, secret.ErrInvalidItemRef) {
					t.Errorf("err = %v, want ErrInvalidItemRef", err)
				}

				return
			}

			if err != nil || got != *tc.want {
				t.Errorf("parse = %+v, %v, want %+v", got, err, *tc.want)
			}
		})
	}
}

// fakeServerStore is an audit.ServerStore that keeps the entries.
type fakeServerStore struct {
	err     error
	entries []audit.Entry
}

func (f *fakeServerStore) AppendServer(_ context.Context, e audit.Entry) (audit.Entry, error) {
	if f.err != nil {
		return audit.Entry{}, f.err
	}

	f.entries = append(f.entries, e)

	return e, nil
}

// TestItemKeyBlobsAuditEndToEnd checks that a refusal reaches the audit store
// through the recorder of the package.
func TestItemKeyBlobsAuditEndToEnd(t *testing.T) {
	t.Parallel()

	entries := &fakeServerStore{err: nil, entries: nil}

	rec, err := scopeaudit.New(entries, secret.ItemKeyFamily(), nil)
	if err != nil {
		t.Fatalf("recorder: %v", err)
	}

	b, err := secret.NewItemKeyBlobs(newMemBlobs(), rec, rec)
	if err != nil {
		t.Fatalf("blobs: %v", err)
	}

	key := blob.Key("namespaces/" + string(nsB) + "/item-keys/ik-abc/v1-" + testAttempt)

	if _, getErr := b.Get(t.Context(), nsA, key, infoOf(t, key, []byte("x"))); !errors.Is(getErr, blob.ErrOutOfScope) {
		t.Fatalf("get err = %v, want ErrOutOfScope", getErr)
	}

	if len(entries.entries) != 1 || entries.entries[0].Kind != audit.KindSecurity ||
		entries.entries[0].Object != string(nsA) {
		t.Errorf("entries = %+v", entries.entries)
	}
}
