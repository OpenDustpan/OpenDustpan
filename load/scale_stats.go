package main

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Route names of the scale run. The names group the requests of the recorded
// CLI traffic by what the server does, not by the unit name in the path.
const (
	routeStackCreate    = "pulumi stack create"
	routeUpdateCreate   = "pulumi update create"
	routeUpdateStart    = "pulumi update start"
	routeCheckpointSave = "pulumi checkpoint save"
	routeUpdateComplete = "pulumi update complete"
	routeWorkspaceNew   = "tofu workspace create"
	routeLock           = "tofu lock"
	routeStateDeclare   = "tofu state declare"
	routeStateUpload    = "tofu state upload"
	routeUnlock         = "tofu unlock"
	routeWorkspaceList  = "tofu workspace list"
	routeInventoryList  = "inventory list"
	routeOther          = "other"
)

// routeOf answers the route name of a request. The name does not hold the
// unit name or an ID, so every request of one kind lands in one group.
func routeOf(method, path string) string {
	switch {
	case strings.Contains(path, "/checkpointverbatim"), strings.Contains(path, "/checkpointdelta"):
		return routeCheckpointSave
	case strings.HasSuffix(path, "/complete"):
		return routeUpdateComplete
	case strings.Contains(path, "/actions/lock"):
		return routeLock
	case strings.Contains(path, "/actions/unlock"):
		return routeUnlock
	}

	return listRouteOf(method, path)
}

// listRouteOf answers the route name of a state or list request.
func listRouteOf(method, path string) string {
	switch {
	case method == http.MethodPost && strings.HasSuffix(path, "/state-versions"):
		return routeStateDeclare
	case method == http.MethodPut:
		return routeStateUpload
	case method == http.MethodGet && strings.HasSuffix(path, "/units"):
		return routeInventoryList
	case method == http.MethodGet && strings.HasSuffix(path, "/workspaces"):
		return routeWorkspaceList
	case method == http.MethodPost && strings.HasSuffix(path, "/workspaces"):
		return routeWorkspaceNew
	}

	return stackRouteOf(method, path)
}

// stackRouteOf answers the route name of a Pulumi stack or update request.
func stackRouteOf(method, path string) string {
	rest, found := strings.CutPrefix(path, stacksPath)
	if !found || method != http.MethodPost {
		return routeOther
	}

	parts := strings.Split(rest, "/")

	const (
		stackCreateParts  = 2
		updateCreateParts = 4
		updateStartParts  = 5
	)

	switch {
	case len(parts) == stackCreateParts:
		return routeStackCreate
	case len(parts) == updateCreateParts && parts[3] == "update":
		return routeUpdateCreate
	case len(parts) == updateStartParts && parts[3] == "update":
		return routeUpdateStart
	}

	return routeOther
}

// sample is one request the harness sent: when it began, which route it
// used, how long the answer took to reach its headers, and how it ended.
type sample struct {
	at     time.Duration
	route  string
	took   time.Duration
	status int
	failed bool
}

// recorder is a round tripper that keeps one sample for each request. A
// request fails when the connection fails or the status is 400 or above.
type recorder struct {
	mu      sync.Mutex
	start   time.Time
	samples []sample
	next    http.RoundTripper
}

// newRecorder answers a recorder that counts time from now.
func newRecorder(next http.RoundTripper) *recorder {
	return &recorder{mu: sync.Mutex{}, start: time.Now(), samples: nil, next: next}
}

// RoundTrip implements http.RoundTripper.
func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	began := time.Now()
	resp, err := r.next.RoundTrip(req)

	one := sample{
		at: began.Sub(r.start), route: routeOf(req.Method, req.URL.Path), took: time.Since(began),
		status: 0, failed: err != nil,
	}

	if err == nil {
		one.status = resp.StatusCode
		one.failed = resp.StatusCode >= http.StatusBadRequest
	}

	r.mu.Lock()
	r.samples = append(r.samples, one)
	r.mu.Unlock()

	if err != nil {
		return nil, fmt.Errorf("round trip: %w", err)
	}

	return resp, nil
}

// since answers the time from the start of the recorder.
func (r *recorder) since() time.Duration {
	return time.Since(r.start)
}

// window answers the samples that began in [from, to).
func (r *recorder) window(from, to time.Duration) []sample {
	r.mu.Lock()
	defer r.mu.Unlock()

	var out []sample

	for _, one := range r.samples {
		if one.at >= from && one.at < to {
			out = append(out, one)
		}
	}

	return out
}

// RouteStats holds the latency of one route over a window. Times use
// milliseconds and cover the time to the response headers.
type RouteStats struct {
	Route    string  `json:"route"`
	Requests int     `json:"requests"`
	Errors   int     `json:"errors"`
	P50Ms    float64 `json:"p50Ms"`
	P95Ms    float64 `json:"p95Ms"`
	P99Ms    float64 `json:"p99Ms"`
	MaxMs    float64 `json:"maxMs"`
}

// PhaseStats holds the requests of one window of the run.
type PhaseStats struct {
	Seconds        float64        `json:"seconds"`
	Requests       int            `json:"requests"`
	Errors         int            `json:"errors"`
	ErrorRate      float64        `json:"errorRate"`
	RequestsPerSec float64        `json:"requestsPerSec"`
	Statuses       map[string]int `json:"errorStatuses,omitempty"`
	Routes         []RouteStats   `json:"routes"`
}

// Quantiles of the latency report.
const (
	quantile50 = 0.5
	quantile95 = 0.95
	quantile99 = 0.99
)

// summarize answers the statistics of samples that span window.
func summarize(samples []sample, window time.Duration) PhaseStats {
	byRoute := map[string][]sample{}
	statuses := map[string]int{}
	errs := 0

	for _, one := range samples {
		byRoute[one.route] = append(byRoute[one.route], one)

		if one.failed {
			errs++

			statuses[statusName(one.status)]++
		}
	}

	out := PhaseStats{
		Seconds: window.Seconds(), Requests: len(samples), Errors: errs, ErrorRate: 0,
		RequestsPerSec: 0, Statuses: statuses, Routes: nil,
	}

	if len(samples) > 0 {
		out.ErrorRate = float64(errs) / float64(len(samples))
	}

	if window > 0 {
		out.RequestsPerSec = float64(len(samples)) / window.Seconds()
	}

	if len(statuses) == 0 {
		out.Statuses = nil
	}

	for route, group := range byRoute {
		out.Routes = append(out.Routes, routeStats(route, group))
	}

	slices.SortFunc(out.Routes, func(a, b RouteStats) int { return strings.Compare(a.Route, b.Route) })

	return out
}

// statusName answers the key of an error status: "transport" for a request
// that got no answer.
func statusName(status int) string {
	if status == 0 {
		return "transport"
	}

	return strconv.Itoa(status)
}

// routeStats answers the statistics of the samples of one route.
func routeStats(route string, group []sample) RouteStats {
	times := make([]float64, 0, len(group))
	errs := 0

	for _, one := range group {
		times = append(times, ms(one.took))

		if one.failed {
			errs++
		}
	}

	return RouteStats{
		Route: route, Requests: len(group), Errors: errs,
		P50Ms: percentile(times, quantile50), P95Ms: percentile(times, quantile95),
		P99Ms: percentile(times, quantile99), MaxMs: slices.Max(times),
	}
}
