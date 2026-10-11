package metrics_test

import (
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/internal/metrics"
)

// wantFormat is the exact Prometheus text output of writeFormatFixture.
const wantFormat = `# HELP demo_build Build.
# TYPE demo_build gauge
demo_build 42
# HELP demo_duration_seconds Time spent.
# TYPE demo_duration_seconds histogram
demo_duration_seconds_bucket{route="/a",le="0.5"} 2
demo_duration_seconds_bucket{route="/a",le="2"} 3
demo_duration_seconds_bucket{route="/a",le="+Inf"} 4
demo_duration_seconds_sum{route="/a"} 5.75
demo_duration_seconds_count{route="/a"} 4
demo_duration_seconds_bucket{route="/b",le="0.5"} 0
demo_duration_seconds_bucket{route="/b",le="2"} 1
demo_duration_seconds_bucket{route="/b",le="+Inf"} 1
demo_duration_seconds_sum{route="/b"} 2
demo_duration_seconds_count{route="/b"} 1
# HELP demo_errors_total Errors.
# TYPE demo_errors_total counter
demo_errors_total 4
# HELP demo_in_flight In flight.\nPer route.
# TYPE demo_in_flight gauge
demo_in_flight{route="/a"} 2.5
demo_in_flight{route="/b"} -0.5
# HELP demo_requests_total Requests.
# TYPE demo_requests_total counter
demo_requests_total{method="GET",route="/a"} 2
demo_requests_total{method="GET",route="/c\nd\"e\\f"} 1
demo_requests_total{method="POST",route="/b"} 3
`

// writeFormatFixture builds a registry with one of each metric type,
// observes fixed values and returns the text output.
func writeFormatFixture(t *testing.T) string {
	t.Helper()

	reg := metrics.NewRegistry()
	requests := reg.NewCounter("demo_requests_total", "Requests.", []string{"method", "route"}, 16)
	failed := reg.NewCounter("demo_errors_total", "Errors.", nil, 0)
	flight := reg.NewGauge("demo_in_flight", "In flight.\nPer route.", []string{"route"}, 16)
	duration := reg.NewHistogram(
		"demo_duration_seconds",
		"Time spent.",
		[]float64{0.5, 2},
		[]string{"route"},
		16,
	)
	reg.RegisterFunc("demo_build", "Build.", func() float64 { return 42 })

	requests.Inc("GET", "/a")
	requests.Inc("GET", "/a")
	requests.Add(3, "POST", "/b")
	requests.Inc("GET", "/c\nd\"e\\f")
	failed.Add(4)
	flight.Set(2.5, "/a")
	flight.Set(-0.5, "/b")
	flight.Inc("/a")
	flight.Dec("/a")
	duration.Observe(0.25, "/a")
	duration.Observe(0.5, "/a")
	duration.Observe(1, "/a")
	duration.Observe(4, "/a")
	duration.Observe(2, "/b")

	var out strings.Builder
	if err := reg.Write(&out); err != nil {
		t.Fatalf("write: %v", err)
	}

	return out.String()
}

func TestFormatStable(t *testing.T) {
	t.Parallel()

	first := writeFormatFixture(t)
	if first != wantFormat {
		t.Fatalf("output differs:\ngot:\n%s\nwant:\n%s", first, wantFormat)
	}
	if second := writeFormatFixture(t); second != first {
		t.Fatalf("output is not stable across runs:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestFormatParses(t *testing.T) {
	t.Parallel()

	samples := checkFormat(t, writeFormatFixture(t))
	for _, want := range []sample{
		{name: "demo_requests_total", attrs: map[string]string{"method": "GET", "route": "/a"}, value: 2},
		{
			name:  "demo_requests_total",
			attrs: map[string]string{"method": "GET", "route": "/c\nd\"e\\f"},
			value: 1,
		},
		{name: "demo_duration_seconds_sum", attrs: map[string]string{"route": "/a"}, value: 5.75},
	} {
		got, found := findSample(samples, want.name, want.attrs)
		if !found {
			t.Errorf("sample %q %v missing", want.name, want.attrs)
		} else if got != want.value {
			t.Errorf("sample %q %v = %v, want %v", want.name, want.attrs, got, want.value)
		}
	}
}

// findSample returns the value of the sample with this name and label set.
func findSample(samples []sample, name string, attrs map[string]string) (float64, bool) {
	for _, item := range samples {
		if item.name == name && maps.Equal(item.attrs, attrs) {
			return item.value, true
		}
	}

	return 0, false
}

func TestCardinalityCap(t *testing.T) {
	t.Parallel()

	reg := metrics.NewRegistry()
	counter := reg.NewCounter("capped_total", "Capped.", []string{"route"}, 2)
	counter.Inc("/a")
	counter.Inc("/b")
	counter.Inc("/c")
	counter.Inc("/c")
	counter.Inc("/a")
	if got := counter.Dropped(); got != 2 {
		t.Errorf("Dropped() = %d, want 2", got)
	}

	histogram := reg.NewHistogram("capped_seconds", "Capped.", []float64{1}, []string{"route"}, 1)
	histogram.Observe(0.5, "/a")
	histogram.Observe(0.5, "/b")
	if got := histogram.Dropped(); got != 1 {
		t.Errorf("Dropped() = %d, want 1", got)
	}

	var out strings.Builder
	if err := reg.Write(&out); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := out.String()
	for _, want := range []string{`capped_total{route="/a"} 2`, `capped_total{route="/b"} 1`} {
		if !strings.Contains(got, want) {
			t.Errorf("output misses %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, `"/c"`) {
		t.Errorf("output holds the dropped series:\n%s", got)
	}
}

func TestConcurrentUse(t *testing.T) {
	t.Parallel()

	reg := metrics.NewRegistry()
	counter := reg.NewCounter("race_total", "Race.", []string{"worker"}, 64)
	gauge := reg.NewGauge("race_gauge", "Race.", nil, 0)
	histogram := reg.NewHistogram("race_seconds", "Race.", []float64{1, 10}, nil, 0)

	const workers = 8
	const perWorker = 250

	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for range workers {
		wg.Go(func() {
			for range perWorker {
				counter.Inc("w")
				gauge.Add(1)
				gauge.Add(-1)
				histogram.Observe(0.5)
				if err := reg.Write(io.Discard); err != nil {
					errs <- err

					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent write: %v", err)
	}

	var out strings.Builder
	if err := reg.Write(&out); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		`race_total{worker="w"} 2000`,
		"race_gauge 0",
		"race_seconds_count 2000",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output misses %q:\n%s", want, got)
		}
	}
}

func TestInvalidMetricsPanic(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		make func(reg *metrics.Registry)
	}{
		{"bad counter name", func(reg *metrics.Registry) { reg.NewCounter("9bad", "Help.", nil, 0) }},
		{"empty help", func(reg *metrics.Registry) { reg.NewCounter("ok_total", "", nil, 0) }},
		{
			"duplicate name",
			func(reg *metrics.Registry) {
				reg.NewCounter("dup_total", "First.", nil, 0)
				reg.NewGauge("dup_total", "Second.", nil, 0)
			},
		},
		{"bad label name", func(reg *metrics.Registry) { reg.NewCounter("x_total", "Help.", []string{"9x"}, 0) }},
		{"duplicate label", func(reg *metrics.Registry) { reg.NewCounter("y_total", "Help.", []string{"a", "a"}, 0) }},
		{
			"reserved bucket label",
			func(reg *metrics.Registry) { reg.NewHistogram("h", "Help.", []float64{1}, []string{"le"}, 0) },
		},
		{"empty buckets", func(reg *metrics.Registry) { reg.NewHistogram("h2", "Help.", nil, nil, 0) }},
		{"unordered buckets", func(reg *metrics.Registry) { reg.NewHistogram("h3", "Help.", []float64{2, 1}, nil, 0) }},
		{"nil sample function", func(reg *metrics.Registry) { reg.RegisterFunc("f", "Help.", nil) }},
		{
			"wrong label arity",
			func(reg *metrics.Registry) { reg.NewCounter("ar_total", "Help.", []string{"a"}, 0).Inc() },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			defer func() {
				if recover() == nil {
					t.Error("constructor did not panic")
				}
			}()
			tc.make(metrics.NewRegistry())
		})
	}
}

func TestRuntimeValues(t *testing.T) {
	t.Parallel()

	if got := metrics.Goroutines(); got < 1 {
		t.Errorf("Goroutines() = %v, want at least 1", got)
	}
	if got := metrics.HeapBytes(); got <= 0 {
		t.Errorf("HeapBytes() = %v, want above zero", got)
	}
	if got := metrics.GCCycles(); got < 0 {
		t.Errorf("GCCycles() = %v, want zero or more", got)
	}
}

// sample is one parsed sample line.
type sample struct {
	name  string
	attrs map[string]string
	value float64
}

// checkFormat parses output as Prometheus text version 0.0.4 and fails the
// test on any shape violation. It is a small reader for this test only. It
// returns the parsed samples.
func checkFormat(t *testing.T, output string) []sample {
	t.Helper()

	helps, types, lines := splitComments(t, output)
	samples := parseSamples(t, lines)
	checkSamples(t, samples, helps, types)
	checkHistograms(t, samples, helps, types)

	return samples
}

// splitComments records HELP and TYPE lines and returns the sample lines.
func splitComments(t *testing.T, output string) (map[string]bool, map[string]string, []string) {
	t.Helper()

	helps := map[string]bool{}
	types := map[string]string{}
	var lines []string
	for line := range strings.SplitSeq(output, "\n") {
		if parseComment(t, line, helps, types) {
			lines = append(lines, line)
		}
	}

	return helps, types, lines
}

// parseComment records one HELP or TYPE line and reports whether line is a
// sample line.
func parseComment(t *testing.T, line string, helps map[string]bool, types map[string]string) bool {
	t.Helper()

	switch {
	case line == "":
		return false
	case strings.HasPrefix(line, "# HELP "):
		addHelp(t, line, helps)

		return false
	case strings.HasPrefix(line, "# TYPE "):
		addType(t, line, types)

		return false
	case strings.HasPrefix(line, "#"):
		t.Fatalf("unknown comment %q", line)

		return false
	default:
		return true
	}
}

// addHelp records one HELP line.
func addHelp(t *testing.T, line string, helps map[string]bool) {
	t.Helper()

	name, _, _ := strings.Cut(strings.TrimPrefix(line, "# HELP "), " ")
	if name == "" || helps[name] {
		t.Fatalf("bad HELP line %q", line)
	}
	helps[name] = true
}

// addType records one TYPE line.
func addType(t *testing.T, line string, types map[string]string) {
	t.Helper()

	name, typ, _ := strings.Cut(strings.TrimPrefix(line, "# TYPE "), " ")
	switch typ {
	case "counter", "gauge", "histogram":
		types[name] = typ
	default:
		t.Fatalf("bad TYPE line %q", line)
	}
}

// parseSamples parses every sample line.
func parseSamples(t *testing.T, lines []string) []sample {
	t.Helper()

	samples := make([]sample, 0, len(lines))
	for _, line := range lines {
		samples = append(samples, parseSample(t, line))
	}

	return samples
}

// checkSamples fails the test when a sample misses HELP or TYPE.
func checkSamples(t *testing.T, samples []sample, helps map[string]bool, types map[string]string) {
	t.Helper()

	for _, item := range samples {
		base := splitSeriesName(item.name).base
		if types[base] == "" || !helps[base] {
			t.Fatalf("sample %q misses HELP or TYPE", item.name)
		}
	}
}

// histogramTables holds the series sets of histogram families.
type histogramTables struct {
	bounds map[string]map[string]bool
	sums   map[string]map[string]bool
	counts map[string]map[string]bool
}

// checkHistograms fails the test when a histogram misses the +Inf bucket or
// a matching _sum and _count series.
func checkHistograms(t *testing.T, samples []sample, helps map[string]bool, types map[string]string) {
	t.Helper()

	tables := collectHistogramSeries(t, samples, types)
	for base, typ := range types {
		if !helps[base] {
			t.Fatalf("family %q misses HELP", base)
		}
		if typ != "histogram" {
			continue
		}
		checkHistogramBuckets(t, base, tables)
	}
}

// checkHistogramBuckets fails the test when the family misses the +Inf
// bucket or a matching _sum and _count series.
func checkHistogramBuckets(t *testing.T, base string, tables histogramTables) {
	t.Helper()

	if !tables.bounds[base]["+Inf"] {
		t.Fatalf("histogram %q misses the +Inf bucket", base)
	}
	for key, hasSum := range tables.sums[base] {
		if !hasSum || !tables.counts[base][key] {
			t.Fatalf("histogram %q misses _sum or _count for %q", base, key)
		}
	}
}

// collectHistogramSeries groups the bucket, sum and count series of
// histogram families.
func collectHistogramSeries(t *testing.T, samples []sample, types map[string]string) histogramTables {
	t.Helper()

	tables := histogramTables{
		bounds: map[string]map[string]bool{},
		sums:   map[string]map[string]bool{},
		counts: map[string]map[string]bool{},
	}
	for _, item := range samples {
		id := splitSeriesName(item.name)
		if id.kind == "" {
			continue
		}
		if types[id.base] != "histogram" {
			t.Fatalf("sample %q has a histogram suffix on a %s", item.name, types[id.base])
		}
		collectHistogramSample(t, item, id, tables)
	}

	return tables
}

// collectHistogramSample records one bucket, sum or count series.
func collectHistogramSample(t *testing.T, item sample, id seriesID, tables histogramTables) {
	t.Helper()

	key := withoutBound(item.attrs)
	switch id.kind {
	case "bucket":
		bound, ok := item.attrs["le"]
		if !ok {
			t.Fatalf("bucket %q misses the le label", item.name)
		}
		markSeries(tables.bounds, id.base, bound, true)
		markSeries(tables.sums, id.base, key, false)
	case "sum":
		markSeries(tables.sums, id.base, key, true)
	case "count":
		markSeries(tables.counts, id.base, key, true)
	default:
		t.Fatalf("unknown series kind %q", id.kind)
	}
}

// markSeries records one series in a family table, creating it when absent.
func markSeries(table map[string]map[string]bool, base, key string, value bool) {
	if table[base] == nil {
		table[base] = map[string]bool{}
	}
	table[base][key] = value
}

// parseSample parses one sample line into its name, labels and value.
func parseSample(t *testing.T, line string) sample {
	t.Helper()

	end := strings.IndexAny(line, "{ ")
	if end <= 0 {
		t.Fatalf("bad sample line %q", line)
	}
	name := line[:end]
	rest := line[end:]
	attrs := map[string]string{}
	if rest[0] == '{' {
		var ok bool
		attrs, rest, ok = parseAttrSet(t, line, rest[1:])
		if !ok || !strings.HasPrefix(rest, " ") {
			t.Fatalf("bad sample line %q", line)
		}
		rest = rest[1:]
	} else {
		rest = rest[1:]
	}
	if rest == "" {
		t.Fatalf("bad sample line %q", line)
	}
	value, err := strconv.ParseFloat(rest, 64)
	if err != nil {
		t.Fatalf("bad value in %q: %v", line, err)
	}

	return sample{name: name, attrs: attrs, value: value}
}

// parseAttrSet parses k="v" pairs up to the closing brace and reports the
// rest of the line.
func parseAttrSet(t *testing.T, line, s string) (map[string]string, string, bool) {
	t.Helper()

	out := map[string]string{}
	for {
		key, rest, found := strings.Cut(s, "=")
		if !found || !validKey(key) || rest == "" || rest[0] != '"' {
			t.Fatalf("bad labels in %q", line)
		}
		parsed := parseValue(rest[1:])
		if !parsed.ok || parsed.rest == "" {
			t.Fatalf("bad labels in %q", line)
		}
		out[key] = parsed.value
		switch parsed.rest[0] {
		case ',':
			s = parsed.rest[1:]
			if s == "" {
				t.Fatalf("bad labels in %q", line)
			}
		case '}':
			return out, parsed.rest[1:], true
		default:
			t.Fatalf("bad labels in %q", line)
		}
	}
}

// quotedValue is one parsed quoted value with the rest of the line.
type quotedValue struct {
	value string
	rest  string
	ok    bool
}

// parseValue parses one quoted value with backslash escapes and reports the
// rest of the line.
func parseValue(s string) quotedValue {
	var val strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			if i+1 >= len(s) {
				return quotedValue{value: "", rest: "", ok: false}
			}
			i++
			switch s[i] {
			case '\\', '"':
				val.WriteByte(s[i])
			case 'n':
				val.WriteByte('\n')
			default:
				return quotedValue{value: "", rest: "", ok: false}
			}
		case '"':
			return quotedValue{value: val.String(), rest: s[i+1:], ok: true}
		default:
			val.WriteByte(s[i])
		}
	}

	return quotedValue{value: "", rest: "", ok: false}
}

// validKey reports whether s may be a label name in the parsed output.
func validKey(s string) bool {
	return s != "" && !strings.ContainsAny(s, "{}=,\" ")
}

// seriesID names a sample: its family and its histogram suffix, if any.
type seriesID struct {
	base string
	kind string
}

// splitSeriesName splits a sample name into its family and its histogram
// suffix, if any.
func splitSeriesName(name string) seriesID {
	switch {
	case strings.HasSuffix(name, "_bucket"):
		return seriesID{base: strings.TrimSuffix(name, "_bucket"), kind: "bucket"}
	case strings.HasSuffix(name, "_sum"):
		return seriesID{base: strings.TrimSuffix(name, "_sum"), kind: "sum"}
	case strings.HasSuffix(name, "_count"):
		return seriesID{base: strings.TrimSuffix(name, "_count"), kind: "count"}
	default:
		return seriesID{base: name, kind: ""}
	}
}

// withoutBound renders a label set without the le label.
func withoutBound(attrs map[string]string) string {
	var sb strings.Builder
	first := true
	for _, key := range slices.Sorted(maps.Keys(attrs)) {
		if key == "le" {
			continue
		}
		if !first {
			sb.WriteString(",")
		}
		first = false
		sb.WriteString(key)
		sb.WriteString("=")
		sb.WriteString(strconv.Quote(attrs[key]))
	}

	return sb.String()
}
