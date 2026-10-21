package trigger_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/cron"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/trigger"
)

func unit(id string) graph.Unit {
	return graph.Unit{Namespace: "acme", Tool: "opentofu", ID: id}
}

func follows(id trigger.ID, unitID, sourceID string) trigger.Trigger {
	return trigger.Trigger{
		ID:        id,
		Unit:      unit(unitID),
		Kind:      trigger.KindFollows,
		Frequency: trigger.Frequency{Preset: "", Cron: ""},
		Source:    unit(sourceID),
		Enabled:   true,
		LastSaver: "user:ada", LastSaverID: "prn-user:ada",
	}
}

func schedule(id trigger.ID, f trigger.Frequency) trigger.Trigger {
	return trigger.Trigger{
		ID: id, Unit: unit("a"), Kind: trigger.KindSchedule, Frequency: f,
		Source: graph.Unit{Namespace: "", Tool: "", ID: ""}, Enabled: true, LastSaver: "user:ada", LastSaverID: "prn-user:ada",
	}
}

func TestIDValidate(t *testing.T) {
	t.Parallel()

	for _, id := range []trigger.ID{"a", "trg_01-x", trigger.ID(strings.Repeat("a", trigger.MaxIDBytes))} {
		if err := id.Validate(); err != nil {
			t.Errorf("ID(%q).Validate() = %v", id, err)
		}
	}

	for _, id := range []trigger.ID{
		"", trigger.ID(strings.Repeat("a", trigger.MaxIDBytes+1)), "A", "a b", "a/b", "a\n", "a\x00", "é", "../x", "a.b",
	} {
		if err := id.Validate(); !errors.Is(err, trigger.ErrInvalid) {
			t.Errorf("ID(%q).Validate() = %v, want ErrInvalid", id, err)
		}
	}
}

func TestKinds(t *testing.T) {
	t.Parallel()

	got := trigger.Kinds()
	if len(got) != 3 || got[0] != "review-request-push" || got[1] != "schedule" || got[2] != "follows" {
		t.Fatalf("Kinds() = %v", got)
	}

	got[0] = "changed"

	if trigger.Kinds()[0] != trigger.KindReviewRequestPush {
		t.Fatal("Kinds() shares its slice between calls")
	}

	for _, k := range []trigger.Kind{"", "manual", "Schedule", "follows "} {
		if k.Valid() {
			t.Errorf("Kind(%q).Valid() = true", k)
		}
	}
}

func TestFrequencyValidate(t *testing.T) {
	t.Parallel()

	for name, f := range map[string]trigger.Frequency{
		"preset":  {Preset: cron.PresetDaily, Cron: ""},
		"cron":    {Preset: "", Cron: "0 3 * * 1-5"},
		"hourly":  {Preset: "", Cron: "0 * * * *"},
		"every 6": {Preset: cron.PresetEvery6Hours, Cron: ""},
	} {
		if err := f.Validate(); err != nil {
			t.Errorf("%s: Validate() = %v", name, err)
		}
	}

	for name, f := range map[string]trigger.Frequency{
		"empty":          {Preset: "", Cron: ""},
		"both":           {Preset: cron.PresetDaily, Cron: "0 3 * * *"},
		"unknown preset": {Preset: "monthly", Cron: ""},
		"every minute":   {Preset: "", Cron: "* * * * *"},
		"every 30 min":   {Preset: "", Cron: "*/30 * * * *"},
		"bad cron":       {Preset: "", Cron: "0 0 30 2 *"},
		"newline":        {Preset: "", Cron: "0 3 * * *\n"},
		"long":           {Preset: "", Cron: strings.Repeat("1,", 300) + "1 * * * *"},
	} {
		if err := f.Validate(); !errors.Is(err, trigger.ErrInvalid) {
			t.Errorf("%s: Validate() = %v, want ErrInvalid", name, err)
		}
	}
}

func TestFrequencyExpression(t *testing.T) {
	t.Parallel()

	got, err := trigger.Frequency{Preset: "", Cron: "0 3 * * *"}.Expression(unit("a"))
	if err != nil || got != "0 3 * * *" {
		t.Fatalf("Expression of a cron = %q, %v", got, err)
	}

	first, err := trigger.Frequency{Preset: cron.PresetDaily, Cron: ""}.Expression(unit("a"))
	if err != nil {
		t.Fatal(err)
	}

	if _, parseErr := cron.Parse(first); parseErr != nil {
		t.Fatalf("Expression of a preset = %q, which Parse refuses: %v", first, parseErr)
	}

	again, err := trigger.Frequency{Preset: cron.PresetDaily, Cron: ""}.Expression(unit("a"))
	if err != nil || again != first {
		t.Fatalf("Expression of a preset changes: %q then %q", first, again)
	}

	if _, err := (trigger.Frequency{Preset: "", Cron: ""}).Expression(unit("a")); !errors.Is(err, trigger.ErrInvalid) {
		t.Errorf("Expression of an empty frequency = %v", err)
	}

	daily := trigger.Frequency{Preset: cron.PresetDaily, Cron: ""}

	if _, err := daily.Expression(noUnit()); !errors.Is(err, trigger.ErrInvalid) {
		t.Errorf("Expression for an invalid unit = %v", err)
	}
}

func TestTriggerValidate(t *testing.T) {
	t.Parallel()

	valid := map[string]trigger.Trigger{
		"schedule preset": schedule("s1", trigger.Frequency{Preset: cron.PresetWeekly, Cron: ""}),
		"schedule cron":   schedule("s2", trigger.Frequency{Preset: "", Cron: "30 4 * * *"}),
		"follows":         follows("f1", "b", "a"),
		"review request": {
			ID:   "r1",
			Unit: unit("a"),
			Kind: trigger.KindReviewRequestPush,
			Frequency: trigger.Frequency{
				Preset: "",
				Cron:   "",
			},
			Source:    graph.Unit{Namespace: "", Tool: "", ID: ""},
			Enabled:   false,
			LastSaver: "user:ada", LastSaverID: "prn-user:ada",
		},
	}

	for name, tr := range valid {
		if err := tr.Validate(); err != nil {
			t.Errorf("%s: Validate() = %v", name, err)
		}
	}

	otherTool := follows("f2", "b", "a")
	otherTool.Source.Tool = "pulumi"

	if err := otherTool.Validate(); err != nil {
		t.Errorf("a unit follows a unit of another tool: Validate() = %v", err)
	}

	bad := func(mutate func(*trigger.Trigger)) trigger.Trigger {
		tr := follows("f1", "b", "a")
		mutate(&tr)

		return tr
	}

	for name, tr := range map[string]trigger.Trigger{
		"empty ID":           bad(func(tr *trigger.Trigger) { tr.ID = "" }),
		"bad ID":             bad(func(tr *trigger.Trigger) { tr.ID = "F1" }),
		"bad unit":           bad(func(tr *trigger.Trigger) { tr.Unit = noUnit() }),
		"unknown kind":       bad(func(tr *trigger.Trigger) { tr.Kind = "manual" }),
		"empty kind":         bad(func(tr *trigger.Trigger) { tr.Kind = "" }),
		"follows no source":  bad(func(tr *trigger.Trigger) { tr.Source = noUnit() }),
		"follows bad source": bad(func(tr *trigger.Trigger) { tr.Source.ID = "" }),
		"follows frequency":  bad(func(tr *trigger.Trigger) { tr.Frequency.Cron = "0 * * * *" }),
		"other namespace":    bad(func(tr *trigger.Trigger) { tr.Source.Namespace = "other" }),
		"schedule no freq":   schedule("s1", trigger.Frequency{Preset: "", Cron: ""}),
		"schedule fast":      schedule("s1", trigger.Frequency{Preset: "", Cron: "*/5 * * * *"}),
		"schedule with a src": func() trigger.Trigger {
			tr := schedule("s1", trigger.Frequency{Preset: cron.PresetDaily, Cron: ""})
			tr.Source = unit("x")
			return tr
		}(),
		"review with a freq": {
			ID: "r1", Unit: unit("a"), Kind: trigger.KindReviewRequestPush,
			Frequency: trigger.Frequency{Preset: cron.PresetDaily, Cron: ""}, Source: graph.Unit{Namespace: "", Tool: "", ID: ""}, Enabled: true,
			LastSaver: "user:ada", LastSaverID: "prn-user:ada",
		},
		"review with a source": {
			ID: "r1", Unit: unit("a"), Kind: trigger.KindReviewRequestPush,
			Frequency: trigger.Frequency{Preset: "", Cron: ""}, Source: unit("b"), Enabled: true,
			LastSaver: "user:ada", LastSaverID: "prn-user:ada",
		},
		"no saver":               bad(func(tr *trigger.Trigger) { tr.LastSaver = "" }),
		"long saver":             bad(func(tr *trigger.Trigger) { tr.LastSaver = strings.Repeat("a", trigger.MaxSaverBytes+1) }),
		"saver with a newline":   bad(func(tr *trigger.Trigger) { tr.LastSaver = "user:ada\nadmin" }),
		"saver with a bidi mark": bad(func(tr *trigger.Trigger) { tr.LastSaver = "user:ada\u202e" }),
		"saver not UTF-8":        bad(func(tr *trigger.Trigger) { tr.LastSaver = "user:\xff" }),
		"saver is a service principal": bad(func(tr *trigger.Trigger) {
			tr.LastSaver = "system:namespace-service:acme"
		}),
		"saver is a service principal in other case": bad(func(tr *trigger.Trigger) {
			tr.LastSaver = "SYSTEM:Namespace-Service:acme"
		}),
	} {
		if err := tr.Validate(); !errors.Is(err, trigger.ErrInvalid) {
			t.Errorf("%s: Validate() = %v, want ErrInvalid", name, err)
		}
	}

	self := follows("f1", "a", "a")

	var cycle *trigger.CycleError

	if err := self.Validate(); !errors.Is(err, trigger.ErrCycle) || !errors.As(err, &cycle) || len(cycle.Path) != 2 {
		t.Errorf("a unit that follows itself: Validate() = %v, want a CycleError of 2 units", err)
	}
}

func TestCheckCycle(t *testing.T) {
	t.Parallel()

	chain := []trigger.Trigger{follows("t1", "b", "a"), follows("t2", "c", "b")}

	tests := []struct {
		name     string
		existing []trigger.Trigger
		add      trigger.Trigger
		path     []string // unit IDs, empty when no loop
	}{
		{"no triggers", nil, follows("n", "b", "a"), nil},
		{"a chain", chain, follows("n", "d", "c"), nil},
		{"a second follower of one source", chain, follows("n", "d", "a"), nil},
		{"two sources for one unit", chain, follows("n", "c", "a"), nil},
		{"loop of two", chain[:1], follows("n", "a", "b"), []string{"b", "a", "b"}},
		{"loop of three", chain, follows("n", "a", "c"), []string{"c", "a", "b", "c"}},
		{"self", nil, follows("n", "a", "a"), []string{"a", "a"}},
		{
			"loop through a disabled trigger",
			[]trigger.Trigger{follows("t1", "b", "a"), disabled(follows("t2", "c", "b"))},
			follows("n", "a", "c"),
			nil,
		},
		{"a disabled trigger closes none", chain, disabled(follows("n", "a", "c")), nil},
		{"a replace drops its old edge", chain, follows("t1", "x", "a"), nil},
		{
			"a replace that closes a loop",
			[]trigger.Trigger{follows("t1", "b", "a"), follows("t2", "a", "c")},
			follows("t1", "c", "a"),
			[]string{"a", "c", "a"},
		},
		{
			"schedule triggers are not edges",
			[]trigger.Trigger{schedule("s1", trigger.Frequency{Preset: cron.PresetDaily, Cron: ""})},
			follows("n", "a", "b"),
			nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkCycle(t, trigger.CheckCycle(tc.existing, tc.add), tc.path)
		})
	}
}

// checkCycle checks that err is nil when path is empty, and else a CycleError
// with the units of path, in order.
func checkCycle(t *testing.T, err error, path []string) {
	t.Helper()

	if path == nil {
		if err != nil {
			t.Fatalf("CheckCycle = %v, want nil", err)
		}

		return
	}

	var cycle *trigger.CycleError

	if !errors.Is(err, trigger.ErrCycle) || !errors.As(err, &cycle) {
		t.Fatalf("CheckCycle = %v, want a CycleError", err)
	}

	got := make([]string, 0, len(cycle.Path))
	for i := range cycle.Path {
		got = append(got, cycle.Path[i].ID)
	}

	if strings.Join(got, ",") != strings.Join(path, ",") {
		t.Fatalf("path = %v, want %v", got, path)
	}
}

func noUnit() graph.Unit {
	return graph.Unit{Namespace: "", Tool: "", ID: ""}
}

func disabled(tr trigger.Trigger) trigger.Trigger {
	tr.Enabled = false

	return tr
}

// TestCheckCycleOfALongChain checks the bound on the work and on the error
// text: a chain of 2000 units finds its loop, and the text names 10 units.
func TestCheckCycleOfALongChain(t *testing.T) {
	t.Parallel()

	const n = 2000

	var existing []trigger.Trigger

	for i := 1; i < n; i++ {
		existing = append(
			existing,
			follows(trigger.ID("t"+strings.Repeat("x", 1)+strconv.Itoa(i)), "u"+strconv.Itoa(i), "u"+strconv.Itoa(i-1)),
		)
	}

	err := trigger.CheckCycle(existing, follows("closing", "u0", "u"+strconv.Itoa(n-1)))

	var cycle *trigger.CycleError

	if err == nil || !errors.As(err, &cycle) || len(cycle.Path) != n+1 {
		t.Fatalf("CheckCycle = %v, want a loop of %d units", err, n+1)
	}

	if strings.Count(err.Error(), "->") != trigger.MaxPathShown-1 || !strings.Contains(err.Error(), "more") {
		t.Fatalf("error text does not cut the path: %q", err)
	}
}

func TestKeys(t *testing.T) {
	t.Parallel()

	slot := time.Date(2026, 10, 21, 3, 17, 0, 0, time.UTC)

	if a, b := trigger.ScheduleKey("t1", slot), trigger.ScheduleKey("t1", slot.In(time.FixedZone("x", 7200))); a != b {
		t.Errorf("ScheduleKey depends on the zone: %q, %q", a, b)
	}

	if trigger.ScheduleKey("t1", slot) == trigger.ScheduleKey("t1", slot.Add(time.Hour)) {
		t.Error("ScheduleKey is the same for two slots")
	}

	if trigger.ScheduleKey("t1", slot) == trigger.ScheduleKey("t2", slot) {
		t.Error("ScheduleKey is the same for two triggers")
	}

	if trigger.UpstreamKey("t1", "v-1") == trigger.UpstreamKey("t1", "v-2") {
		t.Error("UpstreamKey is the same for two versions")
	}

	if trigger.ManualKey("t1", "user:ada", slot) != trigger.ManualKey("t1", "user:ada", slot.Add(30*time.Second)) ||
		trigger.ManualKey("t1", "user:ada", slot) == trigger.ManualKey("t1", "user:ada", slot.Add(time.Minute)) {
		t.Error("ManualKey is not one key for each minute")
	}

	if trigger.ManualKey("t1", "user:ada", slot) == trigger.ManualKey("t1", "user:bob", slot) {
		t.Error("ManualKey is the same for two actors")
	}

	// A key of one kind never equals a key of another kind.
	keys := map[string]bool{}
	for _, k := range []string{
		trigger.ScheduleKey("t1", slot), trigger.UpstreamKey("t1", "1"), trigger.ManualKey("t1", "user:ada", slot),
	} {
		if keys[k] {
			t.Errorf("key %q repeats", k)
		}

		keys[k] = true
	}
}

func TestAppliedValidate(t *testing.T) {
	t.Parallel()

	if err := (trigger.Applied{Unit: unit("a"), Version: "v-1"}).Validate(); err != nil {
		t.Errorf("Validate() = %v", err)
	}

	for name, a := range map[string]trigger.Applied{
		"no version":   {Unit: unit("a"), Version: ""},
		"long version": {Unit: unit("a"), Version: strings.Repeat("v", trigger.MaxVersionBytes+1)},
		"no unit":      {Unit: noUnit(), Version: "v-1"},
		"control byte": {Unit: unit("a"), Version: "v-1\n"},
		"colon":        {Unit: unit("a"), Version: "v:1"},
		"space":        {Unit: unit("a"), Version: "v 1"},
	} {
		if err := a.Validate(); !errors.Is(err, trigger.ErrInvalid) {
			t.Errorf("%s: Validate() = %v, want ErrInvalid", name, err)
		}
	}
}

func TestCycleErrorNames(t *testing.T) {
	t.Parallel()

	path := []graph.Unit{unit("a"), unit("b"), unit("a")}

	plain := &trigger.CycleError{Path: path, Names: nil}
	if got, want := plain.Error(), "trigger closes a loop of units: a -> b -> a"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	named := &trigger.CycleError{Path: path, Names: map[graph.Unit]string{unit("a"): "stack prod"}}
	if got, want := named.Error(), "trigger closes a loop of units: stack prod -> b -> stack prod"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
