package dustpan_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// placementStore is the part of a store that the catch-all seed writes to.
type placementStore interface {
	namespace.Store
	collection.Store
	collection.PlacementStore
}

// seedCatchAllPlacement makes a collection and a placement rule for Pulumi
// stacks of any project and any name in the namespace ns. The server refuses a
// new stack that no rule places, so a test that makes a stack through the API
// needs the rule.
func seedCatchAllPlacement(t *testing.T, store any, ns namespace.Name) {
	t.Helper()

	st, ok := store.(placementStore)
	if !ok {
		t.Fatalf("store %T keeps no collections or placement rules", store)
	}

	at := time.Unix(0, 0)
	entry := func(action identity.Action, object string) audit.Entry {
		e := storetest.ServerChange(at, object)
		e.Namespace = ns
		e.Action = string(action)

		return e
	}

	collections, err := collection.NewService(collection.Config{Store: st, Namespaces: st, Now: nil})
	if err != nil {
		t.Fatalf("collection service: %v", err)
	}

	const name = "everything"

	ref := collection.Ref{Namespace: ns, Name: name}
	created := entry(identity.ActionCollectionCreate, "collection:"+ns.String()+"/"+name)

	if _, createErr := collections.Create(t.Context(), ref, created); createErr != nil {
		t.Fatalf("create collection: %v", createErr)
	}

	rules, err := collection.NewPlacementService(collection.PlacementConfig{Store: st, Namespaces: st, Now: nil})
	if err != nil {
		t.Fatalf("placement service: %v", err)
	}

	in := collection.RuleInput{Tool: "pulumi", Project: "*", Stack: "*", Collection: name, Position: 0}
	ruled := entry(identity.ActionNamespaceManagePlacementRules, "namespace:"+ns.String())

	if _, ruleErr := rules.Create(t.Context(), ns, in, ruled); ruleErr != nil {
		t.Fatalf("create placement rule: %v", ruleErr)
	}
}
