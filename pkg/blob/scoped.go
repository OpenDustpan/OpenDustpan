package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// ErrOutOfScope reports a key or a prefix outside the scope of a Scoped
// view. The view returns it before any call to the store, also for a key that
// fails validation. The error holds no key, so it shows nothing about the
// namespace that owns the key.
var ErrOutOfScope = errors.New("blob key out of scope")

// ErrNilStore reports a Scoped view made without a store.
var ErrNilStore = errors.New("scoped blob view needs a store")

// namespacesDir starts the prefix of every namespace.
const namespacesDir = "namespaces/"

// Scoped is a view of a Store for one namespace. It refuses each key outside
// the prefix "namespaces/<namespace ID>/" with ErrOutOfScope, before any call
// to the store. It implements Store, so a domain service of a namespace takes
// the view where it took the store.
type Scoped struct {
	store Store
	// prefix is "namespaces/<namespace ID>/", with the trailing slash.
	prefix string
}

var _ Store = (*Scoped)(nil)

// NewScoped returns the view of s for the namespace id. It returns
// ErrNilStore when s is nil, and ErrInvalidKey when id cannot be part of a
// key (for example an ID with an upper-case letter).
func NewScoped(s Store, id namespace.ID) (*Scoped, error) {
	if s == nil {
		return nil, ErrNilStore
	}

	if _, err := namespace.ParseID(id.String()); err != nil {
		return nil, fmt.Errorf("%w: namespace ID: %w", ErrInvalidKey, err)
	}

	prefix := namespacesDir + id.String() + "/"

	if _, err := ParseKey(prefix + "x"); err != nil {
		return nil, fmt.Errorf("%w: namespace ID %q", ErrInvalidKey, id)
	}

	return &Scoped{store: s, prefix: prefix}, nil
}

// Prefix returns the key prefix of the view: "namespaces/<namespace ID>/".
func (v *Scoped) Prefix() string {
	return v.prefix
}

// Key returns the key for rel inside the scope of the view. It returns
// ErrInvalidKey when the joined key is not a valid key.
func (v *Scoped) Key(rel string) (Key, error) {
	return ParseKey(v.prefix + rel)
}

// Put stores r under key. It returns ErrOutOfScope, and does not read r,
// when key is outside the scope.
func (v *Scoped) Put(ctx context.Context, key Key, r io.Reader) error {
	if !v.inScope(key) {
		return ErrOutOfScope
	}

	return v.store.Put(ctx, key, r)
}

// Get opens the object under key. It returns ErrOutOfScope when key is
// outside the scope.
func (v *Scoped) Get(ctx context.Context, key Key) (io.ReadCloser, error) {
	if !v.inScope(key) {
		return nil, ErrOutOfScope
	}

	return v.store.Get(ctx, key)
}

// Delete removes the object under key. It returns ErrOutOfScope when key is
// outside the scope.
func (v *Scoped) Delete(ctx context.Context, key Key) error {
	if !v.inScope(key) {
		return ErrOutOfScope
	}

	return v.store.Delete(ctx, key)
}

// List returns the objects under prefix, which must name a directory inside
// the scope, with or without a trailing slash. The empty prefix, and a prefix
// that is the parent of the scope or only a string prefix of it, return
// ErrOutOfScope. If the store returns a key outside the scope, List returns
// ErrOutOfScope and no objects.
func (v *Scoped) List(ctx context.Context, prefix string) ([]Object, error) {
	dir := strings.TrimSuffix(prefix, "/")

	if !v.dirInScope(dir) {
		return nil, ErrOutOfScope
	}

	objects, err := v.store.List(ctx, prefix)
	if err != nil {
		return nil, err
	}

	// Defense in depth: the store lists only under prefix. This check stays so
	// that an adapter with a listing fault cannot show a key of another namespace.
	for i := range objects {
		if !v.inScope(objects[i].Key) {
			return nil, ErrOutOfScope
		}
	}

	return objects, nil
}

// Ping checks the store. It takes no key, so the scope does not apply.
func (v *Scoped) Ping(ctx context.Context) error {
	return v.store.Ping(ctx)
}

// inScope reports whether key is a valid key inside the prefix of the view.
// The check is on whole segments: "namespaces/<id>x/..." does not match.
//
// Defense in depth: the callers build keys with ParseKey, and the adapters
// validate keys too. This check stays so that a Key made with a plain
// conversion, which skips validation, still cannot leave the scope.
func (v *Scoped) inScope(key Key) bool {
	s := key.String()

	if len(s) <= len(v.prefix) || !strings.HasPrefix(s, v.prefix) {
		return false
	}

	_, err := ParseKey(s)

	return err == nil
}

// dirInScope reports whether dir, a directory without a trailing slash, is
// the scope directory or a valid directory inside it.
func (v *Scoped) dirInScope(dir string) bool {
	if dir == strings.TrimSuffix(v.prefix, "/") {
		return true
	}

	if !strings.HasPrefix(dir, v.prefix) {
		return false
	}

	_, err := ParseKey(dir)

	return err == nil
}
