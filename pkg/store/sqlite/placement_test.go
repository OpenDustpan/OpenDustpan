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

func placementEntry(ns namespace.Name, action string) audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: ns, Subject: "admin", IDPSubject: "", TokenID: "",
		Action: action, Object: ns.String(), Version: "", ValuePath: "", ValueCount: nil,
		SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", PrevHash: "", Hash: "", Sequence: 0,
		Changes: nil, ObjectID: "", FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}
}

func TestPlacementService(t *testing.T) {
	t.Parallel()

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
		if _, err := colls.Create(ctx, ref, placementEntry(ns.Name, "collection:create")); err != nil {
			t.Fatalf("create collection %q: %v", name, err)
		}
	}

	svc, err := collection.NewPlacementService(collection.PlacementConfig{Store: s, Namespaces: s, Now: nil})
	if err != nil {
		t.Fatalf("placement service: %v", err)
	}

	in := func(project, stack, coll string, pos int) collection.RuleInput {
		return collection.RuleInput{
			Tool: collection.ToolPulumi, Project: collection.Pattern(project), Stack: collection.Pattern(stack),
			Collection: mustCollectionName(t, coll), Position: pos,
		}
	}
	create := func(project, stack, coll string, pos int) (collection.Rule, error) {
		return svc.Create(ctx, ns.Name, in(project, stack, coll, pos), placementEntry(ns.Name, "namespace:manage-placement-rules"))
	}
	order := func() []string {
		t.Helper()

		rules, err := svc.List(ctx, ns.Name)
		if err != nil {
			t.Fatalf("List: %v", err)
		}

		out := make([]string, len(rules))
		for i, r := range rules {
			out[i] = r.Project.String() + "/" + r.Stack.String()
		}

		return out
	}

	// No rule: nothing matches, and the error names the fix.
	_, err = svc.Place(ctx, ns.Name, collection.ToolPulumi, "payments", "prod")
	if !errors.Is(err, collection.ErrNoPlacement) || !strings.Contains(err.Error(), "placement-rules") {
		t.Fatalf("Place with no rule: err = %v, want ErrNoPlacement with the fix", err)
	}

	general, err := create("payments", "*", "payments", 0)
	if err != nil {
		t.Fatalf("create general rule: %v", err)
	}

	// A narrower rule after a wider one never matches: refused.
	_, err = create("payments", "prod*", "payments-prod", 0)
	if !errors.Is(err, collection.ErrPlacementConflict) {
		t.Fatalf("create hidden rule: err = %v, want ErrPlacementConflict", err)
	}

	// Inserted before the wider rule, it is accepted.
	specific, err := create("payments", "prod*", "payments-prod", 1)
	if err != nil {
		t.Fatalf("create specific rule first: %v", err)
	}

	if want := []string{"payments/prod*", "payments/*"}; !slices.Equal(order(), want) {
		t.Fatalf("order = %v, want %v", order(), want)
	}

	// The same patterns twice is a conflict, whatever the collection.
	if _, err := create("payments", "prod*", "sandbox", 0); !errors.Is(err, collection.ErrPlacementConflict) {
		t.Errorf("create duplicate patterns: err = %v, want ErrPlacementConflict", err)
	}

	// A move that puts the wide rule first hides the specific rule: refused.
	moveIn := in("payments", "*", "payments", 1)
	if _, err := svc.Update(ctx, ns.Name, general.ID, moveIn, placementEntry(ns.Name, "namespace:manage-placement-rules")); !errors.Is(err, collection.ErrPlacementConflict) {
		t.Errorf("update that hides a later rule: err = %v, want ErrPlacementConflict", err)
	}

	// Widening the specific rule to cover the general one is refused too.
	wide := in("payments", "*", "payments-prod", 0)
	if _, err := svc.Update(ctx, ns.Name, specific.ID, wide, placementEntry(ns.Name, "namespace:manage-placement-rules")); !errors.Is(err, collection.ErrPlacementConflict) {
		t.Errorf("update that hides the later rule: err = %v, want ErrPlacementConflict", err)
	}

	tests := []struct {
		project, stack, want string
	}{
		{"payments", "prod-eu", "payments-prod"},
		{"payments", "dev", "payments"},
	}
	for _, tt := range tests {
		got, err := svc.Place(ctx, ns.Name, collection.ToolPulumi, tt.project, tt.stack)
		if err != nil || got.Collection.String() != tt.want {
			t.Errorf("Place(%s/%s) = %q, %v, want %q", tt.project, tt.stack, got.Collection, err, tt.want)
		}
	}

	if _, err := svc.Place(ctx, ns.Name, collection.ToolPulumi, "web", "prod"); !errors.Is(err, collection.ErrNoPlacement) {
		t.Errorf("Place of an unknown project: err = %v, want ErrNoPlacement", err)
	}

	// Invalid input never reaches the store.
	bad := []collection.RuleInput{
		in("(a+)+$", "*", "payments", 0),
		in("a", "x\ny", "payments", 0),
		in("a", "*", "payments", -1),
		in("a", "*", "payments", 9),
		{Tool: "opentofu", Project: "a", Stack: "*", Collection: mustCollectionName(t, "payments"), Position: 0},
		in("a", "*", "nowhere", 0),
	}
	for i, b := range bad {
		if _, err := svc.Create(ctx, ns.Name, b, placementEntry(ns.Name, "namespace:manage-placement-rules")); err == nil {
			t.Errorf("create of bad input %d succeeded", i)
		}
	}

	if want := []string{"payments/prod*", "payments/*"}; !slices.Equal(order(), want) {
		t.Errorf("order after bad input = %v, want %v", order(), want)
	}

	// The write is audited with the rule ID and text.
	update := in("payments", "prod-*", "payments-prod", 0)
	if _, err := svc.Update(ctx, ns.Name, specific.ID, update, placementEntry(ns.Name, "namespace:manage-placement-rules")); err != nil {
		t.Fatalf("update: %v", err)
	}

	if err := svc.Delete(ctx, ns.Name, general.ID, placementEntry(ns.Name, "namespace:manage-placement-rules")); err != nil {
		t.Fatalf("delete: %v", err)
	}

	listing, err := s.List(ctx, ns.Name, audit.Page{Number: 1, Size: 50})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}

	var withRule int

	for _, e := range listing.Entries {
		if e.Action == "namespace:manage-placement-rules" && strings.HasPrefix(e.ObjectID, "plr-") {
			withRule++
		}
	}

	if withRule != 4 {
		t.Errorf("audit entries of rule writes = %d, want 4 (2 creates, 1 update, 1 delete)", withRule)
	}

	// Deleting a collection removes its rules and writes them to the audit entry.
	ref := collection.Ref{Namespace: ns.Name, Name: mustCollectionName(t, "payments-prod")}
	if err := colls.Delete(ctx, ref, placementEntry(ns.Name, "collection:delete")); err != nil {
		t.Fatalf("delete collection: %v", err)
	}

	if got := order(); len(got) != 0 {
		t.Errorf("rules after the delete of their collection = %v, want none", got)
	}

	listing, err = s.List(ctx, ns.Name, audit.Page{Number: 1, Size: 50})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}

	var named bool

	for _, e := range listing.Entries {
		if e.Action != "collection:delete" {
			continue
		}

		for _, c := range e.Changes {
			if c.Key == "placement-rule:"+specific.ID && c.New == "removed" {
				named = true
			}
		}
	}

	if !named {
		t.Error("the audit entry of the collection delete does not name the removed rule")
	}
}
