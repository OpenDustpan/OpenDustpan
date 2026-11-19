package storetest

import (
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// benchLookupUnits is the number of units in the namespace of BenchUnitLookup.
const benchLookupUnits = 20_000

// BenchUnitLookup seeds one namespace with 20,000 stacks that have one label
// each. It then measures the two ways to find one unit by its ID:
// "list and scan" reads every unit of the namespace and scans the result,
// "get by ID" reads the one unit. A caller that knows the unit ID uses the
// second read.
func BenchUnitLookup(b *testing.B, s Store) {
	b.Helper()

	ns, wantID := seedLookupUnits(b, s)

	b.Run("list and scan", func(b *testing.B) { benchListAndScan(b, s, ns, wantID) })
	b.Run("get by ID", func(b *testing.B) { benchGetByID(b, s, wantID) })
}

// benchListAndScan measures a read of every unit of ns and a scan for unitID.
func benchListAndScan(b *testing.B, s Store, ns namespace.Name, unitID string) {
	b.Helper()
	b.ReportAllocs()

	for b.Loop() {
		entries, err := s.ListUnits(b.Context(), ns)
		if err != nil {
			b.Fatalf("list units: %v", err)
		}

		if !containsUnit(entries, unitID) {
			b.Fatalf("unit %q is not in the list", unitID)
		}
	}
}

// benchGetByID measures the read of the one unit with the ID unitID.
func benchGetByID(b *testing.B, s Store, unitID string) {
	b.Helper()
	b.ReportAllocs()

	for b.Loop() {
		e, err := s.GetUnit(b.Context(), unitID)
		if err != nil {
			b.Fatalf("get unit: %v", err)
		}

		if e.UnitID != unitID {
			b.Fatalf("unit ID = %q, want %q", e.UnitID, unitID)
		}
	}
}

// seedLookupUnits writes the namespace and its stacks. It returns the
// namespace name and the ID of the last stack, so that a scan reads every
// entry.
func seedLookupUnits(b *testing.B, s Store) (namespace.Name, string) {
	b.Helper()

	ctx := b.Context()

	ns := namespace.Namespace{
		CreatedAt:           now(),
		ID:                  namespace.NewID(),
		Name:                "bench",
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.CreateNamespace(ctx, ns); err != nil {
		b.Fatalf("create namespace: %v", err)
	}

	var last string

	for i := range benchLookupUnits {
		st := newStack(ns.Name, "app", fmt.Sprintf("s%05d", i))
		if err := s.CreateStack(ctx, st, ns.Fence()); err != nil {
			b.Fatalf("seed stack %d: %v", i, err)
		}

		last = st.ID.String()
	}

	return ns.Name, last
}

// containsUnit reports whether entries holds the unit with the ID unitID.
func containsUnit(entries []inventory.Entry, unitID string) bool {
	for i := range entries {
		if entries[i].UnitID == unitID {
			return true
		}
	}

	return false
}
