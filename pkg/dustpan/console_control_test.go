package dustpan_test

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestConsoleControlPaths(t *testing.T) {
	t.Parallel()

	h := consoleServerHandler(t)
	cases := []struct {
		name    string
		path    string
		rawPath string
	}{
		{"NUL after probe path", "/health/live%00", ""},
		{"NUL at root", "/%00", ""},
		{"NUL in asset name", "/index.html%00.js", ""},
		{"line feed", "/%0a", ""},
		{"DEL", "/%7f", ""},
		{"C1 control", "/%C2%85", ""},
		{"raw NUL", "/x", "/\x00"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.path, nil)
			if tc.rawPath != "" {
				req.URL.Path = tc.rawPath
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			checkConsoleInvalidPathAnswer(t, rec)
		})
	}
}

// checkConsoleInvalidPathAnswer fails the test unless rec is the 400 answer
// with the catalog code request.invalid-path, no arguments and no-store.
func checkConsoleInvalidPathAnswer(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()

	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}

	var got struct {
		Code      int            `json:"code"`
		Message   string         `json:"message"`
		ErrorCode string         `json:"errorCode"`
		ErrorArgs map[string]any `json:"errorArgs"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not JSON: %v: %q", err, rec.Body.String())
	}

	if rec.Code != http.StatusBadRequest || got.Code != http.StatusBadRequest {
		t.Errorf("status = %d, code = %d, want %d", rec.Code, got.Code, http.StatusBadRequest)
	}

	if got.ErrorCode != "request.invalid-path" {
		t.Errorf("errorCode = %q, want request.invalid-path", got.ErrorCode)
	}

	if got.Message == "" || got.ErrorArgs != nil {
		t.Errorf("message = %q, errorArgs = %v", got.Message, got.ErrorArgs)
	}
}

func TestConsoleLivenessWithoutControl(t *testing.T) {
	t.Parallel()

	h := consoleServerHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health/live", nil))

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

// TestConsoleRootRejectsNUL is the file-lookup layer on its own: the os.Root
// file system refuses a name with a NUL byte, without the handler check.
func TestConsoleRootRejectsNUL(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<p>console</p>"), 0o600); err != nil {
		t.Fatalf("write index.html: %v", err)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("open console directory: %v", err)
	}

	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Errorf("close console directory: %v", err)
		}
	})

	fsys := root.FS()
	cases := []struct {
		name   string
		lookup func() error
	}{
		{"stat", func() error {
			if _, err := fs.Stat(fsys, "index.html\x00"); err != nil {
				return fmt.Errorf("stat a name with a NUL byte: %w", err)
			}

			return nil
		}},
		{"read", func() error {
			if _, err := fs.ReadFile(fsys, "index.html\x00"); err != nil {
				return fmt.Errorf("read a name with a NUL byte: %w", err)
			}

			return nil
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if err := tc.lookup(); err == nil {
				t.Fatal("file lookup accepted a NUL byte")
			}
		})
	}
}
