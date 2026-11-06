package sessiontest_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/session/sessiontest"
)

// TestMethodSuites runs the suite of the request method port, which has no
// adapter yet, so that each case shows as skipped until an adapter runs it.
// The adapters of the redirect and the password ports run their suites in
// their own packages.
func TestMethodSuites(t *testing.T) {
	t.Parallel()

	t.Run("RequestMethod", func(t *testing.T) {
		t.Parallel()
		sessiontest.RunRequestMethod(t, nil)
	})
}
