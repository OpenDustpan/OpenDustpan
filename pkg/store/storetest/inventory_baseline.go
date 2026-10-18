package storetest

import (
	"context"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// BaselineSeed is the SQL that a store test runs on a database just before the
// migration of the inventory change log: the namespace acme, a live unit
// stk-live that changed at 1000 ms, and a soft-deleted unit ws-gone that a
// delete removed at 2000 ms. The statements hold no placeholder.
const BaselineSeed = `
	INSERT INTO inventory (namespace, tool, name, unit_id, display_name, last_changed_ms)
	VALUES ('acme', 'pulumi', 'web/prod', 'stk-live', 'web/prod', 1000);
	INSERT INTO inventory_deleted (namespace, tool, unit_id, name, deleted_at_ms, purge_after_ms)
	VALUES ('acme', 'opentofu', 'ws-gone', 'network', 2000, 9000)`

// BaselineStore is a store that CheckBaseline reads and appends to.
type BaselineStore interface {
	inventory.HistoryReader
	AppendChange(ctx context.Context, c inventory.Change, fence namespace.Fence) (inventory.Change, error)
}

// CheckBaseline checks a store that the migration of the inventory change log
// upgraded after the BaselineSeed: each unit has one baseline row, the view at
// a time after a baseline lists the unit, and the counter continues after the
// last baseline row.
func CheckBaseline(t *testing.T, s BaselineStore) {
	t.Helper()

	checkBaselineRows(t, s)
	checkBaselineViews(t, s)

	next, err := s.AppendChange(t.Context(), inventory.Change{
		At: time.UnixMilli(3000), Seq: 0, Namespace: "acme", Tool: "pulumi", UnitID: "stk-new", Name: "web/new",
		DisplayName: "web/new", Status: "", Collection: "", Kind: inventory.ChangeCreated, ResourceCount: nil,
	}, namespace.Fence{Namespace: "acme", FailoverVersion: 1})
	if err != nil || next.Seq != 3 {
		t.Fatalf("append after the baseline: seq %d, err %v, want seq 3", next.Seq, err)
	}
}

// checkBaselineRows checks the two baseline rows of the BaselineSeed.
func checkBaselineRows(t *testing.T, s BaselineStore) {
	t.Helper()

	rows, err := s.ListChanges(t.Context(), "acme", time.UnixMilli(1<<40), nil, 100)
	if err != nil {
		t.Fatalf("list changes: %v", err)
	}

	if len(rows) != 2 || rows[0].Seq != 1 || rows[0].UnitID != "stk-live" || rows[0].Kind != inventory.ChangeCreated ||
		!rows[0].At.Equal(time.UnixMilli(1000)) || rows[1].Seq != 2 || rows[1].UnitID != "ws-gone" ||
		rows[1].Kind != inventory.ChangeDeleted || !rows[1].At.Equal(time.UnixMilli(2000)) {
		t.Fatalf("baseline rows = %+v, want a created row at 1000 and a deleted row at 2000", rows)
	}
}

// checkBaselineViews checks the view after the baseline and before it.
func checkBaselineViews(t *testing.T, s BaselineStore) {
	t.Helper()

	view, err := s.ViewAt(t.Context(), "acme", time.UnixMilli(1500))
	if err != nil || len(view.Units) != 1 || view.Units[0].Entry.UnitID != "stk-live" ||
		view.Units[0].Entry.Name != "web/prod" || view.Units[0].Deleted() {
		t.Fatalf("view at 1500 = %+v, err %v, want the live unit", view, err)
	}

	view, err = s.ViewAt(t.Context(), "acme", time.UnixMilli(500))
	if err != nil || len(view.Units) != 0 {
		t.Fatalf("view at 500 = %+v, err %v, want no unit before the baseline", view, err)
	}
}
