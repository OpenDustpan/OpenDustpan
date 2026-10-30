package postgres

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// TestChangeRowOfLiveWriteHoldsTheCollection checks that the change row which a
// live write appends holds the name of the collection that holds the unit. The
// write is the delete of a workspace that sits in the collection payments. The
// row must name payments, and a row with an empty collection must fail the
// test.
func TestChangeRowOfLiveWriteHoldsTheCollection(t *testing.T) {
	t.Parallel()

	s, ns, _ := membershipSetup(t)
	membershipSoftDelete(t, s, ns)

	changes, err := s.ListChanges(t.Context(), ns.Name, time.UnixMilli(1<<50).UTC(), nil, 100)
	if err != nil {
		t.Fatalf("list changes: %v", err)
	}

	if len(changes) == 0 {
		t.Fatal("the live write appended no change row")
	}

	last := changes[len(changes)-1]
	if last.Kind != inventory.ChangeDeleted || last.Collection != "payments" {
		t.Errorf("last change = kind %q, collection %q; want kind %q, collection %q",
			last.Kind, last.Collection, inventory.ChangeDeleted, "payments")
	}
}

// TestAppendChangeOfMissingNamespaceIsNotFound checks the mapping from the
// empty answer of the one append statement to namespace.ErrNotFound. The test
// runs the append in a transaction without the fence check. The fence check
// answers first for a missing namespace, so it would hide the mapping.
func TestAppendChangeOfMissingNamespaceIsNotFound(t *testing.T) {
	t.Parallel()

	s, ns, id := membershipSetup(t)

	change := inventory.Change{
		At: time.UnixMilli(1_700_000_200_000).UTC(), Seq: 0, Namespace: "missing", Tool: "opentofu",
		UnitID: string(id), Name: "web", DisplayName: "web", Status: "", Collection: "",
		Kind: inventory.ChangeDeleted, ResourceCount: nil,
	}

	err := s.inTx(t.Context(), func(q *db.Queries) error {
		return appendInventoryChange(t.Context(), q, change)
	})
	if !errors.Is(err, namespace.ErrNotFound) {
		t.Errorf("append for a missing namespace = %v, want namespace.ErrNotFound", err)
	}

	change.Namespace = ns.Name

	err = s.inTx(t.Context(), func(q *db.Queries) error {
		return appendInventoryChange(t.Context(), q, change)
	})
	if err != nil {
		t.Errorf("append for an existing namespace = %v, want nil", err)
	}
}
