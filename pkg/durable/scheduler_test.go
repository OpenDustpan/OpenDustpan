package durable_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/durable/durabletest"
)

const testInterval = 200 * time.Millisecond

func sweepSpec(interval time.Duration) durable.ScheduleSpec {
	return durable.ScheduleSpec{Name: "sweep", Kind: testKind, Version: 1, Interval: interval, Input: nil}
}

func schedulerConfig(store durable.ScheduleStore, rnd float64, specs ...durable.ScheduleSpec) durable.SchedulerConfig {
	return durable.SchedulerConfig{
		Store:         store,
		Schedules:     specs,
		MaxFirstDelay: 0,
		ErrorBackoff:  durable.Backoff{Base: 10 * time.Millisecond, Max: 20 * time.Millisecond},
		Logger:        nil,
		Rand:          func() float64 { return rnd },
	}
}

func newScheduler(t *testing.T, cfg durable.SchedulerConfig) *durable.Scheduler {
	t.Helper()

	s, err := durable.NewScheduler(cfg)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}

	return s
}

// runScheduler runs s until the test ends. The returned function stops s
// and waits for it.
func runScheduler(t *testing.T, s *durable.Scheduler) func() {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())

	var wg sync.WaitGroup

	wg.Go(func() { s.Run(ctx) })

	stop := func() {
		cancel()
		wg.Wait()
	}

	t.Cleanup(stop)

	return stop
}

// tickTimes records the time of each ClaimTick call.
type tickTimes struct {
	durabletest.ScheduledStore

	mu    sync.Mutex
	calls []time.Time
	fail  atomic.Int32
}

func (s *tickTimes) ClaimTick(ctx context.Context, spec durable.ScheduleSpec) (durable.Tick, error) {
	s.mu.Lock()
	s.calls = append(s.calls, time.Now())
	s.mu.Unlock()

	if s.fail.Add(-1) >= 0 {
		return durable.Tick{}, errNetwork
	}

	return s.ScheduledStore.ClaimTick(ctx, spec)
}

func (s *tickTimes) first(t *testing.T) time.Time {
	t.Helper()

	deadline := time.Now().Add(settleWait)

	for time.Now().Before(deadline) {
		if at, ok := s.firstCall(); ok {
			return at
		}

		pause(t, time.Millisecond)
	}

	t.Fatal("no claim of a tick within the wait")

	return time.Time{}
}

func (s *tickTimes) firstCall() (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.calls) == 0 {
		return time.Time{}, false
	}

	return s.calls[0], true
}

func countOps(t *testing.T, store durable.Store) int {
	t.Helper()

	ops, err := store.List(t.Context(), durable.Filter{Kind: testKind, States: nil, Limit: 0})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	return len(ops)
}

func TestNewSchedulerValidates(t *testing.T) {
	t.Parallel()

	store := durabletest.NewMem().ScheduleStore(0)
	ok := sweepSpec(time.Second)
	bad := ok
	bad.Interval = 0

	cases := map[string]durable.SchedulerConfig{
		"no store":       schedulerConfig(nil, 0, ok),
		"no schedule":    schedulerConfig(store, 0),
		"bad schedule":   schedulerConfig(store, 0, bad),
		"duplicate name": schedulerConfig(store, 0, ok, ok),
	}

	for name, cfg := range cases {
		if _, err := durable.NewScheduler(cfg); !errors.Is(err, durable.ErrInvalid) {
			t.Errorf("%s: NewScheduler error = %v, want ErrInvalid", name, err)
		}
	}
}

func TestSchedulerFirstDelayBound(t *testing.T) {
	t.Parallel()

	store := durabletest.NewMem().ScheduleStore(0)

	cases := []struct {
		name     string
		rnd      float64
		interval time.Duration
		maxFirst time.Duration
		want     time.Duration
	}{
		{"a tenth of the interval", 0.5, 10 * time.Second, 0, 500 * time.Millisecond},
		{"capped at 60 s by default", 0.5, 24 * time.Hour, 0, 30 * time.Second},
		{"capped by MaxFirstDelay", 0.5, time.Hour, time.Second, 500 * time.Millisecond},
		{"zero at the low end", 0, time.Hour, 0, 0},
	}

	for _, tc := range cases {
		cfg := schedulerConfig(store, tc.rnd, sweepSpec(tc.interval))
		cfg.MaxFirstDelay = tc.maxFirst

		if got := newScheduler(t, cfg).FirstDelay(sweepSpec(tc.interval)); got != tc.want {
			t.Errorf("%s: FirstDelay = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSchedulerWaitsTheFirstDelayBeforeItsFirstClaim(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		rnd      float64
		low, top time.Duration
	}{
		// The interval of 2 s gives a bound of 200 ms.
		{"high draw", 0.999, 190 * time.Millisecond, 2 * time.Second},
		{"low draw", 0, 0, 150 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := &tickTimes{
				ScheduledStore: durabletest.NewMem().
					ScheduleStore(0),
				mu:    sync.Mutex{},
				calls: nil,
				fail:  atomic.Int32{},
			}

			began := time.Now()
			runScheduler(t, newScheduler(t, schedulerConfig(store, tc.rnd, sweepSpec(2*time.Second))))

			if waited := store.first(t).Sub(began); waited < tc.low || waited > tc.top {
				t.Fatalf("first claim after %v, want between %v and %v", waited, tc.low, tc.top)
			}
		})
	}
}

func TestSchedulersOnOneStoreStartOneOperationPerTick(t *testing.T) {
	t.Parallel()

	mem := durabletest.NewMem()

	const instances = 3

	began := time.Now()

	stops := make([]func(), 0, instances)
	for range instances {
		cfg := schedulerConfig(mem.ScheduleStore(0), 0, sweepSpec(testInterval))
		stops = append(stops, runScheduler(t, newScheduler(t, cfg)))
	}

	pause(t, 5*testInterval+testInterval/2)

	for _, stop := range stops {
		stop()
	}

	// One tick at once, then one per interval: at most one operation for
	// each interval that passed, and one more.
	ran := countOps(t, mem.Store())
	most := int(time.Since(began)/testInterval) + 1

	if ran < 4 || ran > most {
		t.Fatalf("%d operations from %d schedulers in %v, want 4 to %d: one per tick", ran, instances,
			time.Since(began), most)
	}
}

func TestSchedulerRunsMissedTicksOnce(t *testing.T) {
	t.Parallel()

	mem := durabletest.NewMem()
	cfg := schedulerConfig(mem.ScheduleStore(0), 0, sweepSpec(testInterval))

	stop := runScheduler(t, newScheduler(t, cfg))

	waitForOps(t, mem.Store(), 1)
	stop()

	// No instance runs for three intervals.
	pause(t, 3*testInterval)

	before := countOps(t, mem.Store())
	runScheduler(t, newScheduler(t, cfg))

	waitForOps(t, mem.Store(), before+1)
	pause(t, testInterval/2)

	if got := countOps(t, mem.Store()); got != before+1 {
		t.Fatalf("%d operations after a restart that missed three ticks, want %d: the missed ticks run once",
			got, before+1)
	}
}

func TestARestartOfManySchedulersGivesOneCatchUp(t *testing.T) {
	t.Parallel()

	mem := durabletest.NewMem()

	const instances = 3

	startAll := func() func() {
		stops := make([]func(), 0, instances)

		for i := range instances {
			// Each instance draws its own first delay.
			cfg := schedulerConfig(mem.ScheduleStore(0), float64(i)/instances, sweepSpec(testInterval))
			stops = append(stops, runScheduler(t, newScheduler(t, cfg)))
		}

		return func() {
			for _, stop := range stops {
				stop()
			}
		}
	}

	stopAll := startAll()

	waitForOps(t, mem.Store(), 1)
	stopAll()

	// All instances are down for three intervals, then all start again.
	pause(t, 3*testInterval)

	before := countOps(t, mem.Store())
	startAll()

	waitForOps(t, mem.Store(), before+1)
	pause(t, testInterval/2)

	if got := countOps(t, mem.Store()); got != before+1 {
		t.Fatalf("%d catch-up operations after a restart of %d instances, want 1", got-before, instances)
	}
}

func TestSchedulerKeepsTryingAfterStoreErrors(t *testing.T) {
	t.Parallel()

	mem := durabletest.NewMem()
	store := &tickTimes{ScheduledStore: mem.ScheduleStore(0), mu: sync.Mutex{}, calls: nil, fail: atomic.Int32{}}
	store.fail.Store(3)

	runScheduler(t, newScheduler(t, schedulerConfig(store, 0, sweepSpec(time.Hour))))

	waitForOps(t, mem.Store(), 1)
}

func waitForOps(t *testing.T, store durable.Store, n int) {
	t.Helper()

	deadline := time.Now().Add(settleWait)

	for countOps(t, store) < n {
		if time.Now().After(deadline) {
			t.Fatalf("fewer than %d operations within %v", n, settleWait)
		}

		pause(t, testPoll)
	}
}
