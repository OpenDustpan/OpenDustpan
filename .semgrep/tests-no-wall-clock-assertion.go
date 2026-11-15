// Test cases of the semgrep rule tests-no-wall-clock-assertion. Run them with
// semgrep --test (make semgrep). The Go tool skips this directory,
// because its name starts with a dot.

package fixture

import (
	"fmt"
	"testing"
	"time"
)

func TestUpperBoundOnTimeSince(t *testing.T) {
	start := time.Now()
	run()

	// ruleid: tests-no-wall-clock-assertion
	if time.Since(start) > time.Second {
		t.Fatalf("run took too long")
	}
}

func TestLowerBoundInTheIfInit(t *testing.T) {
	start := time.Now()
	run()

	// ruleid: tests-no-wall-clock-assertion
	if took := time.Since(start); took < 50*time.Millisecond {
		t.Errorf("run took %s, want at least 50ms", took)
	}
}

func TestMeasuredDuration(t *testing.T) {
	start := time.Now()
	run()
	elapsed := time.Since(start)

	// ruleid: tests-no-wall-clock-assertion
	if elapsed > 2*time.Second {
		t.Fatal("too slow")
	}
}

func TestMeasuredWithSub(t *testing.T) {
	start := time.Now()
	run()
	elapsed := time.Now().Sub(start)

	// ruleid: tests-no-wall-clock-assertion
	if elapsed >= time.Second {
		t.Fatal("too slow")
	}
}

func TestBoundOnTheLeft(t *testing.T) {
	start := time.Now()
	run()

	// ruleid: tests-no-wall-clock-assertion
	if 5*time.Second < time.Since(start) {
		t.Fatal("too slow")
	}
}

func waitForDone(tb testing.TB, done func() bool) {
	tb.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for !done() {
		// ruleid: tests-no-wall-clock-assertion
		if time.Now().After(deadline) {
			tb.Fatal("not done within 10s")
		}

		time.Sleep(10 * time.Millisecond)
	}
}

func TestDeadlineWithUntil(t *testing.T) {
	deadline := time.Now().Add(time.Second)
	for !finished() {
		// ruleid: tests-no-wall-clock-assertion
		if time.Until(deadline) <= 0 {
			t.FailNow()
		}
	}
}

func TestInjectedClockPasses(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return now }
	start := clock()
	now = now.Add(3 * time.Second)

	// ok: tests-no-wall-clock-assertion
	if got := clock().Sub(start); got != 3*time.Second {
		t.Fatalf("elapsed = %s, want 3s", got)
	}
}

func TestDurationThatIsNoWallTime(t *testing.T) {
	timeout := parse("5s")

	// ok: tests-no-wall-clock-assertion
	if timeout > time.Minute {
		t.Fatalf("timeout = %s, want at most a minute", timeout)
	}
}

func TestMeasureWithoutAssertion(t *testing.T) {
	start := time.Now()
	run()

	// ok: tests-no-wall-clock-assertion
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Logf("run took %s", elapsed)
	}
}

func fastest(runs int) time.Duration {
	var best time.Duration

	for range runs {
		start := time.Now()
		run()
		elapsed := time.Since(start)

		// ok: tests-no-wall-clock-assertion
		if best == 0 || elapsed < best {
			best = elapsed
		}
	}

	return best
}

func fakeClockCheck(created time.Time) error {
	age := time.Now().Sub(created)

	// ok: tests-no-wall-clock-assertion
	if age > time.Hour {
		return errExpired
	}

	return nil
}

func fakeStoreCheck(started time.Time) error {
	// ok: tests-no-wall-clock-assertion
	if time.Since(started) > time.Hour {
		return fmt.Errorf("the attempt started at %s: %w", started, errExpired)
	}

	return nil
}

func TestTimestampNearNow(t *testing.T) {
	stamp := issue()

	// ok: tests-no-wall-clock-assertion
	if stamp.After(time.Now().Add(time.Minute)) {
		t.Fatalf("stamp %s is in the future", stamp)
	}
}
