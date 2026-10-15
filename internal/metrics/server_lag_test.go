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

	got := scrapeServer(t, m)

	for _, want := range []string{
		"# HELP dustpan_audit_sink_lag_seconds",
		"# TYPE dustpan_audit_sink_lag_seconds gauge",
		`dustpan_audit_sink_lag_seconds{sink="siem"} 0`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output misses %q:\n%s", want, got)
		}
	}

	checkFormat(t, got)
}
