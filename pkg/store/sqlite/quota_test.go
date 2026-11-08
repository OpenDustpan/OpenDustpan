package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/quota"
	"opendustpan.org/opendustpan/internal/quota/quotatest"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// quotaDomain is quotatest.Domain over a Store: it makes the namespaces and
// the fences that the counter needs, and that other ports own.
type quotaDomain struct {
	t *testing.T
	s *sqlite.Store
}

// CreateNamespace implements quotatest.Domain.
func (d quotaDomain) CreateNamespace(ctx context.Context, name namespace.Name) (namespace.Fence, error) {
	d.t.Helper()

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.UnixMilli(1_700_000_000_000).UTC(),
		Name:                name,
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := d.s.CreateNamespace(ctx, ns); err != nil {
		return namespace.Fence{}, fmt.Errorf("create namespace %s: %w", name, err)
	}

	return ns.Fence(), nil
}

// BumpFence implements quotatest.Domain.
func (d quotaDomain) BumpFence(ctx context.Context, fence namespace.Fence) (namespace.Fence, error) {
	d.t.Helper()

	if _, err := d.s.BumpFailoverVersions(ctx); err != nil {
		return namespace.Fence{}, fmt.Errorf("bump failover versions: %w", err)
	}

	ns, err := d.s.GetNamespace(ctx, fence.Namespace)
	if err != nil {
		return namespace.Fence{}, fmt.Errorf("get namespace %s: %w", fence.Namespace, err)
	}

	return ns.Fence(), nil
}

// newQuotaHarness returns one fresh store for one case of the suite.
func newQuotaHarness(t *testing.T) quotatest.Harness {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	copyTemplate(t, path)

	s := openPath(t, path)

	return quotatest.Harness{Counter: s.QuotaCounter(), Domain: quotaDomain{t: t, s: s}}
}

func TestQuotaCounterConformance(t *testing.T) {
	t.Parallel()

	quotatest.Run(t, newQuotaHarness)
}

// quotaTime is the time of the adapter cases.
func quotaTime() time.Time {
	return time.Unix(1_790_000_000, 0).UTC()
}

// newQuotaStore returns a fresh, empty store for one adapter case.
func newQuotaStore(t *testing.T) *sqlite.Store {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	copyTemplate(t, path)

	return openPath(t, path)
}

// quotaFence creates the namespace name in s and returns its fence.
func quotaFence(t *testing.T, s *sqlite.Store, name namespace.Name) namespace.Fence {
	t.Helper()

	fence, err := (quotaDomain{t: t, s: s}).CreateNamespace(t.Context(), name)
	if err != nil {
		t.Fatalf("create namespace %s: %v", name, err)
	}

	return fence
}

// quotaUsages returns the usage of each check, and fails the test when the
// read fails.
func quotaUsages(t *testing.T, s *sqlite.Store, checks []quota.Check, at time.Time) []quota.Usage {
	t.Helper()

	got, err := s.QuotaCounter().Usage(t.Context(), checks, at)
	if err != nil {
		t.Fatalf("Usage() = %v", err)
	}

	if len(got) != len(checks) {
		t.Fatalf("Usage() gave %d usages for %d checks", len(got), len(checks))
	}

	return got
}

// TestQuotaReserveRefusesReservationOfAnotherNamespace proves that the
// counter refuses a reservation of one namespace under the fence of another,
// and stores nothing.
func TestQuotaReserveRefusesReservationOfAnotherNamespace(t *testing.T) {
	t.Parallel()

	s := newQuotaStore(t)
	fence := quotaFence(t, s, "payments")
	quotaFence(t, s, "other")

	// The reservation and its keys are of another namespace, and the check is
	// of the fenced namespace, so that only the check of the reservation
	// namespace can refuse the reservation.
	other := quota.Key{Namespace: "other", Blueprint: "postgres", Kind: quota.KindUnits, Scope: "namespace"}
	mine := quota.Key{Namespace: "payments", Blueprint: "postgres", Kind: quota.KindUnits, Scope: "namespace"}
	r := quota.Reservation{
		ID: "req-1/1", Namespace: "other", State: quota.ReservationOpen,
		Keys: []quota.Key{other}, At: quotaTime(), EndsAt: quotaTime().Add(24 * time.Hour),
	}

	err := s.QuotaCounter().InTx(t.Context(), fence, func(tx quota.Tx) error {
		_, reserveErr := tx.Reserve(t.Context(), r, []quota.Check{{Key: mine, Window: 0, Max: 1}})

		return reserveErr
	})
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("Reserve(a reservation of another namespace) = %v, want ErrFenced", err)
	}

	for _, key := range []quota.Key{other, mine} {
		if u := quotaUsages(t, s, []quota.Check{{Key: key, Window: 0, Max: 0}}, quotaTime())[0]; u.Settled != 0 ||
			u.Reserved != 0 {
			t.Fatalf("Usage(%s) after the refusal = %+v, want no usage", key, u)
		}
	}
}

// TestQuotaRefusesCheckOfAnotherNamespace proves that the counter refuses a
// check whose key is of another namespace than the fence, in a reservation
// and in a record, and stores nothing.
func TestQuotaRefusesCheckOfAnotherNamespace(t *testing.T) {
	t.Parallel()

	mine := quota.Key{Namespace: "payments", Blueprint: "postgres", Kind: quota.KindUnits, Scope: "namespace"}
	otherUnits := quota.Key{Namespace: "other", Blueprint: "postgres", Kind: quota.KindUnits, Scope: "namespace"}
	otherRequests := quota.Key{
		Namespace: "other",
		Blueprint: "postgres",
		Kind:      quota.KindRequests,
		Scope:     "requester:alice",
	}

	cases := []struct {
		name string
		run  func(t *testing.T, s *sqlite.Store, fence namespace.Fence) error
	}{
		{
			name: "reserve",
			run: func(t *testing.T, s *sqlite.Store, fence namespace.Fence) error {
				t.Helper()

				r := quota.Reservation{
					ID: "req-1/1", Namespace: "payments", State: quota.ReservationOpen,
					Keys: []quota.Key{mine}, At: quotaTime(), EndsAt: quotaTime().Add(24 * time.Hour),
				}

				return s.QuotaCounter().InTx(t.Context(), fence, func(tx quota.Tx) error {
					_, err := tx.Reserve(t.Context(), r, []quota.Check{{Key: otherUnits, Window: 0, Max: 1}})

					return err
				})
			},
		},
		{
			name: "record",
			run: func(t *testing.T, s *sqlite.Store, fence namespace.Fence) error {
				t.Helper()

				return s.QuotaCounter().InTx(t.Context(), fence, func(tx quota.Tx) error {
					return tx.Record(t.Context(), quotaTime(),
						[]quota.Check{{Key: otherRequests, Window: time.Hour, Max: 1}})
				})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newQuotaStore(t)
			fence := quotaFence(t, s, "payments")
			quotaFence(t, s, "other")

			if err := tc.run(t, s, fence); !errors.Is(err, namespace.ErrFenced) {
				t.Fatalf("a check of another namespace = %v, want ErrFenced", err)
			}

			got := quotaUsages(
				t,
				s,
				[]quota.Check{
					{Key: mine, Window: 0, Max: 0},
					{Key: otherRequests, Window: time.Hour, Max: 0},
				},
				quotaTime(),
			)
			if got[0].Settled != 0 || got[0].Reserved != 0 {
				t.Fatalf("Usage(%s) after the refusal = %+v, want no reservation", mine, got[0])
			}

			if got[1].Requests != 0 {
				t.Fatalf("Usage(%s) after the refusal = %+v, want no request", otherRequests, got[1])
			}
		})
	}
}

// TestQuotaSetSettledRefusesKeyOfAnotherNamespace proves that the counter
// refuses the settled count of a key of one namespace under the fence of
// another, and writes nothing.
func TestQuotaSetSettledRefusesKeyOfAnotherNamespace(t *testing.T) {
	t.Parallel()

	s := newQuotaStore(t)
	fence := quotaFence(t, s, "payments")
	quotaFence(t, s, "other")

	other := quota.Key{Namespace: "other", Blueprint: "postgres", Kind: quota.KindUnits, Scope: "namespace"}

	if err := s.QuotaCounter().SetSettled(t.Context(), other, 7, fence); !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("SetSettled(a key of another namespace) = %v, want ErrFenced", err)
	}

	if u := quotaUsages(t, s, []quota.Check{{Key: other, Window: 0, Max: 0}}, quotaTime())[0]; u.Settled != 0 {
		t.Fatalf("Usage(%s) after the refusal = %+v, want no settled count", other, u)
	}
}
