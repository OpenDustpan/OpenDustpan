package zonegen_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/zonegen"
)

// committedPath answers the path of the generated file.
func committedPath(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller holds no file")
	}

	return filepath.Join(filepath.Dir(file), "..", "..", "pkg", "preferences", "zones_gen.go")
}

// archiveOf builds a zip archive with one empty file for each name.
func archiveOf(t *testing.T, names ...string) []byte {
	t.Helper()

	var buf bytes.Buffer

	writer := zip.NewWriter(&buf)

	for _, name := range names {
		w, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := io.WriteString(w, "x"); err != nil {
			t.Fatal(err)
		}
	}

	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

// TestCommittedListMatchesToolchain fails when the committed list and the time
// zone archive of the toolchain differ, for example after a Go upgrade.
func TestCommittedListMatchesToolchain(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	code := zonegen.Run(t.Context(), []string{"-check", "-out", committedPath(t)}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("the zone list does not match the Go toolchain (%s); run make zones-generate",
			strings.TrimSpace(stderr.String()))
	}
}

func TestGenerateOutput(t *testing.T) {
	t.Parallel()

	names := []string{"UTC", "Europe/Berlin", "America/Lima", "UTC"}

	got, err := zonegen.Generate(archiveOf(t, names...))
	if err != nil {
		t.Fatal(err)
	}

	text := string(got)
	if !strings.HasPrefix(text, "// Code generated ") || !strings.Contains(text, "; DO NOT EDIT.\n") {
		t.Errorf("header missing:\n%s", text)
	}

	if n := strings.Count(text, `"UTC"`); n != 1 {
		t.Errorf("UTC listed %d times, want 1", n)
	}

	if strings.Index(text, `"America/Lima"`) > strings.Index(text, `"Europe/Berlin"`) {
		t.Error("names are not sorted")
	}

	again, err := zonegen.Generate(archiveOf(t, names...))
	if err != nil || !bytes.Equal(got, again) {
		t.Error("Generate is not deterministic")
	}
}

func TestGenerateRefusesBadArchive(t *testing.T) {
	t.Parallel()

	if _, err := zonegen.Generate(archiveOf(t)); !errors.Is(err, zonegen.ErrNoZones) {
		t.Errorf("empty archive: got %v, want ErrNoZones", err)
	}

	if _, err := zonegen.Generate([]byte("not a zip")); err == nil {
		t.Error("accepted bytes that are not an archive")
	}
}

// TestGenerateRefusesHostileEntry checks that a changed archive cannot put a
// hostile name in the list.
func TestGenerateRefusesHostileEntry(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"Local", "localtime", "posixrules", "posix/UTC", "right/UTC",
		"../etc/passwd", "/etc/passwd", "Europe//Berlin", "a b", "Europe/Zürich", "a\x00b", "a\"+x+\"", strings.Repeat("a", 65),
	} {
		if _, err := zonegen.Generate(archiveOf(t, name)); !errors.Is(err, zonegen.ErrBadName) {
			t.Errorf("entry %q: got %v, want ErrBadName", name, err)
		}
	}
}
