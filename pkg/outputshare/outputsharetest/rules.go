package outputsharetest

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
)

// Values of the cases of the rules.
const (
	// ruleAction is the action of the audit entry of a rule write.
	ruleAction = "unit:manage-sharing"
	// kindUnit, kindCollection and kindNamespace are the kinds of object that
	// the rule table stores.
	kindUnit       = "unit"
	kindCollection = "collection"
	kindNamespace  = "namespace"
	// webName is the name of the unit that the cases of another namespace use.
	webName = "web"
	// crossRules is the number of rules of the case of the consumers of another
	// namespace.
	crossRules = 4
)

// ruleRig is the state of one case of the rules: a harness, the namespace of
// the producers and a counter for the IDs of the rules.
type ruleRig struct {
	t  *testing.T
	h  Harness
	ns namespace.Name
	n  int
}

// newRuleRig makes the namespace acme and returns the rig of the case.
func newRuleRig(t *testing.T, h Harness) *ruleRig {
	t.Helper()

	h.Namespace(t, acme.String())

	return &ruleRig{t: t, h: h, ns: acme, n: 0}
}

// fence returns the current fence of the namespace of the producers.
func (r *ruleRig) fence() namespace.Fence {
	r.t.Helper()

	return r.h.Namespace(r.t, r.ns.String())
}

// unit makes a workspace in the namespace of the producers, in the collection
// when it is not empty.
func (r *ruleRig) unit(name, collection string) outputshare.Placement {
	r.t.Helper()

	return r.h.Unit(r.t, ToolOpenTofu, r.ns, name, collection)
}

// other makes a workspace in the namespace globex.
func (r *ruleRig) other(name, collection string) outputshare.Placement {
	r.t.Helper()

	r.h.Namespace(r.t, globex.String())

	return r.h.Unit(r.t, ToolOpenTofu, globex, name, collection)
}

// rule returns a rule with a new ID.
func (r *ruleRig) rule(producer, consumer identity.Object) outputshare.Rule {
	r.t.Helper()

	r.n++

	return r.ruleWithID(fmt.Sprintf("rule-%03d", r.n), producer, consumer)
}

// ruleWithID returns a rule with the ID id.
func (r *ruleRig) ruleWithID(id string, producer, consumer identity.Object) outputshare.Rule {
	r.t.Helper()

	return outputshare.Rule{
		CreatedAt: time.Time{}, ID: outputshare.ID(id), CreatedBy: tester, Producer: producer, Consumer: consumer,
	}
}

// entry returns the audit entry of the write of the rule.
func ruleEntry(fence namespace.Fence, rule outputshare.Rule) audit.Entry {
	e := settingEntry(fence)
	e.Action = ruleAction
	e.Object = rule.Producer.String()
	e.ObjectID = string(rule.ID)

	return e
}

// put saves the rule with the current fence.
func (r *ruleRig) put(rule outputshare.Rule) error {
	r.t.Helper()

	fence := r.fence()

	return r.h.Store.Put(r.t.Context(), rule, fence, ruleEntry(fence, rule))
}

// mustPut saves the rule and fails the test on an error.
func (r *ruleRig) mustPut(rule outputshare.Rule) outputshare.Rule {
	r.t.Helper()

	if err := r.put(rule); err != nil {
		r.t.Fatalf("Put(%s on %s): %v", rule.ID, rule.Producer, err)
	}

	return rule
}

// link saves a rule that names the producer and the consumer and returns it.
func (r *ruleRig) link(producer, consumer identity.Object) outputshare.Rule {
	r.t.Helper()

	return r.mustPut(r.rule(producer, consumer))
}

// list returns the rules of the producer.
func (r *ruleRig) list(producer identity.Object) []outputshare.Rule {
	r.t.Helper()

	rules, err := r.h.Store.List(r.t.Context(), producer)
	if err != nil {
		r.t.Fatalf("List(%s): %v", producer, err)
	}

	return rules
}

// wantIDs fails the test unless the rules of the producer have the IDs, in
// this order.
func (r *ruleRig) wantIDs(producer identity.Object, ids ...string) {
	r.t.Helper()

	got := make([]string, 0, len(ids))

	rules := r.list(producer)

	for i := range rules {
		got = append(got, string(rules[i].ID))
	}

	if !slices.Equal(got, ids) {
		r.t.Fatalf("List(%s) = %q, want %q", producer, got, ids)
	}
}

// wantPutErr fails the test unless Put of the rule fails with want and the
// store stays as it was: the producer keeps the same rules and, when the
// adapter reads the audit entries, the namespace gets no entry.
func (r *ruleRig) wantPutErr(rule outputshare.Rule, want error) {
	r.t.Helper()

	audited := r.auditCount()
	// A producer that is not valid has no list. The check of the rules of the
	// producer then has nothing to compare.
	before, listErr := r.h.Store.List(r.t.Context(), rule.Producer)

	err := r.put(rule)
	if !errors.Is(err, want) {
		r.t.Fatalf("Put(%s on %s) = %v, want %v", rule.ID, rule.Producer, err, want)
	}

	if listErr == nil {
		if after := r.list(rule.Producer); len(after) != len(before) {
			r.t.Fatalf(
				"a refused Put changed the rules of %s: %d before, %d after",
				rule.Producer,
				len(before),
				len(after),
			)
		}
	}

	if got := r.auditCount(); got != audited {
		r.t.Fatalf("a refused Put wrote %d audit entries", got-audited)
	}
}

// auditCount returns the number of audit entries of the namespace of the
// producers, or 0 when the adapter has no hook that reads them.
func (r *ruleRig) auditCount() int {
	r.t.Helper()

	if r.h.Audited == nil {
		return 0
	}

	return len(r.h.Audited(r.t, r.ns.String()))
}

// find returns the rule that Find gives for the placements.
func (r *ruleRig) find(producer, consumer outputshare.Placement) (outputshare.Rule, error) {
	r.t.Helper()

	return r.h.Store.Find(r.t.Context(), producer, consumer)
}

// wantFind fails the test unless Find gives the rule id.
func (r *ruleRig) wantFind(producer, consumer outputshare.Placement, id outputshare.ID) {
	r.t.Helper()

	got, err := r.find(producer, consumer)
	if err != nil {
		r.t.Fatalf("Find(%s, %s): %v", producer.Unit, consumer.Unit, err)
	}

	if got.ID != id {
		r.t.Fatalf("Find(%s, %s) = %s, want %s", producer.Unit, consumer.Unit, got.ID, id)
	}
}

// wantNoRule fails the test unless Find gives ErrNoRule.
func (r *ruleRig) wantNoRule(producer, consumer outputshare.Placement) {
	r.t.Helper()

	if _, err := r.find(producer, consumer); !errors.Is(err, outputshare.ErrNoRule) {
		r.t.Fatalf("Find(%s, %s) = %v, want ErrNoRule", producer.Unit, consumer.Unit, err)
	}
}

// del removes the rule with the current fence.
func (r *ruleRig) del(producer identity.Object, id outputshare.ID) error {
	r.t.Helper()

	fence := r.fence()
	e := ruleEntry(fence, outputshare.Rule{
		CreatedAt: time.Time{}, ID: id, CreatedBy: "", Producer: producer, Consumer: identity.Object{},
	})

	return r.h.Store.Delete(r.t.Context(), producer, id, fence, e)
}

// wantHidden fails the test unless Get and Delete of the rule id by the
// producer answer ErrNotFound and the rule stays on its own producer.
func (r *ruleRig) wantHidden(producer identity.Object, id outputshare.ID, owner identity.Object) {
	r.t.Helper()

	if _, err := r.h.Store.Get(r.t.Context(), producer, id); !errors.Is(err, outputshare.ErrNotFound) {
		r.t.Fatalf("Get(%s, %s) = %v, want ErrNotFound", producer, id, err)
	}

	if err := r.del(producer, id); !errors.Is(err, outputshare.ErrNotFound) {
		r.t.Fatalf("Delete(%s, %s) = %v, want ErrNotFound", producer, id, err)
	}

	if _, err := r.h.Store.Get(r.t.Context(), owner, id); err != nil {
		r.t.Fatalf("Get(%s, %s) after a refused Delete: %v", owner, id, err)
	}
}

// collectionOf returns the collection of the placement.
func collectionOf(p outputshare.Placement) identity.Object {
	return p.Collection
}

// emptyRule returns a rule with no field set.
func emptyRule() *outputshare.Rule {
	return &outputshare.Rule{
		CreatedAt: time.Time{}, ID: "", CreatedBy: "", Producer: identity.Object{}, Consumer: identity.Object{},
	}
}

// nsObject returns the object of the namespace ns.
func nsObject(ns namespace.Name) identity.Object {
	return identity.NamespaceObject(ns)
}

// withRuleStore wraps each case so that it skips when the harness has no
// rule store or no hook that makes units.
func withRuleStore(cases []suiteCase) []suiteCase {
	out := make([]suiteCase, 0, len(cases))

	for _, tc := range cases {
		if tc.test == nil {
			out = append(out, tc)

			continue
		}

		run := tc.test

		out = append(out, suiteCase{name: tc.name, test: func(t *testing.T, h Harness) {
			t.Helper()

			if h.Store == nil || h.Unit == nil {
				t.Skip("the harness has no rule store or no hook that makes units")
			}

			run(t, h)
		}})
	}

	return out
}

// needHook skips the case when the optional hook of the harness is missing.
func needHook(t *testing.T, h Harness, hooks ...string) {
	t.Helper()

	have := map[string]bool{
		"Audited":          h.Audited != nil,
		"RemoveUnit":       h.RemoveUnit != nil,
		"RenameUnit":       h.RenameUnit != nil,
		"RemakeCollection": h.RemakeCollection != nil,
		"MoveUnit":         h.MoveUnit != nil,
		"PurgeUnit":        h.PurgeUnit != nil,
		"StoreRaw":         h.StoreRaw != nil,
	}

	for _, hook := range hooks {
		if !have[hook] {
			t.Skip("the adapter has no hook " + hook)
		}
	}
}

func ruleCases() []suiteCase {
	return []suiteCase{
		{testRuleRoundTrip, "store: put and get round trip a rule on a unit, a collection and a namespace"},
		{testRuleRefusesInvalid, "store: put refuses a rule that fails Validate and changes nothing"},
		{testRuleRefusesPrincipal, "store: put refuses a rule that names a principal"},
		{testRuleRefusesWholeNamespace, "store: put refuses a rule that names a whole other namespace"},
		{testRuleKeepsCrossNamespace, "store: put keeps a rule that names a unit or a collection of another namespace"},
		{testRuleRefusesNoStableID, "store: put refuses a producer or a consumer of the namespace with no stable ID"},
		{testRuleUnknownProducer, "store: put refuses a producer unit that does not exist with ErrUnknownObject"},
		{
			testRuleUnknownConsumer,
			"store: put refuses a consumer unit or collection of the namespace that does not exist",
		},
		{testRuleMismatchedID, "store: put refuses a stable ID that names another object than the name"},
		{testRuleCrossWithID, "store: put refuses a consumer of another namespace with a stable ID"},
		{
			testRuleCrossSameAnswer,
			"store: put of a consumer of another namespace answers the same whether it exists or not",
		},
		{testRuleDuplicate, "store: put refuses a second rule with the same producer and consumer"},
		{testRuleLimit, "store: put refuses the rule past the limit of the producer"},
		{testRuleStaleFence, "store: put refuses a stale fence and a fence of another namespace"},
		{testRuleCrossFence, "store: a rule across namespaces carries the fence of the producer only"},
		{testRuleUnknownID, "store: get and delete of an unknown rule"},
		{testRuleOtherUnit, "store: get and delete of a rule of another unit answer ErrNotFound and change nothing"},
		{
			testRuleUnitNotCollection,
			"store: get and delete on a unit never reach the rule of its collection or its namespace",
		},
		{testRuleNamespaceNotUnit, "store: get and delete on the namespace never reach the rule of a unit"},
		{
			testRuleUnitMadeAgain,
			"store: get and delete on a unit made again with the old name never reach the rule of the old unit",
		},
		{testRuleInvalidKey, "store: get and delete refuse an invalid ID and an invalid namespace"},
		{testRuleDeleteStaleFence, "store: delete refuses a stale fence"},
		{testRuleListOrder, "store: list returns the rules of one producer ordered by ID"},
		{testRuleOtherNamespace, "store: a rule is invisible to another namespace"},
		{testRuleRename, "store: a rename of the producer or of a consumer in the namespace keeps the rule"},
		{
			testRuleCollectionMadeAgain,
			"store: a collection of the namespace made again with the old name does not get the rules",
		},
		{
			testRuleCrossCollectionMadeAgain,
			"store: a collection of another namespace made again with the old name matches the rule",
		},
		{testRulePurge, "store: the purge of a unit removes the rules that name it in its namespace"},
		{testFindProducerLevels, "store: find matches a rule on the unit, its collection and its namespace"},
		{
			testFindConsumerLevels,
			"store: find matches a consumer unit, its collection and the namespace of the producer",
		},
		{testFindNoNamespaceAcross, "store: find never matches a namespace rule for a consumer of another namespace"},
		{testFindCrossByName, "store: find matches a consumer of another namespace by its reference"},
		{testFindAfterMove, "store: find after a move of the consumer to another collection"},
		{testFindNoRule, "store: find returns ErrNoRule when no rule matches"},
		{
			testFindUnresolvedProducer,
			"store: find of a producer with no stable ID runs the same query and returns ErrNoRule",
		},
		{testFindSkipsInvalidRow, "store: find never matches a stored row that fails Validate"},
		{testFindInvalidPlacement, "store: find refuses an invalid placement"},
	}
}

func testRuleRoundTrip(t *testing.T, h Harness) {
	t.Helper()

	r := newRuleRig(t, h)
	a, b := r.unit("a", "team"), r.unit("b", "")
	byUnit := r.link(a.Unit, b.Unit)
	byCollection := r.link(collectionOf(a), b.Unit)
	byNamespace := r.link(nsObject(r.ns), collectionOf(a))

	for _, want := range []*outputshare.Rule{&byUnit, &byCollection, &byNamespace} {
		got, err := h.Store.Get(t.Context(), want.Producer, want.ID)
		if err != nil {
			t.Fatalf("Get(%s): %v", want.ID, err)
		}

		if got.ID != want.ID || got.Producer != want.Producer || got.Consumer != want.Consumer ||
			got.CreatedBy != tester {
			t.Fatalf("Get(%s) = %+v, want %+v", want.ID, got, want)
		}

		if got.CreatedAt.IsZero() {
			t.Fatalf("Get(%s) has no time", want.ID)
		}
	}

	r.wantIDs(a.Unit, string(byUnit.ID))
	r.wantIDs(collectionOf(a), string(byCollection.ID))
	r.wantIDs(nsObject(r.ns), string(byNamespace.ID))

	if err := r.del(a.Unit, byUnit.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	r.wantIDs(a.Unit)
}

func testRuleRefusesInvalid(t *testing.T, h Harness) {
	t.Helper()

	r := newRuleRig(t, h)
	a, b := r.unit("a", ""), r.unit("b", "")

	noCreator := r.rule(a.Unit, b.Unit)
	noCreator.CreatedBy = ""

	badID := r.rule(a.Unit, b.Unit)
	badID.ID = "Bad ID"

	longID := r.rule(a.Unit, b.Unit)
	longID.ID = outputshare.ID(strings.Repeat("a", outputshare.MaxIDBytes+1))

	for _, rule := range []*outputshare.Rule{&noCreator, &badID, &longID, emptyRule()} {
		r.wantPutErr(*rule, outputshare.ErrInvalid)
	}
}

func testRuleRefusesPrincipal(t *testing.T, h Harness) {
	t.Helper()

	r := newRuleRig(t, h)
	a := r.unit("a", "")
	p := identity.PrincipalObject("p1")

	r.wantPutErr(r.rule(a.Unit, p), outputshare.ErrInvalid)
	r.wantPutErr(r.rule(p, a.Unit), outputshare.ErrInvalid)
	r.wantPutErr(r.rule(a.Unit, identity.CallerObject()), outputshare.ErrInvalid)
	r.wantPutErr(r.rule(identity.ServerObject(), a.Unit), outputshare.ErrInvalid)
}

func testRuleRefusesWholeNamespace(t *testing.T, h Harness) {
	t.Helper()

	r := newRuleRig(t, h)
	a := r.unit("a", "")
	r.h.Namespace(t, globex.String())

	r.wantPutErr(r.rule(a.Unit, nsObject(globex)), outputshare.ErrInvalid)
	r.wantPutErr(r.rule(nsObject(r.ns), nsObject(globex)), outputshare.ErrInvalid)
}

func testRuleKeepsCrossNamespace(t *testing.T, h Harness) {
	t.Helper()

	r := newRuleRig(t, h)
	a := r.unit("a", "")

	unit, err := identity.UnitObject(ToolOpenTofu, globex, "web")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	byUnit := r.link(a.Unit, unit)
	byCollection := r.link(a.Unit, identity.CollectionObject(globex, "shared"))

	for _, want := range []*outputshare.Rule{&byUnit, &byCollection} {
		got, getErr := h.Store.Get(t.Context(), a.Unit, want.ID)
		if getErr != nil {
			t.Fatalf("Get(%s): %v", want.ID, getErr)
		}

		if got.Consumer != want.Consumer || got.Consumer.UnitID() != "" || got.Consumer.CollectionID() != "" {
			t.Fatalf(
				"Get(%s).Consumer = %+v, want the reference %s with no stable ID",
				want.ID,
				got.Consumer,
				want.Consumer,
			)
		}
	}
}

func testRuleRefusesNoStableID(t *testing.T, h Harness) {
	t.Helper()

	r := newRuleRig(t, h)
	a, b := r.unit("a", "team"), r.unit("b", "")

	bare := func(p outputshare.Placement) identity.Object {
		o, err := identity.UnitObject(p.Unit.Tool(), p.Unit.Namespace(), p.Unit.UnitName())
		if err != nil {
			t.Fatalf("unit object: %v", err)
		}

		return o
	}

	bareCollection := identity.CollectionObject(r.ns, a.Collection.CollectionName())

	r.wantPutErr(r.rule(bare(a), b.Unit), outputshare.ErrInvalid)
	r.wantPutErr(r.rule(a.Unit, bare(b)), outputshare.ErrInvalid)
	r.wantPutErr(r.rule(bareCollection, b.Unit), outputshare.ErrInvalid)
	r.wantPutErr(r.rule(a.Unit, bareCollection), outputshare.ErrInvalid)
}

func testRuleUnknownProducer(t *testing.T, h Harness) {
	t.Helper()

	r := newRuleRig(t, h)
	b := r.unit("b", "")

	ghost, err := identity.UnitObject(ToolOpenTofu, r.ns, "ghost")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	r.wantPutErr(r.rule(ghost.WithUnitID("ws-ghost"), b.Unit), outputshare.ErrUnknownObject)
	r.wantPutErr(r.rule(identity.CollectionObject(r.ns, "ghost").WithCollectionID("col-ghost"), b.Unit),
		outputshare.ErrUnknownObject)
}

func testRuleUnknownConsumer(t *testing.T, h Harness) {
	t.Helper()

	r := newRuleRig(t, h)
	a := r.unit("a", "")

	ghost, err := identity.UnitObject(ToolOpenTofu, r.ns, "ghost")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	r.wantPutErr(r.rule(a.Unit, ghost.WithUnitID("ws-ghost")), outputshare.ErrUnknownObject)
	r.wantPutErr(r.rule(a.Unit, identity.CollectionObject(r.ns, "ghost").WithCollectionID("col-ghost")),
		outputshare.ErrUnknownObject)
}

func testRuleMismatchedID(t *testing.T, h Harness) {
	t.Helper()

	r := newRuleRig(t, h)
	a, b := r.unit("a", "one"), r.unit("b", "two")

	aWithB := a.Unit.WithUnitID(b.Unit.UnitID())
	oneWithTwo := collectionOf(a).WithCollectionID(collectionOf(b).CollectionID())

	r.wantPutErr(r.rule(aWithB, b.Unit), outputshare.ErrUnknownObject)
	r.wantPutErr(r.rule(b.Unit, aWithB), outputshare.ErrUnknownObject)
	r.wantPutErr(r.rule(oneWithTwo, b.Unit), outputshare.ErrUnknownObject)
	r.wantPutErr(r.rule(a.Unit, oneWithTwo), outputshare.ErrUnknownObject)
}

func testRuleCrossWithID(t *testing.T, h Harness) {
	t.Helper()

	r := newRuleRig(t, h)
	a := r.unit("a", "")
	web := r.other("web", "shared")

	r.wantPutErr(r.rule(a.Unit, web.Unit), outputshare.ErrInvalid)
	r.wantPutErr(r.rule(a.Unit, collectionOf(web)), outputshare.ErrInvalid)
}

func testRuleCrossSameAnswer(t *testing.T, h Harness) {
	t.Helper()

	r := newRuleRig(t, h)
	a := r.unit("a", "")
	r.other("web", "shared")

	known, err := identity.UnitObject(ToolOpenTofu, globex, "web")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	unknown, err := identity.UnitObject(ToolOpenTofu, globex, "ghost")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	consumers := []identity.Object{
		known, unknown, identity.CollectionObject(globex, "shared"), identity.CollectionObject(globex, "ghost"),
	}

	for i := range consumers {
		r.link(a.Unit, consumers[i])
	}

	if got := len(r.list(a.Unit)); got != crossRules {
		t.Fatalf(
			"List = %d rules, want %d: an object of another namespace is saved whether it exists or not",
			got,
			crossRules,
		)
	}
}

func testRuleDuplicate(t *testing.T, h Harness) {
	t.Helper()

	r := newRuleRig(t, h)
	a, b := r.unit("a", ""), r.unit("b", "")

	first := r.link(a.Unit, b.Unit)

	r.wantPutErr(r.rule(a.Unit, b.Unit), outputshare.ErrAlreadyExists)
	r.wantPutErr(first, outputshare.ErrAlreadyExists)
	r.wantIDs(a.Unit, string(first.ID))

	// The same pair on another producer is a new rule.
	r.link(b.Unit, a.Unit)
}

func testRuleLimit(t *testing.T, h Harness) {
	t.Helper()

	r := newRuleRig(t, h)
	a := r.unit("a", "")

	for i := range outputshare.MaxRulesPerProducer {
		unit, err := identity.UnitObject(ToolOpenTofu, globex, fmt.Sprintf("web-%03d", i))
		if err != nil {
			t.Fatalf("unit object: %v", err)
		}

		r.link(a.Unit, unit)
	}

	over, err := identity.UnitObject(ToolOpenTofu, globex, "web-over")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	r.wantPutErr(r.rule(a.Unit, over), outputshare.ErrLimit)

	// The limit holds for each producer apart.
	b := r.unit("b", "")
	r.link(b.Unit, over)
}

func testRuleStaleFence(t *testing.T, h Harness) {
	t.Helper()

	r := newRuleRig(t, h)
	a, b := r.unit("a", ""), r.unit("b", "")
	fence := r.fence()
	other := h.Namespace(t, globex.String())

	for _, bad := range []namespace.Fence{
		{Namespace: acme, FailoverVersion: fence.FailoverVersion - 1},
		{Namespace: acme, FailoverVersion: fence.FailoverVersion + 1},
		other,
	} {
		rule := r.rule(a.Unit, b.Unit)

		err := h.Store.Put(t.Context(), rule, bad, ruleEntry(bad, rule))
		if !errors.Is(err, namespace.ErrFenced) {
			t.Fatalf("Put with the fence %+v = %v, want namespace.ErrFenced", bad, err)
		}
	}

	r.wantIDs(a.Unit)
}

func testRuleCrossFence(t *testing.T, h Harness) {
	t.Helper()

	r := newRuleRig(t, h)
	a := r.unit("a", "")
	other := h.Namespace(t, globex.String())

	unit, err := identity.UnitObject(ToolOpenTofu, globex, "web")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	rule := r.rule(a.Unit, unit)

	// The fence of the consumer namespace does not write the rule.
	if err = h.Store.Put(t.Context(), rule, other, ruleEntry(other, rule)); !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("Put with the fence of the consumer namespace = %v, want namespace.ErrFenced", err)
	}

	needHook(t, h, "Audited")

	before := len(h.Audited(t, globex.String()))

	r.mustPut(rule)

	if got := len(h.Audited(t, globex.String())); got != before {
		t.Fatalf("a rule across namespaces wrote %d entries in the consumer namespace, want none", got-before)
	}
}

func testRuleUnknownID(t *testing.T, h Harness) {
	t.Helper()

	r := newRuleRig(t, h)
	a := r.unit("a", "")

	if _, err := h.Store.Get(t.Context(), a.Unit, "rule-missing"); !errors.Is(err, outputshare.ErrNotFound) {
		t.Fatalf("Get of an unknown rule = %v, want ErrNotFound", err)
	}

	if err := r.del(a.Unit, "rule-missing"); !errors.Is(err, outputshare.ErrNotFound) {
		t.Fatalf("Delete of an unknown rule = %v, want ErrNotFound", err)
	}
}

func testRuleOtherUnit(t *testing.T, h Harness) {
	t.Helper()

	r := newRuleRig(t, h)
	a, b := r.unit("a", ""), r.unit("b", "")
	rule := r.link(a.Unit, b.Unit)

	r.wantHidden(b.Unit, rule.ID, a.Unit)
	r.wantIDs(a.Unit, string(rule.ID))
}

func testRuleUnitNotCollection(t *testing.T, h Harness) {
	t.Helper()

	r := newRuleRig(t, h)
	a, b := r.unit("a", "team"), r.unit("b", "")
	onCollection := r.link(collectionOf(a), b.Unit)
	onNamespace := r.link(nsObject(r.ns), b.Unit)

	r.wantHidden(a.Unit, onCollection.ID, collectionOf(a))
	r.wantHidden(a.Unit, onNamespace.ID, nsObject(r.ns))
	r.wantHidden(collectionOf(a), onNamespace.ID, nsObject(r.ns))
}

func testRuleNamespaceNotUnit(t *testing.T, h Harness) {
	t.Helper()

	r := newRuleRig(t, h)
	a, b := r.unit("a", "team"), r.unit("b", "")
	onUnit := r.link(a.Unit, b.Unit)
	onCollection := r.link(collectionOf(a), b.Unit)

	r.wantHidden(nsObject(r.ns), onUnit.ID, a.Unit)
	r.wantHidden(nsObject(r.ns), onCollection.ID, collectionOf(a))
}

func testRuleUnitMadeAgain(t *testing.T, h Harness) {
	t.Helper()
	needHook(t, h, "RemoveUnit")

	r := newRuleRig(t, h)
	a, b := r.unit("a", ""), r.unit("b", "")
	rule := r.link(a.Unit, b.Unit)

	h.RemoveUnit(t, a)

	again := r.unit("a", "")
	if again.Unit.UnitID() == a.Unit.UnitID() {
		t.Fatalf("the unit made again has the old stable ID %s", a.Unit.UnitID())
	}

	if _, err := h.Store.Get(t.Context(), again.Unit, rule.ID); !errors.Is(err, outputshare.ErrNotFound) {
		t.Fatalf("Get of the rule of the old unit by the new unit = %v, want ErrNotFound", err)
	}

	if err := r.del(again.Unit, rule.ID); !errors.Is(err, outputshare.ErrNotFound) {
		t.Fatalf("Delete of the rule of the old unit by the new unit = %v, want ErrNotFound", err)
	}

	r.wantIDs(again.Unit)
	r.wantNoRule(again, b)
}

func testRuleInvalidKey(t *testing.T, h Harness) {
	t.Helper()

	r := newRuleRig(t, h)
	a := r.unit("a", "")

	for _, id := range []outputshare.ID{"", "Bad ID", outputshare.ID(strings.Repeat("a", outputshare.MaxIDBytes+1))} {
		if _, err := h.Store.Get(t.Context(), a.Unit, id); !errors.Is(err, outputshare.ErrInvalid) {
			t.Fatalf("Get(%q) = %v, want ErrInvalid", id, err)
		}

		if err := r.del(a.Unit, id); !errors.Is(err, outputshare.ErrInvalid) {
			t.Fatalf("Delete(%q) = %v, want ErrInvalid", id, err)
		}
	}

	bare, err := identity.UnitObject(ToolOpenTofu, r.ns, "a")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	producers := []identity.Object{
		identity.PrincipalObject("p1"), nsObject("bad name!"), {}, bare, identity.CollectionObject(r.ns, "team"),
	}

	for i := range producers {
		producer := producers[i]

		if _, err := h.Store.Get(t.Context(), producer, "rule-001"); !errors.Is(err, outputshare.ErrInvalid) {
			t.Fatalf("Get on the producer %q = %v, want ErrInvalid", producer, err)
		}

		if err := r.del(producer, "rule-001"); !errors.Is(err, outputshare.ErrInvalid) {
			t.Fatalf("Delete on the producer %q = %v, want ErrInvalid", producer, err)
		}

		if _, err := h.Store.List(t.Context(), producer); !errors.Is(err, outputshare.ErrInvalid) {
			t.Fatalf("List on the producer %q = %v, want ErrInvalid", producer, err)
		}
	}
}

func testRuleDeleteStaleFence(t *testing.T, h Harness) {
	t.Helper()

	r := newRuleRig(t, h)
	a, b := r.unit("a", ""), r.unit("b", "")
	rule := r.link(a.Unit, b.Unit)
	fence := r.fence()
	other := h.Namespace(t, globex.String())

	for _, bad := range []namespace.Fence{
		{Namespace: acme, FailoverVersion: fence.FailoverVersion - 1},
		{Namespace: acme, FailoverVersion: fence.FailoverVersion + 1},
		other,
	} {
		err := h.Store.Delete(t.Context(), a.Unit, rule.ID, bad, ruleEntry(bad, rule))
		if !errors.Is(err, namespace.ErrFenced) {
			t.Fatalf("Delete with the fence %+v = %v, want namespace.ErrFenced", bad, err)
		}
	}

	r.wantIDs(a.Unit, string(rule.ID))
}

func testRuleListOrder(t *testing.T, h Harness) {
	t.Helper()

	r := newRuleRig(t, h)
	a := r.unit("a", "")
	b, c, d := r.unit("b", ""), r.unit("c", ""), r.unit("d", "")

	r.mustPut(r.ruleWithID("rule-300", a.Unit, b.Unit))
	r.mustPut(r.ruleWithID("rule-100", a.Unit, c.Unit))
	r.mustPut(r.ruleWithID("rule-200", a.Unit, d.Unit))
	r.mustPut(r.ruleWithID("rule-150", b.Unit, c.Unit))

	r.wantIDs(a.Unit, "rule-100", "rule-200", "rule-300")
	r.wantIDs(b.Unit, "rule-150")
	r.wantIDs(c.Unit)
}

func testRuleOtherNamespace(t *testing.T, h Harness) {
	t.Helper()

	r := newRuleRig(t, h)
	a, b := r.unit("a", ""), r.unit("b", "")
	r.link(a.Unit, b.Unit)
	r.link(nsObject(r.ns), b.Unit)

	r.other("a", "")
	r.other("b", "")

	r.wantIDs(nsObject(globex))

	ga, gb := r.other("a2", ""), r.other("b2", "")
	if _, err := r.find(ga, gb); !errors.Is(err, outputshare.ErrNoRule) {
		t.Fatalf("Find in globex = %v, want ErrNoRule: a rule of acme does not apply there", err)
	}
}

func testRuleRename(t *testing.T, h Harness) {
	t.Helper()
	needHook(t, h, "RenameUnit")

	r := newRuleRig(t, h)
	a := r.h.Unit(t, ToolPulumi, r.ns, "proj/a", "")
	b := r.h.Unit(t, ToolPulumi, r.ns, "proj/b", "")
	rule := r.link(a.Unit, b.Unit)

	a2 := h.RenameUnit(t, a, "a2")
	b2 := h.RenameUnit(t, b, "b2")

	got := r.list(a2.Unit)
	if len(got) != 1 || got[0].ID != rule.ID {
		t.Fatalf("List after the rename = %+v, want the rule %s", got, rule.ID)
	}

	if got[0].Producer.UnitName() != "proj/a2" || got[0].Consumer.UnitName() != "proj/b2" {
		t.Fatalf("the rule names %s and %s after the rename, want the new names", got[0].Producer, got[0].Consumer)
	}

	r.wantFind(a2, b2, rule.ID)
}

func testRuleCollectionMadeAgain(t *testing.T, h Harness) {
	t.Helper()
	needHook(t, h, "RemakeCollection", "MoveUnit")

	r := newRuleRig(t, h)
	a, b := r.unit("a", "shared"), r.unit("b", "")
	oldRule := r.link(collectionOf(a), b.Unit)

	h.MoveUnit(t, a, "elsewhere")

	again := h.RemakeCollection(t, r.ns, "shared")
	if again.CollectionID() == collectionOf(a).CollectionID() {
		t.Fatal("the collection made again has the old stable ID")
	}

	r.wantIDs(again)

	fresh := r.unit("c", "shared")
	if fresh.Collection.CollectionID() != again.CollectionID() {
		t.Fatalf("the new unit is in the collection %s, want %s", fresh.Collection, again)
	}

	r.wantNoRule(fresh, b)

	if _, err := h.Store.Get(t.Context(), again, oldRule.ID); !errors.Is(err, outputshare.ErrNotFound) {
		t.Fatalf("Get of the old rule by the new collection = %v, want ErrNotFound", err)
	}
}

func testRuleCrossCollectionMadeAgain(t *testing.T, h Harness) {
	t.Helper()
	needHook(t, h, "RemakeCollection", "MoveUnit")

	r := newRuleRig(t, h)
	a := r.unit("a", "")
	web := r.other("web", "shared")

	rule := r.link(a.Unit, identity.CollectionObject(globex, "shared"))
	r.wantFind(a, web, rule.ID)

	h.MoveUnit(t, web, "elsewhere")
	h.RemakeCollection(t, globex, "shared")

	fresh := r.other("web2", "shared")
	r.wantFind(a, fresh, rule.ID)
}

func testRulePurge(t *testing.T, h Harness) {
	t.Helper()
	needHook(t, h, "PurgeUnit")

	r := newRuleRig(t, h)
	a, b, c := r.unit("a", ""), r.unit("b", ""), r.unit("c", "")

	r.link(a.Unit, b.Unit)
	r.link(c.Unit, a.Unit)
	keep := r.link(c.Unit, b.Unit)
	cross, err := identity.UnitObject(ToolOpenTofu, globex, "a")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	byName := r.link(b.Unit, cross)

	h.PurgeUnit(t, a)

	r.wantIDs(a.Unit)
	r.wantIDs(c.Unit, string(keep.ID))
	r.wantIDs(b.Unit, string(byName.ID))

	// The reader leaves out a rule of a unit that is gone, so the count of the
	// limit shows whether the purge removed it. The producer c holds the rule
	// that names b. With 98 more rules it holds 99, and the last place is
	// free only when the rule that named the purged unit is gone.
	const others = outputshare.MaxRulesPerProducer - 2

	for i := range others {
		unit, unitErr := identity.UnitObject(ToolOpenTofu, globex, fmt.Sprintf("web-%03d", i))
		if unitErr != nil {
			t.Fatalf("unit object: %v", unitErr)
		}

		r.link(c.Unit, unit)
	}

	last, err := identity.UnitObject(ToolOpenTofu, globex, "web-last")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	r.link(c.Unit, last)
}

func testFindProducerLevels(t *testing.T, h Harness) {
	t.Helper()

	r := newRuleRig(t, h)
	a := r.unit("a", "team")
	b1, b2, b3, b4 := r.unit("b1", ""), r.unit("b2", ""), r.unit("b3", ""), r.unit("b4", "")

	byUnit := r.link(a.Unit, b1.Unit)
	byCollection := r.link(collectionOf(a), b2.Unit)
	byNamespace := r.link(nsObject(r.ns), b3.Unit)

	r.wantFind(a, b1, byUnit.ID)
	r.wantFind(a, b2, byCollection.ID)
	r.wantFind(a, b3, byNamespace.ID)
	r.wantNoRule(a, b4)

	// A unit outside the collection gets the rule of the namespace only.
	out := r.unit("out", "")
	r.wantNoRule(out, b1)
	r.wantNoRule(out, b2)
	r.wantFind(out, b3, byNamespace.ID)
}

func testFindConsumerLevels(t *testing.T, h Harness) {
	t.Helper()

	r := newRuleRig(t, h)
	a1, a2, a3 := r.unit("a1", ""), r.unit("a2", ""), r.unit("a3", "")
	b, e := r.unit("b", "dest"), r.unit("e", "")

	byUnit := r.link(a1.Unit, b.Unit)
	byCollection := r.link(a2.Unit, collectionOf(b))
	byNamespace := r.link(a3.Unit, nsObject(r.ns))

	r.wantFind(a1, b, byUnit.ID)
	r.wantNoRule(a1, e)
	r.wantFind(a2, b, byCollection.ID)
	r.wantNoRule(a2, e)
	r.wantFind(a3, b, byNamespace.ID)
	r.wantFind(a3, e, byNamespace.ID)
}

func testFindNoNamespaceAcross(t *testing.T, h Harness) {
	t.Helper()

	r := newRuleRig(t, h)
	a := r.unit("a", "")
	r.link(a.Unit, nsObject(r.ns))
	r.link(nsObject(r.ns), nsObject(r.ns))

	r.wantNoRule(a, r.other("web", ""))
	r.wantNoRule(a, r.other("web2", "shared"))
}

func testFindCrossByName(t *testing.T, h Harness) {
	t.Helper()

	r := newRuleRig(t, h)
	a1, a2 := r.unit("a1", ""), r.unit("a2", "")
	web, other, inShared, outside := r.other(
		"web",
		"",
	), r.other(
		"other",
		"",
	), r.other(
		"in",
		"shared",
	), r.other(
		"out",
		"",
	)

	byUnit := r.link(a1.Unit, web.Unit.WithUnitID(""))
	byCollection := r.link(a2.Unit, identity.CollectionObject(globex, "Shared"))

	r.wantFind(a1, web, byUnit.ID)
	r.wantNoRule(a1, other)
	r.wantFind(a2, inShared, byCollection.ID)
	r.wantNoRule(a2, outside)
	r.wantNoRule(a2, web)
}

func testFindAfterMove(t *testing.T, h Harness) {
	t.Helper()
	needHook(t, h, "MoveUnit")

	r := newRuleRig(t, h)
	a := r.unit("a", "")
	b := r.unit("b", "dest")
	rule := r.link(a.Unit, collectionOf(b))

	r.wantFind(a, b, rule.ID)

	moved := h.MoveUnit(t, b, "elsewhere")
	r.wantNoRule(a, moved)

	back := h.MoveUnit(t, moved, "dest")
	r.wantFind(a, back, rule.ID)
}

func testFindNoRule(t *testing.T, h Harness) {
	t.Helper()

	r := newRuleRig(t, h)
	a, b := r.unit("a", ""), r.unit("b", "")

	r.wantNoRule(a, b)
	r.link(b.Unit, a.Unit)
	r.wantNoRule(a, b)
}

func testFindUnresolvedProducer(t *testing.T, h Harness) {
	t.Helper()

	r := newRuleRig(t, h)
	a, b := r.unit("a", ""), r.unit("b", "")
	r.link(nsObject(r.ns), b.Unit)
	r.link(a.Unit, b.Unit)

	ghost, err := identity.UnitObject(ToolOpenTofu, r.ns, "ghost")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	// A producer that the adapter could not resolve has no stable ID. The
	// namespace rule does not cover it, so the answer is the same as for a
	// unit that no rule covers.
	r.wantNoRule(outputshare.Placement{Unit: ghost, Collection: identity.Object{}}, b)
}

func testFindSkipsInvalidRow(t *testing.T, h Harness) {
	t.Helper()
	needHook(t, h, "StoreRaw")

	r := newRuleRig(t, h)
	a, b := r.unit("a", ""), r.unit("b", "")

	bad := r.rule(a.Unit, b.Unit)
	bad.CreatedBy = ""

	h.StoreRaw(t, bad)

	if _, err := r.find(a, b); !errors.Is(err, outputshare.ErrNoRule) {
		t.Fatalf("Find with a stored row that fails Validate = %v, want ErrNoRule", err)
	}

	r.wantIDs(a.Unit)

	if _, err := h.Store.Get(t.Context(), a.Unit, bad.ID); !errors.Is(err, outputshare.ErrNotFound) {
		t.Fatalf("Get of a stored row that fails Validate = %v, want ErrNotFound", err)
	}

	// A valid rule after the bad row still matches: the bad row hides nothing.
	good := r.link(nsObject(r.ns), b.Unit)
	r.wantFind(a, b, good.ID)
}

func testFindInvalidPlacement(t *testing.T, h Harness) {
	t.Helper()

	r := newRuleRig(t, h)
	a := r.unit("a", "")
	ns := outputshare.Placement{Unit: nsObject(r.ns), Collection: identity.Object{}}
	foreign := outputshare.Placement{
		Unit:       a.Unit,
		Collection: identity.CollectionObject(globex, "x").WithCollectionID("c"),
	}
	bare := outputshare.Placement{Unit: identity.Object{}, Collection: identity.Object{}}

	pairs := [][2]outputshare.Placement{{a, ns}, {a, bare}, {ns, a}, {a, foreign}, {bare, a}}

	for i := range pairs {
		pair := &pairs[i]

		if _, err := r.find(pair[0], pair[1]); !errors.Is(err, outputshare.ErrInvalid) {
			t.Fatalf("Find(%s, %s) = %v, want ErrInvalid", pair[0].Unit, pair[1].Unit, err)
		}
	}
}

// RawRuleInsertSQLite is the statement that writes one rule row with direct
// SQL on SQLite.
func RawRuleInsertSQLite() string {
	return rawRuleInsert(func(int) string { return "?" })
}

// RawRuleInsertPostgres is the statement that writes one rule row with direct
// SQL on PostgreSQL.
func RawRuleInsertPostgres() string {
	return rawRuleInsert(func(i int) string { return fmt.Sprintf("$%d", i+1) })
}

// rawRuleInsert builds the statement with the placeholder of the dialect.
func rawRuleInsert(placeholder func(i int) string) string {
	cols := "namespace, id, producer_kind, producer_id, consumer_kind, consumer_namespace, consumer_id, " +
		"consumer_tool, consumer_name, created_by, created_at_ms"

	const columns = 11

	marks := make([]string, 0, columns)

	for i := range columns {
		marks = append(marks, placeholder(i))
	}

	return "INSERT INTO output_sharing_rules (" + cols + ") VALUES (" + strings.Join(marks, ", ") + ")"
}

// RawRuleRow returns the column values of the row that holds the rule r, in
// the order of RawRuleInsert, with no check of r. A harness uses it to plant a
// row that a store write would refuse, as a row of another version or a
// restore could hold.
func RawRuleRow(r outputshare.Rule) []any {
	kind := func(o identity.Object) string {
		switch {
		case o.IsUnit():
			return kindUnit
		case o.IsCollection():
			return kindCollection
		default:
			return kindNamespace
		}
	}

	stable := func(o identity.Object) string {
		if o.IsUnit() {
			return o.UnitID()
		}

		return o.CollectionID()
	}

	row := []any{
		r.Producer.Namespace().String(), string(r.ID), kind(r.Producer), stable(r.Producer),
		kind(r.Consumer), r.Consumer.Namespace().String(), "", "", "", r.CreatedBy, int64(1),
	}

	switch {
	case r.CrossNamespace() && r.Consumer.IsUnit():
		row[7], row[8] = r.Consumer.Tool(), r.Consumer.UnitName()
	case r.CrossNamespace() && r.Consumer.IsCollection():
		row[8] = strings.ToLower(r.Consumer.CollectionName())
	default:
		row[6] = stable(r.Consumer)
	}

	return row
}

// HostileRuleRow is one row for the table test of the CHECK constraints.
type HostileRuleRow struct {
	// Name says what the row names.
	Name string
	// Row holds the namespace, the rule ID, the kind and the stable ID of the
	// producer, the kind, the namespace, the stable ID, the tool and the name
	// of the consumer, and the principal that saved the rule.
	Row [10]string
	// OK is true for the one row that the table accepts.
	OK bool
}

// HostileRuleRows are rows that the CHECK constraints of the rule table
// refuse, each for one reason, and the one row that they accept.
func HostileRuleRows() []HostileRuleRow {
	acmeName, globexName := acme.String(), globex.String()
	longID := strings.Repeat("a", outputshare.MaxIDBytes+1)

	row := func(name string, ok bool, cols ...string) HostileRuleRow {
		r := HostileRuleRow{Name: name, Row: [10]string{}, OK: ok}
		copy(r.Row[:], cols)

		return r
	}

	return []HostileRuleRow{
		row("a unit that names a unit of its namespace", true,
			acmeName, "r1", kindUnit, "u1", kindUnit, acmeName, "u2", "", "", tester),
		row("a unit that names a unit of another namespace by name", true,
			acmeName, "r2", kindUnit, "u1", kindUnit, globexName, "", ToolOpenTofu, webName, tester),
		row("a producer that is a principal", false,
			acmeName, "r3", "principal", "p1", kindUnit, acmeName, "u2", "", "", tester),
		row("a consumer that is a principal", false,
			acmeName, "r4", kindUnit, "u1", "principal", acmeName, "p1", "", "", tester),
		row("a namespace producer with a stable ID", false,
			acmeName, "r5", kindNamespace, "x", kindUnit, acmeName, "u2", "", "", tester),
		row("a unit producer with no stable ID", false,
			acmeName, "r6", kindUnit, "", kindUnit, acmeName, "u2", "", "", tester),
		row("a whole other namespace as the consumer", false,
			acmeName, "r7", kindUnit, "u1", kindNamespace, globexName, "", "", "", tester),
		row("a consumer of the namespace with no stable ID", false,
			acmeName, "r8", kindUnit, "u1", kindUnit, acmeName, "", "", "", tester),
		row("a consumer of the namespace with a name", false,
			acmeName, "r9", kindUnit, "u1", kindUnit, acmeName, "u2", ToolOpenTofu, webName, tester),
		row("a consumer of another namespace with a stable ID", false,
			acmeName, "r10", kindUnit, "u1", kindUnit, globexName, "u2", ToolOpenTofu, webName, tester),
		row("a unit consumer of another namespace with no tool", false,
			acmeName, "r11", kindUnit, "u1", kindUnit, globexName, "", "", webName, tester),
		row("a collection consumer of another namespace with a tool", false,
			acmeName, "r12", kindUnit, "u1", kindCollection, globexName, "", ToolOpenTofu, webName, tester),
		row("a consumer of another namespace with no name", false,
			acmeName, "r13", kindUnit, "u1", kindUnit, globexName, "", ToolOpenTofu, "", tester),
		row("a rule with no creator", false,
			acmeName, "r14", kindUnit, "u1", kindUnit, acmeName, "u2", "", "", ""),
		row("a rule ID with a capital letter", false,
			acmeName, "R15", kindUnit, "u1", kindUnit, acmeName, "u2", "", "", tester),
		row("a rule ID with a space", false,
			acmeName, "r 16", kindUnit, "u1", kindUnit, acmeName, "u2", "", "", tester),
		row("a rule ID that is empty", false,
			acmeName, "", kindUnit, "u1", kindUnit, acmeName, "u2", "", "", tester),
		row("a rule ID that is too long", false,
			acmeName, longID, kindUnit, "u1", kindUnit, acmeName, "u2", "", "", tester),
	}
}
