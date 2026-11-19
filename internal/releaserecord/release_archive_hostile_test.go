package releaserecord_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hostileNames are names that the allowlist must refuse.
func hostileNames() []string {
	return []string{
		"/tmp/escape", "../escape", "third_party_licenses/../../escape", "./LICENSE",
		`third_party_licenses\..\escape`, "evil", "LICENSE/x", "opendustpan/x", "", "LICENSE\x00x",
	}
}

// hostileHeaders are entries with a name that the allowlist lets pass and a
// type or a size that only the writer refuses.
func hostileHeaders() []tar.Header {
	return []tar.Header{
		{Name: "opendustpan", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		{Name: "LICENSE", Typeflag: tar.TypeLink, Linkname: "/etc/passwd"},
		{Name: "third_party_licenses/x", Typeflag: tar.TypeSymlink, Linkname: "../../escape"},
		{Name: "third_party_licenses/dev", Typeflag: tar.TypeChar},
		{Name: "LICENSE", Typeflag: tar.TypeReg, Mode: 0o644, Size: archiveEntryLimit + 1},
	}
}

// tarOf makes a gzip tar of one entry. It answers false when the tar writer
// refuses the header, so that the case cannot be made.
func tarOf(t *testing.T, entry *tar.Header) ([]byte, bool) {
	t.Helper()

	var raw bytes.Buffer

	zw := gzip.NewWriter(&raw)
	tw := tar.NewWriter(zw)
	hdr := *entry
	hdr.Format = tar.FormatGNU

	if err := tw.WriteHeader(&hdr); err != nil {
		return nil, false
	}

	if hdr.Typeflag == tar.TypeReg && hdr.Size > 0 && hdr.Size < 1024 {
		if _, err := tw.Write(bytes.Repeat([]byte("x"), int(hdr.Size))); err != nil {
			t.Fatal(err)
		}
	}

	// The header is written already. The archive has no end blocks, as a cut
	// archive has none, and a header with a declared size needs no body here.
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	return raw.Bytes(), true
}

// targetRoot makes a parent directory with an empty directory target and opens
// the target as a root. It answers the parent and the root.
func targetRoot(t *testing.T) (string, *os.Root) {
	t.Helper()

	parent := t.TempDir()
	target := filepath.Join(parent, "target")

	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}

	return parent, openRootOf(t, target)
}

// assertNothingWritten checks that the parent holds only the target, and that
// the target holds no file.
func assertNothingWritten(t *testing.T, parent, label string) {
	t.Helper()

	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 1 || entries[0].Name() != "target" {
		t.Errorf("a file appeared outside the target for %s: %v (read error %v)", label, entries, err)
	}

	inside, err := os.ReadDir(filepath.Join(parent, "target"))
	if err != nil || len(inside) != 0 {
		t.Errorf("the target holds %v for %s (read error %v)", inside, label, err)
	}
}

// checkAllowlistAlone sends each hostile name to the allowlist alone.
func checkAllowlistAlone(t *testing.T) {
	t.Parallel()

	for _, name := range hostileNames() {
		if archiveEntryAllowed(name) {
			t.Errorf("the allowlist accepted %q", name)
		}
	}

	for _, good := range []string{"opendustpan", "LICENSE", "README.md", "third_party_licenses/", "third_party_licenses/go/LICENSE"} {
		if !archiveEntryAllowed(good) {
			t.Errorf("the allowlist refused %q", good)
		}
	}
}

// checkRootWriterAlone sends hostile entries to the writer, with no allowlist
// before it: only os.Root and the type check stand.
func checkRootWriterAlone(t *testing.T) {
	t.Parallel()

	headers := hostileHeaders()

	for _, name := range []string{"/tmp/escape", "../escape", "third_party_licenses/../../escape"} {
		headers = append(headers, tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: 1})
	}

	for i := range headers {
		hdr := &headers[i]
		parent, root := targetRoot(t)
		err := writeArchiveEntry(root, hdr, strings.NewReader("x"))

		closeRoot(t, root)

		if err == nil {
			t.Errorf("the root writer accepted %q of type %q", hdr.Name, hdr.Typeflag)
		}

		assertNothingWritten(t, parent, hdr.Name)
	}
}

// checkWholeStep sends each hostile entry to the whole unpack step.
func checkWholeStep(t *testing.T) {
	t.Parallel()

	headers := hostileHeaders()

	for _, name := range hostileNames() {
		headers = append(headers, tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: 1})
	}

	for i := range headers {
		hdr := &headers[i]
		data, made := tarOf(t, hdr)
		if !made {
			t.Logf("the case %q cannot be made with the tar writer", hdr.Name)

			continue
		}

		parent, root := targetRoot(t)
		err := unpackArchive(root, data)

		closeRoot(t, root)

		if err == nil {
			t.Errorf("the unpack step accepted %q of type %q", hdr.Name, hdr.Typeflag)
		}

		assertNothingWritten(t, parent, hdr.Name)
	}
}

// TestReleaseArchiveUnpackRefusesHostileEntries sends hostile entries to each
// layer of the unpack step on its own: the allowlist, the writer that uses
// os.Root, and the whole step. No file may appear outside the target.
func TestReleaseArchiveUnpackRefusesHostileEntries(t *testing.T) {
	t.Parallel()

	t.Run("allowlist alone", checkAllowlistAlone)
	t.Run("root writer alone", checkRootWriterAlone)
	t.Run("whole step", checkWholeStep)
}
