package collection_test

import (
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/collection"
)

// TestPlacementErrorNamesTheCommand checks that the no-match error names the
// command that adds a placement rule, and that it uses the word of the tool for
// the thing that it could not place.
func TestPlacementErrorNamesTheCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      collection.PlacementError
		want    []string
		notWant []string
	}{
		{
			name: "pulumi says stack",
			in:   collection.PlacementError{Namespace: "acme", Tool: "pulumi", Project: "demo", Stack: "dev"},
			want: []string{
				`pulumi stack "demo"/"dev"`, "namespace acme", "opendustpan placement add --tool pulumi",
			},
			notWant: []string{"workspace"},
		},
		{
			name: "opentofu says workspace",
			in:   collection.PlacementError{Namespace: "acme", Tool: "opentofu", Project: "*", Stack: "web"},
			want: []string{
				`opentofu workspace "web"`, "namespace acme", "opendustpan placement add --tool opentofu",
			},
			notWant: []string{"stack", `"*"`},
		},
		{
			name:    "unknown tool names no unit",
			in:      collection.PlacementError{Namespace: "acme", Tool: "other", Project: "p", Stack: "s"},
			want:    []string{"stack or workspace", "opendustpan placement add"},
			notWant: []string{"unit"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			text := tc.in.Error()
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Errorf("error text %q does not name %q", text, want)
				}
			}

			for _, bad := range tc.notWant {
				if strings.Contains(text, bad) {
					t.Errorf("error text %q has %q", text, bad)
				}
			}
		})
	}
}
