// Package blob defines the object storage port used for large, immutable
// payloads such as checkpoints. Adapters live in subpackages (blob/fs) or in
// user code; blob/blobtest is the conformance suite every adapter must pass.
package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Sentinel errors returned by Store implementations.
var (
	// ErrNotFound reports that no object exists under the key.
	ErrNotFound = errors.New("blob not found")
	// ErrInvalidKey reports a key that fails validation.
	ErrInvalidKey = errors.New("invalid blob key")
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

// Store is the object storage port. Objects are written whole and are
// immutable once written; Put on an existing key replaces it atomically.
type Store interface {
	// Put stores the contents of r under key, replacing any existing object.
	Put(ctx context.Context, key Key, r io.Reader) error
	// Get opens the object under key, or returns ErrNotFound. The caller closes it.
	Get(ctx context.Context, key Key) (io.ReadCloser, error)
	// Delete removes the object under key. Deleting a missing key is not an error.
	Delete(ctx context.Context, key Key) error
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
