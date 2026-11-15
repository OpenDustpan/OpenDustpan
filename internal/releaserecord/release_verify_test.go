package releaserecord_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const verifyVersion = "v1.2.3"

// verifyArchive is the name of the archive that the cases change.
const verifyArchive = "opendustpan_" + verifyVersion + "_linux_amd64.tar.gz"

// archiveEntry is one entry of a test archive.
type archiveEntry struct {
	name     string
	body     string
	mode     int64
	typeflag byte
	link     string
}

func fileEntry(name, body string, mode int64) archiveEntry {
	return archiveEntry{name: name, body: body, mode: mode, typeflag: tar.TypeReg, link: ""}
}

func dirEntry(name string) archiveEntry {
	return archiveEntry{name: name, body: "", mode: 0o755, typeflag: tar.TypeDir, link: ""}
}

func linkEntry(name, target string) archiveEntry {
	return archiveEntry{name: name, body: "", mode: 0o777, typeflag: tar.TypeSymlink, link: target}
}

// goodEntries returns the entries of a good archive. The binary is a script
// that writes the file "ran" when it runs, so that a verify that runs the
// binary leaves a trace. It holds the stamp as text.
func goodEntries(stamp string) []archiveEntry {
	binary := "#!/bin/sh\ntouch ran\n# main.version=" + stamp + "\"\n"

	return []archiveEntry{
		fileEntry("opendustpan", binary, 0o755),
		fileEntry("LICENSE", "license", 0o644),
		fileEntry("README.md", "readme", 0o644),
		dirEntry("third_party_licenses"),
		fileEntry("third_party_licenses/example.com/mod/LICENSE", "mit", 0o644),
	}
}

// plus returns the good entries and the extra entry.
func plus(extra archiveEntry) []archiveEntry {
	return append(goodEntries(verifyVersion), extra)
}

func without(entries []archiveEntry, name string) []archiveEntry {
	out := make([]archiveEntry, 0, len(entries))

	for _, e := range entries {
		if e.name != name {
			out = append(out, e)
		}
	}

	return out
}

func packArchive(t *testing.T, entries []archiveEntry) []byte {
	t.Helper()

	var buf bytes.Buffer

	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)

	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: e.mode, Typeflag: e.typeflag, Linkname: e.link}
		if e.typeflag == tar.TypeReg {
			hdr.Size = int64(len(e.body))
		}

		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}

		if e.typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

// writeSet writes four archives and a SHA256SUMS file in a new directory. The
// linux/amd64 archive holds the entries that the case gives, or the good
// entries when it gives none.
func writeSet(t *testing.T, entries []archiveEntry) string {
	t.Helper()

	dir := t.TempDir()

	var sums strings.Builder

	for _, platform := range archivePlatforms() {
		name := "opendustpan_" + verifyVersion + "_" + platform + ".tar.gz"

		list := goodEntries(verifyVersion)
		if platform == "linux_amd64" && entries != nil {
			list = entries
		}

		data := packArchive(t, list)
		sum := sha256.Sum256(data)

		writeIn(t, dir, name, data)

		sums.WriteString(hex.EncodeToString(sum[:]) + "  " + name + "\n")
	}

	writeIn(t, dir, "SHA256SUMS", []byte(sums.String()))

	return dir
}

// verifyCase is one set that the script must accept or refuse.
type verifyCase struct {
	name    string
	version string
	entries []archiveEntry                 // the entries of the linux/amd64 archive; nil for the good ones
	edit    func(t *testing.T, dir string) // a change of the files after the set is written
	want    string                         // the text of the refusal; empty when the set passes
}

func removeFile(name string) func(t *testing.T, dir string) {
	return func(t *testing.T, dir string) {
		t.Helper()

		root := openRootOf(t, dir)
		defer closeRoot(t, root)

		if err := root.Remove(name); err != nil {
			t.Fatal(err)
		}
	}
}

func addFile(name string) func(t *testing.T, dir string) {
	return func(t *testing.T, dir string) {
		t.Helper()

		writeIn(t, dir, name, []byte("x"))
	}
}

func appendToFile(name string) func(t *testing.T, dir string) {
	return func(t *testing.T, dir string) {
		t.Helper()

		writeIn(t, dir, name, append(readIn(t, dir, name), 'x'))
	}
}

// editSums changes the lines of SHA256SUMS.
func editSums(change func(lines []string) []string) func(t *testing.T, dir string) {
	return func(t *testing.T, dir string) {
		t.Helper()

		lines := strings.Split(strings.TrimSuffix(string(readIn(t, dir, "SHA256SUMS")), "\n"), "\n")

		writeIn(t, dir, "SHA256SUMS", []byte(strings.Join(change(lines), "\n")+"\n"))
	}
}

// replaceWithLink moves a file out of the set and puts a symbolic link of the
// same name in its place. The link points to the moved file, so the bytes are
// right and only a check for links can refuse the set.
func replaceWithLink(name string) func(t *testing.T, dir string) {
	return func(t *testing.T, dir string) {
		t.Helper()

		target := filepath.Join(t.TempDir(), name)
		if err := os.Rename(filepath.Join(dir, name), target); err != nil {
			t.Fatal(err)
		}

		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
}

func verifyCases() []verifyCase {
	good := goodEntries(verifyVersion)
	zeros := strings.Repeat("0", 64)

	return []verifyCase{
		{"a good set", verifyVersion, nil, nil, ""},
		{"a version with no patch number", "v1.2", nil, nil, "bad version"},
		{"a version with a suffix", "v1.2.3-rc1", nil, nil, "bad version"},
		{"a version with a newline", "v1.2.3\nv1.2.4", nil, nil, "bad version"},
		{"an empty version", "", nil, nil, "bad version"},
		{"a missing archive", verifyVersion, nil, removeFile(verifyArchive), "missing file: " + verifyArchive},
		{"a missing SHA256SUMS", verifyVersion, nil, removeFile("SHA256SUMS"), "missing file: SHA256SUMS"},
		{"a file that is not in the set", verifyVersion, nil, addFile("extra.txt"), "unexpected file: extra.txt"},
		{
			"a SHA256SUMS with a missing entry", verifyVersion, nil,
			editSums(func(lines []string) []string { return lines[1:] }),
			"SHA256SUMS has no entry",
		},
		{
			"a SHA256SUMS with an extra entry", verifyVersion, nil,
			editSums(func(lines []string) []string { return append(lines, zeros+"  other.tar.gz") }),
			"SHA256SUMS has an unexpected entry",
		},
		{
			"a SHA256SUMS with a path entry", verifyVersion, nil,
			editSums(func(lines []string) []string { return append(lines, zeros+"  ../"+verifyArchive) }),
			"SHA256SUMS has an unexpected entry",
		},
		{"a changed archive", verifyVersion, nil, appendToFile(verifyArchive), "checksum mismatch: " + verifyArchive},
		{
			"a binary with another stamp", verifyVersion, goodEntries("v1.2.4"), nil,
			"stamp mismatch: " + verifyArchive,
		},
		{
			"a binary with a longer stamp", verifyVersion, goodEntries("v1.2.30"), nil,
			"stamp mismatch: " + verifyArchive,
		},
		{
			"an archive with no LICENSE", verifyVersion, without(good, "LICENSE"), nil,
			"archive content: " + verifyArchive + " has no LICENSE",
		},
		{
			"an archive with no third party license file", verifyVersion,
			without(good, "third_party_licenses/example.com/mod/LICENSE"), nil,
			"archive content: " + verifyArchive + " has no file in third_party_licenses",
		},
		{
			"an entry with a parent directory part", verifyVersion,
			plus(fileEntry("third_party_licenses/../../evil", "x", 0o644)), nil,
			"archive content: " + verifyArchive,
		},
		{
			"an entry that starts with the parent directory", verifyVersion,
			plus(fileEntry("../evil", "x", 0o644)), nil,
			"archive content: " + verifyArchive,
		},
		{
			"an absolute entry", verifyVersion,
			plus(fileEntry("/etc/evil", "x", 0o644)), nil,
			"archive content: " + verifyArchive,
		},
		{
			"a file outside the allowlist", verifyVersion,
			plus(fileEntry("install.sh", "x", 0o755)), nil,
			"archive content: " + verifyArchive,
		},
		{
			"a symbolic link", verifyVersion,
			plus(linkEntry("third_party_licenses/link", "/etc/passwd")), nil,
			"archive content: " + verifyArchive,
		},
		{
			"a name with a newline", verifyVersion,
			plus(fileEntry("third_party_licenses/a\nLICENSE", "x", 0o644)), nil,
			"archive content: " + verifyArchive,
		},
		{
			"a repeated entry", verifyVersion,
			plus(fileEntry("opendustpan", "x", 0o755)), nil,
			"archive content: " + verifyArchive,
		},
		{
			"an archive that is a symbolic link", verifyVersion, nil,
			replaceWithLink(verifyArchive), "missing file: " + verifyArchive,
		},
		{
			"a SHA256SUMS that is a symbolic link", verifyVersion, nil,
			replaceWithLink("SHA256SUMS"), "missing file: SHA256SUMS",
		},
		{
			"a SHA256SUMS with a repeated entry", verifyVersion, nil,
			editSums(func(lines []string) []string { return append(lines, lines[0]) }),
			"SHA256SUMS has an unexpected entry",
		},
		{
			"a directory named LICENSE", verifyVersion,
			append(without(good, "LICENSE"), dirEntry("LICENSE")), nil,
			"archive content: " + verifyArchive,
		},
		{
			"a directory named README.md", verifyVersion,
			append(without(good, "README.md"), dirEntry("README.md")), nil,
			"archive content: " + verifyArchive,
		},
		{
			"a directory named opendustpan", verifyVersion,
			append(without(good, "opendustpan"), dirEntry("opendustpan")), nil,
			"archive content: " + verifyArchive,
		},
	}
}

// TestReleaseVerifyRefusesBadInput sends each hostile or corrupt set directly
// to scripts/release-verify.sh.
func TestReleaseVerifyRefusesBadInput(t *testing.T) {
	t.Parallel()

	for _, tt := range verifyCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := writeSet(t, tt.entries)
			if tt.edit != nil {
				tt.edit(t, dir)
			}

			out, err := runVerify(t, tt.version, dir)
			checkVerifyResult(t, tt, out, err)

			// The script never runs the binary of an archive.
			if _, err := os.Stat(filepath.Join(dir, "ran")); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("the binary ran (stat: %v)", err)
			}
		})
	}
}

// runVerify runs the script. The script gets the version and the directory
// from the environment, so that the command line holds no variable.
func runVerify(t *testing.T, version, dir string) (string, error) {
	t.Helper()

	cmd := exec.CommandContext(
		t.Context(), "bash", "-c", `exec bash ../../scripts/release-verify.sh "$RELEASE_VERSION" "$RELEASE_DIR"`,
	)

	return run(cmd, ".", "RELEASE_VERSION="+version, "RELEASE_DIR="+dir)
}

func checkVerifyResult(t *testing.T, tt verifyCase, out string, err error) {
	t.Helper()

	if tt.want == "" {
		if err != nil {
			t.Fatalf("release-verify.sh of a good set: %v\n%s", err, out)
		}

		return
	}

	if err == nil {
		t.Fatalf("release-verify.sh accepted the set:\n%s", out)
	}

	if !strings.Contains(out, tt.want) {
		t.Errorf("release-verify.sh said:\n%s\nwant %q", out, tt.want)
	}

	if !strings.Contains(out, "make release-verify VERSION=") {
		t.Errorf("release-verify.sh said:\n%s\nwant the command that helps", out)
	}
}

func openRootOf(t *testing.T, dir string) *os.Root {
	t.Helper()

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}

	return root
}

func writeIn(t *testing.T, dir, name string, data []byte) {
	t.Helper()

	root := openRootOf(t, dir)
	defer closeRoot(t, root)

	if err := root.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readIn(t *testing.T, dir, name string) []byte {
	t.Helper()

	root := openRootOf(t, dir)
	defer closeRoot(t, root)

	data, err := root.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}

	return data
}
