package notifications_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/authgate/authgatetest"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/notify/sinks"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// counterWarningText is the fixed text of the warning about a failed shared
// count of the tests of notification destinations.
const counterWarningText = "the server cannot count the tests of notification destinations"

// TestCounterFailureWarnsBounded proves that a test of a sink whose shared
// count fails answers 503, sends nothing and writes one warning for each
// window, with the count of the events that the window suppressed.
func TestCounterFailureWarnsBounded(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		sends      int
		suppressed int
	}{
		{"one test", 1, 0},
		{"ten tests", 10, 9},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f, counter := counterFixture(t)
			s := f.makeWebhook(t)
			counter.Break()

			checkBoundedWarnings(t, f, base+"/"+s.ID+"/test", tc.sends, tc.suppressed)
		})
	}
}

// TestCounterClockStepBackWarns proves that a clock that goes back before the
// last warning does not silence the warning: the next failure logs again.
func TestCounterClockStepBackWarns(t *testing.T) {
	t.Parallel()

	f, counter := counterFixture(t)
	s := f.makeWebhook(t)
	counter.Break()

	path := base + "/" + s.ID + "/test"

	wantCounterUnavailable(t, f, path, 0)

	f.clock.advance(-2 * time.Minute)

	wantCounterUnavailable(t, f, path, 1)

	if n := strings.Count(f.logs.String(), counterWarningText); n != 2 {
		t.Fatalf("warnings after the clock stepped back = %d, want 2: %s", n, f.logs.String())
	}
}

// counterFixture returns a fixture with a breakable counter of the shared
// test limits, and that counter.
func counterFixture(t *testing.T) (fixture, *storetest.BreakableCounter) {
	t.Helper()

	var counter *storetest.BreakableCounter

	f := newFixtureWithCounter(t, func(now func() time.Time) authgate.FailureCounter {
		counter = storetest.NewBreakableCounter(authgatetest.NewMemory(now))

		return counter
	})

	return f, counter
}

// checkBoundedWarnings sends the sink test of path sends times with the
// counter broken and checks one warning, advances the clock past the window,
// sends once more and checks the second warning and its suppressed count.
func checkBoundedWarnings(t *testing.T, f fixture, path string, sends, suppressed int) {
	t.Helper()

	for i := range sends {
		wantCounterUnavailable(t, f, path, i)
	}

	logs := f.logs.String()
	if n := strings.Count(logs, counterWarningText); n != 1 {
		t.Fatalf("warnings after %d tests = %d, want 1: %s", sends, n, logs)
	}

	checkWarningText(t, logs, "suppressed=0")

	if n := f.hook.count(); n != 0 {
		t.Errorf("sender got %d messages, want none", n)
	}

	if n := len(f.sinkEntries(t)); n != 1 {
		t.Errorf("audit entries = %d, want only the create", n)
	}

	f.clock.advance(2 * time.Minute)

	wantCounterUnavailable(t, f, path, sends)

	logs = f.logs.String()
	if n := strings.Count(logs, counterWarningText); n != 2 {
		t.Fatalf("warnings after the window = %d, want 2: %s", n, logs)
	}

	checkWarningText(t, logs, fmt.Sprintf("suppressed=%d", suppressed))
}

// checkWarningText fails the test when the warnings do not hold want, are not
// at Warn, or hold the text of the failure of the counter or of the limits.
func checkWarningText(t *testing.T, logs, want string) {
	t.Helper()

	if !strings.Contains(logs, "level=WARN") {
		t.Errorf("the warning is not at Warn: %s", logs)
	}

	if !strings.Contains(logs, want) {
		t.Errorf("the warning does not hold %q: %s", want, logs)
	}

	for _, text := range []string{storetest.ErrCounterBroken.Error(), sinks.ErrLimitUnavailable.Error()} {
		if strings.Contains(logs, text) {
			t.Errorf("the warning holds %q: %s", text, logs)
		}
	}
}

// wantCounterUnavailable fails the test unless the sink test of path answers
// 503 with the code of a failed shared count.
func wantCounterUnavailable(t *testing.T, f fixture, path string, i int) {
	t.Helper()

	got := f.do(t, send("admin", http.MethodPost, path, ""))
	if got.code != http.StatusServiceUnavailable {
		t.Fatalf("test %d: %d %s, want %d", i, got.code, got.body, http.StatusServiceUnavailable)
	}

	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusServiceUnavailable, []byte(got.body),
		errcodetest.Want{Code: errcode.SinkTestLimitUnavailable, Args: nil, RequestBody: nil})
}
