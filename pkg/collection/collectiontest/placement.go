package collectiontest

import (
	"errors"
	"slices"
	"strconv"
	"testing"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// PlacementStore is every persistence port that RunPlacement exercises.
type PlacementStore interface {
	Store
	collection.PlacementStore
}

// PlacementFactory returns a fresh, empty store for one subtest.
type PlacementFactory func(t *testing.T) PlacementStore

// RunPlacement runs every case of the placement rule suite against the stores
// that newStore returns. A store that implements collection.PlacementStore
// should run it next to Run.
func RunPlacement(t *testing.T, newStore PlacementFactory) {
	t.Helper()

	cases := []struct {
		name string
		test func(t *testing.T, s PlacementStore)
	}{
		{name: "a new rule goes to the end and reads back", test: testRuleCreateAndGet},
		{name: "a rule inserted at a position moves the later rules down", test: testRuleInsert},
		{name: "a position outside the list is refused", test: testRuleInvalidPosition},
		{name: "a rule for a missing collection is refused", test: testRuleMissingCollection},
		{name: "a collection name in another case finds the collection", test: testRuleCollectionCase},
		{name: "an invalid rule is refused whatever the caller checked", test: testRuleInvalid},
		{name: "a stale fence refuses every rule write", test: testRuleFenced},
		{name: "an update replaces the content and keeps the creator", test: testRuleUpdate},
		{name: "an update moves a rule and the others keep their order", test: testRuleMove},
		{name: "an update of a missing rule is not found", test: testRuleUpdateMissing},
		{name: "a delete closes the gap in the positions", test: testRuleDelete},
		{name: "a delete of a missing rule is not found", test: testRuleDeleteMissing},
		{name: "a rule of another namespace is not found", test: testRuleOtherNamespace},
		{name: "the rules of one namespace are listed alone", test: testRuleListIsolated},
		{name: "a namespace holds at most the limit of rules", test: testRuleLimit},
		{name: "a failed audit write leaves the rules as they were", test: testRuleAuditRollsBack},
		{name: "a deleted collection takes its rules with it", test: testRuleCollectionDelete},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// Names that the placement cases use more than once.
const (
	collA        = "coll-a"
	collB        = "coll-b"
	collPayments = "payments"
	testSubject  = "tester"
	toolOpenTofu = "opentofu"
	// thirdPlace and the like name positions of the list.
	thirdPlace = 3
)

// ruleSpec is the content of a rule that a test makes.
type ruleSpec struct {
	project, stack, coll string
}

// addRule makes the collection of spec when it does not exist, then inserts a
// rule at position pos. Position 0 means the end of the list.
func addRule(
	t *testing.T, s PlacementStore, ns namespace.Namespace, spec ruleSpec, pos int,
) (collection.Rule, error) {
	t.Helper()

	ensureCollection(t, s, ns, spec.coll)

	list, err := s.ListPlacementRules(t.Context(), ns.Name)
	if err != nil {
		t.Fatalf("ListPlacementRules: %v", err)
	}

	if pos == 0 {
		pos = len(list) + 1
	}

	r := collection.Rule{
		CreatedAt:  now(),
		ID:         ident.NewID("plr-"),
		Namespace:  ns.Name,
		Tool:       collection.ToolPulumi,
		Project:    collection.Pattern(spec.project),
		Stack:      collection.Pattern(spec.stack),
		Collection: mustName(t, spec.coll),
		CreatedBy:  testSubject,
		Position:   pos,
	}

	return r, s.CreatePlacementRule(t.Context(), r, ns.Fence(), auditFor(ns.Fence()))
}

// mustAdd is addRule for a rule that must be accepted.
func mustAdd(t *testing.T, s PlacementStore, ns namespace.Namespace, spec ruleSpec, pos int) collection.Rule {
	t.Helper()

	r, err := addRule(t, s, ns, spec, pos)
	if err != nil {
		t.Fatalf("CreatePlacementRule %v at %d: %v", spec, pos, err)
	}

	return r
}

// ensureCollection makes the collection name in ns unless it exists.
func ensureCollection(t *testing.T, s PlacementStore, ns namespace.Namespace, name string) {
	t.Helper()

	err := createColl(t, s, newCollection(t, ns.Name, name), ns.Fence())
	if err != nil && !errors.Is(err, collection.ErrAlreadyExists) {
		t.Fatalf("create collection %q: %v", name, err)
	}
}

// ruleProjects returns the project pattern of each rule in list order.
func ruleProjects(t *testing.T, s PlacementStore, ns namespace.Name) []string {
	t.Helper()

	list, err := s.ListPlacementRules(t.Context(), ns)
	if err != nil {
		t.Fatalf("ListPlacementRules: %v", err)
	}

	out := make([]string, 0, len(list))

	for i := range list {
		if list[i].Position != i+1 {
			t.Errorf("rule %s has position %d, want %d", list[i].Project, list[i].Position, i+1)
		}

		out = append(out, list[i].Project.String())
	}

	return out
}

func wantProjects(t *testing.T, s PlacementStore, ns namespace.Name, want ...string) {
	t.Helper()

	if got := ruleProjects(t, s, ns); !slices.Equal(got, want) {
		t.Errorf("projects in list order = %v, want %v", got, want)
	}
}

func testRuleCreateAndGet(t *testing.T, s PlacementStore) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	first := mustAdd(t, s, ns, ruleSpec{collPayments, "prod*", "payments-prod"}, 0)
	second := mustAdd(t, s, ns, ruleSpec{collPayments, "*", collPayments}, 0)

	if first.Position != 1 || second.Position != 2 {
		t.Fatalf("positions = %d, %d, want 1, 2", first.Position, second.Position)
	}

	got, err := s.GetPlacementRule(t.Context(), ns.Name, first.ID)
	if err != nil {
		t.Fatalf("GetPlacementRule: %v", err)
	}

	if !got.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, first.CreatedAt)
	}

	got.CreatedAt = first.CreatedAt
	if got != first {
		t.Errorf("rule = %+v, want %+v", got, first)
	}

	wantProjects(t, s, ns.Name, "payments", "payments")
}

func testRuleInsert(t *testing.T, s PlacementStore) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	mustAdd(t, s, ns, ruleSpec{"a", "*", collA}, 0)
	mustAdd(t, s, ns, ruleSpec{"b", "*", collB}, 0)
	mustAdd(t, s, ns, ruleSpec{"c", "*", "coll-c"}, 1)
	mustAdd(t, s, ns, ruleSpec{"d", "*", "coll-d"}, thirdPlace)

	wantProjects(t, s, ns.Name, "c", "a", "d", "b")
}

func testRuleInvalidPosition(t *testing.T, s PlacementStore) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	mustAdd(t, s, ns, ruleSpec{"a", "*", collA}, 0)

	for _, pos := range []int{0, -1, 3, 1 << 30} {
		ensureCollection(t, s, ns, collB)

		r := collection.Rule{
			CreatedAt: now(), ID: ident.NewID("plr-"), Namespace: ns.Name, Tool: collection.ToolPulumi,
			Project: "b", Stack: "*", Collection: mustName(t, collB), CreatedBy: testSubject, Position: pos,
		}

		err := s.CreatePlacementRule(t.Context(), r, ns.Fence(), auditFor(ns.Fence()))
		if !errors.Is(err, collection.ErrInvalidPosition) {
			t.Errorf("create at %d: err = %v, want ErrInvalidPosition", pos, err)
		}
	}

	wantProjects(t, s, ns.Name, "a")
}

func testRuleMissingCollection(t *testing.T, s PlacementStore) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	r := collection.Rule{
		CreatedAt: now(), ID: ident.NewID("plr-"), Namespace: ns.Name, Tool: collection.ToolPulumi,
		Project: "a", Stack: "*", Collection: mustName(t, "nowhere"), CreatedBy: testSubject, Position: 1,
	}

	err := s.CreatePlacementRule(t.Context(), r, ns.Fence(), auditFor(ns.Fence()))
	if !errors.Is(err, collection.ErrNotFound) {
		t.Errorf("err = %v, want collection.ErrNotFound", err)
	}

	wantProjects(t, s, ns.Name)
}

func testRuleCollectionCase(t *testing.T, s PlacementStore) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	ensureCollection(t, s, ns, "Payments")

	r := mustAdd(t, s, ns, ruleSpec{"payments", "*", "PAYMENTS"}, 0)

	got, err := s.GetPlacementRule(t.Context(), ns.Name, r.ID)
	if err != nil {
		t.Fatalf("GetPlacementRule: %v", err)
	}

	if got.Collection.String() != "Payments" {
		t.Errorf("collection = %q, want the name as the collection was first written", got.Collection)
	}
}

// testRuleInvalid sends content that ParsePattern and ParseName refuse
// directly to the store, as if every earlier check had failed.
func testRuleInvalid(t *testing.T, s PlacementStore) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	ensureCollection(t, s, ns, collPayments)

	tests := []struct {
		name string
		edit func(r *collection.Rule)
		want error
	}{
		{"regexp in the project", func(r *collection.Rule) { r.Project = "(a+)+$" }, collection.ErrInvalidPattern},
		{"empty stack", func(r *collection.Rule) { r.Stack = "" }, collection.ErrInvalidPattern},
		{"two stars", func(r *collection.Rule) { r.Stack = "a**b" }, collection.ErrInvalidPattern},
		{"too many stars", func(r *collection.Rule) { r.Stack = "*a*b*c*" }, collection.ErrInvalidPattern},
		{"slash", func(r *collection.Rule) { r.Project = "a/b" }, collection.ErrInvalidPattern},
		{"line break", func(r *collection.Rule) { r.Stack = "a\nb" }, collection.ErrInvalidPattern},
		{"null byte", func(r *collection.Rule) { r.Stack = "a\x00" }, collection.ErrInvalidPattern},
		{"too long", func(r *collection.Rule) {
			r.Project = collection.Pattern(string(make([]byte, collection.MaxPatternLength+1)))
		}, collection.ErrInvalidPattern},
		{"tool without rules", func(r *collection.Rule) { r.Tool = toolOpenTofu }, collection.ErrInvalidPlacementTool},
		{"invalid collection name", func(r *collection.Rule) { r.Collection = "a" }, collection.ErrInvalidName},
	}

	for _, tt := range tests {
		r := collection.Rule{
			CreatedAt: now(), ID: ident.NewID("plr-"), Namespace: ns.Name, Tool: collection.ToolPulumi,
			Project: "a", Stack: "*", Collection: mustName(t, collPayments), CreatedBy: testSubject, Position: 1,
		}
		tt.edit(&r)

		err := s.CreatePlacementRule(t.Context(), r, ns.Fence(), auditFor(ns.Fence()))
		if !errors.Is(err, tt.want) {
			t.Errorf("%s: err = %v, want %v", tt.name, err, tt.want)
		}
	}

	wantProjects(t, s, ns.Name)
}

func testRuleFenced(t *testing.T, s PlacementStore) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	r := mustAdd(t, s, ns, ruleSpec{"a", "*", collA}, 0)
	stale := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion + 1}

	other := r
	other.ID = ident.NewID("plr-")
	other.Position = 2

	err := s.CreatePlacementRule(t.Context(), other, stale, auditFor(stale))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("create: err = %v, want ErrFenced", err)
	}

	r.Project = "changed"

	err = s.UpdatePlacementRule(t.Context(), r, stale, auditFor(stale))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("update: err = %v, want ErrFenced", err)
	}

	err = s.DeletePlacementRule(t.Context(), ns.Name, r.ID, stale, auditFor(stale))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("delete: err = %v, want ErrFenced", err)
	}

	wantProjects(t, s, ns.Name, "a")
}

func testRuleUpdate(t *testing.T, s PlacementStore) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	r := mustAdd(t, s, ns, ruleSpec{"a", "*", collA}, 0)
	ensureCollection(t, s, ns, collB)

	changed := r
	changed.Project, changed.Stack, changed.Collection = "b", "prod-*", mustName(t, collB)
	changed.CreatedBy, changed.CreatedAt = "someone else", now().Add(1)

	if err := s.UpdatePlacementRule(t.Context(), changed, ns.Fence(), auditFor(ns.Fence())); err != nil {
		t.Fatalf("UpdatePlacementRule: %v", err)
	}

	got, err := s.GetPlacementRule(t.Context(), ns.Name, r.ID)
	if err != nil {
		t.Fatalf("GetPlacementRule: %v", err)
	}

	if got.Project != "b" || got.Stack != "prod-*" || got.Collection.String() != collB {
		t.Errorf("content = %+v, want the changed content", got)
	}

	if got.CreatedBy != testSubject || !got.CreatedAt.Equal(r.CreatedAt) {
		t.Errorf("creator = %q at %v, want %q at %v", got.CreatedBy, got.CreatedAt, testSubject, r.CreatedAt)
	}
}

func testRuleMove(t *testing.T, s PlacementStore) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	ids := map[string]collection.Rule{}

	for _, p := range []string{"a", "b", "c", "d"} {
		ids[p] = mustAdd(t, s, ns, ruleSpec{p, "*", "coll-" + p}, 0)
	}

	move := func(p string, pos int) {
		t.Helper()

		r := ids[p]
		r.Position = pos

		if err := s.UpdatePlacementRule(t.Context(), r, ns.Fence(), auditFor(ns.Fence())); err != nil {
			t.Fatalf("move %s to %d: %v", p, pos, err)
		}
	}

	move("a", thirdPlace)
	wantProjects(t, s, ns.Name, "b", "c", "a", "d")
	move("d", 1)
	wantProjects(t, s, ns.Name, "d", "b", "c", "a")
	move("c", thirdPlace)
	wantProjects(t, s, ns.Name, "d", "b", "c", "a")

	bad := ids["a"]
	for _, pos := range []int{0, -1, 5} {
		bad.Position = pos

		err := s.UpdatePlacementRule(t.Context(), bad, ns.Fence(), auditFor(ns.Fence()))
		if !errors.Is(err, collection.ErrInvalidPosition) {
			t.Errorf("move to %d: err = %v, want ErrInvalidPosition", pos, err)
		}
	}

	wantProjects(t, s, ns.Name, "d", "b", "c", "a")
}

func testRuleUpdateMissing(t *testing.T, s PlacementStore) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	r := mustAdd(t, s, ns, ruleSpec{"a", "*", collA}, 0)
	r.ID = ident.NewID("plr-")

	err := s.UpdatePlacementRule(t.Context(), r, ns.Fence(), auditFor(ns.Fence()))
	if !errors.Is(err, collection.ErrPlacementRuleNotFound) {
		t.Errorf("err = %v, want ErrPlacementRuleNotFound", err)
	}
}

func testRuleDelete(t *testing.T, s PlacementStore) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	mustAdd(t, s, ns, ruleSpec{"a", "*", collA}, 0)
	b := mustAdd(t, s, ns, ruleSpec{"b", "*", collB}, 0)
	mustAdd(t, s, ns, ruleSpec{"c", "*", "coll-c"}, 0)

	if err := s.DeletePlacementRule(t.Context(), ns.Name, b.ID, ns.Fence(), auditFor(ns.Fence())); err != nil {
		t.Fatalf("DeletePlacementRule: %v", err)
	}

	wantProjects(t, s, ns.Name, "a", "c")

	_, err := s.GetPlacementRule(t.Context(), ns.Name, b.ID)
	if !errors.Is(err, collection.ErrPlacementRuleNotFound) {
		t.Errorf("get after delete: err = %v, want ErrPlacementRuleNotFound", err)
	}
}

func testRuleDeleteMissing(t *testing.T, s PlacementStore) {
	t.Helper()

	ns := newNamespace(t, s, "acme")

	err := s.DeletePlacementRule(t.Context(), ns.Name, ident.NewID("plr-"), ns.Fence(), auditFor(ns.Fence()))
	if !errors.Is(err, collection.ErrPlacementRuleNotFound) {
		t.Errorf("err = %v, want ErrPlacementRuleNotFound", err)
	}
}

func testRuleOtherNamespace(t *testing.T, s PlacementStore) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	other := newNamespace(t, s, "globex")
	r := mustAdd(t, s, ns, ruleSpec{"a", "*", collA}, 0)

	_, err := s.GetPlacementRule(t.Context(), other.Name, r.ID)
	if !errors.Is(err, collection.ErrPlacementRuleNotFound) {
		t.Errorf("get: err = %v, want ErrPlacementRuleNotFound", err)
	}

	err = s.DeletePlacementRule(t.Context(), other.Name, r.ID, other.Fence(), auditFor(other.Fence()))
	if !errors.Is(err, collection.ErrPlacementRuleNotFound) {
		t.Errorf("delete: err = %v, want ErrPlacementRuleNotFound", err)
	}

	moved := r
	moved.Namespace = other.Name

	err = s.UpdatePlacementRule(t.Context(), moved, other.Fence(), auditFor(other.Fence()))
	if !errors.Is(err, collection.ErrPlacementRuleNotFound) {
		t.Errorf("update: err = %v, want ErrPlacementRuleNotFound", err)
	}

	wantProjects(t, s, ns.Name, "a")
}

func testRuleListIsolated(t *testing.T, s PlacementStore) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	other := newNamespace(t, s, "globex")

	mustAdd(t, s, ns, ruleSpec{"a", "*", collA}, 0)
	mustAdd(t, s, other, ruleSpec{"x", "*", "coll-x"}, 0)
	mustAdd(t, s, other, ruleSpec{"y", "*", "coll-y"}, 0)

	wantProjects(t, s, ns.Name, "a")
	wantProjects(t, s, other.Name, "x", "y")
	wantProjects(t, s, "nobody")
}

func testRuleLimit(t *testing.T, s PlacementStore) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	ensureCollection(t, s, ns, collPayments)

	for i := range collection.MaxPlacementRules {
		mustAdd(t, s, ns, ruleSpec{"p" + strconv.Itoa(i), "*", collPayments}, 0)
	}

	_, err := addRule(t, s, ns, ruleSpec{"over", "*", collPayments}, 0)
	if !errors.Is(err, collection.ErrTooManyPlacementRules) {
		t.Errorf("err = %v, want ErrTooManyPlacementRules", err)
	}

	if got := len(ruleProjects(t, s, ns.Name)); got != collection.MaxPlacementRules {
		t.Errorf("rules = %d, want %d", got, collection.MaxPlacementRules)
	}
}

func testRuleAuditRollsBack(t *testing.T, s PlacementStore) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	a := mustAdd(t, s, ns, ruleSpec{"a", "*", collA}, 0)
	b := mustAdd(t, s, ns, ruleSpec{"b", "*", collB}, 0)

	bad := auditFor(ns.Fence())
	bad.Action = ""

	extra := a
	extra.ID, extra.Project, extra.Position = ident.NewID("plr-"), "c", 1

	if err := s.CreatePlacementRule(t.Context(), extra, ns.Fence(), bad); err == nil {
		t.Error("create with an invalid audit entry succeeded")
	}

	b.Position = 1
	if err := s.UpdatePlacementRule(t.Context(), b, ns.Fence(), bad); err == nil {
		t.Error("update with an invalid audit entry succeeded")
	}

	if err := s.DeletePlacementRule(t.Context(), ns.Name, a.ID, ns.Fence(), bad); err == nil {
		t.Error("delete with an invalid audit entry succeeded")
	}

	wantProjects(t, s, ns.Name, "a", "b")
}

func testRuleCollectionDelete(t *testing.T, s PlacementStore) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	mustAdd(t, s, ns, ruleSpec{"a", "*", collA}, 0)
	mustAdd(t, s, ns, ruleSpec{"b", "*", collB}, 0)
	mustAdd(t, s, ns, ruleSpec{"c", "*", "COLL-A"}, 0)
	mustAdd(t, s, ns, ruleSpec{"d", "*", "coll-d"}, 0)

	ref := collection.Ref{Namespace: ns.Name, Name: mustName(t, collA)}
	if err := deleteColl(t, s, ref, ns.Fence()); err != nil {
		t.Fatalf("DeleteCollection: %v", err)
	}

	wantProjects(t, s, ns.Name, "b", "d")
}
