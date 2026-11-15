package tfe_test

import (
	"context"

	"opendustpan.org/opendustpan/internal/api/tofuunit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// fixedRules places every new workspace in the collection "default". With
// none set it places nothing.
type fixedRules struct {
	none bool
}

func (r fixedRules) Place(
	_ context.Context, ns namespace.Name, tool, project, name string,
) (collection.Rule, error) {
	var rule collection.Rule

	if r.none {
		return rule, &collection.PlacementError{Namespace: ns, Tool: tool, Project: project, Stack: name}
	}

	rule.ID = "rule-1"
	rule.Tool = tool
	rule.Collection = "default"

	return rule, nil
}

// fixedCollections holds the one collection "default", or none.
type fixedCollections struct {
	missing bool
}

func (c fixedCollections) CollectionID(context.Context, identity.Object) (string, bool, error) {
	return "collection-id", !c.missing, nil
}

func (c fixedCollections) CollectionOf(context.Context, identity.Object) (string, bool, error) {
	return "default", !c.missing, nil
}

// testPlacer returns a Placer whose one rule puts every workspace in the
// collection "default".
func testPlacer() *tofuunit.Placer {
	return &tofuunit.Placer{Rules: fixedRules{none: false}, Collections: fixedCollections{missing: false}}
}
