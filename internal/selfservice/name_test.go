package selfservice_test

import (
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/selfservice"
	"opendustpan.org/opendustpan/pkg/gate"
)

func TestBlueprintNameBoundIsTheSelfServiceBound(t *testing.T) {
	t.Parallel()

	if gate.MaxBlueprintNameBytes != selfservice.MaxNameBytes {
		t.Fatalf("gate.MaxBlueprintNameBytes = %d, selfservice.MaxNameBytes = %d, want equal",
			gate.MaxBlueprintNameBytes, selfservice.MaxNameBytes)
	}
}

func TestBlueprintScopeAcceptsExactlyTheSelfServiceNames(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"web", "web-2", "a", "", "Web", "1web", "-web", "web_x", "web.x", "wéb",
		strings.Repeat("a", 63), strings.Repeat("a", 64),
	} {
		_, err := gate.BlueprintScope(name, 1)
		if got, want := err == nil, selfservice.ValidName(name); got != want {
			t.Errorf("BlueprintScope(%q, 1) succeeds = %v, selfservice.ValidName = %v", name, got, want)
		}
	}
}
