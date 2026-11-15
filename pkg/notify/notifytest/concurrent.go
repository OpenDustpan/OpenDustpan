package notifytest

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
)

// workers is the count of goroutines that the concurrent cases start.
const workers = 8

// testConcurrentAppend appends from many goroutines to one scope. The
// sequences of the scope are 1 to workers with no gap and no repeat, and
// After shows them in order.
func testConcurrentAppend(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)

	var wg sync.WaitGroup

	errs := make([]error, workers)

	for i := range workers {
		wg.Go(func() {
			errs[i] = h.Outbox.Append(t.Context(), fence,
				[]notify.Notification{sample(fmt.Sprintf("ntf-%d", i), shop)})
		})
	}

	wg.Wait()

	if err := errors.Join(errs...); err != nil {
		t.Fatalf("concurrent Append: %v", err)
	}

	got, err := h.Outbox.After(t.Context(), shop, 0, workers+1)
	if err != nil || len(got) != workers {
		t.Fatalf("After = %d notifications (%v), want %d", len(got), err, workers)
	}

	for i := range got {
		if want := int64(i + 1); got[i].Sequence != want {
			t.Fatalf("sequence at place %d = %d, want %d: %+v", i, got[i].Sequence, want, got)
		}
	}
}

// testConcurrentCursor moves one cursor from many goroutines, all from the
// same sequence. Exactly one move wins, and the others get ErrConflict, so
// two instances never route one notification twice.
func testConcurrentCursor(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)

	var wg sync.WaitGroup

	errs := make([]error, workers)

	for i := range workers {
		wg.Go(func() { errs[i] = h.Outbox.MoveCursor(t.Context(), fence, move(0, int64(i+1))) })
	}

	wg.Wait()

	won := 0

	for _, err := range errs {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, notify.ErrConflict):
			t.Fatalf("concurrent MoveCursor: err = %v, want nil or ErrConflict", err)
		default:
			// A move that lost the race.
		}
	}

	if won != 1 {
		t.Fatalf("concurrent MoveCursor: %d moves won, want 1: %v", won, errs)
	}

	if c, err := h.Outbox.Cursor(t.Context(), "dispatch", shop); err != nil || c < 1 || c > workers {
		t.Fatalf("Cursor = %d (%v), want the target of the move that won", c, err)
	}
}

// testConcurrentClaim claims from many goroutines. Each due delivery goes to
// one claim only, so two instances never hold one delivery under two live
// claims.
func testConcurrentClaim(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)

	const total = 20

	for i := range total {
		enqueue(t, h, fence, delivery(fmt.Sprintf("dlv-%02d", i), fmt.Sprintf("ntf-%02d", i), now()))
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		claimed []string
	)

	errs := make([]error, workers)

	for i := range workers {
		wg.Go(func() {
			got, err := h.Deliveries.Claim(t.Context(), fence, claimAt(now(), 3))
			errs[i] = err

			mu.Lock()
			defer mu.Unlock()

			for j := range got {
				claimed = append(claimed, got[j].ID)
			}
		})
	}

	wg.Wait()

	if err := errors.Join(errs...); err != nil {
		t.Fatalf("concurrent Claim: %v", err)
	}

	slices.Sort(claimed)

	if len(slices.Compact(slices.Clone(claimed))) != len(claimed) {
		t.Fatalf("concurrent Claim returned a delivery twice: %v", claimed)
	}

	// Each call takes up to 3, and 8 calls ask for 24, so 20 are due and all
	// of them are taken at once.
	if len(claimed) != total {
		t.Fatalf("concurrent Claim took %d deliveries, want %d", len(claimed), total)
	}
}

// testConcurrentRedeliver leaves one redelivery on a stopped delivery and
// redelivers it from many goroutines. Exactly one call wins, so the count
// never passes MaxAttempts.
func testConcurrentRedeliver(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	subscribe(t, h, fence)
	enqueue(t, h, fence, delivery("dlv-1", "ntf-1", now()))

	stopAt := now()
	stopDelivery(t, h, fence, stopAt)

	// Leave one redelivery: the calls below race for it.
	for n := 1; n < notify.MaxAttempts; n++ {
		at := stopAt.Add(notify.RetryWait(n))
		checkRedeliverAccepted(t, h, fence, "dlv-1", at)

		stopAt = at
		stopDelivery(t, h, fence, stopAt)
	}

	at := stopAt.Add(notify.RetryWait(notify.MaxAttempts))

	checkOneRedeliverWon(t, redeliverTogether(t, h, fence, at))

	if d := mustDelivery(t, h, "dlv-1"); d.Redeliveries != notify.MaxAttempts {
		t.Fatalf("Redeliveries after the concurrent redelivers = %d, want %d", d.Redeliveries, notify.MaxAttempts)
	}

	// The delivery is at the limit, and the wait after its last stop is
	// over. A redeliver is still refused, so the count never passes
	// MaxAttempts.
	stopDelivery(t, h, fence, at)

	later := at.Add(notify.RetryWait(notify.MaxAttempts + 1))
	if err := h.Deliveries.Redeliver(t.Context(), "dlv-1", later, fence, manage()); !errors.Is(
		err,
		notify.ErrRedeliverLimit,
	) {
		t.Fatalf("Redeliver at the limit after the wait: err = %v, want ErrRedeliverLimit", err)
	}

	if d := mustDelivery(t, h, "dlv-1"); d.Redeliveries != notify.MaxAttempts {
		t.Fatalf("Redeliveries after a refused redeliver at the limit = %d, want %d",
			d.Redeliveries, notify.MaxAttempts)
	}
}

// redeliverTogether calls Redeliver for dlv-1 from many goroutines at at,
// all after the start barrier opens.
func redeliverTogether(t *testing.T, h Harness, fence namespace.Fence, at time.Time) []error {
	t.Helper()

	var (
		wg    sync.WaitGroup
		ready sync.WaitGroup
	)

	start := make(chan struct{})
	errs := make([]error, workers)

	ready.Add(workers)

	for i := range workers {
		wg.Go(func() {
			ready.Done()

			<-start
			errs[i] = h.Deliveries.Redeliver(t.Context(), "dlv-1", at, fence, manage())
		})
	}

	ready.Wait()
	close(start)
	wg.Wait()

	return errs
}

// checkOneRedeliverWon fails unless exactly one error is nil and each other
// error is ErrConflict or ErrRedeliverLimit.
func checkOneRedeliverWon(t *testing.T, errs []error) {
	t.Helper()

	won, conflict, limit := 0, 0, 0

	for _, err := range errs {
		switch {
		case err == nil:
			won++
		case errors.Is(err, notify.ErrConflict):
			conflict++
		case errors.Is(err, notify.ErrRedeliverLimit):
			limit++
		default:
			t.Fatalf("concurrent Redeliver: err = %v, want nil, ErrConflict or ErrRedeliverLimit", err)
		}
	}

	if won != 1 || conflict+limit != workers-1 {
		t.Fatalf(
			"concurrent Redeliver: %d won, %d got ErrConflict and %d got ErrRedeliverLimit, want 1 and %d together",
			won,
			conflict,
			limit,
			workers-1,
		)
	}
}
