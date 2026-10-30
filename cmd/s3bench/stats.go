package main

import (
	"math"
	"slices"
	"time"
)

// stats is the latency summary of one measured operation, in milliseconds.
// P99 of fewer than 100 samples is the highest sample but one rank: the
// report states the sample count next to it.
type stats struct {
	Samples    int     `json:"samples"`
	P50Ms      float64 `json:"p50Ms"`
	P99Ms      float64 `json:"p99Ms"`
	MaxMs      float64 `json:"maxMs"`
	Errors     int     `json:"errors"`
	FirstError string  `json:"firstError,omitempty"`
}

// tally collects the samples and errors of one operation.
type tally struct {
	samples []float64
	errors  int
	first   string
}

// add records one attempt: its time when err is nil, else the error.
func (t *tally) add(took time.Duration, err error) {
	if err != nil {
		t.errors++

		if t.first == "" {
			t.first = err.Error()
		}

		return
	}

	t.samples = append(t.samples, float64(took)/float64(time.Millisecond))
}

// time runs fn and records it.
func (t *tally) time(fn func() error) {
	start := time.Now()
	err := fn()
	t.add(time.Since(start), err)
}

// stats answers the summary of the tally.
func (t *tally) stats() stats {
	const (
		median = 0.50
		p99    = 0.99
	)

	maxMs := 0.0
	for _, s := range t.samples {
		maxMs = max(maxMs, s)
	}

	return stats{
		Samples:    len(t.samples),
		P50Ms:      quantile(t.samples, median),
		P99Ms:      quantile(t.samples, p99),
		MaxMs:      maxMs,
		Errors:     t.errors,
		FirstError: t.first,
	}
}

// quantile answers the q quantile of samples by the nearest rank, or 0
// without samples.
func quantile(samples []float64, q float64) float64 {
	if len(samples) == 0 {
		return 0
	}

	ordered := slices.Clone(samples)
	slices.Sort(ordered)

	at := int(math.Round(q * float64(len(ordered)-1)))

	return ordered[at]
}
