package workspace

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"time"
)

// The reveal lookup over bytes and over an opener, and the masked set, for
// the tests of this package. Production code reveals through RevealVersion
// and reads the masked set through the masked read.

// StateOpener is stateOpener for the tests.
type StateOpener = stateOpener

// RevealValueFromOpener is revealFromOpener for the tests.
func RevealValueFromOpener(open StateOpener, marks SensitiveMarks, path string) (any, []string, error) {
	return revealFromOpener(open, marks, path)
}

// RevealValue returns the plain value at path of state, and the masked set
// holding the path. The path must be in the masked set, else it fails with
// ErrResourceNotFound, so a caller cannot probe paths. A state the client
// encrypted fails with ErrClientEncrypted. It streams the state to the path
// and keeps no more than one resource instance in memory.
func RevealValue(state []byte, marks SensitiveMarks, path string) (any, []string, error) {
	return revealFromOpener(func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(state)), nil
	}, marks, path)
}

// MaskedPaths returns the masked set of state: the value path of each marker
// of every resource instance, masked with marks. With no kept marks for the
// version, every attribute value is a marker.
func MaskedPaths(state []byte, marks SensitiveMarks) ([]string, error) {
	return maskedResourcePaths(context.Background(), state, marks)
}

// MaskedPathsContext is MaskedPaths with the context of the read, for the tests.
func MaskedPathsContext(ctx context.Context, state []byte, marks SensitiveMarks) ([]string, error) {
	return maskedResourcePaths(ctx, state, marks)
}

// MaskedMaxDuplicateWork is the most comparisons of the duplicate pass of one
// instance, for the tests.
const MaskedMaxDuplicateWork = maskedMaxDuplicateWork

// MaskedCursorSpec names what a test cursor of the masked read binds.
type MaskedCursorSpec struct {
	Unit    string
	Version string
	Subject string
	Offset  int
	At      time.Time
}

// NewTestCursor makes a cursor of the masked read for the tests, signed with
// key and bound as spec says.
func NewTestCursor(key []byte, spec MaskedCursorSpec) (string, error) {
	return mintMaskedCursor(
		key, maskedBinding{Unit: spec.Unit, Version: spec.Version, Subject: spec.Subject}, spec.Offset, spec.At,
	)
}

// NewTestSignedCursor returns a cursor of the masked read over an arbitrary
// payload, signed with key, so a test reaches the checks after the signature.
func NewTestSignedCursor(key, payload []byte) string {
	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(signMaskedCursor(key, payload))
}

// LoadMaskedChunks is readMaskedChunks for the tests.
func LoadMaskedChunks(ctx context.Context, r io.Reader) ([]byte, error) {
	return readMaskedChunks(ctx, r)
}

// MaskedChunkBytes is the chunk size of the masked read, for the tests.
const MaskedChunkBytes = maskedChunkBytes

// MaskedMaxVersionBytes is the size limit of the masked read, for the tests.
const MaskedMaxVersionBytes = maskedMaxVersionBytes

// MaskedMaxCursorBytes is the length limit of a cursor, for the tests.
const MaskedMaxCursorBytes = maskedMaxCursorBytes

// MaskedPageMarkers is the size of a page of the masked read, for the tests.
const MaskedPageMarkers = maskedPageMarkers

// MaskedMaxDepth is the deepest nesting of a masked read, for the tests.
const MaskedMaxDepth = maskedMaxDepth

// MaskedMaxMarkers is the most markers of one version, for the tests.
const MaskedMaxMarkers = maskedMaxMarkers

// MaskedStateAndOutputPaths is maskedStatePaths for the tests: the resource markers and
// the sensitive outputs of state, under the bounds of the read.
func MaskedStateAndOutputPaths(ctx context.Context, state []byte, marks SensitiveMarks) ([]string, error) {
	return maskedStatePaths(ctx, state, marks)
}

// MaskedPageAt answers the page of paths that starts at offset, as the masked
// read does after the cursor check. The tests call it with an offset that a
// signed cursor never holds, to reach the guard against a negative offset.
func MaskedPageAt(key []byte, paths []string, offset int) (MaskedPage, error) {
	return maskedPageOf(paths, offset, key, maskedBinding{Unit: "", Version: "sv-1", Subject: ""}, time.Unix(0, 0))
}

// MaskedStatePaths returns the masked set of state with the sensitive outputs
// added, as the masked read builds it, for the tests.
func MaskedStatePaths(ctx context.Context, state []byte, marks SensitiveMarks) ([]string, error) {
	return maskedStatePaths(ctx, state, marks)
}
