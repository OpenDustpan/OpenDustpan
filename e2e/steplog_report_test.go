//go:build e2e

package e2e_test

import (
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// TestStepLogReport checks the text that a failed test prints for a step log:
// the secrets are gone, the control characters are gone and the size is capped.
func TestStepLogReport(t *testing.T) {
	t.Parallel()

	t.Run("replaces_each_secret", func(t *testing.T) {
		t.Parallel()

		got := e2e.StepLogReport([]byte("token abc123 and abc123 again\n"), "abc123", "")
		if want := "token [redacted] and [redacted] again\n"; got != want {
			t.Fatalf("report = %q, want %q", got, want)
		}
	})

	t.Run("replaces_control_characters", func(t *testing.T) {
		t.Parallel()

		got := e2e.StepLogReport([]byte("a\x1b[31mb\x00\tc\r\nd\x7f"))
		if want := "a?[31mb?\tc?\nd?"; got != want {
			t.Fatalf("report = %q, want %q", got, want)
		}
	})

	t.Run("cuts_at_the_cap", func(t *testing.T) {
		t.Parallel()

		got := e2e.StepLogReport([]byte(strings.Repeat("x", e2e.MaxStepLogReport+1)))
		if !strings.HasPrefix(got, strings.Repeat("x", e2e.MaxStepLogReport)+"\n[cut at ") {
			t.Fatalf("report does not hold the cap and the cut note: %d bytes", len(got))
		}
	})

	t.Run("keeps_a_log_of_the_cap_size", func(t *testing.T) {
		t.Parallel()

		text := strings.Repeat("x", e2e.MaxStepLogReport)
		if got := e2e.StepLogReport([]byte(text)); got != text {
			t.Fatalf("report of a log of the cap size changed: %d bytes", len(got))
		}
	})

	t.Run("hides_a_secret_at_the_cap", func(t *testing.T) {
		t.Parallel()

		// The secret begins before the cap and ends after it: the cut must not
		// leave its first bytes in the report.
		secret := "SECRET-VALUE-12345"
		text := strings.Repeat("x", e2e.MaxStepLogReport-5) + secret + "tail"

		got := e2e.StepLogReport([]byte(text), secret)
		if strings.Contains(got, "SECRET") {
			t.Fatalf("report holds the start of the secret: %q", got[len(got)-40:])
		}
	})
}
