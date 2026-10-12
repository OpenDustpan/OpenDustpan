package soak_test

import (
	"strings"
	"testing"

	"opendustpan.org/opendustpan/soak"
)

// soakSample answers one sample with every field set. Heap and FDs stay
// flat; DBConns stays -1 (missing): the server exports no database pool
// gauge.
func soakSample(elapsed, goroutines float64, runs, failures int) soak.Sample {
	rate := 0.0
	if runs > 0 {
		rate = float64(failures) / float64(runs)
	}

	return soak.Sample{
		ElapsedSeconds: elapsed,
		Goroutines:     goroutines,
		HeapBytes:      1 << 20,
		FDs:            30,
		DBConns:        -1,
		Runs:           runs,
		Failures:       failures,
		ErrorRate:      rate,
	}
}

func TestSlopePerMinuteFlat(t *testing.T) {
	t.Parallel()

	times := []float64{0, 10, 20, 30}
	values := []float64{50, 50, 50, 50}

	if slope := soak.SlopePerMinute(times, values); slope != 0 {
		t.Fatalf("flat slope = %v, want 0", slope)
	}
}

func TestSlopePerMinuteRisesOnePerMinute(t *testing.T) {
	t.Parallel()

	times := []float64{0, 60, 120, 180}
	values := []float64{10, 11, 12, 13}

	slope := soak.SlopePerMinute(times, values)
	if slope < 0.99 || slope > 1.01 {
		t.Fatalf("rising slope = %v, want about 1.0 per minute", slope)
	}
}

func TestSlopePerMinuteNeedsTwoPoints(t *testing.T) {
	t.Parallel()

	if slope := soak.SlopePerMinute([]float64{0}, []float64{7}); slope != 0 {
		t.Fatalf("single point slope = %v, want 0", slope)
	}
}

func TestParseMetricsReadsGauges(t *testing.T) {
	t.Parallel()

	body := "# HELP dustpan_go_goroutines Goroutine count.\n" +
		"# TYPE dustpan_go_goroutines gauge\n" +
		"dustpan_go_goroutines 42\n" +
		"# HELP dustpan_go_heap_bytes Live heap size in bytes.\n" +
		"# TYPE dustpan_go_heap_bytes gauge\n" +
		"dustpan_go_heap_bytes 123456\n"

	gauges, err := soak.ParseMetrics(body)
	if err != nil {
		t.Fatalf("parse metrics: %v", err)
	}

	if gauges.Goroutines != 42 || gauges.HeapBytes != 123456 {
		t.Fatalf("gauges = %v, %v, want 42, 123456", gauges.Goroutines, gauges.HeapBytes)
	}

	if !soak.Missing(gauges.DBConns) {
		t.Fatalf("database connections = %v, want missing: the server exports no pool gauge", gauges.DBConns)
	}
}

func TestParseMetricsRejectsEmptyBody(t *testing.T) {
	t.Parallel()

	if _, err := soak.ParseMetrics("# HELP x help\n# TYPE x gauge\n"); err == nil {
		t.Fatal("empty body parsed without an error")
	}
}

func TestParseMetricsReadsTimestampedLines(t *testing.T) {
	t.Parallel()

	body := "dustpan_go_goroutines 42 1234567890\ndustpan_go_heap_bytes 123456 1234567890\n"

	gauges, err := soak.ParseMetrics(body)
	if err != nil {
		t.Fatalf("parse timestamped metrics: %v", err)
	}

	if gauges.Goroutines != 42 || gauges.HeapBytes != 123456 {
		t.Fatalf("gauges = %v, %v, want 42, 123456", gauges.Goroutines, gauges.HeapBytes)
	}
}

func TestParseMetricsReadsSingleGauge(t *testing.T) {
	t.Parallel()

	gauges, err := soak.ParseMetrics("dustpan_go_goroutines 42\n")
	if err != nil {
		t.Fatalf("parse goroutines-only body: %v", err)
	}

	if gauges.Goroutines != 42 {
		t.Fatalf("goroutines = %v, want 42", gauges.Goroutines)
	}

	if !soak.Missing(gauges.HeapBytes) {
		t.Fatalf("heap = %v, want missing", gauges.HeapBytes)
	}
}

func TestAssessPassesFlatRun(t *testing.T) {
	t.Parallel()

	samples := []soak.Sample{
		soakSample(0, 20, 8, 0),
		soakSample(10, 20, 8, 0),
		soakSample(20, 20, 8, 0),
		soakSample(30, 20, 8, 0),
		soakSample(40, 20, 8, 0),
		soakSample(50, 20, 8, 0),
	}

	assessed, err := soak.Assess("sqlite", samples)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}

	if !assessed.Pass {
		t.Fatalf("flat run did not pass: %+v", assessed)
	}

	for _, metric := range assessed.Metrics {
		if metric.Name == soak.MetricDBConns && !metric.Skipped {
			t.Fatalf("database verdict is not skipped: %+v", metric)
		}
	}
}

func TestAssessFailsGrowingGoroutines(t *testing.T) {
	t.Parallel()

	samples := []soak.Sample{
		soakSample(0, 20, 8, 0),
		soakSample(10, 20, 8, 0),
		soakSample(20, 30, 8, 0),
		soakSample(30, 40, 8, 0),
		soakSample(40, 50, 8, 0),
		soakSample(50, 60, 8, 0),
	}

	assessed, err := soak.Assess("sqlite", samples)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}

	if assessed.Pass {
		t.Fatal("growing run passed")
	}
}

func TestAssessFailsErrorRate(t *testing.T) {
	t.Parallel()

	samples := []soak.Sample{
		soakSample(0, 20, 10, 1),
		soakSample(10, 20, 10, 1),
		soakSample(20, 20, 10, 1),
		soakSample(30, 20, 10, 1),
	}

	assessed, err := soak.Assess("sqlite", samples)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}

	if assessed.ErrorPass || assessed.Pass {
		t.Fatalf("error run passed: %+v", assessed)
	}
}

func TestAssessFailsGrowingFDs(t *testing.T) {
	t.Parallel()

	samples := []soak.Sample{
		soakSample(0, 20, 8, 0),
		soakSample(10, 20, 8, 0),
		soakSample(20, 20, 8, 0),
		soakSample(30, 20, 8, 0),
		soakSample(40, 20, 8, 0),
		soakSample(50, 20, 8, 0),
	}
	for i := range samples {
		samples[i].FDs = float64(30 + i*10)
	}

	assessed, err := soak.Assess("sqlite", samples)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}

	if assessed.Pass {
		t.Fatal("growing FD run passed")
	}
}

func TestAssessFailsGrowingHeap(t *testing.T) {
	t.Parallel()

	samples := []soak.Sample{
		soakSample(0, 20, 8, 0),
		soakSample(10, 20, 8, 0),
		soakSample(20, 20, 8, 0),
		soakSample(30, 20, 8, 0),
		soakSample(40, 20, 8, 0),
		soakSample(50, 20, 8, 0),
	}
	for i := range samples {
		samples[i].HeapBytes = float64(1<<20) * float64(1+i)
	}

	assessed, err := soak.Assess("sqlite", samples)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}

	if assessed.Pass {
		t.Fatal("growing heap run passed")
	}
}

func TestAssessPassesHeapAtThreshold(t *testing.T) {
	t.Parallel()

	// A slope at exactly the threshold still passes: only a slope above
	// the threshold fails the run.
	samples := []soak.Sample{
		soakSample(0, 20, 8, 0),
		soakSample(60, 20, 8, 0),
		soakSample(120, 20, 8, 0),
		soakSample(180, 20, 8, 0),
	}
	for i := range samples {
		samples[i].HeapBytes = float64(1<<20) * float64(1+i)
	}

	assessed, err := soak.Assess("sqlite", samples)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}

	if !assessed.Pass {
		t.Fatalf("threshold heap run failed: %+v", assessed)
	}
}

func TestAssessReportsTooFewSamples(t *testing.T) {
	t.Parallel()

	samples := []soak.Sample{
		soakSample(0, 20, 8, 0),
		soakSample(10, 20, 8, 0),
		soakSample(20, 20, 8, 0),
	}

	assessed, err := soak.Assess("sqlite", samples)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}

	if assessed.Pass {
		t.Fatal("short run passed")
	}

	if !strings.Contains(assessed.Note, "too few samples") {
		t.Fatalf("note lacks too few samples: %q", assessed.Note)
	}
}

func TestAssessIgnoresWarmUp(t *testing.T) {
	t.Parallel()

	// A spike in the first third only must not fail the run.
	samples := []soak.Sample{
		soakSample(0, 200, 8, 0),
		soakSample(10, 200, 8, 0),
		soakSample(20, 20, 8, 0),
		soakSample(30, 20, 8, 0),
		soakSample(40, 20, 8, 0),
		soakSample(50, 20, 8, 0),
	}

	assessed, err := soak.Assess("sqlite", samples)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}

	if !assessed.Pass {
		t.Fatalf("warm-up spike failed the run: %+v", assessed)
	}
}

func TestSummaryNamesVerdictPerMetric(t *testing.T) {
	t.Parallel()

	assessed, err := soak.Assess("sqlite", []soak.Sample{
		soakSample(0, 20, 8, 0),
		soakSample(10, 20, 8, 0),
		soakSample(20, 20, 8, 0),
		soakSample(30, 20, 8, 0),
	})
	if err != nil {
		t.Fatalf("assess: %v", err)
	}

	summary := soak.Summary(assessed)

	for _, want := range []string{"goroutines", "heap_bytes", "fds", "db_connections", "Overall: PASS"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary lacks %q:\n%s", want, summary)
		}
	}
}
