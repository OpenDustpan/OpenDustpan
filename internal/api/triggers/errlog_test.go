package triggers_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/api/problem"
	triggersapi "opendustpan.org/opendustpan/internal/api/triggers"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trigger"
)

// errUnknown is an error that no case of the adapter knows.
var errUnknown = errors.New("driver: connection reset")

// errDriverDetail is an error whose text must never reach a log.
var errDriverDetail = errors.New("driver detail")

// cycleError returns a cycle error whose path holds one unit that the caller
// can read and one that the caller cannot read.
func cycleError() *trigger.CycleError {
	a := graph.Unit{Namespace: "acme", Tool: "pulumi", ID: "ws-a"}
	hidden := graph.Unit{Namespace: "acme", Tool: "opentofu", ID: "ws-hidden"}

	return &trigger.CycleError{
		Path:     []graph.Unit{a, hidden, a},
		Names:    map[graph.Unit]string{a: "app", hidden: "secret-net"},
		Readable: map[graph.Unit]bool{a: true},
	}
}

// TestErrorAnswerHeaders fails for an error answer that a cache may keep: every
// error answer of the triggers API carries Cache-Control: no-store.
func TestErrorAnswerHeaders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		status int
	}{
		{"invalid trigger", trigger.ErrInvalid, http.StatusBadRequest},
		{"unknown error", errUnknown, http.StatusInternalServerError},
		{"cycle", cycleError(), http.StatusConflict},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			triggersapi.WriteError(t.Context(), rec, tc.err)

			if rec.Code != tc.status {
				t.Errorf("status = %d, want %d", rec.Code, tc.status)
			}

			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want %q", got, "no-store")
			}
		})
	}
}

// refusedCode answers every error with a code that the catalog refuses.
func refusedCode(error) (problem.Problem, bool) {
	return problem.New(http.StatusBadRequest, "trigger.no-such-code", "secret detail"), true
}

// TestErrorLog fails for an error answer whose log line is not the one of the
// shared error module: a refused code logs error_code, a fenced write logs its
// wrap, and a 500 logs its status and never the error text.
func TestErrorLog(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		classify problem.Classifier
		err      error
		wantLog  []string
		noLog    []string
		wantCode string
		wantBody []string
	}{
		{
			name: "unknown error", classify: nil, err: errUnknown,
			wantLog: []string{"request failed", "status=500"}, noLog: []string{errUnknown.Error()},
			wantCode: errcode.ServerInternal, wantBody: nil,
		},
		{
			name: "refused code", classify: refusedCode, err: errDriverDetail,
			wantLog: []string{"error code refused", "error_code=trigger.no-such-code", "request failed"},
			noLog:   []string{"secret detail", "driver detail"}, wantCode: errcode.ServerInternal, wantBody: nil,
		},
		{
			name: "fenced", classify: nil, err: fmt.Errorf("save the trigger: %w", namespace.ErrFenced),
			wantLog: []string{"write fenced", "save the trigger"}, noLog: nil, wantCode: errcode.NamespaceFenced,
			wantBody: []string{namespace.ErrFenced.Error()},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var logs bytes.Buffer

			logger := slog.New(slog.NewTextHandler(&logs, nil))
			rec := httptest.NewRecorder()

			triggersapi.WriteErrorTo(t.Context(), rec, logger, tc.classify, tc.err)

			checkLog(t, logs.String(), tc.wantLog, tc.noLog)

			for _, want := range tc.wantBody {
				if !strings.Contains(rec.Body.String(), want) {
					t.Errorf("body = %q, want it to hold %q", rec.Body.String(), want)
				}
			}

			if strings.Contains(rec.Body.String(), "save the trigger") {
				t.Errorf("body = %q holds the wrap text of the error", rec.Body.String())
			}

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, rec.Code, rec.Body.Bytes(),
				errcodetest.Want{Code: tc.wantCode, Args: nil, RequestBody: nil})
		})
	}
}

// checkLog fails unless log holds each of want and none of no.
func checkLog(t *testing.T, log string, want, no []string) {
	t.Helper()

	for _, w := range want {
		if !strings.Contains(log, w) {
			t.Errorf("log = %q, want it to hold %q", log, w)
		}
	}

	for _, n := range no {
		if strings.Contains(log, n) {
			t.Errorf("log = %q, want it not to hold %q", log, n)
		}
	}
}

// TestSharedErrorsKeepTheirAnswer fails for an error of a class that every
// tool-neutral API shares, when the triggers API does not answer it in the
// shared words.
func TestSharedErrorsKeepTheirAnswer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"invalid namespace name", namespace.ErrInvalidName, http.StatusBadRequest, errcode.NamespaceInvalidName},
		{"lost idempotency key", idempotent.Lost(), http.StatusConflict, idempotent.CodeLost},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			triggersapi.WriteError(t.Context(), rec, tc.err)

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, tc.status, rec.Body.Bytes(),
				errcodetest.Want{Code: tc.code, Args: nil, RequestBody: nil})
		})
	}
}

// cycleUnit is a unit in the units field of the answer to a cycle.
type cycleUnit struct {
	ID   string `json:"id"`
	Tool string `json:"tool"`
	Name string `json:"name"`
}

// cycleBody is the units field of the answer to a cycle.
type cycleBody struct {
	Units []cycleUnit `json:"units"`
}

// TestCycleKeepsUnits fails when the answer to a refused save that closes a
// loop loses the units field, or names a unit that the caller cannot read.
func TestCycleKeepsUnits(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	triggersapi.WriteError(t.Context(), rec, fmt.Errorf("save: %w", cycleError()))

	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusConflict, rec.Body.Bytes(),
		errcodetest.Want{Code: errcode.TriggerCycle, Args: nil, RequestBody: nil})

	var body cycleBody

	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	if len(body.Units) != 2 || body.Units[0].Name != "app" || body.Units[0].Tool != "Pulumi" {
		t.Errorf("units = %+v, want the readable unit twice", body.Units)
	}

	if strings.Contains(rec.Body.String(), "secret-net") || strings.Contains(rec.Body.String(), "ws-hidden") {
		t.Errorf("body = %q names a unit that the caller cannot read", rec.Body.String())
	}
}
