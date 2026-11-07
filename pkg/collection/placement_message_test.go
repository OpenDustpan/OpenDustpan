package collection_test

import (
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestPlacementErrorNamesTheRoutes proves that the no-match error names the
// route that makes a collection and the route that puts a unit into it. Both
// routes are registered in internal/api/inventory.
func TestPlacementErrorNamesTheRoutes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		namespace namespace.Name
		want      []string
	}{
		{
			name:      "names the collection route and the unit route",
			namespace: "acme",
			want: []string{
				"POST /api/inventory/namespaces/acme/collections",
				"PUT /api/inventory/unit-collection",
				"POST /api/dustpan/v1/namespaces/acme/placement-rules",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := &collection.PlacementError{
				Namespace: tc.namespace, Tool: "pulumi", Project: "pay", Stack: "prod",
			}

			text := err.Error()
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Errorf("error text %q does not name %q", text, want)
				}
			}
		})
	}
}
