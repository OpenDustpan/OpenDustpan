package server_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// forbiddenHandler builds a Handler whose Authorizer refuses every action with
// err.
func forbiddenHandler(t *testing.T, err error) testHandler {
	t.Helper()

	namespaces, nsErr := namespace.NewService(
		namespace.Config{Store: stubNamespaceStore{}, Now: nil, Installation: "test"},
	)
	if nsErr != nil {
		t.Fatalf("namespace service: %v", nsErr)
	}

	handler, newErr := newCheckedHandler(t, &apiserver.Config{
		Cursors:          testCursors(t),
		Reach:            noReach(),
		LocalUsers:       nil,
		Preferences:      nil,
		GitHostLinks:     nil,
		Settings:         nil,
		GitHost:          nil,
		Idempotency:      nil,
		Workers:          nil,
		InstallationKind: "",
		StateWriteGates:  stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
		Now:              nil,
		TempAdmins:       nil,
		Authenticator:    adminAuthenticator{},
		Authorizer:       errcodetest.Authorizer{Err: err},
		Logger:           slog.New(slog.DiscardHandler),
		Namespaces:       namespaces,
		Backups:          nil,
		Restores:         nil,
		Maintenance:      newFakeMaintenance(maintenance.StateNormal),
		Metrics:          expositionWriter{},
		Version:          "test",
		Schema:           nil,
		Journal:          nil,
		Principals:       nil,
		Access:           nil,
		Tokens:           nil,
		Audit:            newTestAudit(),
		OnAuditFailure:   nil,
		DeletedUnits:     nil,
		AuditChain:       nil,
		AuditSinks:       nil,
		IPAllowlists:     nil,
		Gates:            nil,
		SessionPolicy:    nil,
		SignIn:           nil,
		CheckpointEpochs: nil,
		DenialLimiter:    nil,
		Instances:        nil,
		StoreChecks:      nil,
	})
	if newErr != nil {
		t.Fatalf("handler: %v", newErr)
	}

	return handler
}

// TestForbiddenCodes proves that each cause of a 403 keeps its message and
// status and adds its machine code, on the metrics route.
func TestForbiddenCodes(t *testing.T) {
	t.Parallel()

	for _, cause := range errcodetest.Causes(t) {
		t.Run(cause.Name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			forbiddenHandler(t, cause.Err).ServeHTTP(rec, metricsRequest(t, "admin-token"))

			errcodetest.Check(t, rec.Code, rec.Body.Bytes(), cause)
		})
	}
}

// TestForbiddenRefusalCodes proves that a 403 that wraps identity.ErrDenied but
// is not a missing permission answers with the code of its refusal, not with
// access.denied: access.denied means that the caller needs a grant, and these
// callers do not.
func TestForbiddenRefusalCodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want string
	}{
		{"sign-in refused", errcode.Refusal(errcode.SignInRefused, "sign-in refused"), errcode.SignInRefused},
		{
			"wrapped sign-in refused",
			fmt.Errorf("callback: %w", errcode.Refusal(errcode.SignInRefused, "sign-in refused")),
			errcode.SignInRefused,
		},
		{
			"route with no action",
			errcode.Refusal(errcode.AccessNoAction, "GET /x needs no action"),
			errcode.AccessNoAction,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			forbiddenHandler(t, tc.err).ServeHTTP(rec, metricsRequest(t, "admin-token"))

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
			}

			var got struct {
				ErrorCode string `json:"errorCode"`
			}

			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("answer is not JSON: %v: %s", err, rec.Body.String())
			}

			if got.ErrorCode != tc.want {
				t.Fatalf("errorCode = %q, want %q: %s", got.ErrorCode, tc.want, rec.Body.String())
			}
		})
	}
}

// TestClassifyOrderOfForbidden pins the order in which classify picks the
// message of a 403. Every session error and the allowlist error wrap
// identity.ErrDenied, so the sign-in cases must run before the case of a
// missing permission: a swap turns the specific message into "forbidden".
func TestClassifyOrderOfForbidden(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want string
	}{
		{"csrf alone", sessionauth.ErrCSRF, sessionauth.ErrCSRF.Error()},
		{"other site alone", sessionauth.ErrOtherSite, sessionauth.ErrOtherSite.Error()},
		{"csrf with a permission error", errors.Join(identity.ErrDenied, sessionauth.ErrCSRF), ""},
		{
			"other site with a permission error",
			fmt.Errorf("authorize: %w", errors.Join(identity.ErrDenied, sessionauth.ErrOtherSite)), "",
		},
		{"permission error", identity.ErrDenied, "forbidden"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, message := apiserver.ClassifyForTest(tc.err)

			// The message of a session error is the text of the error itself.
			// "forbidden" would mean that the case of a missing permission ran
			// first.
			want := tc.want
			if want == "" {
				want = tc.err.Error()
			}

			if status != http.StatusForbidden || message != want {
				t.Fatalf("classify = %d %q, want 403 %q", status, message, want)
			}
		})
	}
}
