package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/triggerstore"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/trigger"
	"opendustpan.org/opendustpan/pkg/trigger/triggertest"
)

// namespaceFences is trigger.Fences over the namespaces of a store.
type namespaceFences struct {
	s *sqlite.Store
}

// Fence implements trigger.Fences.
func (f namespaceFences) Fence(ctx context.Context, ns namespace.Name) (namespace.Fence, error) {
	found, err := f.s.GetNamespace(ctx, ns)
	if err != nil {
		return namespace.Fence{}, fmt.Errorf("fence of %s: %w", ns, err)
	}

	return found.Fence(), nil
}

// insertTriggerSQL plants a trigger row. It names the namespace by its name, as
// the queries of the store do.
const insertTriggerSQL = `INSERT INTO triggers (
    namespace_id, id, unit_tool, unit_id, kind, preset, cron, source_namespace, source_tool, source_unit_id, enabled,
    last_saver_subject, last_saver_id, created_at_ms, updated_at_ms, next_slot_ms
) SELECT n.id, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ? FROM namespaces AS n WHERE n.name = ?`

// setSlotSQL moves the next slot of a trigger, as the time since its save would.
const setSlotSQL = `UPDATE triggers SET next_slot_ms = ? WHERE id = ? AND namespace_id = (SELECT n.id FROM namespaces AS n WHERE n.name = ?)`

// setNextSlot moves the next slot of a trigger with raw SQL.
func setNextSlot(t *testing.T, s *sqlite.Store, ns namespace.Name, id trigger.ID, slot time.Time) {
	t.Helper()

	err := s.ExecForTest(t.Context(), setSlotSQL, slot.UnixMilli(), string(id), ns.String())
	if err != nil {
		t.Fatalf("set the next slot of %s: %v", id, err)
	}
}

// plantedSlot returns the next slot of a planted row: an enabled schedule trigger
// needs one, and the table refuses a row without it.
func plantedSlot(r triggerstore.Row) int64 {
	if r.Kind == string(trigger.KindSchedule) && r.Enabled == 1 {
		return 1
	}

	return 0
}

// plantTrigger writes tr with raw SQL. It runs no check of the rules of a
// trigger and no check for a loop.
func plantTrigger(t *testing.T, s *sqlite.Store, tr trigger.Trigger) {
	t.Helper()

	r := triggerstore.RowOf(tr, 1)

	err := s.ExecForTest(t.Context(), insertTriggerSQL, r.ID, r.UnitTool, r.UnitID, r.Kind, r.Preset, r.Cron,
		r.SourceNamespace, r.SourceTool, r.SourceUnitID, r.Enabled, r.LastSaverSubject, r.LastSaverID,
		r.CreatedAtMs, r.UpdatedAtMs, plantedSlot(r), r.Namespace)
	if err != nil {
		t.Fatalf("plant trigger %s: %v", tr.ID, err)
	}
}

func triggerHarness(t *testing.T, starter trigger.Starter) triggertest.Harness {
	t.Helper()

	s := open(t)
	doubles := triggertest.NewDoubles()

	dispatcher, err := doubles.Dispatcher(s.Triggers(), starter, namespaceFences{s: s})
	if err != nil {
		t.Fatalf("dispatcher: %v", err)
	}

	return triggertest.Harness{
		Store:      s.Triggers(),
		Dispatcher: dispatcher,
		Doubles:    doubles,
		Now: func(t *testing.T) time.Time {
			t.Helper()

			now, err := s.DatabaseNow(t.Context())
			if err != nil {
				t.Fatalf("read the clock of the store: %v", err)
			}

			return now
		},
		Namespace: func(t *testing.T, name string) namespace.Fence {
			t.Helper()

			return ensureNamespace(t, s, namespace.Name(name))
		},
		Audited: func(t *testing.T, ns string) int {
			t.Helper()

			return len(allAuditEntries(t, s, namespace.Name(ns)))
		},
		SetNextSlot: func(t *testing.T, ns namespace.Name, id trigger.ID, slot time.Time) {
			t.Helper()

			setNextSlot(t, s, ns, id, slot)
		},
		StoreRaw: func(t *testing.T, tr trigger.Trigger) {
			t.Helper()

			plantTrigger(t, s, tr)
		},
	}
}

func TestTriggerConformance(t *testing.T) {
	t.Parallel()

	triggertest.Run(t, triggerHarness)
}

// corruptTriggerRow is one change of a stored row, and the ID of the row.
type corruptTriggerRow struct {
	name   string
	update string
	args   []any
	id     trigger.ID
}

// corruptTriggerRows lists the changes that make a stored trigger fail the rules
// of a trigger. Each one changes the row of the trigger s1 (a schedule trigger
// with a preset) or f1 (a trigger that follows a).
func corruptTriggerRows() []corruptTriggerRow {
	return []corruptTriggerRow{
		{"unknown preset", `UPDATE triggers SET preset = 'yearly' WHERE id = 's1'`, nil, "s1"},
		{"preset and cron", `UPDATE triggers SET cron = '0 6 * * *' WHERE id = 's1'`, nil, "s1"},
		{"no frequency", `UPDATE triggers SET preset = '' WHERE id = 's1'`, nil, "s1"},
		{
			"cron with a newline",
			`UPDATE triggers SET preset = '', cron = ? WHERE id = 's1'`,
			[]any{"0 6 * * *\n"},
			"s1",
		},
		{"cron too fast", `UPDATE triggers SET preset = '', cron = '*/5 * * * *' WHERE id = 's1'`, nil, "s1"},
		{
			"source on a schedule trigger",
			`UPDATE triggers SET source_namespace = 'acme', source_tool = 'opentofu', source_unit_id = 'a' WHERE id = 's1'`,
			nil,
			"s1",
		},
		{"empty saver", `UPDATE triggers SET last_saver_subject = '' WHERE id = 's1'`, nil, "s1"},
		{"empty saver ID", `UPDATE triggers SET last_saver_id = '' WHERE id = 's1'`, nil, "s1"},
		{
			"saver is a service principal",
			`UPDATE triggers SET last_saver_subject = 'system:namespace-service:acme' WHERE id = 's1'`, nil, "s1",
		},
		{
			"saver with a newline",
			`UPDATE triggers SET last_saver_subject = ? WHERE id = 's1'`,
			[]any{"user:ada\nx"},
			"s1",
		},
		{"source in another namespace", `UPDATE triggers SET source_namespace = 'other' WHERE id = 'f1'`, nil, "f1"},
		{
			"no source",
			`UPDATE triggers SET source_namespace = '', source_tool = '', source_unit_id = '' WHERE id = 'f1'`,
			nil,
			"f1",
		},
		{
			"frequency on a follows trigger",
			`UPDATE triggers SET preset = 'daily' WHERE id = 'f1'`, nil, "f1",
		},
		{
			"source is the unit itself",
			`UPDATE triggers SET source_tool = 'opentofu', source_unit_id = 'b' WHERE id = 'f1'`, nil, "f1",
		},
	}
}

// TestTriggerCorruptRowIsNotServed plants each corrupt value in a stored row and
// checks that Get and List never serve the row and that a write in the
// namespace does not pass over it.
func TestTriggerCorruptRowIsNotServed(t *testing.T) {
	t.Parallel()

	for _, tc := range corruptTriggerRows() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkCorruptRow(t, tc)
		})
	}
}

// checkCorruptRow stores the triggers s1 and f1, changes one row with tc, and
// reads the rows back.
func checkCorruptRow(t *testing.T, tc corruptTriggerRow) {
	t.Helper()

	s := open(t)
	store := s.Triggers()
	fence := ensureNamespace(t, s, "acme")
	a := graph.Unit{Namespace: "acme", Tool: "opentofu", ID: "a"}
	b := graph.Unit{Namespace: "acme", Tool: "opentofu", ID: "b"}

	putTrigger(t, store, fence, triggerOf("s1", trigger.KindSchedule, b, a))
	putTrigger(t, store, fence, triggerOf("f1", trigger.KindFollows, b, a))

	if err := s.ExecForTest(t.Context(), tc.update, tc.args...); err != nil {
		t.Fatalf("change the row: %v", err)
	}

	_, err := store.Get(t.Context(), "acme", tc.id)
	if !errors.Is(err, triggerstore.ErrCorrupt) || !errors.Is(err, trigger.ErrInvalid) {
		t.Errorf("get = %v, want ErrCorrupt and ErrInvalid", err)
	}

	list, err := store.List(t.Context(), b)
	if !errors.Is(err, triggerstore.ErrCorrupt) || len(list) != 0 {
		t.Errorf("list = %v, %v; want ErrCorrupt and no trigger", list, err)
	}

	// The dispatcher refuses a follower row that does not validate, so the read
	// serves such a row only to be refused there.
	followers, err := store.Followers(t.Context(), a)
	if err != nil {
		t.Fatalf("followers: %v", err)
	}

	for i := range followers {
		if followers[i].ID == tc.id && followers[i].Validate() == nil {
			t.Errorf("followers served %s, which validates", tc.id)
		}
	}
}

// TestTriggerPutIgnoresACorruptFollowsRow checks that a save does not fail on a
// follows row that cannot be read, that the row takes no part in the loop check
// because it starts no plan, and that a delete removes the row.
func TestTriggerPutIgnoresACorruptFollowsRow(t *testing.T) {
	t.Parallel()

	s := open(t)
	store := s.Triggers()
	fence := ensureNamespace(t, s, "acme")
	a, b, c := graph.Unit{Namespace: "acme", Tool: "opentofu", ID: "a"},
		graph.Unit{Namespace: "acme", Tool: "opentofu", ID: "b"},
		graph.Unit{Namespace: "acme", Tool: "opentofu", ID: "c"}

	putTrigger(t, store, fence, triggerOf("f1", trigger.KindFollows, b, a))

	if err := s.ExecForTest(t.Context(), `UPDATE triggers SET last_saver_subject = '' WHERE id = 'f1'`); err != nil {
		t.Fatalf("change the row: %v", err)
	}

	// The row is no edge: c follows b, and b follows a only through the bad row.
	putTrigger(t, store, fence, triggerOf("f2", trigger.KindFollows, c, b))

	// Get and List still fail closed for the row, and Stored serves it.
	if _, err := store.Get(t.Context(), "acme", "f1"); !errors.Is(err, triggerstore.ErrCorrupt) {
		t.Fatalf("get = %v, want ErrCorrupt", err)
	}

	if _, err := store.Stored(t.Context(), "acme", "f1"); err != nil {
		t.Fatalf("stored: %v", err)
	}

	// A delete removes the bad row, so an operator can clean up.
	if err := store.Delete(t.Context(), "acme", "f1", fence, triggerEntry()); err != nil {
		t.Fatalf("delete of a corrupt row: %v", err)
	}

	if _, err := store.Stored(t.Context(), "acme", "f1"); !errors.Is(err, trigger.ErrNotFound) {
		t.Fatalf("stored after the delete = %v, want ErrNotFound", err)
	}
}

// TestTriggerRowsMatchTheMigration checks that the CHECK constraints refuse a
// kind and an enabled flag that the rules do not know.
func TestTriggerRowsMatchTheMigration(t *testing.T) {
	t.Parallel()

	s := open(t)
	store := s.Triggers()
	fence := ensureNamespace(t, s, "acme")

	putTrigger(t, store, fence, triggerOf("s1", trigger.KindSchedule,
		graph.Unit{Namespace: "acme", Tool: "opentofu", ID: "b"}, graph.Unit{Namespace: "", Tool: "", ID: ""}))

	for _, update := range []string{
		`UPDATE triggers SET kind = 'manual'`,
		`UPDATE triggers SET enabled = 2`,
		`UPDATE triggers SET enabled = -1`,
	} {
		if err := s.ExecForTest(t.Context(), update); err == nil {
			t.Errorf("%s: the database accepted it", update)
		}
	}
}
