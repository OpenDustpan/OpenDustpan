package unitimporttest

import (
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/unitimport"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// RunStore runs every case of the store suite.
func RunStore(t *testing.T, newHarness Factory) {
	t.Helper()

	cases := []struct {
		name string
		test func(t *testing.T, h Harness)
	}{
		{"create and get an import", testCreateGet},
		{"create refuses a bad import", testCreateInvalid},
		{"create refuses a repeated ID", testCreateRepeated},
		{"a create race stores one import", testCreateRace},
		{"an unknown import", testNotFound},
		{"two namespaces hold two sets of imports", testNamespaceIsolation},
		{"list by state", testList},
		{"the transition table", testTransitions},
		{"a transition sets the reason and the end time", testTransitionFields},
		{"the plan of an import", testSetPlan},
		{"a plan of a running import changes the reservations only", testSetPlanRunning},
		{"purge keeps the ID and the ledger", testPurge},
		{"put and read the import units", testPutUnits},
		{"put replaces the import units", testReplaceUnits},
		{"put refuses bad units", testPutUnitsInvalid},
		{"put refuses an import that moves", testPutUnitsState},
		{"the transitions of an import unit", testUnitTransitions},
		{"a unit with a changed source history does not resume", testUnitChangedHistory},
		{"an ended import takes no change of its units", testEndedImport},
		{"counts of an import unit", testCounts},
		{"the ledger of an import unit", testLedger},
		{"the target of an import unit", testTarget},
		{"a stale fence refuses each write", testFenced},
		{"each write appends one audit entry", testAudit},
		{"an entry of another namespace is refused", testAuditNamespace},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newHarness(t))
		})
	}
}

func testCreateGet(t *testing.T, h Harness) {
	t.Helper()
	setup(t, h)

	want := newImport("acme", "imp-1")
	got := mustGet(t, h, "imp-1")

	same := sameImport(got, want)
	if !same {
		t.Errorf("get = %+v, want the fields of %+v", got, want)
	}

	list, err := h.Store.List(t.Context(), "acme", unitimport.Filter{States: nil})
	if err != nil || len(list) != 1 || list[0].ID != "imp-1" {
		t.Errorf("list = %+v, %v, want the one import", list, err)
	}
}

func testCreateInvalid(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")

	cases := []struct {
		name   string
		change func(i *unitimport.Import)
		want   error
	}{
		{"empty ID", func(i *unitimport.Import) { i.ID = "" }, unitimport.ErrInvalid},
		{"unknown kind", func(i *unitimport.Import) { i.Kind = "ftp" }, unitimport.ErrInvalid},
		{"unknown credential way", func(i *unitimport.Import) { i.Credential = "token" }, unitimport.ErrInvalid},
		{"no creator", func(i *unitimport.Import) { i.CreatedBy = "" }, unitimport.ErrInvalid},
		{"no creation time", func(i *unitimport.Import) { i.CreatedAt = time.Time{} }, unitimport.ErrInvalid},
		{"profile way with no profile", func(i *unitimport.Import) { i.Profile = "" }, unitimport.ErrInvalid},
		{"negative history limit", func(i *unitimport.Import) { i.Options.HistoryLimit = -1 }, unitimport.ErrInvalid},
		{"other namespace", func(i *unitimport.Import) { i.Namespace = "other" }, unitimport.ErrInvalid},
		{
			"address with userinfo",
			func(i *unitimport.Import) { i.Address = "https://u:p@host/x" },
			unitimport.ErrAddressInvalid,
		},
		{
			"address with a query",
			func(i *unitimport.Import) { i.Address = "https://host/x?token=1" },
			unitimport.ErrAddressInvalid,
		},
		{
			"address with another scheme",
			func(i *unitimport.Import) { i.Address = "ftp://host/x" },
			unitimport.ErrAddressInvalid,
		},
		{"profile path with another way", func(i *unitimport.Import) {
			i.Credential, i.Profile = unitimport.CredentialEphemeral, ""
		}, unitimport.ErrInvalid},
		{"upload with a profile path", func(i *unitimport.Import) {
			i.Kind, i.Address, i.Credential, i.Profile = unitimport.SourceUpload, "", unitimport.CredentialNone, ""
		}, unitimport.ErrInvalid},
		{"upload with an address", func(i *unitimport.Import) {
			i.Kind, i.Credential, i.Profile, i.ProfilePath = unitimport.SourceUpload, unitimport.CredentialNone, "", ""
		}, unitimport.ErrInvalid},
	}

	for _, tc := range cases {
		imp := newImport("acme", "imp-bad")
		tc.change(&imp)

		_, err := h.Store.Create(t.Context(), fence, imp, entry("acme", "import:run"))
		mustIs(t, tc.name, err, tc.want)
	}

	list, err := h.Store.List(t.Context(), "acme", unitimport.Filter{States: nil})
	if err != nil || len(list) != 0 {
		t.Fatalf("list after refused creates = %v, %v, want none", list, err)
	}

	_, err = h.Store.Create(
		t.Context(),
		fence,
		newUploadImport("acme", "imp-up"),
		entry("acme", "import:run"),
	)
	if err != nil {
		t.Fatalf("create upload import: %v", err)
	}
}

func testCreateRepeated(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)

	_, err := h.Store.Create(t.Context(), fence, newImport("acme", "imp-1"), entry("acme", "import:run"))
	mustIs(t, "create with a repeated ID", err, unitimport.ErrInvalid)

	if got := len(h.Audited(t, "acme")); got != 1 {
		t.Errorf("audit entries after a refused create = %d, want 1", got)
	}
}

func testCreateRace(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	results := make(chan error, raceCreates)

	var wg sync.WaitGroup

	for range raceCreates {
		wg.Go(func() {
			_, err := h.Store.Create(t.Context(), fence, newImport("acme", "imp-race"), entry("acme", "import:run"))
			results <- err
		})
	}

	wg.Wait()
	close(results)

	created := 0

	for err := range results {
		switch {
		case err == nil:
			created++
		case !errors.Is(err, unitimport.ErrInvalid):
			t.Errorf("create in a race: error = %v, want nil or ErrInvalid", err)
		default:
		}
	}

	if created != 1 {
		t.Errorf("creates that succeeded = %d, want 1", created)
	}

	list, err := h.Store.List(t.Context(), "acme", unitimport.Filter{States: nil})
	if err != nil || len(list) != 1 {
		t.Errorf("list after the race = %d imports, %v, want 1", len(list), err)
	}
}

// raceCreates is the count of concurrent creates of one import ID.
const raceCreates = 8

func testNotFound(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	ctx := t.Context()
	e := entry("acme", "import:run")

	_, err := h.Store.Get(ctx, "acme", "missing")
	mustIs(t, "get", err, unitimport.ErrNotFound)

	_, err = h.Store.Get(ctx, "missing-ns", "imp-1")
	mustIs(t, "get in an unknown namespace", err, unitimport.ErrNotFound)

	move := unitimport.Move{From: unitimport.StatePlanning, To: unitimport.StatePlanned, Reason: ""}
	mustIs(t, "transition", h.Store.Transition(ctx, fence, "missing", move, e), unitimport.ErrNotFound)
	mustIs(t, "set plan", h.Store.SetPlan(ctx, fence, "missing", emptyPlan(0), e), unitimport.ErrNotFound)
	mustIs(t, "purge", h.Store.Purge(ctx, fence, "missing", e), unitimport.ErrNotFound)
	mustIs(t, "put units", h.Units.PutUnits(ctx, fence, "missing", nil, e), unitimport.ErrNotFound)

	_, err = h.Units.Units(ctx, "acme", "missing")
	mustIs(t, "units", err, unitimport.ErrNotFound)

	_, err = h.Units.Unit(ctx, "acme", "imp-1", "missing")
	mustIs(t, "unit", err, unitimport.ErrNotFound)

	_, err = h.Units.Ledger(ctx, "acme", "missing", "u-1")
	mustIs(t, "ledger of an unknown import", err, unitimport.ErrNotFound)
}

func testNamespaceIsolation(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	other := h.Namespace(t, "other")

	_, err := h.Store.Create(
		t.Context(),
		other,
		newImport("other", "imp-1"),
		entry("other", "import:run"),
	)
	if err != nil {
		t.Fatalf("create the same ID in another namespace: %v", err)
	}

	putUnits(t, h, fence, newUnit("u-1"))

	got, err := h.Units.Units(t.Context(), "other", "imp-1")
	if err != nil || len(got) != 0 {
		t.Fatalf("units of the other namespace = %v, %v, want none", got, err)
	}

	_, err = h.Units.Unit(t.Context(), "other", "imp-1", "u-1")
	mustIs(t, "unit of another namespace", err, unitimport.ErrNotFound)

	for _, ns := range []namespace.Name{"acme", "other"} {
		list, listErr := h.Store.List(t.Context(), ns, unitimport.Filter{States: nil})
		if listErr != nil || len(list) != 1 {
			t.Errorf("list %s = %d imports, %v, want 1", ns, len(list), listErr)
		}
	}
}

func testList(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)

	for n := 2; n <= 3; n++ {
		imp := newImport("acme", "imp-"+strconv.Itoa(n))
		imp.CreatedAt = at().Add(time.Duration(n) * time.Minute)

		if _, err := h.Store.Create(t.Context(), fence, imp, entry("acme", "import:run")); err != nil {
			t.Fatalf("create %s: %v", imp.ID, err)
		}
	}

	walk(t, h, fence, "imp-2", unitimport.StatePlanning, unitimport.StatePlanned)

	cases := []struct {
		name   string
		states []unitimport.State
		want   []string
	}{
		{"all, oldest first", nil, []string{"imp-1", "imp-2", "imp-3"}},
		{"planning", []unitimport.State{unitimport.StatePlanning}, []string{"imp-1", "imp-3"}},
		{
			"planned or planning",
			[]unitimport.State{unitimport.StatePlanned, unitimport.StatePlanning},
			[]string{"imp-1", "imp-2", "imp-3"},
		},
		{"released", []unitimport.State{unitimport.StateReleased}, []string{}},
	}

	for _, tc := range cases {
		list, err := h.Store.List(t.Context(), "acme", unitimport.Filter{States: tc.states})
		if err != nil {
			t.Fatalf("list %s: %v", tc.name, err)
		}

		got := make([]string, 0, len(list))
		for n := range list {
			got = append(got, list[n].ID)
		}

		if !slices.Equal(got, tc.want) {
			t.Errorf("list %s = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func testTransitions(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	e := entry("acme", "import:run")

	// The path of a normal import, with a second dry run, a pause and a resume.
	walk(t, h, fence, "imp-1",
		unitimport.StatePlanning, unitimport.StatePaused, unitimport.StatePlanning,
		unitimport.StatePlanned, unitimport.StatePlanning, unitimport.StatePlanned,
		unitimport.StateRunning, unitimport.StatePaused, unitimport.StateRunning, unitimport.StateHolding,
		unitimport.StateRunning, unitimport.StateHolding, unitimport.StateReleased)

	for _, to := range []unitimport.State{unitimport.StateRunning, unitimport.StateCanceled, unitimport.StatePlanning} {
		err := h.Store.Transition(
			t.Context(),
			fence,
			"imp-1",
			unitimport.Move{From: unitimport.StateReleased, To: to, Reason: ""},
			e,
		)
		mustIs(t, "released to "+string(to), err, unitimport.ErrTransition)
	}

	checkPausedWithLedger(t, h, fence)

	if _, err := h.Store.Create(t.Context(), fence, newImport("acme", "imp-2"), e); err != nil {
		t.Fatalf("create: %v", err)
	}

	for _, to := range []unitimport.State{unitimport.StateRunning, unitimport.StateHolding, unitimport.StateReleased, "unknown"} {
		err := h.Store.Transition(
			t.Context(),
			fence,
			"imp-2",
			unitimport.Move{From: unitimport.StatePlanning, To: to, Reason: ""},
			e,
		)
		mustIs(t, "planning to "+string(to), err, unitimport.ErrTransition)
	}

	// The from state must be the stored state.
	err := h.Store.Transition(
		t.Context(),
		fence,
		"imp-2",
		unitimport.Move{From: unitimport.StatePlanned, To: unitimport.StateRunning, Reason: ""},
		e,
	)
	mustIs(t, "from a state that the import is not in", err, unitimport.ErrTransition)

	if got := mustGet(t, h, "imp-2"); got.State != unitimport.StatePlanning {
		t.Fatalf("state after refused transitions = %v, want planning", got.State)
	}
}

func testTransitionFields(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	later := at().Add(time.Hour)

	walk(t, h, fence, "imp-1", unitimport.StatePlanning, unitimport.StatePlanned, unitimport.StateRunning)

	pause := unitimport.Move{From: unitimport.StateRunning, To: unitimport.StatePaused, Reason: "fenced"}
	if err := h.Store.Transition(t.Context(), fence, "imp-1", pause, entry("acme", "import:run")); err != nil {
		t.Fatalf("pause: %v", err)
	}

	got := mustGet(t, h, "imp-1")
	if got.State != unitimport.StatePaused || got.Reason != "fenced" || !got.EndedAt.IsZero() {
		t.Fatalf("paused import = %+v, want reason fenced and no end time", got)
	}

	cancel := unitimport.Move{From: unitimport.StatePaused, To: unitimport.StateCanceled, Reason: ""}
	err := h.Store.Transition(
		t.Context(),
		fence,
		"imp-1",
		cancel,
		entryAt("acme", "import:cancel", later),
	)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}

	got = mustGet(t, h, "imp-1")
	if got.State != unitimport.StateCanceled || got.Reason != "" || !got.EndedAt.Equal(later) {
		t.Fatalf("canceled import = %+v, want no reason and the end time %v", got, later)
	}
}

func testSetPlan(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)

	plan := fullPlan()
	if err := h.Store.SetPlan(t.Context(), fence, "imp-1", plan, entry("acme", "import:run")); err != nil {
		t.Fatalf("set plan: %v", err)
	}

	got := mustGet(t, h, "imp-1").Plan
	same := got.Units == plan.Units && got.Versions == plan.Versions && got.Bytes == plan.Bytes &&
		got.At.Equal(plan.At) && slices.Equal(got.Capabilities, plan.Capabilities) &&
		slices.Equal(got.Requests, plan.Requests) && slices.Equal(got.Reservations, plan.Reservations) &&
		slices.Equal(got.Findings, plan.Findings)
	if !same {
		t.Fatalf("plan = %+v, want %+v", got, plan)
	}
}

// fullPlan returns a plan with every field set.
func fullPlan() unitimport.Plan {
	return unitimport.Plan{
		At:           at(),
		Units:        2,
		Versions:     9,
		Bytes:        4096,
		Capabilities: []unitimport.Capability{unitimport.CapabilityDecrypt},
		Findings:     []unitimport.Finding{{Code: "a", Severity: unitimport.SeverityError, Message: "m", Help: "h"}},
		Requests:     []string{"sample decrypt of acme/web"},
		Reservations: []string{"res-1"},
	}
}

func testSetPlanRunning(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	e := entry("acme", "import:run")

	if err := h.Store.SetPlan(t.Context(), fence, "imp-1", fullPlan(), e); err != nil {
		t.Fatalf("set plan: %v", err)
	}

	walk(t, h, fence, "imp-1", unitimport.StatePlanning, unitimport.StatePlanned, unitimport.StateRunning)

	other := fullPlan()
	other.Units, other.Bytes, other.Reservations = 99, 1, []string{"res-1", "res-2"}

	if err := h.Store.SetPlan(t.Context(), fence, "imp-1", other, e); err != nil {
		t.Fatalf("set plan of a running import: %v", err)
	}

	got := mustGet(t, h, "imp-1").Plan
	if got.Units != 2 || got.Bytes != 4096 || !slices.Equal(got.Reservations, []string{"res-1", "res-2"}) {
		t.Fatalf("plan of a running import = %+v, want the old plan with the new reservations", got)
	}
}

func testPurge(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	ctx := t.Context()
	e := entry("acme", "import:run")

	putUnits(t, h, fence, newUnit("u-1"))
	seedLedger(t, h, ledgerRow("u-1", "v1", at()))

	mustIs(t, "purge of an import that did not end", h.Store.Purge(ctx, fence, "imp-1", e), unitimport.ErrTransition)

	walk(t, h, fence, "imp-1", unitimport.StatePlanning, unitimport.StateCanceled)

	if err := h.Store.Purge(ctx, fence, "imp-1", e); err != nil {
		t.Fatalf("purge: %v", err)
	}

	_, err := h.Store.Get(ctx, "acme", "imp-1")
	mustIs(t, "get after the purge", err, unitimport.ErrPurged)

	_, err = h.Units.Units(ctx, "acme", "imp-1")
	mustIs(t, "units after the purge", err, unitimport.ErrPurged)

	_, err = h.Units.Unit(ctx, "acme", "imp-1", "u-1")
	mustIs(t, "unit after the purge", err, unitimport.ErrPurged)

	mustIs(t, "second purge", h.Store.Purge(ctx, fence, "imp-1", e), unitimport.ErrPurged)

	_, err = h.Store.Create(ctx, fence, newImport("acme", "imp-1"), e)
	mustIs(t, "create with a purged ID", err, unitimport.ErrInvalid)

	list, err := h.Store.List(ctx, "acme", unitimport.Filter{States: nil})
	if err != nil || len(list) != 0 {
		t.Fatalf("list after the purge = %v, %v, want none", list, err)
	}

	rows, err := h.Units.Ledger(ctx, "acme", "imp-1", "u-1")
	if err != nil || len(rows) != 1 || rows[0].Version != "v1" {
		t.Fatalf("ledger after the purge = %+v, %v, want the one row", rows, err)
	}
}

// writes lists one call of each write method with the fence, so a case can
// run them all. Each call names the import imp-1 and the unit u-1, except
// the call of create.
func writes(h Harness, fence namespace.Fence, e audit.Entry) map[string]func(t *testing.T) error {
	return map[string]func(t *testing.T) error{
		"create": func(t *testing.T) error {
			t.Helper()

			_, err := h.Store.Create(t.Context(), fence, newImport("acme", "imp-new"), e)

			return err
		},
		"transition": func(t *testing.T) error {
			t.Helper()

			m := unitimport.Move{From: unitimport.StatePlanning, To: unitimport.StatePlanned, Reason: ""}

			return h.Store.Transition(t.Context(), fence, "imp-1", m, e)
		},
		"set plan": func(t *testing.T) error {
			t.Helper()

			return h.Store.SetPlan(t.Context(), fence, "imp-1", emptyPlan(1), e)
		},
		"purge": func(t *testing.T) error {
			t.Helper()

			return h.Store.Purge(t.Context(), fence, "imp-1", e)
		},
		"put units": func(t *testing.T) error {
			t.Helper()

			return h.Units.PutUnits(t.Context(), fence, "imp-1", []unitimport.ImportUnit{newUnit("u-9")}, e)
		},
		"transition unit": func(t *testing.T) error {
			t.Helper()

			m := unitimport.UnitMove{From: unitimport.UnitPlanned, To: unitimport.UnitRunning, Code: ""}

			return h.Units.TransitionUnit(t.Context(), fence, unitKey("u-1"), m, e)
		},
		"set counts": func(t *testing.T) error {
			t.Helper()

			return h.Units.SetCounts(
				t.Context(),
				fence,
				unitKey("u-1"),
				unitimport.Counts{Versions: 3, Committed: 1},
				e,
			)
		},
	}
}

func testFenced(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	putUnits(t, h, fence, newUnit("u-1"))

	before := len(h.Audited(t, "acme"))
	stale := namespace.Fence{Namespace: "acme", FailoverVersion: fence.FailoverVersion + 1}

	for name, write := range writes(h, stale, entry("acme", "import:run")) {
		mustIs(t, name+" with a stale fence", write(t), namespace.ErrFenced)
	}

	if got := len(h.Audited(t, "acme")); got != before {
		t.Errorf("audit entries after the refused writes = %d, want %d", got, before)
	}

	imp, units := mustGet(t, h, "imp-1"), mustUnits(t, h)
	if imp.State != unitimport.StatePlanning || imp.Plan.Units != 0 || len(units) != 1 || units[0].ID != "u-1" ||
		units[0].State != unitimport.UnitPlanned || units[0].Committed != 0 {
		t.Errorf("import = %+v, units = %+v, want nothing changed", imp, units)
	}

	if _, err := h.Store.Get(t.Context(), "acme", "imp-new"); !errors.Is(err, unitimport.ErrNotFound) {
		t.Errorf("get the import of the refused create: %v, want not found", err)
	}
}

func testAudit(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	ctx := t.Context()
	ready := unitimport.Move{From: unitimport.StatePlanning, To: unitimport.StatePlanned, Reason: ""}
	run := unitimport.Move{From: unitimport.StatePlanned, To: unitimport.StateRunning, Reason: ""}
	cancel := unitimport.Move{From: unitimport.StateRunning, To: unitimport.StateCanceled, Reason: ""}
	start := unitimport.UnitMove{From: unitimport.UnitPlanned, To: unitimport.UnitRunning, Code: ""}

	steps := []struct {
		action string
		do     func(e audit.Entry) error
	}{
		{"import:plan", func(e audit.Entry) error {
			return h.Units.PutUnits(ctx, fence, "imp-1", []unitimport.ImportUnit{newUnit("u-1")}, e)
		}},
		{"import:set-plan", func(e audit.Entry) error { return h.Store.SetPlan(ctx, fence, "imp-1", emptyPlan(1), e) }},
		{"import:counts", func(e audit.Entry) error {
			return h.Units.SetCounts(ctx, fence, unitKey("u-1"), unitimport.Counts{Versions: 3, Committed: 0}, e)
		}},
		{"import:ready", func(e audit.Entry) error { return h.Store.Transition(ctx, fence, "imp-1", ready, e) }},
		{"import:run", func(e audit.Entry) error { return h.Store.Transition(ctx, fence, "imp-1", run, e) }},
		{
			"import:unit",
			func(e audit.Entry) error { return h.Units.TransitionUnit(ctx, fence, unitKey("u-1"), start, e) },
		},
		{"import:cancel", func(e audit.Entry) error { return h.Store.Transition(ctx, fence, "imp-1", cancel, e) }},
		{"import:purge", func(e audit.Entry) error { return h.Store.Purge(ctx, fence, "imp-1", e) }},
	}

	want := make([]string, 0, 1+len(steps))
	want = append(want, "import:run") // The create in setup.

	for _, step := range steps {
		if err := step.do(entry("acme", step.action)); err != nil {
			t.Fatalf("%s: %v", step.action, err)
		}

		want = append(want, step.action)
	}

	got := h.Audited(t, "acme")
	actions := make([]string, 0, len(got))

	for n := range got {
		actions = append(actions, got[n].Action)
	}

	if !slices.Equal(actions, want) {
		t.Errorf("audit actions = %v, want %v", actions, want)
	}

	// A write that fails writes no entry.
	bad := unitimport.Move{From: unitimport.StateReleased, To: unitimport.StateRunning, Reason: ""}

	err := h.Store.Transition(ctx, fence, "imp-1", bad, entry("acme", "import:run"))
	if err == nil || len(h.Audited(t, "acme")) != len(want) {
		t.Errorf(
			"refused write: error %v, entries %d, want an error and %d entries",
			err,
			len(h.Audited(t, "acme")),
			len(want),
		)
	}
}

func testAuditNamespace(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	h.Namespace(t, "other")

	_, err := h.Store.Create(t.Context(), fence, newImport("acme", "imp-2"), entry("other", "import:run"))
	mustIs(t, "create with an entry of another namespace", err, audit.ErrInvalidEntry)

	_, err = h.Store.Create(t.Context(), fence, newImport("acme", "imp-2"), entry("acme", ""))
	mustIs(t, "create with an invalid entry", err, audit.ErrInvalidEntry)

	if _, err = h.Store.Get(t.Context(), "acme", "imp-2"); !errors.Is(err, unitimport.ErrNotFound) {
		t.Errorf("get after the refused creates: %v, want not found", err)
	}
}

// sameOptions reports whether a and b hold the same options.
func sameOptions(a, b unitimport.Options) bool {
	return a.HistoryLimit == b.HistoryLimit && a.LockSource == b.LockSource && a.Since.Equal(b.Since)
}

// sameImport reports whether got holds the fields that a create stores for
// want.
func sameImport(got, want unitimport.Import) bool {
	return got.ID == want.ID && got.Namespace == want.Namespace && got.Kind == want.Kind &&
		got.Address == want.Address && slices.Equal(got.Selection.Names, want.Selection.Names) &&
		got.Credential == want.Credential && got.Profile == want.Profile && got.ProfilePath == want.ProfilePath &&
		sameOptions(got.Options, want.Options) && sameStart(got, want)
}

// sameStart reports whether got is a new import of the creator and time of want.
func sameStart(got, want unitimport.Import) bool {
	return got.State == unitimport.StatePlanning && got.CreatedBy == want.CreatedBy &&
		got.CreatedAt.Equal(want.CreatedAt) && got.EndedAt.IsZero()
}

// checkPausedWithLedger checks that an import with a committed version does
// not plan again.
func checkPausedWithLedger(t *testing.T, h Harness, fence namespace.Fence) {
	t.Helper()

	e := entry("acme", "import:run")

	if _, err := h.Store.Create(t.Context(), fence, newImport("acme", "imp-3"), e); err != nil {
		t.Fatalf("create: %v", err)
	}

	walk(t, h, fence, "imp-3",
		unitimport.StatePlanning, unitimport.StatePlanned, unitimport.StateRunning, unitimport.StatePaused)

	row := ledgerRow("u-1", "v1", at())
	row.ImportID = "imp-3"

	if err := h.SeedLedger(t.Context(), "acme", row); err != nil {
		t.Fatalf("seed ledger row: %v", err)
	}

	m := unitimport.Move{From: unitimport.StatePaused, To: unitimport.StatePlanning, Reason: ""}
	err := h.Store.Transition(t.Context(), fence, "imp-3", m, e)
	mustIs(t, "plan again with a committed version", err, unitimport.ErrTransition)
}
