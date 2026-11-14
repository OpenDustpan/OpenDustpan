package problem_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/faultlog"
	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

var (
	errDomain = errors.New("domain refusal")
	errDriver = errors.New("pq: password=hunter2 connection refused")
)

// domain answers errDomain with 409, and nothing else.
func domain(err error) (problem.Problem, bool) {
	if errors.Is(err, errDomain) {
		var p problem.Problem

		p.Status, p.Code, p.Message = http.StatusConflict, errcode.SinkConflict, "refused"

		return p, true
	}

	return problem.Pass()
}

// write answers err and returns the recorder and the log.
func write(t *testing.T, classify problem.Classifier, err error) (*httptest.ResponseRecorder, string) {
	t.Helper()

	var buf bytes.Buffer

	wr := problem.Writer{Logger: slog.New(slog.NewTextHandler(&buf, nil)), KeepDenied: nil, Classify: classify}
	rec := httptest.NewRecorder()

	wr.Write(t.Context(), rec, err)

	return rec, buf.String()
}

func body(t *testing.T, rec *httptest.ResponseRecorder) problem.Body {
	t.Helper()

	var b problem.Body
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body, err)
	}

	return b
}

// TestSharedClasses checks the answer of each shared class.
func TestSharedClasses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"rate limited", identity.ErrRateLimited, http.StatusTooManyRequests, errcode.AuthRateLimited},
		{"unauthenticated", identity.ErrUnauthenticated, http.StatusUnauthorized, errcode.AuthUnauthenticated},
		{"csrf", sessionauth.ErrCSRF, http.StatusForbidden, errcode.SessionCSRF},
		{"denied", identity.ErrDenied, http.StatusForbidden, errcode.AccessDenied},
		{
			"fixed",
			problem.Fixed(http.StatusPreconditionFailed, errcode.RequestPreconditionFailed, "short and stout"),
			http.StatusPreconditionFailed,
			errcode.RequestPreconditionFailed,
		},
		{
			"bad request",
			problem.BadRequest(errcode.RequestBodyInvalid, "bad body"),
			http.StatusBadRequest,
			errcode.RequestBodyInvalid,
		},
		{"idempotency lost", idempotency.ErrLost, http.StatusConflict, idempotent.CodeLost},
		{"invalid namespace", namespace.ErrInvalidName, http.StatusBadRequest, errcode.NamespaceInvalidName},
		{"no namespace", namespace.ErrNotFound, http.StatusNotFound, errcode.NamespaceNotFound},
		{"fenced", namespace.ErrFenced, http.StatusConflict, errcode.NamespaceFenced},
		{"not home", namespace.ErrNotHome, http.StatusMisdirectedRequest, errcode.NamespaceNotHome},
		{"maintenance", maintenance.ErrInMaintenance, http.StatusServiceUnavailable, errcode.MaintenanceInMaintenance},
		{
			"binary below the minimum",
			&schema.MinimumError{SchemaMinimum: "v0.5.0", BinaryVersion: "v0.4.0"},
			http.StatusServiceUnavailable,
			errcode.SchemaBinaryBelowMinimum,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec, _ := write(t, nil, fmt.Errorf("wrapped: %w", tc.err))
			got := body(t, rec)

			if rec.Code != tc.status || got.Code != tc.status || got.ErrorCode != tc.code {
				t.Fatalf("answer %d %+v, want %d with code %q", rec.Code, got, tc.status, tc.code)
			}

			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
			}
		})
	}
}

// TestUnknownErrorIsInternal checks that an error that no class knows
// answers 500 with a fixed text, and that the log names the status but never
// the error text.
func TestUnknownErrorIsInternal(t *testing.T) {
	t.Parallel()

	rec, logged := write(t, domain, errDriver)
	got := body(t, rec)

	if rec.Code != http.StatusInternalServerError || got.Message != "internal error" {
		t.Fatalf("answer %d %+v, want 500 internal error", rec.Code, got)
	}

	if strings.Contains(rec.Body.String(), "hunter2") || strings.Contains(logged, "hunter2") {
		t.Fatalf("the answer or the log holds the error text: %q, %q", rec.Body, logged)
	}

	if !strings.Contains(logged, "status=500") {
		t.Fatalf("log %q, want the status", logged)
	}
}

// TestClassifierRunsFirst checks that the classifier of the API answers
// before the shared classes, so it can answer a shared error in its own
// words.
func TestClassifierRunsFirst(t *testing.T) {
	t.Parallel()

	hide := func(err error) (problem.Problem, bool) {
		if errors.Is(err, namespace.ErrNotFound) {
			var p problem.Problem

			p.Status, p.Code, p.Message = http.StatusNotFound, errcode.SinkNotFound, "not found"

			return p, true
		}

		return problem.Pass()
	}

	rec, _ := write(t, hide, namespace.ErrNotFound)
	if got := body(t, rec); got.Message != "not found" {
		t.Fatalf("message = %q, want the text of the classifier", got.Message)
	}

	rec, _ = write(t, domain, errDomain)
	if got := body(t, rec); rec.Code != http.StatusConflict || got.ErrorCode != errcode.SinkConflict {
		t.Fatalf("answer %d %+v, want the domain answer", rec.Code, got)
	}
}

// TestFencedWriteLogsItsWrap checks that a fenced write answers the sentence
// of the sentinel only and logs the wrap that names the write.
func TestFencedWriteLogsItsWrap(t *testing.T) {
	t.Parallel()

	rec, logged := write(t, nil, fmt.Errorf("save view acme/ops: %w", namespace.ErrFenced))

	if got := body(t, rec); got.Message != namespace.ErrFenced.Error() {
		t.Fatalf("message = %q, want the sentence of the sentinel", got.Message)
	}

	if !strings.Contains(logged, "write fenced") || !strings.Contains(logged, "save view acme/ops") {
		t.Fatalf("log %q, want the fenced line with the wrap", logged)
	}
}

// TestRetryAfter checks that a wait goes to the Retry-After header and the
// argument retryAfterSeconds, rounded up to whole seconds and at least 1.
func TestRetryAfter(t *testing.T) {
	t.Parallel()

	cases := map[time.Duration]string{
		time.Millisecond:        "1",
		1500 * time.Millisecond: "2",
		30 * time.Second:        "30",
	}

	for wait, want := range cases {
		limited := func(error) (problem.Problem, bool) {
			var p problem.Problem

			p.Status, p.Code, p.Message, p.RetryAfter = http.StatusTooManyRequests, errcode.SinkTestRateLimited, "later", wait
			p.Values = []errcode.Value{errcode.Quantity("retryAfterSeconds", problem.RetryAfterSeconds(wait))}

			return p, true
		}

		rec, _ := write(t, limited, errDomain)
		got := body(t, rec)

		if rec.Header().Get("Retry-After") != want || fmt.Sprint(got.ErrorArgs["retryAfterSeconds"]) != want {
			t.Fatalf(
				"wait %s: Retry-After %q, args %v; want %s",
				wait,
				rec.Header().Get("Retry-After"),
				got.ErrorArgs,
				want,
			)
		}
	}
}

// TestReauthenticate checks that the path of a new sign-in reaches the body.
func TestReauthenticate(t *testing.T) {
	t.Parallel()

	old := func(error) (problem.Problem, bool) {
		var p problem.Problem

		p.Status, p.Code, p.Message, p.Reauthenticate = http.StatusUnauthorized, errcode.MFAFreshStepRequired, "too old", "/sign-in"

		return p, true
	}

	rec, _ := write(t, old, errDomain)
	if got := body(t, rec); got.Reauthenticate != "/sign-in" {
		t.Fatalf("reauthenticate = %q, want /sign-in", got.Reauthenticate)
	}
}

// TestCredentialRefusalsComeFirst checks that a classifier that claims every
// error still cannot answer a refusal of the credential, while it can answer
// a refusal that wraps identity.ErrDenied either.
func TestCredentialRefusalsComeFirst(t *testing.T) {
	t.Parallel()

	greedy := func(error) (problem.Problem, bool) {
		var p problem.Problem

		p.Status, p.Code, p.Message = http.StatusTeapot, errcode.SinkConflict, "mine"

		return p, true
	}

	for _, err := range []error{identity.ErrRateLimited, identity.ErrUnauthenticated, sessionauth.ErrCSRF} {
		rec, _ := write(t, greedy, err)
		if rec.Code == http.StatusTeapot {
			t.Fatalf("%v: the classifier answered a credential refusal", err)
		}
	}

	rec, _ := write(t, greedy, identity.ErrDenied)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("denied: answer %d, want the denial", rec.Code)
	}
}

// TestRefusedCodeIsInternal checks that a code the catalog does not have, and
// a code with a missing argument, answer 500 server.internal, and that the log
// names the code and never the message.
func TestRefusedCodeIsInternal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		code string
	}{
		{"unknown code", "x.not-in-the-catalog"},
		{"missing argument", errcode.SinkTestRateLimited},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			bad := func(error) (problem.Problem, bool) {
				var p problem.Problem

				p.Status, p.Code, p.Message = http.StatusConflict, tc.code, "secret hunter2"

				return p, true
			}

			rec, logged := write(t, bad, errDomain)
			got := body(t, rec)

			if rec.Code != http.StatusInternalServerError || got.ErrorCode != errcode.ServerInternal ||
				got.Message != "internal error" {
				t.Fatalf("answer %d %+v, want 500 %s", rec.Code, got, errcode.ServerInternal)
			}

			if !strings.Contains(logged, tc.code) || strings.Contains(logged, "hunter2") {
				t.Fatalf("log %q, want the code and not the message", logged)
			}
		})
	}
}

// TestInternalHasCode checks that the 500 of an error that no class knows has
// the code server.internal.
func TestInternalHasCode(t *testing.T) {
	t.Parallel()

	rec, _ := write(t, nil, errDriver)
	if got := body(t, rec); got.ErrorCode != errcode.ServerInternal {
		t.Fatalf("code = %q, want %q", got.ErrorCode, errcode.ServerInternal)
	}
}

// TestEveryAnswerHasACode checks that every shared class and the 500 answer
// with a code of the catalog.
func TestEveryAnswerHasACode(t *testing.T) {
	t.Parallel()

	errs := []error{
		identity.ErrRateLimited, identity.ErrUnauthenticated, sessionauth.ErrCSRF, sessionauth.ErrOtherSite,
		identity.ErrDenied, idempotency.ErrLost, namespace.ErrInvalidName, namespace.ErrNotFound,
		namespace.ErrFenced, namespace.ErrNotHome, maintenance.ErrInMaintenance, errDriver,
	}

	for _, err := range errs {
		p := problem.Writer{Logger: nil, KeepDenied: nil, Classify: nil}.Problem(err)
		if _, ok := errcode.Lookup(p.Code); !ok {
			t.Errorf("%v: code %q is not in the catalog", err, p.Code)
		}
	}

	if got := problem.Internal().Code; got != errcode.ServerInternal {
		t.Errorf("internal code = %q, want %q", got, errcode.ServerInternal)
	}
}

// TestWriteProblem checks that a route that builds its own answer gets the
// same headers, body and log rules as an answer of Write.
func TestWriteProblem(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	wr := problem.Writer{Logger: slog.New(slog.NewTextHandler(&buf, nil)), KeepDenied: nil, Classify: nil}
	rec := httptest.NewRecorder()

	var p problem.Problem

	p.Status, p.Code, p.Message = http.StatusNotFound, errcode.SinkNotFound, "gone"

	wr.WriteProblem(t.Context(), rec, p)

	got := body(t, rec)
	if rec.Code != http.StatusNotFound || got.ErrorCode != errcode.SinkNotFound ||
		rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("answer %d %+v, header %v", rec.Code, got, rec.Header())
	}

	rec = httptest.NewRecorder()
	p.Status = http.StatusInternalServerError

	wr.WriteProblem(t.Context(), rec, p)

	if !strings.Contains(buf.String(), "status=500") {
		t.Fatalf("log %q, want the 500 status", buf.String())
	}
}

// TestCredentialAnswersTheSentinel checks that a refusal of the session cookie
// answers the text of its sentinel, never the wrap text that names the
// operation.
func TestCredentialAnswersTheSentinel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		sentinel error
		code     string
	}{
		{"csrf", sessionauth.ErrCSRF, sessionauth.ErrCSRF, errcode.SessionCSRF},
		{"other site", sessionauth.ErrOtherSite, sessionauth.ErrOtherSite, errcode.SessionOtherSite},
		{
			"csrf wrapped", fmt.Errorf("save trigger: %w", sessionauth.ErrCSRF),
			sessionauth.ErrCSRF, errcode.SessionCSRF,
		},
		{
			"other site wrapped", fmt.Errorf("save trigger: %w", sessionauth.ErrOtherSite),
			sessionauth.ErrOtherSite, errcode.SessionOtherSite,
		},
		{
			"csrf wrapped twice", fmt.Errorf("drift: %w", fmt.Errorf("save: %w", sessionauth.ErrCSRF)),
			sessionauth.ErrCSRF, errcode.SessionCSRF,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec, _ := write(t, nil, tc.err)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
			}

			b := body(t, rec)
			if b.Message != tc.sentinel.Error() {
				t.Errorf("message = %q, want the sentinel text %q", b.Message, tc.sentinel.Error())
			}

			if b.ErrorCode != tc.code {
				t.Errorf("code = %q, want %q", b.ErrorCode, tc.code)
			}
		})
	}
}

// TestCredentialRefusalLogsNoWrap checks that the log holds the status and the
// code of a refusal of the session cookie at Debug, and not the wrap text, and
// that the answer does not hold the wrap text.
func TestCredentialRefusalLogsNoWrap(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	wr := problem.Writer{
		Logger:     slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
		KeepDenied: nil,
		Classify:   nil,
	}
	rec := httptest.NewRecorder()

	wr.Write(t.Context(), rec, fmt.Errorf("save trigger: %w", sessionauth.ErrCSRF))

	logs := buf.String()
	for _, want := range []string{"level=DEBUG", `msg="request refused"`} {
		if !strings.Contains(logs, want) {
			t.Errorf("log %q does not hold %q", logs, want)
		}
	}

	if strings.Contains(logs, "save trigger") {
		t.Errorf("log %q holds the wrap text", logs)
	}

	if strings.Contains(rec.Body.String(), "save trigger") {
		t.Errorf("answer %q holds the wrap text", rec.Body.String())
	}
}

// TestCredentialBeatsAHostileClassifier checks that a refusal of the session
// cookie keeps its answer when the error also matches a classifier of the API.
// Defense in depth: the credential check runs before the classifier; this test
// holds even if that order changes.
func TestCredentialBeatsAHostileClassifier(t *testing.T) {
	t.Parallel()

	rec, _ := write(t, domain, fmt.Errorf("%w: %w", sessionauth.ErrCSRF, errDomain))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}

	if got := body(t, rec).Message; got != sessionauth.ErrCSRF.Error() {
		t.Errorf("message = %q, want the sentinel text %q", got, sessionauth.ErrCSRF.Error())
	}
}

// TestWriteLogsRefusalsAtDebug pins the log rule of Write for each class. A
// refusal whose answer is not its error text logs its status and code at Debug,
// never the error text. An answer that holds the error text logs nothing more.
// No log line holds the value that an error wraps, and the body never holds it.
func TestWriteLogsRefusalsAtDebug(t *testing.T) {
	t.Parallel()

	tests := []refusalCase{
		{
			name:        "wrapped denial logs its wrap",
			err:         fmt.Errorf("save trigger: %w", identity.ErrDenied),
			status:      http.StatusForbidden,
			wantRefused: true,
			wantLog:     "status=403",
			hidden:      "save trigger",
		},
		{
			name:        "answer holds the error text",
			err:         namespace.ErrInvalidName,
			status:      http.StatusBadRequest,
			wantRefused: false,
			wantLog:     "",
			hidden:      "",
		},
		{
			name:        "5xx writes no error text",
			err:         errDriver,
			status:      http.StatusInternalServerError,
			wantRefused: false,
			wantLog:     "",
			hidden:      "hunter2",
		},
		{
			name:        "wrapped secret never reaches the body or the log",
			err:         fmt.Errorf("pq: password=hunter2: %w", identity.ErrDenied),
			status:      http.StatusForbidden,
			wantRefused: true,
			wantLog:     "",
			hidden:      "hunter2",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			handler := &recordHandler{
				text: slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}),
				recs: nil,
			}

			wr := problem.Writer{
				Logger:     slog.New(handler),
				KeepDenied: nil,
				Classify:   nil,
			}
			rec := httptest.NewRecorder()

			wr.Write(t.Context(), rec, tc.err)

			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}

			checkRefusalLog(t, tc, buf.String(), handler.recs, rec.Body.String())
		})
	}
}

// refusalCase is one row of TestWriteLogsRefusalsAtDebug.
type refusalCase struct {
	name        string
	err         error
	status      int
	wantRefused bool
	// wantLog is the text that the request refused line holds when wantRefused is true, or "".
	wantLog string
	// hidden is a value that the body never holds, and that a 5xx log never holds.
	hidden string
}

// refusedLineHolds reports whether the request refused line of logs holds text.
func refusedLineHolds(logs, text string) bool {
	for line := range strings.SplitSeq(logs, "\n") {
		if strings.Contains(line, `msg="request refused"`) {
			return strings.Contains(line, text)
		}
	}

	return false
}

// checkRefusalLog fails unless the log and the body follow the rule of
// TestWriteLogsRefusalsAtDebug.
func checkRefusalLog(t *testing.T, tc refusalCase, logs string, recs []logRecord, body string) {
	t.Helper()

	if got := strings.Contains(logs, `msg="request refused"`); got != tc.wantRefused {
		t.Errorf("request refused line = %t, want %t: log %q", got, tc.wantRefused, logs)
	}

	if tc.wantRefused && !strings.Contains(logs, "level=DEBUG") {
		t.Errorf("request refused line is not at Debug: log %q", logs)
	}

	if tc.wantRefused && tc.wantLog != "" && !refusedLineHolds(logs, tc.wantLog) {
		t.Errorf("request refused line does not hold %q: log %q", tc.wantLog, logs)
	}

	if tc.hidden != "" && strings.Contains(body, tc.hidden) {
		t.Errorf("body %q holds %q", body, tc.hidden)
	}

	// Defense in depth: the refusal line names the status and the code only,
	// and the allowlist of a 5xx line checks the names of its fields. This
	// check stays for the values, so that a new log field or a secret in an
	// allowed field cannot leak the wrapped value.
	if tc.hidden != "" && strings.Contains(logs, tc.hidden) {
		t.Errorf("log %q holds %q", logs, tc.hidden)
	}

	if tc.status >= http.StatusInternalServerError {
		checkFailureLog(t, tc, recs)
	}
}

// recordHandler writes the text of each record and keeps its message and
// fields, so a test can check the fields of a 5xx log line.
type recordHandler struct {
	text slog.Handler
	recs []logRecord
}

// logRecord is one record that a recordHandler kept.
type logRecord struct {
	message string
	fields  map[string]string
}

// Enabled answers as the text handler does, so the buffer holds the same text.
func (h *recordHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.text.Enabled(ctx, level)
}

// Handle keeps the message and the fields of the record and writes its text.
func (h *recordHandler) Handle(ctx context.Context, r slog.Record) error {
	fields := make(map[string]string, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		fields[a.Key] = a.Value.String()

		return true
	})

	h.recs = append(h.recs, logRecord{message: r.Message, fields: fields})

	if err := h.text.Handle(ctx, r); err != nil {
		return fmt.Errorf("write the log text: %w", err)
	}

	return nil
}

// WithAttrs returns the same handler: the writer adds its fields to the record.
func (h *recordHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

// WithGroup returns the same handler: the writer logs no group.
func (h *recordHandler) WithGroup(string) slog.Handler { return h }

// checkFailureLog fails unless the 5xx log line holds the status of the answer
// and the cause of the error, and no field outside the fields of a failure
// log: the status, the cause, the request ID, the route, the op and the error
// code. faultlog checks each value against a closed list or a fixed form, so
// the line holds no error text.
func checkFailureLog(t *testing.T, tc refusalCase, recs []logRecord) {
	t.Helper()

	var found bool

	for _, r := range recs {
		if r.message != "request failed" {
			continue
		}

		found = true

		for key := range r.fields {
			if !failureField(key) {
				t.Errorf("5xx log holds the field %q", key)
			}
		}

		if got := r.fields["status"]; got != strconv.Itoa(tc.status) {
			t.Errorf("5xx log status = %q, want %d", got, tc.status)
		}

		if want := faultlog.Cause(tc.err); r.fields["cause"] != want {
			t.Errorf("5xx log cause = %q, want %q", r.fields["cause"], want)
		}
	}

	if !found {
		t.Errorf("no 5xx log line in %v", recs)
	}
}

// failureField reports whether key is one of the fields that a failure log
// line may hold.
func failureField(key string) bool {
	switch key {
	case "status", "cause", "request_id", "route", "op", "error_code":
		return true
	default:
		return false
	}
}
