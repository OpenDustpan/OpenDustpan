package tfe_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/freshstep"
	"opendustpan.org/opendustpan/pkg/identity"
)

// TestRevealFollowsTheFreshStepPolicy proves that the reveal route asks for a
// fresh authentication only when the policy requires one. With the setting
// off, an access token and a stale session pass. With the setting on, and with
// no policy, they get the refusal.
func TestRevealFollowsTheFreshStepPolicy(t *testing.T) {
	t.Parallel()

	off := freshstep.PolicyFunc(func(context.Context) bool { return false })
	on := freshstep.PolicyFunc(func(context.Context) bool { return true })

	for _, tc := range []struct {
		policy freshstep.Policy
		name   string
		age    time.Duration
		status int
		token  bool
	}{
		{off, "off, access token", 0, http.StatusOK, true},
		{off, "off, stale session", time.Hour, http.StatusOK, false},
		{on, "on, access token", 0, http.StatusForbidden, true},
		{on, "on, stale session", time.Hour, http.StatusUnauthorized, false},
		{on, "on, recent session", time.Minute, http.StatusOK, false},
		{nil, "no policy, access token", 0, http.StatusForbidden, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := revealConfig(
				t, selectiveAuthorizer{deny: map[identity.Action]bool{}}, nil, nil, slog.New(slog.DiscardHandler),
			)
			cfg.Sessions = aliceChecker(t, tc.age)
			handler := newRevealHandlerFrom(t, cfg)
			handler.SetFreshPolicy(tc.policy)

			target := "/api/v2/state-versions/sv-test123/actions/reveal"
			body := revealBody(t, revealResultPath(t))

			req := sessionPost(t, target, body)
			if tc.token {
				req = httptest.NewRequestWithContext(t.Context(), http.MethodPost, target, strings.NewReader(body))
				req.Header.Set("Authorization", "Bearer test-token")
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tc.status {
				t.Fatalf("reveal = %d %s, want %d", rec.Code, rec.Body.String(), tc.status)
			}

			if tc.status == http.StatusForbidden && !strings.Contains(rec.Body.String(), "mfa.fresh-step-required") {
				t.Errorf("body = %s, want code mfa.fresh-step-required", rec.Body.String())
			}
		})
	}
}
