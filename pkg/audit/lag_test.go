package audit_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

func TestLagSecondsUpToDate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 14, 12, 0, 0, 0, time.UTC)

	if got := audit.LagSeconds(time.Time{}, now); got != 0 {
		t.Fatalf("lag with no pending entry = %v, want 0", got)
	}

	future := now.Add(time.Minute)
	if got := audit.LagSeconds(future, now); got != 0 {
		t.Fatalf("lag with a future entry = %v, want 0", got)
	}
}

func TestLagSecondsBehind(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 14, 12, 0, 0, 0, time.UTC)
	oldest := now.Add(-90 * time.Second)

	if got := audit.LagSeconds(oldest, now); got != 90 {
		t.Fatalf("lag = %v, want 90", got)
	}
}

// lagHook collects the entries lags of the lag hook across goroutines.
type lagHook struct {
	mu  sync.Mutex
	got []int64
}

func (h *lagHook) hook(_ string, entries int64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.got = append(h.got, entries)
}

func (h *lagHook) last() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(h.got) == 0 {
		return -1
	}

	return h.got[len(h.got)-1]
}

// TestLagEntriesGrowsWhileTheReceiverIsDown proves that the delivery lag in
// entries grows while the receiver is down and returns to 0 after it
// recovers: two entries wait while the sink is down, one more arrives, and
// the shipper delivers all three once the sink is up.
func TestLagEntriesGrowsWhileTheReceiverIsDown(t *testing.T) {
	t.Parallel()

	feed := newMemFeed(map[namespace.Name]int{"acme": 2})
	sink := &memSink{refuse: "", batches: nil, mu: sync.Mutex{}, down: true}
	lags := &lagHook{mu: sync.Mutex{}, got: nil}

	s, err := audit.NewShipper(audit.ShipperConfig{
		Feed:          feed,
		Namespaces:    feed,
		Logger:        nil,
		OnShipped:     nil,
		OnFailure:     nil,
		OnSkipped:     nil,
		OnLag:         nil,
		OnLagEntries:  lags.hook,
		Now:           nil,
		Sinks:         []audit.ShipSink{shipSink("siem", sink)},
		InstanceID:    "",
		Leases:        nil,
		LeaseTTL:      0,
		RenewInterval: 0,
	})
	if err != nil {
		t.Fatalf("new shipper: %v", err)
	}

	if err := s.ShipOnce(t.Context()); err == nil {
		t.Fatal("ship once to a down sink: got nil, want an error")
	}

	if got, err := s.LagEntries(t.Context(), "siem"); err != nil || got != 2 {
		t.Fatalf("lag while down = %d, %v, want 2, nil", got, err)
	}

	appendAcmeEntries(feed, 3)

	if got, err := s.LagEntries(t.Context(), "siem"); err != nil || got != 3 {
		t.Fatalf("lag after one more entry = %d, %v, want 3, nil", got, err)
	}

	sink.setDown(false)

	if err := s.ShipOnce(t.Context()); err != nil {
		t.Fatalf("ship once after recovery: %v", err)
	}

	if got, err := s.LagEntries(t.Context(), "siem"); err != nil || got != 0 {
		t.Fatalf("lag after recovery = %d, %v, want 0, nil", got, err)
	}

	if got := sink.sequences(); fmt.Sprint(got) != "[acme/1 acme/2 acme/3]" {
		t.Fatalf("shipped %v, want [acme/1 acme/2 acme/3]", got)
	}

	if got := lags.last(); got != 0 {
		t.Fatalf("last lag of the hook = %d, want 0", got)
	}
}
