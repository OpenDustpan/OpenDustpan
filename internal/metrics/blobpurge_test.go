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

// readFailure is the error of a failed store read in the table.
type readFailure struct{}

func (readFailure) Error() string {
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

func TestBlobPurgeBacklogGauges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		pending     int64
		oldestAge   time.Duration
		readErr     error
		wantPending float64
		wantAge     float64
	}{
		{name: "no record", pending: 0, oldestAge: 0, readErr: nil, wantPending: 0, wantAge: 0},
		{name: "one record", pending: 1, oldestAge: 90 * time.Second, readErr: nil, wantPending: 1, wantAge: 90},
		{name: "three records", pending: 3, oldestAge: time.Hour, readErr: nil, wantPending: 3, wantAge: 3600},
		{name: "failed read exports zero", pending: 3, oldestAge: time.Hour, readErr: readFailure{}, wantPending: 0, wantAge: 0},
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
			metrics.RegisterBlobPurgeBacklog(reg, read, func() time.Time { return testNow() })

			var buf bytes.Buffer
			if err := reg.Write(&buf); err != nil {
				t.Fatalf("write registry: %v", err)
			}

			out := buf.String()
			if got := gaugeValue(t, out, "dustpan_blob_purge_pending"); got != tt.wantPending {
				t.Errorf("dustpan_blob_purge_pending = %v, want %v", got, tt.wantPending)
			}

			if got := gaugeValue(t, out, "dustpan_blob_purge_oldest_age_seconds"); got != tt.wantAge {
				t.Errorf("dustpan_blob_purge_oldest_age_seconds = %v, want %v", got, tt.wantAge)
			}
		})
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
	metrics.RegisterBlobPurgeBacklog(reg, read, func() time.Time { return testNow() })

	var buf bytes.Buffer
	if err := reg.Write(&buf); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	if got := reads.Load(); got != 1 {
		t.Errorf("store reads for one scrape = %d, want 1", got)
	}
}
