// Package blob defines the object storage port for large, immutable payloads.
// The first user of the port is OpenTofu state content. That user writes keys
// as tofu/<workspace ID>/<state version ID>/<attempt>. The port does not
// enforce a layout. Adapters live in subpackages (blob/fs) or in user code.
// blob/blobtest is the conformance suite every adapter must pass.
package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Sentinel errors returned by Store implementations.
var (
	// ErrNotFound reports that no object exists under the key.
	ErrNotFound = errors.New("blob not found")
	// ErrInvalidKey reports a key that fails validation.
	ErrInvalidKey = errors.New("invalid blob key")
	// ErrExists reports that an object already exists under the key.
	ErrExists = errors.New("blob already exists")
)

const maxKeyLength = 512

// Key is a validated object key: '/'-separated segments of [a-z0-9._-],
// without empty, "." or ".." segments.
type Key string

// ParseKey validates s as an object key.
func ParseKey(s string) (Key, error) {
	if s == "" || len(s) > maxKeyLength {
		return "", fmt.Errorf("%w: %q", ErrInvalidKey, s)
	}

	for segment := range strings.SplitSeq(s, "/") {
		if !validSegment(segment) {
			return "", fmt.Errorf("%w: %q", ErrInvalidKey, s)
		}
	}

	return Key(s), nil
}

// String returns the key as a plain string.
func (k Key) String() string {
	return string(k)
}

// Object is one stored object: its key and when it was last modified, in
// UTC. A sweeper uses the time to keep a grace period for uploads that
// still stream.
type Object struct {
	ModifiedAt time.Time
	Key        Key
}

// Store is the object storage port. A caller writes each key once. A caller
// never calls Put on a key that already holds an object. Put on an existing
// key fails with ErrExists and leaves the first object unchanged.
type Store interface {
	// Put stores the contents of r under key. It returns ErrExists when the
	// key already holds an object.
	Put(ctx context.Context, key Key, r io.Reader) error
	// Get opens the object under key, or returns ErrNotFound. The caller closes it.
	Get(ctx context.Context, key Key) (io.ReadCloser, error)
	// Delete removes the object under key. Deleting a missing key is not an error.
	Delete(ctx context.Context, key Key) error
	// List returns the objects under prefix, ordered by key. Prefix names a
	// key directory such as "<unit>/", with or without a trailing slash; a
	// prefix that names no directory yields an empty list. A caller lists
	// one unit to find stored objects no record references.
	List(ctx context.Context, prefix string) ([]Object, error)
	// Ping returns nil when the store can serve a request, and an error
	// otherwise. The error holds no credential and no path.
	Ping(ctx context.Context) error
}

func validSegment(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}

	for _, c := range []byte(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-', c == '_', c == '.':
		default:
			return false
		}
	}

	return true
}
