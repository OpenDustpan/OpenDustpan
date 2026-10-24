package audit_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	apiaudit "opendustpan.org/opendustpan/internal/api/audit"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// errDiskFull stands for an error that no classify function knows.
var errDiskFull = errors.New("disk full")

// TestErrorsHaveCodes proves that each error of the audit API answers its
// status with a code of the catalog.
func TestErrorsHaveCodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"too many sign-ins", identity.ErrRateLimited, http.StatusTooManyRequests, errcode.AuthRateLimited},
		{"no credential", identity.ErrUnauthenticated, http.StatusUnauthorized, errcode.AuthUnauthenticated},
		{"no CSRF header", sessionauth.ErrCSRF, http.StatusForbidden, errcode.SessionCSRF},
		{"no permission", identity.ErrDenied, http.StatusForbidden, errcode.AccessDenied},
		{"no organization", apiaudit.ErrMissingOrganization, http.StatusBadRequest, errcode.RequestInvalidQuery},
		{"invalid page", apiaudit.ErrInvalidPage, http.StatusBadRequest, errcode.RequestInvalidQuery},
		{"invalid sequence", apiaudit.ErrInvalidSequence, http.StatusBadRequest, errcode.RequestInvalidPath},
		{
			"invalid namespace name", fmt.Errorf("parse: %w", namespace.ErrInvalidName),
			http.StatusBadRequest, errcode.NamespaceInvalidName,
		},
		{"invalid object", identity.ErrInvalidObject, http.StatusBadRequest, errcode.RequestInvalidObject},
		{"unknown namespace", namespace.ErrNotFound, http.StatusNotFound, errcode.NamespaceNotFound},
		{"unknown entry", audit.ErrEntryNotFound, http.StatusNotFound, errcode.AuditEntryNotFound},
		{
			"store with no entry reader", apiaudit.ErrNoEntryReader,
			http.StatusNotImplemented, errcode.AuditEntryReadUnavailable,
		},
		{"unknown error", errDiskFull, http.StatusInternalServerError, errcode.ServerInternal},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			apiaudit.WriteError(t.Context(), rec, tc.err)

			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, tc.status, rec.Body.Bytes(),
				errcodetest.Want{Code: tc.code, Args: nil, RequestBody: nil})
		})
	}
}
