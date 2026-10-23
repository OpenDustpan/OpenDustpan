package triggerstore_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/cron"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/triggerstore"
	"opendustpan.org/opendustpan/pkg/trigger"
)

func unitA() graph.Unit { return graph.Unit{Namespace: "acme", Tool: "opentofu", ID: "a"} }
func unitB() graph.Unit { return graph.Unit{Namespace: "acme", Tool: "pulumi", ID: "b"} }
func unitC() graph.Unit { return graph.Unit{Namespace: "acme", Tool: "opentofu", ID: "c"} }
func noOne() graph.Unit { return graph.Unit{Namespace: "", Tool: "", ID: ""} }

func follows(id trigger.ID, unit, source graph.Unit) trigger.Trigger {
	return trigger.Trigger{
		ID: id, Unit: unit, Kind: trigger.KindFollows, Frequency: trigger.Frequency{Preset: "", Cron: ""},
		Source: source, Enabled: true, LastSaver: "user:ada", LastSaverID: "prn-user:ada",
	}
}

func schedule(id trigger.ID, unit graph.Unit, f trigger.Frequency) trigger.Trigger {
	return trigger.Trigger{
		ID: id, Unit: unit, Kind: trigger.KindSchedule, Frequency: f, Source: noOne(), Enabled: true,
		LastSaver: "user:ada", LastSaverID: "prn-user:ada",
	}
}

func review(id trigger.ID, unit graph.Unit) trigger.Trigger {
	return trigger.Trigger{
		ID: id, Unit: unit, Kind: trigger.KindReviewRequestPush, Frequency: trigger.Frequency{Preset: "", Cron: ""},
		Source: noOne(), Enabled: false, LastSaver: "user:ada", LastSaverID: "prn-user:ada",
	}
}

func TestRowRoundTrip(t *testing.T) {
	t.Parallel()

	for _, want := range []trigger.Trigger{
		follows("f1", unitA(), unitC()),
		schedule("s1", unitB(), trigger.Frequency{Preset: cron.PresetWeekly, Cron: ""}),
		schedule("s2", unitB(), trigger.Frequency{Preset: "", Cron: "30 4 * * 1-5"}),
		review("r1", unitA()),
	} {
		t.Run(string(want.ID), func(t *testing.T) {
			t.Parallel()

			row := triggerstore.RowOf(want, 42)
			if row.CreatedAtMs != 42 || row.UpdatedAtMs != 42 || row.Namespace != "acme" {
				t.Errorf("row = %+v, want times 42 and namespace acme", row)
			}

			got, err := triggerstore.Trigger(row)
			if err != nil || got != want {
				t.Fatalf("trigger = %+v, %v; want %+v", got, err, want)
			}
		})
	}
}

func TestRowOfWritesEmptyColumnsForAMissingSourceAndFrequency(t *testing.T) {
	t.Parallel()

	row := triggerstore.RowOf(review("r1", unitA()), 1)
	if row.SourceNamespace != "" || row.SourceTool != "" || row.SourceUnitID != "" || row.Preset != "" ||
		row.Cron != "" ||
		row.Enabled != 0 {
		t.Errorf("row = %+v, want empty source, empty frequency and the flag 0", row)
	}

	if on := triggerstore.RowOf(follows("f1", unitA(), unitC()), 1); on.Enabled != 1 || on.SourceNamespace != "acme" {
		t.Errorf("row = %+v, want the flag 1 and the source namespace acme", on)
	}
}

// TestTriggerRefusesACorruptRow checks each shape of a stored row that the rules
// of a trigger refuse. The error wraps ErrCorrupt and ErrInvalid.
func TestTriggerRefusesACorruptRow(t *testing.T) {
	t.Parallel()

	good := triggerstore.RowOf(schedule("s1", unitB(), trigger.Frequency{Preset: cron.PresetDaily, Cron: ""}), 1)
	goodFollows := triggerstore.RowOf(follows("f1", unitA(), unitC()), 1)

	cases := []struct {
		name   string
		change func(r *triggerstore.Row)
		base   triggerstore.Row
	}{
		{"bad kind", func(r *triggerstore.Row) { r.Kind = "manual" }, good},
		{"empty kind", func(r *triggerstore.Row) { r.Kind = "" }, good},
		{"preset and cron", func(r *triggerstore.Row) { r.Cron = "0 6 * * *" }, good},
		{"unknown preset", func(r *triggerstore.Row) { r.Preset = "yearly" }, good},
		{"no frequency", func(r *triggerstore.Row) { r.Preset = "" }, good},
		{"cron with a newline", func(r *triggerstore.Row) { r.Preset, r.Cron = "", "0 6 * * *\n" }, good},
		{"cron too fast", func(r *triggerstore.Row) { r.Preset, r.Cron = "", "*/10 * * * *" }, good},
		{"source on a schedule trigger", func(r *triggerstore.Row) {
			r.SourceNamespace, r.SourceTool, r.SourceUnitID = "acme", "opentofu", "a"
		}, good},
		{"empty saver", func(r *triggerstore.Row) { r.LastSaverSubject = "" }, good},
		{
			"saver too long",
			func(r *triggerstore.Row) { r.LastSaverSubject = strings.Repeat("s", trigger.MaxSaverBytes+1) },
			good,
		},
		{"saver with a newline", func(r *triggerstore.Row) { r.LastSaverSubject = "user:ada\nroot" }, good},
		{
			"saver with a bidirectional mark",
			func(r *triggerstore.Row) { r.LastSaverSubject = "user:ada" + string(rune(0x202e)) },
			good,
		},
		{"saver that is not UTF-8", func(r *triggerstore.Row) { r.LastSaverSubject = "user:\xff" }, good},
		{"saver is a service principal", func(r *triggerstore.Row) {
			r.LastSaverSubject = "system:namespace-service:acme"
		}, good},
		{"bad trigger ID", func(r *triggerstore.Row) { r.ID = "No Way" }, good},
		{"empty unit tool", func(r *triggerstore.Row) { r.UnitTool = "" }, good},
		{"bad namespace", func(r *triggerstore.Row) { r.Namespace = "bad ns" }, good},
		{"enabled flag 2", func(r *triggerstore.Row) { r.Enabled = 2 }, good},
		{"enabled flag -1", func(r *triggerstore.Row) { r.Enabled = -1 }, good},
		{"follows with no source", func(r *triggerstore.Row) {
			r.SourceNamespace, r.SourceTool, r.SourceUnitID = "", "", ""
		}, goodFollows},
		{"source in another namespace", func(r *triggerstore.Row) { r.SourceNamespace = "other" }, goodFollows},
		{"follows with a frequency", func(r *triggerstore.Row) { r.Preset = "daily" }, goodFollows},
		{"follows itself", func(r *triggerstore.Row) {
			r.SourceTool, r.SourceUnitID = r.UnitTool, r.UnitID
		}, goodFollows},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			row := tc.base
			tc.change(&row)

			got, err := triggerstore.Trigger(row)
			if !errors.Is(err, triggerstore.ErrCorrupt) || !errors.Is(err, trigger.ErrInvalid) {
				t.Fatalf("trigger = %+v, %v; want ErrCorrupt and ErrInvalid", got, err)
			}

			if got.ID != "" || got.LastSaver != "" {
				t.Errorf("a corrupt row returned the trigger %+v", got)
			}
		})
	}
}

func TestTriggersFailsOnTheFirstCorruptRow(t *testing.T) {
	t.Parallel()

	bad := triggerstore.RowOf(review("r2", unitA()), 1)
	bad.Kind = "manual"

	rows := []triggerstore.Row{
		triggerstore.RowOf(review("r1", unitA()), 1),
		bad,
		triggerstore.RowOf(review("r3", unitA()), 1),
	}

	got, err := triggerstore.Triggers(rows)
	if !errors.Is(err, triggerstore.ErrCorrupt) || got != nil {
		t.Fatalf("triggers = %v, %v; want ErrCorrupt and no trigger", got, err)
	}

	good, err := triggerstore.Triggers([]triggerstore.Row{rows[0], rows[2]})
	if err != nil || len(good) != 2 || good[0].ID != "r1" || good[1].ID != "r3" {
		t.Fatalf("triggers = %v, %v; want r1 and r3", good, err)
	}
}

func TestOfUnitRefusesATriggerOfAnotherUnit(t *testing.T) {
	t.Parallel()

	rows := []triggerstore.Row{
		triggerstore.RowOf(review("r1", unitA()), 1), triggerstore.RowOf(review("r2", unitB()), 1),
	}

	got, err := triggerstore.OfUnit(rows, unitA())
	if !errors.Is(err, triggerstore.ErrCorrupt) || got != nil {
		t.Fatalf("of unit = %v, %v; want ErrCorrupt and no trigger", got, err)
	}

	got, err = triggerstore.OfUnit(rows[:1], unitA())
	if err != nil || len(got) != 1 {
		t.Fatalf("of unit = %v, %v; want one trigger", got, err)
	}
}

func TestFollowersOf(t *testing.T) {
	t.Parallel()

	t.Run("serves the followers of the source", func(t *testing.T) {
		t.Parallel()

		rows := []triggerstore.Row{
			triggerstore.RowOf(
				follows("f1", unitA(), unitC()),
				1,
			),
			triggerstore.RowOf(follows("f2", unitB(), unitC()), 1),
		}

		got, err := triggerstore.FollowersOf(rows, unitC())
		if err != nil || len(got) != 2 {
			t.Fatalf("followers = %v, %v; want two", got, err)
		}
	})

	t.Run("serves a row that does not validate so that the dispatcher refuses it", func(t *testing.T) {
		t.Parallel()

		row := triggerstore.RowOf(follows("f1", unitA(), unitC()), 1)
		row.Preset = "daily"

		got, err := triggerstore.FollowersOf([]triggerstore.Row{row}, unitC())
		if err != nil || len(got) != 1 {
			t.Fatalf("followers = %v, %v; want the row", got, err)
		}

		if got[0].Validate() == nil {
			t.Error("the trigger validates, want a value that the dispatcher refuses")
		}
	})

	t.Run("refuses a row that cannot follow the source", func(t *testing.T) {
		t.Parallel()

		off := follows("f1", unitA(), unitC())
		off.Enabled = false

		for name, row := range map[string]triggerstore.Row{
			"another source":        triggerstore.RowOf(follows("f1", unitA(), unitB()), 1),
			"a trigger that is off": triggerstore.RowOf(off, 1),
			"another kind":          triggerstore.RowOf(review("r1", unitA()), 1),
			"the flag 2": func() triggerstore.Row {
				r := triggerstore.RowOf(follows("f1", unitA(), unitC()), 1)
				r.Enabled = 2

				return r
			}(),
		} {
			got, err := triggerstore.FollowersOf([]triggerstore.Row{row}, unitC())
			if !errors.Is(err, triggerstore.ErrCorrupt) || got != nil {
				t.Errorf("%s: followers = %v, %v; want ErrCorrupt", name, got, err)
			}
		}
	})
}

func entryOf(ns namespace.Name) audit.Entry {
	return audit.Entry{
		At: time.UnixMilli(1), Namespace: ns, Subject: "user:ada", IDPSubject: "", TokenID: "", Action: "trigger:put",
		Object: "opentofu:acme/a", Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1",
		UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess,
		RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "", JobID: "",
		Reason: "",
	}
}

func TestCheckPut(t *testing.T) {
	t.Parallel()

	fence := namespace.Fence{Namespace: "acme", FailoverVersion: 1}
	tr := follows("f1", unitA(), unitC())

	noSubject := entryOf("acme")
	noSubject.Subject = ""

	cases := []struct {
		name  string
		tr    trigger.Trigger
		fence namespace.Fence
		entry audit.Entry
		want  error
	}{
		{"valid", tr, fence, entryOf("acme"), nil},
		{"invalid trigger", follows("", unitA(), unitC()), fence, entryOf("acme"), trigger.ErrInvalid},
		{"self follow", follows("f1", unitA(), unitA()), fence, entryOf("acme"), trigger.ErrCycle},
		{
			"fence of another namespace",
			tr,
			namespace.Fence{Namespace: "other", FailoverVersion: 1},
			entryOf("acme"),
			namespace.ErrFenced,
		},
		{"entry of another namespace", tr, fence, entryOf("other"), audit.ErrInvalidEntry},
		{"entry with no subject", tr, fence, noSubject, audit.ErrInvalidEntry},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := triggerstore.CheckPut(tc.tr, tc.fence, tc.entry)
			if tc.want == nil && err != nil || tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("check = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestCheckDeleteAndGet(t *testing.T) {
	t.Parallel()

	fence := namespace.Fence{Namespace: "acme", FailoverVersion: 1}

	for name, tc := range map[string]struct {
		ns    namespace.Name
		id    trigger.ID
		fence namespace.Fence
		entry audit.Entry
		want  error
	}{
		"valid":                      {"acme", "f1", fence, entryOf("acme"), nil},
		"bad ID":                     {"acme", "No Way", fence, entryOf("acme"), trigger.ErrInvalid},
		"empty ID":                   {"acme", "", fence, entryOf("acme"), trigger.ErrInvalid},
		"bad namespace":              {"bad ns", "f1", fence, entryOf("acme"), trigger.ErrInvalid},
		"namespace with a newline":   {"acme\n", "f1", fence, entryOf("acme"), trigger.ErrInvalid},
		"fence of another namespace": {"acme", "f1", namespace.Fence{Namespace: "other", FailoverVersion: 1}, entryOf("acme"), namespace.ErrFenced},
		"entry of another namespace": {"acme", "f1", fence, entryOf("other"), audit.ErrInvalidEntry},
	} {
		err := triggerstore.CheckDelete(tc.ns, tc.id, tc.fence, tc.entry)
		if tc.want == nil && err != nil || tc.want != nil && !errors.Is(err, tc.want) {
			t.Errorf("delete %s: %v, want %v", name, err, tc.want)
		}
	}

	if err := triggerstore.CheckGet("acme", "f1"); err != nil {
		t.Errorf("get: %v", err)
	}

	for _, ns := range []namespace.Name{"", "..", "bad ns"} {
		if err := triggerstore.CheckGet(ns, "f1"); !errors.Is(err, trigger.ErrInvalid) {
			t.Errorf("get in %q: %v, want ErrInvalid", ns, err)
		}
	}

	if err := triggerstore.CheckGet("acme", ""); !errors.Is(err, trigger.ErrInvalid) {
		t.Errorf("get of an empty ID: %v, want ErrInvalid", err)
	}

	if err := triggerstore.CheckUnit(noOne()); !errors.Is(err, trigger.ErrInvalid) {
		t.Errorf("unit check of the zero unit: %v, want ErrInvalid", err)
	}

	if err := triggerstore.CheckUnit(unitA()); err != nil {
		t.Errorf("unit check: %v", err)
	}
}

// memTx is a triggerstore.Tx over a map. It counts the calls, so a test can
// see which step ran.
type memTx struct {
	rows    map[string]triggerstore.Row
	upserts int
	removes int
}

func newMemTx(triggers ...trigger.Trigger) *memTx {
	m := &memTx{rows: map[string]triggerstore.Row{}, upserts: 0, removes: 0}
	for i := range triggers {
		m.rows[string(triggers[i].ID)] = triggerstore.RowOf(triggers[i], 1)
	}

	return m
}

func (m *memTx) Find(_ context.Context, _ namespace.Name, id trigger.ID) (triggerstore.Row, bool, error) {
	r, ok := m.rows[string(id)]

	return r, ok, nil
}

func (m *memTx) EnabledFollows(_ context.Context, _ namespace.Name) ([]triggerstore.Row, error) {
	var out []triggerstore.Row

	for id := range m.rows {
		if r := m.rows[id]; r.Kind == string(trigger.KindFollows) && r.Enabled == 1 {
			out = append(out, r)
		}
	}

	return out, nil
}

func (m *memTx) Upsert(_ context.Context, r triggerstore.Row) error {
	m.rows[r.ID] = r
	m.upserts++

	return nil
}

func (m *memTx) Remove(_ context.Context, _ namespace.Name, id trigger.ID) (bool, error) {
	_, ok := m.rows[string(id)]
	delete(m.rows, string(id))
	m.removes++

	return ok, nil
}

func TestApplyPut(t *testing.T) {
	t.Parallel()

	at := time.UnixMilli(5_000)

	t.Run("writes a new trigger", func(t *testing.T) {
		t.Parallel()

		tx := newMemTx()
		if err := triggerstore.ApplyPut(t.Context(), tx, follows("f1", unitA(), unitC()), at, nil); err != nil {
			t.Fatalf("put: %v", err)
		}

		if row := tx.rows["f1"]; tx.upserts != 1 || row.CreatedAtMs != 5_000 || row.UpdatedAtMs != 5_000 {
			t.Errorf("rows = %+v after %d upserts, want one write stamped 5000", tx.rows, tx.upserts)
		}
	})

	t.Run("refuses a loop and writes nothing", func(t *testing.T) {
		t.Parallel()

		tx := newMemTx(follows("f1", unitA(), unitC()), follows("f2", unitB(), unitA()))

		err := triggerstore.ApplyPut(t.Context(), tx, follows("f3", unitC(), unitB()), at, nil)
		if !errors.Is(err, trigger.ErrCycle) || tx.upserts != 0 {
			t.Fatalf("put = %v after %d upserts, want ErrCycle and no write", err, tx.upserts)
		}
	})

	t.Run("a replace checks the loop without the old edge", func(t *testing.T) {
		t.Parallel()

		tx := newMemTx(follows("f1", unitA(), unitC()), follows("f2", unitC(), unitB()))

		if err := triggerstore.ApplyPut(t.Context(), tx, follows("f1", unitA(), unitB()), at, nil); err != nil {
			t.Fatalf("put: %v", err)
		}
	})
}

func TestApplyPutBesideARowThatFailsItsChecks(t *testing.T) {
	t.Parallel()

	at := time.UnixMilli(5_000)

	t.Run("skips a follows row that does not validate and reports it", func(t *testing.T) {
		t.Parallel()

		tx := newMemTx(follows("f1", unitA(), unitC()))
		bad := tx.rows["f1"]
		bad.LastSaverSubject = ""
		tx.rows["f1"] = bad

		var skipped []string

		// f2 would close a loop with f1 if f1 counted. f1 starts no plan, so it
		// is no edge.
		err := triggerstore.ApplyPut(t.Context(), tx, follows("f2", unitC(), unitA()), at, func(r triggerstore.Row) {
			skipped = append(skipped, r.ID)
		})
		if err != nil || tx.upserts != 1 {
			t.Fatalf("put = %v after %d upserts, want a write", err, tx.upserts)
		}

		if len(skipped) != 1 || skipped[0] != "f1" {
			t.Errorf("skipped = %v, want [f1]", skipped)
		}
	})

	t.Run("a valid row still closes a loop beside a row that does not validate", func(t *testing.T) {
		t.Parallel()

		tx := newMemTx(
			follows("f1", unitA(), unitC()),
			follows("f2", unitB(), unitA()),
			follows("f3", unitC(), unitB()),
		)
		bad := tx.rows["f3"]
		bad.LastSaverID = ""
		tx.rows["f3"] = bad

		// f1 and f2 are valid: A follows C, B follows A. C follows B closes the loop.
		err := triggerstore.ApplyPut(t.Context(), tx, follows("f4", unitC(), unitB()), at, nil)
		if !errors.Is(err, trigger.ErrCycle) || tx.upserts != 0 {
			t.Fatalf("put = %v after %d upserts, want ErrCycle and no write", err, tx.upserts)
		}
	})
}

func TestUsable(t *testing.T) {
	t.Parallel()

	good := triggerstore.RowOf(follows("f1", unitA(), unitC()), 1)
	bad := triggerstore.RowOf(follows("f2", unitB(), unitA()), 1)
	bad.LastSaverID = ""

	var skipped []string

	got := triggerstore.Usable(
		[]triggerstore.Row{bad, good},
		func(r triggerstore.Row) { skipped = append(skipped, r.ID) },
	)
	if len(got) != 1 || got[0].ID != "f1" || len(skipped) != 1 || skipped[0] != "f2" {
		t.Errorf("usable = %v, skipped = %v; want [f1] and [f2]", got, skipped)
	}

	// A nil callback is allowed.
	if got := triggerstore.Usable([]triggerstore.Row{bad}, nil); len(got) != 0 {
		t.Errorf("usable = %v, want none", got)
	}
}

func TestStoredServesARowThatFailsItsChecks(t *testing.T) {
	t.Parallel()

	row := triggerstore.RowOf(follows("f1", unitA(), unitC()), 1)
	row.LastSaverSubject, row.LastSaverID, row.Enabled = "", "", 7

	if _, err := triggerstore.Trigger(row); !errors.Is(err, triggerstore.ErrCorrupt) {
		t.Fatalf("trigger = %v, want ErrCorrupt", err)
	}

	got := triggerstore.Stored(row)
	if got.ID != "f1" || got.Unit != unitA() || got.Kind != trigger.KindFollows || got.Enabled {
		t.Errorf("stored = %+v, want the row with its unit and kind, and off", got)
	}
}

func TestApplyPutKeepsTheUnitAndTheKind(t *testing.T) {
	t.Parallel()

	daily := trigger.Frequency{Preset: cron.PresetDaily, Cron: ""}
	old := schedule("t1", unitA(), daily)

	for name, tr := range map[string]trigger.Trigger{
		"unit":    schedule("t1", unitB(), daily),
		"unit ID": schedule("t1", unitC(), daily),
		"kind":    review("t1", unitA()),
		"follows": follows("t1", unitA(), unitC()),
	} {
		tx := newMemTx(old)

		err := triggerstore.ApplyPut(t.Context(), tx, tr, time.UnixMilli(5_000), nil)
		if !errors.Is(err, trigger.ErrInvalid) || tx.upserts != 0 {
			t.Errorf("%s: put = %v after %d upserts, want ErrInvalid and no write", name, err, tx.upserts)
		}
	}
}

func TestApplyDelete(t *testing.T) {
	t.Parallel()

	t.Run("removes the trigger and returns the unit ID", func(t *testing.T) {
		t.Parallel()

		tx := newMemTx(follows("f1", unitA(), unitC()))

		unitID, err := triggerstore.ApplyDelete(t.Context(), tx, "acme", "f1")
		if err != nil || unitID != "a" || tx.removes != 1 {
			t.Fatalf("delete = %q, %v after %d removes, want a", unitID, err, tx.removes)
		}
	})

	t.Run("an unknown trigger is not found", func(t *testing.T) {
		t.Parallel()

		tx := newMemTx()

		if _, err := triggerstore.ApplyDelete(t.Context(), tx, "acme", "f1"); !errors.Is(err, trigger.ErrNotFound) {
			t.Fatalf("delete = %v, want ErrNotFound", err)
		}
	})

	t.Run("removes a row that does not validate", func(t *testing.T) {
		t.Parallel()

		tx := newMemTx(follows("f1", unitA(), unitC()))
		bad := tx.rows["f1"]
		bad.Kind = "manual"
		tx.rows["f1"] = bad

		if _, err := triggerstore.ApplyDelete(t.Context(), tx, "acme", "f1"); err != nil || len(tx.rows) != 0 {
			t.Fatalf("delete = %v with %d rows left, want a clean-up of the row", err, len(tx.rows))
		}
	})
}
