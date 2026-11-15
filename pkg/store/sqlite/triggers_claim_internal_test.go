package sqlite

import (
	"testing"
)

// TestSlotColumnRefusesAnEnabledScheduleTriggerWithoutOne proves the layer of the
// table: an enabled schedule trigger cannot lose its next slot, whatever the code
// above does, and a trigger that is off needs none.
func TestSlotColumnRefusesAnEnabledScheduleTriggerWithoutOne(t *testing.T) {
	t.Parallel()

	s := openInternalTemplated(t)
	seedTriggers(t, s)

	if _, err := s.conn.ExecContext(t.Context(), `UPDATE triggers SET next_slot_ms = 0 WHERE id = 's1'`); err == nil {
		t.Fatal("an enabled schedule trigger lost its next slot")
	}

	const off = `UPDATE triggers SET enabled = 0, next_slot_ms = 0 WHERE id = 's1'`

	if _, err := s.conn.ExecContext(t.Context(), off); err != nil {
		t.Fatalf("switch the trigger off: %v", err)
	}

	got, err := s.Triggers().NextSlot(t.Context(), "acme", "s1")
	if err != nil || !got.IsZero() {
		t.Fatalf("next slot of a trigger that is off = %v, %v, want none", got, err)
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
