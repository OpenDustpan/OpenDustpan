package unitimporttest

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/unitimport"
)

func testPutUnits(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)

	second := newUnit("u-2")
	second.State, second.ErrorCode, second.Findings = unitimport.UnitSkipped, "import.target-exists", nil

	putUnits(t, h, fence, newUnit("u-1"), second)

	got := mustUnits(t, h)
	if len(got) != 2 || got[0].ID != "u-1" || got[1].ID != "u-2" {
		t.Fatalf("units = %+v, want u-1 and u-2 in the order given", got)
	}

	first, want := got[0], newUnit("u-1")
	same := sameNewUnit(first, want)
	if !same {
		t.Errorf("first unit = %+v, want the fields of %+v", first, want)
	}

	if got[1].State != unitimport.UnitSkipped || got[1].ErrorCode != "import.target-exists" {
		t.Errorf("second unit = %+v, want skipped with its code", got[1])
	}

	if one := mustUnit(t, h, "u-2"); one.ID != "u-2" {
		t.Errorf("unit = %+v, want u-2", one)
	}
}

func testReplaceUnits(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)

	putUnits(t, h, fence, newUnit("u-1"), newUnit("u-2"))
	putUnits(t, h, fence, newUnit("u-3"))

	if got := mustUnits(t, h); len(got) != 1 || got[0].ID != "u-3" {
		t.Fatalf("units after the replace = %+v, want u-3 only", got)
	}

	putUnits(t, h, fence)

	if got := mustUnits(t, h); len(got) != 0 {
		t.Fatalf("units after an empty put = %+v, want none", got)
	}
}

// changed returns the unit u after fn changed it.
func changed(u unitimport.ImportUnit, fn func(u *unitimport.ImportUnit)) unitimport.ImportUnit {
	fn(&u)

	return u
}

func testPutUnitsInvalid(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)

	cases := []struct {
		name  string
		units []unitimport.ImportUnit
	}{
		{"empty ID", []unitimport.ImportUnit{changed(newUnit("u-1"), func(u *unitimport.ImportUnit) { u.ID = "" })}},
		{
			"no source name",
			[]unitimport.ImportUnit{changed(newUnit("u-1"), func(u *unitimport.ImportUnit) { u.Source.Name = "" })},
		},
		{
			"no target name",
			[]unitimport.ImportUnit{changed(newUnit("u-1"), func(u *unitimport.ImportUnit) { u.TargetName = "" })},
		},
		{
			"another import",
			[]unitimport.ImportUnit{changed(newUnit("u-1"), func(u *unitimport.ImportUnit) { u.ImportID = "imp-2" })},
		},
		{
			"running state",
			[]unitimport.ImportUnit{
				changed(newUnit("u-1"), func(u *unitimport.ImportUnit) { u.State = unitimport.UnitRunning }),
			},
		},
		{
			"committed versions",
			[]unitimport.ImportUnit{changed(newUnit("u-1"), func(u *unitimport.ImportUnit) { u.Committed = 1 })},
		},
		{
			"negative bytes",
			[]unitimport.ImportUnit{changed(newUnit("u-1"), func(u *unitimport.ImportUnit) { u.Bytes = -1 })},
		},
		{"repeated ID", []unitimport.ImportUnit{newUnit("u-1"), newUnit("u-1")}},
		{"repeated target name", []unitimport.ImportUnit{
			newUnit("u-1"), changed(newUnit("u-2"), func(u *unitimport.ImportUnit) { u.TargetName = "T-U-1" }),
		}},
		{"too many units", manyUnits(unitimport.MaxUnits + 1)},
	}

	for _, tc := range cases {
		err := h.Units.PutUnits(t.Context(), fence, "imp-1", tc.units, entry("acme", "import:run"))
		mustIs(t, tc.name, err, unitimport.ErrInvalid)
	}

	putUnits(t, h, fence, manyUnits(unitimport.MaxUnits)...)
}

func manyUnits(n int) []unitimport.ImportUnit {
	out := make([]unitimport.ImportUnit, 0, n)
	for i := range n {
		out = append(out, newUnit("u-"+strconv.Itoa(i)))
	}

	return out
}

func testPutUnitsState(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)

	walk(t, h, fence, "imp-1", unitimport.StatePlanning, unitimport.StatePlanned)
	putUnits(t, h, fence, newUnit("u-1"))
	walk(t, h, fence, "imp-1", unitimport.StatePlanned, unitimport.StateRunning)

	err := h.Units.PutUnits(
		t.Context(),
		fence,
		"imp-1",
		[]unitimport.ImportUnit{newUnit("u-2")},
		entry("acme", "import:run"),
	)
	mustIs(t, "put units of a running import", err, unitimport.ErrTransition)

	if got := mustUnits(t, h); len(got) != 1 || got[0].ID != "u-1" {
		t.Fatalf("units after the refused put = %+v, want u-1 only", got)
	}
}

func testUnitTransitions(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	putUnits(t, h, fence, newUnit("u-1"))

	move := func(from, to unitimport.UnitState, code string) error {
		m := unitimport.UnitMove{From: from, To: to, Code: code}

		return h.Units.TransitionUnit(t.Context(), fence, unitKey("u-1"), m, entry("acme", "import:run"))
	}

	steps := []struct {
		from, to unitimport.UnitState
		code     string
	}{
		{unitimport.UnitPlanned, unitimport.UnitRunning, ""},
		{unitimport.UnitRunning, unitimport.UnitPaused, "import.hold-removed"},
		{unitimport.UnitPaused, unitimport.UnitRunning, ""},
		{unitimport.UnitRunning, unitimport.UnitFailed, "import.source-unreachable"},
		{unitimport.UnitFailed, unitimport.UnitRunning, ""},
		{unitimport.UnitRunning, unitimport.UnitImported, ""},
		{unitimport.UnitImported, unitimport.UnitRunning, ""},
		{unitimport.UnitRunning, unitimport.UnitImported, ""},
	}

	for _, step := range steps {
		if err := move(step.from, step.to, step.code); err != nil {
			t.Fatalf("unit %s to %s: %v", step.from, step.to, err)
		}

		if got := mustUnit(t, h, "u-1"); got.State != step.to || got.ErrorCode != step.code {
			t.Fatalf("unit = %s %q, want %s %q", got.State, got.ErrorCode, step.to, step.code)
		}
	}

	refused := []struct{ from, to unitimport.UnitState }{
		{unitimport.UnitImported, unitimport.UnitPlanned},
		{unitimport.UnitImported, unitimport.UnitFailed},
		{unitimport.UnitImported, "unknown"},
		{unitimport.UnitPlanned, unitimport.UnitRunning}, // The unit is not planned.
	}

	for _, tc := range refused {
		mustIs(t, fmt.Sprintf("unit %s to %s", tc.from, tc.to), move(tc.from, tc.to, ""), unitimport.ErrTransition)
	}

	// A running unit may end skipped, with the reason in the code.
	if err := move(unitimport.UnitImported, unitimport.UnitRunning, ""); err != nil {
		t.Fatalf("unit imported to running: %v", err)
	}

	if err := move(unitimport.UnitRunning, unitimport.UnitSkipped, "import.denied"); err != nil {
		t.Fatalf("unit running to skipped: %v", err)
	}

	if got := mustUnit(t, h, "u-1"); got.State != unitimport.UnitSkipped || got.ErrorCode != "import.denied" {
		t.Fatalf("unit = %s %q, want skipped %q", got.State, got.ErrorCode, "import.denied")
	}

	m := unitimport.UnitMove{From: unitimport.UnitPlanned, To: unitimport.UnitRunning, Code: ""}
	err := h.Units.TransitionUnit(t.Context(), fence, unitKey("missing"), m, entry("acme", "import:run"))
	mustIs(t, "unknown unit", err, unitimport.ErrNotFound)
}

func testUnitChangedHistory(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	putUnits(t, h, fence, newUnit("u-1"))

	move := func(from, to unitimport.UnitState, code string) error {
		m := unitimport.UnitMove{From: from, To: to, Code: code}

		return h.Units.TransitionUnit(t.Context(), fence, unitKey("u-1"), m, entry("acme", "import:run"))
	}

	if err := move(unitimport.UnitPlanned, unitimport.UnitRunning, ""); err != nil {
		t.Fatalf("start: %v", err)
	}

	if err := move(unitimport.UnitRunning, unitimport.UnitFailed, unitimport.CodeSourceHistoryChanged); err != nil {
		t.Fatalf("fail: %v", err)
	}

	mustIs(t, "resume after a changed source history",
		move(unitimport.UnitFailed, unitimport.UnitRunning, ""), unitimport.ErrTransition)

	got := mustUnit(t, h, "u-1")
	if got.State != unitimport.UnitFailed || got.ErrorCode != unitimport.CodeSourceHistoryChanged {
		t.Fatalf("unit = %s %q, want failed with its code", got.State, got.ErrorCode)
	}

	if err := move(unitimport.UnitFailed, unitimport.UnitCanceled, ""); err != nil {
		t.Fatalf("cancel: %v", err)
	}
}

func testEndedImport(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	putUnits(t, h, fence, newUnit("u-1"), newUnit("u-2"))
	walk(t, h, fence, "imp-1", unitimport.StatePlanning, unitimport.StatePlanned, unitimport.StateCanceled)

	e := entry("acme", "import:run")
	counts := unitimport.Counts{Versions: 5, Committed: 2}

	mustIs(
		t,
		"counts of an ended import",
		h.Units.SetCounts(t.Context(), fence, unitKey("u-1"), counts, e),
		unitimport.ErrTransition,
	)

	target := history.Unit{Namespace: "acme", Tool: "opentofu", ID: "ws-u-1"}
	mustIs(
		t,
		"target of an ended import",
		h.SetTarget(t.Context(), "acme", unitKey("u-1"), target),
		unitimport.ErrTransition,
	)

	run := unitimport.UnitMove{From: unitimport.UnitPlanned, To: unitimport.UnitRunning, Code: ""}
	mustIs(
		t,
		"start a unit of an ended import",
		h.Units.TransitionUnit(t.Context(), fence, unitKey("u-1"), run, e),
		unitimport.ErrTransition,
	)

	skip := unitimport.UnitMove{From: unitimport.UnitPlanned, To: unitimport.UnitSkipped, Code: "import.target-exists"}
	mustIs(
		t,
		"skip a unit of an ended import",
		h.Units.TransitionUnit(t.Context(), fence, unitKey("u-1"), skip, e),
		unitimport.ErrTransition,
	)

	cancel := unitimport.UnitMove{From: unitimport.UnitPlanned, To: unitimport.UnitCanceled, Code: ""}
	if err := h.Units.TransitionUnit(t.Context(), fence, unitKey("u-1"), cancel, e); err != nil {
		t.Fatalf("cancel a unit of an ended import: %v", err)
	}

	got := mustUnit(t, h, "u-1")
	if got.State != unitimport.UnitCanceled || got.Versions != 3 || got.Committed != 0 || got.Target.ID != "" {
		t.Fatalf("unit = %+v, want canceled with the old counts and no target", got)
	}

	if other := mustUnit(t, h, "u-2"); other.State != unitimport.UnitPlanned {
		t.Fatalf("other unit = %s, want planned", other.State)
	}
}

func testCounts(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	ctx := t.Context()
	e := entry("acme", "import:run")

	putUnits(t, h, fence, newUnit("u-1"))

	err := h.Units.SetCounts(
		ctx,
		fence,
		unitKey("u-1"),
		unitimport.Counts{Versions: 7, Committed: 4},
		e,
	)
	if err != nil {
		t.Fatalf("set counts: %v", err)
	}

	if got := mustUnit(t, h, "u-1"); got.Versions != 7 || got.Committed != 4 {
		t.Fatalf("counts = %d of %d, want 4 of 7", got.Committed, got.Versions)
	}

	for _, c := range []unitimport.Counts{{Versions: -1, Committed: 0}, {Versions: 3, Committed: -1}, {Versions: 3, Committed: 4}} {
		mustIs(
			t,
			fmt.Sprintf("counts %d of %d", c.Committed, c.Versions),
			h.Units.SetCounts(ctx, fence, unitKey("u-1"), c, e),
			unitimport.ErrInvalid,
		)
	}

	err = h.Units.SetCounts(ctx, fence, unitKey("missing"), unitimport.Counts{Versions: 1, Committed: 0}, e)
	mustIs(t, "counts of an unknown unit", err, unitimport.ErrNotFound)
}

func testLedger(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	ctx := t.Context()

	rows, err := h.Units.Ledger(ctx, "acme", "imp-1", "u-1")
	if err != nil || len(rows) != 0 {
		t.Fatalf("ledger before any row = %+v, %v, want none", rows, err)
	}

	putUnits(t, h, fence, newUnit("u-1"), newUnit("u-2"))

	// Seeded out of order: the ledger answers oldest first.
	seedLedger(t, h, ledgerRow("u-1", "v2", at().Add(2*time.Second)))
	seedLedger(t, h, ledgerRow("u-1", "v1", at().Add(time.Second)))
	seedLedger(t, h, ledgerRow("u-2", "w1", at()))

	rows, err = h.Units.Ledger(ctx, "acme", "imp-1", "u-1")
	if err != nil || len(rows) != 2 || rows[0].Version != "v1" || rows[1].Version != "v2" {
		t.Fatalf("ledger = %+v, %v, want v1 and v2", rows, err)
	}

	want := ledgerRow("u-1", "v1", at().Add(time.Second))
	same := sameRow(rows[0], want)
	if !same {
		t.Errorf("ledger row = %+v, want %+v", rows[0], want)
	}

	checkLedgerRepeats(t, h)

	if other, otherErr := h.Units.Ledger(ctx, "other", "imp-1", "u-1"); otherErr == nil && len(other) != 0 {
		t.Errorf("ledger in another namespace = %+v, want none", other)
	}
}

func testTarget(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	putUnits(t, h, fence, newUnit("u-1"))

	target := history.Unit{Namespace: "acme", Tool: "opentofu", ID: "ws-u-1"}
	if err := h.SetTarget(t.Context(), "acme", unitKey("u-1"), target); err != nil {
		t.Fatalf("set target: %v", err)
	}

	if got := mustUnit(t, h, "u-1"); got.Target != target {
		t.Fatalf("target of unit = %+v, want %+v", got.Target, target)
	}

	if all := mustUnits(t, h); len(all) != 1 || all[0].Target != target {
		t.Fatalf("target of units = %+v, want %+v", all, target)
	}
}

// sameNewUnit reports whether got holds the fields of the new unit want in the
// import imp-1.
func sameNewUnit(got, want unitimport.ImportUnit) bool {
	return got.ImportID == "imp-1" && got.Source == want.Source && got.TargetName == want.TargetName &&
		got.State == unitimport.UnitPlanned && got.Versions == want.Versions && got.Bytes == want.Bytes &&
		got.Committed == 0 && got.Encryption == want.Encryption && len(got.Findings) == 1 &&
		got.Findings[0] == want.Findings[0] && got.Target == (history.Unit{Namespace: "", Tool: "", ID: ""})
}

// sameRow reports whether two ledger rows hold the same values.
func sameRow(got, want unitimport.LedgerRow) bool {
	return got.ImportID == want.ImportID && got.ImportUnitID == want.ImportUnitID && got.Version == want.Version &&
		got.SourceSHA256 == want.SourceSHA256 && got.StoredSHA256 == want.StoredSHA256 && got.Item == want.Item &&
		got.CommittedAt.Equal(want.CommittedAt)
}

// checkLedgerRepeats checks the key of a ledger row, the answer for an unknown
// unit and the order of rows with one commit time. It runs after testLedger
// seeded v1 and v2 of u-1 and w1 of u-2.
func checkLedgerRepeats(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	mustIs(t, "a second ledger row of one version",
		h.SeedLedger(ctx, "acme", ledgerRow("u-1", "v1", at().Add(time.Hour))), unitimport.ErrLedgerExists)

	// The version is the key of a row, so a row of another unit or version is not a repeat.
	if err := h.SeedLedger(ctx, "acme", ledgerRow("u-2", "v1", at())); err != nil {
		t.Fatalf("seed the same version of another unit: %v", err)
	}

	rows, err := h.Units.Ledger(ctx, "acme", "imp-1", "u-1")
	if err != nil || len(rows) != 2 {
		t.Fatalf("ledger after the refused row = %+v, %v, want 2 rows", rows, err)
	}

	unknown, err := h.Units.Ledger(ctx, "acme", "imp-1", "u-unknown")
	if err != nil || len(unknown) != 0 {
		t.Fatalf("ledger of an unknown unit = %+v, %v, want an empty list", unknown, err)
	}

	// Rows of one commit time sort by version.
	seedLedger(t, h, ledgerRow("u-1", "v0", at().Add(2*time.Second)))

	rows, err = h.Units.Ledger(ctx, "acme", "imp-1", "u-1")
	if err != nil || len(rows) != 3 || rows[1].Version != "v0" || rows[2].Version != "v2" {
		t.Fatalf("ledger with a tie = %+v, %v, want v1, v0, v2", rows, err)
	}
}
