// Package blob defines the object storage port for large, immutable payloads.
// The first user of the port is OpenTofu state content. That user writes keys
// as namespaces/<namespace ID>/tofu/<workspace ID>/<state version ID>/<attempt>.
// The port does not enforce a layout. Adapters live in subpackages (blob/fs, blob/s3) or in
// user code. blob/blobtest is the conformance suite every adapter must pass.
//
// The S3 adapter sends each object with one PUT of 5 GiB at most. Its suite
// needs a live S3-compatible server. Start a local server. Create one bucket.
// Set DUSTPAN_TEST_S3_ENDPOINT, DUSTPAN_TEST_S3_BUCKET,
// DUSTPAN_TEST_S3_ACCESS_KEY and DUSTPAN_TEST_S3_SECRET_KEY. Run make
// test-s3. Any S3-compatible server works. No specific server is required.
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
	// ErrExists reports that an object already exists under the key. A
	// store can return it from Put, but a caller must not depend on it to
	// find the one winner of two writes.
	ErrExists = errors.New("blob already exists")
	// ErrThrottled reports that the store asked the client to slow down, for
	// example with an S3 SlowDown answer, and the retries of the adapter did
	// not get through. A caller backs off and tries again later.
	ErrThrottled = errors.New("blob store throttled the request")
	// ErrInvalidPage reports a page request with a limit out of range.
	ErrInvalidPage = errors.New("invalid blob list page")
)

// DeleteConcurrency is the number of Delete calls that one caller runs in
// parallel when it deletes many objects. It is fixed: no flag and no setting
// changes it.
const DeleteConcurrency = 8

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

// Store is the object storage port.
//
// A store does not decide which of two writes wins. The database decides: a
// caller writes each attempt under a key that is unique to that attempt, and
// the fenced row that names the key decides which attempt counts. A blob
// that no committed row names is garbage, and a reaper deletes it. So the
// correctness of a caller never depends on a store capability, for example
// on conditional writes.
//
// A caller that deletes many objects calls Delete in parallel, with a fixed
// concurrency of DeleteConcurrency, and backs off on ErrThrottled. The port
// has no batch delete.
type Store interface {
	// Put stores the contents of r under key. Put on a key that already
	// holds an object can replace the object, or fail with ErrExists and
	// leave the old object unchanged. A reader sees the whole old object or
	// the whole new object, never a part of each. A failed Put leaves the
	// old object, or no object when the key held none.
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
