package metrics_test

import (
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/metrics"
)

func TestServerAuditSinkLag(t *testing.T) {
	t.Parallel()

	m := metrics.NewServerMetrics("dev", time.Now())
	m.ObserveRequest("server", "GET /api/server/audit/sinks", "GET", 200, time.Millisecond)
	m.SetAuditSinkLag("siem", 90)
	m.SetAuditSinkLag("siem", 0)
	m.SetAuditSinkLagEntries("siem", 7)
	m.SetAuditSinkLagEntries("siem", 0)

	got := scrapeServer(t, m)

	for _, want := range []string{
		"# HELP dustpan_audit_sink_lag_seconds",
		"# TYPE dustpan_audit_sink_lag_seconds gauge",
		`dustpan_audit_sink_lag_seconds{sink="siem"} 0`,
		"# HELP dustpan_audit_sink_lag_entries",
		"# TYPE dustpan_audit_sink_lag_entries gauge",
		`dustpan_audit_sink_lag_entries{sink="siem"} 0`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output misses %q:\n%s", want, got)
		}
	}

	checkFormat(t, got)
}

// TestServerAuditSinkFailures proves that each failed delivery counts with
// the sink name and the reason, and that an empty reason counts as a failed
// write.
func TestServerAuditSinkFailures(t *testing.T) {
	t.Parallel()

	m := metrics.NewServerMetrics("dev", time.Now())
	m.ObserveRequest("server", "GET /api/server/audit/sinks", "GET", 200, time.Millisecond)
	m.IncAuditSinkFailure("siem", "size")
	m.IncAuditSinkFailure("siem", "unreachable")
	m.IncAuditSinkFailure("siem", "")

	got := scrapeServer(t, m)

	for _, want := range []string{
		"# HELP dustpan_audit_sink_failures_total",
		"# TYPE dustpan_audit_sink_failures_total counter",
		`dustpan_audit_sink_failures_total{sink="siem",reason="size"} 1`,
		`dustpan_audit_sink_failures_total{sink="siem",reason="unreachable"} 1`,
		`dustpan_audit_sink_failures_total{sink="siem",reason="write"} 1`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output misses %q:\n%s", want, got)
		}
	}

	checkFormat(t, got)
}
