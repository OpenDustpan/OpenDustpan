package metrics_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/metrics"
)

// formatStart renders the Unix seconds of t as the metrics output does.
func formatStart(t time.Time) string {
	return strconv.FormatFloat(float64(t.Unix()), 'g', -1, 64)
}

// scrapeServer writes the server metrics and returns the text output.
func scrapeServer(t *testing.T, m *metrics.ServerMetrics) string {
	t.Helper()

	var out strings.Builder
	if err := m.Write(&out); err != nil {
		t.Fatalf("write: %v", err)
	}

	return out.String()
}

func TestServerFamilies(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)
	m := metrics.NewServerMetrics("1.2.3", started)
	m.ObserveRequest("pulumi", "GET /api/user", "GET", 200, 10*time.Millisecond)

	got := scrapeServer(t, m)

	for _, want := range []string{
		"# HELP dustpan_http_requests_total",
		"# TYPE dustpan_http_requests_total counter",
		"# HELP dustpan_http_request_duration_seconds",
		"# TYPE dustpan_http_request_duration_seconds histogram",
		"# HELP dustpan_http_requests_in_flight",
		"# TYPE dustpan_http_requests_in_flight gauge",
		`dustpan_build_info{version="1.2.3"} 1`,
		"dustpan_process_start_time_seconds",
		"# HELP dustpan_go_goroutines",
		"# HELP dustpan_go_heap_bytes",
		"# HELP dustpan_go_gc_cycles_total",
		"# HELP dustpan_readiness_failures_total",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output misses %q:\n%s", want, got)
		}
	}

	if want := "dustpan_process_start_time_seconds " + formatStart(started); !strings.Contains(got, want) {
		t.Errorf("output misses %q:\n%s", want, got)
	}

	checkFormat(t, got)
}

func TestServerDefaultVersion(t *testing.T) {
	t.Parallel()

	m := metrics.NewServerMetrics("", time.Now())

	if got := scrapeServer(t, m); !strings.Contains(got, `dustpan_build_info{version="dev"} 1`) {
		t.Errorf("default build info missing:\n%s", got)
	}
}

func TestServerObserveRequest(t *testing.T) {
	t.Parallel()

	m := metrics.NewServerMetrics("dev", time.Now())
	m.ObserveRequest("pulumi", "GET /api/user", "GET", 200, 25*time.Millisecond)
	m.ObserveRequest("pulumi", "GET /api/user", "GET", 200, 75*time.Millisecond)

	got := scrapeServer(t, m)

	want := `dustpan_http_requests_total{adapter="pulumi",route="GET /api/user",method="GET",code="200"} 2`
	if !strings.Contains(got, want) {
		t.Errorf("output misses %q:\n%s", want, got)
	}

	for _, want := range []string{
		`dustpan_http_request_duration_seconds_bucket{adapter="pulumi",route="GET /api/user",method="GET",le=`,
		`dustpan_http_request_duration_seconds_count{adapter="pulumi",route="GET /api/user",method="GET"} 2`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output misses %q:\n%s", want, got)
		}
	}

	checkFormat(t, got)
}

func TestServerObserveNormalizes(t *testing.T) {
	t.Parallel()

	m := metrics.NewServerMetrics("dev", time.Now())
	m.ObserveRequest("", "", "", 0, time.Millisecond)
	m.ObserveRequest("bogus", "/api/v2/", "GET", 404, time.Millisecond)

	got := scrapeServer(t, m)

	if strings.Contains(got, `adapter="bogus"`) || strings.Contains(got, `route="/api/v2/"`) {
		t.Errorf("output holds a raw adapter or path:\n%s", got)
	}

	if !strings.Contains(got, `route="unmatched"`) {
		t.Errorf("output misses the unmatched route:\n%s", got)
	}

	if !strings.Contains(got, `adapter="server"`) {
		t.Errorf("output misses the normalized adapter:\n%s", got)
	}
}

func TestServerInFlight(t *testing.T) {
	t.Parallel()

	m := metrics.NewServerMetrics("dev", time.Now())
	m.IncInFlight("pulumi")
	m.IncInFlight("bogus")
	m.DecInFlight("pulumi")
	m.DecInFlight("bogus")

	got := scrapeServer(t, m)

	for _, want := range []string{
		`dustpan_http_requests_in_flight{adapter="pulumi"} 0`,
		`dustpan_http_requests_in_flight{adapter="server"} 0`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output misses %q:\n%s", want, got)
		}
	}
}

func TestServerReadiness(t *testing.T) {
	t.Parallel()

	m := metrics.NewServerMetrics("dev", time.Now())
	m.IncReadinessFailure()
	m.IncReadinessFailure()

	if got := scrapeServer(t, m); !strings.Contains(got, "dustpan_readiness_failures_total 2") {
		t.Errorf("readiness count missing:\n%s", got)
	}
}

func TestNormalizeAdapter(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ in, want string }{
		{"pulumi", "pulumi"},
		{"opentofu", "opentofu"},
		{"inventory", "inventory"},
		{"server", "server"},
		{"", "server"},
		{"bogus", "server"},
		{"/api/user", "server"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()

			if got := metrics.NormalizeAdapter(tc.in); got != tc.want {
				t.Errorf("NormalizeAdapter(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeRoute(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ in, want string }{
		{"GET /api/user", "GET /api/user"},
		{"", "unmatched"},
		{"unmatched", "unmatched"},
		{"/api/v2/", "unmatched"},
		{"/api/stacks/acme/web/prod", "unmatched"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()

			if got := metrics.NormalizeRoute(tc.in); got != tc.want {
				t.Errorf("NormalizeRoute(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestServerMetricsConfigReload(t *testing.T) {
	t.Parallel()

	m := metrics.NewServerMetrics("1.2.3", time.Unix(0, 0))

	if got := scrapeServer(t, m); !strings.Contains(got, "dustpan_config_last_reload_success 1") {
		t.Fatalf("the gauge does not start at 1:\n%s", got)
	}

	m.ObserveConfigReload(metrics.ReloadFailure)

	got := scrapeServer(t, m)
	for _, want := range []string{
		`dustpan_config_reloads_total{result="failure"} 1`,
		"dustpan_config_last_reload_success 0",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output misses %q:\n%s", want, got)
		}
	}

	m.ObserveConfigReload(metrics.ReloadSuccess)
	m.ObserveConfigReload(metrics.ReloadSuccess)

	got = scrapeServer(t, m)
	for _, want := range []string{
		`dustpan_config_reloads_total{result="success"} 2`,
		`dustpan_config_reloads_total{result="failure"} 1`,
		"dustpan_config_last_reload_success 1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output misses %q:\n%s", want, got)
		}
	}
}
