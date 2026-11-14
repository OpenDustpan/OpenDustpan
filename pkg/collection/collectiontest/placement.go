package collectiontest

import (
	"errors"
	"slices"
	"strconv"
	"sync"
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
		{name: "a create that leaves a hidden rule is refused", test: testRuleCreateHidden},
		{name: "an update or a move that leaves a hidden rule is refused", test: testRuleUpdateHidden},
		{name: "two writers never store a hidden rule", test: testRuleConcurrentHidden},
		{name: "position 0 means the end for a create and the same place for an update", test: testRulePositionZero},
		{name: "a rule of another namespace than the fence is refused", test: testRuleFenceNamespaceMismatch},
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

	for _, pos := range []int{-1, thirdPlace, 1 << 30} {
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

// testRuleMissingCollection checks that a rule write that names a collection
// the namespace does not hold returns ErrPlacementCollectionMissing, and not
// the plain ErrNotFound of every other missing collection.
func testRuleMissingCollection(t *testing.T, s PlacementStore) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	stored := mustAdd(t, s, ns, ruleSpec{"a", "*", collA}, 0)
	missing := mustName(t, "nowhere")

	tests := []struct {
		name string
		rule collection.Rule
		run  func(r collection.Rule) error
	}{
		{
			name: "create",
			rule: collection.Rule{
				CreatedAt: now(), ID: ident.NewID("plr-"), Namespace: ns.Name, Tool: collection.ToolPulumi,
				Project: "b", Stack: "*", Collection: missing, CreatedBy: testSubject, Position: 1,
			},
			run: func(r collection.Rule) error {
				return s.CreatePlacementRule(t.Context(), r, ns.Fence(), auditFor(ns.Fence()))
			},
		},
		{
			name: "update",
			rule: stored,
			run: func(r collection.Rule) error {
				return s.UpdatePlacementRule(t.Context(), r, ns.Fence(), auditFor(ns.Fence()))
			},
		},
	}

	for i := range tests {
		tt := &tests[i]

		t.Run(tt.name, func(t *testing.T) {
			r := tt.rule
			r.Collection = missing

			err := tt.run(r)
			if !errors.Is(err, collection.ErrPlacementCollectionMissing) {
				t.Errorf("err = %v, want ErrPlacementCollectionMissing", err)
			}

			if errors.Is(err, collection.ErrNotFound) {
				t.Errorf("err = %v matches the plain ErrNotFound too", err)
			}
		})
	}

	wantProjects(t, s, ns.Name, "a")
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
		{"tool without rules", func(r *collection.Rule) { r.Tool = "terraform" }, collection.ErrInvalidPlacementTool},
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
	for _, pos := range []int{-1, 5} {
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

// newRule builds a rule of ns without a store call. The collection must exist.
func newRule(t *testing.T, ns namespace.Name, spec ruleSpec) collection.Rule {
	t.Helper()

	return collection.Rule{
		CreatedAt: now(), ID: ident.NewID("plr-"), Namespace: ns, Tool: collection.ToolPulumi,
		Project: collection.Pattern(spec.project), Stack: collection.Pattern(spec.stack),
		Collection: mustName(t, spec.coll), CreatedBy: testSubject, Position: 0,
	}
}

func testRuleCreateHidden(t *testing.T, s PlacementStore) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	mustAdd(t, s, ns, ruleSpec{"a", "*", collA}, 0)

	for name, spec := range map[string]ruleSpec{
		"narrower after wider": {"a", "x*", collB},
		"same patterns":        {"a", "*", collB},
	} {
		if _, err := addRule(t, s, ns, spec, 0); !errors.Is(err, collection.ErrPlacementConflict) {
			t.Errorf("%s: err = %v, want ErrPlacementConflict", name, err)
		}
	}

	// A wider rule in front of a narrower one hides it.
	mustAdd(t, s, ns, ruleSpec{"b", "x*", collB}, 0)

	if _, err := addRule(t, s, ns, ruleSpec{"b", "*", collA}, 1); !errors.Is(err, collection.ErrPlacementConflict) {
		t.Errorf("wider before narrower: err = %v, want ErrPlacementConflict", err)
	}

	wantProjects(t, s, ns.Name, "a", "b")
}

func testRuleUpdateHidden(t *testing.T, s PlacementStore) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	narrow := mustAdd(t, s, ns, ruleSpec{"a", "x*", collA}, 0)
	wide := mustAdd(t, s, ns, ruleSpec{"a", "*", collB}, 0)

	moved := wide
	moved.Position = 1

	err := s.UpdatePlacementRule(t.Context(), moved, ns.Fence(), auditFor(ns.Fence()))
	if !errors.Is(err, collection.ErrPlacementConflict) {
		t.Errorf("move of the wide rule first: err = %v, want ErrPlacementConflict", err)
	}

	widened := narrow
	widened.Stack = "*"

	err = s.UpdatePlacementRule(t.Context(), widened, ns.Fence(), auditFor(ns.Fence()))
	if !errors.Is(err, collection.ErrPlacementConflict) {
		t.Errorf("widening the first rule: err = %v, want ErrPlacementConflict", err)
	}

	got, err := s.GetPlacementRule(t.Context(), ns.Name, narrow.ID)
	if err != nil || got.Stack != "x*" {
		t.Errorf("rule after the refused update = %+v, %v, want the old stack", got, err)
	}

	wantProjects(t, s, ns.Name, "a", "a")
}

// testRuleConcurrentHidden makes two writers race. The rule "a" / "*" hides
// "a" / "x*" when it comes first. Each writer puts its rule at the end. The
// outcomes that are valid are: "x*" then "*", or "*" alone with the "x*" write
// refused. The list "*" then "x*" is never valid.
func testRuleConcurrentHidden(t *testing.T, s PlacementStore) {
	t.Helper()

	const rounds = 20

	for round := range rounds {
		ns := newNamespace(t, s, "race"+strconv.Itoa(round))
		ensureCollection(t, s, ns, collA)

		rules := []collection.Rule{
			newRule(t, ns.Name, ruleSpec{"a", "*", collA}),
			newRule(t, ns.Name, ruleSpec{"a", "x*", collA}),
		}

		for _, err := range createTogether(t, s, ns, rules) {
			if err != nil && !errors.Is(err, collection.ErrPlacementConflict) {
				t.Fatalf("round %d: unexpected error %v", round, err)
			}
		}

		assertNoHiddenRule(t, s, ns.Name, round)
	}
}

// createTogether creates the rules at the same time, each in its own goroutine,
// and returns the error of each create.
func createTogether(t *testing.T, s PlacementStore, ns namespace.Namespace, rules []collection.Rule) []error {
	t.Helper()

	errs := make([]error, len(rules))

	var wg sync.WaitGroup

	for i := range rules {
		wg.Go(func() {
			errs[i] = s.CreatePlacementRule(t.Context(), rules[i], ns.Fence(), auditFor(ns.Fence()))
		})
	}

	wg.Wait()

	return errs
}

// assertNoHiddenRule fails the test when the store holds a rule that another
// rule hides.
func assertNoHiddenRule(t *testing.T, s PlacementStore, ns namespace.Name, round int) {
	t.Helper()

	list, err := s.ListPlacementRules(t.Context(), ns)
	if err != nil {
		t.Fatalf("ListPlacementRules: %v", err)
	}

	for i := range list {
		if checkErr := collection.CheckPlacementOrder(list, list[i].ID); checkErr != nil {
			t.Fatalf("round %d: the store holds a hidden rule: %v", round, checkErr)
		}
	}
}

func testRulePositionZero(t *testing.T, s PlacementStore) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	first := mustAdd(t, s, ns, ruleSpec{"a", "*", collA}, 0)
	mustAdd(t, s, ns, ruleSpec{"b", "*", collB}, 0)
	third := newRule(t, ns.Name, ruleSpec{"c", "*", collA})

	if err := s.CreatePlacementRule(t.Context(), third, ns.Fence(), auditFor(ns.Fence())); err != nil {
		t.Fatalf("create at position 0: %v", err)
	}

	wantProjects(t, s, ns.Name, "a", "b", "c")

	first.Position = 0
	first.Collection = mustName(t, collB)

	if err := s.UpdatePlacementRule(t.Context(), first, ns.Fence(), auditFor(ns.Fence())); err != nil {
		t.Fatalf("update at position 0: %v", err)
	}

	wantProjects(t, s, ns.Name, "a", "b", "c")
}

// testRuleFenceNamespaceMismatch sends a rule of one namespace under the fence
// of another. The store must refuse each write and change nothing in either
// namespace. Defense in depth: the service reads the fence of the namespace that
// it writes, so this guard is the last layer.
func testRuleFenceNamespaceMismatch(t *testing.T, s PlacementStore) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	other := newNamespace(t, s, "globex")
	own := mustAdd(t, s, ns, ruleSpec{"a", "*", collA}, 0)
	foreign := mustAdd(t, s, other, ruleSpec{"x", "*", collB}, 0)

	// Create: the rule names globex, the fence names acme.
	stray := newRule(t, other.Name, ruleSpec{"y", "*", collB})

	err := s.CreatePlacementRule(t.Context(), stray, ns.Fence(), auditFor(ns.Fence()))
	if err == nil {
		t.Error("create of a rule of another namespace succeeded")
	}

	// Update: the rule is in globex, the fence names acme.
	changed := foreign
	changed.Project = "changed"

	err = s.UpdatePlacementRule(t.Context(), changed, ns.Fence(), auditFor(ns.Fence()))
	if !errors.Is(err, collection.ErrPlacementRuleNotFound) {
		t.Errorf("update under the wrong fence: err = %v, want ErrPlacementRuleNotFound", err)
	}

	// Delete: the rule is in globex, the fence names acme.
	err = s.DeletePlacementRule(t.Context(), other.Name, foreign.ID, ns.Fence(), auditFor(ns.Fence()))
	if !errors.Is(err, collection.ErrPlacementRuleNotFound) {
		t.Errorf("delete under the wrong fence: err = %v, want ErrPlacementRuleNotFound", err)
	}

	wantProjects(t, s, ns.Name, own.Project.String())
	wantProjects(t, s, other.Name, "x")
}
