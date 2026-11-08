package runnerapi_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/runnerapi"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/runner"
)

// debugSecret is the session detail that each error of TestAnonymousRefusalLogHoldsNoText
// wraps. No log line and no answer may hold it.
const debugSecret = "session=s3cr3t-session-value"

// TestAnonymousRefusalLogHoldsNoText checks that a refusal that an anonymous caller
// can cause logs its line at Debug without the text of its error, and that the
// answer holds no wrapped detail either.
func TestAnonymousRefusalLogHoldsNoText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		scope runnerapi.Scope
		err   error
	}{
		{
			"no sign-in",
			runnerapi.ScopeAuthorized,
			fmt.Errorf("sign-in: %s: %w", debugSecret, identity.ErrUnauthenticated),
		},
		{
			"proof that does not verify",
			runnerapi.ScopeRunnerKey,
			fmt.Errorf("verify: %s: %w", debugSecret, runner.ErrBadProof),
		},
		{"unknown runner", runnerapi.ScopeAuthorized, fmt.Errorf("lookup: %s: %w", debugSecret, runner.ErrNotFound)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
			rec := httptest.NewRecorder()

			runnerapi.WriteError(t.Context(), rec, logger, tc.scope, tc.err)

			logs := buf.String()
			if !strings.Contains(logs, "level=DEBUG") || !strings.Contains(logs, `msg="runner request refused"`) {
				t.Errorf("log %q does not hold the Debug refusal line", logs)
			}

			if !strings.Contains(logs, "status=") || !strings.Contains(logs, "code=") {
				t.Errorf("log %q does not hold the status and the code", logs)
			}

			if strings.Contains(logs, debugSecret) {
				t.Errorf("log %q holds the wrapped session detail", logs)
			}

			if strings.Contains(rec.Body.String(), debugSecret) {
				t.Errorf("answer %q holds the wrapped session detail", rec.Body.String())
			}
		})
	}
}
