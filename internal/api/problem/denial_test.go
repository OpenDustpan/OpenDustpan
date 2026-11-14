package problem_test

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
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
		args    map[string]any
	}{
		{"bare denial", identity.ErrDenied, errcode.AccessDenied, "forbidden", nil},
		{"wrapped denial", fmt.Errorf("authorize: %w", identity.ErrDenied), errcode.AccessDenied, "forbidden", nil},
		{
			"denial then sentinel",
			fmt.Errorf("%w: %w", identity.ErrDenied, errDomain),
			errcode.AccessDenied,
			"forbidden",
			nil,
		},
		{
			"sentinel then denial",
			fmt.Errorf("%w: %w", errDomain, identity.ErrDenied),
			errcode.AccessDenied,
			"forbidden",
			nil,
		},
		{
			"CSRF keeps the credential answer",
			fmt.Errorf("%w: %w", sessionauth.ErrCSRF, errDomain),
			errcode.SessionCSRF, sessionauth.ErrCSRF.Error(),
			nil,
		},
		{
			"address not allowed",
			fmt.Errorf("%w: %w", identity.ErrDenied, ipallow.ErrAddressNotAllowed),
			errcode.AccessIPNotAllowed, "forbidden",
			nil,
		},
		{
			"typed denial with an address that is not allowed",
			errors.Join(
				&identity.DeniedError{
					Subject: deniedSubject,
					Action:  identity.ActionNamespaceRead,
					Object:  identity.NamespaceObject(deniedNamespace),
				},
				ipallow.ErrAddressNotAllowed,
			),
			errcode.AccessIPNotAllowed, "forbidden",
			nil,
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

			if !reflect.DeepEqual(got.ErrorArgs, tc.args) {
				t.Errorf("errorArgs = %v, want %v", got.ErrorArgs, tc.args)
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

// The names of the object, of the subject and of the namespace of the denial
// of TestDeniedErrorAnswersTheActionAndTheObjectKind. They are distinctive, so
// a body that holds one of them fails the test.
const (
	deniedObject    = "secret-object-7f3"
	deniedSubject   = "person-7f3"
	deniedNamespace = "acme"
)

// TestDeniedErrorAnswersTheActionAndTheObjectKind checks that a denial of the
// typed error answers 403 with access.denied, the missing action and the kind
// of the object, and that the body names no part of the object. A denial
// whose action and kind cannot be in the answer, and a denial that is not the
// typed error, keep the fixed body.
func TestDeniedErrorAnswersTheActionAndTheObjectKind(t *testing.T) {
	t.Parallel()

	unit, err := identity.UnitObject("pulumi", deniedNamespace, deniedObject)
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	tests := []struct {
		name    string
		err     error
		message string
		args    map[string]any
	}{
		{
			name: "a collection",
			err: &identity.DeniedError{
				Subject: deniedSubject,
				Action:  identity.ActionCollectionCreateUnit,
				Object:  identity.CollectionObject(deniedNamespace, deniedObject),
			},
			message: "forbidden: you need the action collection:create-unit on this collection",
			args:    map[string]any{"action": "collection:create-unit", "objectKind": "collection"},
		},
		{
			name: "a wrapped unit",
			err: fmt.Errorf("authorize: %w", &identity.DeniedError{
				Subject: deniedSubject,
				Action:  identity.ActionUnitRead,
				Object:  unit,
			}),
			message: "forbidden: you need the action unit:read on this unit",
			args:    map[string]any{"action": "unit:read", "objectKind": "unit"},
		},
		{
			name: "a namespace",
			err: &identity.DeniedError{
				Subject: deniedSubject,
				Action:  identity.ActionNamespaceRead,
				Object:  identity.NamespaceObject(deniedObject),
			},
			message: "forbidden: you need the action namespace:read on this namespace",
			args:    map[string]any{"action": "namespace:read", "objectKind": "namespace"},
		},
		{
			name: "the installation",
			err: &identity.DeniedError{
				Subject: deniedSubject,
				Action:  identity.ActionServerObserve,
				Object:  identity.ServerObject(),
			},
			message: "forbidden: you need the action server:observe on this server",
			args:    map[string]any{"action": "server:observe", "objectKind": "server"},
		},
		{
			name: "the caller",
			err: &identity.DeniedError{
				Subject: deniedSubject,
				Action:  identity.ActionCallerRead,
				Object:  identity.CallerObject(),
			},
			message: "forbidden: you need the action caller:read on this caller",
			args:    map[string]any{"action": "caller:read", "objectKind": "caller"},
		},
		{
			name: "a principal",
			err: &identity.DeniedError{
				Subject: deniedSubject,
				Action:  identity.ActionPrincipalManageTokens,
				Object:  identity.PrincipalObject(deniedObject),
			},
			message: "forbidden: you need the action principal:manage-tokens on this principal",
			args:    map[string]any{"action": "principal:manage-tokens", "objectKind": "principal"},
		},
		{
			name: "an action that an argument cannot hold",
			err: &identity.DeniedError{
				Subject: deniedSubject,
				Action:  "unit:read\n",
				Object:  unit,
			},
			message: "forbidden",
			args:    nil,
		},
		{
			name: "the zero object",
			err: &identity.DeniedError{
				Subject: deniedSubject,
				Action:  identity.ActionUnitRead,
				Object:  identity.Object{},
			},
			message: "forbidden",
			args:    nil,
		},
		{
			name:    "a plain denial",
			err:     identity.ErrDenied,
			message: "forbidden",
			args:    nil,
		},
		{
			name:    "a wrapped plain denial",
			err:     fmt.Errorf("authorize: %w", identity.ErrDenied),
			message: "forbidden",
			args:    nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkDeniedAnswer(t, tc.err, tc.message, tc.args)
		})
	}
}

// checkDeniedAnswer fails unless err answers 403 with the code access.denied,
// message and args, and a body that names no part of the object of the denial.
func checkDeniedAnswer(t *testing.T, err error, message string, args map[string]any) {
	t.Helper()

	rec := writeWith(t, problem.Writer{Logger: nil, Classify: nil, KeepDenied: nil}, err)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}

	got := body(t, rec)
	if got.Code != http.StatusForbidden || got.ErrorCode != errcode.AccessDenied || got.Message != message {
		t.Errorf("answer = %d %q %q, want %d %q %q",
			got.Code, got.ErrorCode, got.Message, http.StatusForbidden, errcode.AccessDenied, message)
	}

	if !reflect.DeepEqual(got.ErrorArgs, args) {
		t.Errorf("errorArgs = %v, want %v", got.ErrorArgs, args)
	}

	for _, absent := range []string{deniedObject, deniedSubject, deniedNamespace} {
		if strings.Contains(rec.Body.String(), absent) {
			t.Errorf("body holds %q: %s", absent, rec.Body.String())
		}
	}
}
