package releaserecord_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
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
		"third_party_licenses/a\\b", "third_party_licenses/a\x00b", "third_party_licenses/\u00e9",
		"third_party_licenses/e\u0301", "third_party_licenses/a b", "third_party_licenses/a:b",
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

	// The NFC and the NFD form of é are one file on APFS: refuse both.
	for _, name := range []string{"third_party_licenses/\u00e9", "third_party_licenses/e\u0301"} {
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

// checkAbsoluteAlone sends a leading slash to the absolute-path check alone.
func checkAbsoluteAlone(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"/x", "/"} {
		if !archiveNameAbsolute(name) {
			t.Errorf("the absolute-path check accepted %q", name)
		}
	}

	for _, name := range []string{"x", "x/y", ""} {
		if archiveNameAbsolute(name) {
			t.Errorf("the absolute-path check refused %q", name)
		}
	}
}

// checkSeparatorByteAlone sends a backslash and a NUL byte to the byte check
// alone.
func checkSeparatorByteAlone(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"third_party_licenses/a\\b", "third_party_licenses/a\x00b", "\\", "\x00"} {
		if !archiveNameHasSeparatorByte(name) {
			t.Errorf("the byte check accepted %q", name)
		}
	}

	if archiveNameHasSeparatorByte("third_party_licenses/a") {
		t.Error("the byte check refused a plain name")
	}
}

// checkPartAlone sends bytes outside the set to the part check alone.
func checkPartAlone(t *testing.T) {
	t.Parallel()

	for _, part := range []string{"", ".", "..", "a b", "a:b", "a*", "\u00e9", "e\u0301", "a\\b", "a\x00b", "a\nb"} {
		if archivePartAllowed(part) {
			t.Errorf("the part check accepted %q", part)
		}
	}

	for _, part := range []string{"LICENSE", "README.md", "a_b~c+d-e.1", "third_party_licenses"} {
		if !archivePartAllowed(part) {
			t.Errorf("the part check refused %q", part)
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

	// A second entry of a name must not replace the first: only O_EXCL stands.
	t.Run("duplicate entry name", func(t *testing.T) {
		t.Parallel()

		_, root := targetRoot(t)
		defer closeRoot(t, root)

		hdr := &tar.Header{Name: "LICENSE", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1}
		if err := writeArchiveEntry(root, hdr, strings.NewReader("a")); err != nil {
			t.Fatal(err)
		}

		if err := writeArchiveEntry(root, hdr, strings.NewReader("b")); err == nil {
			t.Error("the root writer replaced a file")
		}
	})

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

// checkSeenNameAlone sends repeated names to the seen-name check alone.
func checkSeenNameAlone(t *testing.T) {
	t.Parallel()

	seen := make(map[string]struct{})

	if err := noteArchiveName(seen, "third_party_licenses/"); err != nil {
		t.Fatalf("the seen-name check refused a first name: %v", err)
	}

	for _, name := range []string{"third_party_licenses/", "third_party_licenses"} {
		if err := noteArchiveName(seen, name); err == nil {
			t.Errorf("the seen-name check accepted the repeated name %q", name)
		}
	}

	if err := noteArchiveName(seen, "LICENSE"); err != nil {
		t.Errorf("the seen-name check refused a new name: %v", err)
	}

	// Two spellings of one file must give one key: a doubled slash, a leading
	// dot part and a case fold. The two Unicode forms of é are two byte
	// strings, so only the allowlist refuses them: see checkAllowlistAlone.
	pairs := [][2]string{
		{"a/b", "a//b"}, {"a/b", "./a/b"}, {"Foo", "foo"},
	}

	for _, pair := range pairs {
		fresh := make(map[string]struct{})

		if err := noteArchiveName(fresh, pair[0]); err != nil {
			t.Fatalf("the seen-name check refused the first name %q: %v", pair[0], err)
		}

		if err := noteArchiveName(fresh, pair[1]); err == nil {
			t.Errorf("the seen-name check accepted %q after %q", pair[1], pair[0])
		}
	}
}

// tarOfEntries makes a gzip tar of regular files, one per name, each with the
// body "x".
func tarOfEntries(t *testing.T, names ...string) []byte {
	t.Helper()

	var raw bytes.Buffer

	zw := gzip.NewWriter(&raw)
	tw := tar.NewWriter(zw)

	for _, name := range names {
		hdr := &tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: 1}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}

		if _, err := tw.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	return raw.Bytes()
}

// tarOfDirs makes a gzip tar of directory entries, one per name.
func tarOfDirs(t *testing.T, names ...string) []byte {
	t.Helper()

	var raw bytes.Buffer

	zw := gzip.NewWriter(&raw)
	tw := tar.NewWriter(zw)

	for _, name := range names {
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
			t.Fatal(err)
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	return raw.Bytes()
}

// checkWholeStep sends each hostile entry to the whole unpack step.
func checkWholeStep(t *testing.T) {
	t.Parallel()

	headers := hostileHeaders()

	for _, name := range hostileNames() {
		headers = append(headers, tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: 1})
	}

	t.Run("duplicate entry name", func(t *testing.T) {
		t.Parallel()

		_, root := targetRoot(t)
		defer closeRoot(t, root)

		if err := unpackArchive(root, tarOfEntries(t, "LICENSE", "LICENSE")); err == nil {
			t.Error("the unpack step accepted a repeated name")
		}
	})

	// The directory is the same entry twice: only the seen-name check stands,
	// because the writer accepts a directory that exists.
	t.Run("duplicate directory entry", func(t *testing.T) {
		t.Parallel()

		_, root := targetRoot(t)
		defer closeRoot(t, root)

		data := tarOfDirs(t, "third_party_licenses/", "third_party_licenses/")
		if err := unpackArchive(root, data); err == nil {
			t.Error("the unpack step accepted a repeated directory entry")
		}
	})

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
	t.Run("absolute-path check alone", checkAbsoluteAlone)
	t.Run("separator byte check alone", checkSeparatorByteAlone)
	t.Run("part check alone", checkPartAlone)
	t.Run("seen-name check alone", checkSeenNameAlone)
	t.Run("root writer alone", checkRootWriterAlone)
	t.Run("whole step", checkWholeStep)
}

// archiveOfFiles makes a gzip tar of regular files below third_party_licenses,
// each with a body of size bytes.
func archiveOfFiles(t *testing.T, count, size int) []byte {
	t.Helper()

	var raw bytes.Buffer

	zw := gzip.NewWriter(&raw)
	tw := tar.NewWriter(zw)
	body := bytes.Repeat([]byte("x"), size)

	for i := range count {
		hdr := &tar.Header{
			Name: fmt.Sprintf("third_party_licenses/f%d", i), Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(size),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}

		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	return raw.Bytes()
}

// TestReleaseArchiveUnpackBoundsTheArchive sends an archive with more entries
// than the limit, and an archive with more bytes than the total limit, to the
// unpack step. Each must be refused, and the limit itself must pass.
func TestReleaseArchiveUnpackBoundsTheArchive(t *testing.T) {
	t.Parallel()

	t.Run("more entries than the limit", func(t *testing.T) {
		t.Parallel()

		_, root := targetRoot(t)
		defer closeRoot(t, root)

		if err := unpackArchive(root, archiveOfFiles(t, archiveFileLimit+1, 1)); err == nil {
			t.Errorf("the unpack step accepted %d entries", archiveFileLimit+1)
		}
	})

	t.Run("entries at the limit", func(t *testing.T) {
		t.Parallel()

		_, root := targetRoot(t)
		defer closeRoot(t, root)

		if err := unpackArchive(root, archiveOfFiles(t, archiveFileLimit, 1)); err != nil {
			t.Errorf("the unpack step refused %d entries: %v", archiveFileLimit, err)
		}
	})

	t.Run("more bytes than the total limit", func(t *testing.T) {
		t.Parallel()

		_, root := targetRoot(t)
		defer closeRoot(t, root)

		if err := unpackArchiveLimited(root, archiveOfFiles(t, 4, 100), 10, 399); err == nil {
			t.Error("the unpack step accepted 400 bytes with a total limit of 399")
		}
	})

	t.Run("bytes at the total limit", func(t *testing.T) {
		t.Parallel()

		_, root := targetRoot(t)
		defer closeRoot(t, root)

		if err := unpackArchiveLimited(root, archiveOfFiles(t, 4, 100), 10, 400); err != nil {
			t.Errorf("the unpack step refused 400 bytes with a total limit of 400: %v", err)
		}
	})

	t.Run("the total limit is bound by the entry limit", func(t *testing.T) {
		t.Parallel()

		if archiveTotalLimit < archiveEntryLimit {
			t.Errorf("the total limit %d is below the limit of one entry %d", archiveTotalLimit, archiveEntryLimit)
		}
	})
}
