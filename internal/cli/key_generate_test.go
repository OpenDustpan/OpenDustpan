package cli_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli"
)

// runKeyGenerate runs key generate with args and answers its report and its
// error.
func runKeyGenerate(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var stdout, stderr bytes.Buffer

	err := cli.KeyGenerateCommand().Run(t.Context(), args, &stdout, &stderr)

	return stdout.String(), err
}

// readKey reads the key file at path and answers its raw bytes.
func readKey(t *testing.T, path string) []byte {
	t.Helper()

	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		t.Fatalf("open directory: %v", err)
	}

	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Errorf("close directory: %v", closeErr)
		}
	}()

	raw, err := root.ReadFile(filepath.Base(path))
	if err != nil {
		t.Fatalf("read key file: %v", err)
	}

	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("decode key: %v", err)
	}

	return key
}

// TestKeyGenerateWritesKey proves the result: a file with 32 random bytes in
// base64, mode 0600, a report that names the path and the warning, and a
// report that never holds the key. A second key differs from the first.
func TestKeyGenerateWritesKey(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	first := filepath.Join(dir, "first.key")
	second := filepath.Join(dir, "second.key")

	report, err := runKeyGenerate(t, "--out", first)
	if err != nil {
		t.Fatalf("key generate: %v", err)
	}

	if _, secondErr := runKeyGenerate(t, "--out", second); secondErr != nil {
		t.Fatalf("key generate: %v", secondErr)
	}

	key := readKey(t, first)
	if len(key) != cli.KeyBytes {
		t.Fatalf("key holds %d bytes, want %d", len(key), cli.KeyBytes)
	}

	if bytes.Equal(key, readKey(t, second)) {
		t.Fatal("two keys are equal, want different random keys")
	}

	info, err := os.Stat(first)
	if err != nil {
		t.Fatalf("stat key file: %v", err)
	}

	if info.Mode().Perm() != cli.KeyFileMode {
		t.Fatalf("key file mode = %04o, want %04o", info.Mode().Perm(), cli.KeyFileMode)
	}

	for _, want := range []string{first, "loses every secret", "Nobody can recover them", "--key-encryption-key-file"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report lacks %q:\n%s", want, report)
		}
	}

	if strings.Contains(report, base64.StdEncoding.EncodeToString(key)) {
		t.Fatal("report holds the key")
	}
}

// TestKeyGenerateRefusesBadInput sends hostile input straight to the check of
// the arguments: it makes no file and the error names what helps.
func TestKeyGenerateRefusesBadInput(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	cases := []struct {
		name string
		args []string
		want error
	}{
		{"no path", nil, cli.ErrKeyOutMissing},
		{"blank path", []string{"--out", "   "}, cli.ErrKeyOutMissing},
		{"line end in the path", []string{"--out", dir + "/" + "a\nb.key"}, cli.ErrKeyOutInvalid},
		{"carriage return in the path", []string{"--out", dir + "/" + "a\rb.key"}, cli.ErrKeyOutInvalid},
		{"escape in the path", []string{"--out", dir + "/" + "a\x1bb.key"}, cli.ErrKeyOutInvalid},
		{"delete in the path", []string{"--out", dir + "/" + "a\x7fb.key"}, cli.ErrKeyOutInvalid},
		{"a directory with a separator", []string{"--out", dir + "/"}, cli.ErrKeyOutDirectory},
		{"a directory with two separators", []string{"--out", dir + "//"}, cli.ErrKeyOutDirectory},
		{"the dot", []string{"--out", "."}, cli.ErrKeyOutDirectory},
		{"the directory dot", []string{"--out", dir + "/."}, cli.ErrKeyOutDirectory},
		{"the parent", []string{"--out", dir + "/.."}, cli.ErrKeyOutDirectory},
		{"the root", []string{"--out", "/"}, cli.ErrKeyOutDirectory},
		{"an operand", []string{"--out", filepath.Join(dir, "a.key"), "extra"}, cli.ErrKeyGenerateArgs},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			report, err := runKeyGenerate(t, tc.args...)
			if !errors.Is(err, tc.want) {
				t.Fatalf("key generate error = %v, want %v", err, tc.want)
			}

			if report != "" {
				t.Fatalf("key generate printed a report on an error:\n%s", report)
			}
		})
	}

	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("a refused key generate made files (read err %v): %v", err, entries)
	}
}

// TestKeyGenerateNeverOverwrites proves that a file at the path stays as it
// is, also when the path is a symbolic link to a file or to a missing file:
// the command writes nothing through the link.
func TestKeyGenerateNeverOverwrites(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.key")
	target := filepath.Join(dir, "target.key")
	missing := filepath.Join(dir, "missing.key")
	linkToFile := filepath.Join(dir, "link-to-file.key")
	linkToMissing := filepath.Join(dir, "link-to-missing.key")

	for _, path := range []string{existing, target} {
		if err := os.WriteFile(path, []byte("keep\n"), cli.KeyFileMode); err != nil {
			t.Fatalf("write file: %v", err)
		}
	}

	if err := os.Symlink(target, linkToFile); err != nil {
		t.Skipf("symbolic links are not available: %v", err)
	}

	if err := os.Symlink(missing, linkToMissing); err != nil {
		t.Fatalf("make link: %v", err)
	}

	for _, path := range []string{existing, linkToFile, linkToMissing} {
		_, err := runKeyGenerate(t, "--out", path)
		if !errors.Is(err, cli.ErrKeyFileExists) {
			t.Fatalf("key generate over %s: error = %v, want ErrKeyFileExists", filepath.Base(path), err)
		}

		if !strings.Contains(err.Error(), "set --out to a new path") {
			t.Fatalf("error = %q, want the setting that helps", err)
		}
	}

	for _, path := range []string{existing, target} {
		if got := string(readKeyRaw(t, path)); got != "keep\n" {
			t.Fatalf("%s = %q, want it unchanged", filepath.Base(path), got)
		}
	}

	if _, err := os.Lstat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("key generate wrote through a link to %s (lstat err = %v)", filepath.Base(missing), err)
	}
}

// readKeyRaw reads the file at path as it is.
func readKeyRaw(t *testing.T, path string) []byte {
	t.Helper()

	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		t.Fatalf("open directory: %v", err)
	}

	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Errorf("close directory: %v", closeErr)
		}
	}()

	raw, err := root.ReadFile(filepath.Base(path))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}

	return raw
}

// TestKeyGenerateMakesNoDirectory proves that a missing directory is an error
// and that the command makes no directory: the operator chose the place.
func TestKeyGenerateMakesNoDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "missing", "dustpan.key")

	if _, err := runKeyGenerate(t, "--out", path); err == nil {
		t.Fatal("key generate in a missing directory succeeded, want an error")
	}

	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("key generate made the directory (stat err = %v)", err)
	}
}

// errTestWrite is the error of the failing write step.
var errTestWrite = errors.New("test write failure")

// TestWriteKeyFileFailedWriteLeavesNoFile proves that a key file whose write
// fails does not stay on disk: the next start must not read a half-written
// key.
func TestWriteKeyFileFailedWriteLeavesNoFile(t *testing.T) {
	t.Parallel()

	keyPath := filepath.Join(t.TempDir(), "dustpan.key")

	failingWrite := func(file *os.File, contents string) error {
		// Write half the contents, then fail, as a full disk might.
		if _, err := file.WriteString(contents[:len(contents)/2]); err != nil {
			t.Errorf("write half the key: %v", err)
		}

		return errTestWrite
	}

	err := cli.WriteKeyFile(keyPath, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n", failingWrite)
	if !errors.Is(err, errTestWrite) {
		t.Fatalf("write key file with a failing write: err = %v, want the write error", err)
	}

	if _, statErr := os.Stat(keyPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("key file after a failed write: stat err = %v, want no file", statErr)
	}
}

// TestWriteKeyFileWritesContents proves that WriteKeyFile with the real write
// step leaves the full contents with owner-only mode.
func TestWriteKeyFileWritesContents(t *testing.T) {
	t.Parallel()

	keyPath := filepath.Join(t.TempDir(), "dustpan.key")
	contents := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n"

	if err := cli.WriteKeyFile(keyPath, contents, cli.WriteKeyContents); err != nil {
		t.Fatalf("write key file: %v", err)
	}

	if got := string(readKeyRaw(t, keyPath)); got != contents {
		t.Fatalf("key file = %q, want %q", got, contents)
	}

	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat key file: %v", err)
	}

	if info.Mode().Perm() != cli.KeyFileMode {
		t.Fatalf("key file mode = %o, want %o", info.Mode().Perm(), cli.KeyFileMode)
	}
}

// TestWriteKeyFileRefusesDirectoryPath sends a path of a directory straight to
// the writer, as if the check of key generate had failed: it writes no file
// in the directory and answers ErrKeyOutDirectory.
func TestWriteKeyFileRefusesDirectoryPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	for _, path := range []string{dir + "/", dir + "//", dir + "/.", dir + "/..", "."} {
		err := cli.WriteKeyFile(path, "AAAA\n", cli.WriteKeyContents)
		if !errors.Is(err, cli.ErrKeyOutDirectory) {
			t.Fatalf("WriteKeyFile(%q) error = %v, want ErrKeyOutDirectory", path, err)
		}
	}

	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("the writer made files in the directory (read err %v): %v", err, entries)
	}
}
