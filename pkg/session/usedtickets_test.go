package session_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/session"
)

// errDown is the error of a store that cannot write.
var errDown = errors.New("store down")

// countingTicketStore counts the calls of UseTicket and answers err.
type countingTicketStore struct {
	err   error
	calls atomic.Int64
}

func (c *countingTicketStore) UseTicket(context.Context, string, session.TicketDigest, time.Time, time.Time) error {
	c.calls.Add(1)

	return c.err
}

func (*countingTicketStore) PruneTickets(context.Context, time.Time) (int64, error) {
	return 0, nil
}

// TestStoreTicketsRefuseHostileInputBeforeTheStore sends hostile input to the
// adapter layer alone: the store would accept all of it, so only the adapter
// can refuse it.
func TestStoreTicketsRefuseHostileInputBeforeTheStore(t *testing.T) {
	t.Parallel()

	now := time.UnixMilli(1_800_000_000_000)
	until := now.Add(time.Hour)

	for _, c := range []struct {
		until    time.Time
		name     string
		provider string
		ticket   string
	}{
		{name: "empty provider", provider: "", ticket: "ST-1", until: until},
		{
			name: "long provider", provider: strings.Repeat("p", session.MaxProviderIDLength+1), ticket: "ST-1",
			until: until,
		},
		{name: "empty ticket", provider: "corp", ticket: "", until: until},
		{name: "long ticket", provider: "corp", ticket: strings.Repeat("T", session.MaxTicketLength+1), until: until},
		{name: "zero until", provider: "corp", ticket: "ST-1", until: time.Time{}},
	} {
		store := &countingTicketStore{err: nil, calls: atomic.Int64{}}

		tickets, err := session.NewStoreTickets(store, func() time.Time { return now })
		if err != nil {
			t.Fatalf("NewStoreTickets() = %v", err)
		}

		if err := tickets.Use(t.Context(), c.provider, c.ticket, c.until); !errors.Is(err, session.ErrMethodInput) {
			t.Errorf("Use(%s) = %v, want ErrMethodInput", c.name, err)
		}

		if store.calls.Load() != 0 {
			t.Errorf("Use(%s) called the store %d times, want 0", c.name, store.calls.Load())
		}
	}
}

// TestStoreTicketsFailClosedOnAStoreError proves that a store error is an
// error of Use, never nil, and keeps its identity.
func TestStoreTicketsFailClosedOnAStoreError(t *testing.T) {
	t.Parallel()

	for _, want := range []error{errDown, session.ErrReplay} {
		tickets, err := session.NewStoreTickets(&countingTicketStore{err: want, calls: atomic.Int64{}},
			func() time.Time { return time.UnixMilli(1_800_000_000_000) })
		if err != nil {
			t.Fatalf("NewStoreTickets() = %v", err)
		}

		if err := tickets.Use(t.Context(), "corp", "ST-1", time.UnixMilli(1_800_003_600_000)); !errors.Is(err, want) {
			t.Errorf("Use() = %v, want %v", err, want)
		}
	}
}

// TestNewStoreTicketsRefusesNil proves a missing store or clock is refused.
func TestNewStoreTicketsRefusesNil(t *testing.T) {
	t.Parallel()

	clock := func() time.Time { return time.UnixMilli(0) }

	_, err := session.NewStoreTickets(nil, clock)
	if !errors.Is(err, session.ErrMethodInput) {
		t.Errorf("nil store: err = %v, want ErrMethodInput", err)
	}

	_, err = session.NewStoreTickets(&countingTicketStore{err: nil, calls: atomic.Int64{}}, nil)
	if !errors.Is(err, session.ErrMethodInput) {
		t.Errorf("nil clock: err = %v, want ErrMethodInput", err)
	}
}
