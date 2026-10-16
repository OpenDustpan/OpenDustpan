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

// TestDurableConformance runs the durable operations suite. Each handle of a
// case opens the one database file of the case, as two instances open one
// database. The store reads every time from the SQLite clock in SQL, so the
// clock offset of a handle changes nothing.
func TestDurableConformance(t *testing.T) {
	t.Parallel()

	durabletest.Run(t, func(t *testing.T) durabletest.Harness {
		t.Helper()

		return durableHarness(t)
	})
}

// durableHarness returns a harness on a fresh database file.
func durableHarness(t *testing.T, opts ...sqlite.Option) durabletest.Harness {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")
	copyTemplate(t, path)

	domain, err := sqlite.DurableDomainForTest(t.Context(), openWith(t, path, opts...))
	if err != nil {
		t.Fatalf("domain: %v", err)
	}

	return durabletest.Harness{
		Open: func(t *testing.T, _ time.Duration) durable.Store {
			t.Helper()

			return openWith(t, path, opts...).DurableOperations()
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
// stalled write records nothing, and another handle writes before the stall
// ends.
func TestDurableStalledStepEndsAtBound(t *testing.T) {
	t.Parallel()

	const (
		bound = 300 * time.Millisecond
		stall = 3 * time.Second
	)

	h := durableHarness(t, sqlite.WithStepTransactionBound(bound))
	holder, other := h.Open(t, 0), h.Open(t, 0)
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

	// The write of the other handle waits for the write lock, which the
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

	if writeErr := <-done; !errors.Is(writeErr, sqlite.ErrStepTransactionBoundForTest) {
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
