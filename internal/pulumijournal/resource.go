// Copyright 2016, Pulumi Corporation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// This file is derived from sdk/go/common/apitype/core.go (the JSON names of
// the upstream apitype.ResourceV3) of pulumi/pulumi at commit
// 128d3f2ec94e147915a6a928fdf7449d561d560e (tag v3.268.0). OpenDustpan's struct
// names only the members that replay reads.

package pulumijournal

import "encoding/json/jsontext"

// resourceView is the part of a resource state that the replayer reads. The
// state itself stays raw JSON (ADR 43, decision 8.3). The fields are the members
// that ApplyFeatures reads (pkg/resource/stack/deployment.go, lines 160 to 185,
// commit 128d3f2). Each field has a test in features_internal_test.go.
type resourceView struct {
	RefreshBeforeUpdate bool                      `json:"refreshBeforeUpdate,omitempty"`
	ViewOf              string                    `json:"viewOf,omitempty"`
	ResourceHooks       map[string]jsontext.Value `json:"resourceHooks,omitempty"`
	Taint               bool                      `json:"taint,omitempty"`
	ReplaceWith         []string                  `json:"replaceWith,omitempty"`
	ExtensionRef        string                    `json:"extensionRef,omitempty"`
	SnippetID           string                    `json:"snippetID,omitempty"`
}
