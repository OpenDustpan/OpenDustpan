package unitsettingstest

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/unitsettings"
)

const testPool = "rp-0123456789abcdef"

func defaultsCases() []suiteCase {
	return []suiteCase{
		{name: "defaults of a namespace that never set them are empty at version 0", test: testDefaultsEmpty},
		{name: "defaults of an unknown namespace return not found", test: testDefaultsUnknown},
		{name: "set defaults stores the pool and appends an entry", test: testDefaultsSet},
		{name: "set defaults with a wrong version returns conflict", test: testDefaultsConflict},
		{name: "set defaults of equal values writes nothing", test: testDefaultsNoChange},
		{name: "set defaults with an invalid pool returns invalid", test: testDefaultsInvalid},
		{name: "set defaults with a stale fence returns fenced", test: testDefaultsStaleFence},
		{name: "defaults of two namespaces are separate", test: testDefaultsSeparate},
	}
}

func testDefaultsEmpty(t *testing.T, h Harness) {
	t.Helper()

	ns := h.Namespace(t, "acme").Namespace

	d, err := h.Store.Defaults(t.Context(), ns)
	if err != nil || d.Pool != "" || d.Version != 0 || d.Namespace != ns {
		t.Fatalf("Defaults = %+v, %v, want the empty defaults of acme at version 0", d, err)
	}
}

func testDefaultsUnknown(t *testing.T, h Harness) {
	t.Helper()

	if _, err := h.Store.Defaults(t.Context(), mustName(t, "ghost")); !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("Defaults = %v, want namespace.ErrNotFound", err)
	}
}

func testDefaultsSet(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")

	got, err := trySetDefaults(t, h, poolDefaults(fence.Namespace, testPool), 0, fence)
	if err != nil || got.Pool != testPool || got.Version != 1 || !got.UpdatedAt.Equal(writeTime()) {
		t.Fatalf("SetDefaults = %+v, %v, want the pool at version 1", got, err)
	}

	read, err := h.Store.Defaults(t.Context(), fence.Namespace)
	if err != nil || read.Pool != testPool || read.Version != 1 {
		t.Fatalf("Defaults = %+v, %v", read, err)
	}

	entries := h.Audit(t, "acme")
	if len(entries) != 1 || entries[0].Action != unitsettings.AuditActionDefaultsChanged ||
		len(entries[0].Changes) != 1 || entries[0].Changes[0].Key != "pool" {
		t.Fatalf("audit entries = %v, want one change of the pool", auditActions(entries))
	}
}

func testDefaultsConflict(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")

	_, err := trySetDefaults(t, h, poolDefaults(fence.Namespace, testPool), wrongVersion, fence)
	if !errors.Is(err, unitsettings.ErrConflict) {
		t.Fatalf("SetDefaults = %v, want ErrConflict", err)
	}
}

func testDefaultsNoChange(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	d := poolDefaults(fence.Namespace, testPool)

	if _, err := trySetDefaults(t, h, d, 0, fence); err != nil {
		t.Fatal(err)
	}

	got, err := trySetDefaults(t, h, d, 1, fence)
	if err != nil || got.Version != 1 || len(h.Audit(t, "acme")) != 1 {
		t.Fatalf("SetDefaults of equal values = %+v, %v with %d entries, want version 1 and 1 entry", got, err,
			len(h.Audit(t, "acme")))
	}
}

func testDefaultsInvalid(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")

	_, err := trySetDefaults(t, h, poolDefaults(fence.Namespace, "not-a-pool"), 0, fence)
	if !errors.Is(err, unitsettings.ErrInvalid) {
		t.Fatalf("SetDefaults = %v, want ErrInvalid", err)
	}
}

func testDefaultsStaleFence(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")

	_, err := trySetDefaults(t, h, poolDefaults(fence.Namespace, testPool), 0, staleFence(fence))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("SetDefaults = %v, want ErrFenced", err)
	}

	d, err := h.Store.Defaults(t.Context(), fence.Namespace)
	if err != nil || d.Version != 0 {
		t.Fatalf("Defaults = %+v, %v, want the defaults that a fenced write left", d, err)
	}
}

func testDefaultsSeparate(t *testing.T, h Harness) {
	t.Helper()

	a := h.Namespace(t, "acme")
	b := h.Namespace(t, "beta")

	if _, err := trySetDefaults(t, h, poolDefaults(a.Namespace, testPool), 0, a); err != nil {
		t.Fatal(err)
	}

	if d, err := h.Store.Defaults(t.Context(), b.Namespace); err != nil || d.Pool != "" {
		t.Fatalf("Defaults of beta = %+v, %v, want empty", d, err)
	}
}
