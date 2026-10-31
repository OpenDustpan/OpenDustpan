package blobtest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Namespace IDs of the scope cases. They are valid IDs. scopeA is the
// namespace of the view under test, scopeB the namespace that must stay out
// of reach. scopeA + "x" is another valid ID that scopeA matches as a string.
const (
	scopeA  = namespace.ID("ns-aaaaaaaaaaaaaaaaaaaa")
	scopeB  = namespace.ID("ns-bbbbbbbbbbbbbbbbbbbb")
	scopeAx = namespace.ID("ns-aaaaaaaaaaaaaaaaaaaax")
)

// runScope executes the cases of the scoped view (blob.Scoped) against stores
// produced by newStore. Run calls it, so each adapter that passes Run also
// passes the view: an adapter that the view wraps must keep a key of another
// namespace out of reach.
//
// Each case gives the view a store that counts its calls, so a case can prove
// that the view refuses a key before it calls the store.
func runScope(t *testing.T, newStore Factory) {
	t.Helper()

	t.Run("scope: a key inside the prefix round-trips", func(t *testing.T) {
		t.Parallel()
		testScopeInside(t, newStore(t))
	})

	t.Run("scope: a hostile key is refused before the store", func(t *testing.T) {
		t.Parallel()
		testScopeHostileKeys(t, newStore(t))
	})

	t.Run("scope: a key of another namespace is refused and stays intact", func(t *testing.T) {
		t.Parallel()
		testScopeOtherNamespace(t, newStore(t))
	})

	t.Run("scope: list stays inside the prefix", func(t *testing.T) {
		t.Parallel()
		testScopeList(t, newStore(t))
	})

	t.Run("scope: a hostile list prefix is refused before the store", func(t *testing.T) {
		t.Parallel()
		testScopeHostilePrefixes(t, newStore(t))
	})

	t.Run("scope: ping reaches the store", func(t *testing.T) {
		t.Parallel()
		testScopePing(t, newStore(t))
	})
}

// countingStore counts the calls that reach the wrapped store.
type countingStore struct {
	blob.Store

	calls atomic.Int64
}

func (c *countingStore) Put(ctx context.Context, key blob.Key, r io.Reader) error {
	c.calls.Add(1)

	return c.Store.Put(ctx, key, r)
}

func (c *countingStore) Get(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	c.calls.Add(1)

	return c.Store.Get(ctx, key)
}

func (c *countingStore) Delete(ctx context.Context, key blob.Key) error {
	c.calls.Add(1)

	return c.Store.Delete(ctx, key)
}

func (c *countingStore) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	c.calls.Add(1)

	return c.Store.ListPage(ctx, req)
}

func (c *countingStore) Ping(ctx context.Context) error {
	c.calls.Add(1)

	return c.Store.Ping(ctx)
}

// scopeViews returns the counting store over s and a view of it for each of
// the namespaces.
func scopeViews(t *testing.T, s blob.Store, ids ...namespace.ID) (*countingStore, []*blob.Scoped) {
	t.Helper()

	counted := &countingStore{Store: s, calls: atomic.Int64{}}
	views := make([]*blob.Scoped, 0, len(ids))

	for _, id := range ids {
		v, err := blob.NewScoped(counted, id)
		if err != nil {
			t.Fatalf("new scoped view for %s: %v", id, err)
		}

		views = append(views, v)
	}

	return counted, views
}

func testScopeInside(t *testing.T, s blob.Store) {
	t.Helper()

	counted, views := scopeViews(t, s, scopeA)
	view := views[0]

	key, err := view.Key("item-keys/ik-1/v1")
	if err != nil {
		t.Fatalf("view key: %v", err)
	}

	want := []byte("sealed")
	put(t, view, key, want)

	// The view writes through to the store: the raw store holds the key.
	if got := get(t, s, key); !bytes.Equal(got, want) {
		t.Fatalf("raw get %s = %q, want %q", key, got, want)
	}

	if got := get(t, view, key); !bytes.Equal(got, want) {
		t.Fatalf("view get %s = %q, want %q", key, got, want)
	}

	if putErr := view.Put(t.Context(), key, bytes.NewReader(want)); !errors.Is(putErr, blob.ErrExists) {
		t.Fatalf("second put through the view: err = %v, want ErrExists", putErr)
	}

	objects, err := ListAll(t.Context(), view, view.Prefix())
	if err != nil {
		t.Fatalf("view list: %v", err)
	}

	if keys := objectKeys(objects); !slices.Equal(keys, []string{key.String()}) {
		t.Fatalf("view list = %v, want [%s]", keys, key)
	}

	if err := view.Delete(t.Context(), key); err != nil {
		t.Fatalf("view delete: %v", err)
	}

	if _, err := view.Get(t.Context(), key); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("view get after delete: err = %v, want ErrNotFound", err)
	}

	if counted.calls.Load() == 0 {
		t.Fatal("the view made no call to the store for a key inside the scope")
	}
}

// hostileKeys returns keys that the view of scopeA must refuse. Each one is
// built with a plain conversion, as a caller that skips ParseKey would.
func hostileKeys() map[string]string {
	a := "namespaces/" + scopeA.String()
	b := "namespaces/" + scopeB.String()

	return map[string]string{
		"empty":                    "",
		"no prefix":                "tofu/ws-1/sv-1/a1",
		"other namespace":          b + "/item-keys/ik-1/v1",
		"string prefix only":       "namespaces/" + scopeAx.String() + "/item-keys/ik-1/v1",
		"scope directory itself":   a,
		"scope directory, slash":   a + "/",
		"namespaces directory":     "namespaces/",
		"dot-dot to other":         a + "/../" + scopeB.String() + "/x",
		"dot-dot leading":          "../" + a + "/x",
		"dot-dot after prefix":     a + "/a/../../" + scopeB.String() + "/x",
		"dot segment":              a + "/./x",
		"double slash":             a + "//x",
		"double slash before":      "namespaces//" + scopeA.String() + "/x",
		"leading slash":            "/" + a + "/x",
		"encoded dot-dot":          a + "/%2e%2e/" + scopeB.String() + "/x",
		"encoded slash":            a + "/..%2f" + scopeB.String() + "/x",
		"encoded slash, other":     a + "%2fx",
		"double-encoded separator": a + "/%252e%252e/x",
		"backslash":                a + "\\..\\" + scopeB.String() + "\\x",
		"upper-case prefix":        "NAMESPACES/" + scopeA.String() + "/x",
		"NUL byte":                 a + "/x\x00/../" + scopeB.String() + "/x",
		"newline":                  a + "/x\n" + b + "/x",
		"trailing dot-dot":         a + "/..",
		"too long":                 a + "/" + strings.Repeat("a", 600),
		// Characters that look like the ASCII ones. A comparison after a
		// normalization could treat them as the ASCII character, so the view
		// must refuse each of them as it is.
		"fullwidth solidus":        a + "\uff0fx",
		"division slash":           a + "\u2215x",
		"fullwidth namespaces":     "\uff4eamespaces/" + scopeA.String() + "/x",
		"NFD letter after prefix":  a + "/e\u0301x",
		"non-ASCII in the ID":      "namespaces/" + scopeA.String() + "\u00e9/x",
		"non-ASCII byte in the ID": "namespaces/" + scopeA.String() + "\xc3/x",
	}
}

// testScopeHostileKeys sends each hostile key to each method of the view and
// checks ErrOutOfScope, no call to the store and no read of the reader.
func testScopeHostileKeys(t *testing.T, s blob.Store) {
	t.Helper()

	counted, views := scopeViews(t, s, scopeA)
	view := views[0]

	for name, raw := range hostileKeys() {
		key := blob.Key(raw)
		before := counted.calls.Load()
		reader := &countingReader{reads: atomic.Int64{}}

		if err := view.Put(t.Context(), key, reader); !errors.Is(err, blob.ErrOutOfScope) {
			t.Errorf("put %s: err = %v, want ErrOutOfScope", name, err)
		}

		if reader.reads.Load() != 0 {
			t.Errorf("put %s: the view read the body of a refused key", name)
		}

		if rc, err := view.Get(t.Context(), key); !errors.Is(err, blob.ErrOutOfScope) {
			t.Errorf("get %s: err = %v, want ErrOutOfScope", name, err)
		} else if rc != nil {
			t.Errorf("get %s: the view returned a reader with the error", name)
		}

		if err := view.Delete(t.Context(), key); !errors.Is(err, blob.ErrOutOfScope) {
			t.Errorf("delete %s: err = %v, want ErrOutOfScope", name, err)
		}

		if got := counted.calls.Load(); got != before {
			t.Errorf("%s: %d call(s) reached the store, want 0", name, got-before)
		}
	}
}

// countingReader counts the reads of a body.
type countingReader struct {
	reads atomic.Int64
}

func (r *countingReader) Read(_ []byte) (int, error) {
	r.reads.Add(1)

	return 0, io.EOF
}

// testScopeOtherNamespace puts an object in B and the same relative key in A,
// then checks A reaches only its own object and the object of B stays intact.
func testScopeOtherNamespace(t *testing.T, s blob.Store) {
	t.Helper()

	_, views := scopeViews(t, s, scopeA, scopeB)
	viewA, viewB := views[0], views[1]

	keyA, err := viewA.Key("item-keys/ik-1/v1")
	if err != nil {
		t.Fatalf("key of A: %v", err)
	}

	keyB, err := viewB.Key("item-keys/ik-1/v1")
	if err != nil {
		t.Fatalf("key of B: %v", err)
	}

	put(t, viewB, keyB, []byte("secret of B"))
	put(t, viewA, keyA, []byte("secret of A"))

	if _, getErr := viewA.Get(t.Context(), keyB); !errors.Is(getErr, blob.ErrOutOfScope) {
		t.Fatalf("A get of a key of B: err = %v, want ErrOutOfScope", getErr)
	}

	if putErr := viewA.Put(t.Context(), keyB, strings.NewReader("overwrite")); !errors.Is(putErr, blob.ErrOutOfScope) {
		t.Fatalf("A put on a key of B: err = %v, want ErrOutOfScope", putErr)
	}

	if delErr := viewA.Delete(t.Context(), keyB); !errors.Is(delErr, blob.ErrOutOfScope) {
		t.Fatalf("A delete of a key of B: err = %v, want ErrOutOfScope", delErr)
	}

	if got := get(t, viewB, keyB); string(got) != "secret of B" {
		t.Fatalf("object of B after the attempts = %q, want %q", got, "secret of B")
	}

	if got := get(t, viewA, keyA); string(got) != "secret of A" {
		t.Fatalf("object of A = %q, want %q", got, "secret of A")
	}

	// The error text shows nothing about the other namespace.
	_, err = viewA.Get(t.Context(), keyB)
	if err == nil || strings.Contains(err.Error(), scopeB.String()) {
		t.Fatalf("error %v shows the namespace ID of B", err)
	}
}

func testScopeList(t *testing.T, s blob.Store) {
	t.Helper()

	_, views := scopeViews(t, s, scopeA, scopeB, scopeAx)
	viewA, viewB, viewAx := views[0], views[1], views[2]

	for _, v := range []*blob.Scoped{viewA, viewB, viewAx} {
		for _, rel := range []string{"item-keys/ik-1/v2", "item-keys/ik-1/v1", "other/x"} {
			key, err := v.Key(rel)
			if err != nil {
				t.Fatalf("key %s: %v", rel, err)
			}

			put(t, v, key, []byte(rel))
		}
	}

	root := "namespaces/" + scopeA.String()
	want := []string{
		root + "/item-keys/ik-1/v1",
		root + "/item-keys/ik-1/v2",
		root + "/other/x",
	}

	for _, prefix := range []string{root, root + "/"} {
		got, err := ListAll(t.Context(), viewA, prefix)
		if err != nil {
			t.Fatalf("list %q: %v", prefix, err)
		}

		if keys := objectKeys(got); !slices.Equal(keys, want) {
			t.Fatalf("list %q = %v, want %v", prefix, keys, want)
		}
	}

	got, err := ListAll(t.Context(), viewA, root+"/item-keys/")
	if err != nil {
		t.Fatalf("list item-keys: %v", err)
	}

	if keys := objectKeys(got); !slices.Equal(keys, want[:2]) {
		t.Fatalf("list item-keys = %v, want %v", keys, want[:2])
	}

	got, err = ListAll(t.Context(), viewA, root+"/ghost/")
	if err != nil || len(got) != 0 {
		t.Fatalf("list of a missing directory = %v, %v; want empty", objectKeys(got), err)
	}
}

// hostilePrefixes returns list prefixes that the view of scopeA must refuse.
func hostilePrefixes() map[string]string {
	a := "namespaces/" + scopeA.String()
	b := "namespaces/" + scopeB.String()

	return map[string]string{
		"empty":                "",
		"slash":                "/",
		"namespaces":           "namespaces",
		"namespaces, slash":    "namespaces/",
		"no prefix":            "tofu/ws-1/",
		"other namespace":      b + "/",
		"other, no slash":      b,
		"string prefix only":   "namespaces/" + scopeAx.String(),
		"string prefix, slash": "namespaces/" + scopeAx.String() + "/",
		"cut string prefix":    a[:len(a)-3],
		"dot-dot to parent":    a + "/..",
		"dot-dot to other":     a + "/../" + scopeB.String() + "/",
		"dot-dot, trailing":    a + "/a/../../" + scopeB.String(),
		"double slash":         a + "//",
		"double slash inside":  a + "//x/",
		"encoded dot-dot":      a + "/%2e%2e/" + scopeB.String() + "/",
		"encoded slash":        a + "%2f",
		"backslash":            a + "\\..\\",
		"leading slash":        "/" + a + "/",
		"upper-case prefix":    "NAMESPACES/" + scopeA.String() + "/",
		"NUL byte":             a + "/\x00",
		"too long":             a + "/" + strings.Repeat("a", 600),
		"two trailing slashes": a + "/x//",
		"scope plus junk":      a + "x",
		"scope with dot":       a + "./",
		// Characters that look like the ASCII ones (see hostileKeys).
		"fullwidth solidus":        a + "\uff0f",
		"division slash":           a + "\u2215",
		"fullwidth namespaces":     "\uff4eamespaces/" + scopeA.String() + "/",
		"NFD letter after prefix":  a + "/e\u0301/",
		"non-ASCII in the ID":      "namespaces/" + scopeA.String() + "\u00e9/",
		"non-ASCII byte in the ID": "namespaces/" + scopeA.String() + "\xc3/",
	}
}

func testScopeHostilePrefixes(t *testing.T, s blob.Store) {
	t.Helper()

	counted, views := scopeViews(t, s, scopeA)
	view := views[0]

	for name, prefix := range hostilePrefixes() {
		before := counted.calls.Load()

		objects, err := ListAll(t.Context(), view, prefix)
		if !errors.Is(err, blob.ErrOutOfScope) {
			t.Errorf("list %s: err = %v, want ErrOutOfScope", name, err)
		}

		if len(objects) != 0 {
			t.Errorf("list %s: %d object(s) returned with the error", name, len(objects))
		}

		if got := counted.calls.Load(); got != before {
			t.Errorf("list %s: %d call(s) reached the store, want 0", name, got-before)
		}
	}
}

func testScopePing(t *testing.T, s blob.Store) {
	t.Helper()

	counted, views := scopeViews(t, s, scopeA)

	if err := views[0].Ping(t.Context()); err != nil {
		t.Fatalf("ping through the view: %v", err)
	}

	if counted.calls.Load() != 1 {
		t.Fatalf("ping made %d call(s) to the store, want 1", counted.calls.Load())
	}
}
