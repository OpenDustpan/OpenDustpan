package tfe

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/workspace"
)

// TestWriteStateDiffErrorLogsBothVersions proves that a diff that the bound
// refuses answers 413 and logs one warning with both state versions, because
// the error does not tell which version is too deep or too large.
func TestWriteStateDiffErrorLogsBothVersions(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	h := new(Handler)
	h.logger = slog.New(slog.NewTextHandler(&buf, nil))

	rec := httptest.NewRecorder()
	err := fmt.Errorf("diff states: %w", workspace.ErrStateBound)

	h.writeStateDiffError(t.Context(), rec, err, "sv-from123", "sv-to123")

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413: %s", rec.Code, rec.Body.String())
	}

	line := buf.String()
	for _, want := range []string{
		"level=WARN", "state is too deep or too large for a masked read",
		"from_state_version=sv-from123", "to_state_version=sv-to123",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("log %q misses %q", line, want)
		}
	}
}

// TestWriteStateDiffErrorLogsNothingForOtherErrors proves that an error that
// is not the bound writes its own answer and no bound warning.
func TestWriteStateDiffErrorLogsNothingForOtherErrors(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	h := new(Handler)
	h.logger = slog.New(slog.NewTextHandler(&buf, nil))

	rec := httptest.NewRecorder()
	h.writeStateDiffError(t.Context(), rec, workspace.ErrStateVersionNotFound, "sv-from123", "sv-to123")

	if rec.Code != http.StatusNotFound || buf.Len() != 0 {
		t.Fatalf("status = %d, log = %q, want 404 and no log", rec.Code, buf.String())
	}
}

// TestStateBoundLogsRemoveCRAndLF is the hostile case of the log layer: an
// identifier with CR and LF must not reach the log record with those bytes.
func TestStateBoundLogsRemoveCRAndLF(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	h := new(Handler)
	h.logger = slog.New(slog.NewTextHandler(&buf, nil))

	rec := httptest.NewRecorder()
	err := fmt.Errorf("diff states: %w", workspace.ErrStateBound)

	h.writeStateDiffError(t.Context(), rec, err, "sv-a\r\nlevel=ERROR msg=forged", "sv-b\nforged2")

	line := buf.String()
	if strings.ContainsAny(line, "\r") || strings.Count(line, "\n") != 1 || strings.Contains(line, `\r`) ||
		strings.Contains(line, `\n`) {
		t.Fatalf("log holds CR or LF from an identifier: %q", line)
	}

	if got := oneLine("a\r\nb"); got != "ab" {
		t.Fatalf("oneLine = %q, want %q", got, "ab")
	}
}
