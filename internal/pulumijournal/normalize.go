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
// This file is derived from the following code of pulumi/pulumi at commit
// 128d3f2ec94e147915a6a928fdf7449d561d560e (tag v3.268.0):
//
//   - pkg/resource/deploy/snapshot.go: (*Snapshot).NormalizeURNReferences
//     (lines 567 to 655, the alias map, fixUrn, fixProvider, fixResource and the
//     snippet rewrite) and withUpdatedResources (line 864).
//   - pkg/resource/deploy/state_builder.go: stateBuilder, withUpdatedURN,
//     withAllUpdatedDependencies, withUpdatedAliases, build, setString and
//     setURN.
//   - pkg/resource/state.go: State.GetAllDependencies (line 366).
//
// OpenDustpan changed the input and the output: the functions work on the link
// index (linkFields) of the update, not on a Snapshot. Upstream asserts on a
// malformed URN or provider reference, and the port returns an error instead.

package pulumijournal

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
)

// ErrAliasConflict is returned when two resources declare the same alias with
// different URNs (upstream NormalizeURNReferences).
var ErrAliasConflict = errors.New("pulumijournal: two resources are aliased to the same URN")

// normalizer maps each URN that an alias names to the URN of the resource that
// declares the alias. A URN that is not a key keeps its value. The zero value
// maps every URN to itself.
type normalizer struct {
	aliased map[string]string
}

// urn returns the URN that u is normalized to (upstream fixUrn).
func (n normalizer) urn(u string) string {
	if newURN, ok := n.aliased[u]; ok {
		return newURN
	}
	return u
}

// provider returns the provider reference with its URN normalized. The ID is
// kept (upstream fixProvider).
func (n normalizer) provider(p string) (string, error) {
	ref, err := parseProviderReference(p)
	if err != nil {
		return "", fmt.Errorf("pulumijournal: normalize provider reference: %w", err)
	}
	fixed, err := newProviderReference(n.urn(ref.urn), ref.id)
	if err != nil {
		return "", fmt.Errorf("pulumijournal: normalize provider reference: %w", err)
	}
	return fixed.String(), nil
}

// normalizeIndex normalizes the URN references in the link index of the update, after
// the rebuild when the update has a refresh (see rebuildIndex). It returns the
// normalizer, which also rewrites the snippets.
func normalizeIndex(index []linkFields) (normalizer, error) {
	aliased, err := aliasMap(index)
	if err != nil {
		return normalizer{}, err
	}
	n := normalizer{aliased: aliased}
	for i := range index {
		next, err := n.links(index[i])
		if err != nil {
			return normalizer{}, err
		}
		index[i] = next
	}
	return n, nil
}

// rebuildIndex rebuilds the links of each resource in the order of the output,
// as upstream rebuildDependencies does (see links.go).
func rebuildIndex(index []linkFields, final map[string]bool) {
	referenceable := make(map[string]bool)
	available := make(map[string]string)
	for i := range index {
		rebuildLinks(&index[i], referenceable)
		referenceable[index[i].URN] = true
		undangleParent(&index[i], final, available)
	}
}

// aliasMap returns the map from each alias to the URN of its resource. A resource
// whose parent is aliased gets a URN with the parent's type (upstream
// NormalizeURNReferences, first loop).
func aliasMap(index []linkFields) (map[string]string, error) {
	aliased := make(map[string]string)
	for i := range index {
		if err := addAliases(aliased, &index[i]); err != nil {
			return nil, err
		}
		if err := aliasChild(aliased, &index[i]); err != nil {
			return nil, err
		}
	}
	return aliased, nil
}

// addAliases maps each alias of state to the URN of state.
func addAliases(aliased map[string]string, state *linkFields) error {
	for _, alias := range state.Aliases {
		// Some SDKs create the same alias to the same resource more than once.
		// That is allowed. Only a different target is an error.
		if other, has := aliased[alias]; has && other != state.URN {
			return fmt.Errorf("%w: %q and %q are aliased to %q", ErrAliasConflict, other, state.URN, alias)
		}
		aliased[alias] = state.URN
	}
	return nil
}

// aliasChild gives state a URN with the type of its aliased parent, when the
// parent is not the root stack.
func aliasChild(aliased map[string]string, state *linkFields) error {
	parent, has := aliased[state.Parent]
	if !has || parent == "" {
		return nil
	}
	if err := requireURN(parent); err != nil {
		return fmt.Errorf("pulumijournal: normalize parent %q: %w", parent, err)
	}
	if urnQualifiedType(parent) == rootStackType {
		return nil
	}
	if err := requireURN(state.URN); err != nil {
		return fmt.Errorf("pulumijournal: normalize resource %q: %w", state.URN, err)
	}
	aliased[state.URN] = newURN(
		urnStack(state.URN), urnProject(state.URN),
		urnQualifiedType(parent), urnType(state.URN), urnName(state.URN),
	)
	return nil
}

// links returns the links of one resource with its URN references normalized and
// its aliases removed. The input is not changed in place (upstream builds a new
// state, and its state builder copies the original).
func (n normalizer) links(in linkFields) (linkFields, error) {
	out := in
	out.URN = n.urn(in.URN)
	if err := n.dependencies(&out); err != nil {
		return linkFields{}, err
	}
	out.Aliases = nil
	return out, nil
}

// dependencies normalizes the provider and the dependencies of one resource, as
// upstream withAllUpdatedDependencies does. The replaceWith entries are rewritten
// once for each non-empty entry, because upstream loops over the dependency list
// that way. Each rewrite maps the same entries, so the repeats only matter when an
// alias maps to another alias.
func (n normalizer) dependencies(links *linkFields) error {
	if links.Provider != "" {
		provider, err := n.provider(links.Provider)
		if err != nil {
			return err
		}
		links.Provider = provider
	}
	if links.Parent != "" {
		links.Parent = n.urn(links.Parent)
	}
	if links.DeletedWith != "" {
		links.DeletedWith = n.urn(links.DeletedWith)
	}
	for range countNonEmpty(links.ReplaceWith) {
		for i, replaceWith := range links.ReplaceWith {
			links.ReplaceWith[i] = n.urn(replaceWith)
		}
	}
	// Upstream keeps the dependencies and the property dependencies as they are
	// when nothing changed. Only a change replaces them, and then the empty URNs
	// are dropped.
	if fixed, edited := n.fixURNs(links.Dependencies); edited {
		links.Dependencies = fixed
	}
	n.propertyDependencies(links)
	return nil
}

// propertyDependencies normalizes the property dependencies of one resource. A
// key with no URN left is dropped, as upstream does when it changed the map.
func (n normalizer) propertyDependencies(links *linkFields) {
	fixed := make(map[string][]string)
	edited := false
	for key, deps := range links.PropertyDependencies {
		urns, changed := n.fixURNs(deps)
		if len(urns) > 0 {
			fixed[key] = urns
		}
		edited = edited || changed
	}
	if edited {
		links.PropertyDependencies = fixed
	}
}

// fixURNs returns the normalized URNs without the empty ones, and whether a URN
// changed.
func (n normalizer) fixURNs(urns []string) ([]string, bool) {
	var fixed []string
	edited := false
	for _, u := range urns {
		if u == "" {
			continue
		}
		newURN := n.urn(u)
		fixed = append(fixed, newURN)
		if newURN != u {
			edited = true
		}
	}
	return fixed, edited
}

// countNonEmpty returns the number of entries that are not empty.
func countNonEmpty(urns []string) int {
	count := 0
	for _, u := range urns {
		if u != "" {
			count++
		}
	}
	return count
}

// snippetRefs holds the references of one snippet. Upstream rewrites them with
// the same normalization as the resources.
type snippetRefs struct {
	References map[string]string `json:"references,omitempty"`
}

// snippets returns the snippets with their references normalized. A snippet with
// no change keeps its raw value.
func (n normalizer) snippets(in []jsontext.Value) ([]jsontext.Value, error) {
	out := make([]jsontext.Value, len(in))
	for i, raw := range in {
		out[i] = raw
		var refs snippetRefs
		if err := json.Unmarshal(raw, &refs); err != nil {
			return nil, fmt.Errorf("pulumijournal: read snippet references: %w", err)
		}
		fixed := make(map[string]string, len(refs.References))
		changed := false
		for key, value := range refs.References {
			newValue := n.urn(value)
			fixed[key] = newValue
			if newValue != value {
				changed = true
			}
		}
		if !changed {
			continue
		}
		patched, err := patchObject(raw, map[string]any{"references": fixed})
		if err != nil {
			return nil, err
		}
		out[i] = patched
	}
	return out, nil
}
