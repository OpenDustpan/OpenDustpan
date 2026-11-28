package issuer_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/freshstep"
)

// TestFreshStepPolicyDecidesTheFreshSignIn proves that a change that reduces
// security asks for a fresh sign-in only when the policy requires one. With
// the setting off, an access token and a stale session pass. With the setting
// on, and with no policy, they get the refusal.
func TestFreshStepPolicyDecidesTheFreshSignIn(t *testing.T) {
	t.Parallel()

	const change = `{"issuerURL": "https://files.example.com/acme"}`

	off := freshstep.PolicyFunc(func(context.Context) bool { return false })
	on := freshstep.PolicyFunc(func(context.Context) bool { return true })

	for _, tc := range []struct {
		policy freshstep.Policy
		name   string
		code   string
		age    time.Duration
		status int
		token  bool
	}{
		{off, "off, access token", "", 0, http.StatusOK, true},
		{off, "off, stale session", "", time.Hour, http.StatusOK, false},
		{on, "on, access token", errcode.MFAFreshStepRequired, 0, http.StatusForbidden, true},
		{on, "on, stale session", errcode.MFAFreshStepRequired, time.Hour, http.StatusUnauthorized, false},
		{on, "on, recent session", "", time.Minute, http.StatusOK, false},
		{nil, "no policy, access token", errcode.MFAFreshStepRequired, 0, http.StatusForbidden, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := buildFixture(t, defaults().session(tc.age))
			f.handler.SetFreshPolicy(tc.policy)

			req := patch(change).withHeader("If-Match", `"issuer.0"`)
			if !tc.token {
				req = req.inSession()
			}

			got := f.do(t, req)
			wantCode(t, "patch", got, tc.status)

			if tc.code != "" && decode(t, got).Code != tc.code {
				t.Errorf("code = %q, want %s", decode(t, got).Code, tc.code)
			}
		})
	}
}
