package trustruletest

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// readCases are the cases of Get and List.
func readCases() []suiteCase {
	return []suiteCase{
		{"CreateThenGetGivesTheSameRule", testCreateThenGet},
		{"CreateThenGetGivesTheSameStaticRule", testCreateThenGetStatic},
		{"ListOrdersByNameThenID", testListOrder},
		{"ListKeepsNamespacesApart", testListNamespaces},
		{"ListOfAnEmptyNamespaceIsEmpty", testListEmpty},
		{"GetOfAnUnknownRuleIsNotFound", testGetUnknown},
		{"GetOfARuleOfAnotherNamespaceIsNotFound", testGetOtherNamespace},
		{"GetOfAnInvalidIDIsInvalid", testGetInvalidID},
	}
}

func testCreateThenGet(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, nsAcme)
	r := newRule(nsAcme, "deploy from main", workload(t, h, nsAcme))
	mustCreate(t, h, r)

	sameRule(t, mustGet(t, h, r.ID), r)
}

func testCreateThenGetStatic(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, nsAcme)
	r := newStaticRule(t, nsAcme, "cluster jobs", workload(t, h, nsAcme))
	mustCreate(t, h, r)

	sameRule(t, mustGet(t, h, r.ID), r)
}

func testListOrder(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, nsAcme)
	p := workload(t, h, nsAcme)

	b := newRule(nsAcme, "b", p)
	a := newRule(nsAcme, "a", p)
	upper := newRule(nsAcme, "B", p)

	mustCreate(t, h, b)
	mustCreate(t, h, a)
	mustCreate(t, h, upper)

	got, err := h.Store.List(t.Context(), nsAcme)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	// The order compares bytes: "B" comes before "a" and "b".
	want := []trustrule.ID{upper.ID, a.ID, b.ID}
	if len(got) != len(want) {
		t.Fatalf("list = %d rules, want %d", len(got), len(want))
	}

	for i := range want {
		if got[i].ID != want[i] {
			t.Errorf("list[%d] = %s %q, want %s", i, got[i].ID, got[i].Name, want[i])
		}
	}
}

func testListNamespaces(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, nsAcme)
	h.Namespace(t, nsOther)

	mine := newRule(nsAcme, "deploy", workload(t, h, nsAcme))
	theirs := newRule(nsOther, "deploy", workload(t, h, nsOther))

	mustCreate(t, h, mine)
	mustCreate(t, h, theirs)

	got, err := h.Store.List(t.Context(), nsAcme)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(got) != 1 || got[0].ID != mine.ID {
		t.Fatalf("list of %s = %+v, want only %s", nsAcme, got, mine.ID)
	}
}

func testListEmpty(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, nsAcme)

	got, err := h.Store.List(t.Context(), nsAcme)
	if err != nil || len(got) != 0 {
		t.Fatalf("list = %v, %v; want an empty list", got, err)
	}
}

func testGetUnknown(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, nsAcme)
	mustBeGone(t, h, nsAcme, trustrule.NewID())
}

func testGetOtherNamespace(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, nsAcme)
	h.Namespace(t, nsOther)

	r := newRule(nsAcme, "deploy", workload(t, h, nsAcme))
	mustCreate(t, h, r)

	mustBeGone(t, h, nsOther, r.ID)
}

func testGetInvalidID(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, nsAcme)

	for _, id := range []trustrule.ID{"", "trr-", "tok-abc", "trr-a b", "trr-../x"} {
		if _, err := h.Store.Get(t.Context(), nsAcme, id); !errors.Is(err, trustrule.ErrInvalid) {
			t.Errorf("get %q = %v, want ErrInvalid", id, err)
		}
	}

	if _, err := h.Store.Get(t.Context(), namespace.Name("Not A Name"), trustrule.NewID()); !errors.Is(
		err, trustrule.ErrInvalid,
	) {
		t.Errorf("get in an invalid namespace = %v, want ErrInvalid", err)
	}
}
