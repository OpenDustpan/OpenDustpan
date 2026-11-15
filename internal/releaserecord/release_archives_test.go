package releaserecord_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const archiveTestVersion = "v9.9.9"

func archivePlatforms() []string {
	return []string{"linux_amd64", "linux_arm64", "darwin_amd64", "darwin_arm64"}
}

// TestReleaseArchivesAreReproducible builds the release archives twice in a
// temporary copy of the work tree and checks the layout of each archive: the
// tar order, the owner, the mode and the time of each entry, and the gzip
// header. The two builds must give byte equal archives and SHA256SUMS. The
// copy gets a line in the release record for the test version.
func TestReleaseArchivesAreReproducible(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("the test builds four platforms twice")
	}

	tree := copyWorkTree(t)
	extendRecord(t, tree, archiveTestVersion)

	first := filepath.Join(t.TempDir(), "first")
	second := filepath.Join(t.TempDir(), "second")

	for _, outdir := range []string{first, second} {
		out, err := runInTree(t, tree, "build", archiveTestVersion, outdir)
		if err != nil {
			t.Fatalf("release-build.sh: %v\n%s", err, out)
		}
	}

	names := make([]string, 0, 1+len(archivePlatforms()))
	names = append(names, "SHA256SUMS")

	for _, platform := range archivePlatforms() {
		names = append(names, "opendustpan_"+archiveTestVersion+"_"+platform+".tar.gz")
	}

	entries, err := os.ReadDir(first)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != len(names) {
		t.Errorf("the output holds %d files, want %d: %v", len(entries), len(names), entries)
	}

	for _, name := range names {
		a := readIn(t, first, name)
		b := readIn(t, second, name)

		if !bytes.Equal(a, b) {
			t.Errorf("%s differs between two builds of one version", name)
		}

		if name != "SHA256SUMS" {
			checkArchiveLayout(t, name, a)
		}
	}

	// The good set passes the verify script.
	out, err := runInTree(t, tree, "verify", archiveTestVersion, first)
	if err != nil {
		t.Errorf("release-verify.sh of a good set: %v\n%s", err, out)
	}
}

// checkArchiveGzipHeader checks the header of the gzip stream: the magic
// bytes, no name, no time, and the flag of level 9.
func checkArchiveGzipHeader(t *testing.T, name string, data []byte) {
	t.Helper()

	if len(data) < 10 || data[0] != 0x1f || data[1] != 0x8b || data[3] != 0 ||
		!bytes.Equal(data[4:8], []byte{0, 0, 0, 0}) || data[8] != 2 {
		t.Errorf("%s: gzip header % x: want no name, mtime 0 and level 9 (gzip -n -9)",
			name, data[:min(10, len(data))])
	}
}

// checkArchiveLayout checks the gzip header and each tar entry of one archive.
func checkArchiveLayout(t *testing.T, name string, data []byte) {
	t.Helper()

	checkArchiveGzipHeader(t, name, data)

	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}

	tr := tar.NewReader(zr)
	children := map[string][]string{}
	seen := map[string]bool{}

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		entry := strings.TrimSuffix(hdr.Name, "/")
		parent := path.Dir(entry)

		if parent != "." && !seen[parent] {
			t.Errorf("%s: %s comes before its directory %s", name, entry, parent)
		}

		seen[entry] = true
		children[parent] = append(children[parent], path.Base(entry))

		checkArchiveEntry(t, name, entry, hdr)
	}

	for parent, list := range children {
		if !slices.IsSorted(list) {
			t.Errorf("%s: the entries of %s are not in name order: %v", name, parent, list)
		}
	}

	for _, want := range []string{"opendustpan", "LICENSE", "README.md", "third_party_licenses"} {
		if !seen[want] {
			t.Errorf("%s: no entry %s", name, want)
		}
	}
}

// checkArchiveEntry checks the owner, the time and the mode of one entry.
func checkArchiveEntry(t *testing.T, name, entry string, hdr *tar.Header) {
	t.Helper()

	if hdr.Uid != 0 || hdr.Gid != 0 || hdr.Uname != "" || hdr.Gname != "" {
		t.Errorf("%s: %s has owner %d:%d %q:%q, want 0:0 and no names",
			name, entry, hdr.Uid, hdr.Gid, hdr.Uname, hdr.Gname)
	}

	if hdr.ModTime.Unix() != 0 {
		t.Errorf("%s: %s has mtime %v, want the epoch", name, entry, hdr.ModTime)
	}

	want := int64(0o644)
	if hdr.Typeflag == tar.TypeDir || entry == "opendustpan" {
		want = 0o755
	}

	if hdr.Mode&0o777 != want {
		t.Errorf("%s: %s has mode %o, want %o", name, entry, hdr.Mode&0o777, want)
	}
}

// TestReleaseBuildRefusesABadVersion sends a version outside the allowlist to
// the build script. The script refuses it and writes no file.
func TestReleaseBuildRefusesABadVersion(t *testing.T) {
	t.Parallel()

	versions := []string{"", "0.1.0", "v0.1", "v0.1.0-rc1", "v01.2.3", "v0.1.0\nv0.2.0", "../v0.1.0", "v0.1.0 "}

	for _, version := range versions {
		t.Run(strings.ReplaceAll(version, "\n", "_"), func(t *testing.T) {
			t.Parallel()

			outdir := filepath.Join(t.TempDir(), "out")

			// The script gets the values from the environment, so that the
			// command line holds no variable.
			cmd := exec.CommandContext(
				t.Context(), "bash", "-c", `exec bash ../../scripts/release-build.sh "$RELEASE_VERSION" "$RELEASE_DIR"`,
			)

			out, err := run(cmd, ".", "RELEASE_VERSION="+version, "RELEASE_DIR="+outdir)
			if err == nil {
				t.Fatalf("the script accepted version %q", version)
			}

			if !strings.Contains(out, "bad version") || !strings.Contains(out, "make release-build VERSION=v0.1.0") {
				t.Errorf("the script said %q, want a bad version error with the command that helps", out)
			}

			if _, err := os.Stat(outdir); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("the script made %s (stat: %v), want no file", outdir, err)
			}
		})
	}
}

// copyWorkTree copies the files of the work tree that the build needs to a
// temporary directory with its own Git repository, and returns it.
func copyWorkTree(t *testing.T) string {
	t.Helper()

	dst := t.TempDir()

	if out, err := run(exec.CommandContext(t.Context(), "git", "init", "--quiet"), dst); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	list := exec.CommandContext(t.Context(), "git", "ls-files", "-co", "--exclude-standard", "-z")
	list.Dir = "../.."

	out, err := list.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}

	srcRoot := openRootOf(t, "../..")
	defer closeRoot(t, srcRoot)

	dstRoot := openRootOf(t, dst)
	defer closeRoot(t, dstRoot)

	for name := range strings.SplitSeq(string(out), "\x00") {
		if needsForBuild(name) {
			copyFile(t, srcRoot, dstRoot, name)
		}
	}

	return dst
}

// copyFile copies one regular file with its permission bits. It skips a file
// that is gone from the work tree and a link.
func copyFile(t *testing.T, src, dst *os.Root, name string) {
	t.Helper()

	info, err := src.Lstat(name)
	if err != nil || !info.Mode().IsRegular() {
		return
	}

	data, err := src.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}

	if err := dst.MkdirAll(path.Dir(name), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := dst.WriteFile(name, data, info.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
}

// needsForBuild reports whether the release build reads the file.
func needsForBuild(name string) bool {
	if name == "" {
		return false
	}

	if !strings.Contains(name, "/") {
		return true
	}

	for _, prefix := range []string{"scripts/", "cmd/", "internal/", "pkg/"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}

	return false
}

// extendRecord adds the lines of version to the release record of the tree
// at dir, from the migration files of the tree.
func extendRecord(t *testing.T, dir, version string) {
	t.Helper()

	root := openRootOf(t, dir)
	defer closeRoot(t, root)

	record, err := root.ReadFile("internal/releaserecord/shipped.txt")
	if err != nil {
		t.Fatal(err)
	}

	var lines strings.Builder

	lines.Write(record)

	for _, dialect := range []string{"sqlite", "postgres"} {
		files, err := fs.Glob(root.FS(), "pkg/store/"+dialect+"/migrations/[0-9]*.sql")
		if err != nil {
			t.Fatal(err)
		}

		for _, file := range files {
			data, err := root.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}

			sum := sha256.Sum256(data)
			lines.WriteString(version + " " + dialect + " " + path.Base(file) + " " + hex.EncodeToString(sum[:]) + "\n")
		}
	}

	if err := root.WriteFile("internal/releaserecord/shipped.txt", []byte(lines.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

// runInTree runs scripts/release-<step>.sh in dir with the Go caches of the
// test process. The build of the other platforms may need modules that the
// cache of the test process does not hold, so the Go tool may download them.
func runInTree(t *testing.T, dir, step, version, outdir string) (string, error) {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "go", "env", "GOCACHE", "GOMODCACHE").Output()
	if err != nil {
		t.Fatalf("go env: %v", err)
	}

	goEnv := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(goEnv) != 2 {
		t.Fatalf("go env gave %q, want two lines", out)
	}

	env := []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "GOTOOLCHAIN=local",
		"GOCACHE=" + goEnv[0], "GOMODCACHE=" + goEnv[1],
		"RELEASE_STEP=" + step, "RELEASE_VERSION=" + version, "RELEASE_DIR=" + outdir,
	}

	// The step, the version and the directory go in the environment, so that
	// the command line holds no variable.
	cmd := exec.CommandContext(t.Context(), "bash", "-c",
		`exec bash "scripts/release-$RELEASE_STEP.sh" "$RELEASE_VERSION" "$RELEASE_DIR"`)
	cmd.Dir = dir
	cmd.Env = env

	combined, err := cmd.CombinedOutput()

	return string(combined), err
}
