package storetest

import (
	"context"
	"testing"
	"time"
)

// NullEndedWitness is an optional capability for the cases that prove the
// stored shape of ended_at_ms: a row that is not ended stores NULL, and a row
// that is ended stores its time. The store reads the column with one raw
// query, not through the domain record, so a store that maps NULL and 0 to
// the zero time still shows the stored value. table is one of "backups",
// "restores" and "store_checks".
type NullEndedWitness interface {
	// StoredEndedAtMs returns the stored ended_at_ms of the row id in table.
	// valid is false when the column holds NULL.
	StoredEndedAtMs(ctx context.Context, table, id string) (ms int64, valid bool, err error)
}

// requireNotEndedStored fails unless the row table/id stores NULL in
// ended_at_ms.
func requireNotEndedStored(t *testing.T, w NullEndedWitness, table, id string) {
	t.Helper()

	ms, valid, err := w.StoredEndedAtMs(t.Context(), table, id)
	if err != nil {
		t.Fatalf("read ended_at_ms of %s %s: %v", table, id, err)
	}

	if valid {
		t.Fatalf("ended_at_ms of %s %s = %d, want NULL", table, id, ms)
	}
}

// requireEndedStored fails unless the row table/id stores want in ended_at_ms.
func requireEndedStored(t *testing.T, w NullEndedWitness, table, id string, want time.Time) {
	t.Helper()

	ms, valid, err := w.StoredEndedAtMs(t.Context(), table, id)
	if err != nil {
		t.Fatalf("read ended_at_ms of %s %s: %v", table, id, err)
	}

	if !valid {
		t.Fatalf("ended_at_ms of %s %s = NULL, want %d", table, id, want.UnixMilli())
	}

	if ms != want.UnixMilli() {
		t.Fatalf("ended_at_ms of %s %s = %d, want %d", table, id, ms, want.UnixMilli())
	}
}
