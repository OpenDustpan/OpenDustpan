package launcher_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/launcher"
)

const sampleHandoff = "dph_AbC-123_xyz"

// pipeWith returns the read end of a pipe that holds data.
func pipeWith(t *testing.T, data string) *os.File {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}

	if _, err = w.WriteString(data); err != nil {
		t.Fatalf("write the pipe: %v", err)
	}

	if err = w.Close(); err != nil {
		t.Fatalf("close the pipe: %v", err)
	}

	return r
}

func TestReadHandoffFromAPipe(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		data string
		ok   bool
	}{
		{"token", sampleHandoff, true},
		{"token and a line feed", sampleHandoff + "\n", true},
		{"no prefix", "AbC-123", false},
		{"prefix only", "dph_", false},
		{"a space", "dph_abc def", false},
		{"two lines", sampleHandoff + "\n\n", false},
		{"a carriage return", sampleHandoff + "\r\n", false},
		{"empty", "", false},
		{"too long", "dph_" + strings.Repeat("a", launcher.MaxHandoff), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := launcher.ReadHandoff(pipeWith(t, tc.data))
			if tc.ok {
				if err != nil || got != sampleHandoff {
					t.Fatalf("ReadHandoff = %q, %v; want the token", got, err)
				}

				return
			}

			wantRefused(t, got, err, tc.data)
		})
	}
}

// TestReadHandoffRefusesAFile checks that a token in a regular file is
// refused: only a pipe carries the handoff.
func TestReadHandoffRefusesAFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "token"), []byte(sampleHandoff), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	f, err := os.OpenInRoot(dir, "token")
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	if got, err := launcher.ReadHandoff(f); !errors.Is(err, launcher.ErrHandoff) || got != "" {
		t.Fatalf("ReadHandoff(file) = %q, %v; want ErrHandoff", got, err)
	}

	if _, err := launcher.ReadHandoff(nil); !errors.Is(err, launcher.ErrHandoff) {
		t.Fatalf("ReadHandoff(nil) = %v, want ErrHandoff", err)
	}
}

// wantRefused fails the test when a read of the handoff data did not fail
// with ErrHandoff, or when its error holds the data.
func wantRefused(t *testing.T, got string, err error, data string) {
	t.Helper()

	if !errors.Is(err, launcher.ErrHandoff) || got != "" {
		t.Fatalf("ReadHandoff = %q, %v; want ErrHandoff", got, err)
	}

	if data != "" && strings.Contains(err.Error(), data) {
		t.Fatalf("the error %q holds the input", err)
	}
}
