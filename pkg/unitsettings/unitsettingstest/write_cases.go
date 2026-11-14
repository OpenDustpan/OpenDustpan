package unitsettingstest

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/unitsettings"
)

func writeCases() []suiteCase {
	return []suiteCase{
		{name: "put with a version that is not the stored one returns conflict", test: testPutConflict},
		{name: "put with version 0 on an existing record returns conflict", test: testPutCreateConflict},
		{name: "put raises the version by one", test: testPutRaisesVersion},
		{name: "put of equal settings writes nothing", test: testPutNoChange},
		{name: "put appends one entry with the old and the new value of each changed field", test: testPutAudit},
		{name: "put of invalid settings returns invalid and stores nothing", test: testPutInvalid},
		{name: "put with a stale fence returns fenced and stores nothing", test: testPutStaleFence},
		{name: "put with the fence of another namespace returns fenced", test: testPutOtherFence},
		{name: "put of a unit with a wrong stable ID returns unknown unit", test: testPutWrongID},
		{name: "put of a unit with no stable ID returns invalid", test: testPutNoID},
		{name: "put keeps the arguments of the steps out of the audit entry", test: testPutStepsAudit},
		{
			name: "put after a change of the defaults gives the effective value of the new defaults",
			test: testPutDefaultsOfTheTransaction,
		},
	}
}

// put writes s and fails the test on an error.
func put(
	t *testing.T,
	h Harness,
	s *unitsettings.Settings,
	expected int64,
	fence namespace.Fence,
) unitsettings.Settings {
	t.Helper()

	out, err := h.Store.Put(t.Context(), s, expected, fence, auditEntry(fence.Namespace))
	if err != nil {
		t.Fatalf("Put at version %d: %v", expected, err)
	}

	return out.Settings
}

func testPutConflict(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	s := settingsFor(h.Unit(t, "acme", "pulumi", "web/prod"))
	put(t, h, &s, 0, fence)

	s.CodeLocation.Ref = refRelease

	if err := tryPut(t, h, &s, wrongVersion, fence); !errors.Is(err, unitsettings.ErrConflict) {
		t.Fatalf("Put at a wrong version = %v, want ErrConflict", err)
	}

	read, err := h.Store.Get(t.Context(), s.Unit)
	if err != nil || read.CodeLocation.Ref != refMain || read.Version != 1 {
		t.Fatalf("Get = %+v, %v, want the first record", read, err)
	}
}

func testPutCreateConflict(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	s := settingsFor(h.Unit(t, "acme", "pulumi", "web/prod"))
	put(t, h, &s, 0, fence)

	s.CodeLocation.Ref = refRelease

	if err := tryPut(t, h, &s, 0, fence); !errors.Is(err, unitsettings.ErrConflict) {
		t.Fatalf("Put at version 0 over a record = %v, want ErrConflict", err)
	}
}

func testPutRaisesVersion(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	s := settingsFor(h.Unit(t, "acme", "pulumi", "web/prod"))
	put(t, h, &s, 0, fence)

	s.CodeLocation.Ref = refRelease

	if got := put(t, h, &s, 1, fence); got.Version != 2 || got.CodeLocation.Ref != refRelease {
		t.Fatalf("Put = %+v, want version 2 with the new ref", got)
	}
}

func testPutNoChange(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	s := settingsFor(h.Unit(t, "acme", "pulumi", "web/prod"))
	put(t, h, &s, 0, fence)

	before := len(h.Audit(t, "acme"))

	got := put(t, h, &s, 1, fence)
	if got.Version != 1 {
		t.Fatalf("Put of equal settings gave version %d, want 1", got.Version)
	}

	if after := len(h.Audit(t, "acme")); after != before {
		t.Fatalf("Put of equal settings wrote %d audit entries, want 0", after-before)
	}
}

func testPutAudit(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	s := settingsFor(h.Unit(t, "acme", "pulumi", "web/prod"))
	put(t, h, &s, 0, fence)

	s.CodeLocation.Ref = refRelease
	put(t, h, &s, 1, fence)

	entries := h.Audit(t, "acme")
	if len(entries) != auditPair {
		t.Fatalf("got %d audit entries, want %d: %v", len(entries), auditPair, auditActions(entries))
	}

	last := entries[1]
	if last.Action != unitsettings.AuditActionChanged || len(last.Changes) != 1 {
		t.Fatalf("last entry = %q with %d changes, want one change of the ref", last.Action, len(last.Changes))
	}

	c := last.Changes[0]
	if c.Key != "codeLocation.ref" || c.Old == nil || *c.Old != refMain || c.New != refRelease {
		t.Fatalf("change = %+v, want codeLocation.ref main -> release", c)
	}
}

func testPutInvalid(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	s := settingsFor(h.Unit(t, "acme", "pulumi", "web/prod"))
	s.CodeLocation.Directory = "../outside"

	if err := tryPut(t, h, &s, 0, fence); !errors.Is(err, unitsettings.ErrInvalid) {
		t.Fatalf("Put = %v, want ErrInvalid", err)
	}

	if _, err := h.Store.Get(t.Context(), s.Unit); !errors.Is(err, unitsettings.ErrNotFound) {
		t.Fatalf("Get after a refused Put = %v, want ErrNotFound", err)
	}

	if n := len(h.Audit(t, "acme")); n != 0 {
		t.Fatalf("a refused Put wrote %d audit entries, want 0", n)
	}
}

func testPutStaleFence(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	s := settingsFor(h.Unit(t, "acme", "pulumi", "web/prod"))

	if err := tryPut(t, h, &s, 0, staleFence(fence)); !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("Put = %v, want ErrFenced", err)
	}

	if _, err := h.Store.Get(t.Context(), s.Unit); !errors.Is(err, unitsettings.ErrNotFound) {
		t.Fatalf("Get after a fenced Put = %v, want ErrNotFound", err)
	}

	if n := len(h.Audit(t, "acme")); n != 0 {
		t.Fatalf("a fenced Put wrote %d audit entries, want 0", n)
	}
}

func testPutOtherFence(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, "acme")
	other := h.Namespace(t, "beta")
	s := settingsFor(h.Unit(t, "acme", "pulumi", "web/prod"))

	if err := tryPut(t, h, &s, 0, other); !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("Put with the fence of beta = %v, want ErrFenced", err)
	}
}

func testPutWrongID(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	s := settingsFor(h.Unit(t, "acme", "pulumi", "web/prod"))
	s.Unit = s.Unit.WithUnitID("unit-another")

	if err := tryPut(t, h, &s, 0, fence); !errors.Is(err, unitsettings.ErrUnknownUnit) {
		t.Fatalf("Put = %v, want ErrUnknownUnit", err)
	}
}

func testPutNoID(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	s := settingsFor(h.Unit(t, "acme", "pulumi", "web/prod"))
	s.Unit = s.Unit.WithUnitID("")

	if err := tryPut(t, h, &s, 0, fence); !errors.Is(err, unitsettings.ErrInvalid) {
		t.Fatalf("Put = %v, want ErrInvalid", err)
	}
}

func testPutStepsAudit(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	s := settingsFor(h.Unit(t, "acme", "opentofu", "network"))
	s.Steps = []job.Step{{
		Name: "plan", Role: job.RoleTool, Phase: job.PhasePlan, Args: []string{"tofu", "plan", "-var=note=hunter2"},
		Env: nil, Dir: "", Timeout: 0, AllowFailure: false, ChangesResources: false, Outputs: nil,
	}}
	put(t, h, &s, 0, fence)

	entries := h.Audit(t, "acme")
	for i := range entries {
		e := &entries[i]

		for _, c := range e.Changes {
			if strings.Contains(c.New, "hunter2") || (c.Old != nil && strings.Contains(*c.Old, "hunter2")) {
				t.Fatalf("the audit change %q holds an argument of a step", c.Key)
			}
		}

		if !slices.ContainsFunc(e.Changes, func(c audit.Change) bool { return c.Key == "steps" }) {
			t.Fatalf("the entry has no change of the steps: %+v", e.Changes)
		}
	}
}

// testPutDefaultsOfTheTransaction checks that Put returns the unit defaults of
// its own transaction, so a change of the defaults between an earlier read and
// the write cannot give an effective value from the old defaults.
func testPutDefaultsOfTheTransaction(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")

	if _, err := trySetDefaults(t, h, poolDefaults(fence.Namespace, testPool), 0, fence); err != nil {
		t.Fatalf("SetDefaults: %v", err)
	}

	read, readErr := h.Store.Defaults(t.Context(), fence.Namespace)
	if readErr != nil {
		t.Fatalf("Defaults: %v", readErr)
	}

	if read.Pool != testPool || read.Version != 1 {
		t.Fatalf("Defaults = %+v, want the first pool at version 1", read)
	}

	// Another writer changes the defaults after the read above. The write must
	// give the defaults of its own transaction, not the defaults that the read
	// found.
	next := "rp-" + "fedcba9876543210"

	if _, err := trySetDefaults(t, h, poolDefaults(fence.Namespace, next), 1, fence); err != nil {
		t.Fatalf("SetDefaults: %v", err)
	}

	s := settingsFor(unit)

	out, err := h.Store.Put(t.Context(), &s, 0, fence, auditEntry(fence.Namespace))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	if out.Settings.Pool != "" {
		t.Fatalf("stored pool = %q, want empty: a write copies no default into the record", out.Settings.Pool)
	}

	wantDefaultsOfTheWrite(t, out, next)
}

// wantDefaultsOfTheWrite fails the test unless out holds the defaults pool at
// version 2 and the effective pool of the record comes from them.
func wantDefaultsOfTheWrite(t *testing.T, out unitsettings.PutResult, pool string) {
	t.Helper()

	if out.Defaults.Pool != pool || out.Defaults.Version != 2 || !out.Defaults.UpdatedAt.Equal(writeTime()) {
		t.Fatalf("Put defaults = %+v, want the pool %s at version 2 at %v", out.Defaults, pool, writeTime())
	}

	c := unitsettings.Context{DefaultConnection: "", HasJobPool: false, DefaultFiles: nil}

	eff := unitsettings.Resolve(&out.Settings, out.Defaults, c)
	if eff.Pool.Value != pool || eff.Pool.Origin != unitsettings.OriginNamespaceDefault {
		t.Fatalf("effective pool = %+v, want %s from the namespace defaults", eff.Pool, pool)
	}
}
