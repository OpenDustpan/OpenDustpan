package sqlite_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/durable/durabletest"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// TestDurableConformance runs the durable operations suite. One SQLite file
// allows one store, so each case runs on the one store of its database file.
// The store reads every time from the SQLite clock in SQL, so the clock
// offset of a handle changes nothing.
func TestDurableConformance(t *testing.T) {
	t.Parallel()

	durabletest.Run(t, func(t *testing.T) durabletest.Harness {
		t.Helper()

		return durableHarness(t)
	})
}

// TestDurableScheduleConformance runs the schedule suite. One SQLite file
// allows one store, so the suite gets that store on every Open call: one
// database with concurrent callers.
func TestDurableScheduleConformance(t *testing.T) {
	t.Parallel()

	durabletest.RunSchedules(t, func(t *testing.T) durabletest.ScheduleHarness {
		t.Helper()

		path := filepath.Join(t.TempDir(), "dustpan.db")
		copyTemplate(t, path)

		s := openWith(t, path)

		return durabletest.ScheduleHarness{
			Open: func(t *testing.T, _ time.Duration) durabletest.ScheduledStore {
				t.Helper()

				ops := s.DurableOperations()
				if ops == nil {
					t.Fatal("durable operations are nil, want *sqlite.DurableOperations")
				}

				return ops
			},
		}
	})
}

// TestDurableHeartbeatConformance runs the heartbeat suite. One SQLite file
// allows one store, so the suite gets that store on every Open call.
func TestDurableHeartbeatConformance(t *testing.T) {
	t.Parallel()

	durabletest.RunHeartbeats(t, func(t *testing.T) durabletest.HeartbeatHarness {
		t.Helper()

		path := filepath.Join(t.TempDir(), "dustpan.db")
		copyTemplate(t, path)

		s := openWith(t, path)

		return durabletest.HeartbeatHarness{
			Open: func(t *testing.T, _ time.Duration) durable.HeartbeatStore {
				t.Helper()

				ops := s.DurableOperations()
				if ops == nil {
					t.Fatal("durable operations are nil, want *sqlite.DurableOperations")
				}

				return ops
			},
			Seed: func(t *testing.T, owner string, at time.Time, lease time.Duration) {
				t.Helper()

				if err := sqlite.SeedHeartbeatRow(t.Context(), s, owner, at, lease); err != nil {
					t.Fatalf("seed heartbeat: %v", err)
				}
			},
		}
	})
}

// durableHarness returns a harness on a fresh database file. One SQLite file
// allows one store, so every Open call returns the operations of that one
// store.
func durableHarness(t *testing.T, opts ...sqlite.Option) durabletest.Harness {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")
	copyTemplate(t, path)

	s := openWith(t, path, opts...)

	domain, err := sqlite.DurableDomainForTest(t.Context(), s)
	if err != nil {
		t.Fatalf("domain: %v", err)
	}

	return durabletest.Harness{
		Open: func(t *testing.T, _ time.Duration) durable.Store {
			t.Helper()

			return s.DurableOperations()
		},
		Domain: domain,
	}
}

// openWith opens a store on path with opts, closed when the test ends.
func openWith(t *testing.T, path string, opts ...sqlite.Option) *sqlite.Store {
	t.Helper()

	s, err := sqlite.Open(t.Context(), path, opts...)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	return s
}

// TestDurableStalledStepEndsAtBound proves the SQLite rule for a stalled
// holder: a step transaction that stalls holds the write lock of the
// database, so it rolls back at the bound of the step transaction. The
// stalled write records nothing, and another caller writes before the stall
// ends. One store serves every caller here: holder and other are the same
// store.
func TestDurableStalledStepEndsAtBound(t *testing.T) {
	t.Parallel()

	const (
		bound = 300 * time.Millisecond
		stall = 3 * time.Second
	)

	h := durableHarness(t, sqlite.WithStepTransactionBound(bound))
	other := h.Open(t, 0)
	holder := other
	held := claimOne(t, holder)

	ns, err := h.Domain.CreateNamespace(t.Context(), "ns-stall")
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	stalled, stallEnd := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)

	go func() {
		done <- h.Domain.WriteStep(t.Context(), held.Claim.Step("one"), ns, "stalled", durabletest.Hooks{
			Before: func() error {
				close(stalled)

				select {
				case <-time.After(stall):
				case <-t.Context().Done():
				}

				close(stallEnd)

				return nil
			},
			After: nil,
		})
	}()

	<-stalled

	// The write of the other caller waits for the write lock, which the
	// stalled step transaction holds until its bound.
	_, _, startErr := other.Start(t.Context(), durable.StartRequest{Key: "k2", Kind: stallKind, Version: 0, Input: nil})
	if startErr != nil {
		t.Fatalf("start while a step transaction stalls: %v", startErr)
	}

	select {
	case <-stallEnd:
		t.Fatal("the other write ended after the stall, want it to end at the bound of the step transaction")
	default:
	}

	if writeErr := <-done; !errors.Is(writeErr, sqlite.ErrStepTransactionBound) {
		t.Fatalf("stalled WriteStep: error = %v, want the step transaction bound", writeErr)
	}

	checkNothingRecorded(t, h, other, held.ID)
}

// stallKind is the operation kind of the stall test.
const stallKind durable.Kind = "kind-a"

// claimOne starts one operation through s and claims it with a long lease.
func claimOne(t *testing.T, s durable.Store) durable.Operation {
	t.Helper()

	req := durable.StartRequest{Key: "k", Kind: stallKind, Version: 0, Input: nil}
	if _, _, err := s.Start(t.Context(), req); err != nil {
		t.Fatalf("start: %v", err)
	}

	held, err := s.Claim(t.Context(), durable.ClaimRequest{
		Owner: "instance-a", Kinds: []durable.Kind{stallKind}, MaxVersions: nil, Lease: time.Minute,
		TakeCanceled: false,
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	return held
}

// checkNothingRecorded fails the test when operation id holds a step or the
// domain holds a value.
func checkNothingRecorded(t *testing.T, h durabletest.Harness, s durable.Store, id durable.ID) {
	t.Helper()

	got, err := s.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if len(got.Steps) != 0 {
		t.Fatalf("steps after a stalled step transaction = %+v, want none", got.Steps)
	}

	values, err := h.Domain.Values(t.Context())
	if err != nil {
		t.Fatalf("values: %v", err)
	}

	if len(values) != 0 {
		t.Fatalf("domain values after a stalled step transaction = %v, want none", values)
	}
}

// TestDurableStepWaitsBehindWriteLock proves that a step record reads the
// clock of the store after it holds the write lock, not before. One store
// serves every caller here: one caller holds the write lock longer than a
// short lease through that store, and a RecordStep of another caller waits
// behind it and then finds the claim expired.
func TestDurableStepWaitsBehindWriteLock(t *testing.T) {
	t.Parallel()

	const (
		lease = 300 * time.Millisecond
		hold  = 900 * time.Millisecond
	)

	h := durableHarness(t)
	other := h.Open(t, 0)
	holder := other

	ns, err := h.Domain.CreateNamespace(t.Context(), "ns-lock")
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	req := durable.StartRequest{Key: "k", Kind: stallKind, Version: 0, Input: nil}
	if _, _, startErr := holder.Start(t.Context(), req); startErr != nil {
		t.Fatalf("start: %v", startErr)
	}

	held, err := holder.Claim(t.Context(), durable.ClaimRequest{
		Owner: "instance-a", Kinds: []durable.Kind{stallKind}, MaxVersions: nil, Lease: lease,
		TakeCanceled: false,
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	locked := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		done <- h.Domain.WriteStep(t.Context(), held.Claim.Step("held"), ns, "held", durabletest.Hooks{
			Before: func() error {
				close(locked)

				select {
				case <-time.After(hold):
				case <-t.Context().Done():
				}

				return nil
			},
			After: nil,
		})
	}()

	<-locked

	recordErr := other.RecordStep(t.Context(), held.Claim.Step("late"), nil)
	if !errors.Is(recordErr, durable.ErrClaimLost) {
		t.Fatalf("RecordStep behind a held write lock: error = %v, want ErrClaimLost", recordErr)
	}

	if writeErr := <-done; !errors.Is(writeErr, durable.ErrClaimLost) {
		t.Fatalf("WriteStep that held the lock past the lease: error = %v, want ErrClaimLost", writeErr)
	}

	checkNothingRecorded(t, h, other, held.ID)
}
