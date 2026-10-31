package storecheck

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
)

const (
	// sampleObjects is the count of small objects of the latency sample.
	sampleObjects = 50
	// sampleBytes is the size of one small object.
	sampleBytes = 4096
	// throughputObjects is the count of large objects of the throughput sample.
	throughputObjects = 4
	// throughputBytes is the size of one large object: 4 MiB.
	throughputBytes = 4 << 20
)

// The ranks of the nearest-rank rule, in percent.
const (
	rankMedian = 50
	rankTail   = 99
	percent    = 100
)

// sample describes one set of objects that measure writes: the directory, the
// name prefix, the count and the size of each object.
type sample struct {
	dir   string
	name  string
	count int
	size  int
}

// measure writes, reads and deletes a sample of objects, and sets the
// latency, the throughput and the delete rate of report. The sample is
// 50 objects of 4 KiB and 4 objects of 4 MiB: about 16 MiB. It stops at the
// first error. A figure is a value from one run on one store at one time.
func (r *run) measure(ctx context.Context, report *Report) error {
	dir := r.prefix + "measure/"

	small, putTimes, err := r.timePuts(ctx, sample{dir: dir, name: "s-", count: sampleObjects, size: sampleBytes})
	if err != nil {
		return err
	}

	getTimes, err := r.timeGets(ctx, small)
	if err != nil {
		return err
	}

	large, largePut, err := r.timePuts(
		ctx, sample{dir: dir, name: "l-", count: throughputObjects, size: throughputBytes},
	)
	if err != nil {
		return err
	}

	largeGet, err := r.timeGets(ctx, large)
	if err != nil {
		return err
	}

	deleteTimes, err := r.timeDeletes(ctx, slices.Concat(small, large))
	if err != nil {
		return err
	}

	largeBytes := int64(throughputObjects) * throughputBytes

	report.PutLatency = percentiles(putTimes)
	report.GetLatency = percentiles(getTimes)
	report.DeleteLatency = percentiles(deleteTimes)
	report.PutBytesPerSecond = perSecond(largeBytes, sum(largePut))
	report.GetBytesPerSecond = perSecond(largeBytes, sum(largeGet))
	report.DeletesPerSecond = perSecond(int64(len(deleteTimes)), sum(deleteTimes))

	return nil
}

// timePuts writes the objects of s and returns their keys and the time of
// each put.
func (r *run) timePuts(ctx context.Context, s sample) ([]blob.Key, []time.Duration, error) {
	data := payload(s.name, s.size)
	keys := make([]blob.Key, 0, s.count)
	times := make([]time.Duration, 0, s.count)

	for i := range s.count {
		key, err := r.key(s.dir, fmt.Sprintf("%s%03d", s.name, i))
		if err != nil {
			return nil, nil, err
		}

		start := time.Now()
		if err := r.m.Put(ctx, key, bytes.NewReader(data)); err != nil {
			return nil, nil, err
		}

		times = append(times, time.Since(start))
		keys = append(keys, key)
	}

	return keys, times, nil
}

// timeGets reads each key and returns the time of each get.
func (r *run) timeGets(ctx context.Context, keys []blob.Key) ([]time.Duration, error) {
	times := make([]time.Duration, 0, len(keys))

	for _, key := range keys {
		start := time.Now()

		rc, err := r.m.Get(ctx, key)
		if err != nil {
			return nil, err
		}

		_, copyErr := io.Copy(io.Discard, rc)
		closeErr := rc.Close()

		if copyErr != nil || closeErr != nil {
			return nil, fmt.Errorf("read object: %w", errors.Join(copyErr, closeErr))
		}

		times = append(times, time.Since(start))
	}

	return times, nil
}

// timeDeletes deletes each key with one Delete call and returns the time of
// each delete.
func (r *run) timeDeletes(ctx context.Context, keys []blob.Key) ([]time.Duration, error) {
	times := make([]time.Duration, 0, len(keys))

	for _, key := range keys {
		start := time.Now()
		if err := r.m.Delete(ctx, key); err != nil {
			return nil, err
		}

		times = append(times, time.Since(start))
	}

	return times, nil
}

// percentiles returns the p50 and the p99 of times by the nearest-rank rule.
func percentiles(times []time.Duration) Latency {
	if len(times) == 0 {
		return Latency{P50: 0, P99: 0}
	}

	sorted := slices.Clone(times)
	slices.Sort(sorted)

	rank := func(p int) time.Duration {
		idx := (p*len(sorted) + percent - 1) / percent

		return sorted[max(idx, 1)-1]
	}

	return Latency{P50: rank(rankMedian), P99: rank(rankTail)}
}

func sum(times []time.Duration) time.Duration {
	var total time.Duration
	for _, d := range times {
		total += d
	}

	return total
}

// perSecond returns n per second over elapsed. A run too fast to measure
// counts as one nanosecond.
func perSecond(n int64, elapsed time.Duration) int64 {
	return int64(float64(n) / max(elapsed, time.Nanosecond).Seconds())
}
