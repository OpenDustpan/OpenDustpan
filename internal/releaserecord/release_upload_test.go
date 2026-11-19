package releaserecord_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
)

const (
	uploadRepo     = "owner/name"
	uploadMarker   = "ghs_TOKEN_MARKER_0123456789"
	uploadSuccess  = "release-upload: v1.2.3: draft release created with four archives and SHA256SUMS; review it, then publish it with: gh release edit v1.2.3 --draft=false\n"
	uploadFakeGH   = "#!/usr/bin/env bash\n" + fakeGHBody
	uploadAlwaysOK = "#!/usr/bin/env bash\nexit 0\n"
)

// fakeGHBody is a test fake of the gh command. It records each call, one
// record for each call, with a NUL after each argument and a record separator
// at the end. It answers gh api from api.out and api.code, and gh release
// create from create.code.
const fakeGHBody = `d="$FAKE_GH_DIR"
{ printf '%s\0' "$@"; printf '\036'; } >>"$d/calls"
code() { if [[ -f "$d/$1.code" ]]; then cat "$d/$1.code"; else echo 0; fi; }
case "$1" in
api)
  [[ -f "$d/api.out" ]] && cat "$d/api.out"
  exit "$(code api)"
  ;;
release)
  if [[ "$2" == create ]]; then
    pwd -P >"$d/create.cwd"
    exit "$(code create)"
  fi
  ;;
esac
echo "fake gh: unexpected call" >&2
exit 99
`

// uploadRun describes one run of scripts/release-upload.sh.
type uploadRun struct {
	version       string
	repo          *string // nil leaves GH_REPO unset
	apiOut        string
	apiCode       string
	createCode    string
	set           string // the directory of the set; empty makes a good set
	edit          func(t *testing.T, dir string)
	replaceVerify bool   // replace scripts/release-verify.sh by a script that exits 0
	hostile       string // how the directory argument is made hostile: cdpath, dash, newline; empty for none
}

// uploadResult is what one run of the script did.
type uploadResult struct {
	stdout string
	stderr string
	code   int
	calls  [][]string
	cwd    string
	dir    string
}

// skipUpload skips the test where the script cannot run.
func skipUpload(t *testing.T) {
	t.Helper()

	if testing.Short() {
		t.Skip("the test runs the upload script")
	}

	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("the release scripts run on Linux and macOS only")
	}
}

// copyScript copies one script of the work tree into tree/scripts.
func copyScript(t *testing.T, tree, name string) {
	t.Helper()

	src := openRootOf(t, "../..")
	defer closeRoot(t, src)

	data, err := src.ReadFile("scripts/" + name)
	if err != nil {
		t.Fatal(err)
	}

	writeIn(t, tree, "scripts/"+name, data)
}

// newFakeGH writes the fake gh, with the answers of r, and returns the
// directory of the fake and its bin directory.
// fakeDirs holds the directory of the fake gh and its bin directory.
type fakeDirs struct {
	root string
	bin  string
}

func newFakeGH(t *testing.T, r uploadRun) fakeDirs {
	t.Helper()

	fake := t.TempDir()
	bin := filepath.Join(fake, "bin")

	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}

	// The fake must be executable.
	binRoot := openRootOf(t, bin)
	defer closeRoot(t, binRoot)

	if err := binRoot.WriteFile("gh", []byte(uploadFakeGH), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := binRoot.Chmod("gh", 0o500); err != nil {
		t.Fatal(err)
	}

	writeIn(t, fake, "api.out", []byte(r.apiOut))

	if r.apiCode != "" {
		writeIn(t, fake, "api.code", []byte(r.apiCode))
	}

	if r.createCode != "" {
		writeIn(t, fake, "create.code", []byte(r.createCode))
	}

	return fakeDirs{root: fake, bin: bin}
}

// runUpload runs the upload script of a small copy of the work tree with a
// fake gh first in PATH.
func runUpload(t *testing.T, r uploadRun) uploadResult {
	t.Helper()

	tree := t.TempDir()
	if err := os.Mkdir(filepath.Join(tree, "scripts"), 0o700); err != nil {
		t.Fatal(err)
	}

	copyScript(t, tree, "release-upload.sh")
	copyScript(t, tree, "release-verify.sh")

	if r.replaceVerify {
		writeIn(t, tree, "scripts/release-verify.sh", []byte(uploadAlwaysOK))
	}

	dirs := newFakeGH(t, r)
	fake, bin := dirs.root, dirs.bin

	dir := r.set
	if dir == "" {
		dir = writeSet(t, nil)
	}

	if r.edit != nil {
		r.edit(t, dir)
	}

	dirArg, hostileEnv := hostileDir(t, r.hostile, tree, dir)

	env := []string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + fake,
		"GH_TOKEN=" + uploadMarker,
		"FAKE_GH_DIR=" + fake,
	}
	if r.repo != nil {
		env = append(env, "GH_REPO="+*r.repo)
	}

	env = append(env, hostileEnv...)

	var stdout, stderr bytes.Buffer

	// The script of the work tree runs on purpose. The version and the
	// directory come from the environment (TEST_RELEASE_VERSION, TEST_RELEASE_DIR), so
	// that the command line holds no variable; each is one quoted argument.
	cmd := exec.CommandContext(
		t.Context(), "bash", "-c", `exec bash scripts/release-upload.sh "$TEST_RELEASE_VERSION" "$TEST_RELEASE_DIR"`,
	)
	cmd.Dir = tree
	env = append(env, "TEST_RELEASE_VERSION="+r.version, "TEST_RELEASE_DIR="+dirArg)
	cmd.Env = env
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	res := uploadResult{stdout: "", stderr: "", code: 0, calls: nil, cwd: "", dir: dir}

	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatal(err)
		}

		res.code = exit.ExitCode()
	}

	res.stdout, res.stderr, res.calls = stdout.String(), stderr.String(), readCalls(t, fake)

	fakeRoot := openRootOf(t, fake)
	defer closeRoot(t, fakeRoot)

	if raw, err := fakeRoot.ReadFile("create.cwd"); err == nil {
		res.cwd = strings.TrimSpace(string(raw))
	}

	return res
}

// otherSet writes a second good set whose bytes differ from the set of
// writeSet(t, nil).
func otherSet(t *testing.T) string {
	t.Helper()

	entries := goodEntries(verifyVersion)
	entries[2] = fileEntry("README.md", "another readme", 0o644)

	return writeSet(t, entries)
}

// hostileDir returns the directory argument for the script and the extra
// environment. The argument names dir only through a path that a hostile
// environment can send to another set: "cdpath" gives a relative name and a
// CDPATH that holds another set of that name; "dash" gives "-" and an OLDPWD
// that is another set; "newline" gives a missing directory whose name forges a
// log line.
func hostileDir(t *testing.T, kind, tree, dir string) (string, []string) {
	t.Helper()

	switch kind {
	case "cdpath":
		if err := os.Symlink(dir, filepath.Join(tree, "set")); err != nil {
			t.Fatal(err)
		}

		cdpath := t.TempDir()
		if err := os.Symlink(otherSet(t), filepath.Join(cdpath, "set")); err != nil {
			t.Fatal(err)
		}

		return "set", []string{"CDPATH=" + cdpath}
	case "dash":
		if err := os.Symlink(dir, filepath.Join(tree, "-")); err != nil {
			t.Fatal(err)
		}

		return "-", []string{"OLDPWD=" + otherSet(t)}
	case "newline":
		return "missing\nrelease-upload: forged line", nil
	}

	return dir, nil
}

// readCalls returns the recorded calls of the fake gh.
func readCalls(t *testing.T, fake string) [][]string {
	t.Helper()

	fakeRoot := openRootOf(t, fake)
	defer closeRoot(t, fakeRoot)

	raw, err := fakeRoot.ReadFile("calls")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		t.Fatal(err)
	}

	var calls [][]string

	for rec := range strings.SplitSeq(string(raw), "\x1e") {
		if rec == "" {
			continue
		}

		args := strings.Split(rec, "\x00")
		calls = append(calls, args[:len(args)-1])
	}

	return calls
}

// createCalls returns the recorded gh release create calls.
func createCalls(calls [][]string) [][]string {
	var out [][]string

	for _, c := range calls {
		if len(c) >= 2 && c[0] == "release" && c[1] == "create" {
			out = append(out, c)
		}
	}

	return out
}

func goodRun() uploadRun {
	return uploadRun{
		version: verifyVersion, repo: new(uploadRepo), apiOut: "", apiCode: "", createCode: "", set: "", edit: nil,
		replaceVerify: false, hostile: "",
	}
}

func archiveName(platform string) string {
	return "opendustpan_" + verifyVersion + "_" + platform + ".tar.gz"
}

// TestReleaseUploadCreatesOneDraft checks the one gh call that makes a release
// and the line that the script prints.
func TestReleaseUploadCreatesOneDraft(t *testing.T) {
	t.Parallel()
	skipUpload(t)

	res := runUpload(t, goodRun())

	if res.code != 0 {
		t.Fatalf("exit code %d, stderr %q", res.code, res.stderr)
	}

	if res.stdout != uploadSuccess {
		t.Errorf("stdout %q, want %q", res.stdout, uploadSuccess)
	}

	if res.stderr != "" {
		t.Errorf("stderr %q, want empty", res.stderr)
	}

	creates := createCalls(res.calls)
	if len(creates) != 1 {
		t.Fatalf("%d release create calls, want 1: %q", len(creates), res.calls)
	}

	checkCreateArgs(t, creates[0])
	checkNoPublishCall(t, res.calls)

	if got, want := evalDir(t, res.cwd), evalDir(t, res.dir); got != want {
		t.Errorf("create ran in %s, want the set directory %s", got, want)
	}
}

// checkCreateArgs checks the flags, the title and the notes of the create call.
func checkCreateArgs(t *testing.T, args []string) {
	t.Helper()

	for _, want := range []string{"--draft", "--verify-tag"} {
		if !slices.Contains(args, want) {
			t.Errorf("create call lacks %s: %q", want, args)
		}
	}

	if args[2] != verifyVersion {
		t.Errorf("tag argument %q, want %q", args[2], verifyVersion)
	}

	title := slices.Index(args, "--title")
	if title < 0 || args[title+1] != verifyVersion {
		t.Errorf("create call has no --title %s: %q", verifyVersion, args)
	}

	notes := slices.Index(args, "--notes")
	if notes < 0 || !strings.Contains(args[notes+1], "SHA256SUMS is not a signature.") {
		t.Errorf("create call has no notes with the signature sentence: %q", args)
	}
}

// checkNoPublishCall checks that no call publishes, overwrites or edits.
func checkNoPublishCall(t *testing.T, calls [][]string) {
	t.Helper()

	for _, call := range calls {
		for _, deny := range []string{"--latest", "--clobber", "--draft=false", "upload", "edit", "publish"} {
			if slices.Contains(call, deny) {
				t.Errorf("a call holds %s: %q", deny, call)
			}
		}
	}
}

func evalDir(t *testing.T, dir string) string {
	t.Helper()

	out, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}

	return out
}

// TestReleaseUploadFilesAreTheVerifiedNames compares the file names of the
// create call with the file set that scripts/release-verify.sh accepts.
func TestReleaseUploadFilesAreTheVerifiedNames(t *testing.T) {
	t.Parallel()
	skipUpload(t)

	res := runUpload(t, goodRun())

	creates := createCalls(res.calls)
	if len(creates) != 1 {
		t.Fatalf("%d release create calls, want 1", len(creates))
	}

	args := creates[0]

	sep := slices.Index(args, "--")
	if sep < 0 {
		t.Fatalf("create call has no -- before the file names: %q", args)
	}

	got := slices.Clone(args[sep+1:])
	slices.Sort(got)

	entries, err := os.ReadDir(res.dir)
	if err != nil {
		t.Fatal(err)
	}

	want := make([]string, 0, len(entries))
	for _, e := range entries {
		want = append(want, e.Name())
	}

	slices.Sort(want)

	if !slices.Equal(got, want) {
		t.Errorf("uploaded names %q, want the names that the verify script accepts %q", got, want)
	}

	if len(got) != 5 {
		t.Errorf("%d names, want 5", len(got))
	}

	for _, name := range got {
		if strings.ContainsAny(name, "*?[") {
			t.Errorf("name %q is a glob", name)
		}
	}
}

// TestReleaseUploadRefusesABadVersion sends hostile versions to the script.
// A refusal makes no gh call, prints one line and does not echo the value.
func TestReleaseUploadRefusesABadVersion(t *testing.T) {
	t.Parallel()
	skipUpload(t)

	versions := []string{
		"v1$(id)", "v1.2.3`id`", "v1.2.3;id", "v1.2.3 ", "v1.2.3\nv1.2.4", "v1.2.3\n", "-x", "v1.2", "v01.2.3",
		"v1.2.3-rc1", "v1.2." + strings.Repeat("1", 60), "", "V1.2.3", "1.2.3", "v1.2.3.4",
	}

	// The second run replaces the verify script, so that the allowlist of
	// the upload script stops the version alone.
	for _, version := range versions {
		for _, replaced := range []bool{false, true} {
			testBadVersion(t, version, replaced)
		}
	}
}

func testBadVersion(t *testing.T, version string, replaced bool) {
	t.Helper()

	t.Run(fmt.Sprintf("version %q replaced %v", version[:min(len(version), 12)], replaced), func(t *testing.T) {
		t.Parallel()

		r := goodRun()
		r.version = version
		r.replaceVerify = replaced
		res := runUpload(t, r)

		checkRefusal(t, res, 0, "bad version", "three dot separated numbers")

		if version != "" && strings.Contains(res.stderr, strings.TrimSpace(version)) {
			t.Errorf("stderr echoes the version: %q", res.stderr)
		}
	})
}

// checkRefusal checks a refusal: non-zero exit, the given number of gh calls and no create call, no stdout and one
// line on stderr that holds the texts.
func checkRefusal(t *testing.T, res uploadResult, apiCalls int, texts ...string) {
	t.Helper()

	if res.code == 0 {
		t.Error("the script exited 0, want a refusal")
	}

	if len(res.calls) != apiCalls {
		t.Errorf("a refusal made %d gh calls, want %d: %q", len(res.calls), apiCalls, res.calls)
	}

	if len(createCalls(res.calls)) != 0 {
		t.Errorf("a refusal made a create call: %q", res.calls)
	}

	if res.stdout != "" {
		t.Errorf("stdout %q, want empty", res.stdout)
	}

	if !strings.HasPrefix(res.stderr, "release-upload: ") || strings.Count(res.stderr, "\n") != 1 {
		t.Errorf("stderr %q, want one line that starts with release-upload:", res.stderr)
	}

	for _, text := range texts {
		if !strings.Contains(res.stderr, text) {
			t.Errorf("stderr %q lacks %q", res.stderr, text)
		}
	}
}

// TestReleaseUploadRefusesABadRepo sends a hostile GH_REPO to the script.
func TestReleaseUploadRefusesABadRepo(t *testing.T) {
	t.Parallel()
	skipUpload(t)

	tests := []struct {
		name string
		repo *string
	}{
		{"unset", nil},
		{"empty", new("")},
		{"space", new("owner/na me")},
		{"second slash", new("owner/name/extra")},
		{"command", new("$(id)/name")},
		{"command in name", new("owner/$(id)")},
		{"newline", new("owner/name\nx/y")},
		{"no owner", new("/name")},
		{"no name", new("owner/")},
		{"no slash", new("owner")},
		{"flag", new("--repo=x/y")},
		{"parent segments", new("../..")},
		{"dot segments", new("./.")},
		{"dot owner", new("../name")},
		{"dot name", new("owner/..")},
	}

	for _, tt := range tests {
		for _, replaced := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s replaced %v", tt.name, replaced), func(t *testing.T) {
				t.Parallel()

				r := goodRun()
				r.repo = tt.repo
				r.replaceVerify = replaced
				res := runUpload(t, r)

				checkRefusal(t, res, 0, "GH_REPO", "set GH_REPO to owner/name")
			})
		}
	}
}

// TestReleaseUploadRefusesAChangedSet changes the set in each way that the
// checks must find. Each case runs twice: with the real verify script, and with
// a verify script that always passes, so that the checks of the upload script
// itself stop the case alone.
func TestReleaseUploadRefusesAChangedSet(t *testing.T) {
	t.Parallel()
	skipUpload(t)

	flip := func(t *testing.T, dir string) {
		t.Helper()

		data := readIn(t, dir, archiveName("linux_arm64"))
		data[len(data)/2] ^= 0xff
		writeIn(t, dir, archiveName("linux_arm64"), data)
	}

	tests := []struct {
		name string
		edit func(t *testing.T, dir string)
	}{
		{"flipped byte", flip},
		{"line for the wrong file", editSums(func(l []string) []string {
			l[1] = strings.Replace(l[1], archiveName("linux_arm64"), "other.tar.gz", 1)

			return l
		})},
		{"missing line", editSums(func(l []string) []string { return l[1:] })},
		{"repeated line", editSums(func(l []string) []string { return append(l, l[0]) })},
		{"extra file", addFile("extra.txt")},
		{"extra hidden file", addFile(".hidden")},
		{"link in place of an archive", replaceWithLink(archiveName("darwin_amd64"))},
		{"link in place of SHA256SUMS", replaceWithLink("SHA256SUMS")},
		{"missing archive", removeFile(archiveName("darwin_arm64"))},
		{"empty SHA256SUMS", func(t *testing.T, dir string) { t.Helper(); writeIn(t, dir, "SHA256SUMS", nil) }},
		{"extra line with an absolute path", extraSumsLine(func(t *testing.T, _, _ string) string {
			t.Helper()

			outside := t.TempDir()
			writeIn(t, outside, "outside.txt", []byte("x"))

			return sha256Hex([]byte("x")) + "  " + filepath.Join(outside, "outside.txt")
		})},
		{"extra line with a parent path", extraSumsLine(func(t *testing.T, dir, sum string) string {
			t.Helper()

			return sum + "  ../" + filepath.Base(dir) + "/" + archiveName("linux_amd64")
		})},
		{"extra line with a dot path", extraSumsLine(func(t *testing.T, _, sum string) string {
			t.Helper()

			return sum + "  ./" + archiveName("linux_amd64")
		})},
		{"extra BSD tag line", extraSumsLine(func(t *testing.T, _, sum string) string {
			t.Helper()

			return "SHA256 (" + archiveName("linux_amd64") + ") = " + sum
		})},
		{"extra comment line", extraSumsLine(func(t *testing.T, _, sum string) string {
			t.Helper()

			return "# " + sum
		})},
		{"NUL byte and junk after a line", editSums(func(l []string) []string {
			l[0] += "\x00junk"

			return l
		})},
		{"NUL byte at the end of a line", editSums(func(l []string) []string {
			l[3] += "\x00"

			return l
		})},
		{"carriage return at the end of a line", editSums(func(l []string) []string {
			l[1] += "\r"

			return l
		})},
		{"no newline at the end of the file", func(t *testing.T, dir string) {
			t.Helper()

			sums := readIn(t, dir, "SHA256SUMS")
			writeIn(t, dir, "SHA256SUMS", sums[:len(sums)-1])
		}},
		{"extra blank line", extraSumsLine(func(t *testing.T, _, _ string) string {
			t.Helper()

			return ""
		})},
	}

	// Only the verify script reads the entries of an archive: a replaced
	// verify script would pass this set, so this case runs once.
	t.Run("archive with an entry that is not allowed", func(t *testing.T) {
		t.Parallel()

		r := goodRun()
		r.set = writeSet(t, plus(fileEntry("evil", "x", 0o755)))
		checkRefusal(t, runUpload(t, r), 0, "evil", "rebuild the set, then run make release-verify VERSION=v1.2.3")
	})

	for _, tt := range tests {
		for _, replaced := range []bool{false, true} {
			name := tt.name
			if replaced {
				name += " with a verify script that passes"
			}

			t.Run(name, func(t *testing.T) {
				t.Parallel()

				r := goodRun()
				r.edit = tt.edit
				r.replaceVerify = replaced
				res := runUpload(t, r)

				checkRefusal(t, res, 0, "rebuild the set, then run make release-verify VERSION=v1.2.3")
			})
		}
	}
}

// sha256Hex returns the lowercase hex SHA-256 of data.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:])
}

// extraSumsLine appends one line to SHA256SUMS. The line holds a checksum that
// is right for the file that it names, so that sha256sum alone would pass it.
// line gets the directory of the set and the real checksum of the linux_amd64
// archive.
func extraSumsLine(line func(t *testing.T, dir, sum string) string) func(t *testing.T, dir string) {
	return func(t *testing.T, dir string) {
		t.Helper()

		sum := sha256Hex(readIn(t, dir, archiveName("linux_amd64")))
		sums := readIn(t, dir, "SHA256SUMS")

		writeIn(t, dir, "SHA256SUMS", append(sums, []byte(line(t, dir, sum)+"\n")...))
	}
}

// TestReleaseUploadChecksWhatItUploads gives the script a directory argument
// that another set can answer to. The set that the script checks must be the
// set that gh uploads from: the script exits 0, makes one create call, and the
// call runs in the checked directory. Each case runs with the real verify script and with a
// replaced one.
func TestReleaseUploadChecksWhatItUploads(t *testing.T) {
	t.Parallel()
	skipUpload(t)

	for _, hostile := range []string{"cdpath", "dash"} {
		for _, replaced := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s replaced %v", hostile, replaced), func(t *testing.T) {
				t.Parallel()

				r := goodRun()
				r.hostile = hostile
				r.replaceVerify = replaced
				r.set = writeSet(t, nil)
				res := runUpload(t, r)

				// Both sets are good, so a refusal is a wrong result.
				if res.code != 0 {
					t.Fatalf("exit %d, want 0; stderr: %q", res.code, res.stderr)
				}

				if got := len(createCalls(res.calls)); got != 1 {
					t.Fatalf("%d release create calls, want 1: %q", got, res.calls)
				}

				if res.cwd == "" {
					t.Fatalf("no create call ran in a directory: %q", res.calls)
				}

				if got, want := evalDir(t, res.cwd), evalDir(t, r.set); got != want {
					t.Errorf("gh ran in %s, want the checked set %s", got, want)
				}
			})
		}
	}
}

// TestReleaseUploadRefusesAMissingDirectoryWithoutEchoingIt checks that the
// refusal of a missing directory prints no byte of the caller's value.
func TestReleaseUploadRefusesAMissingDirectoryWithoutEchoingIt(t *testing.T) {
	t.Parallel()
	skipUpload(t)

	for _, replaced := range []bool{false, true} {
		t.Run(fmt.Sprintf("replaced %v", replaced), func(t *testing.T) {
			t.Parallel()

			r := goodRun()
			r.hostile = "newline"
			r.replaceVerify = replaced
			res := runUpload(t, r)

			checkRefusal(t, res, 0, "missing file: the set directory is not a directory")

			if strings.Contains(res.stderr, "forged") {
				t.Errorf("stderr echoes the directory: %q", res.stderr)
			}
		})
	}
}

// TestReleaseUploadRefusesAnUnreadableDirectoryWithoutEchoingIt checks that a
// directory that exists but cannot be entered gets the fixed refusal, and that
// bash prints no byte of the caller's value to stderr.
func TestReleaseUploadRefusesAnUnreadableDirectoryWithoutEchoingIt(t *testing.T) {
	t.Parallel()
	skipUpload(t)

	if os.Getuid() == 0 {
		t.Skip("root can enter a directory with mode 000")
	}

	for _, replaced := range []bool{false, true} {
		t.Run(fmt.Sprintf("replaced %v", replaced), func(t *testing.T) {
			t.Parallel()

			r := goodRun()
			r.replaceVerify = replaced
			r.set = writeSet(t, nil)

			if err := os.Chmod(r.set, 0); err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() {
				if err := os.Chmod(r.set, 0o700); err != nil {
					t.Error(err)
				}
			})

			res := runUpload(t, r)

			checkRefusal(t, res, 0, "missing file: the set directory is not a directory")

			if strings.Contains(res.stderr, r.set) {
				t.Errorf("stderr echoes the directory: %q", res.stderr)
			}
		})
	}
}

// TestReleaseUploadRefusesAnExistingRelease gives the script a list of
// releases that holds the tag.
func TestReleaseUploadRefusesAnExistingRelease(t *testing.T) {
	t.Parallel()
	skipUpload(t)

	testExistingReleaseRefused(t)

	for _, tt := range []struct {
		name   string
		apiOut string
	}{
		{"the list call fails", ""},
		{"the list call fails after it printed the tag", "v1.0.0\nv1.2.3\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := goodRun()
			r.apiOut = tt.apiOut
			r.apiCode = "1"
			res := runUpload(t, r)

			if res.code == 0 || len(createCalls(res.calls)) != 0 {
				t.Errorf("a failed list call must refuse: exit %d, calls %q", res.code, res.calls)
			}

			if !strings.Contains(res.stderr, "gh release list --repo owner/name") {
				t.Errorf("stderr %q lacks the list command", res.stderr)
			}
		})
	}

	// A tag that only looks like the version does not block it.
	for _, other := range []string{"v1.2.30", "v1.2.03", "xv1.2.3", "v1.2.3.1", "v1.2.3-rc1", " v1.2.3", "V1.2.3"} {
		t.Run("other tag "+other, func(t *testing.T) {
			t.Parallel()

			r := goodRun()
			r.apiOut = other + "\n"
			res := runUpload(t, r)

			if res.code != 0 || len(createCalls(res.calls)) != 1 {
				t.Errorf("%q must not block v1.2.3: exit %d, stderr %q", other, res.code, res.stderr)
			}
		})
	}
}

// testExistingReleaseRefused runs the cases where the list holds the tag.
func testExistingReleaseRefused(t *testing.T) {
	t.Helper()

	for _, tt := range []struct {
		name   string
		apiOut string
	}{
		{"draft or published", "v1.2.3\n"},
		{"among others", "v1.2.2\nv1.2.3\nv1.1.0\n"},
		{"last line with no newline", "v1.0.0\nv1.2.3"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := goodRun()
			r.apiOut = tt.apiOut
			res := runUpload(t, r)

			checkRefusal(t, res, 1, "a release for v1.2.3 exists", "gh release list --repo owner/name")
		})
	}
}

// TestReleaseUploadSaysHowToCleanAFailedCreate makes the create call fail.
func TestReleaseUploadSaysHowToCleanAFailedCreate(t *testing.T) {
	t.Parallel()
	skipUpload(t)

	r := goodRun()
	r.createCode = "1"
	res := runUpload(t, r)

	if res.code == 0 {
		t.Error("exit code 0, want non-zero")
	}

	for _, want := range []string{
		"the draft may exist with some files",
		"gh release delete v1.2.3 --repo owner/name",
		"re-run the failed job",
	} {
		if !strings.Contains(res.stderr, want) {
			t.Errorf("stderr %q lacks %q", res.stderr, want)
		}
	}

	if n := len(createCalls(res.calls)); n != 1 {
		t.Errorf("%d create calls, want 1: no retry", n)
	}

	for _, c := range res.calls {
		if len(c) > 1 && c[0] == "release" && c[1] != "create" {
			t.Errorf("the script ran %q: it must not delete or edit", c)
		}
	}

	if res.stdout != "" {
		t.Errorf("stdout %q, want empty", res.stdout)
	}
}

// TestReleaseUploadKeepsTheTokenOut fails each step in turn and looks for the
// marker of GH_TOKEN in each output and in each argument of a gh call.
func TestReleaseUploadKeepsTheTokenOut(t *testing.T) {
	t.Parallel()
	skipUpload(t)

	tests := []struct {
		name string
		edit func(r *uploadRun)
	}{
		{"success", func(*uploadRun) {}},
		{"bad version", func(r *uploadRun) { r.version = "v1$(id)" }},
		{"bad repo", func(r *uploadRun) { r.repo = new("a b") }},
		{"unset repo", func(r *uploadRun) { r.repo = nil }},
		{"changed set", func(r *uploadRun) { r.edit = flipArchive }},
		{"changed set, verify replaced", func(r *uploadRun) { r.edit = flipArchive; r.replaceVerify = true }},
		{"list failure", func(r *uploadRun) { r.apiCode = "1" }},
		{"existing release", func(r *uploadRun) { r.apiOut = "v1.2.3\n" }},
		{"create failure", func(r *uploadRun) { r.createCode = "1" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := goodRun()
			tt.edit(&r)
			res := runUpload(t, r)

			checkNoToken(t, res)
		})
	}
}

// checkNoToken checks that the marker of GH_TOKEN is in no output and in no
// argument of a gh call.
func checkNoToken(t *testing.T, res uploadResult) {
	t.Helper()

	if strings.Contains(res.stdout, uploadMarker) || strings.Contains(res.stderr, uploadMarker) {
		t.Errorf("the token is in the output: %q %q", res.stdout, res.stderr)
	}

	for _, call := range res.calls {
		for _, arg := range call {
			if strings.Contains(arg, uploadMarker) {
				t.Errorf("the token is in a gh argument: %q", call)
			}
		}
	}
}

func flipArchive(t *testing.T, dir string) {
	t.Helper()

	data := readIn(t, dir, archiveName("linux_amd64"))
	data[len(data)/2] ^= 0xff
	writeIn(t, dir, archiveName("linux_amd64"), data)
}

// installSection returns the maintainer section of the install page.
func installSection(t *testing.T) string {
	t.Helper()

	root := openRootOf(t, "../..")
	defer closeRoot(t, root)

	raw, err := root.ReadFile("docs/guides/install.md")
	if err != nil {
		t.Fatal(err)
	}

	_, rest, ok := strings.Cut(string(raw), "\n## Maintainer: publish the release archives\n")
	if !ok {
		t.Fatal(`docs/guides/install.md has no section "Maintainer: publish the release archives"`)
	}

	section, _, _ := strings.Cut(rest, "\n## ")

	return section
}

// TestInstallGuideReleaseSection checks the commands and the sentences that
// the maintainer section must hold, and that it cites no internal reference.
func TestInstallGuideReleaseSection(t *testing.T) {
	t.Parallel()

	section := installSection(t)

	for _, want := range []string{
		"gh release edit", "--draft=false", "gh release list", "gh release delete",
		"Re-run failed jobs", "SHA256SUMS is not a signature.",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the section lacks %q", want)
		}
	}

	internalRef := regexp.MustCompile(`(?i)\b(ADR|RFC|issue|PR|pull request|milestone)\s*#?\d+|#\d{2,}`)
	if m := internalRef.FindString(section); m != "" {
		t.Errorf("the section holds the internal reference %q", m)
	}
}

// TestReleaseRecordCheckTargetKeepsTheVersionOutOfTheShell runs the make
// target, in a copy of the work tree, with a version that holds a quote and a
// command. The command must not run.
func TestReleaseRecordCheckTargetKeepsTheVersionOutOfTheShell(t *testing.T) {
	t.Parallel()

	tree := copyWorkTree(t)
	marker := filepath.Join(t.TempDir(), "marker")
	version := "v1'; touch " + marker + "; '"

	// Make of a copy of the work tree runs on purpose. The tree and the
	// hostile version come from the environment (RELEASE_TREE,
	// TEST_RELEASE_VERSION); the version is one quoted argument.
	cmd := exec.CommandContext(
		t.Context(), "bash", "-c",
		`exec make --no-print-directory -C "$RELEASE_TREE" release-record-check "VERSION=$TEST_RELEASE_VERSION"`,
	)
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "RELEASE_TREE=" + tree, "TEST_RELEASE_VERSION=" + version,
	}

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Errorf("make accepted the version; output:\n%s", out)
	}

	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("the marker file exists: the version ran as shell text (%v)", statErr)
	}

	if !strings.Contains(string(out), "is not vMAJOR.MINOR.PATCH") {
		t.Errorf("output lacks the version message of the script:\n%s", out)
	}
}

// TestReleaseUploadScriptHasNoTraceOrEval checks that the script has no
// command that prints the environment or runs a string as code.
func TestReleaseUploadScriptHasNoTraceOrEval(t *testing.T) {
	t.Parallel()

	script := uploadScriptText(t)

	for _, deny := range []*regexp.Regexp{
		regexp.MustCompile(`(?m)^\s*set\s+-[a-z]*x`),
		regexp.MustCompile(`(?i)xtrace`),
		regexp.MustCompile(`(?m)(^|[;&|(]\s*|\s)eval\s`),
		regexp.MustCompile(`\bbash\s+-c\b`),
		regexp.MustCompile(`\bsh\s+-c\b`),
	} {
		if m := deny.FindString(script); m != "" {
			t.Errorf("the script holds %q", m)
		}
	}
}

// TestReleaseUploadCreatesFromTheFileList checks that the create line passes
// the five names as the list and does not use a glob.
func TestReleaseUploadCreatesFromTheFileList(t *testing.T) {
	t.Parallel()

	script := uploadScriptText(t)

	var create string

	for line := range strings.SplitSeq(script, "\n") {
		if strings.Contains(line, "gh release create") && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			create = line
		}
	}

	if create == "" {
		t.Fatal("the script has no gh release create line")
	}

	if !strings.Contains(create, `-- "${files[@]}"`) {
		t.Errorf("the create line does not pass the file list: %s", create)
	}

	if strings.ContainsAny(strings.ReplaceAll(create, "[@]", ""), "*?") {
		t.Errorf("the create line holds a glob: %s", create)
	}
}

func uploadScriptText(t *testing.T) string {
	t.Helper()

	root := openRootOf(t, "../..")
	defer closeRoot(t, root)

	raw, err := root.ReadFile("scripts/release-upload.sh")
	if err != nil {
		t.Fatal(err)
	}

	return string(raw)
}
