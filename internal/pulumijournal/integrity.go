// Copyright 2025, Pulumi Corporation.
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
// This file is derived from pkg/resource/stack/snapshot/integrity.go
// (VerifyIntegrity, SnapshotIntegrityError, SnapshotIntegrityErrorf, ForRead,
// ForReadWithMetadata, AsSnapshotIntegrityError and the write and read operation
// values) and from pkg/backend/journal.go (saveSnapshot, lines 591 to 601: run the
// check on the new deployment, clear the integrity error metadata when the check
// passes and set it with the version, the command, the error text and the environment
// variables when it fails) of pulumi/pulumi at commit
// 128d3f2ec94e147915a6a928fdf7449d561d560e (tag v3.268.0). It ports these functions:
// VerifyIntegrity, SnapshotIntegrityErrorf, ForRead, ForReadWithMetadata and
// AsSnapshotIntegrityError, with the helpers they call: integrityMagic (the manifest
// magic check), isProviderType (the provider type check), and the checks for the
// identity, provider, parent, dependencies, property dependencies, deleted-with,
// duplicate resources and snippet UUIDs.
//
// The port differs in these ways, each marked in the code:
//
//   - The check runs on IntegrityDeployment and its member types, which carry the same
//     members that the check reads, instead of the upstream deployment types. The JSON
//     names of the error metadata match the upstream exchange names.
//   - The error metadata written on failure holds the schema version of the output and
//     the error text only. The server has no command line of its own for the update, so
//     it writes no command and no environment variables.
//   - A child whose URN does not extend the parent type only warns upstream. The port
//     skips that warning and returns no error for it, as upstream does.
//   - ForRead with no deployment keeps no metadata instead of failing, and the port
//     never panics: malformed URNs and provider references return errors.

package pulumijournal

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"runtime/debug"
)

// IntegrityManifest carries the manifest members that the integrity check reads.
type IntegrityManifest struct {
	Magic   string
	Version string
}

// IntegrityResource carries the resource members that the integrity check reads.
type IntegrityResource struct {
	URN                  string
	ResourceType         string
	Custom               bool
	ID                   string
	Provider             string
	Parent               string
	Dependencies         []string
	PropertyDependencies map[string][]string
	DeletedWith          string
	Delete               bool
	PendingReplacement   bool
}

// IntegritySnippet carries the snippet members that the integrity check reads.
type IntegritySnippet struct {
	UUID string
}

// IntegrityErrorMetadata describes a failed integrity check. Port difference: it
// holds the schema version of the output and the error text only. The server has no
// command line of its own for the update, so it writes no command and no environment
// variables.
type IntegrityErrorMetadata struct {
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

// IntegrityDeployment carries the deployment members that the integrity check reads.
type IntegrityDeployment struct {
	Manifest  IntegrityManifest
	Resources []IntegrityResource
	Snippets  []IntegritySnippet
	Metadata  *IntegrityErrorMetadata
}

// SnapshotIntegrityOperation names when an integrity check ran.
type SnapshotIntegrityOperation int

const (
	// SnapshotIntegrityWrite marks a check that ran when the deployment was written.
	SnapshotIntegrityWrite SnapshotIntegrityOperation = 0
	// SnapshotIntegrityRead marks a check that ran when the deployment was read.
	SnapshotIntegrityRead SnapshotIntegrityOperation = 1
)

// SnapshotIntegrityError reports a malformed or invalid deployment.
type SnapshotIntegrityError struct {
	// Err is the underlying cause of the failure, if any.
	Err error
	// Op names when the check ran.
	Op SnapshotIntegrityOperation
	// Stack is the stack trace at the point the error was raised.
	Stack []byte
	// Metadata describes the write that stored the bad deployment, if known.
	Metadata *IntegrityErrorMetadata
	// AutoRepairErr reports a failed automatic repair of this error, if one ran.
	AutoRepairErr error
}

// SnapshotIntegrityErrorf returns a write-time integrity error with a formatted message.
func SnapshotIntegrityErrorf(format string, args ...any) error {
	return &SnapshotIntegrityError{
		Err:           fmt.Errorf(format, args...),
		Op:            SnapshotIntegrityWrite,
		Stack:         debug.Stack(),
		Metadata:      nil,
		AutoRepairErr: nil,
	}
}

// Error returns the message of the underlying cause.
func (s *SnapshotIntegrityError) Error() string {
	if s.Err == nil {
		return "snapshot integrity error"
	}

	return s.Err.Error()
}

// Unwrap returns the underlying cause.
func (s *SnapshotIntegrityError) Unwrap() error {
	return s.Err
}

// ForRead returns a copy of the error marked as read-time, with the metadata of the
// deployment that was read. Port difference: a missing deployment keeps no metadata
// instead of failing.
func (s *SnapshotIntegrityError) ForRead(snap *IntegrityDeployment) *SnapshotIntegrityError {
	var metadata *IntegrityErrorMetadata
	if snap != nil {
		metadata = snap.Metadata
	}

	return &SnapshotIntegrityError{
		Err:           s.Err,
		Op:            SnapshotIntegrityRead,
		Stack:         s.Stack,
		Metadata:      metadata,
		AutoRepairErr: s.AutoRepairErr,
	}
}

// ForReadWithMetadata returns a copy of the error marked as read-time with the given metadata.
func (s *SnapshotIntegrityError) ForReadWithMetadata(
	metadata *IntegrityErrorMetadata,
) *SnapshotIntegrityError {
	return &SnapshotIntegrityError{
		Err:           s.Err,
		Op:            SnapshotIntegrityRead,
		Stack:         s.Stack,
		Metadata:      metadata,
		AutoRepairErr: s.AutoRepairErr,
	}
}

// AsSnapshotIntegrityError returns the first integrity error in the tree of err, if any.
func AsSnapshotIntegrityError(err error) (*SnapshotIntegrityError, bool) {
	return errors.AsType[*SnapshotIntegrityError](err)
}

// VerifyIntegrity checks that the deployment is well formed: the manifest magic
// matches, every resource names its type, every link names a resource that comes
// before it, no URN repeats without a delete mark, and every snippet names a UUID.
func VerifyIntegrity(snap *IntegrityDeployment) error {
	if snap == nil {
		return nil
	}

	if snap.Manifest.Magic != integrityMagic(snap.Manifest.Version) {
		return SnapshotIntegrityErrorf("magic cookie mismatch: possible tampering/corruption detected")
	}

	checker := &integrityChecker{
		resources: snap.Resources,
		seen:      make(map[string][]IntegrityResource, len(snap.Resources)),
		providers: make(map[providerReference]struct{}),
	}

	if err := checker.checkAll(); err != nil {
		return err
	}

	if err := checkIntegrityDuplicates(checker.seen); err != nil {
		return err
	}

	return checkIntegritySnippets(snap.Snippets)
}

// integrityMagic returns the magic of a manifest version: the hex SHA-256 of the
// version, empty when the version is empty.
func integrityMagic(version string) string {
	if version == "" {
		return ""
	}

	sum := sha256.Sum256([]byte(version))

	return hex.EncodeToString(sum[:])
}

// isProviderType reports whether a type token names a provider.
func isProviderType(typ string) bool {
	module, err := typeModule(typ)
	if err != nil {
		return false
	}

	if module != "pulumi:providers" {
		return false
	}

	name, err := typeName(typ)
	if err != nil || name == "" {
		return false
	}

	return true
}

// integrityChecker holds the state of one integrity check: the resources in output
// order, the resources seen so far by URN, and the provider references seen so far.
type integrityChecker struct {
	resources []IntegrityResource
	seen      map[string][]IntegrityResource
	providers map[providerReference]struct{}
}

// checkAll checks every resource in order.
func (c *integrityChecker) checkAll() error {
	for i := range c.resources {
		if err := c.checkResource(i); err != nil {
			return err
		}
	}

	return nil
}

// checkResource checks one resource and records it as seen.
func (c *integrityChecker) checkResource(i int) error {
	res := c.resources[i]

	if err := c.checkIdentity(i); err != nil {
		return err
	}

	if err := c.checkProvider(res); err != nil {
		return err
	}

	if err := c.checkParent(res, i); err != nil {
		return err
	}

	if err := c.checkDependencies(res, i); err != nil {
		return err
	}

	if err := c.checkPropertyDependencies(res, i); err != nil {
		return err
	}

	if err := c.checkDeletedWith(res, i); err != nil {
		return err
	}

	c.seen[res.URN] = append(c.seen[res.URN], res)

	return nil
}

// checkIdentity checks the URN, the type, the ID and the provider registration of
// the resource at index i.
func (c *integrityChecker) checkIdentity(i int) error {
	res := c.resources[i]

	if res.URN == "" {
		return SnapshotIntegrityErrorf("resource at index %d missing required 'urn' field", i)
	}

	if res.ResourceType == "" {
		return SnapshotIntegrityErrorf("resource '%s' missing required 'type' field", res.URN)
	}

	if !res.Custom && res.ID != "" {
		return SnapshotIntegrityErrorf("resource '%s' has 'custom false but non-empty ID", res.URN)
	}

	if !isProviderType(res.ResourceType) {
		return nil
	}

	ref, err := newProviderReference(res.URN, res.ID)
	if err != nil {
		return SnapshotIntegrityErrorf("provider %s is not referenceable: %w", res.URN, err)
	}

	c.providers[ref] = struct{}{}

	return nil
}

// checkProvider checks that the provider of res parses and was seen, unless the
// resource is pending replacement.
func (c *integrityChecker) checkProvider(res IntegrityResource) error {
	if res.Provider == "" {
		return nil
	}

	ref, err := parseProviderReference(res.Provider)
	if err != nil {
		return SnapshotIntegrityErrorf("failed to parse provider reference for resource %s: %w", res.URN, err)
	}

	if _, has := c.providers[ref]; !has && !res.PendingReplacement {
		return SnapshotIntegrityErrorf("resource %s refers to unknown provider %s", res.URN, ref)
	}

	return nil
}

// checkParent checks that the parent of res was seen. Port difference: the check of
// the child URN against the parent type only warns upstream, so the port skips it.
func (c *integrityChecker) checkParent(res IntegrityResource, i int) error {
	return c.checkReference(
		i,
		res.URN,
		res.Parent,
		"child resource %s's parent %s comes after it",
		"child resource %s refers to missing parent %s",
	)
}

// checkDependencies checks that every dependency of res was seen.
func (c *integrityChecker) checkDependencies(res IntegrityResource, i int) error {
	for _, dep := range res.Dependencies {
		if err := c.checkReference(
			i,
			res.URN,
			dep,
			"resource %s's dependency %s comes after it",
			"resource %s refers to missing dependency %s",
		); err != nil {
			return err
		}
	}

	return nil
}

// checkPropertyDependencies checks that every property dependency of res was seen.
func (c *integrityChecker) checkPropertyDependencies(res IntegrityResource, i int) error {
	for _, deps := range res.PropertyDependencies {
		for _, dep := range deps {
			if err := c.checkReference(
				i,
				res.URN,
				dep,
				"resource %s's property dependency %s comes after it",
				"resource %s refers to missing property dependency %s",
			); err != nil {
				return err
			}
		}
	}

	return nil
}

// checkDeletedWith checks that the deleted-with resource of res was seen.
func (c *integrityChecker) checkDeletedWith(res IntegrityResource, i int) error {
	return c.checkReference(
		i,
		res.URN,
		res.DeletedWith,
		"resource %s is specified as being deleted with %s, which comes after it",
		"resource %s is specified as being deleted with %s, which is missing",
	)
}

// checkReference checks that one link names a resource seen before index i. An empty
// link names nothing and passes. A link that comes later gives the after message,
// and a link that never comes gives the missing message.
func (c *integrityChecker) checkReference(i int, urn, dep, after, missing string) error {
	if dep == "" {
		return nil
	}

	if _, has := c.seen[dep]; has {
		return nil
	}

	if c.comesLater(dep, i) {
		return SnapshotIntegrityErrorf(after, urn, dep)
	}

	return SnapshotIntegrityErrorf(missing, urn, dep)
}

// comesLater reports whether urn names a resource after index i.
func (c *integrityChecker) comesLater(urn string, i int) bool {
	for _, other := range c.resources[i+1:] {
		if other.URN == urn {
			return true
		}
	}

	return false
}

// checkIntegrityDuplicates checks that a repeated URN is marked for deletion on all
// or all but one of its states.
func checkIntegrityDuplicates(seen map[string][]IntegrityResource) error {
	for urn, states := range seen {
		if len(states) == 1 {
			continue
		}

		deletes := 0

		for _, state := range states {
			if state.Delete {
				deletes++
			}
		}

		if deletes != len(states)-1 && deletes != len(states) {
			return SnapshotIntegrityErrorf("duplicate resource %s (not marked for deletion)", urn)
		}
	}

	return nil
}

// checkIntegritySnippets checks that every snippet names a UUID and no UUID repeats.
func checkIntegritySnippets(snippets []IntegritySnippet) error {
	seen := make(map[string]int, len(snippets))

	for i, snippet := range snippets {
		if snippet.UUID == "" {
			return SnapshotIntegrityErrorf("snippet at index %d missing required 'uuid' field", i)
		}

		if other, has := seen[snippet.UUID]; has {
			return SnapshotIntegrityErrorf("duplicate snippet uuid %q at indexes %d and %d", snippet.UUID, other, i)
		}

		seen[snippet.UUID] = i
	}

	return nil
}
