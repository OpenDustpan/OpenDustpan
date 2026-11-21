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
		CreatedAt: time.Time{},
		ID:        outputshare.ID(id),
		CreatedBy: tester,
		Producer:  producer,
		Consumer:  consumerOf(consumer),
	}
}

// ruleFor returns a rule with a new ID whose consumer is c.
func (r *ruleRig) ruleFor(producer identity.Object, c outputshare.Consumer) outputshare.Rule {
	r.t.Helper()

	rule := r.rule(producer, identity.Object{})
	rule.Consumer = c

	return rule
}

// linkTo saves a rule that names the producer and the consumer c.
func (r *ruleRig) linkTo(producer identity.Object, c outputshare.Consumer) outputshare.Rule {
	r.t.Helper()

	return r.mustPut(r.ruleFor(producer, c))
}

// foreignUnit returns the consumer that is the unit with the stable ID id in
// the namespace ns. The store does not resolve it.
func foreignUnit(ns namespace.Name, id string) outputshare.Consumer {
	return outputshare.Consumer{Kind: outputshare.ConsumerUnit, Namespace: ns, ID: id}
}

// foreignCollection returns the consumer that is the collection with the
// stable ID id in the namespace globex. The store does not resolve it.
func foreignCollection(id string) outputshare.Consumer {
	return outputshare.Consumer{Kind: outputshare.ConsumerCollection, Namespace: globex, ID: id}
}

// consumerOf returns the stable reference of the unit, the collection or the
// namespace o: its namespace and its stable ID. The zero object gives the zero
// consumer.
func consumerOf(o identity.Object) outputshare.Consumer {
	switch {
	case o.IsUnit():
		return outputshare.Consumer{Kind: outputshare.ConsumerUnit, Namespace: o.Namespace(), ID: o.UnitID()}
	case o.IsCollection():
		return outputshare.Consumer{
			Kind: outputshare.ConsumerCollection, Namespace: o.Namespace(), ID: o.CollectionID(),
		}
	case o.IsNamespace():
		return outputshare.Consumer{Kind: outputshare.ConsumerNamespace, Namespace: o.Namespace(), ID: ""}
	default:
		return outputshare.Consumer{Kind: "", Namespace: "", ID: ""}
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
		CreatedAt: time.Time{},
		ID:        id,
		CreatedBy: "",
		Producer:  producer,
		Consumer:  outputshare.Consumer{Kind: "", Namespace: "", ID: ""},
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
		CreatedAt: time.Time{},
		ID:        "",
		CreatedBy: "",
		Producer:  identity.Object{},
		Consumer:  outputshare.Consumer{Kind: "", Namespace: "", ID: ""},
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

// ruleCases are the cases of the rule store.
func ruleCases() []suiteCase {
	return slices.Concat(rulePutCases(), ruleLifeCases())
}

// rulePutCases are the cases of the write, the read and the delete of a rule.
func rulePutCases() []suiteCase {
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
		{testRuleCrossIDFormat, "store: put refuses a consumer ID of another namespace with a bad format"},
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
	}
}

// ruleLifeCases are the cases of the rules over the life of the objects that
// they name (rename, made again, purge and delete), and the cases of Find.
func ruleLifeCases() []suiteCase {
	return []suiteCase{
		{testRuleRename, "store: a rename of the producer or of a consumer in the namespace keeps the rule"},
		{testRuleCrossUnitRename, "store: a rename of a consumer unit in another namespace keeps the rule"},
		{
			testRuleCollectionMadeAgain,
			"store: a collection of the namespace made again with the old name does not get the rules",
		},
		{
			testRuleCrossCollectionMadeAgain,
			"store: a collection of another namespace made again with the old name does not get the rule",
		},
		{testRulePurge, "store: the purge of a unit removes the rules that name it in its namespace"},
		{
			testRulePurgeCrossNamespace,
			"store: the purge of a unit removes the rules of any namespace that name its stable ID",
		},
		{
			testRulePurgeSameIDOtherNamespace,
			"store: the purge of a unit keeps a rule that names the same ID in another namespace",
		},
		{testRuleCollectionDelete, "store: the delete of a collection removes the rules that name it in its namespace"},
		{
			testRuleCollectionDeleteSameIDOtherNamespace,
			"store: the delete of a collection keeps a rule that names the same ID in another namespace",
		},
		{
			testRuleCollectionDeleteCrossNamespace,
			"store: the delete of a collection removes the rules of any namespace that name its stable ID",
		},
		{testFindProducerLevels, "store: find matches a rule on the unit, its collection and its namespace"},
		{
			testFindConsumerLevels,
			"store: find matches a consumer unit, its collection and the namespace of the producer",
		},
		{testFindNoNamespaceAcross, "store: find never matches a namespace rule for a consumer of another namespace"},
		{testFindCrossByID, "store: find matches a consumer of another namespace by its stable ID, never by name"},
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

	// The IDs name no object: the store keeps the ID that the caller gives.
	byUnit := r.linkTo(a.Unit, foreignUnit(globex, "ws-other"))
	byCollection := r.linkTo(a.Unit, foreignCollection("col-other"))

	for _, want := range []*outputshare.Rule{&byUnit, &byCollection} {
		got, getErr := h.Store.Get(t.Context(), a.Unit, want.ID)
		if getErr != nil {
			t.Fatalf("Get(%s): %v", want.ID, getErr)
		}

		if got.Consumer != want.Consumer {
			t.Fatalf("Get(%s).Consumer = %+v, want the stable reference %+v", want.ID, got.Consumer, want.Consumer)
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
	r.wantPutErr(r.rule(oneWithTwo, b.Unit), outputshare.ErrUnknownObject)
}

func testRuleCrossIDFormat(t *testing.T, h Harness) {
	t.Helper()

	r := newRuleRig(t, h)
	a := r.unit("a", "")

	// Only the format of the ID of a consumer of another namespace is checked.
	// Each value below is refused alone, as if the handler had passed it.
	for _, id := range []string{
		"", "Upper", "a/b", "a b", "a\nb", "../x", "a.b", "a:b", "caf\u00e9", "a\x00b",
		strings.Repeat("a", outputshare.MaxIDBytes+1),
	} {
		r.wantPutErr(r.ruleFor(a.Unit, foreignUnit(globex, id)), outputshare.ErrInvalid)
		r.wantPutErr(r.ruleFor(a.Unit, foreignCollection(id)), outputshare.ErrInvalid)
	}

	for _, ns := range []namespace.Name{"", "Bad Name", "a/b"} {
		r.wantPutErr(r.ruleFor(a.Unit, foreignUnit(ns, "ws-ok")), outputshare.ErrInvalid)
	}

	r.wantPutErr(r.ruleFor(a.Unit, outputshare.Consumer{Kind: "principal", Namespace: globex, ID: "ws-ok"}),
		outputshare.ErrInvalid)
	r.wantPutErr(r.ruleFor(a.Unit, outputshare.Consumer{Kind: outputshare.ConsumerNamespace, Namespace: r.ns, ID: "x"}),
		outputshare.ErrInvalid)

	r.wantIDs(a.Unit)

	r.linkTo(a.Unit, foreignUnit(globex, strings.Repeat("a", outputshare.MaxIDBytes)))
	r.linkTo(a.Unit, foreignUnit(globex, "ws_ok-1"))
}

func testRuleCrossSameAnswer(t *testing.T, h Harness) {
	t.Helper()

	r := newRuleRig(t, h)
	a := r.unit("a", "")
	web := r.other("web", "shared")

	// An ID of an object that exists and an ID of one that does not get the
	// same answer: the save never tells whether an object of another namespace
	// exists.
	consumers := []outputshare.Consumer{
		foreignUnit(globex, web.Unit.UnitID()),
		foreignUnit(globex, "ws-ghost"),
		foreignCollection(web.Collection.CollectionID()),
		foreignCollection("col-ghost"),
	}

	for i := range consumers {
		r.linkTo(a.Unit, consumers[i])
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
		r.linkTo(a.Unit, foreignUnit(globex, fmt.Sprintf("ws-other-%03d", i)))
	}

	over := foreignUnit(globex, "ws-over")

	r.wantPutErr(r.ruleFor(a.Unit, over), outputshare.ErrLimit)

	// The limit holds for each producer apart.
	b := r.unit("b", "")
	r.linkTo(b.Unit, over)
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

	rule := r.ruleFor(a.Unit, foreignUnit(globex, "ws-other"))

	// The fence of the consumer namespace does not write the rule.
	if err := h.Store.Put(t.Context(), rule, other, ruleEntry(other, rule)); !errors.Is(err, namespace.ErrFenced) {
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

	if got[0].Producer.UnitName() != "proj/a2" || got[0].Consumer != consumerOf(b.Unit) {
		t.Fatalf(
			"the rule names %s and %+v after the rename, want the new producer name and the same consumer ID",
			got[0].Producer, got[0].Consumer,
		)
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

	rule := r.linkTo(a.Unit, consumerOf(collectionOf(web)))
	r.wantFind(a, web, rule.ID)

	h.MoveUnit(t, web, "elsewhere")
	h.RemakeCollection(t, globex, "shared")

	// The collection that someone makes again with the old name has a new ID,
	// so it does not get the rule.
	fresh := r.other("web2", "shared")
	r.wantNoRule(a, fresh)
}

func testRuleCrossUnitRename(t *testing.T, h Harness) {
	t.Helper()
	needHook(t, h, "RenameUnit")

	r := newRuleRig(t, h)
	a := r.unit("a", "")
	r.h.Namespace(t, globex.String())
	web := r.h.Unit(t, ToolPulumi, globex, "proj/web", "")

	rule := r.linkTo(a.Unit, consumerOf(web.Unit))
	r.wantFind(a, web, rule.ID)

	// A rename in the other namespace changes no rule.
	renamed := h.RenameUnit(t, web, "web2")
	r.wantFind(a, renamed, rule.ID)
}

func testRulePurge(t *testing.T, h Harness) {
	t.Helper()
	needHook(t, h, "PurgeUnit")

	r := newRuleRig(t, h)
	a, b, c := r.unit("a", ""), r.unit("b", ""), r.unit("c", "")

	r.link(a.Unit, b.Unit)
	r.link(c.Unit, a.Unit)
	keep := r.link(c.Unit, b.Unit)
	byID := r.linkTo(b.Unit, foreignUnit(globex, "ws-other"))

	h.PurgeUnit(t, a)

	r.wantIDs(a.Unit)
	r.wantIDs(c.Unit, string(keep.ID))
	r.wantIDs(b.Unit, string(byID.ID))

	// The reader leaves out a rule of a unit that is gone, so the count of the
	// limit shows whether the purge removed it. The producer c holds the rule
	// that names b. With 98 more rules it holds 99, and the last place is
	// free only when the rule that named the purged unit is gone.
	const others = outputshare.MaxRulesPerProducer - 2

	for i := range others {
		r.linkTo(c.Unit, foreignUnit(globex, fmt.Sprintf("ws-other-%03d", i)))
	}

	r.linkTo(c.Unit, foreignUnit(globex, "ws-other-last"))
}

func testRulePurgeCrossNamespace(t *testing.T, h Harness) {
	t.Helper()
	needHook(t, h, "PurgeUnit")

	r := newRuleRig(t, h)
	a := r.unit("a", "")
	g := r.other("g", "")
	other := r.other("h", "")

	named := r.linkTo(a.Unit, consumerOf(g.Unit))
	kept := r.linkTo(a.Unit, consumerOf(other.Unit))

	// The purge of a unit of globex removes each rule of acme that names its
	// ID, in the transaction of the purge.
	h.PurgeUnit(t, g)

	r.wantIDs(a.Unit, string(kept.ID))

	if _, err := h.Store.Get(t.Context(), a.Unit, named.ID); !errors.Is(err, outputshare.ErrNotFound) {
		t.Fatalf("Get of the rule that named the purged unit = %v, want ErrNotFound", err)
	}

	// The rule row is gone, not only hidden: the limit counts rows.
	for i := range outputshare.MaxRulesPerProducer - 1 {
		r.linkTo(a.Unit, foreignUnit(globex, fmt.Sprintf("ws-other-%03d", i)))
	}

	if err := r.put(r.ruleFor(a.Unit, foreignUnit(globex, "ws-last"))); !errors.Is(err, outputshare.ErrLimit) {
		t.Fatalf("Put after 100 rules = %v, want ErrLimit", err)
	}
}

// thirdNamespace is a consumer namespace that no case makes: the store keeps a
// consumer of another namespace with no look-up.
const thirdNamespace = namespace.Name("initech")

func testRulePurgeSameIDOtherNamespace(t *testing.T, h Harness) {
	t.Helper()
	needHook(t, h, "PurgeUnit")

	r := newRuleRig(t, h)
	a := r.unit("a", "")
	g := r.other("g", "")

	named := r.linkTo(a.Unit, consumerOf(g.Unit))
	// The same ID in a third namespace is another reference: the purge of the
	// unit of globex must not remove it, and must not list it in its audit entry.
	survivor := r.linkTo(a.Unit, foreignUnit(thirdNamespace, g.Unit.UnitID()))

	h.PurgeUnit(t, g)

	r.wantIDs(a.Unit, string(survivor.ID))

	if _, err := h.Store.Get(t.Context(), a.Unit, named.ID); !errors.Is(err, outputshare.ErrNotFound) {
		t.Fatalf("Get of the rule that named the purged unit = %v, want ErrNotFound", err)
	}
}

func testRuleCollectionDeleteSameIDOtherNamespace(t *testing.T, h Harness) {
	t.Helper()
	needHook(t, h, "RemakeCollection", "MoveUnit")

	r := newRuleRig(t, h)
	a := r.unit("a", "")
	web := r.other("web", "shared")

	named := r.linkTo(a.Unit, consumerOf(collectionOf(web)))
	survivor := r.linkTo(
		a.Unit,
		outputshare.Consumer{
			Kind:      outputshare.ConsumerCollection,
			Namespace: thirdNamespace,
			ID:        collectionOf(web).CollectionID(),
		},
	)

	h.MoveUnit(t, web, "elsewhere")
	h.RemakeCollection(t, globex, "shared")

	r.wantIDs(a.Unit, string(survivor.ID))

	if _, err := h.Store.Get(t.Context(), a.Unit, named.ID); !errors.Is(err, outputshare.ErrNotFound) {
		t.Fatalf("Get of the rule that named the deleted collection = %v, want ErrNotFound", err)
	}
}

func testRuleCollectionDelete(t *testing.T, h Harness) {
	t.Helper()
	needHook(t, h, "RemakeCollection", "MoveUnit")

	r := newRuleRig(t, h)
	a, c := r.unit("a", "shared"), r.unit("c", "")

	// The rule that names the collection as consumer, and the rule that has the
	// collection as producer.
	r.link(c.Unit, collectionOf(a))
	r.link(collectionOf(a), c.Unit)

	h.MoveUnit(t, a, "elsewhere")
	again := h.RemakeCollection(t, r.ns, "shared")

	r.wantIDs(again)

	// The reader leaves out a rule of a collection that is gone, so the count of
	// the limit shows whether the delete removed it. The producer c holds 99
	// rules after the loop only when the rule that named the old collection is
	// gone, and so has room for the last one.
	for i := range outputshare.MaxRulesPerProducer - 1 {
		r.linkTo(c.Unit, foreignUnit(globex, fmt.Sprintf("ws-other-%03d", i)))
	}

	r.linkTo(c.Unit, foreignUnit(globex, "ws-other-last"))
}

func testRuleCollectionDeleteCrossNamespace(t *testing.T, h Harness) {
	t.Helper()
	needHook(t, h, "RemakeCollection", "MoveUnit")

	r := newRuleRig(t, h)
	a := r.unit("a", "")
	web := r.other("web", "shared")

	named := r.linkTo(a.Unit, consumerOf(collectionOf(web)))

	h.MoveUnit(t, web, "elsewhere")
	h.RemakeCollection(t, globex, "shared")

	if _, err := h.Store.Get(t.Context(), a.Unit, named.ID); !errors.Is(err, outputshare.ErrNotFound) {
		t.Fatalf("Get of the rule that named the deleted collection = %v, want ErrNotFound", err)
	}

	r.wantIDs(a.Unit)
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

func testFindCrossByID(t *testing.T, h Harness) {
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

	byUnit := r.link(a1.Unit, web.Unit)
	byCollection := r.link(a2.Unit, collectionOf(inShared))

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
		"created_by, created_at_ms"

	const columns = 9

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

	return []any{
		r.Producer.Namespace().String(), string(r.ID), kind(r.Producer), stable(r.Producer),
		string(r.Consumer.Kind), r.Consumer.Namespace.String(), r.Consumer.ID, r.CreatedBy, int64(1),
	}
}

// HostileRuleRow is one row for the table test of the CHECK constraints.
type HostileRuleRow struct {
	// Name says what the row names.
	Name string
	// Row holds the namespace, the rule ID, the kind and the stable ID of the
	// producer, the kind, the namespace and the stable ID of the consumer, and
	// the principal that saved the rule.
	Row [8]string
	// OK is true for the rows that the table accepts.
	OK bool
}

// HostileRuleRows are rows that the CHECK constraints of the rule table
// refuse, each for one reason, and the rows that they accept.
func HostileRuleRows() []HostileRuleRow {
	acmeName, globexName := acme.String(), globex.String()
	longID := strings.Repeat("a", outputshare.MaxIDBytes+1)

	row := func(name string, ok bool, cols ...string) HostileRuleRow {
		r := HostileRuleRow{Name: name, Row: [8]string{}, OK: ok}
		copy(r.Row[:], cols)

		return r
	}

	return []HostileRuleRow{
		row("a unit that names a unit of its namespace", true,
			acmeName, "r1", kindUnit, "u1", kindUnit, acmeName, "u2", tester),
		row("a unit that names a unit of another namespace by its ID", true,
			acmeName, "r2", kindUnit, "u1", kindUnit, globexName, "u9", tester),
		row("a unit that names a collection of another namespace by its ID", true,
			acmeName, "r2b", kindUnit, "u1", kindCollection, globexName, "c9", tester),
		row("a unit that names the namespace of the producer", true,
			acmeName, "r2c", kindUnit, "u1", kindNamespace, acmeName, "", tester),
		row("a producer that is a principal", false,
			acmeName, "r3", "principal", "p1", kindUnit, acmeName, "u2", tester),
		row("a consumer that is a principal", false,
			acmeName, "r4", kindUnit, "u1", "principal", acmeName, "p1", tester),
		row("a namespace producer with a stable ID", false,
			acmeName, "r5", kindNamespace, "x", kindUnit, acmeName, "u2", tester),
		row("a unit producer with no stable ID", false,
			acmeName, "r6", kindUnit, "", kindUnit, acmeName, "u2", tester),
		row("a whole other namespace as the consumer", false,
			acmeName, "r7", kindUnit, "u1", kindNamespace, globexName, "", tester),
		row("a namespace consumer with an ID", false,
			acmeName, "r7b", kindUnit, "u1", kindNamespace, acmeName, "u2", tester),
		row("a consumer of the namespace with no stable ID", false,
			acmeName, "r8", kindUnit, "u1", kindUnit, acmeName, "", tester),
		row("a consumer of another namespace with no stable ID", false,
			acmeName, "r10", kindUnit, "u1", kindUnit, globexName, "", tester),
		row("a collection consumer of another namespace with no stable ID", false,
			acmeName, "r12", kindUnit, "u1", kindCollection, globexName, "", tester),
		row("a rule with no creator", false,
			acmeName, "r14", kindUnit, "u1", kindUnit, acmeName, "u2", ""),
		row("a rule ID with a capital letter", false,
			acmeName, "R15", kindUnit, "u1", kindUnit, acmeName, "u2", tester),
		row("a rule ID with a space", false,
			acmeName, "r 16", kindUnit, "u1", kindUnit, acmeName, "u2", tester),
		row("a rule ID that is empty", false,
			acmeName, "", kindUnit, "u1", kindUnit, acmeName, "u2", tester),
		row("a rule ID that is too long", false,
			acmeName, longID, kindUnit, "u1", kindUnit, acmeName, "u2", tester),
	}
}
