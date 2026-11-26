package pulumi_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/freshstep"
	"opendustpan.org/opendustpan/pkg/session"
)

// tokenRevealRequest returns a reveal request with an access token.
func tokenRevealRequest(t *testing.T, body string) *http.Request {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, revealTarget, strings.NewReader(body))
	req.Header.Set("Authorization", "token test-token")

	return req
}

// cookieRevealRequest returns a reveal request with a console session cookie.
func cookieRevealRequest(t *testing.T, body string) *http.Request {
	t.Helper()

	sessionSecret, err := session.NewSecret()
	if err != nil {
		t.Fatalf("secret: %v", err)
	}

	return sessionPost(t, revealTarget, sessionSecret, body)
}

// TestRevealFollowsTheFreshStepPolicy proves that the reveal route asks for a
// fresh authentication only when the policy requires one. With the setting
// off, an access token and a stale sign-in pass. With the setting on, and with
// no policy, they get the refusal.
func TestRevealFollowsTheFreshStepPolicy(t *testing.T) {
	t.Parallel()

	off := freshstep.PolicyFunc(func(context.Context) bool { return false })
	on := freshstep.PolicyFunc(func(context.Context) bool { return true })

	for _, tc := range []struct {
		policy     freshstep.Policy
		request    func(t *testing.T, body string) *http.Request
		name       string
		age        time.Duration
		wantStatus int
	}{
		{name: "off, access token", policy: off, request: tokenRevealRequest, age: 0, wantStatus: http.StatusOK},
		{
			name: "off, stale sign-in", policy: off, request: cookieRevealRequest, age: time.Hour,
			wantStatus: http.StatusOK,
		},
		{name: "on, access token", policy: on, request: tokenRevealRequest, age: 0, wantStatus: http.StatusForbidden},
		{
			name: "on, stale sign-in", policy: on, request: cookieRevealRequest, age: time.Hour,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "on, recent sign-in", policy: on, request: cookieRevealRequest, age: time.Minute,
			wantStatus: http.StatusOK,
		},
		{
			name: "no policy, access token", policy: nil, request: tokenRevealRequest, age: 0,
			wantStatus: http.StatusForbidden,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newRevealFixtureWithSessions(
				t, allowAll(), fixtureAudit{store: nil, onFail: nil, logger: slog.New(slog.DiscardHandler)},
				aliceCheckerSignedIn(t, tc.age),
			)
			f.handler.SetFreshPolicy(tc.policy)

			_, path := seedRevealVersion(t, f, "service")

			rec := serve(f.handler, tc.request(t, revealBody(t, path)))
			if rec.Code != tc.wantStatus {
				t.Fatalf("reveal = %d %s, want %d", rec.Code, rec.Body.String(), tc.wantStatus)
			}

			if tc.wantStatus == http.StatusForbidden && !strings.Contains(rec.Body.String(), freshCode) {
				t.Errorf("body = %s, want errorCode %s", rec.Body.String(), freshCode)
			}
		})
	}
}
