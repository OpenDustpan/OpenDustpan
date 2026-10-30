package sqlite

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trigger"
)

// TestClaimDueRowWithoutSlot proves that a schedule trigger which a build from
// before the column stored (next_slot_ms 0) accepts the first claim of a slot
// of its schedule, and then keeps the slot after it.
func TestClaimDueRowWithoutSlot(t *testing.T) {
	t.Parallel()

	s := openInternalTemplated(t)
	seedTriggers(t, s)

	if _, err := s.conn.ExecContext(t.Context(), `UPDATE triggers SET next_slot_ms = 0 WHERE id = 's1'`); err != nil {
		t.Fatalf("clear the slot: %v", err)
	}

	st := s.Triggers()

	got, err := st.NextSlot(t.Context(), "acme", "s1")
	if err != nil || !got.IsZero() {
		t.Fatalf("next slot = %v, %v, want none", got, err)
	}

	tr, err := st.Get(t.Context(), "acme", "s1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	now := time.UnixMilli(1_800_000_000_000).UTC()

	slot, ok, err := trigger.LatestSlot(tr, time.Time{}, now)
	if err != nil || !ok {
		t.Fatalf("latest slot = %v, %v, %v", slot, ok, err)
	}

	fence := namespace.Fence{Namespace: "acme", FailoverVersion: 1}
	c := trigger.Claim{Namespace: "acme", Trigger: "s1", Slot: slot, Now: now}

	if err := st.ClaimDue(t.Context(), c, fence, triggerAuditEntry()); err != nil {
		t.Fatalf("claim: %v", err)
	}

	if err := st.ClaimDue(t.Context(), c, fence, triggerAuditEntry()); !errors.Is(err, trigger.ErrNotDue) {
		t.Fatalf("second claim = %v, want ErrNotDue", err)
	}
}

// TestNextSlotColumnRefusesANegativeValue proves the layer of the table: a
// negative slot cannot be stored, whatever the code above does.
func TestNextSlotColumnRefusesANegativeValue(t *testing.T) {
	t.Parallel()

	s := openInternalTemplated(t)
	seedTriggers(t, s)

	if _, err := s.conn.ExecContext(t.Context(), `UPDATE triggers SET next_slot_ms = -1 WHERE id = 's1'`); err == nil {
		t.Fatal("a negative next slot was stored")
	}
}

// TestMoveNextSlotComparesTheOldValue proves the guard of the statement: a move
// from a value that is not the stored one changes no row.
func TestMoveNextSlotComparesTheOldValue(t *testing.T) {
	t.Parallel()

	s := openInternalTemplated(t)
	seedTriggers(t, s)

	slot, err := s.Triggers().NextSlot(t.Context(), "acme", "s1")
	if err != nil || slot.IsZero() {
		t.Fatalf("next slot = %v, %v", slot, err)
	}

	tx := triggerTx{q: s.queries}

	moved, err := tx.MoveNextSlot(t.Context(), "acme", "s1", slot.UnixMilli()+1, slot.UnixMilli()+2)
	if err != nil || moved {
		t.Fatalf("move from a wrong value = %v, %v, want false", moved, err)
	}

	moved, err = tx.MoveNextSlot(t.Context(), "acme", "s1", slot.UnixMilli(), slot.UnixMilli()+2)
	if err != nil || !moved {
		t.Fatalf("move from the stored value = %v, %v, want true", moved, err)
	}
}
