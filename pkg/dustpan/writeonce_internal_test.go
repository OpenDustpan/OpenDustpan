package dustpan

import (
	"bytes"
	"context"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/blob"
)

const writeOnceNamespace = "ns-abcdefghijklmnopqrst"

// TestWriteOnceKey proves which keys the blob cache may keep: the OpenTofu
// state of a namespace, and nothing else. A Pulumi checkpoint of a namespace
// can be written again after a delete, so it never matches.
func TestWriteOnceKey(t *testing.T) {
	t.Parallel()

	ns := "namespaces/" + writeOnceNamespace + "/"

	cases := []struct {
		name string
		key  string
		want bool
	}{
		{"a state version of a namespace", ns + "tofu/ws-1/sv-1/a1", true},
		{"a state version of another namespace", "namespaces/ns-other/tofu/ws-1/sv-1/a1", true},
		{"a Pulumi checkpoint of a namespace", ns + "pulumi/st-1/up-1/1", false},
		{"a Pulumi journal batch of a namespace", ns + "pulumi/st-1/up-1/journal/1", false},
		{"an item key of a namespace", ns + "item-keys/ik-1/v1", false},
		{"a family that only starts like ours", ns + "tofux/ws-1/sv-1/a1", false},
		{"a family with another case", ns + "Tofu/ws-1/sv-1/a1", false},
		{"the family directory without a key", ns + "tofu/", false},
		{"the old layout", "tofu/ws-1/sv-1/a1", false},
		{"the old Pulumi layout", "pulumi/st-1/up-1/1", false},
		{"the namespaces directory only", "namespaces/", false},
		{"a namespace directory only", ns, false},
		{"a family named like a namespace", "namespaces/tofu/ws-1/sv-1/a1", false},
		{"a namespace ID with a bad character", "namespaces/ns-abc$/tofu/ws-1/sv-1/a1", false},
		{"a namespace name for the ID", "namespaces/acme/tofu/ws-1/sv-1/a1", false},
		{"a directory above the namespaces", "x/namespaces/" + writeOnceNamespace + "/tofu/ws-1/sv-1/a1", false},
		{"a state under an audit sink prefix", "audit/" + ns + "tofu/ws-1/sv-1/a1", false},
		{"an empty key", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := writeOnceKey(blob.Key(tc.key)); got != tc.want {
				t.Errorf("writeOnceKey(%q) = %v, want %v", tc.key, got, tc.want)
			}
		})
	}
}

// getCounter counts the Get calls that reach the store under it.
type getCounter struct {
	blob.Store

	mu   sync.Mutex
	gets int
}

func (c *getCounter) Get(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	c.mu.Lock()
	c.gets++
	c.mu.Unlock()

	return c.Store.Get(ctx, key)
}

func (c *getCounter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.gets
}

// memoryBlobs is a blob store in memory.
type memoryBlobs struct {
	mu      sync.Mutex
	objects map[blob.Key][]byte
}

func (m *memoryBlobs) Put(_ context.Context, key blob.Key, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err //nolint:wrapcheck // A test double.
	}

	m.mu.Lock()
	m.objects[key] = data
	m.mu.Unlock()

	return nil
}

func (m *memoryBlobs) Get(_ context.Context, key blob.Key) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, ok := m.objects[key]
	if !ok {
		return nil, blob.ErrNotFound
	}

	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *memoryBlobs) Delete(_ context.Context, key blob.Key) error {
	m.mu.Lock()
	delete(m.objects, key)
	m.mu.Unlock()

	return nil
}

// ListPage implements blob.Store: it cuts the page that req selects from the
// objects of list.
func (m *memoryBlobs) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
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

func (m *memoryBlobs) Ping(context.Context) error { return nil }

func (m *memoryBlobs) list(context.Context, string) ([]blob.Object, error) {
	return nil, nil
}

// TestWrapBlobCacheKeepsOnlyTheStateOfANamespace proves the wiring: with a
// cache size, a second read of the state of a namespace stays in the cache,
// and a second read of a Pulumi checkpoint of the same namespace reaches the
// store. The state of another namespace has its own key, so it never hits the
// entry of the first.
func TestWrapBlobCacheKeepsOnlyTheStateOfANamespace(t *testing.T) {
	t.Parallel()

	ns := "namespaces/" + writeOnceNamespace + "/"
	inner := &memoryBlobs{mu: sync.Mutex{}, objects: map[blob.Key][]byte{}}
	counter := &getCounter{Store: inner, mu: sync.Mutex{}, gets: 0}
	cfg := defaultSettings()
	cfg.blobStore = counter
	cfg.blobCacheBytes = 1 << 20

	if err := wrapBlobCache(cfg); err != nil {
		t.Fatalf("wrap blob cache: %v", err)
	}

	read := func(key string) {
		t.Helper()

		rc, err := cfg.workspaceBlobs.Get(t.Context(), blob.Key(key))
		if err != nil {
			t.Fatalf("get %q: %v", key, err)
		}

		if _, err = io.Copy(io.Discard, rc); err != nil {
			t.Fatalf("read %q: %v", key, err)
		}

		if err = rc.Close(); err != nil {
			t.Fatalf("close %q: %v", key, err)
		}
	}

	state := ns + "tofu/ws-1/sv-1/a1"
	checkpoint := ns + "pulumi/st-1/up-1/1"
	foreign := "namespaces/ns-zzzzzzzzzzzzzzzzzzzz/tofu/ws-1/sv-1/a1"

	for _, key := range []string{state, checkpoint, foreign} {
		if err := inner.Put(t.Context(), blob.Key(key), strings.NewReader("bytes")); err != nil {
			t.Fatalf("put %q: %v", key, err)
		}
	}

	for _, tc := range []struct {
		key        string
		storeReads int
	}{
		{state, 1},
		{checkpoint, 2},
		{foreign, 1},
	} {
		before := counter.count()

		read(tc.key)
		read(tc.key)

		if got := counter.count() - before; got != tc.storeReads {
			t.Errorf("two reads of %q reached the store %d times, want %d", tc.key, got, tc.storeReads)
		}
	}
}
