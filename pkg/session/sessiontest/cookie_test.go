package sessiontest_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/session/sessiontest"
)

// TestCookieSuites runs the suites of the cookie ports with no adapter, so
// that each case shows as skipped until an adapter runs them.
func TestCookieSuites(t *testing.T) {
	t.Parallel()

	t.Run("Sealer", func(t *testing.T) {
		t.Parallel()
		sessiontest.RunSealer(t, nil)
	})
	t.Run("StateSealer", func(t *testing.T) {
		t.Parallel()
		sessiontest.RunStateSealer(t, nil)
	})
	t.Run("KeyStore", func(t *testing.T) {
		t.Parallel()
		sessiontest.RunKeyStore(t, nil)
	})
	t.Run("PrincipalStateStore", func(t *testing.T) {
		t.Parallel()
		sessiontest.RunPrincipalStateStore(t, nil)
	})
	t.Run("ProofVerifier", func(t *testing.T) {
		t.Parallel()
		sessiontest.RunProofVerifier(t, nil)
	})
	t.Run("CookieSessions", func(t *testing.T) {
		t.Parallel()
		sessiontest.RunCookieSessions(t, nil)
	})
	t.Run("NoSessionAtRest", func(t *testing.T) {
		t.Parallel()
		sessiontest.RunNoSessionAtRest(t, nil)
	})
}
