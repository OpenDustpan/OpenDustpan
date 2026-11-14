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
