package forge_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/forge"
)

// TestAppBudgetBoundsTheCallsOfOneApp spends the budget of one app: the call
// over the limit is refused with a wait and not recorded, another app keeps
// its own budget, and the call passes again after the window.
func TestAppBudgetBoundsTheCallsOfOneApp(t *testing.T) {
	t.Parallel()

	b, err := forge.NewAppBudget(2, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	now := time.UnixMilli(1_700_000_000_000)

	for i, tc := range []struct {
		app   string
		after time.Duration
		want  bool
	}{
		{"https://api.github.com#1", 0, true},
		{"https://api.github.com#1", time.Second, true},
		{"https://api.github.com#1", 2 * time.Second, false},
		{"https://api.github.com#1", 3 * time.Second, false},
		{"https://api.github.com#2", 3 * time.Second, true},
		{"https://ghe.example#1", 3 * time.Second, true},
		{"https://api.github.com#1", time.Minute, true},
	} {
		wait, ok := b.Spend(tc.app, now.Add(tc.after))
		if ok != tc.want {
			t.Fatalf("call %d of %s = %v, want %v", i, tc.app, ok, tc.want)
		}

		if !ok && wait <= 0 {
			t.Fatalf("call %d: refused with no wait", i)
		}
	}
}

// TestAppBudgetAllowsTheLimitAtOneTime spends one budget of one app from
// several goroutines at one fixed time. Exactly the limit of the calls pass,
// whatever the order of the goroutines.
func TestAppBudgetAllowsTheLimitAtOneTime(t *testing.T) {
	t.Parallel()

	const limit = 50

	b, err := forge.NewAppBudget(limit, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	now := time.UnixMilli(1_700_000_000_000)
	app := "https://api.github.com#1"

	var wg sync.WaitGroup

	var mu sync.Mutex

	passed := 0

	for range 4 * limit {
		wg.Go(func() {
			if _, ok := b.Spend(app, now); !ok {
				return
			}

			mu.Lock()
			defer mu.Unlock()

			passed++
		})
	}

	wg.Wait()

	if passed != limit {
		t.Fatalf("calls allowed = %d, want %d", passed, limit)
	}
}

// TestAppBudgetRefusesLimitsThatAreNotPositive checks that a budget that
// would be off cannot be made, and that a nil budget refuses each call.
func TestAppBudgetRefusesLimitsThatAreNotPositive(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		limit  int
		window time.Duration
	}{
		"zero calls": {0, time.Minute}, "negative calls": {-1, time.Minute},
		"zero window": {1, 0}, "negative window": {1, -time.Minute},
	} {
		if _, err := forge.NewAppBudget(tc.limit, tc.window); !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("%s: NewAppBudget = %v, want ErrInvalid", name, err)
		}
	}

	var none *forge.AppBudget

	if _, ok := none.Spend("https://api.github.com#1", time.UnixMilli(1_700_000_000_000)); ok {
		t.Error("a nil budget allowed a call")
	}
}
