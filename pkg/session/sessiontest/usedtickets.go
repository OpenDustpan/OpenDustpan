package sessiontest

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/session"
)

// MemoryTickets is an in-memory session.UsedTickets for tests. It removes
// the records whose until is past at each Use. It is safe for concurrent
// use.
type MemoryTickets struct {
	now   func() time.Time
	used  map[string]time.Time
	mu    sync.Mutex
	err   error
	calls atomic.Int64
}

var _ session.UsedTickets = (*MemoryTickets)(nil)

// NewMemoryTickets returns an empty MemoryTickets with the clock now. Nil
// uses time.Now.
func NewMemoryTickets(now func() time.Time) *MemoryTickets {
	if now == nil {
		now = time.Now
	}

	return &MemoryTickets{now: now, used: map[string]time.Time{}, mu: sync.Mutex{}, err: nil, calls: atomic.Int64{}}
}

// Use implements session.UsedTickets.
func (m *MemoryTickets) Use(_ context.Context, ticket string, until time.Time) error {
	m.calls.Add(1)

	if ticket == "" || until.IsZero() {
		return fmt.Errorf("%w: the ticket and the until time are required", session.ErrMethodInput)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.err != nil {
		return m.err
	}

	now := m.now()

	for k, v := range m.used {
		if !v.After(now) {
			delete(m.used, k)
		}
	}

	if _, ok := m.used[ticket]; ok {
		return session.ErrReplay
	}

	m.used[ticket] = until

	return nil
}

// Fail makes each later Use return err, as a store that cannot write. Nil
// ends it.
func (m *MemoryTickets) Fail(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.err = err
}

// Calls returns the count of the calls of Use.
func (m *MemoryTickets) Calls() int64 {
	return m.calls.Load()
}

// TicketsFixture is a session.UsedTickets with its clock.
type TicketsFixture struct {
	// Tickets is the store under test.
	Tickets session.UsedTickets
	// Advance moves the clock of the store forward by d.
	Advance func(d time.Duration)
	// Now is the clock of the store.
	Now func() time.Time
}

// RunUsedTickets runs the conformance suite of a session.UsedTickets. The
// factory returns a fixture over an empty store.
func RunUsedTickets(t *testing.T, factory func(t *testing.T) TicketsFixture) {
	t.Helper()

	cases := []struct {
		run  func(t *testing.T, f TicketsFixture)
		name string
	}{
		{name: "TheFirstUseIsNil", run: utTheFirstUseIsNil},
		{name: "ASecondUseIsErrReplay", run: utASecondUseIsErrReplay},
		{name: "OtherTicketsAreIndependent", run: utOtherTicketsAreIndependent},
		{name: "ARecordEndsAtItsUntil", run: utARecordEndsAtItsUntil},
		{name: "ConcurrentUsesOfOneTicketPassOnce", run: utConcurrentUsesOfOneTicketPassOnce},
		{name: "AnEmptyTicketOrAZeroUntilIsErrMethodInput", run: utAnEmptyTicketOrAZeroUntilIsErrMethodInput},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.run(t, factory(t))
		})
	}
}

// suiteTicket is the ticket of most cases.
const suiteTicket = "ST-1-c3VpdGUtdGlja2V0"

// utTheFirstUseIsNil is the case TheFirstUseIsNil.
func utTheFirstUseIsNil(t *testing.T, f TicketsFixture) {
	t.Helper()

	if err := f.Tickets.Use(t.Context(), suiteTicket, f.Now().Add(session.UsedTicketTTL)); err != nil {
		t.Fatalf("Use() = %v, want nil", err)
	}
}

// utASecondUseIsErrReplay is the case ASecondUseIsErrReplay.
func utASecondUseIsErrReplay(t *testing.T, f TicketsFixture) {
	t.Helper()

	until := f.Now().Add(session.UsedTicketTTL)
	if err := f.Tickets.Use(t.Context(), suiteTicket, until); err != nil {
		t.Fatalf("Use() = %v, want nil", err)
	}

	f.Advance(session.UsedTicketTTL - time.Minute)

	if err := f.Tickets.Use(t.Context(), suiteTicket, f.Now().Add(session.UsedTicketTTL)); !errors.Is(err,
		session.ErrReplay) {
		t.Fatalf("second Use() = %v, want ErrReplay", err)
	}
}

// utOtherTicketsAreIndependent is the case OtherTicketsAreIndependent.
func utOtherTicketsAreIndependent(t *testing.T, f TicketsFixture) {
	t.Helper()

	until := f.Now().Add(session.UsedTicketTTL)

	for i := range 3 {
		if err := f.Tickets.Use(t.Context(), suiteTicket+"-"+strconv.Itoa(i), until); err != nil {
			t.Fatalf("Use(%d) = %v, want nil", i, err)
		}
	}
}

// utARecordEndsAtItsUntil is the case ARecordEndsAtItsUntil.
func utARecordEndsAtItsUntil(t *testing.T, f TicketsFixture) {
	t.Helper()

	if err := f.Tickets.Use(t.Context(), suiteTicket, f.Now().Add(session.UsedTicketTTL)); err != nil {
		t.Fatalf("Use() = %v, want nil", err)
	}

	f.Advance(session.UsedTicketTTL + time.Second)

	if err := f.Tickets.Use(t.Context(), suiteTicket, f.Now().Add(session.UsedTicketTTL)); err != nil {
		t.Fatalf("Use() after until = %v, want nil", err)
	}
}

// utConcurrentUsesOfOneTicketPassOnce is the case ConcurrentUsesOfOneTicketPassOnce.
func utConcurrentUsesOfOneTicketPassOnce(t *testing.T, f TicketsFixture) {
	t.Helper()

	const n = 16

	var (
		wg      sync.WaitGroup
		passed  atomic.Int64
		replays atomic.Int64
	)

	until := f.Now().Add(session.UsedTicketTTL)

	for range n {
		wg.Go(func() {
			switch err := f.Tickets.Use(t.Context(), suiteTicket, until); {
			case err == nil:
				passed.Add(1)
			case errors.Is(err, session.ErrReplay):
				replays.Add(1)
			default:
				t.Errorf("Use() = %v", err)
			}
		})
	}

	wg.Wait()

	if passed.Load() != 1 || replays.Load() != n-1 {
		t.Fatalf("passed %d and replays %d, want 1 and %d", passed.Load(), replays.Load(), n-1)
	}
}

// utAnEmptyTicketOrAZeroUntilIsErrMethodInput is the case AnEmptyTicketOrAZeroUntilIsErrMethodInput.
func utAnEmptyTicketOrAZeroUntilIsErrMethodInput(t *testing.T, f TicketsFixture) {
	t.Helper()

	if err := f.Tickets.Use(t.Context(), "", f.Now().Add(time.Hour)); !errors.Is(err, session.ErrMethodInput) {
		t.Fatalf("Use(empty ticket) = %v, want ErrMethodInput", err)
	}

	if err := f.Tickets.Use(t.Context(), suiteTicket, time.Time{}); !errors.Is(err, session.ErrMethodInput) {
		t.Fatalf("Use(zero until) = %v, want ErrMethodInput", err)
	}
}
