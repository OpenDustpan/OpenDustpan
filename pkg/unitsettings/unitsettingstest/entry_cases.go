package unitsettingstest

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/unitsettings"
)

func entryCases() []suiteCase {
	return []suiteCase{
		{name: "put with an invalid audit entry returns invalid and stores nothing", test: testPutInvalidEntry},
		{name: "put with an audit entry of another namespace returns invalid", test: testPutOtherNamespaceEntry},
		{name: "delete with an invalid audit entry returns invalid and keeps the record", test: testDeleteInvalidEntry},
		{name: "delete with an audit entry of another namespace returns invalid", test: testDeleteOtherNamespaceEntry},
		{name: "set defaults with an invalid audit entry returns invalid", test: testDefaultsInvalidEntry},
		{
			name: "set defaults with an audit entry of another namespace returns invalid",
			test: testDefaultsOtherNamespaceEntry,
		},
	}
}

// invalidEntry returns an audit entry in ns that fails audit.Entry.Validate: it
// names no actor.
func invalidEntry(ns namespace.Name) audit.Entry {
	e := auditEntry(ns)
	e.Subject = ""

	return e
}

func wantInvalidEntry(t *testing.T, err error) {
	t.Helper()

	if !errors.Is(err, unitsettings.ErrInvalid) || !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("error = %v, want ErrInvalid that wraps audit.ErrInvalidEntry", err)
	}
}

func testPutInvalidEntry(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	s := settingsFor(h.Unit(t, "acme", "pulumi", "web/prod"))

	_, err := h.Store.Put(t.Context(), &s, 0, fence, invalidEntry(fence.Namespace))
	wantInvalidEntry(t, err)

	if _, getErr := h.Store.Get(t.Context(), s.Unit); !errors.Is(getErr, unitsettings.ErrNotFound) {
		t.Fatalf("Get after a refused Put = %v, want ErrNotFound", getErr)
	}
}

func testPutOtherNamespaceEntry(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	other := h.Namespace(t, "beta")
	s := settingsFor(h.Unit(t, "acme", "pulumi", "web/prod"))

	_, err := h.Store.Put(t.Context(), &s, 0, fence, auditEntry(other.Namespace))
	wantInvalidEntry(t, err)

	if _, getErr := h.Store.Get(t.Context(), s.Unit); !errors.Is(getErr, unitsettings.ErrNotFound) {
		t.Fatalf("Get after a refused Put = %v, want ErrNotFound", getErr)
	}
}

func testDeleteInvalidEntry(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	s := settingsFor(h.Unit(t, "acme", "pulumi", "web/prod"))
	put(t, h, &s, 0, fence)

	wantInvalidEntry(t, h.Store.Delete(t.Context(), s.Unit, 1, fence, invalidEntry(fence.Namespace)))

	if _, err := h.Store.Get(t.Context(), s.Unit); err != nil {
		t.Fatalf("Get after a refused Delete: %v", err)
	}
}

func testDeleteOtherNamespaceEntry(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	other := h.Namespace(t, "beta")
	s := settingsFor(h.Unit(t, "acme", "pulumi", "web/prod"))
	put(t, h, &s, 0, fence)

	wantInvalidEntry(t, h.Store.Delete(t.Context(), s.Unit, 1, fence, auditEntry(other.Namespace)))

	if _, err := h.Store.Get(t.Context(), s.Unit); err != nil {
		t.Fatalf("Get after a refused Delete: %v", err)
	}
}

func testDefaultsInvalidEntry(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")

	_, err := h.Store.SetDefaults(t.Context(), poolDefaults(fence.Namespace, testPool), 0, fence,
		invalidEntry(fence.Namespace))
	wantInvalidEntry(t, err)

	if d, getErr := h.Store.Defaults(t.Context(), fence.Namespace); getErr != nil || d.Version != 0 {
		t.Fatalf("Defaults = %+v, %v, want the defaults that a refused write left", d, getErr)
	}
}

func testDefaultsOtherNamespaceEntry(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	other := h.Namespace(t, "beta")

	_, err := h.Store.SetDefaults(t.Context(), poolDefaults(fence.Namespace, testPool), 0, fence,
		auditEntry(other.Namespace))
	wantInvalidEntry(t, err)

	if d, getErr := h.Store.Defaults(t.Context(), fence.Namespace); getErr != nil || d.Version != 0 {
		t.Fatalf("Defaults = %+v, %v, want the defaults that a refused write left", d, getErr)
	}
}
