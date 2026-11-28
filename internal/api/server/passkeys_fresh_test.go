package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/pkg/freshstep"
	"opendustpan.org/opendustpan/pkg/session"
)

// factorChange is one change of a passkey or a local-user factor that needs a
// fresh authentication when the setting freshAuthEnabled is on.
type factorChange struct {
	name   string
	method string
	target string
	body   string
}

// factorChanges returns the passkey and local-user factor changes. carol is the
// local user that the reset names.
func factorChanges(carol string) []factorChange {
	return []factorChange{
		{name: "passkey begin", method: http.MethodPost, target: beginTarget, body: "{}"},
		{name: "passkey finish", method: http.MethodPost, target: finishTarget, body: "{}"},
		{name: "passkey removal", method: http.MethodDelete, target: ownKeysTarget + "/pky-nobody", body: ""},
		{
			name: "local-user reset", method: http.MethodPost, target: "/api/dustpan/v1/local-users/reset",
			body: `{"principal":"` + carol + `","reason":"lost the password"}`,
		},
	}
}

// newFactorFixture returns a handler whose policy reads the setting
// freshAuthEnabled as off: the policy asks for no fresh step. The policy is
// set after the handler is built, as the server does before it serves a
// request.
func newFactorFixture(t *testing.T) accessFixture {
	t.Helper()

	f := newAccessFixture(t)
	f.handler.SetFreshPolicy(freshstep.PolicyFunc(func(context.Context) bool { return false }))

	return f
}

// sessionAt sends one factor change in a console session of subject whose
// sign-in is at authTime.
func (f accessFixture) sessionAt(
	t *testing.T, subject string, authTime time.Time, c factorChange,
) *httptest.ResponseRecorder {
	t.Helper()

	cookie, csrf := f.sessionOf(t, subject, authTime)

	req := httptest.NewRequestWithContext(t.Context(), c.method, c.target, strings.NewReader(c.body))
	req.AddCookie(cookie)
	req.Header.Set(apiserver.CSRFHeader, csrf)
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	return rec
}

// wantFreshRefusal fails unless rec is 403 with the errorCode of a missing
// fresh step. It does not stop the test on a body that is not an error.
func wantFreshRefusal(t *testing.T, who string, rec *httptest.ResponseRecorder) {
	t.Helper()

	var got struct {
		ErrorCode string `json:"errorCode"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode the error body of %s: %v", who, err)
	}

	if rec.Code != http.StatusForbidden || got.ErrorCode != freshErrorCode {
		t.Errorf("%s = %d %s, want 403 with errorCode %s", who, rec.Code, rec.Body.String(), freshErrorCode)
	}
}

// TestFactorChangeNeedsFreshStepSettingOff checks that with the setting
// freshAuthEnabled off, a passkey begin, finish and removal and a local-user
// reset still get 403 mfa.fresh-step-required, for an access token and for a
// console session whose sign-in is older than the token freshness window. The
// fresh step of these factor changes does not follow the setting.
func TestFactorChangeNeedsFreshStepSettingOff(t *testing.T) {
	t.Parallel()

	f := newFactorFixture(t)
	carol := createRouteLocalUser(t, f)
	stale := f.sessions.Now().Add(-(session.DefaultTokenFreshness + time.Minute))

	for _, c := range factorChanges(carol) {
		wantFreshRefusal(t, c.name+" with an access token",
			f.asToken(t, "admin", c.method, c.target, []byte(c.body)))

		wantFreshRefusal(t, c.name+" with a session with an old sign-in",
			f.sessionAt(t, "admin", stale, c))
	}
}
