package sessiontest

import (
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/session"
)

// TicketStoreHarness gives RunTicketStore its handles on one database.
type TicketStoreHarness struct {
	// Open returns a handle on the database of the case. Two calls give two
	// handles on the same rows, as two servers of one installation hold. A
	// store that allows one handle may return the same handle again.
	Open func(t *testing.T) session.TicketStore
	// Reopen closes the handle old and returns a new handle on the same
	// rows, as a restart does.
	Reopen func(t *testing.T, old session.TicketStore) session.TicketStore
}

// RunTicketStore runs the conformance suite of a session.TicketStore. The
// factory returns a harness over an empty database.
func RunTicketStore(t *testing.T, factory func(t *testing.T) TicketStoreHarness) {
	t.Helper()

	cases := []struct {
		run  func(t *testing.T, h TicketStoreHarness)
		name string
	}{
		{name: "TheFirstUseIsNil", run: tsTheFirstUseIsNil},
		{name: "ASecondUseIsErrReplay", run: tsASecondUseIsErrReplay},
		{name: "AReplayDoesNotMoveTheUntil", run: tsAReplayDoesNotMoveTheUntil},
		{name: "ADifferentDigestOrProviderIsIndependent", run: tsADifferentDigestOrProviderIsIndependent},
		{name: "ARecordEndsAtItsUntil", run: tsARecordEndsAtItsUntil},
		{name: "AnotherHandleSeesTheRecord", run: tsAnotherHandleSeesTheRecord},
		{name: "AReopenedStoreKeepsTheRecord", run: tsAReopenedStoreKeepsTheRecord},
		{name: "ConcurrentUsesOfOneTicketPassOnce", run: tsConcurrentUsesOfOneTicketPassOnce},
		{name: "ConcurrentUsesAfterAnExpiryPassOnce", run: tsConcurrentUsesAfterAnExpiryPassOnce},
		{name: "PruneDeletesTheEndedRecordsOnly", run: tsPruneDeletesTheEndedRecordsOnly},
		{name: "HostileInputIsErrMethodInput", run: tsHostileInputIsErrMethodInput},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.run(t, factory(t))
		})
	}
}

// tsStart() returns the time at which the cases start.
func tsStart() time.Time {
	return time.UnixMilli(1_800_000_000_000)
}

// tsDigest returns the digest of the ticket number i.
func tsDigest(i int) session.TicketDigest {
	return session.DigestOfTicket("ST-" + strconv.Itoa(i) + "-c3VpdGUtdGlja2V0")
}

// tsTheFirstUseIsNil is the case TheFirstUseIsNil.
func tsTheFirstUseIsNil(t *testing.T, h TicketStoreHarness) {
	t.Helper()

	err := h.Open(t).UseTicket(t.Context(), suiteProvider, tsDigest(1), tsStart(), tsStart().Add(session.UsedTicketTTL))
	if err != nil {
		t.Fatalf("UseTicket() = %v, want nil", err)
	}
}

// tsASecondUseIsErrReplay is the case ASecondUseIsErrReplay.
func tsASecondUseIsErrReplay(t *testing.T, h TicketStoreHarness) {
	t.Helper()

	s := h.Open(t)
	until := tsStart().Add(session.UsedTicketTTL)

	if err := s.UseTicket(t.Context(), suiteProvider, tsDigest(1), tsStart(), until); err != nil {
		t.Fatalf("UseTicket() = %v, want nil", err)
	}

	err := s.UseTicket(t.Context(), suiteProvider, tsDigest(1), tsStart().Add(time.Minute), until)
	if !errors.Is(err, session.ErrReplay) {
		t.Fatalf("second UseTicket() = %v, want ErrReplay", err)
	}
}

// tsAReplayDoesNotMoveTheUntil is the case AReplayDoesNotMoveTheUntil: a
// refused replay with a later until must not extend the record.
func tsAReplayDoesNotMoveTheUntil(t *testing.T, h TicketStoreHarness) {
	t.Helper()

	s := h.Open(t)

	if err := s.UseTicket(t.Context(), suiteProvider, tsDigest(1), tsStart(), tsStart().Add(time.Hour)); err != nil {
		t.Fatalf("UseTicket() = %v, want nil", err)
	}

	later := tsStart().Add(30 * time.Minute)

	err := s.UseTicket(t.Context(), suiteProvider, tsDigest(1), later, later.Add(time.Hour))
	if !errors.Is(err, session.ErrReplay) {
		t.Fatalf("replay with a later until = %v, want ErrReplay", err)
	}

	// The first record ended at tsStart() plus 1 hour. A moved until would
	// still refuse here.
	end := tsStart().Add(time.Hour + time.Second)
	if err := s.UseTicket(t.Context(), suiteProvider, tsDigest(1), end, end.Add(time.Hour)); err != nil {
		t.Fatalf("UseTicket() after the first until = %v, want nil", err)
	}
}

// tsADifferentDigestOrProviderIsIndependent is the case
// ADifferentDigestOrProviderIsIndependent.
func tsADifferentDigestOrProviderIsIndependent(t *testing.T, h TicketStoreHarness) {
	t.Helper()

	s := h.Open(t)
	until := tsStart().Add(session.UsedTicketTTL)

	for _, c := range []struct {
		provider string
		digest   session.TicketDigest
	}{{suiteProvider, tsDigest(1)}, {suiteProvider, tsDigest(2)}, {"other", tsDigest(1)}} {
		if err := s.UseTicket(t.Context(), c.provider, c.digest, tsStart(), until); err != nil {
			t.Fatalf("UseTicket(%q) = %v, want nil", c.provider, err)
		}
	}

	if err := s.UseTicket(t.Context(), "other", tsDigest(1), tsStart(), until); !errors.Is(err, session.ErrReplay) {
		t.Fatalf("second UseTicket(other) = %v, want ErrReplay", err)
	}
}

// tsARecordEndsAtItsUntil is the case ARecordEndsAtItsUntil: at the until
// itself, the record is ended.
func tsARecordEndsAtItsUntil(t *testing.T, h TicketStoreHarness) {
	t.Helper()

	s := h.Open(t)
	until := tsStart().Add(time.Hour)

	if err := s.UseTicket(t.Context(), suiteProvider, tsDigest(1), tsStart(), until); err != nil {
		t.Fatalf("UseTicket() = %v, want nil", err)
	}

	err := s.UseTicket(t.Context(), suiteProvider, tsDigest(1), until.Add(-time.Millisecond), until.Add(time.Hour))
	if !errors.Is(err, session.ErrReplay) {
		t.Fatalf("UseTicket() before the until = %v, want ErrReplay", err)
	}

	err = s.UseTicket(t.Context(), suiteProvider, tsDigest(1), until, until.Add(time.Hour))
	if err != nil {
		t.Fatalf("UseTicket() at the until = %v, want nil", err)
	}
}

// tsAnotherHandleSeesTheRecord is the case AnotherHandleSeesTheRecord: the
// replay across two servers.
func tsAnotherHandleSeesTheRecord(t *testing.T, h TicketStoreHarness) {
	t.Helper()

	until := tsStart().Add(session.UsedTicketTTL)

	if err := h.Open(t).UseTicket(t.Context(), suiteProvider, tsDigest(1), tsStart(), until); err != nil {
		t.Fatalf("UseTicket() on the first handle = %v, want nil", err)
	}

	err := h.Open(t).UseTicket(t.Context(), suiteProvider, tsDigest(1), tsStart(), until)
	if !errors.Is(err, session.ErrReplay) {
		t.Fatalf("UseTicket() on the second handle = %v, want ErrReplay", err)
	}
}

// tsAReopenedStoreKeepsTheRecord is the case AReopenedStoreKeepsTheRecord:
// the replay after a restart.
func tsAReopenedStoreKeepsTheRecord(t *testing.T, h TicketStoreHarness) {
	t.Helper()

	s := h.Open(t)
	until := tsStart().Add(session.UsedTicketTTL)

	if err := s.UseTicket(t.Context(), suiteProvider, tsDigest(1), tsStart(), until); err != nil {
		t.Fatalf("UseTicket() = %v, want nil", err)
	}

	reopened := h.Reopen(t, s)

	err := reopened.UseTicket(t.Context(), suiteProvider, tsDigest(1), tsStart(), until)
	if !errors.Is(err, session.ErrReplay) {
		t.Fatalf("UseTicket() after a reopen = %v, want ErrReplay", err)
	}
}

// tsConcurrentUsesOfOneTicketPassOnce is the case
// ConcurrentUsesOfOneTicketPassOnce: 16 callers over two handles.
func tsConcurrentUsesOfOneTicketPassOnce(t *testing.T, h TicketStoreHarness) {
	t.Helper()

	tsRace(t, []session.TicketStore{h.Open(t), h.Open(t)}, tsStart(), tsStart().Add(session.UsedTicketTTL))
}

// tsConcurrentUsesAfterAnExpiryPassOnce is the case
// ConcurrentUsesAfterAnExpiryPassOnce: the replace of an ended record is
// atomic too.
func tsConcurrentUsesAfterAnExpiryPassOnce(t *testing.T, h TicketStoreHarness) {
	t.Helper()

	stores := []session.TicketStore{h.Open(t), h.Open(t)}

	err := stores[0].UseTicket(t.Context(), suiteProvider, tsDigest(1), tsStart(), tsStart().Add(time.Minute))
	if err != nil {
		t.Fatalf("UseTicket() = %v, want nil", err)
	}

	at := tsStart().Add(2 * time.Minute)

	tsRace(t, stores, at, at.Add(session.UsedTicketTTL))
}

// tsRace calls UseTicket for one ticket from 16 goroutines and requires that
// exactly one returns nil and the others ErrReplay.
func tsRace(t *testing.T, stores []session.TicketStore, at, until time.Time) {
	t.Helper()

	const n = 16

	var (
		wg      sync.WaitGroup
		passed  atomic.Int64
		replays atomic.Int64
	)

	for i := range n {
		wg.Go(func() {
			switch err := stores[i%len(stores)].UseTicket(t.Context(), suiteProvider, tsDigest(1), at,
				until); {
			case err == nil:
				passed.Add(1)
			case errors.Is(err, session.ErrReplay):
				replays.Add(1)
			default:
				t.Errorf("UseTicket() = %v", err)
			}
		})
	}

	wg.Wait()

	if passed.Load() != 1 || replays.Load() != n-1 {
		t.Fatalf("passed %d and replays %d, want 1 and %d", passed.Load(), replays.Load(), n-1)
	}
}

// tsPruneDeletesTheEndedRecordsOnly is the case PruneDeletesTheEndedRecordsOnly.
func tsPruneDeletesTheEndedRecordsOnly(t *testing.T, h TicketStoreHarness) {
	t.Helper()

	s := h.Open(t)

	for i, until := range []time.Duration{time.Minute, 2 * time.Minute, time.Hour} {
		if err := s.UseTicket(t.Context(), suiteProvider, tsDigest(i), tsStart(), tsStart().Add(until)); err != nil {
			t.Fatalf("UseTicket(%d) = %v, want nil", i, err)
		}
	}

	// At minute 2, the records of minutes 1 and 2 ended; a record ends at its until.
	n, err := s.PruneTickets(t.Context(), tsStart().Add(2*time.Minute))
	if err != nil {
		t.Fatalf("PruneTickets() = %v", err)
	}

	if n != 2 {
		t.Fatalf("PruneTickets() deleted %d records, want 2", n)
	}

	err = s.UseTicket(t.Context(), suiteProvider, tsDigest(2), tsStart().Add(2*time.Minute),
		tsStart().Add(time.Hour))
	if !errors.Is(err, session.ErrReplay) {
		t.Fatalf("UseTicket() of the record that did not end = %v, want ErrReplay", err)
	}

	err = s.UseTicket(t.Context(), suiteProvider, tsDigest(0), tsStart().Add(2*time.Minute), tsStart().Add(time.Hour))
	if err != nil {
		t.Fatalf("UseTicket() of a pruned record = %v, want nil", err)
	}
}

// tsHostileInputIsErrMethodInput is the case HostileInputIsErrMethodInput:
// the store checks its input at its own boundary, as if the layers above it
// had failed.
func tsHostileInputIsErrMethodInput(t *testing.T, h TicketStoreHarness) {
	t.Helper()

	s := h.Open(t)
	until := tsStart().Add(time.Hour)

	for _, c := range []struct {
		until    time.Time
		provider string
		name     string
		digest   session.TicketDigest
	}{
		{name: "empty provider", provider: "", digest: tsDigest(1), until: until},
		{
			name: "long provider", provider: string(make([]byte, session.MaxProviderIDLength+1)), digest: tsDigest(1),
			until: until,
		},
		{name: "zero digest", provider: suiteProvider, digest: session.TicketDigest{}, until: until},
		{name: "zero until", provider: suiteProvider, digest: tsDigest(1), until: time.Time{}},
		{name: "until at now", provider: suiteProvider, digest: tsDigest(1), until: tsStart()},
		{name: "until before now", provider: suiteProvider, digest: tsDigest(1), until: tsStart().Add(-time.Hour)},
		{name: "until far ahead", provider: suiteProvider, digest: tsDigest(1), until: tsStart().Add(24 * time.Hour)},
	} {
		err := s.UseTicket(t.Context(), c.provider, c.digest, tsStart(), c.until)
		if !errors.Is(err, session.ErrMethodInput) {
			t.Errorf("UseTicket(%s) = %v, want ErrMethodInput", c.name, err)
		}
	}

	// A refused hostile call wrote nothing, so the ticket is still unused.
	if err := s.UseTicket(t.Context(), suiteProvider, tsDigest(1), tsStart(), until); err != nil {
		t.Fatalf("UseTicket() after the refusals = %v, want nil", err)
	}
}
