// Package savedviewtest is the conformance suite of the Store port of package
// savedview. A new adapter runs Run with a Factory.
//
// Each case states one rule of the port.
package savedviewtest

import (
	"errors"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/savedview"
)

// Harness is one fresh, empty store for one subtest. Namespace makes the
// namespace called name and returns its current fence. Audit returns the
// audit entries of a namespace, oldest first.
//
// Seed writes views directly to the storage of the store, with no fence, no
// audit entry and no limit check, in one transaction. The namespace of each
// view exists already. The limit cases use it to fill a namespace up to one
// view below a limit, and then write the last views through Store. An adapter
// can leave Seed nil: the cases then fill the namespace through Store.
type Harness struct {
	Store     savedview.Store
	Namespace func(t *testing.T, name string) namespace.Fence
	Audit     func(t *testing.T, ns string) []audit.Entry
	Seed      func(t *testing.T, views []savedview.View)
}

// Factory returns a fresh harness for one subtest.
type Factory func(t *testing.T) Harness

type suiteCase struct {
	name string
	test func(t *testing.T, h Harness)
}

// CaseNames returns the name of each case, in order.
func CaseNames() []string {
	cases := cases()
	names := make([]string, 0, len(cases))

	for _, tc := range cases {
		names = append(names, tc.name)
	}

	return names
}

// Run runs every case of the suite.
func Run(t *testing.T, newHarness Factory) {
	t.Helper()

	for _, tc := range cases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.test(t, newHarness(t))
		})
	}
}

// cases lists the rules of the Store port. Each name states one rule.
func cases() []suiteCase {
	return slices.Concat(
		// Create stores the view, and Get returns the same name, owner and
		// query. The query holds no result.
		createCases(),
		// A view is visible to its owner only. This is the defense layer of
		// the store: the handler checks the owner too.
		visibilityCases(),
		// A write carries the fence and the audit entry of the change, and a
		// failure changes nothing.
		writeCases(),
	)
}

// testNewPrincipalSameSubjectSeesNone creates views for the ID of a principal,
// then reads as a new principal that has the same subject and another ID. The
// store sees only the ID, so the new principal gets no view.
func testNewPrincipalSameSubjectSeesNone(t *testing.T, h Harness) {
	t.Helper()

	v := newView("acme", ownerA, "Payments")
	mustCreate(t, h, v)

	fence := h.Namespace(t, "acme")

	_, err := h.Store.Get(t.Context(), "acme", v.ID, ownerB)
	if !errors.Is(err, savedview.ErrNotFound) {
		t.Fatalf("get as the new principal = %v, want ErrNotFound", err)
	}

	views, err := h.Store.List(t.Context(), "acme", ownerB)
	if err != nil || len(views) != 0 {
		t.Fatalf("list as the new principal = %v, %v; want no view", views, err)
	}

	// The new principal cannot delete the view, and the view stays.
	err = h.Store.Delete(t.Context(), "acme", v.ID, ownerB, fence, auditEntry("acme", "delete"))
	if !errors.Is(err, savedview.ErrNotFound) {
		t.Fatalf("delete as the new principal = %v, want ErrNotFound", err)
	}

	if _, err = h.Store.Get(t.Context(), "acme", v.ID, ownerA); err != nil {
		t.Fatalf("get as the first principal: %v", err)
	}
}
