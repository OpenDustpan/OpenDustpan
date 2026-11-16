package service_test

import (
	"bytes"
	crand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/registry"
)

// archiveCase is one hostile archive: the entries of the zip and the rule it
// breaks.
type archiveCase struct {
	name  string
	files []zipFile
}

// hostileArchives holds one archive for each unpack rule.
func hostileArchives(t *testing.T) []archiveCase {
	t.Helper()

	zeros := make([]byte, 66<<20)

	// The test env stages at most 1 MB of zip. Five entries of 15.5 MB, each
	// with 170 KB of random bytes and zeros after them, give 77.5 MB unpacked
	// in about 0.93 MB of zip. Each entry stays under the per-file bound, and
	// the ratio stays under 100 for every prefix, so only the total bound
	// refuses the fifth entry.
	noise := randomThenZeros(t, 170<<10, 15<<20+512<<10)

	many := make([]zipFile, 0, 1030)
	for i := range 1030 {
		many = append(many, fileEntry(fmt.Sprintf("file-%04d.txt", i), []byte("x")))
	}

	evil := []byte("evil")

	return []archiveCase{
		{name: "dot-dot", files: []zipFile{fileEntry("../../evil", evil)}},
		{name: "absolute", files: []zipFile{fileEntry("/tmp/evil", evil)}},
		{name: "nested dot-dot", files: []zipFile{fileEntry("a/../../evil", evil)}},
		{name: "symlink", files: []zipFile{linkEntry("link", "/etc/passwd")}},
		{
			name:  "duplicate",
			files: []zipFile{fileEntry("a.txt", []byte("x")), fileEntry("a.txt", []byte("y"))},
		},
		{
			name:  "case fold duplicate",
			files: []zipFile{fileEntry("File.TXT", []byte("x")), fileEntry("file.txt", []byte("y"))},
		},
		{name: "too many files", files: many},
		{name: "file over limit", files: []zipFile{fileEntry("big.bin", zeros)}},
		{
			name: "total over limit",
			files: []zipFile{
				fileEntry("a.bin", noise),
				fileEntry("b.bin", noise),
				fileEntry("c.bin", noise),
				fileEntry("d.bin", noise),
				fileEntry("e.bin", noise),
			},
		},
		{name: "ratio", files: []zipFile{fileEntry("zeros.bin", make([]byte, 300<<10))}},
	}
}

// TestPublish_refusesUnsafeArchive checks that a publish refuses an archive
// with an unsafe entry or over an unpack limit with registry.ErrBadArchive,
// and stores no release. It fails on main: traversal and zip bomb entries
// pass unpack.
func TestPublish_refusesUnsafeArchive(t *testing.T) {
	t.Parallel()

	for _, tc := range hostileArchives(t) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t, false)
			sg := newSigner(t)
			e.trust(t, acme, sg)

			s := buildZipSet(t, sg, makeZip(t, tc.files))

			_, err := e.publish(t, acme, s)
			if !errors.Is(err, registry.ErrBadArchive) {
				t.Fatalf("publish of %s: got %v, want %v", tc.name, err, registry.ErrBadArchive)
			}

			requireNoRelease(t, e)

			for _, left := range e.listing(t, e.ids[acme], "staging") {
				if !strings.HasSuffix(left, "/") {
					t.Fatalf("staged file left after refused publish: %s", left)
				}
			}
		})
	}
}

// randomThenZeros returns size bytes: random bytes first, then zeros. The
// random bytes do not compress, and the zeros compress about 1000 times, so
// the whole buffer compresses to about the size of its random prefix.
func randomThenZeros(t *testing.T, random, size int) []byte {
	t.Helper()

	out := make([]byte, size)
	if _, err := crand.Read(out[:random]); err != nil {
		t.Fatalf("make random bytes: %v", err)
	}

	return out
}

// declareCompressedSize returns a copy of the zip bytes data with the
// compressed size of its first central directory entry set to declared. The
// header then claims a size that the entry does not have.
func declareCompressedSize(t *testing.T, data []byte, declared uint32) []byte {
	t.Helper()

	// The central directory header starts with its signature, and the
	// compressed size is the field at byte 20 of that header.
	at := bytes.Index(data, []byte("PK\x01\x02"))
	if at < 0 {
		t.Fatal("the archive has no central directory header")
	}

	out := bytes.Clone(data)
	binary.LittleEndian.PutUint32(out[at+20:], declared)

	return out
}

// TestPublish_refusesLyingDeclaredSize checks that a header which declares a
// large compressed size does not get the archive past the unpack. The entry
// is 4 MB of zeros, so its real compressed bytes give a ratio far over the
// bound. The refusal can come from the zip reader or from the ratio bound;
// either one is a registry.ErrBadArchive, and the release is not stored.
func TestPublish_refusesLyingDeclaredSize(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)
	sg := newSigner(t)
	e.trust(t, acme, sg)

	zipped := makeZip(t, []zipFile{fileEntry("zeros.bin", make([]byte, 4<<20))})
	s := buildZipSet(t, sg, declareCompressedSize(t, zipped, 0xFFFFFFFE))

	_, err := e.publish(t, acme, s)
	if !errors.Is(err, registry.ErrBadArchive) {
		t.Fatalf("publish with a lying header: got %v, want %v", err, registry.ErrBadArchive)
	}

	requireNoRelease(t, e)
}
