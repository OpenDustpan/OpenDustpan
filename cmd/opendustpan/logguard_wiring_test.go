package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// guardEscape is the forged message as the guard writes it: a backslash
// and an "n", not a line break. A handler without the guard writes the JSON
// or Go escape of a raw line break, which a reader decodes back to the line
// break.
const guardEscape = `ok\nlevel=ERROR msg=forged`

// TestAppLogEscapesHiddenRunesBeforeEachHandler is the logger layer in the
// application log: each stream and each sink gets the escape of the guard.
func TestAppLogEscapesHiddenRunesBeforeEachHandler(t *testing.T) {
	t.Parallel()

	for _, format := range []string{"json", "text"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			var stderr bytes.Buffer

			dir := t.TempDir()
			path := filepath.Join(dir, sinkFileName)

			app, err := newAppLog(
				[]string{
					"stderr",
					"file:" + path,
				},
				slog.LevelInfo,
				format,
				&stderr,
				sinkEnv{lookup: noEnv, ranges: nil},
			)
			if err != nil {
				t.Fatalf("new app log: %v", err)
			}

			app.logger.Info("ok\nlevel=ERROR msg=forged", slog.String("path", "a\x1bb"))

			if closeErr := app.close(t.Context()); closeErr != nil {
				t.Fatalf("close app log: %v", closeErr)
			}

			checkStreamEscape(t, format, stderr.Bytes())
			checkSinkEscape(t, dir)
		})
	}
}

// checkStreamEscape fails when the stream line does not hold the escape of
// the guard.
func checkStreamEscape(t *testing.T, format string, out []byte) {
	t.Helper()

	if format == "text" {
		if !strings.Contains(string(out), `msg="ok\\nlevel=ERROR msg=forged"`) {
			t.Errorf("stderr = %q, want the escape of the guard", out)
		}

		return
	}

	var rec struct {
		Msg string `json:"msg"`
	}

	if err := json.Unmarshal(bytes.TrimSpace(out), &rec); err != nil {
		t.Fatalf("stderr is not one JSON line: %v: %q", err, out)
	}

	if rec.Msg != guardEscape {
		t.Errorf("stderr msg = %q, want %q", rec.Msg, guardEscape)
	}
}

// sinkFileName is the name of the file sink in the temp directory of a test.
const sinkFileName = "app.jsonl"

// checkSinkEscape fails when the record of the file sink in dir does not
// hold the escape of the guard. It reads the file through an os.Root, so
// the read stays in dir.
func checkSinkEscape(t *testing.T, dir string) {
	t.Helper()

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("open sink directory: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Errorf("close sink directory: %v", closeErr)
		}
	})

	data, err := root.ReadFile(sinkFileName)
	if err != nil {
		t.Fatalf("read sink file: %v", err)
	}

	var rec struct {
		Msg  string `json:"msg"`
		Path string `json:"path"`
	}

	if err := json.Unmarshal(bytes.TrimSpace(data), &rec); err != nil {
		t.Fatalf("sink file is not one JSON line: %v: %q", err, data)
	}

	if rec.Msg != guardEscape || rec.Path != `a\x1bb` {
		t.Errorf("sink record = %+v, want the escape of the guard", rec)
	}
}
