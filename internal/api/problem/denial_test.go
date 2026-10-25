package problem_test

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
)

var errKept = fmt.Errorf("%w: a class that keeps its own code", identity.ErrDenied)

// hostile answers a denial as a not-found, and errDomain as a conflict. It is
// a classifier that tries to hide a denial.
func hostile(err error) (problem.Problem, bool) {
	if errors.Is(err, identity.ErrDenied) {
		var p problem.Problem

		p.Status, p.Code, p.Message = http.StatusNotFound, errcode.SinkNotFound, "not found"

		return p, true
	}

	return domain(err)
}

// keepKept answers errKept with its own 403 code, and nothing else.
func keepKept(err error) (problem.Problem, bool) {
	if errors.Is(err, errKept) {
		var p problem.Problem

		p.Status, p.Code, p.Message = http.StatusForbidden, errcode.SinkConflict, "kept"

		return p, true
	}

	return problem.Pass()
}

// writeWith answers err with wr and returns the recorder.
func writeWith(t *testing.T, wr problem.Writer, err error) *httptest.ResponseRecorder {
	t.Helper()

	var buf bytes.Buffer

	wr.Logger = slog.New(slog.NewTextHandler(&buf, nil))
	rec := httptest.NewRecorder()

	wr.Write(t.Context(), rec, err)

	return rec
}

// TestDenialComesBeforeTheClassifier checks that an error that wraps
// identity.ErrDenied answers 403 with the denial code, whatever else it wraps,
// and that no classifier sees it. The hostile classifier maps a denial to a
// not-found: the writer must still answer the denial.
func TestDenialComesBeforeTheClassifier(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		err     error
		code    string
		message string
	}{
		{"bare denial", identity.ErrDenied, errcode.AccessDenied, "forbidden"},
		{"wrapped denial", fmt.Errorf("authorize: %w", identity.ErrDenied), errcode.AccessDenied, "forbidden"},
		{
			"denial then sentinel",
			fmt.Errorf("%w: %w", identity.ErrDenied, errDomain),
			errcode.AccessDenied,
			"forbidden",
		},
		{
			"sentinel then denial",
			fmt.Errorf("%w: %w", errDomain, identity.ErrDenied),
			errcode.AccessDenied,
			"forbidden",
		},
		{
			"CSRF keeps the credential answer",
			fmt.Errorf("%w: %w", sessionauth.ErrCSRF, errDomain),
			errcode.SessionCSRF, sessionauth.ErrCSRF.Error(),
		},
		{
			"address not allowed",
			fmt.Errorf("%w: %w", identity.ErrDenied, ipallow.ErrAddressNotAllowed),
			errcode.AccessIPNotAllowed, "forbidden",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var calls atomic.Int32

			counting := func(err error) (problem.Problem, bool) {
				calls.Add(1)

				return hostile(err)
			}

			rec := writeWith(t, problem.Writer{Logger: nil, Classify: counting, KeepDenied: nil}, tc.err)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
			}

			got := body(t, rec)
			if got.ErrorCode != tc.code || got.Message != tc.message {
				t.Errorf("answer = %q %q, want %q %q", got.ErrorCode, got.Message, tc.code, tc.message)
			}

			if n := calls.Load(); n != 0 {
				t.Errorf("classifier called %d times for a denial, want 0", n)
			}
		})
	}
}

// TestClassifierStillAnswersWhatIsNotADenial checks that the denial rule does
// not change an error that does not wrap identity.ErrDenied.
func TestClassifierStillAnswersWhatIsNotADenial(t *testing.T) {
	t.Parallel()

	rec := writeWith(t, problem.Writer{Logger: nil, Classify: hostile, KeepDenied: nil}, errDomain)

	if got := body(t, rec); rec.Code != http.StatusConflict || got.ErrorCode != errcode.SinkConflict {
		t.Fatalf("answer %d %+v, want the domain answer", rec.Code, got)
	}
}

// TestKeepDenied checks the hook for the classes of a denial that keep their
// own code: it answers its own error, an error it does not match is a normal
// denial, and it never runs for an error that does not wrap a denial.
func TestKeepDenied(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		err       error
		status    int
		code      string
		wantCalls int32
	}{
		{"kept class", errKept, http.StatusForbidden, errcode.SinkConflict, 1},
		{"kept class wrapped", fmt.Errorf("save: %w", errKept), http.StatusForbidden, errcode.SinkConflict, 1},
		{
			"other denial",
			fmt.Errorf("authorize: %w", identity.ErrDenied),
			http.StatusForbidden,
			errcode.AccessDenied,
			1,
		},
		{
			"hostile mix",
			fmt.Errorf("%w: %w", identity.ErrDenied, errDomain),
			http.StatusForbidden,
			errcode.AccessDenied,
			1,
		},
		{"not a denial", errDomain, http.StatusConflict, errcode.SinkConflict, 0},
		{"unknown error", errDriver, http.StatusInternalServerError, errcode.ServerInternal, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var calls atomic.Int32

			keep := func(err error) (problem.Problem, bool) {
				calls.Add(1)

				return keepKept(err)
			}

			rec := writeWith(t, problem.Writer{Logger: nil, Classify: domain, KeepDenied: keep}, tc.err)

			if got := body(t, rec); rec.Code != tc.status || got.ErrorCode != tc.code {
				t.Errorf("answer = %d %q, want %d %q", rec.Code, got.ErrorCode, tc.status, tc.code)
			}

			if n := calls.Load(); n != tc.wantCalls {
				t.Errorf("KeepDenied called %d times, want %d", n, tc.wantCalls)
			}
		})
	}
}

// TestKeepDeniedCannotHideACredentialRefusal checks that a hostile KeepDenied, which
// answers every error as a not-found, only runs for a denial and cannot change
// the answer of the credential.
func TestKeepDeniedCannotHideACredentialRefusal(t *testing.T) {
	t.Parallel()

	rec := writeWith(t, problem.Writer{Logger: nil, Classify: nil, KeepDenied: hostile}, sessionauth.ErrOtherSite)

	if got := body(t, rec); rec.Code != http.StatusForbidden || got.ErrorCode != errcode.SessionOtherSite {
		t.Fatalf("answer = %d %q, want the credential answer", rec.Code, got.ErrorCode)
	}
}

// TestKeepDeniedStatusContract checks that a KeepDenied answer keeps its code
// only with status 403 or 404. Any other status is a hostile or wrong hook, and
// the writer answers the standard 403 access.denied instead.
func TestKeepDeniedStatusContract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		want   int
		code   string
	}{
		{"403 is kept", http.StatusForbidden, http.StatusForbidden, errcode.SinkConflict},
		{"404 is kept", http.StatusNotFound, http.StatusNotFound, errcode.SinkConflict},
		{"200 becomes 403", http.StatusOK, http.StatusForbidden, errcode.AccessDenied},
		{"409 becomes 403", http.StatusConflict, http.StatusForbidden, errcode.AccessDenied},
		{"500 becomes 403", http.StatusInternalServerError, http.StatusForbidden, errcode.AccessDenied},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			keep := func(error) (problem.Problem, bool) {
				var p problem.Problem

				p.Status, p.Code, p.Message = tc.status, errcode.SinkConflict, "kept"

				return p, true
			}

			rec := writeWith(t, problem.Writer{Logger: nil, KeepDenied: keep, Classify: nil}, errKept)

			if got := body(t, rec); rec.Code != tc.want || got.ErrorCode != tc.code {
				t.Errorf("answer = %d %q, want %d %q", rec.Code, got.ErrorCode, tc.want, tc.code)
			}

			if rec.Code == http.StatusForbidden && body(t, rec).ErrorCode == errcode.AccessDenied &&
				body(t, rec).Message != "forbidden" {
				t.Errorf("message = %q, want forbidden", body(t, rec).Message)
			}
		})
	}
}
