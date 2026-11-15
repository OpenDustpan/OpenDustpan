package unitsettingstest

import (
	"errors"
	"reflect"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/unitsettings"
)

func recordCases() []suiteCase {
	return []suiteCase{
		{name: "get of a unit with no record returns not found", test: testGetNoRecord},
		{name: "get of an unknown unit returns unknown unit", test: testGetUnknownUnit},
		{name: "put with version 0 creates the record at version 1", test: testPutCreates},
		{name: "put round-trips every field and the steps", test: testPutRoundTrip},
		{name: "records of two units are separate", test: testRecordsSeparate},
	}
}

func testGetNoRecord(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")

	if _, err := h.Store.Get(t.Context(), unit); !errors.Is(err, unitsettings.ErrNotFound) {
		t.Fatalf("Get = %v, want ErrNotFound", err)
	}
}

func testGetUnknownUnit(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, "acme")

	ghost, err := identity.UnitObject("pulumi", mustName(t, "acme"), "web/ghost")
	if err != nil {
		t.Fatal(err)
	}

	ghost = ghost.WithUnitID("unit-does-not-exist")

	if _, err := h.Store.Get(t.Context(), ghost); !errors.Is(err, unitsettings.ErrUnknownUnit) {
		t.Fatalf("Get = %v, want ErrUnknownUnit", err)
	}
}

func testPutCreates(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	s := settingsFor(unit)

	got, err := h.Store.Put(t.Context(), &s, 0, fence, auditEntry(fence.Namespace))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	if got.Settings.Version != 1 || !got.Settings.UpdatedAt.Equal(writeTime()) {
		t.Fatalf("Put gave version %d at %v, want 1 at %v", got.Settings.Version, got.Settings.UpdatedAt,
			writeTime())
	}

	read, err := h.Store.Get(t.Context(), unit)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if read.Version != 1 || read.CodeLocation != s.CodeLocation || read.Unit.UnitID() != unit.UnitID() {
		t.Fatalf("Get = %+v, want the record that Put stored", read)
	}
}

func testPutRoundTrip(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "opentofu", "network")
	s := settingsFor(unit)
	s.Pool = "rp-" + "0123456789abcdef"
	s.Steps = []job.Step{{
		Name: "init", Role: job.RoleTool, Phase: job.PhasePlan, Args: []string{"tofu", "init"},
		Env: map[string]string{"TF_IN_AUTOMATION": "1"}, Dir: "modules", Timeout: stepTimeout,
		AllowFailure: false, ChangesResources: false,
		Outputs: []job.Output{{Kind: job.OutputPlanDisplay, Path: "plan.json"}},
	}}

	if err := tryPut(t, h, &s, 0, fence); err != nil {
		t.Fatalf("Put: %v", err)
	}

	read, err := h.Store.Get(t.Context(), unit)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if !reflect.DeepEqual(read.Steps, s.Steps) || read.Pool != s.Pool || read.Executor != s.Executor ||
		!reflect.DeepEqual(read.WriteBack, s.WriteBack) || read.ReviewRequests != s.ReviewRequests {
		t.Fatalf("Get = %+v, want %+v", read, s)
	}
}

func testRecordsSeparate(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	a := h.Unit(t, "acme", "pulumi", "web/a")
	b := h.Unit(t, "acme", "pulumi", "web/b")
	s := settingsFor(a)

	if err := tryPut(t, h, &s, 0, fence); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if _, err := h.Store.Get(t.Context(), b); !errors.Is(err, unitsettings.ErrNotFound) {
		t.Fatalf("Get of the other unit = %v, want ErrNotFound", err)
	}
}

func mustName(t *testing.T, s string) namespace.Name {
	t.Helper()

	n, err := namespace.ParseName(s)
	if err != nil {
		t.Fatal(err)
	}

	return n
}

// auditActions returns the actions of the entries.
func auditActions(entries []audit.Entry) []string {
	out := make([]string, 0, len(entries))
	for i := range entries {
		out = append(out, entries[i].Action)
	}

	return out
}
