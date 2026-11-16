package service_test

import (
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
func hostileArchives() []archiveCase {
	zeros := make([]byte, 66<<20)

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
			name:  "total over limit",
			files: []zipFile{fileEntry("a.bin", zeros[:33<<20]), fileEntry("b.bin", zeros[:33<<20])},
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

	for _, tc := range hostileArchives() {
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

			requireNoRelease(t, e, "1.0.0")

			for _, left := range e.listing(t, e.ids[acme], "staging") {
				if !strings.HasSuffix(left, "/") {
					t.Fatalf("staged file left after refused publish: %s", left)
				}
			}
		})
	}
}
