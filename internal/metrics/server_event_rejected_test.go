package metrics_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/metrics"
	"opendustpan.org/opendustpan/pkg/forge"
)

// eventRejectedReasons is the fixed reason set of the intake of the Git host
// events. The metric holds its own copy of the strings, so a test compares it
// with the constants of the domain.
func eventRejectedReasons() []string {
	return []string{
		forge.EventReasonUnknownHook, forge.EventReasonNoRegistration, forge.EventReasonSignature,
		forge.EventReasonTooLarge, forge.EventReasonMalformed, forge.EventReasonUnsupported,
		forge.EventReasonDuplicate, forge.EventReasonNotAssigned, forge.EventReasonNotPrimary,
		forge.EventReasonSecretNotOpened, forge.EventReasonReceiptsFull, forge.EventReasonOther,
	}
}

func eventRejectedLine(reason string, n int) string {
	return fmt.Sprintf(`dustpan_git_host_event_rejected_total{reason=%q} %d`, reason, n)
}

func TestGitHostEventRejected(t *testing.T) {
	t.Parallel()

	m := metrics.NewServerMetrics("dev", time.Now())
	m.ObserveRequest("server", "POST /webhooks/{hookID}", "POST", 204, time.Millisecond)

	// Each known reason counts under its own label.
	for _, reason := range eventRejectedReasons() {
		m.ObserveGitHostEventRejected(reason)
	}

	// A value outside the set counts as other, whatever its text.
	hostile := []string{"", "0123456789abcdef0123456789abcdef", "ghi_installation_77", "sig\r\nX: y", "SIGNATURE"}
	for _, reason := range hostile {
		m.ObserveGitHostEventRejected(reason)
	}

	got := scrapeServer(t, m)

	for _, want := range []string{
		"# HELP dustpan_git_host_event_rejected_total",
		"# TYPE dustpan_git_host_event_rejected_total counter",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output misses %q:\n%s", want, got)
		}
	}

	for _, reason := range eventRejectedReasons() {
		n := 1
		if reason == forge.EventReasonOther {
			n = 6
		}

		if want := eventRejectedLine(reason, n); !strings.Contains(got, want) {
			t.Errorf("output misses %q:\n%s", want, got)
		}
	}

	if series := strings.Count(got, "dustpan_git_host_event_rejected_total{"); series != len(eventRejectedReasons()) {
		t.Errorf("series = %d, want %d (one for each reason)", series, len(eventRejectedReasons()))
	}

	for _, leak := range []string{"ghi_installation_77", "0123456789abcdef", "SIGNATURE", "X: y"} {
		if strings.Contains(got, leak) {
			t.Errorf("output holds the hostile value %q", leak)
		}
	}

	checkFormat(t, got)
}
