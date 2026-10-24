package profile_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	apiprofile "opendustpan.org/opendustpan/internal/api/profile"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
)

// errDiskFull stands for an error that no classify function knows.
var errDiskFull = errors.New("disk full")

// TestErrorsHaveCodes proves that each error of the profiles API answers its
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
		{"request from another site", sessionauth.ErrOtherSite, http.StatusForbidden, errcode.SessionOtherSite},
		{"no fresh step", apiprofile.ErrFreshStepRequired, http.StatusForbidden, errcode.MFAFreshStepRequired},
		{"no fresh reveal", profile.ErrFreshRequired, http.StatusForbidden, errcode.MFAFreshStepRequired},
		{
			"old sign-in", apiprofile.ReauthenticateError("/signin"),
			http.StatusUnauthorized, errcode.MFAFreshStepRequired,
		},
		{
			"routing change", &profile.RoutingError{Changes: []string{"backend"}},
			http.StatusForbidden, errcode.ProfileRoutingChangeNeedsOpen,
		},
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
		{"unknown profile", profile.ErrNotFound, http.StatusNotFound, errcode.ProfileNotFound},
		{"schema version", profile.ErrSchemaVersion, http.StatusUnprocessableEntity, errcode.ProfileSchemaVersion},
		{"over a limit", profile.ErrLimit, http.StatusUnprocessableEntity, errcode.ProfileLimit},
		{"invalid profile", profile.ErrInvalid, http.StatusBadRequest, errcode.ProfileInvalid},
		{"changed profile", profile.ErrConflict, http.StatusConflict, errcode.ProfileConflict},
		{
			"changed profile with If-Match", apiprofile.StaleError(profile.ErrConflict),
			http.StatusPreconditionFailed, errcode.RequestPreconditionFailed,
		},
		{
			"wrong If-Match",
			apiprofile.ErrPrecondition,
			http.StatusPreconditionFailed,
			errcode.RequestPreconditionFailed,
		},
		{
			"no If-Match", apiprofile.ErrPreconditionRequired,
			http.StatusPreconditionRequired, errcode.RequestPreconditionRequired,
		},
		{"unknown error", errDiskFull, http.StatusInternalServerError, errcode.ServerInternal},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			apiprofile.WriteError(t.Context(), rec, tc.err)

			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, tc.status, rec.Body.Bytes(),
				errcodetest.Want{Code: tc.code, Args: nil, RequestBody: nil})
		})
	}
}

// TestRequestErrorsHaveCodes proves that the refusals of a request body, a
// query and a header answer 400 with their code of the catalog.
func TestRequestErrorsHaveCodes(t *testing.T) {
	t.Parallel()

	f := newFixtureWithoutKeys(t)

	tests := []struct {
		name string
		req  request
		code string
	}{
		{"body that is no JSON", send("admin", http.MethodPost, base, "{"), errcode.RequestBodyInvalid},
		{"page size of 0", get("admin", base+"?pageSize=0"), errcode.RequestInvalidQuery},
		{"page token", get("admin", base+"?pageToken=x"), errcode.RequestInvalidQuery},
		{
			"key on a server with no keys",
			send("admin", http.MethodPost, base, "{}").withHeader("Idempotency-Key", "k1"),
			errcode.RequestIdempotencyKeyUnsupported,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := f.do(t, tc.req)

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusBadRequest, []byte(got.body),
				errcodetest.Want{Code: tc.code, Args: nil, RequestBody: []byte(tc.req.body)})
		})
	}
}
