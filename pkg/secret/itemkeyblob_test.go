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
type memBlobs struct {
	objects map[blob.Key][]byte
	calls   int
	mu      sync.Mutex
}

func newMemBlobs() *memBlobs {
	return &memBlobs{objects: map[blob.Key][]byte{}, calls: 0, mu: sync.Mutex{}}
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

func (m *memBlobs) List(_ context.Context, prefix string) ([]blob.Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls++

	prefix = strings.TrimSuffix(prefix, "/") + "/"

	var out []blob.Object

	for k := range m.objects {
		if strings.HasPrefix(k.String(), prefix) {
			out = append(out, blob.Object{Key: k, ModifiedAt: time.Time{}})
		}
	}

	slices.SortFunc(out, func(a, b blob.Object) int { return strings.Compare(a.Key.String(), b.Key.String()) })

	return out, nil
}

func (m *memBlobs) Ping(context.Context) error { return nil }

func (m *memBlobs) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.calls
}

var (
	errAuditDown    = errors.New("audit store is down")
	errAppendFailed = errors.New("append failed")
)

// refusal is one call of a recorder.
type refusal struct {
	op string
	ns namespace.ID
}

// fakeRecorder is an OutOfScopeRecorder that keeps its calls.
type fakeRecorder struct {
	err   error
	calls []refusal
	mu    sync.Mutex
}

func (f *fakeRecorder) RecordOutOfScope(_ context.Context, ns namespace.ID, op string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, refusal{ns: ns, op: op})

	return f.err
}

func (f *fakeRecorder) refusals() []refusal {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.calls)
}

func newFakeRecorder(err error) *fakeRecorder {
	return &fakeRecorder{err: err, calls: nil, mu: sync.Mutex{}}
}

func newItemKeyBlobs(t *testing.T) (*secret.ItemKeyBlobs, *memBlobs, *fakeRecorder) {
	t.Helper()

	store, rec := newMemBlobs(), newFakeRecorder(nil)

	b, err := secret.NewItemKeyBlobs(store, rec)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	return b, store, rec
}

// mustKey returns the blob key of version of id in ns.
func mustKey(t *testing.T, b *secret.ItemKeyBlobs, ns namespace.ID, id secret.ItemKeyID, version int) blob.Key {
	t.Helper()

	key, err := b.Key(ns, id, version)
	if err != nil {
		t.Fatalf("key v%d: %v", version, err)
	}

	return key
}

func TestNewItemKeyBlobsNeedsItsPorts(t *testing.T) {
	t.Parallel()

	if _, err := secret.NewItemKeyBlobs(nil, newFakeRecorder(nil)); !errors.Is(err, secret.ErrMissingDependency) {
		t.Errorf("nil store err = %v, want ErrMissingDependency", err)
	}

	if _, err := secret.NewItemKeyBlobs(newMemBlobs(), nil); !errors.Is(err, secret.ErrMissingDependency) {
		t.Errorf("nil recorder err = %v, want ErrMissingDependency", err)
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
		want    string
		version int
	}{
		{
			name: "version one", ns: nsA, id: "ik-abc", version: 1, wantErr: nil,
			want: "namespaces/" + string(nsA) + "/item-keys/ik-abc/v1",
		},
		{
			name: "a later version", ns: nsB, id: "ik-abc", version: 12, wantErr: nil,
			want: "namespaces/" + string(nsB) + "/item-keys/ik-abc/v12",
		},
		{name: "version zero", ns: nsA, id: "ik-abc", version: 0, want: "", wantErr: secret.ErrInvalidItemRef},
		{name: "a negative version", ns: nsA, id: "ik-abc", version: -1, want: "", wantErr: secret.ErrInvalidItemRef},
		{name: "a huge version", ns: nsA, id: "ik-abc", version: 1 << 40, want: "", wantErr: secret.ErrInvalidItemRef},
		{
			name:    "an ID without the prefix",
			ns:      nsA,
			id:      "key-1",
			version: 1,
			want:    "",
			wantErr: secret.ErrInvalidItemRef,
		},
		{name: "an ID with a slash", ns: nsA, id: "ik-a/b", version: 1, want: "", wantErr: secret.ErrInvalidItemRef},
		{name: "an empty ID", ns: nsA, id: "", version: 1, want: "", wantErr: secret.ErrInvalidItemRef},
		{name: "an empty namespace ID", ns: "", id: "ik-abc", version: 1, want: "", wantErr: blob.ErrInvalidKey},
		{
			name:    "a namespace ID with a slash",
			ns:      "ns-a/../ns-b",
			id:      "ik-abc",
			version: 1,
			want:    "",
			wantErr: blob.ErrInvalidKey,
		},
		{
			name:    "a namespace ID with upper case",
			ns:      "ns-AAAA",
			id:      "ik-abc",
			version: 1,
			want:    "",
			wantErr: blob.ErrInvalidKey,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			key, err := b.Key(tc.ns, tc.id, tc.version)
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

	if _, err := b.Get(t.Context(), nsA, keyV1); !errors.Is(err, secret.ErrKeyNotFound) {
		t.Fatalf("get before put err = %v, want ErrKeyNotFound", err)
	}

	if err := b.Put(t.Context(), nsA, keyV1, wrapped); err != nil {
		t.Fatalf("put: %v", err)
	}

	if err := b.Put(t.Context(), nsA, keyV1, []byte("other")); !errors.Is(err, blob.ErrExists) {
		t.Errorf("second put err = %v, want ErrExists", err)
	}

	if got, err := b.Get(t.Context(), nsA, keyV1); err != nil || !bytes.Equal(got, wrapped) {
		t.Fatalf("get = %q, %v, want %q", got, err, wrapped)
	}

	if err := b.Delete(t.Context(), nsA, keyV1); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if err := b.Delete(t.Context(), nsA, keyV1); err != nil {
		t.Errorf("second delete: %v", err)
	}

	if _, err := b.Get(t.Context(), nsA, keyV1); !errors.Is(err, secret.ErrKeyNotFound) {
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

	if _, err := b.Get(t.Context(), nsA, key2); !errors.Is(err, secret.ErrTooLarge) {
		t.Errorf("get of a large blob err = %v, want ErrTooLarge", err)
	}
}

// foreignKeys returns keys that a call in nsA must refuse, with a name for
// each. Each key is a plain conversion, so no check of ParseKey ran on it.
func foreignKeys(id secret.ItemKeyID) map[string]blob.Key {
	a, other, tail := string(nsA), string(nsB), "/item-keys/"+string(id)+"/v1"

	return map[string]blob.Key{
		"the item key of another namespace":       blob.Key("namespaces/" + other + tail),
		"a later version in another namespace":    blob.Key("namespaces/" + other + "/item-keys/" + string(id) + "/v9"),
		"a string prefix of the namespace":        blob.Key("namespaces/" + a + "x" + tail),
		"the parent of the namespace":             blob.Key("namespaces" + tail),
		"the old layout":                          blob.Key("item-keys/" + string(id)),
		"the old layout with a version":           blob.Key("item-keys/" + string(id) + "/v1"),
		"another directory of the namespace":      blob.Key("namespaces/" + a + "/tofu/ws-1/sv-1/1"),
		"a parent segment out of the namespace":   blob.Key("namespaces/" + a + "/../" + other + tail),
		"the item-keys directory without a slash": blob.Key("namespaces/" + a + "/item-keys"),
		"an empty key":                            "",
		"a key with a line break":                 blob.Key("namespaces/" + other + "/item-keys/ik-x\n/v1"),
		"a key in upper case":                     blob.Key("NAMESPACES/" + other + "/ITEM-KEYS/" + string(id) + "/v1"),
	}
}

// itemKeyOps are the blob calls of the service that take a key.
func itemKeyOps() map[string]func(ctx context.Context, b *secret.ItemKeyBlobs, key blob.Key) error {
	return map[string]func(ctx context.Context, b *secret.ItemKeyBlobs, key blob.Key) error{
		"get": func(ctx context.Context, b *secret.ItemKeyBlobs, key blob.Key) error {
			_, err := b.Get(ctx, nsA, key)

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
		for keyName, key := range foreignKeys(id) {
			t.Run(opName+" with "+keyName, func(t *testing.T) {
				t.Parallel()

				b, store, rec := newItemKeyBlobs(t)

				// The blob of the other namespace exists, so a leak would show.
				other := mustKey(t, b, nsB, id, 1)
				if err := store.Put(t.Context(), other, strings.NewReader("secret")); err != nil {
					t.Fatalf("seed: %v", err)
				}

				before := store.callCount()
				err := call(t.Context(), b, key)

				assertRefusal(t, err, rec, opName)

				if n := store.callCount(); n != before {
					t.Errorf("%s reached the store %d times, want 0", opName, n-before)
				}
			})
		}
	}
}

// assertRefusal checks that err is a refusal and that the recorder holds one
// call for op in nsA. A malformed key of the namespace itself is not a
// refusal: it gives ErrInvalidItemRef and no record.
func assertRefusal(t *testing.T, err error, rec *fakeRecorder, op string) {
	t.Helper()

	if errors.Is(err, secret.ErrInvalidItemRef) {
		if n := len(rec.refusals()); n != 0 {
			t.Errorf("a malformed key is not a refusal, recorder called %d times", n)
		}

		return
	}

	if !errors.Is(err, blob.ErrOutOfScope) {
		t.Fatalf("%s err = %v, want ErrOutOfScope", op, err)
	}

	if got := rec.refusals(); !slices.Equal(got, []refusal{{ns: nsA, op: op}}) {
		t.Errorf("refusals = %v, want one for %s in %s", got, op, nsA)
	}

	if strings.Contains(err.Error(), string(nsB)) {
		t.Errorf("error %q names the other namespace", err)
	}
}

func TestItemKeyBlobsOtherNamespaceKeyLeavesTheBlobAlone(t *testing.T) {
	t.Parallel()

	b, store, rec := newItemKeyBlobs(t)
	other := mustKey(t, b, nsB, secret.NewItemKeyID(), 1)

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

	if got, err := b.Get(t.Context(), nsB, other); err != nil || string(got) != "b-secret" {
		t.Errorf("blob of B = %q, %v", got, err)
	}
}

func TestItemKeyBlobsRefuseWhenTheRecorderFails(t *testing.T) {
	t.Parallel()

	store, rec := newMemBlobs(), newFakeRecorder(errAuditDown)

	b, err := secret.NewItemKeyBlobs(store, rec)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	key := mustKey(t, b, nsB, secret.NewItemKeyID(), 1)

	_, getErr := b.Get(t.Context(), nsA, key)
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
		"no version":           prefix + "ik-abc",
		"a version of zero":    prefix + "ik-abc/v0",
		"a version with a +":   prefix + "ik-abc/v+1",
		"a padded version":     prefix + "ik-abc/v01",
		"no v in the version":  prefix + "ik-abc/1",
		"a deep key":           prefix + "ik-abc/v1/x",
		"an ID without prefix": prefix + "key-1/v1",
		"a huge version":       prefix + "ik-abc/v" + strconv.Itoa(1<<30),
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
	key := blob.Key("namespaces/" + string(nsA) + "/item-keys/ik-abc/v1")

	for _, ns := range []namespace.ID{"", "ns-", "ns-A", "ns-a/b", "ns-a b"} {
		if _, err := b.Get(t.Context(), ns, key); !errors.Is(err, blob.ErrInvalidKey) {
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

	good := func(ns namespace.ID, id secret.ItemKeyID, v int) *secret.ItemKeyBlob {
		return &secret.ItemKeyBlob{Namespace: ns, ID: id, Version: v}
	}

	cases := []struct {
		want *secret.ItemKeyBlob
		name string
		key  string
	}{
		{name: "version one", key: "namespaces/ns-abc/item-keys/ik-1/v1", want: good("ns-abc", "ik-1", 1)},
		{name: "version 40", key: "namespaces/ns-abc/item-keys/ik-1/v40", want: good("ns-abc", "ik-1", 40)},
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

func TestAuditOutOfScope(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.October, 20, 12, 0, 0, 0, time.UTC)
	store := &fakeServerStore{err: nil, entries: nil}

	rec, err := secret.NewAuditOutOfScope(store, func() time.Time { return at })
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	for _, op := range []string{"put", "get", "delete", "list"} {
		if recErr := rec.RecordOutOfScope(t.Context(), nsA, op); recErr != nil {
			t.Fatalf("record %s: %v", op, recErr)
		}
	}

	if len(store.entries) != 4 {
		t.Fatalf("entries = %d, want 4", len(store.entries))
	}

	for _, e := range store.entries {
		checkSecurityEntry(t, e, at)
	}
}

// checkSecurityEntry checks one entry of a refusal.
func checkSecurityEntry(t *testing.T, e audit.Entry, at time.Time) {
	t.Helper()

	if err := e.Validate(); err != nil {
		t.Errorf("entry %q is not valid: %v", e.Reason, err)
	}

	if e.Kind != audit.KindSecurity || e.Outcome != audit.OutcomeDenied || e.Namespace != audit.ServerScope {
		t.Errorf("kind, outcome or scope of %+v", e)
	}

	if e.Action != secret.ActionBlobOutOfScope || e.Object != string(nsA) || !e.At.Equal(at) {
		t.Errorf("action, object or time of %+v", e)
	}

	if strings.Contains(e.Reason, "namespaces/") || strings.Contains(e.Reason, string(nsB)) {
		t.Errorf("reason %q holds a key", e.Reason)
	}
}

func TestAuditOutOfScopeRefusesHostileInput(t *testing.T) {
	t.Parallel()

	store := &fakeServerStore{err: nil, entries: nil}

	rec, err := secret.NewAuditOutOfScope(store, nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	opErr := rec.RecordOutOfScope(t.Context(), nsA, "get\nforged entry")
	if !errors.Is(opErr, secret.ErrInvalidItemRef) {
		t.Errorf("hostile op err = %v, want ErrInvalidItemRef", opErr)
	}

	if recErr := rec.RecordOutOfScope(t.Context(), "ns-A\nx", "get"); !errors.Is(recErr, namespace.ErrInvalidID) {
		t.Errorf("hostile namespace err = %v, want ErrInvalidID", recErr)
	}

	if len(store.entries) != 0 {
		t.Errorf("entries = %d, want 0", len(store.entries))
	}

	store.err = errAppendFailed

	if recErr := rec.RecordOutOfScope(t.Context(), nsA, "get"); !errors.Is(recErr, errAppendFailed) {
		t.Errorf("append failure err = %v, want the store error", recErr)
	}

	if _, newErr := secret.NewAuditOutOfScope(nil, nil); !errors.Is(newErr, secret.ErrMissingDependency) {
		t.Errorf("nil store err = %v, want ErrMissingDependency", newErr)
	}
}

// TestItemKeyBlobsAuditEndToEnd checks that a refusal reaches the audit store
// through the recorder of the package.
func TestItemKeyBlobsAuditEndToEnd(t *testing.T) {
	t.Parallel()

	entries := &fakeServerStore{err: nil, entries: nil}

	rec, err := secret.NewAuditOutOfScope(entries, nil)
	if err != nil {
		t.Fatalf("recorder: %v", err)
	}

	b, err := secret.NewItemKeyBlobs(newMemBlobs(), rec)
	if err != nil {
		t.Fatalf("blobs: %v", err)
	}

	key := blob.Key("namespaces/" + string(nsB) + "/item-keys/ik-abc/v1")

	if _, getErr := b.Get(t.Context(), nsA, key); !errors.Is(getErr, blob.ErrOutOfScope) {
		t.Fatalf("get err = %v, want ErrOutOfScope", getErr)
	}

	if len(entries.entries) != 1 || entries.entries[0].Kind != audit.KindSecurity ||
		entries.entries[0].Object != string(nsA) {
		t.Errorf("entries = %+v", entries.entries)
	}
}
