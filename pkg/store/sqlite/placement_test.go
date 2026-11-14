package sqlite_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/namespace"
)

const manageRules = "namespace:manage-placement-rules"

// placementFixture is the acme namespace with three collections and a
// placement service on the real store.
type placementFixture struct {
	t     *testing.T
	log   audit.Store
	svc   *collection.PlacementService
	colls *collection.Service
	ns    namespace.Name
}

func placementEntry(ns namespace.Name, action string) audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: ns, Subject: "admin", IDPSubject: "", TokenID: "",
		Action: action, Object: ns.String(), Version: "", ValuePath: "", ValueCount: nil,
		SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", PrevHash: "", Hash: "", Sequence: 0,
		Changes: nil, ObjectID: "", FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}
}

func newPlacementFixture(t *testing.T) *placementFixture {
	t.Helper()

	s := open(t)
	ctx := t.Context()

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.UnixMilli(1_700_000_000_000).UTC(), Name: "acme",
		PrimaryInstallation: "eu-west", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.CreateNamespace(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	colls, err := collection.NewService(collection.Config{Store: s, Namespaces: s, Now: nil})
	if err != nil {
		t.Fatalf("collection service: %v", err)
	}

	for _, name := range []string{"payments", "payments-prod", "sandbox"} {
		ref := collection.Ref{Namespace: ns.Name, Name: mustCollectionName(t, name)}
		if _, createErr := colls.Create(ctx, ref, placementEntry(ns.Name, "collection:create")); createErr != nil {
			t.Fatalf("create collection %q: %v", name, createErr)
		}
	}

	svc, err := collection.NewPlacementService(collection.PlacementConfig{Store: s, Namespaces: s, Now: nil})
	if err != nil {
		t.Fatalf("placement service: %v", err)
	}

	return &placementFixture{t: t, log: s, svc: svc, colls: colls, ns: ns.Name}
}

func (f *placementFixture) input(project, stack, coll string, pos int) collection.RuleInput {
	f.t.Helper()

	return collection.RuleInput{
		Tool: collection.ToolPulumi, Project: collection.Pattern(project), Stack: collection.Pattern(stack),
		Collection: mustCollectionName(f.t, coll), Position: pos,
	}
}

func (f *placementFixture) create(project, stack, coll string, pos int) (collection.Rule, error) {
	f.t.Helper()

	return f.svc.Create(f.t.Context(), f.ns, f.input(project, stack, coll, pos), placementEntry(f.ns, manageRules))
}

func (f *placementFixture) mustCreate(project, stack, coll string, pos int) collection.Rule {
	f.t.Helper()

	r, err := f.create(project, stack, coll, pos)
	if err != nil {
		f.t.Fatalf("create %s/%s: %v", project, stack, err)
	}

	return r
}

func (f *placementFixture) update(id string, in collection.RuleInput) error {
	f.t.Helper()

	_, err := f.svc.Update(f.t.Context(), f.ns, id, in, placementEntry(f.ns, manageRules))

	return err
}

func (f *placementFixture) order() []string {
	f.t.Helper()

	rules, err := f.svc.List(f.t.Context(), f.ns)
	if err != nil {
		f.t.Fatalf("List: %v", err)
	}

	out := make([]string, len(rules))
	for i := range rules {
		out[i] = rules[i].Project.String() + "/" + rules[i].Stack.String()
	}

	return out
}

func (f *placementFixture) wantOrder(want ...string) {
	f.t.Helper()

	if got := f.order(); !slices.Equal(got, want) {
		f.t.Errorf("order = %v, want %v", got, want)
	}
}

func (f *placementFixture) place(project, stack string) (collection.Rule, error) {
	f.t.Helper()

	return f.svc.Place(f.t.Context(), f.ns, collection.ToolPulumi, project, stack)
}

func (f *placementFixture) auditEntries() []audit.Entry {
	f.t.Helper()

	listing, err := f.log.List(f.t.Context(), f.ns, audit.Page{Number: 1, Size: 50})
	if err != nil {
		f.t.Fatalf("list audit: %v", err)
	}

	return listing.Entries
}

func TestPlacementNoMatch(t *testing.T) {
	t.Parallel()

	f := newPlacementFixture(t)

	_, err := f.place("payments", "prod")
	if !errors.Is(err, collection.ErrNoPlacement) || !strings.Contains(err.Error(), "placement-rules") {
		t.Fatalf("Place with no rule: err = %v, want ErrNoPlacement with the fix", err)
	}

	f.mustCreate("payments", "prod*", "payments-prod", 0)

	if _, err = f.place("web", "prod"); !errors.Is(err, collection.ErrNoPlacement) {
		t.Errorf("Place of an unknown project: err = %v, want ErrNoPlacement", err)
	}

	if _, err = f.place("payments", "dev"); !errors.Is(err, collection.ErrNoPlacement) {
		t.Errorf("Place of an unmatched stack: err = %v, want ErrNoPlacement", err)
	}
}

func TestPlacementOrderDecidesTheCollection(t *testing.T) {
	t.Parallel()

	f := newPlacementFixture(t)
	f.mustCreate("payments", "*", "payments", 0)
	f.mustCreate("payments", "prod*", "payments-prod", 1)
	f.mustCreate("billing", "*", "sandbox", 0)
	f.wantOrder("payments/prod*", "payments/*", "billing/*")

	tests := []struct{ project, stack, want string }{
		{"payments", "prod-eu", "payments-prod"},
		{"payments", "dev", "payments"},
	}
	for _, tt := range tests {
		got, err := f.place(tt.project, tt.stack)
		if err != nil || got.Collection.String() != tt.want {
			t.Errorf("Place(%s/%s) = %q, %v, want %q", tt.project, tt.stack, got.Collection, err, tt.want)
		}
	}
}

func TestPlacementConflicts(t *testing.T) {
	t.Parallel()

	f := newPlacementFixture(t)
	general := f.mustCreate("payments", "*", "payments", 0)

	// A narrower rule after a wider one never matches.
	if _, err := f.create("payments", "prod*", "payments-prod", 0); !errors.Is(err, collection.ErrPlacementConflict) {
		t.Errorf("create hidden rule: err = %v, want ErrPlacementConflict", err)
	}

	specific := f.mustCreate("payments", "prod*", "payments-prod", 1)

	// The same patterns twice is a conflict, whatever the collection.
	if _, err := f.create("payments", "prod*", "sandbox", 0); !errors.Is(err, collection.ErrPlacementConflict) {
		t.Errorf("create duplicate patterns: err = %v, want ErrPlacementConflict", err)
	}

	// A move that puts the wide rule first hides the specific rule.
	err := f.update(general.ID, f.input("payments", "*", "payments", 1))
	if !errors.Is(err, collection.ErrPlacementConflict) {
		t.Errorf("update that hides a later rule: err = %v, want ErrPlacementConflict", err)
	}

	// Widening the specific rule to cover the general one hides it too.
	err = f.update(specific.ID, f.input("payments", "*", "payments-prod", 0))
	if !errors.Is(err, collection.ErrPlacementConflict) {
		t.Errorf("update that hides the later rule: err = %v, want ErrPlacementConflict", err)
	}

	f.wantOrder("payments/prod*", "payments/*")
}

func TestPlacementInvalidInputNeverReachesTheStore(t *testing.T) {
	t.Parallel()

	f := newPlacementFixture(t)

	bad := []collection.RuleInput{
		f.input("(a+)+$", "*", "payments", 0),
		f.input("a", "x\ny", "payments", 0),
		f.input("a", "*", "payments", -1),
		f.input("a", "*", "payments", 9),
		f.input("a", "*", "nowhere", 0),
		{Tool: "terraform", Project: "a", Stack: "*", Collection: mustCollectionName(t, "payments"), Position: 0},
	}
	for i, b := range bad {
		_, err := f.svc.Create(t.Context(), f.ns, b, placementEntry(f.ns, manageRules))
		if err == nil {
			t.Errorf("create of bad input %d succeeded", i)
		}
	}

	f.wantOrder()
}

func TestPlacementWritesAreAudited(t *testing.T) {
	t.Parallel()

	f := newPlacementFixture(t)
	general := f.mustCreate("payments", "*", "payments", 0)
	specific := f.mustCreate("payments", "prod*", "payments-prod", 1)

	if err := f.update(specific.ID, f.input("payments", "prod-*", "payments-prod", 0)); err != nil {
		t.Fatalf("update: %v", err)
	}

	err := f.svc.Delete(t.Context(), f.ns, general.ID, placementEntry(f.ns, manageRules))
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	var withRule int

	for _, e := range f.auditEntries() {
		if e.Action == manageRules && strings.HasPrefix(e.ObjectID, "plr-") {
			withRule++
		}
	}

	if withRule != 4 {
		t.Errorf("audit entries of rule writes = %d, want 4 (2 creates, 1 update, 1 delete)", withRule)
	}
}

func TestPlacementCollectionDeleteTakesRulesAndAuditsThem(t *testing.T) {
	t.Parallel()

	f := newPlacementFixture(t)
	rule := f.mustCreate("payments", "prod*", "payments-prod", 0)

	ref := collection.Ref{Namespace: f.ns, Name: mustCollectionName(t, "payments-prod")}
	if err := f.colls.Delete(t.Context(), ref, placementEntry(f.ns, "collection:delete")); err != nil {
		t.Fatalf("delete collection: %v", err)
	}

	f.wantOrder()

	var named bool

	for _, e := range f.auditEntries() {
		if e.Action != "collection:delete" {
			continue
		}

		for _, c := range e.Changes {
			named = named || (c.Key == "placement-rule:"+rule.ID && c.New == "removed")
		}
	}

	if !named {
		t.Error("the audit entry of the collection delete does not name the deleted rule")
	}
}
