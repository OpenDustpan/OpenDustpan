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
			if want.Kind == trigger.KindSchedule {
				row.NextSlotMs = 1 // Put sets the next slot of an enabled schedule trigger.
			}

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

// TestCheckStagedRefusesANextSlotThatTheScheduleCannotReach checks the rule of a
// staged row: an enabled schedule trigger whose next slot is not a slot of its
// schedule, or is after the first slot of the schedule after now plus the claim
// skew, would never be due, so the schedule would stop. A claim accepts a slot
// up to now plus MaxClaimSkew, so a backup from an instance whose clock is ahead
// inside the skew can hold a next slot up to that bound. A trigger that is off,
// or of another kind, has no slot to check.
func TestCheckStagedRefusesANextSlotThatTheScheduleCannotReach(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 30, 11, 59, 30, 0, time.UTC)
	first := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	in2100 := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)

	// The first slot after now plus the claim skew: a claim accepts a slot up
	// to that time, and the next slot of the claim is the first slot after it.
	afterSkew, err := trigger.NextSlot(hourly(), now.Add(trigger.MaxClaimSkew))
	if err != nil {
		t.Fatalf("next slot: %v", err)
	}

	if !afterSkew.After(first) {
		t.Fatalf("the first slot after now plus the claim skew = %v, want later than %v", afterSkew, first)
	}

	stepAfterSkew, err := trigger.NextSlot(hourly(), afterSkew)
	if err != nil {
		t.Fatalf("next slot: %v", err)
	}

	rowWithSlot := func(tr trigger.Trigger, slot time.Time) triggerstore.Row {
		row := triggerstore.RowOf(tr, now.UnixMilli())
		row.NextSlotMs = slot.UnixMilli()

		return row
	}

	off := hourly()
	off.Enabled = false

	cases := []struct {
		name string
		row  triggerstore.Row
		want error
	}{
		{"the first slot after now", rowWithSlot(hourly(), first), nil},
		{"the first slot after now plus the claim skew", rowWithSlot(hourly(), afterSkew), nil},
		{
			"one schedule step after the first slot after now plus the claim skew",
			rowWithSlot(hourly(), stepAfterSkew),
			triggerstore.ErrCorrupt,
		},
		{"a slot in 2100", rowWithSlot(hourly(), in2100), triggerstore.ErrCorrupt},
		{
			"a time that is not a slot of the schedule",
			rowWithSlot(hourly(), first.Add(-30*time.Minute)),
			triggerstore.ErrCorrupt,
		},
		{"a trigger that is off", triggerstore.RowOf(off, 1), nil},
		{"a follows trigger", triggerstore.RowOf(follows("f1", unitA(), unitC()), 1), nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := triggerstore.CheckStaged(tc.row, now)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("check = %v, want nil", err)
				}

				return
			}

			if !errors.Is(err, tc.want) || !errors.Is(err, trigger.ErrInvalid) {
				t.Fatalf("check = %v, want ErrCorrupt and ErrInvalid", err)
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
	moves   int
	// clock is the clock of the store. newMemTx sets a fixed time, and a test
	// that cares about the clock replaces it.
	clock time.Time
	// stale makes MoveNextSlot see a stored slot other than the one it is given.
	stale bool
}

func newMemTx(triggers ...trigger.Trigger) *memTx {
	m := &memTx{
		rows: map[string]triggerstore.Row{}, upserts: 0, removes: 0, moves: 0,
		clock: time.UnixMilli(1_700_000_000_000).UTC(), stale: false,
	}
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

func (m *memTx) Now(_ context.Context) (time.Time, error) {
	return m.clock, nil
}

func (m *memTx) MoveNextSlot(_ context.Context, _ namespace.Name, id trigger.ID, old, next int64) (bool, error) {
	r, ok := m.rows[string(id)]
	if !ok || m.stale || r.NextSlotMs != old {
		return false, nil
	}

	r.NextSlotMs = next
	m.rows[string(id)] = r
	m.moves++

	return true, nil
}

func hourly() trigger.Trigger {
	return schedule("s1", unitA(), trigger.Frequency{Preset: "", Cron: "0 * * * *"})
}

func claimAt(slot time.Time) trigger.Claim {
	return trigger.Claim{Namespace: "acme", Trigger: "s1", Slot: slot}
}

func TestApplyPutRecordsTheNextSlot(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 30, 10, 20, 0, 0, time.UTC)
	first := time.Date(2026, 10, 30, 11, 0, 0, 0, time.UTC)

	tx := newMemTx()
	tx.clock = at

	if err := triggerstore.ApplyPut(t.Context(), tx, hourly(), at.Add(time.Hour), nil); err != nil {
		t.Fatalf("put: %v", err)
	}

	// The slot comes from the clock of the store, and not from the time of the entry.
	if got := triggerstore.NextSlot(tx.rows["s1"]); !got.Equal(first) {
		t.Fatalf("next slot = %v, want %v", got, first)
	}

	// A claim moves it, and a replace with the same frequency keeps the move.
	tx.clock = first

	if _, err := triggerstore.ApplyClaim(t.Context(), tx, claimAt(first)); err != nil {
		t.Fatalf("claim: %v", err)
	}

	if err := triggerstore.ApplyPut(t.Context(), tx, hourly(), at.Add(time.Minute), nil); err != nil {
		t.Fatalf("replace: %v", err)
	}

	if got, want := triggerstore.NextSlot(tx.rows["s1"]), first.Add(time.Hour); !got.Equal(want) {
		t.Fatalf("next slot after a replace = %v, want %v", got, want)
	}

	off := hourly()
	off.Enabled = false

	if err := triggerstore.ApplyPut(t.Context(), tx, off, at, nil); err != nil {
		t.Fatalf("put off: %v", err)
	}

	if got := triggerstore.NextSlot(tx.rows["s1"]); !got.IsZero() {
		t.Fatalf("next slot of a trigger that is off = %v, want none", got)
	}
}

func claimSlot() time.Time { return time.Date(2026, 10, 30, 11, 0, 0, 0, time.UTC) }

func TestApplyClaimMovesTheSlotOnce(t *testing.T) {
	t.Parallel()

	slot := claimSlot()

	tx := newMemTx()
	tx.clock = slot.Add(-time.Minute)

	if err := triggerstore.ApplyPut(t.Context(), tx, hourly(), slot.Add(-time.Minute), nil); err != nil {
		t.Fatalf("put: %v", err)
	}

	tx.clock = slot

	unitID, err := triggerstore.ApplyClaim(t.Context(), tx, claimAt(slot))
	if err != nil || unitID != "a" || tx.moves != 1 {
		t.Fatalf("claim = %q, %v, moves %d", unitID, err, tx.moves)
	}

	_, err = triggerstore.ApplyClaim(t.Context(), tx, claimAt(slot))
	if !errors.Is(err, trigger.ErrNotDue) {
		t.Fatalf("second claim = %v, want ErrNotDue", err)
	}
}

func TestApplyClaimMoveThatChangesNoRow(t *testing.T) {
	t.Parallel()

	slot := claimSlot()

	tx := newMemTx()
	tx.clock = slot.Add(-time.Minute)

	if err := triggerstore.ApplyPut(t.Context(), tx, hourly(), slot.Add(-time.Minute), nil); err != nil {
		t.Fatalf("put: %v", err)
	}

	tx.clock = slot

	tx.stale = true

	_, err := triggerstore.ApplyClaim(t.Context(), tx, claimAt(slot))
	if !errors.Is(err, trigger.ErrNotDue) {
		t.Fatalf("claim = %v, want ErrNotDue", err)
	}
}

func TestApplyClaimUnknownTrigger(t *testing.T) {
	t.Parallel()

	_, err := triggerstore.ApplyClaim(t.Context(), newMemTx(), claimAt(claimSlot()))
	if !errors.Is(err, trigger.ErrNotFound) {
		t.Fatalf("claim = %v, want ErrNotFound", err)
	}
}

func TestApplyClaimRefusesARowThatFailsItsChecks(t *testing.T) {
	t.Parallel()

	bad := schedule("s1", unitA(), trigger.Frequency{Preset: "", Cron: "* * * * *"})
	negative := newMemTx(hourly())
	row := negative.rows["s1"]
	row.NextSlotMs = -5
	negative.rows["s1"] = row

	noSlot := newMemTx(hourly())
	row = noSlot.rows["s1"]
	row.NextSlotMs = 0
	noSlot.rows["s1"] = row

	tests := []struct {
		name string
		tx   *memTx
		want error
	}{
		{"a cron expression that is too frequent", newMemTx(bad), trigger.ErrInvalid},
		{"a negative slot", negative, triggerstore.ErrCorrupt},
		{"an enabled schedule trigger with no slot", noSlot, triggerstore.ErrCorrupt},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := triggerstore.ApplyClaim(t.Context(), tc.tx, claimAt(claimSlot()))
			if !errors.Is(err, triggerstore.ErrCorrupt) || !errors.Is(err, tc.want) || tc.tx.moves != 0 {
				t.Fatalf("claim = %v, moves %d, want ErrCorrupt and %v", err, tc.tx.moves, tc.want)
			}
		})
	}
}

func TestCheckClaim(t *testing.T) {
	t.Parallel()

	fence := namespace.Fence{Namespace: "acme", FailoverVersion: 1}
	otherFence := namespace.Fence{Namespace: "other", FailoverVersion: 1}
	bad := entryOf("acme")
	bad.Action = ""

	tests := []struct {
		name  string
		claim trigger.Claim
		fence namespace.Fence
		entry audit.Entry
		want  error
	}{
		{
			"empty claim",
			trigger.Claim{Namespace: "", Trigger: "", Slot: time.Time{}},
			fence,
			entryOf("acme"), trigger.ErrInvalid,
		},
		{"fence of another namespace", claimAt(claimSlot()), otherFence, entryOf("acme"), namespace.ErrFenced},
		{"entry of another namespace", claimAt(claimSlot()), fence, entryOf("other"), audit.ErrInvalidEntry},
		{"invalid entry", claimAt(claimSlot()), fence, bad, audit.ErrInvalidEntry},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if err := triggerstore.CheckClaim(tc.claim, tc.fence, tc.entry); !errors.Is(err, tc.want) {
				t.Errorf("CheckClaim = %v, want %v", err, tc.want)
			}
		})
	}

	if err := triggerstore.CheckClaim(claimAt(claimSlot()), fence, entryOf("acme")); err != nil {
		t.Errorf("a valid claim = %v, want nil", err)
	}
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

// TestApplyClaimRefusesASlotLaterThanTheClockOfTheStore proves the layer of the
// store: the claim reads the clock of the store, so a slot far in the future is
// refused and the next slot does not move.
func TestApplyClaimRefusesASlotLaterThanTheClockOfTheStore(t *testing.T) {
	t.Parallel()

	slot := claimSlot()

	tx := newMemTx()
	tx.clock = slot.Add(-time.Minute)

	if err := triggerstore.ApplyPut(t.Context(), tx, hourly(), slot.Add(-time.Minute), nil); err != nil {
		t.Fatalf("put: %v", err)
	}

	before := tx.rows["s1"].NextSlotMs
	far := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)

	_, err := triggerstore.ApplyClaim(t.Context(), tx, claimAt(far))
	if !errors.Is(err, trigger.ErrInvalid) || tx.moves != 0 || tx.rows["s1"].NextSlotMs != before {
		t.Fatalf("claim of a slot in 2100 = %v, moves %d, want ErrInvalid and no move", err, tx.moves)
	}
}
