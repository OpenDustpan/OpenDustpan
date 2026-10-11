// Package metrics writes server metrics in the Prometheus text format,
// version 0.0.4, with the standard library only.
//
// A Registry holds counters, gauges, histograms and callback gauges. It
// writes HELP, TYPE and sample lines with escaped labels in stable order.
// Every type is safe for concurrent use. An update costs one map lookup and
// one atomic operation. It holds no lock across I/O.
//
// Each labeled metric caps its series count. The author sets the cap with
// maxSeries at construction. An observation with a new label set past the cap
// is dropped and counted in Dropped. A caller bug that varies a label value
// thus cannot grow memory without bound. Size the cap for the label set. A
// route label holds one registered pattern per route, so a small cap fits.
//
// Callback gauges sample a function at scrape time. They suit runtime values
// from runtime/metrics. See Goroutines, HeapBytes and GCCycles. The caller
// registers them with RegisterFunc.
//
// Domain packages never import this package. Only dustpan.go and the HTTP
// middleware use it (ADR 32 decision 9).
package metrics

import (
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// DefaultMaxSeries bounds the series of a labeled metric when the author
// passes a maxSeries below one.
const DefaultMaxSeries = 256

// Registry holds metric families and writes them in the Prometheus text
// format. Create it with NewRegistry. It is safe for concurrent use.
type Registry struct {
	mu         sync.RWMutex
	names      map[string]struct{}
	counters   []*Counter
	gauges     []*Gauge
	histograms []*Histogram
	funcs      []funcGauge
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		mu:         sync.RWMutex{},
		names:      map[string]struct{}{},
		counters:   nil,
		gauges:     nil,
		histograms: nil,
		funcs:      nil,
	}
}

// NewCounter creates a counter family, adds it to the registry and returns
// it. labelNames names the labels in order. maxSeries bounds the series
// count. A maxSeries below one selects DefaultMaxSeries. It panics on an
// invalid or duplicate name, an empty help text, or an invalid or duplicate
// label name.
func (r *Registry) NewCounter(name, help string, labelNames []string, maxSeries int) *Counter {
	r.mu.Lock()
	defer r.mu.Unlock()
	checkFamilyName(r.names, name, help)
	counter := &Counter{
		name: name,
		help: help,
		vec:  newSeriesMap(name, checkLabelNames(labelNames), maxSeries),
	}
	r.counters = append(r.counters, counter)
	r.names[name] = struct{}{}

	return counter
}

// NewGauge creates a gauge family, adds it to the registry and returns it.
// labelNames names the labels in order. maxSeries bounds the series count. A
// maxSeries below one selects DefaultMaxSeries. It panics on an invalid or
// duplicate name, an empty help text, or an invalid or duplicate label name.
func (r *Registry) NewGauge(name, help string, labelNames []string, maxSeries int) *Gauge {
	r.mu.Lock()
	defer r.mu.Unlock()
	checkFamilyName(r.names, name, help)
	gauge := &Gauge{
		name: name,
		help: help,
		vec:  newSeriesMap(name, checkLabelNames(labelNames), maxSeries),
	}
	r.gauges = append(r.gauges, gauge)
	r.names[name] = struct{}{}

	return gauge
}

// NewHistogram creates a histogram family, adds it to the registry and
// returns it. buckets holds the finite upper bounds in increasing order. The
// registry adds the +Inf bucket itself. labelNames names the labels in
// order. maxSeries bounds the series count. A maxSeries below one selects
// DefaultMaxSeries. It panics on an invalid or duplicate name, an empty help
// text, an invalid, duplicate or "le" label name, or buckets that are empty,
// unordered or not finite.
func (r *Registry) NewHistogram(
	name, help string,
	buckets []float64,
	labelNames []string,
	maxSeries int,
) *Histogram {
	r.mu.Lock()
	defer r.mu.Unlock()
	checkFamilyName(r.names, name, help)
	histogram := &Histogram{
		name:       name,
		help:       help,
		buckets:    checkBuckets(buckets),
		labelNames: checkHistogramLabels(labelNames),
		maxSeries:  normalizeMaxSeries(maxSeries),
		series:     map[string]*histogramSeries{},
		mu:         sync.RWMutex{},
		dropped:    atomic.Uint64{},
	}
	r.histograms = append(r.histograms, histogram)
	r.names[name] = struct{}{}

	return histogram
}

// RegisterFunc adds a gauge that samples fn at scrape time. fn must be cheap
// and safe for concurrent use. It panics on an invalid or duplicate name, an
// empty help text or a nil fn.
func (r *Registry) RegisterFunc(name, help string, fn func() float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	checkFamilyName(r.names, name, help)
	if fn == nil {
		panic("metrics: " + name + " has a nil sample function")
	}
	r.funcs = append(r.funcs, funcGauge{name: name, help: help, fn: fn})
	r.names[name] = struct{}{}
}

// Write writes the registry in the Prometheus text format, version 0.0.4.
// Families appear sorted by name. Series appear sorted by labels. It returns
// an error when the writer fails.
func (r *Registry) Write(w io.Writer) error {
	r.mu.RLock()
	families := make([]family, 0, len(r.counters)+len(r.gauges)+len(r.histograms)+len(r.funcs))
	for _, counter := range r.counters {
		families = append(families, family{name: counter.name, write: counter.write})
	}
	for _, gauge := range r.gauges {
		families = append(families, family{name: gauge.name, write: gauge.write})
	}
	for _, histogram := range r.histograms {
		families = append(families, family{name: histogram.name, write: histogram.write})
	}
	for _, gauge := range r.funcs {
		families = append(families, family{name: gauge.name, write: gauge.write})
	}
	r.mu.RUnlock()
	slices.SortFunc(families, func(a, b family) int { return strings.Compare(a.name, b.name) })
	var output strings.Builder
	for _, item := range families {
		item.write(&output)
	}
	if _, err := io.WriteString(w, output.String()); err != nil {
		return fmt.Errorf("write metrics: %w", err)
	}

	return nil
}

// family is one metric family with its writer.
type family struct {
	name  string
	write func(sb *strings.Builder)
}

// funcGauge is a gauge sampled at scrape time.
type funcGauge struct {
	name string
	help string
	fn   func() float64
}

// write writes the gauge with its current value.
func (g funcGauge) write(sb *strings.Builder) {
	writeHead(sb, g.name, g.help, "gauge")
	sb.WriteString(g.name)
	sb.WriteString(" ")
	sb.WriteString(formatFloat(g.fn()))
	sb.WriteString("\n")
}

// Counter counts events, with one series per label set. Create it with
// Registry.NewCounter. It is safe for concurrent use.
type Counter struct {
	name string
	help string
	vec  *seriesMap
}

// Inc adds one to the series with these label values.
func (c *Counter) Inc(labelValues ...string) {
	c.Add(1, labelValues...)
}

// Add adds delta to the series with these label values. The delta is an
// integer count; fractional counts are unrepresentable. It drops the
// observation and counts it in Dropped when the label set is new and the
// series cap is reached.
func (c *Counter) Add(delta uint64, labelValues ...string) {
	series := c.vec.forValues(labelValues)
	if series == nil {
		return
	}
	series.bits.Add(delta)
}

// Dropped counts observations dropped by the series cap.
func (c *Counter) Dropped() uint64 {
	return c.vec.Dropped()
}

// write writes the counter family.
func (c *Counter) write(sb *strings.Builder) {
	writeHead(sb, c.name, c.help, "counter")
	for _, series := range c.vec.snapshot() {
		sb.WriteString(c.name)
		writeAttrs(sb, series.attrs)
		sb.WriteString(" ")
		sb.WriteString(strconv.FormatUint(series.bits, 10))
		sb.WriteString("\n")
	}
}

// Gauge holds a value that can rise and fall, with one series per label set.
// Create it with Registry.NewGauge. It is safe for concurrent use.
type Gauge struct {
	name string
	help string
	vec  *seriesMap
}

// Set replaces the series value. It drops the observation and counts it in
// Dropped when the label set is new and the series cap is reached.
func (g *Gauge) Set(value float64, labelValues ...string) {
	series := g.vec.forValues(labelValues)
	if series == nil {
		return
	}
	series.bits.Store(math.Float64bits(value))
}

// Add adds delta to the series value. It drops the observation and counts it
// in Dropped when the label set is new and the series cap is reached.
func (g *Gauge) Add(delta float64, labelValues ...string) {
	series := g.vec.forValues(labelValues)
	if series == nil {
		return
	}
	for {
		old := series.bits.Load()
		sum := math.Float64frombits(old) + delta
		if series.bits.CompareAndSwap(old, math.Float64bits(sum)) {
			return
		}
	}
}

// Inc adds one to the series value.
func (g *Gauge) Inc(labelValues ...string) {
	g.Add(1, labelValues...)
}

// Dec subtracts one from the series value.
func (g *Gauge) Dec(labelValues ...string) {
	g.Add(-1, labelValues...)
}

// Dropped counts observations dropped by the series cap.
func (g *Gauge) Dropped() uint64 {
	return g.vec.Dropped()
}

// write writes the gauge family.
func (g *Gauge) write(sb *strings.Builder) {
	writeHead(sb, g.name, g.help, "gauge")
	for _, series := range g.vec.snapshot() {
		sb.WriteString(g.name)
		writeAttrs(sb, series.attrs)
		sb.WriteString(" ")
		sb.WriteString(formatFloat(math.Float64frombits(series.bits)))
		sb.WriteString("\n")
	}
}

// Histogram samples observations into fixed buckets, with one series per
// label set. Create it with Registry.NewHistogram. It is safe for concurrent
// use.
type Histogram struct {
	name       string
	help       string
	buckets    []float64
	labelNames []string
	maxSeries  int
	mu         sync.RWMutex
	series     map[string]*histogramSeries
	dropped    atomic.Uint64
}

// histogramSeries holds the counts of one label set. counts has one slot per
// bucket plus one slot for +Inf.
type histogramSeries struct {
	values []string
	mu     sync.Mutex
	counts []uint64
	sum    float64
	total  uint64
}

// Observe adds one observation. It drops the observation and counts it in
// Dropped when the label set is new and the series cap is reached.
func (h *Histogram) Observe(value float64, labelValues ...string) {
	if len(labelValues) != len(h.labelNames) {
		panic("metrics: " + h.name + " needs " + strconv.Itoa(len(h.labelNames)) + " label values")
	}
	series, found := lookupSeries(&h.mu, h.series, seriesKey(labelValues), h.maxSeries,
		func() *histogramSeries {
			return &histogramSeries{
				values: slices.Clone(labelValues),
				counts: make([]uint64, len(h.buckets)+1),
				mu:     sync.Mutex{},
				sum:    0,
				total:  0,
			}
		})
	if !found {
		h.dropped.Add(1)

		return
	}
	index, _ := slices.BinarySearch(h.buckets, value)
	series.mu.Lock()
	series.counts[index]++
	series.sum += value
	series.total++
	series.mu.Unlock()
}

// Dropped counts observations dropped by the series cap.
func (h *Histogram) Dropped() uint64 {
	return h.dropped.Load()
}

// write writes the histogram family with cumulative buckets, the +Inf
// bucket, and the _sum and _count series.
func (h *Histogram) write(sb *strings.Builder) {
	writeHead(sb, h.name, h.help, "histogram")
	for _, series := range h.snapshot() {
		cumulative := uint64(0)
		for i, upper := range h.buckets {
			cumulative += series.counts[i]
			h.writeBucket(sb, series, formatFloat(upper), cumulative)
		}
		cumulative += series.counts[len(h.buckets)]
		h.writeBucket(sb, series, "+Inf", cumulative)
		sb.WriteString(h.name)
		sb.WriteString("_sum")
		writeAttrs(sb, series.attrs)
		sb.WriteString(" ")
		sb.WriteString(formatFloat(series.sum))
		sb.WriteString("\n")
		sb.WriteString(h.name)
		sb.WriteString("_count")
		writeAttrs(sb, series.attrs)
		sb.WriteString(" ")
		sb.WriteString(strconv.FormatUint(series.total, 10))
		sb.WriteString("\n")
	}
}

// writeBucket writes one bucket sample.
func (h *Histogram) writeBucket(sb *strings.Builder, series histogramPoint, upper string, count uint64) {
	sb.WriteString(h.name)
	sb.WriteString("_bucket")
	writeBucketAttrs(sb, series.attrs, upper)
	sb.WriteString(" ")
	sb.WriteString(strconv.FormatUint(count, 10))
	sb.WriteString("\n")
}

// histogramPoint is a copy of one series for output.
type histogramPoint struct {
	attrs  string
	counts []uint64
	sum    float64
	total  uint64
}

// snapshot copies the series sorted by labels.
func (h *Histogram) snapshot() []histogramPoint {
	h.mu.RLock()
	points := make([]histogramPoint, 0, len(h.series))
	for _, series := range h.series {
		series.mu.Lock()
		points = append(points, histogramPoint{
			attrs:  renderAttrs(h.labelNames, series.values),
			counts: slices.Clone(series.counts),
			sum:    series.sum,
			total:  series.total,
		})
		series.mu.Unlock()
	}
	h.mu.RUnlock()
	slices.SortFunc(points, func(a, b histogramPoint) int { return strings.Compare(a.attrs, b.attrs) })

	return points
}

// seriesMap holds the labeled series shared by counters and gauges.
type seriesMap struct {
	name       string
	labelNames []string
	maxSeries  int
	mu         sync.RWMutex
	series     map[string]*labelSeries
	dropped    atomic.Uint64
}

// labelSeries holds the value of one label set as an atomic word. Counters
// store the count. Gauges store the math.Float64bits of the value.
type labelSeries struct {
	values []string
	bits   atomic.Uint64
}

// newSeriesMap returns a series map for a family with these label names.
func newSeriesMap(name string, labelNames []string, maxSeries int) *seriesMap {
	return &seriesMap{
		name:       name,
		labelNames: labelNames,
		maxSeries:  normalizeMaxSeries(maxSeries),
		series:     map[string]*labelSeries{},
		mu:         sync.RWMutex{},
		dropped:    atomic.Uint64{},
	}
}

// Dropped counts observations dropped by the series cap.
func (m *seriesMap) Dropped() uint64 {
	return m.dropped.Load()
}

// forValues returns the series for these label values, creating it when
// absent. It counts the observation in dropped and returns nil when the
// label set is new and the map is at its cap.
func (m *seriesMap) forValues(labelValues []string) *labelSeries {
	if len(labelValues) != len(m.labelNames) {
		panic("metrics: " + m.name + " needs " + strconv.Itoa(len(m.labelNames)) + " label values")
	}
	series, found := lookupSeries(&m.mu, m.series, seriesKey(labelValues), m.maxSeries,
		func() *labelSeries {
			return &labelSeries{values: slices.Clone(labelValues), bits: atomic.Uint64{}}
		})
	if !found {
		m.dropped.Add(1)

		return nil
	}

	return series
}

// seriesSnapshot is a copy of one series for output.
type seriesSnapshot struct {
	attrs string
	bits  uint64
}

// snapshot copies the series sorted by labels.
func (m *seriesMap) snapshot() []seriesSnapshot {
	m.mu.RLock()
	out := make([]seriesSnapshot, 0, len(m.series))
	for _, series := range m.series {
		out = append(out, seriesSnapshot{
			attrs: renderAttrs(m.labelNames, series.values),
			bits:  series.bits.Load(),
		})
	}
	m.mu.RUnlock()
	slices.SortFunc(out, func(a, b seriesSnapshot) int { return strings.Compare(a.attrs, b.attrs) })

	return out
}

// lookupSeries returns the series for key. It creates the series with create
// when absent. It returns false without creating when the map holds
// maxSeries series already. Callers must not hold mu.
func lookupSeries[S any](mu *sync.RWMutex, all map[string]*S, key string, maxSeries int, create func() *S) (*S, bool) {
	mu.RLock()
	series, found := all[key]
	mu.RUnlock()
	if found {
		return series, true
	}
	mu.Lock()
	defer mu.Unlock()
	series, found = all[key]
	if found {
		return series, true
	}
	if len(all) >= maxSeries {
		return nil, false
	}
	series = create()
	all[key] = series

	return series, true
}

// seriesKey maps label values to a map key. It quotes each value, so the key
// is unique per label set.
func seriesKey(labelValues []string) string {
	if len(labelValues) == 0 {
		return ""
	}
	quoted := make([]string, len(labelValues))
	for i, value := range labelValues {
		quoted[i] = strconv.Quote(value)
	}

	return strings.Join(quoted, "\x00")
}

// normalizeMaxSeries selects DefaultMaxSeries for a maxSeries below one.
func normalizeMaxSeries(maxSeries int) int {
	if maxSeries < 1 {
		return DefaultMaxSeries
	}

	return maxSeries
}

// checkFamilyName panics on an empty help text or an invalid or duplicate
// family name.
func checkFamilyName(used map[string]struct{}, name, help string) {
	if !validMetricName(name) {
		panic("metrics: invalid metric name " + strconv.Quote(name))
	}
	if help == "" {
		panic("metrics: " + name + " needs help text")
	}
	if _, duplicate := used[name]; duplicate {
		panic("metrics: duplicate metric name " + name)
	}
}

// checkLabelNames returns a copy of the label names. It panics on an invalid
// or duplicate label name.
func checkLabelNames(labelNames []string) []string {
	for _, name := range labelNames {
		if !validLabelName(name) {
			panic("metrics: invalid label name " + strconv.Quote(name))
		}
	}
	checkDuplicateLabels(labelNames)

	return slices.Clone(labelNames)
}

// checkHistogramLabels returns a copy of the label names. It panics on an
// invalid, duplicate or "le" label name. "le" is the bucket label.
func checkHistogramLabels(labelNames []string) []string {
	names := checkLabelNames(labelNames)
	for _, name := range names {
		if name == "le" {
			panic(`metrics: histogram label name "le" is reserved for buckets`)
		}
	}

	return names
}

// checkDuplicateLabels panics on a duplicate label name.
func checkDuplicateLabels(labelNames []string) {
	seen := make(map[string]struct{}, len(labelNames))
	for _, name := range labelNames {
		if _, duplicate := seen[name]; duplicate {
			panic("metrics: duplicate label name " + strconv.Quote(name))
		}
		seen[name] = struct{}{}
	}
}

// checkBuckets returns a copy of the buckets. It panics on buckets that are
// empty, unordered or not finite.
func checkBuckets(buckets []float64) []float64 {
	out := slices.Clone(buckets)
	for i, upper := range out {
		if math.IsNaN(upper) || math.IsInf(upper, 0) {
			panic("metrics: histogram buckets must be finite")
		}
		if i > 0 && upper <= out[i-1] {
			panic("metrics: histogram buckets must increase")
		}
	}
	if len(out) == 0 {
		panic("metrics: histogram needs at least one bucket")
	}

	return out
}

// validMetricName reports whether s fits [a-zA-Z_:][a-zA-Z0-9_:]*.
func validMetricName(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if !validMetricChar(s[i], i == 0) {
			return false
		}
	}

	return true
}

// validMetricChar reports whether c may appear in a metric name.
func validMetricChar(c byte, first bool) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_', c == ':':
		return true
	case c >= '0' && c <= '9':
		return !first
	default:
		return false
	}
}

// validLabelName reports whether s fits [a-zA-Z_][a-zA-Z0-9_]*.
func validLabelName(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
		case c >= '0' && c <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}

	return true
}

// writeHead writes the HELP and TYPE lines of a family.
func writeHead(sb *strings.Builder, name, help, typ string) {
	sb.WriteString("# HELP ")
	sb.WriteString(name)
	sb.WriteString(" ")
	sb.WriteString(escapeHelp(help))
	sb.WriteString("\n# TYPE ")
	sb.WriteString(name)
	sb.WriteString(" ")
	sb.WriteString(typ)
	sb.WriteString("\n")
}

// writeAttrs writes the label set in braces, or nothing when attrs is empty.
func writeAttrs(sb *strings.Builder, attrs string) {
	if attrs == "" {
		return
	}
	sb.WriteString("{")
	sb.WriteString(attrs)
	sb.WriteString("}")
}

// writeBucketAttrs writes the label set with the bucket bound appended.
func writeBucketAttrs(sb *strings.Builder, attrs, upper string) {
	sb.WriteString("{")
	if attrs != "" {
		sb.WriteString(attrs)
		sb.WriteString(",")
	}
	sb.WriteString(`le="`)
	sb.WriteString(upper)
	sb.WriteString(`"}`)
}

// renderAttrs renders label names and values as k="v" pairs for output and
// sorting.
func renderAttrs(labelNames, labelValues []string) string {
	var sb strings.Builder
	for i, name := range labelNames {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(name)
		sb.WriteString(`="`)
		sb.WriteString(escapeLabelValue(labelValues[i]))
		sb.WriteString(`"`)
	}

	return sb.String()
}

// escapeLabelValue escapes a label value for the text format.
func escapeLabelValue(s string) string {
	if !strings.ContainsAny(s, "\\\"\n") {
		return s
	}
	var sb strings.Builder
	for i := range len(s) {
		switch s[i] {
		case '\\':
			sb.WriteString(`\\`)
		case '"':
			sb.WriteString(`\"`)
		case '\n':
			sb.WriteString(`\n`)
		default:
			sb.WriteByte(s[i])
		}
	}

	return sb.String()
}

// escapeHelp escapes help text for the text format.
func escapeHelp(s string) string {
	if !strings.ContainsAny(s, "\\\n") {
		return s
	}
	var sb strings.Builder
	for i := range len(s) {
		switch s[i] {
		case '\\':
			sb.WriteString(`\\`)
		case '\n':
			sb.WriteString(`\n`)
		default:
			sb.WriteByte(s[i])
		}
	}

	return sb.String()
}

// formatFloat formats a sample value for the text format.
func formatFloat(value float64) string {
	switch {
	case math.IsNaN(value):
		return "NaN"
	case math.IsInf(value, 1):
		return "+Inf"
	case math.IsInf(value, -1):
		return "-Inf"
	default:
		return strconv.FormatFloat(value, 'g', -1, 64)
	}
}
