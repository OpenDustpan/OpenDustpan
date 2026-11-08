package durabletest

import (
	"errors"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
)

// ScheduledStore is a store of durable operations that also stores
// schedules. The store adapters implement both ports on one type.
type ScheduledStore interface {
	durable.Store
	durable.ScheduleStore
}

// ScheduleHarness is one fresh, empty database for one schedule case.
type ScheduleHarness struct {
	// Open returns a new handle on the database of the harness, with the
	// clock skew of its instance, as Harness.Open does.
	Open func(t *testing.T, skew time.Duration) ScheduledStore
}

// ScheduleFactory returns a fresh schedule harness for one case.
type ScheduleFactory func(t *testing.T) ScheduleHarness

// tickInterval is the interval of the schedules of the cases. A case that
// waits for a tick waits about this long.
const tickInterval = 400 * time.Millisecond

// RunSchedules executes the conformance suite for durable.ScheduleStore
// against harnesses from newHarness.
func RunSchedules(t *testing.T, newHarness ScheduleFactory) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, h ScheduleHarness)
	}{
		{"schedule requests are validated", testScheduleValidates},
		{"a new schedule is due at once and ensure keeps its next run", testScheduleEnsure},
		{"a tick uses the stored interval", testTickStoredInterval},
		{"concurrent claims of one tick give one operation", testTickRace},
		{"concurrent ensures of one schedule all succeed", testEnsureRace},
		{"missed ticks collapse into one run", testMissedTicks},
		{"an instance clock ahead cannot claim a tick early", testTickClockSkew},
		{"a shorter interval moves the next run closer", testScheduleShorterInterval},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, newHarness(t))
		})
	}
}

func scheduleSpec(name string, interval time.Duration) durable.ScheduleSpec {
	return durable.ScheduleSpec{Name: name, Kind: kindA, Version: 1, Interval: interval, Input: []byte("tick")}
}

func mustEnsure(t *testing.T, s durable.ScheduleStore, spec durable.ScheduleSpec) durable.Schedule {
	t.Helper()

	sc, err := s.EnsureSchedule(t.Context(), spec)
	if err != nil {
		t.Fatalf("EnsureSchedule(%q): %v", spec.Name, err)
	}

	return sc
}

func mustTick(t *testing.T, s durable.ScheduleStore, spec durable.ScheduleSpec) durable.Tick {
	t.Helper()

	tick, err := s.ClaimTick(t.Context(), spec)
	if err != nil {
		t.Fatalf("ClaimTick(%q): %v", spec.Name, err)
	}

	return tick
}

func testScheduleValidates(t *testing.T, h ScheduleHarness) {
	t.Helper()

	s := h.Open(t, 0)

	for _, spec := range []durable.ScheduleSpec{
		{Name: "", Kind: kindA, Version: 0, Interval: time.Second, Input: nil},
		{Name: "s", Kind: "", Version: 0, Interval: time.Second, Input: nil},
		{Name: "s", Kind: kindA, Version: -1, Interval: time.Second, Input: nil},
		{Name: "s", Kind: kindA, Version: 0, Interval: 0, Input: nil},
		{Name: "s", Kind: kindA, Version: 0, Interval: time.Millisecond - 1, Input: nil},
	} {
		if _, err := s.EnsureSchedule(t.Context(), spec); !errors.Is(err, durable.ErrInvalid) {
			t.Errorf("EnsureSchedule(%+v) error = %v, want ErrInvalid", spec, err)
		}

		if _, err := s.ClaimTick(t.Context(), spec); !errors.Is(err, durable.ErrInvalid) {
			t.Errorf("ClaimTick(%+v) error = %v, want ErrInvalid", spec, err)
		}
	}

	if _, err := s.GetSchedule(t.Context(), "missing"); !errors.Is(err, durable.ErrNotFound) {
		t.Errorf("GetSchedule of an unknown schedule: error = %v, want ErrNotFound", err)
	}

	if _, err := s.ClaimTick(t.Context(), scheduleSpec("missing", time.Second)); !errors.Is(err, durable.ErrNotFound) {
		t.Errorf("ClaimTick of an unknown schedule: error = %v, want ErrNotFound", err)
	}
}

func testScheduleEnsure(t *testing.T, h ScheduleHarness) {
	t.Helper()

	s := h.Open(t, 0)
	spec := scheduleSpec("sweep", time.Hour)

	created := mustEnsure(t, s, spec)
	if created.Wait() != 0 || !created.LastClaimedAt.IsZero() || created.Interval != time.Hour {
		t.Fatalf("new schedule = %+v, want due at once, never claimed, interval 1h", created)
	}

	tick := mustTick(t, s, spec)

	again := mustEnsure(t, s, spec)
	if !again.NextRunAt.Equal(tick.Schedule.NextRunAt) {
		t.Fatalf("next run after a second ensure = %v, want it kept at %v", again.NextRunAt, tick.Schedule.NextRunAt)
	}

	got, err := s.GetSchedule(t.Context(), "sweep")
	if err != nil || !got.NextRunAt.Equal(tick.Schedule.NextRunAt) ||
		!got.LastClaimedAt.Equal(tick.Schedule.LastClaimedAt) {
		t.Fatalf("GetSchedule = %+v, %v; want the state after the claim", got, err)
	}

	notDue, err := s.ClaimTick(t.Context(), spec)
	if !errors.Is(err, durable.ErrNoWork) {
		t.Fatalf("ClaimTick before the next run: error = %v, want ErrNoWork", err)
	}

	checkNotDue(t, notDue, "sweep", time.Hour)
}

// checkNotDue fails the test unless tick, from a ClaimTick that answered
// ErrNoWork, holds the schedule called name with its next run about wait
// away, so that a caller can wait for it.
func checkNotDue(t *testing.T, tick durable.Tick, name string, wait time.Duration) {
	t.Helper()

	if tick.Schedule.Name != name || tick.Schedule.Wait() < wait-time.Minute || tick.Schedule.Wait() > wait {
		t.Fatalf("schedule with ErrNoWork = %+v (wait %v), want %s with its next run about %v away", tick.Schedule,
			tick.Schedule.Wait(), name, wait)
	}
}

func testTickStoredInterval(t *testing.T, h ScheduleHarness) {
	t.Helper()

	s := h.Open(t, 0)
	mustEnsure(t, s, scheduleSpec("sweep", time.Hour))

	// The spec of the claim has another interval. The claim uses the
	// stored one.
	tick := mustTick(t, s, scheduleSpec("sweep", tickInterval))

	sc := tick.Schedule
	if gap := sc.NextRunAt.Sub(sc.LastClaimedAt); gap < time.Hour-clockSlack || gap > time.Hour+clockSlack {
		t.Fatalf("next run minus claim time = %v, want the stored interval 1h", gap)
	}
}

func testTickRace(t *testing.T, h ScheduleHarness) {
	t.Helper()

	stores := []ScheduledStore{h.Open(t, 0), h.Open(t, 0)}
	spec := scheduleSpec("sweep", time.Hour)
	due := mustEnsure(t, stores[0], spec)

	ticks := make([]durable.Tick, racers)
	errs := make([]error, racers)

	var wg sync.WaitGroup

	for i := range racers {
		wg.Go(func() { ticks[i], errs[i] = stores[i%len(stores)].ClaimTick(t.Context(), spec) })
	}

	wg.Wait()

	var won []durable.Tick

	for i := range racers {
		switch {
		case errs[i] == nil:
			won = append(won, ticks[i])
		case errors.Is(errs[i], durable.ErrNoWork):
		default:
			t.Fatalf("ClaimTick %d: error = %v, want nil or ErrNoWork", i, errs[i])
		}
	}

	if len(won) != 1 {
		t.Fatalf("%d of %d concurrent claims of one tick succeeded, want exactly 1", len(won), racers)
	}

	checkTick(t, won[0], due, time.Hour)

	if ops := mustList(t, stores[1], durable.Filter{Kind: kindA, States: nil, Limit: 0}); len(ops) != 1 {
		t.Fatalf("%d operations after one tick, want 1", len(ops))
	}
}

func testEnsureRace(t *testing.T, h ScheduleHarness) {
	t.Helper()

	stores := []ScheduledStore{h.Open(t, 0), h.Open(t, 0)}
	spec := scheduleSpec("sweep-race", time.Hour)

	schedules := make([]durable.Schedule, racers)
	errs := make([]error, racers)

	var wg sync.WaitGroup

	for i := range racers {
		wg.Go(func() { schedules[i], errs[i] = stores[i%len(stores)].EnsureSchedule(t.Context(), spec) })
	}

	wg.Wait()

	for i := range racers {
		if errs[i] != nil {
			t.Fatalf("EnsureSchedule %d: error = %v, want nil", i, errs[i])
		}

		if schedules[i].Name != spec.Name || schedules[i].Interval != spec.Interval {
			t.Fatalf("schedule %d = %+v, want name %q and interval %v", i, schedules[i], spec.Name, spec.Interval)
		}
	}

	got, err := stores[1].GetSchedule(t.Context(), spec.Name)
	if err != nil || got.Interval != spec.Interval {
		t.Fatalf("GetSchedule after the race = %+v, %v; want one schedule with interval %v", got, err, spec.Interval)
	}
}

// checkTick fails the test unless tick started the operation of the spec
// of scheduleSpec for the tick that was due at due.NextRunAt, and moved the
// next run one interval after the claim.
func checkTick(t *testing.T, tick durable.Tick, due durable.Schedule, interval time.Duration) {
	t.Helper()

	op := tick.Operation
	if op.Key != durable.TickKey(due.Name, due.NextRunAt) || op.Kind != kindA || op.Version != 1 ||
		string(op.Input) != "tick" || op.State != durable.StatePending {
		t.Fatalf("operation of the tick = %+v, want a pending kind-a version 1 with the tick key and input", op)
	}

	sc := tick.Schedule
	if gap := sc.NextRunAt.Sub(sc.LastClaimedAt); gap < interval-clockSlack || gap > interval+clockSlack {
		t.Fatalf("next run minus claim time = %v, want the interval %v", gap, interval)
	}
}

func testMissedTicks(t *testing.T, h ScheduleHarness) {
	t.Helper()

	s := h.Open(t, 0)
	spec := scheduleSpec("sweep", tickInterval)
	mustEnsure(t, s, spec)
	mustTick(t, s, spec)

	// No instance runs for three intervals.
	pause(t, 3*tickInterval+4*pollEvery)

	late := mustTick(t, s, spec)

	if late.Schedule.Wait() < tickInterval-clockSlack-pollEvery {
		t.Fatalf("next run after a late claim is %v away, want one interval from the claim", late.Schedule.Wait())
	}

	// The next claim may come after the next run on a slow store, so the
	// case reads the times of the store, not the time between the claims.
	// A claim before the next run finds no work. A claim at or after it is
	// the tick of the next run, not a missed tick.
	want := 2

	next, err := s.ClaimTick(t.Context(), spec)

	switch {
	case errors.Is(err, durable.ErrNoWork):
	case err != nil:
		t.Fatalf("second claim after missed ticks: %v", err)
	case next.Schedule.Now.Before(late.Schedule.NextRunAt) ||
		next.Operation.Key != durable.TickKey(spec.Name, late.Schedule.NextRunAt):
		t.Fatalf(
			"second claim after missed ticks at %v = operation %q, want ErrNoWork before %v: missed ticks run once",
			next.Schedule.Now, next.Operation.Key, late.Schedule.NextRunAt,
		)
	default:
		t.Logf("the second claim came at %v, after the next run at %v", next.Schedule.Now, late.Schedule.NextRunAt)

		want = 3
	}

	if ops := mustList(t, s, durable.Filter{Kind: kindA, States: nil, Limit: 0}); len(ops) != want {
		t.Fatalf("%d operations after one tick, three missed ticks and a claim, want %d", len(ops), want)
	}
}

func testTickClockSkew(t *testing.T, h ScheduleHarness) {
	t.Helper()

	plain, ahead := h.Open(t, 0), h.Open(t, instanceSkew)
	spec := scheduleSpec("sweep", time.Hour)
	mustEnsure(t, plain, spec)
	mustTick(t, plain, spec)

	notDue, err := ahead.ClaimTick(t.Context(), spec)
	if !errors.Is(err, durable.ErrNoWork) {
		t.Fatalf("ClaimTick from an instance one hour ahead, before the next run: error = %v, want ErrNoWork", err)
	}

	// The wait comes from the clock of the store, so it is about one hour,
	// not 0.
	checkNotDue(t, notDue, "sweep", time.Hour)

	sc, err := ahead.GetSchedule(t.Context(), "sweep")
	if err != nil || sc.Wait() < time.Hour-time.Minute {
		t.Fatalf("schedule from an instance one hour ahead = %+v, %v; want the next run about one hour away", sc, err)
	}
}

func testScheduleShorterInterval(t *testing.T, h ScheduleHarness) {
	t.Helper()

	s := h.Open(t, 0)
	mustEnsure(t, s, scheduleSpec("sweep", time.Hour))
	mustTick(t, s, scheduleSpec("sweep", time.Hour))

	short := scheduleSpec("sweep", tickInterval)

	sc := mustEnsure(t, s, short)
	if sc.Interval != tickInterval || sc.Wait() > tickInterval+clockSlack {
		t.Fatalf("schedule after a shorter interval = %+v, want the next run at most %v away", sc, tickInterval)
	}

	pause(t, sc.Wait()+2*pollEvery)

	if _, err := s.ClaimTick(t.Context(), short); err != nil {
		t.Fatalf("ClaimTick after the shorter interval: %v", err)
	}
}
