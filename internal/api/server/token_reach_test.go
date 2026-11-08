package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/identity"
)

// TestTokenReachRefusal proves that the route that makes a token of a
// service principal refuses an account administrator whose reach does not
// cover the reach of the principal, with 403 and the code of the reach
// rule, and that it makes a token for a service principal that holds less.
func TestTokenReachRefusal(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	f.seedService(t, "svc-ops")
	f.seedService(t, "svc-plain")
	f.grant(t, "svc-ops", access.RoleInstallationOperator, identity.ServerObject())
	f.grant(t, "svc-plain", access.RoleViewer, identity.ServerObject())

	// ann holds account-administrator on the installation. The session is
	// fresh, so the fresh check of a token change passes.
	rec := f.postWithSessionOf(t, "ann", "/api/server/principals/prn-svc-ops/tokens", `{"name":"ci"}`)

	got := decodeErrorBody(t, rec)

	if rec.Code != http.StatusForbidden || got.ErrorCode != errcode.TokenReachExceeded {
		t.Fatalf("create for an operator service principal = %d %s, want %d with the code %s", rec.Code,
			rec.Body.String(), http.StatusForbidden, errcode.TokenReachExceeded)
	}

	if !strings.Contains(got.Message, "server:backup on the installation") {
		t.Fatalf("message = %q, want the action outside the reach", got.Message)
	}

	rec = f.postWithSessionOf(t, "ann", "/api/server/principals/prn-svc-plain/tokens", `{"name":"ci"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create for a viewer service principal = %d %s, want %d", rec.Code, rec.Body.String(),
			http.StatusCreated)
	}
}

// TestTokenReachClassifier proves that the token classifier of the adapter
// answers the reach refusal with 403 and the code of the rule on its own: a
// caller that classifies a token error directly cannot skip them. A route
// takes the refusal from the server, which answers it before the classifier
// runs.
func TestTokenReachClassifier(t *testing.T) {
	t.Parallel()

	status, message := apiserver.ClassifyForTest(accesstoken.ErrReachExceeded)
	if status != http.StatusForbidden {
		t.Fatalf("classify = %d, want %d", status, http.StatusForbidden)
	}

	if want := accesstoken.ErrReachExceeded.Error(); message != want {
		t.Fatalf("message = %q, want %q", message, want)
	}

	code, mapped := apiserver.TokenCodeForTest(accesstoken.ErrReachExceeded)
	if !mapped || code != errcode.TokenReachExceeded {
		t.Fatalf("code = %q, mapped = %t, want %q", code, mapped, errcode.TokenReachExceeded)
	}
}

// postWithSessionOf sends a POST as a console session of subject with a
// sign-in now, and returns the recorder.
func (f accessFixture) postWithSessionOf(t *testing.T, subject, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	cookie, csrf := f.sessionOf(t, subject, f.sessions.Now())

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(body))
	req.AddCookie(cookie)
	req.Header.Set(apiserver.CSRFHeader, csrf)
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	return rec
}
