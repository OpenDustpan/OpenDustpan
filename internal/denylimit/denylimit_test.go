package denylimit_test

import (
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/denylimit"
)

// clock is a settable clock.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.t = c.t.Add(d)
}

func newClock() *clock {
	return &clock{mu: sync.Mutex{}, t: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
}

func key(principal string) denylimit.Key {
	return denylimit.Key{Principal: principal, Action: "unit:write", Kind: "unit", Scope: "team"}
}

func TestOneEntryForEachKeyAndWindow(t *testing.T) {
	t.Parallel()

	c := newClock()
	l := denylimit.New(denylimit.Config{Window: time.Minute, MaxKeys: 0, MaxFlush: 0, Now: c.now})

	writes, suppressed := 0, 0

	for range 1000 {
		d := l.Allow(key("bo"))
		if d.Write {
			writes++
		} else {
			suppressed++
		}
	}

	if writes != 1 || suppressed != 999 {
		t.Fatalf("writes = %d, suppressed = %d, want 1 and 999", writes, suppressed)
	}

	// A second key has its own window.
	if d := l.Allow(key("ann")); !d.Write {
		t.Fatal("a second principal was suppressed by the first")
	}

	// The next window writes again and carries the count.
	c.advance(time.Minute)

	d := l.Allow(key("bo"))
	if !d.Write || d.Suppressed != 999 {
		t.Fatalf("next window = %+v, want a write with 999 suppressed", d)
	}
}

func TestQuietKeyIsFlushedByTheNextCall(t *testing.T) {
	t.Parallel()

	c := newClock()
	l := denylimit.New(denylimit.Config{Window: time.Minute, MaxKeys: 0, MaxFlush: 0, Now: c.now})

	for range 11 {
		l.Allow(key("bo"))
	}

	c.advance(2 * time.Minute)

	d := l.Allow(key("ann"))
	if len(d.Flush) != 1 || d.Flush[0].Key != key("bo") || d.Flush[0].Suppressed != 10 {
		t.Fatalf("flush = %+v, want the 10 suppressed denials of bo", d.Flush)
	}

	if l.Len() != 1 {
		t.Fatalf("tracked keys = %d, want 1 after the flush", l.Len())
	}
}

func TestOverflowAggregatesAndStaysBounded(t *testing.T) {
	t.Parallel()

	c := newClock()
	l := denylimit.New(denylimit.Config{Window: time.Minute, MaxKeys: 4, MaxFlush: 0, Now: c.now})

	count := overflowCount{written: 0, held: 0}

	for i := range 1000 {
		d := l.Allow(key(fmt.Sprintf("p%d", i)))

		count.add(d)

		if l.Len() > 4 {
			t.Fatalf("tracked keys = %d, want at most 4", l.Len())
		}
	}

	if count.written != 1 || count.held != 995 {
		t.Fatalf("aggregate writes = %d, suppressed = %d, want 1 and 995", count.written, count.held)
	}

	c.advance(time.Minute)

	d := l.Allow(key("late"))

	if !slices.ContainsFunc(d.Flush, func(p denylimit.Pending) bool { return p.Overflow && p.Suppressed == 995 }) {
		t.Fatalf("flush = %+v, want the aggregate with 995", d.Flush)
	}
}

func TestFlushIsBounded(t *testing.T) {
	t.Parallel()

	c := newClock()
	l := denylimit.New(denylimit.Config{Window: time.Minute, MaxKeys: 100, MaxFlush: 3, Now: c.now})

	for i := range 50 {
		l.Allow(key(fmt.Sprintf("p%d", i)))
		l.Allow(key(fmt.Sprintf("p%d", i)))
	}

	c.advance(2 * time.Minute)

	total := 0

	for range 30 {
		d := l.Allow(key("probe"))
		if len(d.Flush) > 3 {
			t.Fatalf("flush has %d items, want at most 3", len(d.Flush))
		}

		total += len(d.Flush)
	}

	if total != 50 {
		t.Fatalf("flushed %d keys over the calls, want 50", total)
	}
}

func TestConcurrentUse(t *testing.T) {
	t.Parallel()

	l := denylimit.New(denylimit.Config{Window: time.Minute, MaxKeys: 8, MaxFlush: 0, Now: nil})

	var wg sync.WaitGroup

	for g := range 8 {
		wg.Go(func() {
			for i := range 500 {
				l.Allow(key(fmt.Sprintf("p%d", (g*500+i)%20)))
			}
		})
	}

	wg.Wait()

	if l.Len() > 8 {
		t.Fatalf("tracked keys = %d, want at most 8", l.Len())
	}
}

// overflowCount counts the denials of the aggregate: those that wrote an entry
// and those that the limiter held back.
type overflowCount struct{ written, held int }

// add counts d when it belongs to the aggregate.
func (c *overflowCount) add(d denylimit.Decision) {
	switch {
	case d.Overflow && d.Write:
		c.written++
	case d.Overflow:
		c.held++
	default:
	}
}
