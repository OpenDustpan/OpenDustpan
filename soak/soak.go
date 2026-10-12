package soak

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// Sampling and growth thresholds. The driver samples every SampleInterval.
// A slope over the last two thirds of the samples fails the run when it
// passes the matching per-minute threshold. Section "Growth rule" in
// docs/guides/soak.md names the same numbers.
const (
	SampleInterval         = 10 * time.Second
	MaxGoroutinesPerMinute = 1.0
	MaxHeapBytesPerMinute  = 1048576.0
	MaxFDsPerMinute        = 1.0
	MaxDBConnsPerMinute    = 1.0
	// MaxErrorRate bounds failed CLI runs over total CLI runs.
	MaxErrorRate = 0.01
	// MinSamples is the smallest sample count Assess accepts. A 60 s smoke
	// run at the 10 s interval holds about six samples, so four is enough
	// for a slope while shorter runs report "too few samples".
	MinSamples = 4
	// missing marks a sample value the run could not read: file descriptors
	// off Linux, or database connections, which the server never exports.
	missing = -1.0
	// secondsPerMinute scales a per-second slope to a per-minute slope.
	secondsPerMinute = 60.0
	// minSlopePoints is the smallest point count SlopePerMinute accepts.
	minSlopePoints = 2
)

// Metric names in verdicts and in the summary.
const (
	MetricGoroutines = "goroutines"
	MetricHeapBytes  = "heap_bytes"
	MetricFDs        = "fds"
	MetricDBConns    = "db_connections"
)

// errNoMetrics reports a metrics body with neither gauge in it.
var errNoMetrics = errors.New("metrics body holds no goroutine or heap gauge")

// errNoSamples reports an assessment of an empty sample list.
var errNoSamples = errors.New("no samples to assess")

// Sample is one 10 s observation of the server process with the CLI error
// count of its interval. FDs and DBConns hold missing when the run could
// not read them. Runs and Failures count CLI runs that ended in the
// interval; ErrorRate is Failures over Runs, or zero with no runs.
type Sample struct {
	ElapsedSeconds float64
	Goroutines     float64
	HeapBytes      float64
	FDs            float64
	DBConns        float64
	Runs           int
	Failures       int
	ErrorRate      float64
}

// Missing reports whether the run could not read a value for this series.
func Missing(value float64) bool {
	return value < 0
}

// Gauges holds the parsed server gauges of one scrape. DBConns always
// holds missing: the server exports no database pool gauge and neither
// store exposes sql.DBStats, so that series always stays empty.
type Gauges struct {
	Goroutines float64
	HeapBytes  float64
	DBConns    float64
}

// ParseMetrics reads the goroutine and heap gauges out of one Prometheus
// text body from GET /api/server/metrics. It returns errNoMetrics when the
// body holds neither gauge.
func ParseMetrics(body string) (Gauges, error) {
	gauges := Gauges{Goroutines: missing, HeapBytes: missing, DBConns: missing}

	for line := range strings.Lines(strings.TrimSpace(body)) {
		if strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < minSlopePoints {
			continue
		}

		value, parseErr := strconv.ParseFloat(fields[1], 64)
		if parseErr != nil {
			continue
		}

		switch fields[0] {
		case "dustpan_go_goroutines":
			gauges.Goroutines = value
		case "dustpan_go_heap_bytes":
			gauges.HeapBytes = value
		default:
		}
	}

	if Missing(gauges.Goroutines) && Missing(gauges.HeapBytes) {
		return Gauges{Goroutines: missing, HeapBytes: missing, DBConns: missing}, errNoMetrics
	}

	return gauges, nil
}

// SlopePerMinute fits a least-squares slope through values at times (both
// in seconds) and answers it per minute. It answers zero with fewer than
// two points or with no time span.
func SlopePerMinute(times, values []float64) float64 {
	if len(times) < minSlopePoints || len(values) < minSlopePoints || len(times) != len(values) {
		return 0
	}

	meanTime := mean(times)
	meanValue := mean(values)

	var numerator, denominator float64

	for i := range times {
		deviation := times[i] - meanTime
		numerator += deviation * (values[i] - meanValue)
		denominator += deviation * deviation
	}

	if denominator == 0 {
		return 0
	}

	return numerator / denominator * secondsPerMinute
}

// mean answers the arithmetic mean of values, or zero with no values.
func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	var sum float64

	for _, value := range values {
		sum += value
	}

	return sum / float64(len(values))
}

// MetricVerdict is the growth verdict of one series.
type MetricVerdict struct {
	Name           string
	SlopePerMinute float64
	Threshold      float64
	Pass           bool
	// Skipped is true when the series held no readable samples, so the
	// slope never ran. A skipped series never fails the run.
	Skipped bool
	Reason  string
}

// Assessment is the growth and error verdict over one soak run.
type Assessment struct {
	Store         string
	Samples       int
	TotalRuns     int
	TotalFailures int
	ErrorRate     float64
	ErrorPass     bool
	Metrics       []MetricVerdict
	Pass          bool
	Note          string
}

// Assess fits the growth rule over samples: the least-squares slope per
// minute over the last two thirds of the samples, with the first third as
// warm-up. It fails the run when a readable series grows faster than its
// threshold per minute, or when failed CLI runs over total CLI runs passes
// MaxErrorRate. Series with no readable samples are skipped, never failed.
// It answers errNoSamples with no samples, and marks runs with fewer than
// MinSamples as not passed with a note instead of a slope.
func Assess(store string, samples []Sample) (Assessment, error) {
	if len(samples) == 0 {
		return Assessment{}, errNoSamples
	}

	assessed := Assessment{
		Store:         store,
		Samples:       len(samples),
		TotalRuns:     0,
		TotalFailures: 0,
		ErrorRate:     0,
		ErrorPass:     false,
		Metrics:       nil,
		Pass:          false,
		Note:          "",
	}

	for _, sample := range samples {
		assessed.TotalRuns += sample.Runs
		assessed.TotalFailures += sample.Failures
	}

	if assessed.TotalRuns > 0 {
		assessed.ErrorRate = float64(assessed.TotalFailures) / float64(assessed.TotalRuns)
	}

	assessed.ErrorPass = assessed.ErrorRate <= MaxErrorRate

	if len(samples) < MinSamples {
		assessed.Note = "too few samples: " + strconv.Itoa(len(samples)) +
			", want at least " + strconv.Itoa(MinSamples)
		assessed.Pass = false

		return assessed, nil
	}

	window := samples[len(samples)/3:]

	assessed.Metrics = []MetricVerdict{
		assessSeries(window, MetricGoroutines, MaxGoroutinesPerMinute, seriesGoroutines),
		assessSeries(window, MetricHeapBytes, MaxHeapBytesPerMinute, seriesHeapBytes),
		assessSeries(window, MetricFDs, MaxFDsPerMinute, seriesFDs),
		assessSeries(window, MetricDBConns, MaxDBConnsPerMinute, seriesDBConns),
	}
	assessed.Pass = assessed.ErrorPass

	for _, verdict := range assessed.Metrics {
		if !verdict.Pass {
			assessed.Pass = false
		}
	}

	return assessed, nil
}

// seriesFunc reads one series value out of a sample.
type seriesFunc func(Sample) float64

// seriesGoroutines reads the goroutine count of a sample.
func seriesGoroutines(sample Sample) float64 { return sample.Goroutines }

// seriesHeapBytes reads the heap size of a sample.
func seriesHeapBytes(sample Sample) float64 { return sample.HeapBytes }

// seriesFDs reads the open file descriptor count of a sample.
func seriesFDs(sample Sample) float64 { return sample.FDs }

// seriesDBConns reads the database connection count of a sample.
func seriesDBConns(sample Sample) float64 { return sample.DBConns }

// assessSeries fits one series over the window and checks its threshold.
func assessSeries(window []Sample, name string, threshold float64, read seriesFunc) MetricVerdict {
	var times, values []float64

	for _, sample := range window {
		if value := read(sample); !Missing(value) {
			times = append(times, sample.ElapsedSeconds)
			values = append(values, value)
		}
	}

	if len(values) < minSlopePoints {
		return MetricVerdict{
			Name: name, SlopePerMinute: 0, Threshold: threshold,
			Pass: true, Skipped: true, Reason: "no data",
		}
	}

	slope := SlopePerMinute(times, values)

	return MetricVerdict{
		Name: name, SlopePerMinute: slope, Threshold: threshold,
		Pass: slope <= threshold, Skipped: false, Reason: "",
	}
}

// WriteCSV writes samples as CSV with a header row. Missing values write
// as empty fields.
func WriteCSV(writer io.Writer, samples []Sample) error {
	rows := make([][]string, 0, 1+len(samples))
	rows = append(rows, []string{
		"elapsed_s", "goroutines", "heap_bytes", "fds", "db_conns", "runs", "failures", "error_rate",
	})

	for _, sample := range samples {
		rows = append(rows, []string{
			strconv.FormatFloat(sample.ElapsedSeconds, 'f', 1, 64),
			numberOrEmpty(sample.Goroutines),
			numberOrEmpty(sample.HeapBytes),
			numberOrEmpty(sample.FDs),
			numberOrEmpty(sample.DBConns),
			strconv.Itoa(sample.Runs),
			strconv.Itoa(sample.Failures),
			strconv.FormatFloat(sample.ErrorRate, 'f', 4, 64),
		})
	}

	if err := csv.NewWriter(writer).WriteAll(rows); err != nil {
		return fmt.Errorf("write soak csv: %w", err)
	}

	return nil
}

// numberOrEmpty formats a sample value, or "" when it is missing.
func numberOrEmpty(value float64) string {
	if Missing(value) {
		return ""
	}

	return strconv.FormatFloat(value, 'f', 1, 64)
}

// WriteReport writes samples as CSV to csvPath and the assessment as
// Markdown to summaryPath. Both paths come from the operator's report
// directory flag, never from the network.
func WriteReport(csvPath, summaryPath string, samples []Sample, assessed Assessment) error {
	//nolint:gosec // The paths join the operator's report directory flag, never request input.
	csvFile, err := os.OpenFile(csvPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create soak csv: %w", err)
	}

	writeErr := WriteCSV(csvFile, samples)
	closeErr := csvFile.Close()

	if writeErr != nil {
		return writeErr
	}

	if closeErr != nil {
		return fmt.Errorf("close soak csv: %w", closeErr)
	}

	if err := os.WriteFile(summaryPath, []byte(Summary(assessed)), 0o600); err != nil {
		return fmt.Errorf("write soak summary: %w", err)
	}

	return nil
}

// verdict is one PASS or FAIL outcome with its Markdown word.
type verdict bool

// String answers PASS or FAIL.
func (v verdict) String() string {
	if v {
		return "PASS"
	}

	return "FAIL"
}

// Summary renders the assessment as Markdown with the verdict per metric.
func Summary(assessed Assessment) string {
	var sb strings.Builder

	sb.WriteString("# Soak verdict\n\n")
	sb.WriteString("Store: " + assessed.Store + "\n\n")
	sb.WriteString("Samples: " + strconv.Itoa(assessed.Samples) + "\n\n")
	sb.WriteString("CLI runs: " + strconv.Itoa(assessed.TotalRuns) +
		", failures: " + strconv.Itoa(assessed.TotalFailures) +
		", error rate: " + strconv.FormatFloat(assessed.ErrorRate, 'f', 4, 64) +
		" (threshold " + strconv.FormatFloat(MaxErrorRate, 'f', 2, 64) + "): " +
		verdict(assessed.ErrorPass).String() + "\n\n")

	for _, metric := range assessed.Metrics {
		sb.WriteString("- " + metric.Name +
			": slope " + strconv.FormatFloat(metric.SlopePerMinute, 'f', 3, 64) + "/min" +
			" (threshold " + strconv.FormatFloat(metric.Threshold, 'f', 3, 64) + "/min): " +
			verdict(metric.Pass).String() + skippedSuffix(metric) + "\n")
	}

	if assessed.Note != "" {
		sb.WriteString("\nNote: " + assessed.Note + "\n")
	}

	sb.WriteString("\nOverall: " + verdict(assessed.Pass).String() + "\n")

	return sb.String()
}

// skippedSuffix answers the skip note of a metric verdict, or "".
func skippedSuffix(metric MetricVerdict) string {
	if metric.Skipped {
		return " (skipped: " + metric.Reason + ")"
	}

	return ""
}
