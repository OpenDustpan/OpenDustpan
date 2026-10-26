package server_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
)

// wrapText is in the wrap of each cause below. No answer may hold it.
const wrapText = "secret-wrap-text-acme"

var (
	errSignInRefused = errcode.Refusal(errcode.SignInRefused, "sign-in refused")
	errWrapText      = errors.New(wrapText)
	errDriverDetail  = errors.New("driver detail")
)

// deniedAround returns an error that wraps identity.ErrDenied and cause, as
// the access-limit authorizer wraps the error of its check.
func deniedAround(cause error) error {
	return fmt.Errorf("%s: %w: %w", wrapText, identity.ErrDenied, cause)
}

func writeErrorRecorder(t *testing.T, err error) (*httptest.ResponseRecorder, string) {
	t.Helper()

	rec, body := writeErrorBody(t, err)

	return rec, body.Message
}

type denialBody struct {
	Message   string `json:"message"`
	Code      int    `json:"code"`
	ErrorCode string `json:"errorCode"`
}

func writeErrorBody(t *testing.T, err error) (*httptest.ResponseRecorder, denialBody) {
	t.Helper()

	rec := httptest.NewRecorder()
	apiserver.WriteErrorForTest(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil), err)

	var body denialBody

	if decodeErr := json.Unmarshal(rec.Body.Bytes(), &body); decodeErr != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), decodeErr)
	}

	return rec, body
}

// TestWriteErrorDenialComesFirst checks that an error that wraps
// identity.ErrDenied answers 403 with a fixed text, for the sentinel of each
// classify function. The denial wraps the sentinel, as the access-limit
// authorizer wraps the error of its check.
func TestWriteErrorDenialComesFirst(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		cause error
	}{
		{"maintenance", maintenance.ErrInMaintenance},
		{"maintenance active work", maintenance.ErrActiveWork},
		{"audit chain", audit.ErrChainMigrationsStopped},
		{"namespace not found", namespace.ErrNotFound},
		{"namespace fenced", namespace.ErrFenced},
		{"namespace exists", namespace.ErrAlreadyExists},
		{"backup", backup.ErrNotFound},
		{"backup target", backup.ErrDevBackupRefused},
		{"principal", principal.ErrNotFound},
		{"role or grant", access.ErrNotFound},
		{"token", accesstoken.ErrNotFound},
		{"worker", apiserver.ErrNoWorker},
		{"ip allowlist", ipallow.ErrDisabled},
		{"gate config", gate.ErrNotFound},
		{"session policy", session.ErrInvalid},
		{"provider", session.ErrProviderNotFound},
		{"git host", forge.ErrInstallationNotAssigned},
		{"request", identity.ErrInvalidObject},
		{"request body", apiserver.ErrInvalidBodyForTest},
		{"registry", backup.ErrRestoreNotFound},
		// Hostile: a denial around a not-found, around a conflict and around
		// an unknown error.
		{"hostile not found", fmt.Errorf("%w: %w", namespace.ErrNotFound, errWrapText)},
		{"hostile unknown", errDriverDetail},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec, body := writeErrorBody(t, deniedAround(tc.cause))
			message := body.Message

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusForbidden, rec.Body.String())
			}

			if body.ErrorCode != errcode.AccessDenied {
				t.Errorf("errorCode = %q, want %q", body.ErrorCode, errcode.AccessDenied)
			}

			if strings.Contains(rec.Body.String(), wrapText) {
				t.Errorf("body holds the wrap text: %s", rec.Body.String())
			}

			if message != "forbidden" {
				t.Errorf("message = %q, want %q", message, "forbidden")
			}
		})
	}
}

// TestWriteErrorKeepDenied checks the classes of a denial that keep their own
// sentence: the status is 403 and the text is the sentence of the sentinel,
// with no wrap text.
func TestWriteErrorKeepDenied(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want string
	}{
		{"escalation", deniedAround(access.ErrEscalation), access.ErrEscalation.Error()},
		{
			"sign-in refused",
			fmt.Errorf("%s: %w", wrapText, apiserver.SignInRefusalForTest("no principal")),
			errSignInRefused.Error() + ": no principal",
		},
		{
			"escalation around a not-found",
			fmt.Errorf("%w: %w: %w", identity.ErrDenied, namespace.ErrNotFound, access.ErrEscalation),
			access.ErrEscalation.Error(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec, message := writeErrorRecorder(t, tc.err)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
			}

			if message != tc.want {
				t.Errorf("message = %q, want %q", message, tc.want)
			}

			if strings.Contains(rec.Body.String(), wrapText) {
				t.Errorf("body holds the wrap text: %s", rec.Body.String())
			}
		})
	}
}

// TestWriteErrorNoStore checks that each error answer carries
// Cache-Control: no-store, for the classes of the answers: a denial, a coded
// error, a classified error and an error that nothing knows.
func TestWriteErrorNoStore(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want int
	}{
		{"denial", deniedAround(namespace.ErrNotFound), http.StatusForbidden},
		{"unauthenticated", identity.ErrUnauthenticated, http.StatusUnauthorized},
		{"not found", namespace.ErrNotFound, http.StatusNotFound},
		{"conflict", namespace.ErrFenced, http.StatusConflict},
		{"maintenance", maintenance.ErrInMaintenance, http.StatusServiceUnavailable},
		{"unknown", errDriverDetail, http.StatusInternalServerError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec, _ := writeErrorRecorder(t, tc.err)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}

			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want %q", got, "no-store")
			}
		})
	}
}

// TestWriteErrorCredentialBeforeDenial checks that a refusal of the credential
// of a request keeps its status, as in problem.Writer, whatever else the error
// wraps.
func TestWriteErrorCredentialBeforeDenial(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		cause error
		want  int
	}{
		{"csrf", sessionauth.ErrCSRF, http.StatusForbidden},
		{"other site", sessionauth.ErrOtherSite, http.StatusForbidden},
		{"unauthenticated", identity.ErrUnauthenticated, http.StatusUnauthorized},
		{"rate limited", identity.ErrRateLimited, http.StatusTooManyRequests},
		{
			"unauthenticated around a not-found",
			fmt.Errorf("%w: %w", identity.ErrUnauthenticated, namespace.ErrNotFound), http.StatusUnauthorized,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec, _ := writeErrorRecorder(t, deniedAround(tc.cause))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tc.want, rec.Body.String())
			}

			if strings.Contains(rec.Body.String(), wrapText) {
				t.Errorf("body holds the wrap text: %s", rec.Body.String())
			}
		})
	}
}

// TestWriteEnterErrorDenialComesFirst checks that the answer of a failed enter
// of maintenance mode answers a denial as 403 with no list of canceled work,
// and that the answer has no-store.
func TestWriteEnterErrorDenialComesFirst(t *testing.T) {
	t.Parallel()

	active := &maintenance.ActiveWorkError{
		Work: []string{"pulumi/acme/web/prod"}, Canceled: []string{"opentofu/acme/net"},
	}

	rec := httptest.NewRecorder()
	apiserver.WriteEnterErrorForTest(rec,
		httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", http.NoBody), deniedAround(active))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}

	for _, leak := range []string{wrapText, "opentofu/acme/net", "canceled"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Errorf("body holds %q: %s", leak, rec.Body.String())
		}
	}

	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

// TestWriteErrorKeepDeniedCannotHideACredentialRefusal checks that an error
// that wraps a kept class and a refusal of the credential at once answers the
// refusal of the credential, so the keep list cannot hide it.
func TestWriteErrorKeepDeniedCannotHideACredentialRefusal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want int
	}{
		{
			"escalation and unauthenticated",
			fmt.Errorf("%w: %w: %w", identity.ErrDenied, access.ErrEscalation, identity.ErrUnauthenticated),
			http.StatusUnauthorized,
		},
		{
			"sign-in refused and rate limited",
			fmt.Errorf("%w: %w", errSignInRefused, identity.ErrRateLimited),
			http.StatusTooManyRequests,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec, _ := writeErrorRecorder(t, tc.err)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

// TestWriteErrorNamespaceNotFoundFixedText checks that a not-found namespace
// answers the fixed text and never the wrap text of the error.
func TestWriteErrorNamespaceNotFoundFixedText(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("%s: %w", wrapText, namespace.ErrNotFound)

	rec, body := writeErrorBody(t, err)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}

	if body.ErrorCode != errcode.NamespaceNotFound {
		t.Errorf("errorCode = %q, want %q", body.ErrorCode, errcode.NamespaceNotFound)
	}

	if body.Message != "namespace not found" {
		t.Errorf("message = %q, want %q", body.Message, "namespace not found")
	}

	if strings.Contains(rec.Body.String(), wrapText) {
		t.Errorf("body holds the wrap text: %s", rec.Body.String())
	}
}
