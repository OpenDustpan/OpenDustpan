package file_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/logsink"
	"opendustpan.org/opendustpan/pkg/logsink/file"
	"opendustpan.org/opendustpan/pkg/logsink/logsinktest"
)

func TestConformance(t *testing.T) {
	t.Parallel()

	logsinktest.Run(t, func(t *testing.T) logsinktest.TestSink {
		t.Helper()

		path := filepath.Join(t.TempDir(), "audit.jsonl")

		s, err := file.Open(path)
		if err != nil {
			t.Fatalf("open: %v", err)
		}

		return logsinktest.TestSink{
			Sink: s,
			Delivered: func(t *testing.T, _ int) [][]byte {
				t.Helper()

				return readLines(t, path)
			},
		}
	})
}

func TestNewFileIsPrivate(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "audit.jsonl")

	s, err := file.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	closeOnCleanup(t, s)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("mode: got %o, want 600", mode)
	}
}

func TestReopenWritesToTheNewFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	moved := filepath.Join(dir, "audit.jsonl.1")

	s, err := file.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	closeOnCleanup(t, s)

	rec := logsink.Record{Time: time.Unix(0, 0), Message: "one", Attrs: nil, Level: logsink.LevelInfo}

	if err := s.Write(t.Context(), []logsink.Record{rec}); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := os.Rename(path, moved); err != nil {
		t.Fatalf("rename: %v", err)
	}

	if err := s.Reopen(); err != nil {
		t.Fatalf("reopen: %v", err)
	}

	rec.Message = "two"

	if err := s.Write(t.Context(), []logsink.Record{rec}); err != nil {
		t.Fatalf("write after reopen: %v", err)
	}

	if got := len(readLines(t, moved)); got != 1 {
		t.Fatalf("moved file: got %d lines, want 1", got)
	}

	lines := readLines(t, path)
	if len(lines) != 1 || !bytes.Contains(lines[0], []byte(`"msg":"two"`)) {
		t.Fatalf("new file: got %q, want one line with message two", lines)
	}
}

func TestOpenEmptyPathFails(t *testing.T) {
	t.Parallel()

	if _, err := file.Open(""); err == nil {
		t.Fatal("open of an empty path: got nil error")
	}
}

// readLines answers the lines of the file at path, without the newlines.
func readLines(t *testing.T, path string) [][]byte {
	t.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // The path is a test temp file.
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	data = bytes.TrimSuffix(data, []byte("\n"))
	if len(data) == 0 {
		return nil
	}

	return bytes.Split(data, []byte("\n"))
}

// closeOnCleanup closes s when the test ends and reports a failed close.
func closeOnCleanup(t *testing.T, s logsink.Sink) {
	t.Helper()

	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close sink: %v", err)
		}
	})
}
