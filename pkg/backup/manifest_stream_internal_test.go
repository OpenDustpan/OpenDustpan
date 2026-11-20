package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
)

// streamManifest returns a manifest with every optional part set, so that the
// stream and json.Marshal meet each field.
func streamManifest() Manifest {
	return Manifest{
		CreatedAt:            time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC),
		ID:                   "bkp-stream",
		Dialect:              DialectSQLite,
		PrimaryInstallations: []string{`inst "blobs":[]`, "b"},
		WrapperIDs:           []string{"w"},
		Blobs:                nil,
		Version:              "v",
		SQLSHA256:            "00",
		Format:               FormatVersion,
		SchemaVersion:        BaselineSchemaVersion,
		SQLSize:              7,
		Attempt:              "1",
		RowCounts:            map[string]int64{"blobs": 1, "a": 2},
		SQLFormat:            PortableFormat,
		SQLFiles:             []PortableFile{},
		InstallationKind:     "production",
		Unverified:           []string{`k"blobs":[]`},
		Damaged:              []DamagedBlob{},
		RegistryFiles:        map[string]RegistryFileSection{"blobs": {Version: 0, Files: nil}},
	}
}

// TestWriteManifestJSONMatchesMarshal checks that the stream is the same bytes
// as json.Marshal of the whole manifest, for no blob, one blob and many blobs,
// also when a string of the manifest holds the text of an empty blob list.
func TestWriteManifestJSONMatchesMarshal(t *testing.T) {
	t.Parallel()

	many := make([]ManifestBlob, 50)
	for i := range many {
		many[i] = ManifestBlob{
			Key: strings.Repeat(
				"k",
				i,
			) + `"blobs":[]`,
			Size:   int64(i),
			MD5:    "00112233445566778899aabbccddeeff",
			Sealed: i%2 == 0,
		}
	}

	cases := map[string][]ManifestBlob{
		"no blob":   {},
		"one blob":  many[:1],
		"many blob": many,
	}

	for name, blobs := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			manifest := streamManifest()

			var got bytes.Buffer
			if err := writeManifestJSON(&got, manifest, blobs); err != nil {
				t.Fatalf("writeManifestJSON = %v", err)
			}

			manifest.Blobs = blobs

			want, err := json.Marshal(manifest)
			if err != nil {
				t.Fatalf("Marshal = %v", err)
			}

			if !bytes.Equal(got.Bytes(), want) {
				t.Errorf("stream differs from json.Marshal:\n got %.200s\nwant %.200s", got.String(), want)
			}
		})
	}
}

// failWriter fails the write after n bytes.
type failWriter struct{ left int }

var errFailWrite = errors.New("write failed")

// Write implements io.Writer.
func (f *failWriter) Write(p []byte) (int, error) {
	if len(p) > f.left {
		return 0, errFailWrite
	}

	f.left -= len(p)

	return len(p), nil
}

// TestWriteManifestJSONStopsOnWriteError checks that a failed write stops the
// stream and that the error reaches the caller.
func TestWriteManifestJSONStopsOnWriteError(t *testing.T) {
	t.Parallel()

	blobs := []ManifestBlob{
		{Key: "a", Size: 1, MD5: "00", Sealed: false},
		{Key: "b", Size: 1, MD5: "00", Sealed: false},
	}

	var whole bytes.Buffer
	if err := writeManifestJSON(&whole, streamManifest(), blobs); err != nil {
		t.Fatalf("writeManifestJSON = %v", err)
	}

	// A failure in the head, between two blobs and in the tail.
	for _, left := range []int{0, whole.Len() / 2, whole.Len() - 1} {
		err := writeManifestJSON(&failWriter{left: left}, streamManifest(), blobs)
		if !errors.Is(err, errFailWrite) {
			t.Errorf("left %d: error = %v, want errFailWrite", left, err)
		}
	}
}

// refusingTarget is a blob store whose Put returns an error at once, without
// a read of the stream.
type refusingTarget struct{ blob.Store }

var errRefusePut = errors.New("put refused")

// Put implements blob.Store.
func (refusingTarget) Put(context.Context, blob.Key, io.Reader) error { return errRefusePut }

// TestPutManifestReturnsPutErrorAndStopsEncoder checks that a target that
// leaves without a read of the stream gives its error to the caller and does
// not leave the encoder goroutine blocked (the goleak check of the package
// fails the run when it stays).
func TestPutManifestReturnsPutErrorAndStopsEncoder(t *testing.T) {
	t.Parallel()

	blobs := make([]ManifestBlob, 1000)
	for i := range blobs {
		blobs[i] = ManifestBlob{Key: "k", Size: 1, MD5: "00", Sealed: false}
	}

	key, err := blob.ParseKey("backups/bkp-x/1/manifest.json")
	if err != nil {
		t.Fatalf("key: %v", err)
	}

	s := serviceOver(refusingTarget{Store: nil})

	if err := s.putManifest(t.Context(), key, streamManifest(), blobs); !errors.Is(err, errRefusePut) {
		t.Errorf("putManifest = %v, want errRefusePut", err)
	}
}

// serviceOver returns a service that has target as its backup target and no
// other dependency.
func serviceOver(target blob.Store) *Service {
	return &Service{
		exporter: nil, kinds: nil, registry: nil, source: nil, opener: nil, target: target, targetName: "",
		now: nil, version: "bench", maxSQL: 0, maxUnverified: 0, runner: nil, operations: nil, lease: 0, logger: nil,
	}
}

// emptySummary returns a summary with no value.
func emptySummary() Summary {
	return Summary{
		Dialect: "", PrimaryInstallations: nil, WrapperIDs: nil, RowCounts: nil, Blobs: nil, RegistryFiles: nil,
		Unverified: nil, SchemaVersion: 0,
	}
}

// shortReadTarget is a blob store whose Put reads n bytes of the stream and
// returns nil.
type shortReadTarget struct {
	blob.Store

	n int64
}

// Put implements blob.Store.
func (s shortReadTarget) Put(_ context.Context, _ blob.Key, r io.Reader) error {
	_, err := io.CopyN(io.Discard, r, s.n)
	if err != nil {
		return fmt.Errorf("short read: %w", err)
	}

	return nil
}

// TestPutManifestRejectsPartialRead checks that a target that reads a part of
// the stream and returns nil does not make a partial manifest valid.
func TestPutManifestRejectsPartialRead(t *testing.T) {
	t.Parallel()

	blobs := make([]ManifestBlob, 1000)
	for i := range blobs {
		blobs[i] = ManifestBlob{Key: "k", Size: 1, MD5: "00", Sealed: false}
	}

	key, err := blob.ParseKey("backups/bkp-x/1/manifest.json")
	if err != nil {
		t.Fatalf("key: %v", err)
	}

	s := serviceOver(shortReadTarget{Store: nil, n: 100})

	if err := s.putManifest(t.Context(), key, streamManifest(), blobs); err == nil {
		t.Error("putManifest = nil after a read of 100 bytes, want an error")
	}
}

// drainTarget is a blob store whose Put reads the stream to its end or its
// first error and returns nil in both cases. It keeps the read error in
// readErr.
type drainTarget struct {
	blob.Store

	readErr *error
}

// Put implements blob.Store.
func (d drainTarget) Put(_ context.Context, _ blob.Key, r io.Reader) error {
	// The target returns nil after a failed stream on purpose: a target must
	// not hide an encoder error from the caller.
	_, *d.readErr = io.Copy(io.Discard, r)

	return nil
}

// TestPutManifestReturnsEncoderError checks that an encoder error reaches the
// caller when Put returns nil.
func TestPutManifestReturnsEncoderError(t *testing.T) {
	t.Parallel()

	key, err := blob.ParseKey("backups/bkp-x/1/manifest.json")
	if err != nil {
		t.Fatalf("key: %v", err)
	}

	var readErr error

	s := serviceOver(drainTarget{Store: nil, readErr: &readErr})

	// time.Time refuses to encode a year after 9999, so json.Marshal of the
	// manifest fails in writeManifestJSON before the first write.
	manifest := streamManifest()
	manifest.CreatedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)

	if err := s.putManifest(t.Context(), key, manifest, nil); err == nil {
		t.Error("putManifest = nil, want the encoder error")
	}

	if readErr == nil {
		t.Error("the target read no error, so the test did not fail the stream")
	}
}
