package dustpan_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// scrapeMetrics returns the metrics output of srv.
func scrapeMetrics(t *testing.T, f fixture) string {
	t.Helper()

	var out strings.Builder
	if err := f.srv.Metrics().Write(&out); err != nil {
		t.Fatalf("write metrics: %v", err)
	}

	return out.String()
}

// createStack creates a stack with name in project web of namespace acme.
func createStack(t *testing.T, f fixture, name string) {
	t.Helper()

	status, body := f.do(t, http.MethodPost, "/api/stacks/acme/web", adminToken, `{"stackName":"`+name+`"}`)
	if status != http.StatusOK {
		t.Fatalf("create stack %s: status %d, body %s", name, status, body)
	}
}

// readStack reads a stack with name in project web of namespace acme.
func readStack(t *testing.T, f fixture, name string) {
	t.Helper()

	status, body := f.do(t, http.MethodGet, "/api/stacks/acme/web/"+name, adminToken, "")
	if status != http.StatusOK {
		t.Fatalf("get stack %s: status %d, body %s", name, status, body)
	}
}

// checkAdapterSample fails when output holds no request count or duration
// sample with the adapter label.
func checkAdapterSample(t *testing.T, output, adapter string) {
	t.Helper()

	if want := `dustpan_http_requests_total{adapter="` + adapter + `"`; !strings.Contains(output, want) {
		t.Errorf("output misses a request count for adapter %q:\n%s", adapter, output)
	}

	want := `dustpan_http_request_duration_seconds_bucket{adapter="` + adapter + `"`
	if !strings.Contains(output, want) {
		t.Errorf("output misses a duration bucket for adapter %q:\n%s", adapter, output)
	}
}

// TestMetricsCountsPerAdapter proves a request to each adapter leaves a
// request count sample with that adapter label, and that the in-flight gauge
// returns to zero after the requests finish.
func TestMetricsCountsPerAdapter(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.createNamespace(t, "acme", installationName)

	status, body := f.do(t, http.MethodGet, "/api/user", adminToken, "")
	if status != http.StatusOK {
		t.Fatalf("pulumi request: status %d, body %s", status, body)
	}

	status, _, _ = f.doRaw(t, http.MethodGet, "/api/v2/ping", "")
	if status != http.StatusOK {
		t.Fatalf("opentofu request: status %d", status)
	}

	path := "/api/dustpan/v1/namespaces/acme/units"

	status, body = f.do(t, http.MethodGet, path, adminToken, "")
	if status != http.StatusOK {
		t.Fatalf("inventory request: status %d, body %s", status, body)
	}

	status, _, _ = f.doRaw(t, http.MethodGet, "/api/server/no-such-route", "")
	if status != http.StatusNotFound {
		t.Fatalf("server request: status %d, want %d", status, http.StatusNotFound)
	}

	got := scrapeMetrics(t, f)

	for _, adapter := range []string{"pulumi", "opentofu", "inventory", "server"} {
		checkAdapterSample(t, got, adapter)
	}

	if want := `dustpan_build_info{version="dev"} 1`; !strings.Contains(got, want) {
		t.Errorf("output misses %q:\n%s", want, got)
	}

	checkInFlightZero(t, got)
}

// checkInFlightZero fails when any in-flight gauge sample is not zero.
func checkInFlightZero(t *testing.T, output string) {
	t.Helper()

	found := false

	for line := range strings.Lines(output) {
		if !strings.HasPrefix(line, "dustpan_http_requests_in_flight{") {
			continue
		}

		found = true

		value := line[strings.LastIndex(line, " ")+1:]

		got, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			t.Fatalf("bad in-flight sample %q: %v", line, err)
		}

		if got != 0 {
			t.Fatalf("in-flight sample %q is %v, want 0", line, got)
		}
	}

	if !found {
		t.Fatal("output holds no in-flight sample")
	}
}

// checkPatternShared fails when output holds other than one GET 200 series
// for pattern, or when the series count is not want.
func checkPatternShared(t *testing.T, output, pattern string, want int) {
	t.Helper()

	count := 0

	for line := range strings.Lines(output) {
		if !strings.HasPrefix(line, "dustpan_http_requests_total{") {
			continue
		}

		if !strings.Contains(line, pattern) || !strings.Contains(line, `method="GET"`) {
			continue
		}

		if !strings.Contains(line, `code="200"`) {
			continue
		}

		count++

		if !strings.HasSuffix(strings.TrimSpace(line), " "+strconv.Itoa(want)) {
			t.Fatalf("series %q holds one request, want %d", line, want)
		}
	}

	if count != 1 {
		t.Fatalf("series count = %d, want 1 shared series", count)
	}
}

// TestMetricsRouteLabelIsPattern proves the route label names the registered
// pattern, never the path: two stacks with different names share one series,
// and no namespace or unit name appears in the output.
func TestMetricsRouteLabelIsPattern(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.createNamespace(t, "acme", installationName)

	for _, stack := range []string{"alpha", "beta"} {
		createStack(t, f, stack)
		readStack(t, f, stack)
	}

	got := scrapeMetrics(t, f)

	const pattern = `route="GET /api/stacks/{namespace}/{project}/{stack}"`

	if !strings.Contains(got, pattern) {
		t.Fatalf("output misses the route pattern %s:\n%s", pattern, got)
	}

	for _, name := range []string{"acme", "alpha", "beta"} {
		if strings.Contains(got, name) {
			t.Fatalf("output holds %q, want the pattern only:\n%s", name, got)
		}
	}

	checkPatternShared(t, got, pattern, 2)
	checkInFlightZero(t, got)
}

// TestMetricsNeverHoldsCanaries proves no label holds a namespace, a
// principal, an access token or a unit name: canary values sent through both
// tool adapters and through an unmatched path never appear in the metrics
// output.
func TestMetricsNeverHoldsCanaries(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.createNamespace(t, "acme", installationName)

	//nolint:gosec // Canary values for the never-held test, not credentials.
	tokenCanary := "canary-token-9f2c7a4e1b5d3a7c8e6f0a2b4d6c8e0a"
	queryCanary := "canary-namespace-7a1b2c3d4e5f6a7b8"
	bodyCanary := "canary-stack-3f4a5b6c7d8e9f0a1b"
	pathCanary := "canary-path-5e6f7a8b9c0d1e2f3a"

	f.do(t, http.MethodGet, "/api/user", tokenCanary, "")
	f.do(t, http.MethodGet, "/api/user/stacks?organization="+queryCanary, adminToken, "")
	createStack(t, f, bodyCanary)
	f.doRaw(t, http.MethodGet, "/api/v2/ping", "Bearer "+tokenCanary)
	f.doRaw(t, http.MethodPut, "/api/v2/state-uploads/sv-canary123/"+pathCanary, "")
	f.doRaw(t, http.MethodGet, "/no-such-path-"+pathCanary, "")

	got := scrapeMetrics(t, f)

	for _, canary := range []string{tokenCanary, queryCanary, bodyCanary, pathCanary, "sv-canary123"} {
		if strings.Contains(got, canary) {
			t.Fatalf("metrics output holds canary value %q", canary)
		}
	}

	if !strings.Contains(got, `route="unmatched"`) {
		t.Fatalf("output misses the unmatched route:\n%s", got)
	}
}
