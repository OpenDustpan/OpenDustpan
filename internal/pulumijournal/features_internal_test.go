package pulumijournal

import (
	"encoding/json/jsontext"
	"maps"
	"testing"
)

// TestApplyFeatures checks that each rule sets its feature, and nothing else.
func TestApplyFeatures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		set  func(*resourceView)
		want string
	}{
		{
			name: "refresh before update",
			set:  func(r *resourceView) { r.RefreshBeforeUpdate = true },
			want: refreshBeforeUpdateFeature,
		},
		{
			name: "views",
			set:  func(r *resourceView) { r.ViewOf = "urn:pulumi:stack::project::type::parent" },
			want: viewsFeature,
		},
		{
			name: "hooks",
			set:  func(r *resourceView) { r.ResourceHooks = map[string]jsontext.Value{"beforeCreate": nil} },
			want: hooksFeature,
		},
		{name: "taint", set: func(r *resourceView) { r.Taint = true }, want: taintFeature},
		{
			name: "replace with",
			set:  func(r *resourceView) { r.ReplaceWith = []string{"urn:pulumi:stack::project::type::other"} },
			want: replaceWithFeature,
		},
		{
			name: "extension parameterization",
			set:  func(r *resourceView) { r.ExtensionRef = "ref" },
			want: extensionParameterizationFeature,
		},
		{name: "snippets", set: func(r *resourceView) { r.SnippetID = "snippet" }, want: snippetsFeature},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var res resourceView
			tt.set(&res)
			features := make(map[string]bool)
			applyFeatures(res, features)
			if !maps.Equal(features, map[string]bool{tt.want: true}) {
				t.Fatalf("features are %v, want only %s", features, tt.want)
			}
		})
	}
}

// TestApplyFeaturesNoRuleSetsNothing checks a resource that uses no feature.
func TestApplyFeaturesNoRuleSetsNothing(t *testing.T) {
	t.Parallel()
	var res resourceView
	features := make(map[string]bool)
	applyFeatures(res, features)
	if len(features) != 0 {
		t.Fatalf("features are %v, want none", features)
	}
}

// TestMarkByteString checks the byte string feature set by the caller.
func TestMarkByteString(t *testing.T) {
	t.Parallel()
	features := make(map[string]bool)
	markByteString(features)
	if !maps.Equal(features, map[string]bool{byteStringFeature: true}) {
		t.Fatalf("features are %v, want byteString", features)
	}
}

// TestSchemaVersion checks the version for a feature set: latest only when a feature is set.
func TestSchemaVersion(t *testing.T) {
	t.Parallel()
	if got := schemaVersion(map[string]bool{}); got != deploymentSchemaVersionCurrent {
		t.Fatalf("empty features give version %d, want %d", got, deploymentSchemaVersionCurrent)
	}
	if got := schemaVersion(map[string]bool{taintFeature: true}); got != deploymentSchemaVersionLatest {
		t.Fatalf("taint gives version %d, want %d", got, deploymentSchemaVersionLatest)
	}
}
