package problem_test

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
	"time"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
)

var (
	errDomain = errors.New("domain refusal")
	errDriver = errors.New("pq: password=hunter2 connection refused")
)

// domain answers errDomain with 409, and nothing else.
func domain(err error) (problem.Problem, bool) {
	if errors.Is(err, errDomain) {
		var p problem.Problem

		p.Status, p.Code, p.Message = http.StatusConflict, "domain.refused", "refused"

		return p, true
	}

	return problem.Pass()
}

// write answers err and returns the recorder and the log.
func write(t *testing.T, classify problem.Classifier, err error) (*httptest.ResponseRecorder, string) {
	t.Helper()

	var buf bytes.Buffer

	wr := problem.Writer{Logger: slog.New(slog.NewTextHandler(&buf, nil)), Classify: classify}
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
		{"rate limited", identity.ErrRateLimited, http.StatusTooManyRequests, "auth.rate-limited"},
		{"unauthenticated", identity.ErrUnauthenticated, http.StatusUnauthorized, "auth.unauthenticated"},
		{"csrf", sessionauth.ErrCSRF, http.StatusForbidden, errcode.SessionCSRF},
		{"denied", identity.ErrDenied, http.StatusForbidden, errcode.AccessDenied},
		{
			"fixed",
			problem.Fixed(http.StatusTeapot, "request.teapot", "short and stout"),
			http.StatusTeapot,
			"request.teapot",
		},
		{
			"bad request",
			problem.BadRequest("request.body-invalid", "bad body"),
			http.StatusBadRequest,
			"request.body-invalid",
		},
		{"idempotency lost", idempotency.ErrLost, http.StatusConflict, idempotent.CodeInProgress},
		{"invalid namespace", namespace.ErrInvalidName, http.StatusBadRequest, "namespace.invalid-name"},
		{"no namespace", namespace.ErrNotFound, http.StatusNotFound, "namespace.not-found"},
		{"fenced", namespace.ErrFenced, http.StatusConflict, "namespace.fenced"},
		{"not home", namespace.ErrNotHome, http.StatusMisdirectedRequest, "namespace.not-home"},
		{"maintenance", maintenance.ErrInMaintenance, http.StatusServiceUnavailable, "maintenance.in-maintenance"},
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

			p.Status, p.Message = http.StatusNotFound, "not found"

			return p, true
		}

		return problem.Pass()
	}

	rec, _ := write(t, hide, namespace.ErrNotFound)
	if got := body(t, rec); got.Message != "not found" {
		t.Fatalf("message = %q, want the text of the classifier", got.Message)
	}

	rec, _ = write(t, domain, errDomain)
	if got := body(t, rec); rec.Code != http.StatusConflict || got.ErrorCode != "domain.refused" {
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

			p.Status, p.Code, p.Message, p.RetryAfter = http.StatusTooManyRequests, "x.limited", "later", wait
			p.Args = map[string]any{"limit": 3}

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

		if fmt.Sprint(got.ErrorArgs["limit"]) != "3" {
			t.Fatalf("args %v, want the arguments of the classifier kept", got.ErrorArgs)
		}
	}
}

// TestReauthenticate checks that the path of a new sign-in reaches the body.
func TestReauthenticate(t *testing.T) {
	t.Parallel()

	old := func(error) (problem.Problem, bool) {
		var p problem.Problem

		p.Status, p.Code, p.Message, p.Reauthenticate = http.StatusUnauthorized, "x.old", "too old", "/sign-in"

		return p, true
	}

	rec, _ := write(t, old, errDomain)
	if got := body(t, rec); got.Reauthenticate != "/sign-in" {
		t.Fatalf("reauthenticate = %q, want /sign-in", got.Reauthenticate)
	}
}
