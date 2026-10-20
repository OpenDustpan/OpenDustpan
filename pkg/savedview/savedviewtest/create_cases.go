package savedviewtest

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/savedview"
)

func createCases() []suiteCase {
	return []suiteCase{
		{name: "create then get returns the same view", test: testCreateThenGet},
		{name: "create refuses a view that fails validation", test: testCreateRefusesInvalid},
		{name: "create refuses a second view with the same name for the same owner", test: testCreateSameName},
		{name: "create accepts the same name for another owner", test: testCreateSameNameOtherOwner},
		{name: "create accepts the same name in another namespace", test: testCreateSameNameOtherNamespace},
		{name: "create refuses the view after the largest number of views of one owner", test: testOwnerLimit},
		{
			name: "create at the namespace cap returns ErrLimit, also for an owner with no view",
			test: testNamespaceCap,
		},
		{
			name: "create accepts a query with a repeated label and a repeated reference, as the unit list does",
			test: testRepeatedFilters,
		},
		{name: "list is ordered by name, then ID", test: testListOrder},
		{name: "list returns an empty list for an owner with no view", test: testListEmpty},
		{name: "a query round trip keeps the order of its labels and references", test: testQueryOrder},
	}
}

func testCreateThenGet(t *testing.T, h Harness) {
	t.Helper()

	v := newView("acme", ownerA, "Payments")
	v.Query = savedview.Query{
		Labels:     []savedview.Label{{Key: labelKeyTeam, Value: labelValuePayments}, {Key: "tier", Value: ""}},
		References: []string{testRef},
	}
	mustCreate(t, h, v)

	got, err := h.Store.Get(t.Context(), "acme", v.ID, ownerA)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	assertSameView(t, got, v)
}

func testCreateRefusesInvalid(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")

	bad := map[string]savedview.View{
		"empty name":      newView("acme", ownerA, ""),
		"hidden in name":  newView("acme", ownerA, "pay\u200bments"),
		"owner subject":   newView("acme", subjectAlice, "Payments"),
		"empty owner":     newView("acme", "", "Payments"),
		"foreign ref":     withRefs(newView("acme", ownerA, "Payments"), "pulumi:other/web/prod"),
		"label with ctrl": withLabel(newView("acme", ownerA, "Payments"), labelKeyTeam, "a\nb"),
	}

	noID := newView("acme", ownerA, "Payments")
	noID.ID = ""
	bad["empty ID"] = noID

	for name := range bad {
		err := h.Store.Create(t.Context(), bad[name], fence, auditEntry("acme", "create"))
		if !errors.Is(err, savedview.ErrInvalid) {
			t.Errorf("%s: create = %v, want ErrInvalid", name, err)
		}
	}

	views, err := h.Store.List(t.Context(), "acme", ownerA)
	if err != nil || len(views) != 0 {
		t.Fatalf("list after refused creates = %v, %v; want none", views, err)
	}
}

func testCreateSameName(t *testing.T, h Harness) {
	t.Helper()

	mustCreate(t, h, newView("acme", ownerA, "Payments"))

	fence := h.Namespace(t, "acme")
	err := h.Store.Create(t.Context(), newView("acme", ownerA, "Payments"), fence, auditEntry("acme", "create"))

	if !errors.Is(err, savedview.ErrExists) {
		t.Fatalf("second create = %v, want ErrExists", err)
	}

	// The name is compared exactly: another case is another name.
	mustCreate(t, h, newView("acme", ownerA, "payments"))
}

func testCreateSameNameOtherOwner(t *testing.T, h Harness) {
	t.Helper()

	mustCreate(t, h, newView("acme", ownerA, "Payments"))
	mustCreate(t, h, newView("acme", ownerB, "Payments"))
}

func testCreateSameNameOtherNamespace(t *testing.T, h Harness) {
	t.Helper()

	mustCreate(t, h, newView("acme", ownerA, "Payments"))
	mustCreate(t, h, newView("beta", ownerA, "Payments"))
}

func testOwnerLimit(t *testing.T, h Harness) {
	t.Helper()

	for i := range savedview.MaxPerOwner {
		mustCreate(t, h, newView("acme", ownerA, fmt.Sprintf("view %03d", i)))
	}

	fence := h.Namespace(t, "acme")
	extra := newView("acme", ownerA, "one more")

	err := h.Store.Create(t.Context(), extra, fence, auditEntry("acme", "create"))
	if !errors.Is(err, savedview.ErrLimit) {
		t.Fatalf("create after %d views = %v, want ErrLimit", savedview.MaxPerOwner, err)
	}

	// The limit is for one owner in one namespace.
	mustCreate(t, h, newView("acme", ownerB, "one more"))
	mustCreate(t, h, newView("beta", ownerA, "one more"))

	// A delete frees a place.
	views, err := h.Store.List(t.Context(), "acme", ownerA)
	if err != nil || len(views) != savedview.MaxPerOwner {
		t.Fatalf("list = %d views, %v; want %d", len(views), err, savedview.MaxPerOwner)
	}

	err = h.Store.Delete(t.Context(), "acme", views[0].ID, ownerA, fence, auditEntry("acme", "delete"))
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	mustCreate(t, h, extra)
}

func testNamespaceCap(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	owners := savedview.MaxPerNamespace / savedview.MaxPerOwner

	// One writer: a store that serializes its writes must not time out.
	for o := range owners {
		for i := range savedview.MaxPerOwner {
			v := newView("acme", ownerN(o), fmt.Sprintf("view %03d", i))

			if err := h.Store.Create(t.Context(), v, fence, auditEntry("acme", "create")); err != nil {
				t.Fatalf("fill the namespace: owner %d view %d: %v", o, i, err)
			}
		}
	}

	fresh := newView("acme", ownerA, "Payments")

	err := h.Store.Create(t.Context(), fresh, fence, auditEntry("acme", "create"))
	if !errors.Is(err, savedview.ErrLimit) {
		t.Fatalf("create at the namespace cap = %v, want ErrLimit", err)
	}

	// Another namespace has its own cap.
	mustCreate(t, h, newView("beta", ownerA, "Payments"))
}

func testRepeatedFilters(t *testing.T, h Harness) {
	t.Helper()

	v := newView("acme", ownerA, "Payments")
	v.Query = savedview.Query{
		Labels: []savedview.Label{
			{Key: labelKeyTeam, Value: labelValuePayments}, {Key: labelKeyTeam, Value: labelValuePayments},
		},
		References: []string{testRef, testRef},
	}
	mustCreate(t, h, v)

	got, err := h.Store.Get(t.Context(), "acme", v.ID, ownerA)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	assertSameView(t, got, v)
}

func testListOrder(t *testing.T, h Harness) {
	t.Helper()

	// Upper case letters sort before lower case ones: the order is by code
	// point, whatever the collation of the database.
	for _, name := range []string{"alpha", "Zeta", "beta", "Beta", "Émile", "10", "9"} {
		mustCreate(t, h, newView("acme", ownerA, name))
	}

	views, err := h.Store.List(t.Context(), "acme", ownerA)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	got := make([]string, 0, len(views))
	for i := range views {
		got = append(got, views[i].Name)
	}

	want := []string{"10", "9", "Beta", "Zeta", "alpha", "beta", "Émile"}
	if !slices.Equal(got, want) {
		t.Fatalf("list order = %q, want %q", got, want)
	}
}

func testListEmpty(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, "acme")

	views, err := h.Store.List(t.Context(), "acme", ownerA)
	if err != nil || len(views) != 0 {
		t.Fatalf("list = %v, %v; want an empty list", views, err)
	}
}

func testQueryOrder(t *testing.T, h Harness) {
	t.Helper()

	v := newView("acme", ownerA, "Payments")
	v.Query = savedview.Query{
		Labels: []savedview.Label{
			{Key: "z", Value: "1"}, {Key: "a", Value: "2"}, {Key: "m", Value: "3"},
		},
		References: []string{"pulumi:acme/c/c", "pulumi:acme/a/a", "opentofu:acme/b"},
	}
	mustCreate(t, h, v)

	got, err := h.Store.Get(t.Context(), "acme", v.ID, ownerA)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	assertSameView(t, got, v)

	// The list route returns the same order as the get route.
	views, err := h.Store.List(t.Context(), "acme", ownerA)
	if err != nil || len(views) != 1 {
		t.Fatalf("list = %v, %v", views, err)
	}

	assertSameView(t, views[0], v)
}

func withRefs(v savedview.View, refs ...string) savedview.View {
	v.Query.References = refs

	return v
}

func withLabel(v savedview.View, key, value string) savedview.View {
	v.Query.Labels = []savedview.Label{{Key: key, Value: value}}

	return v
}

// assertSameView fails when got differs from want. The store keeps the time in
// milliseconds, so the times compare at that precision.
func assertSameView(t *testing.T, got, want savedview.View) {
	t.Helper()

	if got.ID != want.ID || got.Namespace != want.Namespace || got.Name != want.Name || got.Owner != want.Owner ||
		got.CreatedAt.UnixMilli() != want.CreatedAt.UnixMilli() ||
		!slices.Equal(got.Query.Labels, want.Query.Labels) ||
		!slices.Equal(got.Query.References, want.Query.References) {
		t.Fatalf("view = %+v, want %+v", got, want)
	}
}
