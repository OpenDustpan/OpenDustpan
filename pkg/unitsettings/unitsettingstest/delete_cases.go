package unitsettingstest

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/unitsettings"
)

func deleteCases() []suiteCase {
	return []suiteCase{
		{name: "delete removes the record and appends a reset entry", test: testDelete},
		{name: "delete of a unit with no record returns not found", test: testDeleteNone},
		{name: "delete with a version that is not the stored one returns conflict", test: testDeleteConflict},
		{name: "delete with a stale fence returns fenced and keeps the record", test: testDeleteStaleFence},
		{name: "a record can be created again after a delete, at version 1", test: testDeleteThenCreate},
	}
}

func testDelete(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	s := settingsFor(h.Unit(t, "acme", "pulumi", "web/prod"))
	put(t, h, &s, 0, fence)

	if err := tryDelete(t, h, s.Unit, 1, fence); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := h.Store.Get(t.Context(), s.Unit); !errors.Is(err, unitsettings.ErrNotFound) {
		t.Fatalf("Get after Delete = %v, want ErrNotFound", err)
	}

	entries := h.Audit(t, "acme")
	if last := entries[len(entries)-1]; last.Action != unitsettings.AuditActionReset || len(last.Changes) == 0 {
		t.Fatalf("last entry = %q with %d changes, want a reset with the fields that were set", last.Action,
			len(last.Changes))
	}
}

func testDeleteNone(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")

	if err := tryDelete(t, h, unit, 1, fence); !errors.Is(err, unitsettings.ErrNotFound) {
		t.Fatalf("Delete = %v, want ErrNotFound", err)
	}
}

func testDeleteConflict(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	s := settingsFor(h.Unit(t, "acme", "pulumi", "web/prod"))
	put(t, h, &s, 0, fence)

	if err := tryDelete(t, h, s.Unit, wrongVersion, fence); !errors.Is(err, unitsettings.ErrConflict) {
		t.Fatalf("Delete = %v, want ErrConflict", err)
	}
}

func testDeleteStaleFence(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	s := settingsFor(h.Unit(t, "acme", "pulumi", "web/prod"))
	put(t, h, &s, 0, fence)

	if err := tryDelete(t, h, s.Unit, 1, staleFence(fence)); !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("Delete = %v, want ErrFenced", err)
	}

	if _, err := h.Store.Get(t.Context(), s.Unit); err != nil {
		t.Fatalf("Get after a fenced Delete: %v", err)
	}
}

func testDeleteThenCreate(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	s := settingsFor(h.Unit(t, "acme", "pulumi", "web/prod"))
	put(t, h, &s, 0, fence)

	if err := tryDelete(t, h, s.Unit, 1, fence); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if got := put(t, h, &s, 0, fence); got.Version != 1 {
		t.Fatalf("Put after Delete gave version %d, want 1", got.Version)
	}
}
