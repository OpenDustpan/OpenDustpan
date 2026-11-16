package service

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/registry"
)

// rawEntry is one entry of a test archive for the unpack layer.
type rawEntry struct {
	name   string
	data   []byte
	method uint16
}

// manifestBody is a valid terraform-registry-manifest.json with protocol 5.0.
const manifestBody = `{"version":1,"metadata":{"protocol_versions":["5.0"]}}`

// stored returns an entry that the archive stores without compression, so
// its compressed size is its real size and no ratio bound applies.
func stored(name string, data []byte) rawEntry {
	return rawEntry{name: name, data: data, method: zip.Store}
}

// zipOf returns the bytes of a zip archive with the entries in order. It
// writes each name as it is, also a name that the unpack must refuse.
func zipOf(t *testing.T, entries ...rawEntry) []byte {
	t.Helper()

	var buf bytes.Buffer

	w := zip.NewWriter(&buf)

	for _, e := range entries {
		header := new(zip.FileHeader)
		header.Name = e.name
		header.Method = e.method

		f, err := w.CreateHeader(header)
		if err != nil {
			t.Fatalf("create zip entry %q: %v", e.name, err)
		}

		if _, err := f.Write(e.data); err != nil {
			t.Fatalf("write zip entry %q: %v", e.name, err)
		}
	}

	if err := w.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	return buf.Bytes()
}

// withManifest returns the entries and a valid manifest after them, so that
// the archive passes when the guard under test is removed.
func withManifest(entries ...rawEntry) []rawEntry {
	return append(entries, stored(manifestName, []byte(manifestBody)))
}

// collisionName returns the name of the one entry of archive B: the entry
// holds the bytes of file "a" of archive A and its name forges the H1 line of
// file "b" of archive A.
func collisionName(payloadB []byte) string {
	sum := sha256.Sum256(payloadB)

	return "a\n" + hex.EncodeToString(sum[:]) + "  b"
}

// TestUnpackProviderArchive_hostile sends each hostile archive straight to
// unpackProviderArchive, as if the measure step and the handler had failed.
// Each case names the text of the error of its own guard, so a case fails
// when its guard is removed, also where a later guard refuses the archive
// too.
func TestUnpackProviderArchive_hostile(t *testing.T) {
	t.Parallel()

	payloadX, payloadY := []byte("payload-x"), []byte("payload-y")

	manifest := []byte(manifestBody)
	overManifest := append(slices.Clone(manifest), bytes.Repeat([]byte{' '}, maxManifestBytes+1-len(manifest))...)
	twoValues := []byte(manifestBody + ` {"metadata":{"protocol_versions":["6.0"]}}`)

	// Each case is either raw bytes or the entries of a zip archive.
	cases := []struct {
		name    string
		raw     []byte
		entries []rawEntry
		want    string
	}{
		{
			name: "not a zip file", raw: []byte("this is not a zip file"), entries: nil,
			want: "is not a zip file",
		},
		{
			name: "empty entry name", raw: nil, entries: withManifest(stored("", payloadX)),
			want: "empty entry name",
		},
		{
			name: "backslash prefix", raw: nil, entries: withManifest(stored(`\evil`, payloadX)),
			want: "is absolute",
		},
		{
			name: "backslash inside the name", raw: nil, entries: withManifest(stored(`dir\..\evil`, payloadX)),
			want: "not allowed",
		},
		{
			name: "newline that forges the H1 line of another file", raw: nil,
			entries: withManifest(stored(collisionName(payloadY), payloadX)),
			want:    "not allowed",
		},
		{
			name: "control character", raw: nil, entries: withManifest(stored("a\x00b", payloadX)),
			want: "not allowed",
		},
		{
			name: "duplicate manifest", raw: nil,
			entries: []rawEntry{stored(manifestName, manifest), stored(manifestName, manifest)},
			want:    "duplicate entry",
		},
		{
			name: "file over the per-file bound", raw: nil,
			entries: withManifest(stored("big", make([]byte, maxUnpackedFileBytes+1))),
			want:    fmt.Sprintf("is over %d bytes", maxUnpackedFileBytes),
		},
		{
			name: "manifest over the manifest bound", raw: nil,
			entries: []rawEntry{stored(manifestName, overManifest)},
			want:    fmt.Sprintf("is over %d bytes", maxManifestBytes),
		},
		{
			name: "manifest with more than one JSON value", raw: nil,
			entries: []rawEntry{stored(manifestName, twoValues)},
			want:    "more than one value",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data := tc.raw
			if tc.entries != nil {
				data = zipOf(t, tc.entries...)
			}

			_, err := unpackProviderArchive(data)
			if !errors.Is(err, registry.ErrBadArchive) {
				t.Fatalf("unpack: got %v, want %v", err, registry.ErrBadArchive)
			}

			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unpack: got %q, want the error of the guard %q", err, tc.want)
			}
		})
	}
}

// TestUnpackProviderArchive_atTheBounds checks that a file of exactly the
// per-file bound and a manifest of exactly the manifest bound pass, so the
// hostile cases above test the bound and not another fault of the archive.
func TestUnpackProviderArchive_atTheBounds(t *testing.T) {
	t.Parallel()

	manifest := append([]byte(manifestBody), bytes.Repeat([]byte{' '}, maxManifestBytes-len(manifestBody))...)

	data := zipOf(t, stored("big", make([]byte, maxUnpackedFileBytes)), stored(manifestName, manifest))

	if _, err := unpackProviderArchive(data); err != nil {
		t.Fatalf("unpack at the bounds: %v", err)
	}
}

// TestHashEntries_separatorCollision shows the collision that the name
// allowlist stops: archive A with files a and b, and archive B with one file
// whose name holds a newline, give the same H1 line text. The unpack refuses
// archive B, so no publish can store its H1.
func TestHashEntries_separatorCollision(t *testing.T) {
	t.Parallel()

	payloadX, payloadY := []byte("payload-x"), []byte("payload-y")
	sumX, sumY := sha256.Sum256(payloadX), sha256.Sum256(payloadY)
	sumManifest := sha256.Sum256([]byte(manifestBody))

	h1A := hashEntries(
		[]string{"a", "b", manifestName},
		map[string][]byte{"a": sumX[:], "b": sumY[:], manifestName: sumManifest[:]},
	)

	forged := collisionName(payloadY)
	h1B := hashEntries(
		[]string{forged, manifestName},
		map[string][]byte{forged: sumX[:], manifestName: sumManifest[:]},
	)

	if h1A != h1B {
		t.Fatalf("the test archives do not collide: %s and %s", h1A, h1B)
	}

	archiveA := zipOf(t, stored("a", payloadX), stored("b", payloadY), stored(manifestName, []byte(manifestBody)))

	facts, err := unpackProviderArchive(archiveA)
	if err != nil || facts.h1 != h1A {
		t.Fatalf("unpack archive A: got %q, %v, want %q", facts.h1, err, h1A)
	}

	archiveB := zipOf(t, stored(forged, payloadX), stored(manifestName, []byte(manifestBody)))

	if _, err := unpackProviderArchive(archiveB); !errors.Is(err, registry.ErrBadArchive) {
		t.Fatalf("unpack archive B: got %v, want %v", err, registry.ErrBadArchive)
	}
}

// TestCollectorObserve_secondManifest sends a second manifest straight to
// observe, as if the duplicate check of checkEntry had failed. It fails when
// the manifest check of observe is removed.
func TestCollectorObserve_secondManifest(t *testing.T) {
	t.Parallel()

	c := collector{
		names: nil, hashes: map[string][]byte{}, manifestBytes: nil,
		seen: map[string]bool{}, totalReal: 0, totalCompress: 0,
	}

	entry := checkedEntry{name: manifestName, folded: manifestName, isManifest: true}
	read := readEntry{sum: nil, size: 1, compressed: 1, held: []byte(manifestBody)}

	if err := c.observe(entry, read); err != nil {
		t.Fatalf("observe the first manifest: %v", err)
	}

	err := c.observe(entry, read)
	if !errors.Is(err, registry.ErrBadArchive) || !strings.Contains(err.Error(), "more than one") {
		t.Fatalf("observe a second manifest: got %v, want %v", err, registry.ErrBadArchive)
	}
}
