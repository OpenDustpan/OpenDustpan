package storetest

import (
	"errors"
	"maps"
	"testing"

	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/stack"
)

// testGetUnitFound checks that GetUnit returns the entry of a live unit by
// its ID alone, with its labels, and that the entry equals the list entry.
func testGetUnitFound(t *testing.T, s Store) {
	t.Helper()

	want := map[string]string{"app": "web", "team": "payments"}
	ns, st := createTagged(t, s, want)

	got, err := s.GetUnit(t.Context(), st.ID.String())
	if err != nil {
		t.Fatalf("get unit %s: %v", st.ID, err)
	}

	if got.UnitID != st.ID.String() || got.Namespace != ns.Name || got.Tool != "pulumi" {
		t.Fatalf("unit = %+v, want unit %s of %q", got, st.ID, ns.Name)
	}

	if !maps.Equal(got.Labels, want) {
		t.Fatalf("labels = %v, want %v", got.Labels, want)
	}

	listed := listInventory(t, s, ns.Name)
	if len(listed) != 1 || listed[0].Name != got.Name || !listed[0].LastChanged.Equal(got.LastChanged) {
		t.Fatalf("list = %+v, get = %+v: want the same unit", listed, got)
	}
}

// testGetUnitNoLabels checks that a unit with no label reads an empty map,
// never nil.
func testGetUnitNoLabels(t *testing.T, s Store) {
	t.Helper()

	_, st := createTagged(t, s, nil)

	got, err := s.GetUnit(t.Context(), st.ID.String())
	if err != nil {
		t.Fatalf("get unit %s: %v", st.ID, err)
	}

	if got.Labels == nil || len(got.Labels) != 0 {
		t.Fatalf("labels = %#v, want an empty map", got.Labels)
	}
}

// testGetUnitUnknown checks that an unknown ID is ErrUnitNotFound.
func testGetUnitUnknown(t *testing.T, s Store) {
	t.Helper()

	_, err := s.GetUnit(t.Context(), "stk-unknown")
	if !errors.Is(err, inventory.ErrUnitNotFound) {
		t.Fatalf("get unknown unit error = %v, want ErrUnitNotFound", err)
	}
}

// testGetUnitDeleted checks that a soft-deleted unit is ErrUnitNotFound, and
// that a restore brings it back under the same ID.
func testGetUnitDeleted(t *testing.T, s Store) {
	t.Helper()

	ns, st := createTagged(t, s, map[string]string{"app": "web"})

	err := s.DeleteStack(t.Context(), st.Ref, ns.Fence(), stack.EmptyGuard(), inventory.Deletion{
		Entry: deleteEntry(ns.Name), PurgeAfter: purgeAt(),
	})
	if err != nil {
		t.Fatalf("delete stack: %v", err)
	}

	_, err = s.GetUnit(t.Context(), st.ID.String())
	if !errors.Is(err, inventory.ErrUnitNotFound) {
		t.Fatalf("get deleted unit error = %v, want ErrUnitNotFound", err)
	}

	restore := lifecycleEntry(ns.Name, "unit:restore")
	if _, err = s.RestoreStack(t.Context(), st.ID, "back", ns.Fence(), restore); err != nil {
		t.Fatalf("restore stack: %v", err)
	}

	got, err := s.GetUnit(t.Context(), st.ID.String())
	if err != nil {
		t.Fatalf("get restored unit: %v", err)
	}

	if got.Name != "web/back" {
		t.Fatalf("restored unit name = %q, want web/back", got.Name)
	}
}

// testGetUnitFollowsRename checks that a rename keeps the ID and shows the new
// name.
func testGetUnitFollowsRename(t *testing.T, s Store) {
	t.Helper()

	ns, st := createTagged(t, s, nil)

	if err := s.RenameStack(t.Context(), st.Ref, "prod2", ns.Fence(), renameEntry(ns.Name, st.Ref)); err != nil {
		t.Fatalf("rename stack: %v", err)
	}

	got, err := s.GetUnit(t.Context(), st.ID.String())
	if err != nil {
		t.Fatalf("get renamed unit: %v", err)
	}

	if got.Name != "web/prod2" {
		t.Fatalf("name = %q, want web/prod2", got.Name)
	}
}
