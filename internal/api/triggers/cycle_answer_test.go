package triggers_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	triggersapi "opendustpan.org/opendustpan/internal/api/triggers"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/identity"
)

// cycleGolden is the body of the 409 answer to a save that closes a loop, as it
// was before the answer went through the shared error body. The bytes are the
// contract of the console.
const cycleGolden = `{"message":"trigger closes a loop of units: app -\u003e 1 unit that you cannot read -\u003e app",` +
	`"code":409,"errorCode":"trigger.cycle","units":[{"id":"ws-a","tool":"Pulumi","name":"app"},` +
	`{"id":"ws-a","tool":"Pulumi","name":"app"}]}` + "\n"

// TestCycleAnswer checks the answer to a refused save that closes a loop: the
// body is byte for byte the golden body, and the headers are the ones of every
// error answer.
func TestCycleAnswer(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	triggersapi.WriteError(t.Context(), rec, fmt.Errorf("save: %w", cycleError()))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}

	if got := rec.Body.String(); got != cycleGolden {
		t.Errorf("body = %q\nwant   %q", got, cycleGolden)
	}

	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}

	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
}

// TestCycleThatWrapsADenialIsForbidden checks that a cycle error that also wraps
// identity.ErrDenied answers the denial, in either order of the wrap, and that
// the body names no unit.
func TestCycleThatWrapsADenialIsForbidden(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{"denial first", fmt.Errorf("%w: %w", identity.ErrDenied, cycleError())},
		{"cycle first", fmt.Errorf("%w: %w", cycleError(), identity.ErrDenied)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			triggersapi.WriteError(t.Context(), rec, tc.err)

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusForbidden, rec.Body.Bytes(),
				errcodetest.Want{Code: errcode.AccessDenied, Args: nil, RequestBody: nil})

			if strings.Contains(rec.Body.String(), "units") || strings.Contains(rec.Body.String(), "app") {
				t.Errorf("a denied cycle names its units: %s", rec.Body)
			}
		})
	}
}

// TestCycleLogsItsRefusalAtDebug checks the content of the one log line of a
// cycle answer: the Debug line of the shared error module, with the status, the
// code and the error text, which names no unit that the caller cannot read.
func TestCycleLogsItsRefusalAtDebug(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	rec := httptest.NewRecorder()

	triggersapi.WriteErrorTo(t.Context(), rec, logger, nil, fmt.Errorf("save: %w", cycleError()))

	logs := buf.String()

	for _, want := range []string{"level=DEBUG", `msg="request refused"`, "status=409", "code=trigger.cycle"} {
		if !strings.Contains(logs, want) {
			t.Errorf("log %q lacks %q", logs, want)
		}
	}

	if strings.Contains(logs, "secret-net") || strings.Contains(logs, "ws-hidden") {
		t.Errorf("log names a unit that the caller cannot read: %s", logs)
	}

	if n := strings.Count(logs, "\n"); n != 1 {
		t.Errorf("log has %d lines, want 1: %s", n, logs)
	}
}
