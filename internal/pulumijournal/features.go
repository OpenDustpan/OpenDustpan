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
// This file is derived from pkg/resource/stack/deployment.go (ApplyFeatures)
// and sdk/go/common/apitype/core.go (the schema version constants) of
// pulumi/pulumi at commit 128d3f2ec94e147915a6a928fdf7449d561d560e (tag
// v3.268.0). OpenDustpan changed the input: the function reads a resourceView
// instead of apitype.ResourceV3, and it takes no byte string flag: the caller
// calls markByteString. The feature names and the rules are unchanged.

package pulumijournal

// Schema versions of the Deployment document. The values are the ones at the
// pinned upstream commit. The upstream-watch job (issue #420) flags a change
// of these values.
const (
	// deploymentSchemaVersionCurrent is the version when no feature needs the latest version.
	deploymentSchemaVersionCurrent = 3
	// deploymentSchemaVersionLatest is the version when a feature needs it.
	deploymentSchemaVersionLatest = 4
)

// Feature names that a deployment can require of its reader. The names are
// the ones at the pinned upstream commit.
const (
	refreshBeforeUpdateFeature       = "refreshBeforeUpdate"
	viewsFeature                     = "views"
	hooksFeature                     = "hooks"
	taintFeature                     = "taint"
	replaceWithFeature               = "replaceWith"
	snippetsFeature                  = "snippets-prototype"
	extensionParameterizationFeature = "extensionParameterization"
	byteStringFeature                = "byteString"
)

// applyFeatures adds the features that one resource uses to the feature map.
// The byteString feature is not a rule of a resource: the caller sets it when a
// journal entry reports that its strings were encoded with bytes (see
// markByteString).
func applyFeatures(res resourceView, features map[string]bool) {
	if res.RefreshBeforeUpdate {
		features[refreshBeforeUpdateFeature] = true
	}
	if res.ViewOf != "" {
		features[viewsFeature] = true
	}
	if len(res.ResourceHooks) > 0 {
		features[hooksFeature] = true
	}
	if res.Taint {
		features[taintFeature] = true
	}
	if len(res.ReplaceWith) > 0 {
		features[replaceWithFeature] = true
	}
	if res.ExtensionRef != "" {
		features[extensionParameterizationFeature] = true
	}
	if res.SnippetID != "" {
		features[snippetsFeature] = true
	}
}

// markByteString adds the byteString feature. Byte strings inside secrets are
// encrypted away by serialization, so the journal reports them with the entry.
func markByteString(features map[string]bool) {
	features[byteStringFeature] = true
}

// schemaVersion returns the Deployment schema version for a feature set. It is
// the latest version when any feature is set, and the current version otherwise
// (upstream pkg/backend/journal.go, GenerateDeployment).
func schemaVersion(features map[string]bool) int {
	if len(features) > 0 {
		return deploymentSchemaVersionLatest
	}
	return deploymentSchemaVersionCurrent
}
