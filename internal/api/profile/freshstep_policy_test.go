package profile_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/freshstep"
)

// TestRevealFollowsTheFreshStepPolicy proves that a reveal asks for a fresh
// authentication only when the policy requires one. With the setting off, an
// access token and a stale session pass. With the setting on, and with no
// policy, they get the refusal.
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

			f := newSessionFixture(t, tc.age)
			f.handler.SetFreshPolicy(tc.policy)

			created := f.create(t, "aws-prod", secretDoc)
			target := base + "/" + created.ID + "/revisions/1?showSecrets=true"

			req := get("admin", target)
			if !tc.token {
				req = req.inSession()
			}

			wantCode(t, "reveal", f.do(t, req), tc.status)
		})
	}
}
