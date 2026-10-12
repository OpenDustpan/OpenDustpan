package pulumijournal

import "encoding/json/jsontext"

// resourceView is the part of a resource state that the replayer reads. The
// state itself stays raw JSON (ADR 43, decision 8.3). The fields are the JSON
// members of the upstream apitype.ResourceV3 that ApplyFeatures reads
// (pkg/resource/stack/deployment.go, lines 160 to 185, commit 128d3f2). Each
// field has a test in features_internal_test.go.
type resourceView struct {
	RefreshBeforeUpdate bool                      `json:"refreshBeforeUpdate,omitempty"`
	ViewOf              string                    `json:"viewOf,omitempty"`
	ResourceHooks       map[string]jsontext.Value `json:"resourceHooks,omitempty"`
	Taint               bool                      `json:"taint,omitempty"`
	ReplaceWith         []string                  `json:"replaceWith,omitempty"`
	ExtensionRef        string                    `json:"extensionRef,omitempty"`
	SnippetID           string                    `json:"snippetID,omitempty"`
}
