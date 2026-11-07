package selfservicetest

import (
	"context"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/selfservice"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// UnitTxHarness is one fresh, empty database for one case of RunUnitTx.
type UnitTxHarness interface {
	// CreateNamespace creates the namespace name and returns its fence.
	CreateNamespace(ctx context.Context, name namespace.Name) (namespace.Fence, error)
	// InTx runs fn with the selfservice.UnitTx of one new transaction, as the
	// store adapter makes it for a submit. It commits when fn returns nil,
	// and rolls back each write of the UnitTx when fn returns an error,
	// which InTx then returns.
	InTx(ctx context.Context, fn func(tx selfservice.UnitTx) error) error
	// Unit returns the unit of tool with the tool's own name in the
	// namespace ns, with its stable unit ID set, and false when none exists.
	Unit(ctx context.Context, ns namespace.Name, tool, name string) (identity.Object, bool, error)
}

// UnitTxFactory returns a fresh harness for one case of RunUnitTx.
type UnitTxFactory func(t *testing.T) UnitTxHarness

// RunUnitTx executes the suite of selfservice.UnitTx against harnesses from
// newHarness. Every implementation of the wiring runs it.
func RunUnitTx(t *testing.T, newHarness UnitTxFactory) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, h UnitTxHarness)
	}{
		{"a rollback keeps no unit", testUnitRollback},
		{"a second provision of a request returns the same unit", testUnitIdempotent},
		{"a name that another request owns is ErrUnitName", testUnitOwned},
		{"a provision with an empty field is ErrInvalid", testUnitInvalid},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			if _, err := h.CreateNamespace(t.Context(), ns); err != nil {
				t.Fatalf("CreateNamespace() = %v", err)
			}

			tc.run(t, h)
		})
	}
}

// errRollback makes InTx roll back.
var errRollback = errors.New("roll back")

func unitProvision(request selfservice.RequestID) selfservice.Provision {
	return selfservice.Provision{
		Request: request, Tool: "opentofu", Unit: "databases/orders-db", Ref: ref(),
		Blueprint: selfservice.BlueprintRef{Namespace: ns, Name: "postgres"}, Version: 1,
	}
}

func provisionIn(t *testing.T, h UnitTxHarness, p selfservice.Provision) (identity.Object, error) {
	t.Helper()

	var got identity.Object

	err := h.InTx(t.Context(), func(tx selfservice.UnitTx) error {
		u, err := tx.Provision(t.Context(), p)
		got = u

		return err
	})

	return got, err
}

func testUnitRollback(t *testing.T, h UnitTxHarness) {
	t.Helper()

	err := h.InTx(t.Context(), func(tx selfservice.UnitTx) error {
		if _, err := tx.Provision(t.Context(), unitProvision("req-1")); err != nil {
			t.Fatalf("Provision() = %v", err)
		}

		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("InTx() = %v, want the error of fn", err)
	}

	if _, ok, err := h.Unit(t.Context(), ns, "opentofu", "databases/orders-db"); err != nil || ok {
		t.Fatalf("Unit() after a rollback = %v, %v, want none", ok, err)
	}
}

func testUnitIdempotent(t *testing.T, h UnitTxHarness) {
	t.Helper()

	first, err := provisionIn(t, h, unitProvision("req-1"))
	if err != nil || first.UnitID() == "" {
		t.Fatalf("Provision() = %v, %v, want a unit with its unit ID", first, err)
	}

	second, err := provisionIn(t, h, unitProvision("req-1"))
	if err != nil || second.UnitID() != first.UnitID() {
		t.Fatalf("Provision(again) = %v, %v, want the unit %v", second, err, first)
	}

	u, ok, err := h.Unit(t.Context(), ns, "opentofu", "databases/orders-db")
	if err != nil || !ok || u.UnitID() != first.UnitID() {
		t.Fatalf("Unit() = %v, %v, %v, want the unit %v", u, ok, err, first)
	}
}

func testUnitOwned(t *testing.T, h UnitTxHarness) {
	t.Helper()

	if _, err := provisionIn(t, h, unitProvision("req-1")); err != nil {
		t.Fatalf("Provision() = %v", err)
	}

	if _, err := provisionIn(t, h, unitProvision("req-2")); !errors.Is(err, selfservice.ErrUnitName) {
		t.Fatalf("Provision(another request) = %v, want ErrUnitName", err)
	}
}

func testUnitInvalid(t *testing.T, h UnitTxHarness) {
	t.Helper()

	if _, err := provisionIn(t, h, unitProvision("")); !errors.Is(err, selfservice.ErrInvalid) {
		t.Fatalf("Provision(no request) = %v, want ErrInvalid", err)
	}

	if _, ok, err := h.Unit(t.Context(), ns, "opentofu", "databases/orders-db"); err != nil || ok {
		t.Fatalf("Unit() after ErrInvalid = %v, %v, want none", ok, err)
	}
}
