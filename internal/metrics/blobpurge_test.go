package metrics_test

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/metrics"
)

// testNow is the clock of the blob purge gauge tests.
func testNow() time.Time {
	return time.Date(2026, time.October, 29, 12, 0, 0, 0, time.UTC)
}

// readFailureError is the error of a failed store read in the table.
type readFailureError struct{}

func (readFailureError) Error() string {
	return "read failed"
}

// gaugeValue returns the value of the sample of name in the exposition text out.
func gaugeValue(t *testing.T, out, name string) float64 {
	t.Helper()

	for line := range strings.SplitSeq(out, "\n") {
		value, ok := strings.CutPrefix(line, name+" ")
		if !ok {
			continue
		}

		v, err := strconv.ParseFloat(value, 64)
		if err != nil {
			t.Fatalf("sample %s = %q: %v", name, value, err)
		}

		return v
	}

	t.Fatalf("no sample of %s in:\n%s", name, out)

	return 0
}

// blobPurgeCase is one row of the blob purge gauge table.
type blobPurgeCase struct {
	name           string
	pending        int64
	oldestAge      time.Duration
	readErr        error
	wantPending    float64
	wantAge        float64
	wantReadErrors float64
}

// checkBlobPurgeGauges checks the two backlog gauges and the read error
// counter of one case against the exposition text out.
func checkBlobPurgeGauges(t *testing.T, out string, tt blobPurgeCase) {
	t.Helper()

	if got := gaugeValue(t, out, "dustpan_blob_purge_pending"); got != tt.wantPending {
		t.Errorf("dustpan_blob_purge_pending = %v, want %v", got, tt.wantPending)
	}

	if got := gaugeValue(t, out, "dustpan_blob_purge_oldest_age_seconds"); got != tt.wantAge {
		t.Errorf("dustpan_blob_purge_oldest_age_seconds = %v, want %v", got, tt.wantAge)
	}

	if tt.wantReadErrors > 0 {
		if got := gaugeValue(t, out, "dustpan_blob_purge_read_errors_total"); got != tt.wantReadErrors {
			t.Errorf("dustpan_blob_purge_read_errors_total = %v, want %v", got, tt.wantReadErrors)
		}
	}
}

func TestBlobPurgeBacklogGauges(t *testing.T) {
	t.Parallel()

	tests := []blobPurgeCase{
		{
			name:           "no record",
			pending:        0,
			oldestAge:      0,
			readErr:        nil,
			wantPending:    0,
			wantAge:        0,
			wantReadErrors: 0,
		},
		{
			name:           "one record",
			pending:        1,
			oldestAge:      90 * time.Second,
			readErr:        nil,
			wantPending:    1,
			wantAge:        90,
			wantReadErrors: 0,
		},
		{
			name:           "three records",
			pending:        3,
			oldestAge:      time.Hour,
			readErr:        nil,
			wantPending:    3,
			wantAge:        3600,
			wantReadErrors: 0,
		},
		{
			name:           "failed read",
			pending:        3,
			oldestAge:      time.Hour,
			readErr:        readFailureError{},
			wantPending:    0,
			wantAge:        0,
			wantReadErrors: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			oldest := testNow().Add(-tt.oldestAge)
			if tt.pending == 0 {
				oldest = time.Time{}
			}

			read := func(context.Context) (int64, time.Time, error) {
				return tt.pending, oldest, tt.readErr
			}

			reg := metrics.NewRegistry()
			metrics.RegisterBlobPurgeBacklog(reg, read, testNow)

			var buf bytes.Buffer
			if err := reg.Write(&buf); err != nil {
				t.Fatalf("write registry: %v", err)
			}

			checkBlobPurgeGauges(t, buf.String(), tt)
		})
	}
}

// TestBlobPurgeBacklogGaugesSlowRead proves that one read supplies both gauges
// when the read takes longer than the cache lifetime: the lifetime of the
// snapshot starts when the read completes.
func TestBlobPurgeBacklogGaugesSlowRead(t *testing.T) {
	t.Parallel()

	now := testNow()
	clock := func() time.Time { return now }

	var reads atomic.Int64

	read := func(context.Context) (int64, time.Time, error) {
		reads.Add(1)
		now = now.Add(3 * time.Second)

		return 2, now.Add(-time.Minute), nil
	}

	reg := metrics.NewRegistry()
	metrics.RegisterBlobPurgeBacklog(reg, read, clock)

	var buf bytes.Buffer
	if err := reg.Write(&buf); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	if got := reads.Load(); got != 1 {
		t.Errorf("store reads for one scrape = %d, want 1", got)
	}

	out := buf.String()
	if got := gaugeValue(t, out, "dustpan_blob_purge_pending"); got != 2 {
		t.Errorf("dustpan_blob_purge_pending = %v, want 2", got)
	}

	if got := gaugeValue(t, out, "dustpan_blob_purge_oldest_age_seconds"); got != 60 {
		t.Errorf("dustpan_blob_purge_oldest_age_seconds = %v, want 60", got)
	}
}

func TestBlobPurgeBacklogGaugesShareOneRead(t *testing.T) {
	t.Parallel()

	var reads atomic.Int64

	read := func(context.Context) (int64, time.Time, error) {
		reads.Add(1)

		return 2, testNow().Add(-time.Minute), nil
	}

	reg := metrics.NewRegistry()
	metrics.RegisterBlobPurgeBacklog(reg, read, testNow)

	var buf bytes.Buffer
	if err := reg.Write(&buf); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	if got := reads.Load(); got != 1 {
		t.Errorf("store reads for one scrape = %d, want 1", got)
	}
}

// blobPurgeAgeCase is one row of the oldest age table. oldestSkew is the
// offset of the oldest record time from the clock.
type blobPurgeAgeCase struct {
	name       string
	oldestSkew time.Duration
	wantAge    float64
}

// TestBlobPurgeBacklogGaugesNegativeAge proves that a record that carries a
// time later than the clock gives age 0, and not a negative age. The last row
// is the control: a record one second older than the clock gives age 1.
func TestBlobPurgeBacklogGaugesNegativeAge(t *testing.T) {
	t.Parallel()

	tests := []blobPurgeAgeCase{
		{
			name:       "oldest record one second later than the clock",
			oldestSkew: time.Second,
			wantAge:    0,
		},
		{
			name:       "oldest record one hour later than the clock",
			oldestSkew: time.Hour,
			wantAge:    0,
		},
		{
			name:       "oldest record one second earlier than the clock",
			oldestSkew: -time.Second,
			wantAge:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			now := testNow()
			clock := func() time.Time { return now }

			read := func(context.Context) (int64, time.Time, error) {
				return 1, now.Add(tt.oldestSkew), nil
			}

			reg := metrics.NewRegistry()
			metrics.RegisterBlobPurgeBacklog(reg, read, clock)

			var buf bytes.Buffer
			if err := reg.Write(&buf); err != nil {
				t.Fatalf("write registry: %v", err)
			}

			out := buf.String()
			if got := gaugeValue(t, out, "dustpan_blob_purge_pending"); got != 1 {
				t.Errorf("dustpan_blob_purge_pending = %v, want 1", got)
			}

			if got := gaugeValue(t, out, "dustpan_blob_purge_oldest_age_seconds"); got != tt.wantAge {
				t.Errorf("dustpan_blob_purge_oldest_age_seconds = %v, want %v", got, tt.wantAge)
			}
		})
	}
}

// blobPurgeCacheStep is one row of the snapshot cache table. offset is the
// time of the scrape since the first scrape of the test.
type blobPurgeCacheStep struct {
	name        string
	offset      time.Duration
	wantReads   int64
	wantPending float64
}

// TestBlobPurgeBacklogGaugesCacheReuse proves that the two gauges share one
// read of the store for the lifetime of the snapshot, that the snapshot
// expires at exactly that lifetime, and that a later scrape reads the store
// again. Each step moves the clock from the time of the first scrape, scrapes
// once and checks the total read count and the pending gauge. The reader
// gives one more pending record at each read, so a reused snapshot shows the
// earlier value. The steps share the registry, the reader and the clock, and
// each step needs the snapshot of the step before it, so the test walks the
// table in order.
func TestBlobPurgeBacklogGaugesCacheReuse(t *testing.T) {
	t.Parallel()

	now := testNow()
	clock := func() time.Time { return now }

	var reads atomic.Int64

	read := func(context.Context) (int64, time.Time, error) {
		return reads.Add(1), testNow().Add(-time.Minute), nil
	}

	reg := metrics.NewRegistry()
	metrics.RegisterBlobPurgeBacklog(reg, read, clock)

	steps := []blobPurgeCacheStep{
		{
			name:        "the first scrape reads the store",
			offset:      0,
			wantReads:   1,
			wantPending: 1,
		},
		{
			name:        "one millisecond before the lifetime reuses the snapshot",
			offset:      1999 * time.Millisecond,
			wantReads:   1,
			wantPending: 1,
		},
		{
			name:        "the scrape at the lifetime reads the store again",
			offset:      2 * time.Second,
			wantReads:   2,
			wantPending: 2,
		},
		{
			name:        "one millisecond before the second lifetime reuses the snapshot",
			offset:      3999 * time.Millisecond,
			wantReads:   2,
			wantPending: 2,
		},
		{
			name:        "the scrape at the second lifetime reads the store again",
			offset:      4 * time.Second,
			wantReads:   3,
			wantPending: 3,
		},
	}

	for _, step := range steps {
		now = testNow().Add(step.offset)

		var buf bytes.Buffer
		if err := reg.Write(&buf); err != nil {
			t.Fatalf("%s: write registry: %v", step.name, err)
		}

		if got := reads.Load(); got != step.wantReads {
			t.Errorf("%s: store reads = %d, want %d", step.name, got, step.wantReads)
		}

		if got := gaugeValue(t, buf.String(), "dustpan_blob_purge_pending"); got != step.wantPending {
			t.Errorf("%s: dustpan_blob_purge_pending = %v, want %v", step.name, got, step.wantPending)
		}
	}
}
