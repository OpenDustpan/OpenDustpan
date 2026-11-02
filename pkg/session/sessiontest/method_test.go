package sessiontest_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/session/sessiontest"
)

// TestMethodSuites runs the suites of the sign-in method ports with no
// adapter, so that each case shows as skipped until an adapter runs them.
func TestMethodSuites(t *testing.T) {
	t.Parallel()

	t.Run("RedirectMethod", func(t *testing.T) {
		t.Parallel()
		sessiontest.RunRedirectMethod(t, nil)
	})
	t.Run("PasswordMethod", func(t *testing.T) {
		t.Parallel()
		sessiontest.RunPasswordMethod(t, nil)
	})
	t.Run("RequestMethod", func(t *testing.T) {
		t.Parallel()
		sessiontest.RunRequestMethod(t, nil)
	})
}
