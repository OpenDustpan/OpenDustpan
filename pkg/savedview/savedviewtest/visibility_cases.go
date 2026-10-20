package savedviewtest

import (
	"errors"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/savedview"
)

func visibilityCases() []suiteCase {
	return []suiteCase{
		{name: "get of the view of another owner returns not found", test: testGetOtherOwner},
		{
			name: "get of an unknown ID returns the same error as a view of another owner",
			test: testGetUnknownSameError,
		},
		{name: "get of a view in another namespace returns not found", test: testGetOtherNamespace},
		{name: "list returns the views of the owner only", test: testListOwnerOnly},
		{
			name: "a new principal with the same subject gets, lists and deletes none of the views of the old principal ID",
			test: testNewPrincipalSameSubjectSeesNone,
		},
		{name: "delete of the view of another owner returns not found and keeps the view", test: testDeleteOtherOwner},
		{
			name: "a reader whose owner is not a principal ID gets, lists and deletes nothing",
			test: testReaderWithoutPrincipalID,
		},
	}
}

func testGetOtherOwner(t *testing.T, h Harness) {
	t.Helper()

	v := newView("acme", ownerA, "Payments")
	mustCreate(t, h, v)

	_, err := h.Store.Get(t.Context(), "acme", v.ID, ownerB)
	if !errors.Is(err, savedview.ErrNotFound) {
		t.Fatalf("get as another owner = %v, want ErrNotFound", err)
	}
}

func testGetUnknownSameError(t *testing.T, h Harness) {
	t.Helper()

	v := newView("acme", ownerA, "Payments")
	mustCreate(t, h, v)

	_, other := h.Store.Get(t.Context(), "acme", v.ID, ownerB)
	_, unknown := h.Store.Get(t.Context(), "acme", savedview.NewID(), ownerB)

	if !errors.Is(other, savedview.ErrNotFound) || !errors.Is(unknown, savedview.ErrNotFound) {
		t.Fatalf("errors = %v and %v; want ErrNotFound twice", other, unknown)
	}

	// A caller cannot tell the two apart by the text either.
	if other.Error() != unknown.Error() {
		t.Fatalf("texts differ: %q and %q", other, unknown)
	}
}

func testGetOtherNamespace(t *testing.T, h Harness) {
	t.Helper()

	v := newView("acme", ownerA, "Payments")
	mustCreate(t, h, v)
	h.Namespace(t, "beta")

	_, err := h.Store.Get(t.Context(), "beta", v.ID, ownerA)
	if !errors.Is(err, savedview.ErrNotFound) {
		t.Fatalf("get in another namespace = %v, want ErrNotFound", err)
	}

	err = h.Store.Delete(t.Context(), "beta", v.ID, ownerA, h.Namespace(t, "beta"), auditEntry("beta", "delete"))
	if !errors.Is(err, savedview.ErrNotFound) {
		t.Fatalf("delete in another namespace = %v, want ErrNotFound", err)
	}

	if _, err := h.Store.Get(t.Context(), "acme", v.ID, ownerA); err != nil {
		t.Fatalf("view in its own namespace: %v", err)
	}
}

func testListOwnerOnly(t *testing.T, h Harness) {
	t.Helper()

	a1 := newView("acme", ownerA, "One")
	a2 := newView("acme", ownerA, "Two")
	b1 := newView("acme", ownerB, "One")
	other := newView("beta", ownerA, "Three")

	for _, v := range []savedview.View{a1, a2, b1, other} {
		mustCreate(t, h, v)
	}

	views, err := h.Store.List(t.Context(), "acme", ownerA)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	ids := make([]savedview.ID, 0, len(views))
	for _, v := range views {
		ids = append(ids, v.ID)
	}

	if !slices.Equal(ids, []savedview.ID{a1.ID, a2.ID}) {
		t.Fatalf("list as owner A = %v, want %v", ids, []savedview.ID{a1.ID, a2.ID})
	}

	none, err := h.Store.List(t.Context(), "acme", ownerC)
	if err != nil || len(none) != 0 {
		t.Fatalf("list as owner C = %v, %v; want none", none, err)
	}
}

func testDeleteOtherOwner(t *testing.T, h Harness) {
	t.Helper()

	v := newView("acme", ownerA, "Payments")
	mustCreate(t, h, v)

	before := auditCount(t, h, "acme")

	err := h.Store.Delete(t.Context(), "acme", v.ID, ownerB, h.Namespace(t, "acme"), auditEntry("acme", "delete"))
	if !errors.Is(err, savedview.ErrNotFound) {
		t.Fatalf("delete as another owner = %v, want ErrNotFound", err)
	}

	if _, err := h.Store.Get(t.Context(), "acme", v.ID, ownerA); err != nil {
		t.Fatalf("view after a refused delete: %v", err)
	}

	if got := auditCount(t, h, "acme"); got != before {
		t.Fatalf("audit entries = %d, want %d: a refused delete appends none", got, before)
	}
}

// testReaderWithoutPrincipalID asks as readers whose owner is not the ID of a
// principal: a subject, an empty string and text with SQL and LIKE
// characters. The store matches the owner exactly, so none of them reads a
// view.
func testReaderWithoutPrincipalID(t *testing.T, h Harness) {
	t.Helper()

	v := newView("acme", ownerA, "Payments")
	mustCreate(t, h, v)

	fence := h.Namespace(t, "acme")

	for _, reader := range []string{"", "alice", "prn-%", "prn-_aaaaaaaaaaaaaaaaaaa", "' OR '1'='1", ownerA + " ", "PRN-AAAAAAAAAAAAAAAAAAAA"} {
		if _, err := h.Store.Get(t.Context(), "acme", v.ID, reader); !errors.Is(err, savedview.ErrNotFound) {
			t.Errorf("get as %q = %v, want ErrNotFound", reader, err)
		}

		if views, err := h.Store.List(t.Context(), "acme", reader); err != nil || len(views) != 0 {
			t.Errorf("list as %q = %v, %v; want none", reader, views, err)
		}

		err := h.Store.Delete(t.Context(), "acme", v.ID, reader, fence, auditEntry("acme", "delete"))
		if !errors.Is(err, savedview.ErrNotFound) {
			t.Errorf("delete as %q = %v, want ErrNotFound", reader, err)
		}
	}

	if _, err := h.Store.Get(t.Context(), "acme", v.ID, ownerA); err != nil {
		t.Fatalf("view after the hostile readers: %v", err)
	}
}
