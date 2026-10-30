package stack

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/scopeaudit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/blobtest"
	"opendustpan.org/opendustpan/pkg/namespace"
)

const (
	scopeNS    = namespace.ID("ns-scope-a")
	scopeOther = "ns-scope-b"
	// scopeStack is the stack that the scoped access of the tests serves.
	scopeStack = ID("stk-1")
)

// countingStore counts every call that reaches it, so a test proves that a
// refused key never gets to the store.
type countingStore struct {
	objects map[blob.Key]string
	calls   int
	mu      sync.Mutex
}

func newCountingStore() *countingStore {
	return &countingStore{objects: map[blob.Key]string{}, calls: 0, mu: sync.Mutex{}}
}

func (c *countingStore) Put(_ context.Context, key blob.Key, r io.Reader) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.calls++

	data, err := io.ReadAll(r)
	if err != nil {
		return err //nolint:wrapcheck // A test double passes the reader error through.
	}

	c.objects[key] = string(data)

	return nil
}

func (c *countingStore) Get(_ context.Context, key blob.Key) (io.ReadCloser, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.calls++

	data, ok := c.objects[key]
	if !ok {
		return nil, blob.ErrNotFound
	}

	return io.NopCloser(strings.NewReader(data)), nil
}

func (c *countingStore) Delete(_ context.Context, key blob.Key) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.calls++
	delete(c.objects, key)

	return nil
}

func (c *countingStore) list(_ context.Context, prefix string) ([]blob.Object, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.calls++

	var out []blob.Object

	for key := range c.objects {
		if strings.HasPrefix(key.String(), prefix) {
			out = append(out, blob.Object{Key: key, ModifiedAt: time.Time{}})
		}
	}

	return out, nil
}

// ListPage implements blob.Store: it cuts the page that req selects from the
// objects of list.
func (c *countingStore) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	if err := req.Validate(); err != nil {
		return blob.Page{}, err
	}

	objects, err := c.list(ctx, req.Prefix)
	if err != nil {
		return blob.Page{}, err
	}

	slices.SortFunc(objects, func(a, b blob.Object) int { return strings.Compare(a.Key.String(), b.Key.String()) })

	return blob.PageOf(objects, req), nil
}

func (c *countingStore) Ping(context.Context) error { return nil }

// count returns the number of calls that reached the store.
func (c *countingStore) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.calls
}

// recordedRefusal is one refusal that scopeRecorder saw.
type recordedRefusal struct {
	ns    namespace.ID
	op    string
	where scopeaudit.Where
}

// scopeRecorder is an OutOfScopeRecorder that keeps the refusals. A non-nil
// err makes each record fail.
type scopeRecorder struct {
	err error
	got []recordedRefusal
}

func (r *scopeRecorder) RecordOutOfScope(_ context.Context, ns namespace.ID, op string, where scopeaudit.Where) error {
	r.got = append(r.got, recordedRefusal{ns: ns, op: op, where: where})

	return r.err
}

// hostileKeys are keys that the stack blobs of scopeNS must refuse, with the
// place that the refusal names. A conversion makes a Key with no validation,
// as a row from a damaged or hostile database would.
func hostileKeys() map[string]struct {
	key   blob.Key
	where scopeaudit.Where
} {
	type entry = struct {
		key   blob.Key
		where scopeaudit.Where
	}

	return map[string]entry{
		"another namespace":        {"namespaces/" + scopeOther + "/pulumi/stk-1/upd-1/1", scopeaudit.OutsideNamespace},
		"old layout without ns":    {"pulumi/stk-1/upd-1/1", scopeaudit.OutsideNamespace},
		"id as a string prefix":    {"namespaces/ns-scope-ax/pulumi/stk-1/upd-1/1", scopeaudit.OutsideNamespace},
		"id with a longer segment": {"namespaces/ns-scope-a.b/pulumi/stk-1/upd-1/1", scopeaudit.OutsideNamespace},
		"parent segment": {
			"namespaces/ns-scope-a/../" + scopeOther + "/pulumi/stk-1/upd-1/1",
			scopeaudit.OutsideNamespace,
		},
		"parent of the scope":         {"namespaces/pulumi/stk-1/upd-1/1", scopeaudit.OutsideNamespace},
		"empty key":                   {"", scopeaudit.OutsideNamespace},
		"scope directory only":        {"namespaces/ns-scope-a/", scopeaudit.OutsideNamespace},
		"absolute path":               {"/namespaces/ns-scope-a/pulumi/stk-1/upd-1/1", scopeaudit.OutsideNamespace},
		"backslash":                   {`namespaces\ns-scope-a\pulumi\stk-1\upd-1\1`, scopeaudit.OutsideNamespace},
		"own namespace, tofu":         {"namespaces/ns-scope-a/tofu/ws-1/sv-1/1", scopeaudit.OutsideDirectory},
		"own namespace, item keys":    {"namespaces/ns-scope-a/item-keys/ik-1/v1", scopeaudit.OutsideDirectory},
		"own namespace, string match": {"namespaces/ns-scope-a/pulumix/stk-1/upd-1/1", scopeaudit.OutsideDirectory},
		"same namespace, another stack": {
			"namespaces/ns-scope-a/pulumi/stk-2/upd-1/1", scopeaudit.OutsideUnitDirectory,
		},
		"same namespace, another stack, journal": {
			"namespaces/ns-scope-a/pulumi/stk-2/upd-1/journal/1", scopeaudit.OutsideUnitDirectory,
		},
		"same namespace, stack ID as a string prefix": {
			"namespaces/ns-scope-a/pulumi/stk-10/upd-1/1", scopeaudit.OutsideUnitDirectory,
		},
		"same namespace, family directory only": {
			"namespaces/ns-scope-a/pulumi/", scopeaudit.OutsideNamespace,
		},
		"same namespace, stack ID only": {
			"namespaces/ns-scope-a/pulumi/stk-1", scopeaudit.OutsideUnitDirectory,
		},
	}
}

// scopeOp is one blob operation that a test sends a key to.
type scopeOp func(ctx context.Context, b *scopedBlobs, key blob.Key) error

// scopeOps are the three operations that take a key.
func scopeOps() map[string]scopeOp {
	return map[string]scopeOp{
		"put": func(ctx context.Context, b *scopedBlobs, key blob.Key) error {
			return b.Put(ctx, key, strings.NewReader("x"))
		},
		"get": func(ctx context.Context, b *scopedBlobs, key blob.Key) error {
			rc, err := b.Get(ctx, key)
			if err != nil {
				return err
			}

			return rc.Close()
		},
		"delete": func(ctx context.Context, b *scopedBlobs, key blob.Key) error {
			return b.Delete(ctx, key)
		},
	}
}

// newScopeFixture returns the scoped access of scopeNS over a counting store
// and a recorder.
func newScopeFixture(t *testing.T, recordErr error) (*scopedBlobs, *countingStore, *scopeRecorder) {
	t.Helper()

	store := newCountingStore()
	rec := &scopeRecorder{err: recordErr, got: nil}

	access, err := newBlobAccess(store, rec)
	if err != nil {
		t.Fatalf("access: %v", err)
	}

	b, err := access.scope(scopeNS, scopeStack)
	if err != nil {
		t.Fatalf("scope: %v", err)
	}

	return b, store, rec
}

// TestScopedBlobsRefuseHostileKeys sends a hostile key straight to each blob
// operation, as if the key checks of the callers had failed. Each operation
// returns blob.ErrOutOfScope, never calls the store, and writes one refusal
// with the right place.
func TestScopedBlobsRefuseHostileKeys(t *testing.T) {
	t.Parallel()

	for opName, run := range scopeOps() {
		for name, tc := range hostileKeys() {
			t.Run(opName+"/"+name, func(t *testing.T) {
				t.Parallel()

				checkHostileKey(t, opName, run, tc.key, tc.where)
			})
		}
	}
}

// checkHostileKey sends key to run and checks the refusal.
func checkHostileKey(t *testing.T, opName string, run scopeOp, key blob.Key, where scopeaudit.Where) {
	t.Helper()

	b, store, rec := newScopeFixture(t, nil)

	if err := run(t.Context(), b, key); !errors.Is(err, blob.ErrOutOfScope) {
		t.Fatalf("%s: err = %v, want blob.ErrOutOfScope", opName, err)
	}

	if n := store.count(); n != 0 {
		t.Fatalf("%s reached the store %d times, want 0", opName, n)
	}

	want := recordedRefusal{ns: scopeNS, op: opName, where: where}
	if len(rec.got) != 1 || rec.got[0] != want {
		t.Fatalf("refusals = %+v, want %+v", rec.got, want)
	}
}

// TestScopedBlobsRefuseHostilePrefixes checks the listing: a prefix outside
// the stack blob directory of the namespace is refused before the store.
func TestScopedBlobsRefuseHostilePrefixes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		prefix string
		where  scopeaudit.Where
	}{
		{"empty prefix", "", scopeaudit.OutsideNamespace},
		{"all namespaces", "namespaces/", scopeaudit.OutsideNamespace},
		{"another namespace", "namespaces/" + scopeOther + "/pulumi/", scopeaudit.OutsideNamespace},
		{"string prefix of the id", "namespaces/ns-scope-ax/pulumi/", scopeaudit.OutsideNamespace},
		{"old layout", "pulumi/stk-1/", scopeaudit.OutsideNamespace},
		{"scope root", "namespaces/ns-scope-a/", scopeaudit.OutsideDirectory},
		{"same namespace, family directory", "namespaces/ns-scope-a/pulumi/", scopeaudit.OutsideUnitDirectory},
		{"same namespace, another stack", "namespaces/ns-scope-a/pulumi/stk-2/", scopeaudit.OutsideUnitDirectory},
		{
			"same namespace, stack ID as a string prefix",
			"namespaces/ns-scope-a/pulumi/stk-10/",
			scopeaudit.OutsideUnitDirectory,
		},
		{"scope root without slash", "namespaces/ns-scope-a", scopeaudit.OutsideDirectory},
		{"own tofu directory", "namespaces/ns-scope-a/tofu/", scopeaudit.OutsideDirectory},
		{"string prefix of the directory", "namespaces/ns-scope-a/pulumix/", scopeaudit.OutsideDirectory},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			b, store, rec := newScopeFixture(t, nil)

			if _, err := blobtest.ListAll(t.Context(), b, tc.prefix); !errors.Is(err, blob.ErrOutOfScope) {
				t.Fatalf("list %q: err = %v, want blob.ErrOutOfScope", tc.prefix, err)
			}

			if n := store.count(); n != 0 {
				t.Fatalf("list reached the store %d times, want 0", n)
			}

			if len(rec.got) != 1 || rec.got[0].op != "list" || rec.got[0].where != tc.where {
				t.Fatalf("refusals = %+v, want one list refusal %q", rec.got, tc.where)
			}
		})
	}
}

// TestScopedBlobsServeTheirOwnKeys checks that a key inside the stack blob
// directory of the namespace passes each operation and writes no refusal.
func TestScopedBlobsServeTheirOwnKeys(t *testing.T) {
	t.Parallel()

	b, _, rec := newScopeFixture(t, nil)
	ctx := t.Context()

	key, keyErr := b.Key("stk-1/upd-1/1")
	if keyErr != nil {
		t.Fatalf("key: %v", keyErr)
	}

	if want := "namespaces/ns-scope-a/pulumi/stk-1/upd-1/1"; key.String() != want {
		t.Fatalf("key = %q, want %q", key, want)
	}

	if putErr := b.Put(ctx, key, strings.NewReader("body")); putErr != nil {
		t.Fatalf("put: %v", putErr)
	}

	if got := readBlob(t, b, key); got != "body" {
		t.Fatalf("get = %q, want body", got)
	}

	listed, listErr := blobtest.ListAll(ctx, b, b.updatePrefix("stk-1", "upd-1"))
	if listErr != nil || len(listed) != 1 {
		t.Fatalf("list = %v, %v; want one object", listed, listErr)
	}

	if delErr := b.Delete(ctx, key); delErr != nil {
		t.Fatalf("delete: %v", delErr)
	}

	if len(rec.got) != 0 {
		t.Fatalf("refusals = %+v, want none", rec.got)
	}
}

// readBlob returns the content under key.
func readBlob(t *testing.T, b *scopedBlobs, key blob.Key) string {
	t.Helper()

	rc, err := b.Get(t.Context(), key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	data, readErr := io.ReadAll(rc)
	if closeErr := rc.Close(); closeErr != nil {
		t.Fatalf("close: %v", closeErr)
	}

	if readErr != nil {
		t.Fatalf("read: %v", readErr)
	}

	return string(data)
}

// errAuditDown is the failure of the audit store in the fail-closed test.
var errAuditDown = errors.New("audit store down")

// TestScopedBlobsKeepTheRefusalWhenTheRecordFails checks the fail-closed
// rule: when the audit write fails, the call is still refused, and the error
// holds both causes.
func TestScopedBlobsKeepTheRefusalWhenTheRecordFails(t *testing.T) {
	t.Parallel()

	b, store, _ := newScopeFixture(t, errAuditDown)

	_, err := b.Get(t.Context(), blob.Key("namespaces/"+scopeOther+"/pulumi/stk-1/upd-1/1"))
	if !errors.Is(err, blob.ErrOutOfScope) || !errors.Is(err, errAuditDown) {
		t.Fatalf("get: err = %v, want ErrOutOfScope joined with the audit failure", err)
	}

	if strings.Contains(err.Error(), scopeOther) {
		t.Fatalf("error holds the other namespace: %v", err)
	}

	if n := store.count(); n != 0 {
		t.Fatalf("store calls = %d, want 0", n)
	}
}

// TestBlobAccessRefusesBadInput checks the boundary: a missing store, a
// missing recorder and a namespace ID that cannot be part of a key.
func TestBlobAccessRefusesBadInput(t *testing.T) {
	t.Parallel()

	rec := &scopeRecorder{err: nil, got: nil}

	if _, err := newBlobAccess(nil, rec); !errors.Is(err, ErrMissingDependency) {
		t.Fatalf("nil store: err = %v, want ErrMissingDependency", err)
	}

	if _, err := newBlobAccess(newCountingStore(), nil); !errors.Is(err, ErrMissingDependency) {
		t.Fatalf("nil recorder: err = %v, want ErrMissingDependency", err)
	}

	access, err := newBlobAccess(newCountingStore(), rec)
	if err != nil {
		t.Fatalf("access: %v", err)
	}

	for _, id := range []namespace.ID{"", "ns-", "acme", "ns-A", "ns-a/b", "ns-a/../ns-b", "ns-a b"} {
		if _, err := access.scope(id, scopeStack); err == nil {
			t.Errorf("scope(%q): err = nil, want an error", id)
		}
	}

	for _, id := range []ID{"", "stk-1/", "stk-1/upd-1", "..", "stk/../stk-2", "stk\\1", " "} {
		if _, err := access.scope(scopeNS, id); !errors.Is(err, ErrInvalidScope) {
			t.Errorf("scope with stack %q: err = %v, want ErrInvalidScope", id, err)
		}
	}
}

// TestCheckJournalBlobKey checks the row store layer: only the key of a batch
// of this update and this stack, in this namespace, passes.
func TestCheckJournalBlobKey(t *testing.T) {
	t.Parallel()

	const good = "namespaces/ns-scope-a/pulumi/stk-1/upd-1/journal/3"

	tests := []struct {
		name string
		ns   namespace.ID
		key  string
		ok   bool
	}{
		{"a batch of the update", scopeNS, good, true},
		{"another namespace", scopeOther2, good, false},
		{"another stack", scopeNS, "namespaces/ns-scope-a/pulumi/stk-2/upd-1/journal/3", false},
		{"another update", scopeNS, "namespaces/ns-scope-a/pulumi/stk-1/upd-2/journal/3", false},
		{"old layout", scopeNS, "pulumi/stk-1/upd-1/journal/3", false},
		{"a checkpoint key", scopeNS, "namespaces/ns-scope-a/pulumi/stk-1/upd-1/3", false},
		{"no sequence", scopeNS, "namespaces/ns-scope-a/pulumi/stk-1/upd-1/journal/", false},
		{"nested", scopeNS, good + "/4", false},
		{"tofu directory", scopeNS, "namespaces/ns-scope-a/tofu/stk-1/upd-1/journal/3", false},
		{"parent segment", scopeNS, "namespaces/ns-scope-a/pulumi/stk-1/upd-1/journal/../3", false},
		{"empty key", scopeNS, "", false},
		{"invalid namespace ID", "acme", good, false},
		{"empty namespace ID", "", good, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := CheckJournalBlobKey(tc.ns, "stk-1", "upd-1", blob.Key(tc.key))

			switch {
			case tc.ok && err != nil:
				t.Fatalf("err = %v, want nil", err)
			case !tc.ok && !errors.Is(err, ErrJournalKey):
				t.Fatalf("err = %v, want ErrJournalKey", err)
			case !tc.ok && tc.key != "" && strings.Contains(err.Error(), tc.key):
				t.Fatalf("error holds the key: %v", err)
			default:
			}
		})
	}
}

// scopeOther2 is the namespace ID that matches scopeOther.
const scopeOther2 = namespace.ID(scopeOther)

// TestCheckCheckpointBlobKey checks the row store layer: only the key of a save
// of this update and this stack, in this namespace, passes.
func TestCheckCheckpointBlobKey(t *testing.T) {
	t.Parallel()

	const good = "namespaces/ns-scope-a/pulumi/stk-1/upd-1/3"

	tests := []struct {
		name string
		ns   namespace.ID
		key  string
		ok   bool
	}{
		{"a save of the update", scopeNS, good, true},
		{"the first save", scopeNS, "namespaces/ns-scope-a/pulumi/stk-1/upd-1/0", true},
		{"another namespace", scopeOther2, good, false},
		{"same namespace, another stack", scopeNS, "namespaces/ns-scope-a/pulumi/stk-2/upd-1/3", false},
		{"same namespace, stack ID as a string prefix", scopeNS, "namespaces/ns-scope-a/pulumi/stk-10/upd-1/3", false},
		{"same namespace, another update", scopeNS, "namespaces/ns-scope-a/pulumi/stk-1/upd-2/3", false},
		{"old layout", scopeNS, "pulumi/stk-1/upd-1/3", false},
		{"a journal key", scopeNS, "namespaces/ns-scope-a/pulumi/stk-1/upd-1/journal/3", false},
		{"no number", scopeNS, "namespaces/ns-scope-a/pulumi/stk-1/upd-1/", false},
		{"not a number", scopeNS, "namespaces/ns-scope-a/pulumi/stk-1/upd-1/x", false},
		{"negative number", scopeNS, "namespaces/ns-scope-a/pulumi/stk-1/upd-1/-1", false},
		{"nested", scopeNS, good + "/4", false},
		{"tofu directory", scopeNS, "namespaces/ns-scope-a/tofu/stk-1/upd-1/3", false},
		{"parent segment", scopeNS, "namespaces/ns-scope-a/pulumi/stk-1/upd-1/../3", false},
		{"empty key", scopeNS, "", false},
		{"invalid namespace ID", "acme", good, false},
		{"empty namespace ID", "", good, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := CheckCheckpointBlobKey(tc.ns, "stk-1", "upd-1", blob.Key(tc.key))

			switch {
			case tc.ok && err != nil:
				t.Fatalf("err = %v, want nil", err)
			case !tc.ok && !errors.Is(err, ErrCheckpointKey):
				t.Fatalf("err = %v, want ErrCheckpointKey", err)
			case !tc.ok && tc.key != "" && strings.Contains(err.Error(), tc.key):
				t.Fatalf("error holds the key: %v", err)
			default:
			}
		})
	}
}

// TestNarrowedBlobsRefuseTheRestOfTheStack checks the use-time scope of a key
// that comes from a row: the access narrowed to one update refuses the
// directory of another update of the same stack, the journal directory of the
// update, and the directory of another stack. Each operation refuses before
// the store and writes one refusal.
func TestNarrowedBlobsRefuseTheRestOfTheStack(t *testing.T) {
	t.Parallel()

	keys := map[string]blob.Key{
		"another update, same stack":    "namespaces/ns-scope-a/pulumi/stk-1/upd-2/1",
		"another stack, same update ID": "namespaces/ns-scope-a/pulumi/stk-2/upd-1/1",
		"update ID as a string prefix":  "namespaces/ns-scope-a/pulumi/stk-1/upd-10/1",
	}

	for opName, run := range scopeOps() {
		for name, key := range keys {
			t.Run(opName+"/"+name, func(t *testing.T) {
				t.Parallel()

				checkNarrowedRefusal(t, opName, run, key)
			})
		}
	}
}

// checkNarrowedRefusal sends key to run on the access narrowed to the update
// upd-1, and checks the refusal before the store.
func checkNarrowedRefusal(t *testing.T, opName string, run scopeOp, key blob.Key) {
	t.Helper()

	b, store, rec := newScopeFixture(t, nil)
	narrowed := b.narrow(b.updatePrefix(scopeStack, "upd-1"))

	if err := run(t.Context(), narrowed, key); !errors.Is(err, blob.ErrOutOfScope) {
		t.Fatalf("%s: err = %v, want blob.ErrOutOfScope", opName, err)
	}

	if n := store.count(); n != 0 {
		t.Fatalf("%s reached the store %d times, want 0", opName, n)
	}

	if len(rec.got) != 1 || rec.got[0].op != opName || rec.got[0].where != scopeaudit.OutsideUnitDirectory {
		t.Fatalf("refusals = %+v, want one %s refusal in the stack directory", rec.got, opName)
	}
}

// TestNarrowOutsideTheStackRefusesEveryKey checks that a narrow to a directory
// outside the stack gives an access that refuses all keys, also its own.
func TestNarrowOutsideTheStackRefusesEveryKey(t *testing.T) {
	t.Parallel()

	b, store, rec := newScopeFixture(t, nil)
	other := b.narrow("namespaces/ns-scope-a/pulumi/stk-2/upd-1/")

	for _, key := range []blob.Key{
		"namespaces/ns-scope-a/pulumi/stk-2/upd-1/1",
		"namespaces/ns-scope-a/pulumi/stk-1/upd-1/1",
	} {
		if err := other.Delete(t.Context(), key); !errors.Is(err, blob.ErrOutOfScope) {
			t.Fatalf("delete %s: err = %v, want blob.ErrOutOfScope", key, err)
		}
	}

	if n := store.count(); n != 0 || len(rec.got) != 2 {
		t.Fatalf("store calls = %d, refusals = %d; want 0 and 2", n, len(rec.got))
	}
}

// foreignLister lists the keys of two stacks whatever the prefix says, as an
// adapter with a listing fault would.
type foreignLister struct {
	*countingStore
}

func (foreignLister) list(context.Context, string) ([]blob.Object, error) {
	return []blob.Object{
		{Key: "namespaces/ns-scope-a/pulumi/stk-1/upd-1/1", ModifiedAt: time.Time{}},
		{Key: "namespaces/ns-scope-a/pulumi/stk-2/upd-1/1", ModifiedAt: time.Time{}},
	}, nil
}

// ListPage implements blob.Store: it cuts the page that req selects from the
// objects of list.
func (x foreignLister) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	if err := req.Validate(); err != nil {
		return blob.Page{}, err
	}

	objects, err := x.list(ctx, req.Prefix)
	if err != nil {
		return blob.Page{}, err
	}

	slices.SortFunc(objects, func(a, b blob.Object) int { return strings.Compare(a.Key.String(), b.Key.String()) })

	return blob.PageOf(objects, req), nil
}

// TestListRefusesAKeyOutsideTheStack checks the layer under the delete of a
// purge and of a sweep: when the store lists a key of another stack of the
// same namespace, the listing is refused and returns no key, so the caller
// deletes nothing.
func TestListRefusesAKeyOutsideTheStack(t *testing.T) {
	t.Parallel()

	rec := &scopeRecorder{err: nil, got: nil}

	access, err := newBlobAccess(foreignLister{newCountingStore()}, rec)
	if err != nil {
		t.Fatalf("access: %v", err)
	}

	b, err := access.scope(scopeNS, scopeStack)
	if err != nil {
		t.Fatalf("scope: %v", err)
	}

	listed, err := blobtest.ListAll(t.Context(), b, b.stackPrefix(scopeStack))
	if !errors.Is(err, blob.ErrOutOfScope) || len(listed) != 0 {
		t.Fatalf("list = %v, %v; want no key and blob.ErrOutOfScope", listed, err)
	}

	if len(rec.got) != 1 || rec.got[0].op != "list" || rec.got[0].where != scopeaudit.OutsideUnitDirectory {
		t.Fatalf("refusals = %+v, want one list refusal in the stack directory", rec.got)
	}
}
