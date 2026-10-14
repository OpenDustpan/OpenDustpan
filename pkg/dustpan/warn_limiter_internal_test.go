package dustpan

import (
	"fmt"
	"testing"
	"time"
)

// TestWarnLimiterRate proves the same peer warns once per minute: twice
// inside the minute logs once, after a minute logs again.
func TestWarnLimiterRate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 14, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	limiter := newWarnLimiter(clock)

	if !limiter.allow("10.1.2.3") {
		t.Fatal("first warning refused, want it allowed")
	}

	if limiter.allow("10.1.2.3") {
		t.Fatal("second warning inside the minute allowed, want it dropped")
	}

	now = now.Add(warnInterval)

	if !limiter.allow("10.1.2.3") {
		t.Fatal("warning after a minute refused, want it allowed")
	}
}

// TestWarnLimiterCapsPeers proves 2000 different peers never hold more than
// warnMaxPeers entries.
func TestWarnLimiterCapsPeers(t *testing.T) {
	t.Parallel()

	limiter := newWarnLimiter(nil)

	for i := range 2000 {
		limiter.allow(fmt.Sprintf("peer-%d", i))
	}

	if got := len(limiter.last); got > warnMaxPeers {
		t.Fatalf("peers = %d, want at most %d", got, warnMaxPeers)
	}
}

// TestWarnLimiterEmptyPeer proves an empty peer never warns.
func TestWarnLimiterEmptyPeer(t *testing.T) {
	t.Parallel()

	limiter := newWarnLimiter(nil)

	if limiter.allow("") {
		t.Fatal("empty peer allowed, want it dropped")
	}
}
