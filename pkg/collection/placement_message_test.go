package collection_test

import (
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestPlacementErrorNamesTheCommand checks that the no-match error names
// the command that adds a placement rule.
func TestPlacementErrorNamesTheCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		namespace namespace.Name
		want      []string
	}{
		{
			name:      "names the placement command",
			namespace: "acme",
			want: []string{
				"opendustpan placement add",
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
