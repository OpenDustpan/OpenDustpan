package registry_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	apiregistry "opendustpan.org/opendustpan/internal/api/registry"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/internal/registry/modulecheck"
	"opendustpan.org/opendustpan/internal/registry/releasecheck"
	"opendustpan.org/opendustpan/internal/registry/service"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
)

// errDiskFull stands for an error that no classify function knows.
var errDiskFull = errors.New("disk full")

// TestErrorsHaveCodes proves that each error of the registry API answers its
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
		{
			"invalid namespace name", fmt.Errorf("parse: %w", namespace.ErrInvalidName),
			http.StatusBadRequest, errcode.NamespaceInvalidName,
		},
		{"unknown namespace", namespace.ErrNotFound, http.StatusNotFound, errcode.NamespaceNotFound},
		{"fenced write", namespace.ErrFenced, http.StatusConflict, errcode.NamespaceFenced},
		{"other region", namespace.ErrNotHome, http.StatusMisdirectedRequest, errcode.NamespaceNotHome},
		{
			"maintenance mode", maintenance.ErrInMaintenance,
			http.StatusServiceUnavailable, errcode.MaintenanceInMaintenance,
		},
		{"unknown provider", registry.ErrNotFound, http.StatusNotFound, errcode.RegistryNotFound},
		{"unknown key", registry.ErrKeyNotFound, http.StatusNotFound, errcode.RegistryNotFound},
		{"file not staged", service.ErrNotStaged, http.StatusNotFound, errcode.RegistryNotStaged},
		{"file too large", service.ErrTooLarge, http.StatusRequestEntityTooLarge, errcode.RegistryTooLarge},
		{"version exists", registry.ErrVersionExists, http.StatusConflict, errcode.RegistryVersionExists},
		{"key exists", registry.ErrKeyExists, http.StatusConflict, errcode.RegistryKeyExists},
		{
			"bad signature", registry.ErrBadSignature,
			http.StatusUnprocessableEntity, errcode.RegistrySignatureInvalid,
		},
		{
			"untrusted key", registry.ErrUntrustedKey,
			http.StatusUnprocessableEntity, errcode.RegistrySignatureUntrustedKey,
		},
		{
			"archive set refused", releasecheck.ErrRefused,
			http.StatusUnprocessableEntity, errcode.RegistryArchiveSetRefused,
		},
		{
			"module archive refused", modulecheck.ErrRefused,
			http.StatusUnprocessableEntity, errcode.RegistryModuleArchiveRefused,
		},
		{"no keys", service.ErrNoKeys, http.StatusUnprocessableEntity, errcode.RegistryNoKeys},
		{"too many keys", registry.ErrKeyLimit, http.StatusUnprocessableEntity, errcode.RegistryKeyLimit},
		{"bad key", registry.ErrBadKey, http.StatusBadRequest, errcode.RegistryKeyInvalid},
		{"invalid release", registry.ErrInvalid, http.StatusBadRequest, errcode.RegistryInvalid},
		{"bad request", service.ErrBadRequest, http.StatusBadRequest, errcode.RegistryInvalid},
		{"unknown error", errDiskFull, http.StatusInternalServerError, errcode.ServerInternal},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			apiregistry.WriteError(t.Context(), rec, tc.err)

			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, tc.status, rec.Body.Bytes(),
				errcodetest.Want{Code: tc.code, Args: nil, RequestBody: nil})
		})
	}
}

// TestRequestErrorsHaveCodes proves that the refusals of a request body, a
// query and a media type answer their status with their code of the catalog.
func TestRequestErrorsHaveCodes(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	tests := []struct {
		name   string
		req    request
		status int
		code   string
	}{
		{
			"body that is no JSON",
			request{token: "admin", method: http.MethodPost, target: base + "/keys", body: []byte("{"), headers: nil},
			http.StatusBadRequest, errcode.RequestBodyInvalid,
		},
		{
			"page size of 0",
			request{
				token:   "admin",
				method:  http.MethodGet,
				target:  base + "/providers?pageSize=0",
				body:    nil,
				headers: nil,
			},
			http.StatusBadRequest,
			errcode.RequestPageSizeInvalid,
		},
		{
			"page token that is no name",
			request{
				token:   "admin",
				method:  http.MethodGet,
				target:  base + "/providers?pageToken=%00",
				body:    nil,
				headers: nil,
			},
			http.StatusBadRequest,
			errcode.RequestPageTokenInvalid,
		},
		{
			"upload that is no file",
			request{
				token: "publisher", method: http.MethodPost, target: base + "/uploads", body: []byte("x"),
				headers: map[string]string{"Content-Type": "text/plain"},
			},
			http.StatusUnsupportedMediaType, errcode.RequestContentTypeUnsupported,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := f.do(t, tc.req)

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, tc.status, []byte(got.body),
				errcodetest.Want{Code: tc.code, Args: nil, RequestBody: tc.req.body})
		})
	}
}
