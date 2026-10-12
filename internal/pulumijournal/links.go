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
// This file is derived from pkg/backend/journal.go (rebuildDependencies and
// undangleParentResources) of pulumi/pulumi at commit
// 128d3f2ec94e147915a6a928fdf7449d561d560e (tag v3.268.0). OpenDustpan changed
// the input: the rules work on linkFields, and the resources stay raw JSON.

package pulumijournal

// linkFields are the members of a resource state that the rebuild of the
// dependencies reads and writes. The JSON names are the ones of the upstream
// apitype.ResourceV3 (sdk/go/common/apitype/core.go, commit 128d3f2). The line
// numbers below are lines of pkg/backend/journal.go at the same commit:
//
//   - urn: the rebuild records it as referenceable (line 385).
//   - dependencies: lines 355 to 358 (prune) and 379 to 380 (write).
//   - propertyDependencies: lines 360 to 366 (prune) and 382 to 383 (write).
//   - replaceWith: lines 367 to 374 (prune and write).
//   - deletedWith: lines 376 to 377.
//   - parent: undangleParentResources, lines 391 to 402.
//
// The URN normalization (normalize.go) also reads and writes these members:
//
//   - urn and aliases: NormalizeURNReferences and withUpdatedAliases (state
//     builder, withUpdatedAliases).
//   - provider: withAllUpdatedDependencies, which calls updateProviderRef.
//
// The features read the other members (resourceView). Upstream reads the same
// members of a state in GenerateDeployment through ApplyFeatures.
type linkFields struct {
	URN                  string              `json:"urn"`
	Parent               string              `json:"parent,omitempty"`
	Dependencies         []string            `json:"dependencies,omitempty"`
	PropertyDependencies map[string][]string `json:"propertyDependencies,omitempty"`
	ReplaceWith          []string            `json:"replaceWith,omitempty"`
	DeletedWith          string              `json:"deletedWith,omitempty"`
	Aliases              []string            `json:"aliases,omitempty"`
	Provider             string              `json:"provider,omitempty"`
}

// rebuildLinks rebuilds the dependencies of one resource, the way the upstream
// rebuildDependencies does in its first loop. referenceable holds the URNs of
// the resources before this one in the output order.
func rebuildLinks(links *linkFields, referenceable map[string]bool) {
	if len(links.Dependencies) > 0 {
		links.Dependencies = pruneURNs(links.Dependencies, referenceable)
	}
	if len(links.ReplaceWith) > 0 {
		links.ReplaceWith = pruneURNs(links.ReplaceWith, referenceable)
	}
	if len(links.PropertyDependencies) > 0 {
		links.PropertyDependencies = prunePropertyDependencies(links.PropertyDependencies, referenceable)
	}
	if !referenceable[links.DeletedWith] {
		links.DeletedWith = ""
	}
}

// pruneURNs keeps the URNs that are referenceable.
func pruneURNs(urns []string, referenceable map[string]bool) []string {
	kept := []string{}
	for _, urn := range urns {
		if referenceable[urn] {
			kept = append(kept, urn)
		}
	}
	return kept
}

// prunePropertyDependencies keeps the referenceable URNs of each property.
func prunePropertyDependencies(deps map[string][]string, referenceable map[string]bool) map[string][]string {
	kept := make(map[string][]string)
	for key, urns := range deps {
		for _, urn := range urns {
			if referenceable[urn] {
				kept[key] = append(kept[key], urn)
			}
		}
	}
	return kept
}

// undangleParent sets the parent of one resource to the nearest parent that is
// still in the deployment. undeleted holds the URNs of all resources in the
// final deployment. availableParents maps the URN of each earlier resource to
// its parent after the rebuild (journal.go 391 to 402).
func undangleParent(links *linkFields, undeleted map[string]bool, availableParents map[string]string) {
	// Upstream records the parent before this resource changes it, because its
	// loop variable is a copy. The same copy is kept here.
	original := links.Parent
	if _, ok := undeleted[original]; !ok {
		// The parent was deleted, so use the parent of the parent. The
		// availableParents lookup gives "" when the parent is not an earlier resource.
		links.Parent = availableParents[original]
	}
	availableParents[links.URN] = original
}
