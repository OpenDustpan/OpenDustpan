package catalog_test

import (
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/catalog"
	"opendustpan.org/opendustpan/pkg/gate"
)

func TestBlueprintNameBoundIsTheCatalogBound(t *testing.T) {
	t.Parallel()

	if gate.MaxBlueprintNameBytes != catalog.MaxNameBytes {
		t.Fatalf("gate.MaxBlueprintNameBytes = %d, catalog.MaxNameBytes = %d, want equal",
			gate.MaxBlueprintNameBytes, catalog.MaxNameBytes)
	}
}

func TestBlueprintScopeAcceptsExactlyTheCatalogNames(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"web", "web-2", "a", "", "Web", "1web", "-web", "web_x", "web.x", "wéb",
		strings.Repeat("a", 63), strings.Repeat("a", 64),
	} {
		_, err := gate.BlueprintScope(name, 1)
		if got, want := err == nil, catalog.ValidName(name); got != want {
			t.Errorf("BlueprintScope(%q, 1) succeeds = %v, catalog.ValidName = %v", name, got, want)
		}
	}
}
