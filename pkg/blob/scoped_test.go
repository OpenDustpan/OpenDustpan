package blob_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/blobtest"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// memStore is a map-backed blob.Store for the view tests.
type memStore struct {
	mu      sync.Mutex
	objects map[blob.Key][]byte
	// listExtra is returned by List in addition to the real objects, as an
	// adapter with a listing fault would.
	listExtra []blob.Object
}

func newMemStore() *memStore {
	return &memStore{mu: sync.Mutex{}, objects: map[blob.Key][]byte{}, listExtra: nil}
}

func (m *memStore) Put(_ context.Context, key blob.Key, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err //nolint:wrapcheck // Test fake.
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.objects[key]; ok {
		return blob.ErrExists
	}

	m.objects[key] = data

	return nil
}

func (m *memStore) Get(_ context.Context, key blob.Key) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, ok := m.objects[key]
	if !ok {
		return nil, blob.ErrNotFound
	}

	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *memStore) Delete(_ context.Context, key blob.Key) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.objects, key)

	return nil
}

func (m *memStore) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	if err := req.Validate(); err != nil {
		return blob.Page{}, err
	}

	if err := ctx.Err(); err != nil {
		return blob.Page{}, fmt.Errorf("list: %w", err)
	}

	dir := req.Dir()

	m.mu.Lock()
	defer m.mu.Unlock()

	out := slices.Clone(m.listExtra)

	for key := range m.objects {
		if strings.HasPrefix(key.String(), dir) {
			out = append(out, blob.Object{ModifiedAt: time.Now().UTC(), Key: key})
		}
	}

	slices.SortFunc(out, func(a, b blob.Object) int { return strings.Compare(a.Key.String(), b.Key.String()) })

	return blob.PageOf(out, req), nil
}

func (m *memStore) Ping(ctx context.Context) error {
	return ctx.Err() //nolint:wrapcheck // Test fake.
}

// TestScopedConformance runs the conformance suite on the view's own
// reference store: a map-backed store that passes the port cases.
func TestScopedConformance(t *testing.T) {
	t.Parallel()

	blobtest.Run(t, func(t *testing.T) blob.Store {
		t.Helper()

		return newMemStore()
	})
}

func TestNewScopedRejects(t *testing.T) {
	t.Parallel()

	if _, err := blob.NewScoped(nil, "ns-aaaaaaaaaaaaaaaaaaaa"); !errors.Is(err, blob.ErrNilStore) {
		t.Fatalf("nil store: err = %v, want ErrNilStore", err)
	}

	for _, id := range []namespace.ID{"", "ns-", "aaa", "ns-UPPER", "ns-a/../b", "ns-a b", "ns-%2e"} {
		if _, err := blob.NewScoped(newMemStore(), id); !errors.Is(err, blob.ErrInvalidKey) {
			t.Errorf("ID %q: err = %v, want ErrInvalidKey", id, err)
		}
	}
}

// TestNewScopedErrorHoldsNoID proves that the error of a refused namespace ID
// does not show the ID. A wrong value can be the ID of another namespace, and
// an error often reaches a log.
func TestNewScopedErrorHoldsNoID(t *testing.T) {
	t.Parallel()

	for _, id := range []namespace.ID{"ns-UPPERCASEID", "ns-secret id", "ns-secret/../b", "ns-secret%2e"} {
		_, err := blob.NewScoped(newMemStore(), id)
		if err == nil {
			t.Fatalf("ID %q: no error", id)
		}

		if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "UPPERCASEID") ||
			strings.Contains(err.Error(), id.String()) {
			t.Errorf("ID %q: the error shows the ID: %q", id, err)
		}
	}
}

func TestScopedKeyAndPrefix(t *testing.T) {
	t.Parallel()

	v, err := blob.NewScoped(newMemStore(), "ns-abc")
	if err != nil {
		t.Fatalf("new scoped: %v", err)
	}

	if got, want := v.Prefix(), "namespaces/ns-abc/"; got != want {
		t.Fatalf("Prefix = %q, want %q", got, want)
	}

	if key, err := v.Key("item-keys/ik-1/v1"); err != nil || key.String() != "namespaces/ns-abc/item-keys/ik-1/v1" {
		t.Fatalf("Key = %q, %v", key, err)
	}

	for _, rel := range []string{"", "../x", "a//b", "A", "a/./b"} {
		if _, err := v.Key(rel); !errors.Is(err, blob.ErrInvalidKey) {
			t.Errorf("Key(%q): err = %v, want ErrInvalidKey", rel, err)
		}
	}
}

// TestScopedListFailsClosed proves that the view refuses a listing in which
// the store shows a key outside the scope.
func TestScopedListFailsClosed(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	store.listExtra = []blob.Object{{ModifiedAt: time.Time{}, Key: "namespaces/ns-other/x"}}

	v, err := blob.NewScoped(store, "ns-abc")
	if err != nil {
		t.Fatalf("new scoped: %v", err)
	}

	objects, err := blobtest.ListAll(t.Context(), v, "namespaces/ns-abc/")
	if !errors.Is(err, blob.ErrOutOfScope) || len(objects) != 0 {
		t.Fatalf("list = %v, %v; want no objects and ErrOutOfScope", objects, err)
	}
}

// nextFault is a store that answers a Next key outside the scope, as an
// adapter with a listing fault would.
type nextFault struct {
	*memStore

	next blob.Key
}

func (n nextFault) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	page, err := n.memStore.ListPage(ctx, req)
	page.Next = n.next

	return page, err
}

// TestScopedListPageRefusesBadRequests proves the view fails closed on a
// prefix outside the scope, on a Next key outside the scope, and on a request
// that the store refuses.
func TestScopedListPageRefusesBadRequests(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		store func() blob.Store
		req   blob.PageRequest
		want  error
	}{
		{
			"empty prefix",
			func() blob.Store { return newMemStore() },
			blob.PageRequest{Prefix: "", After: "", Limit: 10},
			blob.ErrOutOfScope,
		},
		{
			"prefix of another namespace",
			func() blob.Store { return newMemStore() },
			blob.PageRequest{Prefix: "namespaces/ns-other/", After: "", Limit: 10},
			blob.ErrOutOfScope,
		},
		{
			"next key of another namespace",
			func() blob.Store { return nextFault{memStore: newMemStore(), next: "namespaces/ns-other/x"} },
			blob.PageRequest{Prefix: "namespaces/ns-abc/", After: "", Limit: 10},
			blob.ErrOutOfScope,
		},
		{
			"limit out of range",
			func() blob.Store { return newMemStore() },
			blob.PageRequest{Prefix: "namespaces/ns-abc/", After: "", Limit: 0},
			blob.ErrInvalidPage,
		},
		{
			"after outside the prefix",
			func() blob.Store { return newMemStore() },
			blob.PageRequest{Prefix: "namespaces/ns-abc/", After: "namespaces/ns-other/x", Limit: 10},
			blob.ErrInvalidKey,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			v, err := blob.NewScoped(tc.store(), "ns-abc")
			if err != nil {
				t.Fatalf("new scoped: %v", err)
			}

			page, err := v.ListPage(t.Context(), tc.req)
			if !errors.Is(err, tc.want) || len(page.Objects) != 0 || page.Next != "" {
				t.Fatalf("page = %v, err = %v; want an empty page and %v", page, err, tc.want)
			}
		})
	}
}

// TestScopedWorksWithDeleteRetried proves the view is a Store for the helpers
// of the package.
func TestScopedWorksWithDeleteRetried(t *testing.T) {
	t.Parallel()

	v, err := blob.NewScoped(newMemStore(), "ns-abc")
	if err != nil {
		t.Fatalf("new scoped: %v", err)
	}

	key, err := v.Key("x")
	if err != nil {
		t.Fatalf("key: %v", err)
	}

	if err := v.Put(t.Context(), key, strings.NewReader("x")); err != nil {
		t.Fatalf("put: %v", err)
	}

	if err := blob.DeleteRetried(t.Context(), v, key); err != nil {
		t.Fatalf("delete retried: %v", err)
	}
}
