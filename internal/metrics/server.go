package metrics

import (
	"io"
	"strconv"
	"strings"
	"time"
)

// Adapter labels for the request metrics. The middleware maps each request
// to one of these from its route pattern, or from its path prefix when no
// pattern matched. It never uses the path itself as a label.
const (
	AdapterPulumi    = "pulumi"
	AdapterOpenTofu  = "opentofu"
	AdapterInventory = "inventory"
	AdapterServer    = "server"
)

// UnmatchedRoute is the route label for a request that matches no pattern.
// It is fixed, so scans cannot grow the label set.
const UnmatchedRoute = "unmatched"

// Series caps for the request families. The route label holds one registered
// pattern per route, so a small cap fits the in-flight gauge, while the
// count and the duration cover every adapter, route, method and status.
const (
	maxRequestSeries = 1024
	maxInFlightCount = 16
	maxBuildCount    = 4
	// maxSinkSeries caps the series of the audit sink families: one per
	// configured sink.
	maxSinkSeries = MaxSinkSeries
	// maxReloadSeries holds one series for each reload result.
	maxReloadSeries = 2
	// maxSweepResults holds one series for each result of the inactivity
	// disable.
	maxSweepResults = 2
	// maxDeletedSweepResults holds one series for each outcome of the
	// retention sweep.
	maxDeletedSweepResults = 4
)

// Label names of the request families. No label holds a namespace, a
// principal, an access token or a unit name.
const (
	labelAdapter = "adapter"
	labelRoute   = "route"
	labelMethod  = "method"
	labelCode    = "code"
	labelVersion = "version"
	// labelSink names a configured audit sink, for example "siem". The
	// operator sets the name; it never holds an address or a secret.
	labelSink   = "sink"
	labelResult = "result"
)

// Results of a reload of the configuration file (ADR 63 decision 5).
const (
	ReloadSuccess = "success"
	ReloadFailure = "failure"
)

// MaxSinkSeries is the count of sinks that the audit sink families label.
// The metrics of a sink past this count go to no series.
const MaxSinkSeries = 16

// ServerMetrics holds the request count, the request duration histogram,
// the requests in flight gauge, the build info, the process start time, the
// runtime gauges, the readiness failure counter (ADR 32 decision 5) and the
// audit append failure counter (ADR 41 decision 5), and the audit sink
// counters and the log sink drop counter (ADR 60 decisions 13 and 14). All
// names start with dustpan_. The middleware in dustpan.go records each
// request through ObserveRequest with the registered route pattern, never
// the path. Domain packages never import this package. Only dustpan.go and
// the HTTP middleware do (ADR 32 decision 9). It also holds the two
// families of the reload of the configuration file (ADR 63 decision 5),
// and counts the runs of the inactivity disable by result (ADR 53).
//
// Create it with NewServerMetrics. It is safe for concurrent use.
type ServerMetrics struct {
	registry *Registry
	requests *Counter
	duration *Histogram
	inFlight *Gauge
	ready    *Counter
	audit    *Counter
	sinks    sinkFamilies
	reloads  *Counter
	reloadOK *Gauge
	sweeps   *Counter
	deleted  *Counter
}

// NewServerMetrics creates the request, build, process, runtime and
// readiness families in a new registry. version names the server release for
// dustpan_build_info; empty means dev. startedAt sets
// dustpan_process_start_time_seconds. It registers the runtime gauges from
// runtime/metrics and the counter of failed GET /health/ready probes.
func NewServerMetrics(version string, startedAt time.Time) *ServerMetrics {
	if strings.TrimSpace(version) == "" {
		version = "dev"
	}

	reg := NewRegistry()

	m := &ServerMetrics{
		registry: reg,
		requests: reg.NewCounter(
			"dustpan_http_requests_total",
			"HTTP requests by adapter, route, method and status.",
			[]string{labelAdapter, labelRoute, labelMethod, labelCode},
			maxRequestSeries,
		),
		duration: reg.NewHistogram(
			"dustpan_http_request_duration_seconds",
			"HTTP request duration in seconds by adapter, route and method.",
			[]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
			[]string{labelAdapter, labelRoute, labelMethod},
			maxRequestSeries,
		),
		inFlight: reg.NewGauge(
			"dustpan_http_requests_in_flight",
			"HTTP requests in flight by adapter.",
			[]string{labelAdapter},
			maxInFlightCount,
		),
		ready: reg.NewCounter(
			"dustpan_readiness_failures_total",
			"Readiness check failures.",
			nil,
			1,
		),
		audit: reg.NewCounter(
			"dustpan_audit_append_failures_total",
			"Audit entries lost because the append failed.",
			nil,
			1,
		),
		sinks: newSinkFamilies(reg),
		reloads: reg.NewCounter(
			"dustpan_config_reloads_total",
			"Reloads of the configuration file by result.",
			[]string{labelResult},
			maxReloadSeries,
		),
		reloadOK: reg.NewGauge(
			"dustpan_config_last_reload_success",
			"1 when the last reload of the configuration file succeeded, else 0.",
			nil,
			1,
		),
		sweeps:  newSweepCounter(reg),
		deleted: newDeletedSweepCounter(reg),
	}

	// The configuration that the server started with is valid, so the gauge
	// starts at 1: an alert on 0 fires only after a failed reload.
	m.reloadOK.Set(1, []string{}...)

	build := reg.NewGauge("dustpan_build_info", "Server build info.", []string{labelVersion}, maxBuildCount)
	build.Set(1, version)

	start := reg.NewGauge("dustpan_process_start_time_seconds", "Process start time in seconds.", nil, 1)
	start.Set(float64(startedAt.Unix()), []string{}...)

	reg.RegisterFunc("dustpan_go_goroutines", "Goroutine count.", Goroutines)
	reg.RegisterFunc("dustpan_go_heap_bytes", "Live heap size in bytes.", HeapBytes)
	reg.RegisterFunc("dustpan_go_gc_cycles_total", "Completed garbage collection cycles.", GCCycles)

	return m
}

// Registry returns the registry that holds every family.
func (m *ServerMetrics) Registry() *Registry {
	return m.registry
}

// Write writes the registry in the Prometheus text format. The metrics route
// of issue #165 reads through this method without importing this package.
func (m *ServerMetrics) Write(w io.Writer) error {
	return m.registry.Write(w)
}

// ObserveRequest records one finished request. adapter is one of the Adapter
// constants, route is the registered ServeMux pattern or UnmatchedRoute,
// method is the HTTP method and code is the status. duration is the time the
// handler took. It normalizes an empty or unknown adapter to server and an
// empty or bare-prefix route to UnmatchedRoute, so no path leaks into a label.
func (m *ServerMetrics) ObserveRequest(adapter, route, method string, code int, duration time.Duration) {
	adapter = NormalizeAdapter(adapter)
	route = NormalizeRoute(route)
	if method == "" {
		method = "UNKNOWN"
	}

	m.requests.Inc(adapter, route, method, strconv.Itoa(code))
	m.duration.Observe(duration.Seconds(), adapter, route, method)
}

// IncInFlight adds one to the in-flight gauge of adapter. The middleware
// calls it before the next handler runs, when only the path is known, so the
// gauge has no route label: the route is not known until the handler returns.
func (m *ServerMetrics) IncInFlight(adapter string) {
	m.inFlight.Inc(NormalizeAdapter(adapter))
}

// DecInFlight subtracts one from the in-flight gauge of adapter. The caller
// must pass the same adapter value as IncInFlight.
func (m *ServerMetrics) DecInFlight(adapter string) {
	m.inFlight.Dec(NormalizeAdapter(adapter))
}

// IncReadinessFailure adds one to the readiness failure counter. The GET
// /health/ready probe calls it when a readiness check fails.
func (m *ServerMetrics) IncReadinessFailure() {
	m.ready.Inc()
}

// ReadinessFailures returns the readiness failure counter, so the readiness
// probe can count on the same registry as the request families.
func (m *ServerMetrics) ReadinessFailures() *Counter {
	return m.ready
}

// IncAuditAppendFailure adds one to the audit append failure counter. The
// Pulumi decrypt audit routes call it when the audit append fails: the
// request still answers 204, so the counter is the only record of the lost
// entry besides the log line. Operators alert when it is above zero.
func (m *ServerMetrics) IncAuditAppendFailure() {
	m.audit.Inc()
}

// sinkFamilies holds the audit and log sink families: counters labeled by sink,
// with one series for each configured sink.
type sinkFamilies struct {
	shipped  *Counter
	sinkFail *Counter
	sinkSkip *Counter
	logDrop  *Counter
}

// newSinkFamilies registers the audit sink families in reg.
func newSinkFamilies(reg *Registry) sinkFamilies {
	family := func(name, help string) *Counter {
		return reg.NewCounter(name, help, []string{labelSink}, maxSinkSeries)
	}

	return sinkFamilies{
		shipped:  family("dustpan_audit_sink_entries_total", "Audit entries that a sink accepted, by sink."),
		sinkFail: family("dustpan_audit_sink_failures_total", "Failed delivery rounds of an audit sink, by sink."),
		sinkSkip: family(
			"dustpan_audit_sink_skipped_total",
			"Audit entries that a sink skipped because it cannot hold them, by sink.",
		),
		logDrop: family("dustpan_log_sink_dropped_total", "Application log records a log sink dropped, by sink."),
	}
}

// AddAuditShipped adds entries to the count of audit entries that the sink
// named sink accepted.
func (m *ServerMetrics) AddAuditShipped(sink string, entries int) {
	if entries > 0 {
		m.sinks.shipped.Add(uint64(entries), sink)
	}
}

// IncAuditSinkFailure adds one to the failed delivery rounds of the sink
// named sink. The shipper retries, so a failure loses no entry, but a count
// that keeps growing means the sink falls behind. Operators alert on it.
func (m *ServerMetrics) IncAuditSinkFailure(sink string) {
	m.sinks.sinkFail.Inc(sink)
}

// AddAuditSkipped adds entries to the count of audit entries that the sink
// named sink skipped because it cannot hold them, for example an entry
// above the message size cap of a syslog sink. The sink never receives
// such an entry. Operators alert when it is above zero.
func (m *ServerMetrics) AddAuditSkipped(sink string, entries int) {
	if entries > 0 {
		m.sinks.sinkSkip.Add(uint64(entries), sink)
	}
}

// newSweepCounter creates dustpan_inactivity_sweeps_total in reg.
func newSweepCounter(reg *Registry) *Counter {
	return reg.NewCounter(
		"dustpan_inactivity_sweeps_total",
		"Runs of the inactivity disable by result.",
		[]string{labelResult},
		maxSweepResults,
	)
}

// Results of a run of the inactivity disable.
const (
	SweepSuccess = "success"
	SweepFailure = "failure"
)

// ObserveSweep adds one to the inactivity disable counter for result:
// SweepSuccess or SweepFailure. Any other value counts as SweepFailure. The
// server calls it after each run (ADR 53). Operators alert when the failure
// series grows: a failed run disables nothing.
func (m *ServerMetrics) ObserveSweep(result string) {
	if result != SweepSuccess {
		result = SweepFailure
	}

	m.sweeps.Inc(result)
}

// newDeletedSweepCounter creates dustpan_deleted_unit_sweep_total in reg.
func newDeletedSweepCounter(reg *Registry) *Counter {
	return reg.NewCounter(
		"dustpan_deleted_unit_sweep_total",
		"Outcomes of the retention sweep of deleted units by result.",
		[]string{labelResult},
		maxDeletedSweepResults,
	)
}

// Outcomes of the retention sweep (ADR 47).
const (
	// DeletedSweepPurged counts the deleted units that the sweep purged.
	DeletedSweepPurged = "purged"
	// DeletedSweepSkipped counts the expired units that the sweep left to
	// another installation or that a restore took first.
	DeletedSweepSkipped = "skipped"
	// DeletedSweepFailed counts the failed purges, blob deletes and record
	// finishes, and the runs that could not read the store.
	DeletedSweepFailed = "failed"
	// DeletedSweepFinished counts the blob purge records that the sweep
	// finished.
	DeletedSweepFinished = "finished"
)

// ObserveDeletedSweep adds n to the retention sweep counter for result: one
// of the DeletedSweep outcomes. Any other value counts as
// DeletedSweepFailed, so the label set stays fixed. Operators alert when the
// failed series grows: a failed purge keeps the unit until the next run.
func (m *ServerMetrics) ObserveDeletedSweep(result string, n int) {
	switch result {
	case DeletedSweepPurged, DeletedSweepSkipped, DeletedSweepFinished:
	default:
		result = DeletedSweepFailed
	}

	if n > 0 {
		m.deleted.Add(uint64(n), result)
	}
}

// ObserveConfigReload records one reload of the configuration file (ADR 63
// decision 5): it counts result (ReloadSuccess or ReloadFailure) in
// dustpan_config_reloads_total and sets dustpan_config_last_reload_success
// to 1 for a success and 0 for a failure. Any other result counts as a
// failure, so the label set stays fixed.
func (m *ServerMetrics) ObserveConfigReload(result string) {
	if result == ReloadSuccess {
		m.reloads.Inc(ReloadSuccess)
		m.reloadOK.Set(1, []string{}...)

		return
	}

	m.reloads.Inc(ReloadFailure)
	m.reloadOK.Set(0, []string{}...)
}

// AddLogSinkDropped adds records to the count of application log records
// that the log sink named sink dropped: its queue was full or its write
// failed (ADR 60 decision 14).
func (m *ServerMetrics) AddLogSinkDropped(sink string, records int) {
	if records > 0 {
		m.sinks.logDrop.Add(uint64(records), sink)
	}
}

// NormalizeAdapter maps an adapter label to one of the four adapters. Any
// other value becomes server, so a new prefix cannot grow the label set.
func NormalizeAdapter(adapter string) string {
	switch adapter {
	case AdapterPulumi, AdapterOpenTofu, AdapterInventory, AdapterServer:
		return adapter
	default:
		return AdapterServer
	}
}

// NormalizeRoute maps a route label to itself when it is the registered
// pattern or UnmatchedRoute, and to UnmatchedRoute otherwise. A registered
// pattern always holds a method and a space (for example "GET /api/user").
// A bare prefix such as "/api/v2/" means the adapter found no route, so it
// becomes UnmatchedRoute too. The path never passes through.
func NormalizeRoute(route string) string {
	if route == "" {
		return UnmatchedRoute
	}

	if route == UnmatchedRoute {
		return UnmatchedRoute
	}

	if !strings.Contains(route, " ") {
		return UnmatchedRoute
	}

	return route
}
