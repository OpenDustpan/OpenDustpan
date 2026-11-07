//go:build e2e

package e2e_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/pkg/namespace"
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

// TestStepLogReportHostile checks the secrets that a plain replacement in the
// order of the arguments or a cut at the cap does not hide.
func TestStepLogReportHostile(t *testing.T) {
	t.Parallel()

	t.Run("hides_the_rest_of_a_longer_secret", func(t *testing.T) {
		t.Parallel()

		// The shorter secret is first in the arguments: the longer one must still
		// be replaced as a whole.
		got := e2e.StepLogReport([]byte("x abcdef-tail y"), "abc", "abcdef-tail")
		if want := "x [redacted] y"; got != want {
			t.Fatalf("report = %q, want %q", got, want)
		}
	})

	t.Run("hides_a_partial_secret_at_the_read_end", func(t *testing.T) {
		t.Parallel()

		// A long secret that repeats to the read limit: the replacements make the
		// text short, so the read end holds the start of one more secret.
		secret := "SECRET-VALUE-12345"
		limit := e2e.MaxStepLogReport + 1 + len(secret)
		text := strings.Repeat(secret, limit/len(secret)+2)[:limit]

		got := e2e.StepLogReport([]byte(text), secret)
		if strings.Contains(got, "SECRE") {
			t.Fatalf("report holds the start of the secret: %q", got[len(got)-60:])
		}

		if !strings.Contains(got, "[cut at ") {
			t.Fatalf("report has no cut note: %q", got[len(got)-60:])
		}
	})
}

// openError is the text of an error of a LogOpener.
type openError string

func (e openError) Error() string { return string(e) }

// failingOpener is a LogOpener that fails with the error err when it is not
// empty, or opens text.
type failingOpener struct {
	err  openError
	text string
}

func (o failingOpener) OpenLog(context.Context, namespace.Name, string) (io.ReadCloser, error) {
	if o.err != "" {
		return nil, o.err
	}

	return io.NopCloser(strings.NewReader(o.text)), nil
}

// TestStepLogEntry checks that the name of a step and an error text pass the
// same redaction and control-character removal as the log, and that a failed
// open is a line of the report and not a failure.
func TestStepLogEntry(t *testing.T) {
	t.Parallel()

	secret := "SECRET-VALUE-12345"

	tests := []struct {
		name    string
		opener  failingOpener
		step    string
		want    []string
		wantNot []string
	}{
		{
			name:    "secret and control characters in the name",
			opener:  failingOpener{err: "", text: "ok\n"},
			step:    "plan-" + secret + "\x1b[31m\nforged line",
			want:    []string{"step log plan-[redacted]?[31m?forged line of job j1 (exit code 3):\nok\n"},
			wantNot: []string{secret, "\x1b", "forged line\n"},
		},
		{
			name:    "secret and control characters in the error text",
			opener:  failingOpener{err: openError("denied for " + secret + "\x00\r\nnext"), text: ""},
			step:    "plan",
			want:    []string{"cannot open: denied for [redacted]???next\n"},
			wantNot: []string{secret, "\x00", "\r"},
		},
		{
			name:    "failed open ends as a line of the report",
			opener:  failingOpener{err: "blob missing", text: ""},
			step:    "plan",
			want:    []string{"step log plan of job j1: cannot open: blob missing\n"},
			wantNot: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ref := e2e.StepLogRef{Namespace: "acme", JobID: "j1", Name: tc.step, Version: "v1", ExitCode: 3}
			got := e2e.StepLogEntry(t.Context(), tc.opener, ref, []string{secret})

			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("entry = %q, want it to hold %q", got, w)
				}
			}

			for _, w := range tc.wantNot {
				if strings.Contains(got, w) {
					t.Errorf("entry = %q, must not hold %q", got, w)
				}
			}
		})
	}
}
