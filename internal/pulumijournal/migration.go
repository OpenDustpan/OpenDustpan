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
// This file is derived from pkg/backend/journal_state_migration.go
// (applyStateMigration) of pulumi/pulumi at commit
// 128d3f2ec94e147915a6a928fdf7449d561d560e (tag v3.268.0). OpenDustpan changed
// the replayer in these ways, each marked in the code:
//
//   - The base is read through the streaming reader instead of an in-memory
//     deployment. The migration holds the base resources while it runs, and
//     writes the migrated base back as a new readable base, as a rebuilt base
//     state entry does.
//   - A reference to an entry that is not applied is an error
//     (ErrMissingReference), as for the other entries that name new resources.
//     A new-state patch that names a removed operation is refused the same
//     way.
//   - A malformed resource state returns an error where upstream reads fields
//     that are always set. The port never panics.
//   - The prospective integrity check runs on the link index of the first pass
//     (see replay.go), as the output check does, instead of on the upstream
//     deployment types.

package pulumijournal

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"slices"
)

// ErrStateMigration reports a state migration entry that does not apply to the
// checkpoint built so far: a migration with pending work, a layout or patch
// that names no resource, or a result that fails the integrity check.
var ErrStateMigration = errors.New("pulumijournal: state migration entry does not apply")

// migrationStateView is the part of an inserted resource state that a state
// migration checks. The state itself stays raw JSON, as for the other entries.
type migrationStateView struct {
	URN          string `json:"urn"`
	Type         string `json:"type"`
	Custom       bool   `json:"custom"`
	ID           string `json:"id,omitempty"`
	Delete       bool   `json:"delete,omitempty"`
	ViewOf       string `json:"viewOf,omitempty"`
	ExtensionRef string `json:"extensionRef,omitempty"`
}

// baseDocument is the base deployment that a state migration rewrites: its
// resources in order, and the other members it keeps. The pending operations
// are kept apart, because any of them refuses the migration.
type baseDocument struct {
	resources []jsontext.Value
	members   map[string]jsontext.Value
	pending   []jsontext.Value
}

// readBaseDocument reads the whole base deployment: the resources in order and
// the other members. Port difference: upstream reads the in-memory deployment,
// while this port reads through the streaming reader and holds the resources
// until the migration is applied, as a rebuilt base state entry does.
func readBaseDocument(ctx context.Context, openBase OpenBase) (*baseDocument, error) {
	doc := &baseDocument{resources: nil, members: make(map[string]jsontext.Value), pending: nil}
	err := walkBase(ctx, openBase, func(_ int, raw jsontext.Value) error {
		if cerr := ctx.Err(); cerr != nil {
			return fmt.Errorf("pulumijournal: replay canceled: %w", cerr)
		}
		doc.resources = append(doc.resources, raw)
		return nil
	}, func(key string, raw jsontext.Value) error {
		if cerr := ctx.Err(); cerr != nil {
			return fmt.Errorf("pulumijournal: replay canceled: %w", cerr)
		}
		if key == "pending_operations" {
			if present(raw) {
				if uerr := json.Unmarshal(raw, &doc.pending); uerr != nil {
					return fmt.Errorf("pulumijournal: read base pending operations: %w", uerr)
				}
			}
			return nil
		}
		doc.members[key] = raw
		return nil
	})
	if err != nil {
		return nil, err
	}
	return doc, nil
}

// encodeBaseDocument writes the base deployment with resources in place of the
// resources member. The other members keep their raw values. The member order
// is sorted, so the same migration gives the same bytes.
func encodeBaseDocument(members map[string]jsontext.Value, resources []jsontext.Value) ([]byte, error) {
	var buf bytes.Buffer
	enc := jsontext.NewEncoder(&buf)
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return nil, fmt.Errorf("pulumijournal: write migrated base: %w", err)
	}
	if err := enc.WriteToken(jsontext.String("resources")); err != nil {
		return nil, fmt.Errorf("pulumijournal: write migrated base: %w", err)
	}
	if err := enc.WriteToken(jsontext.BeginArray); err != nil {
		return nil, fmt.Errorf("pulumijournal: write migrated base: %w", err)
	}
	for _, raw := range resources {
		if err := enc.WriteValue(raw); err != nil {
			return nil, fmt.Errorf("pulumijournal: write migrated base: %w", err)
		}
	}
	if err := enc.WriteToken(jsontext.EndArray); err != nil {
		return nil, fmt.Errorf("pulumijournal: write migrated base: %w", err)
	}
	for _, key := range slices.Sorted(maps.Keys(members)) {
		if err := writeKeyRaw(enc, key, members[key]); err != nil {
			return nil, err
		}
	}
	if err := enc.WriteToken(jsontext.EndObject); err != nil {
		return nil, fmt.Errorf("pulumijournal: write migrated base: %w", err)
	}
	return buf.Bytes(), nil
}

// remapIndexSet maps a set of base indices to the positions of the migrated
// base. Indices that the layout drops are gone (upstream remapSet).
func remapIndexSet(set map[int64]struct{}, positions map[int64]int64) map[int64]struct{} {
	remapped := make(map[int64]struct{}, len(set))
	for index := range set {
		if next, ok := positions[index]; ok {
			remapped[next] = struct{}{}
		}
	}
	return remapped
}

// baseExtensionKeys returns the extension references of the base deployment.
// It returns an empty set when the base holds none.
func baseExtensionKeys(members map[string]jsontext.Value) (map[string]struct{}, error) {
	keys := make(map[string]struct{})
	raw := members["extensions"]
	if !present(raw) {
		return keys, nil
	}
	var base map[string]jsontext.Value
	if err := json.Unmarshal(raw, &base); err != nil {
		return nil, fmt.Errorf("pulumijournal: read base extensions: %w", err)
	}
	for ref := range base {
		keys[ref] = struct{}{}
	}
	return keys, nil
}

// migrationViewOf reads the members of an inserted state that the migration
// checks. Port difference: upstream reads typed fields that are always set,
// while the port returns an error for a state that does not decode.
func migrationViewOf(raw jsontext.Value) (migrationStateView, error) {
	var view migrationStateView
	if err := json.Unmarshal(raw, &view); err != nil {
		return migrationStateView{}, fmt.Errorf("pulumijournal: read migrated state: %w", err)
	}
	return view, nil
}

// applyMigration applies a state migration entry (upstream applyStateMigration,
// kind 10). The layout becomes the base: retained resources stay by their base
// index and inserted states take their positions. Base patches replace retained
// resources, and new-state patches replace the resources that earlier
// operations made. The sets of base indices move to the new positions, and a
// replacement that a base patch covers is dropped. The staged result runs the
// prospective integrity check, and the replay keeps its state unchanged when
// any step fails.
func (s *replayState) applyMigration(ctx context.Context, entry JournalEntry, name string) error {
	doc, derr := readBaseDocument(ctx, s.base)
	if derr != nil {
		return fmt.Errorf("pulumijournal: entry %s: read base for migration: %w", name, derr)
	}
	if len(doc.pending) != 0 {
		return fmt.Errorf(
			"pulumijournal: entry %s: migration with pending base operations: %w",
			name,
			ErrStateMigration,
		)
	}
	for id, op := range s.incomplete {
		// An unmatched begin entry without an operation body is safe, as
		// upstream finds: the same steps run no provider operation.
		if present(op) {
			return fmt.Errorf(
				"pulumijournal: entry %s: migration with incomplete operation %d: %w",
				name,
				id,
				ErrStateMigration,
			)
		}
	}
	if len(entry.States) == 0 {
		return fmt.Errorf(
			"pulumijournal: entry %s: migration inserts no resources: %w",
			name,
			ErrStateMigration,
		)
	}

	positions, perr := migrationPositions(entry, len(doc.resources), name)
	if perr != nil {
		return perr
	}

	// The staged plan carries the migration, so an error leaves the replay
	// unchanged. The maps that the migration changes are replaced, never
	// written in place.
	plan := stagedMigration{
		staged:        *s,
		baseResources: slices.Clone(doc.resources),
		members:       doc.members,
		positions:     positions,
		patchedBase:   nil,
		name:          name,
	}
	plan.staged.newRecords = slices.Clone(s.newRecords)

	patchedBase, berr := applyBasePatches(entry, plan.baseResources, positions, name)
	if berr != nil {
		return berr
	}
	plan.patchedBase = patchedBase

	if nerr := stageNewPatches(&plan.staged, entry, name); nerr != nil {
		return nerr
	}

	if cerr := checkInsertedStates(entry, doc.members, s.extensions, name); cerr != nil {
		return cerr
	}

	return commitMigration(ctx, s, &plan, entry)
}

// applyBasePatches replaces the retained base resources that a state
// migration rewrites. It returns the patched indices, so the finish drops the
// earlier replacements they cover.
func applyBasePatches(
	entry JournalEntry,
	baseResources []jsontext.Value,
	positions map[int64]int64,
	name string,
) (map[int64]struct{}, error) {
	patched := make(map[int64]struct{}, len(entry.BaseStatePatches))
	for _, patch := range entry.BaseStatePatches {
		if patch.Index < 0 || patch.Index >= int64(len(baseResources)) {
			return nil, fmt.Errorf(
				"pulumijournal: entry %s: migration base patch index %d is outside the base with %d resources: %w",
				name,
				patch.Index,
				len(baseResources),
				ErrStateMigration,
			)
		}
		if _, retained := positions[patch.Index]; !retained {
			return nil, fmt.Errorf(
				"pulumijournal: entry %s: migration base patch index %d is not retained by the layout: %w",
				name,
				patch.Index,
				ErrStateMigration,
			)
		}
		if _, duplicate := patched[patch.Index]; duplicate {
			return nil, fmt.Errorf(
				"pulumijournal: entry %s: migration holds base patch index %d more than once: %w",
				name,
				patch.Index,
				ErrStateMigration,
			)
		}
		patched[patch.Index] = struct{}{}
		baseResources[patch.Index] = patch.State
	}
	return patched, nil
}

// stagedMigration holds a validated state migration ready to commit: the
// staged replay state, the base resources with the base patches applied, the
// other base members, and the maps that move the sets of base indices.
type stagedMigration struct {
	staged        replayState
	baseResources []jsontext.Value
	members       map[string]jsontext.Value
	positions     map[int64]int64
	patchedBase   map[int64]struct{}
	name          string
}

// commitMigration builds the migrated base, moves the sets of base indices to
// their new positions, and runs the prospective integrity check on the staged
// result. The replay takes the staged state only when every step passes.
func commitMigration(ctx context.Context, s *replayState, plan *stagedMigration, entry JournalEntry) error {
	staged := &plan.staged
	migrated := make([]jsontext.Value, 0, len(entry.Layout))
	for _, item := range entry.Layout {
		if item.BaseIndex != nil {
			migrated = append(migrated, plan.baseResources[*item.BaseIndex])
		} else {
			migrated = append(migrated, entry.States[*item.StateIndex])
		}
	}
	encoded, eerr := encodeBaseDocument(plan.members, migrated)
	if eerr != nil {
		return fmt.Errorf("pulumijournal: entry %s: %w", plan.name, eerr)
	}
	staged.base = snapshotOpener(encoded)

	staged.toDeleteInSnapshot = remapIndexSet(s.toDeleteInSnapshot, plan.positions)
	staged.markAsDeletion = remapIndexSet(s.markAsDeletion, plan.positions)
	staged.markAsPending = remapIndexSet(s.markAsPending, plan.positions)
	replaced := make(map[int64]stateRecord, len(s.toReplaceInSnapshot))
	for index := range s.toReplaceInSnapshot {
		// A base patch covers the resource already, so its earlier
		// replacement is dropped.
		if _, patched := plan.patchedBase[index]; patched {
			continue
		}
		if next, ok := plan.positions[index]; ok {
			replaced[next] = s.toReplaceInSnapshot[index]
		}
	}
	staged.toReplaceInSnapshot = replaced

	// The prospective check builds the migrated snapshot and runs the
	// integrity check on it, before the replay takes any of it.
	scan, serr := staged.scanBase(ctx, staged.base)
	if serr != nil {
		return fmt.Errorf("pulumijournal: entry %s: assemble migrated snapshot: %w", plan.name, serr)
	}
	snap, serr := staged.integritySnapshot(scan, staged.replayer)
	if serr != nil {
		return fmt.Errorf("pulumijournal: entry %s: assemble migrated snapshot: %w", plan.name, serr)
	}
	if verr := VerifyIntegrity(snap); verr != nil {
		return fmt.Errorf("pulumijournal: entry %s: migration produces invalid snapshot: %w", plan.name, verr)
	}

	*s = *staged
	return nil
}

// migrationPositions maps each retained base index to its position in the
// migrated base. It checks the layout as upstream does: each item names
// exactly one resource, each index is in range, no resource is placed twice,
// and every inserted state is placed.
func migrationPositions(entry JournalEntry, baseLen int, name string) (map[int64]int64, error) {
	placer := layoutPlacer{
		positions: make(map[int64]int64, len(entry.Layout)),
		placed:    make(map[int64]struct{}, len(entry.States)),
		baseLen:   baseLen,
		stateLen:  len(entry.States),
		name:      name,
	}
	for position, item := range entry.Layout {
		if perr := placer.place(item, position); perr != nil {
			return nil, perr
		}
	}
	if len(placer.placed) != len(entry.States) {
		return nil, fmt.Errorf(
			"pulumijournal: entry %s: migration layout places %d of %d inserted states: %w",
			name,
			len(placer.placed),
			len(entry.States),
			ErrStateMigration,
		)
	}
	return placer.positions, nil
}

// layoutPlacer maps each retained base index to its position in the migrated
// base, and records the placed inserted states.
type layoutPlacer struct {
	positions map[int64]int64
	placed    map[int64]struct{}
	baseLen   int
	stateLen  int
	name      string
}

// place maps one layout item: a retained base index to its position, or
// records a placed inserted state.
func (p *layoutPlacer) place(item JournalLayoutItem, position int) error {
	switch {
	case item.BaseIndex != nil && item.StateIndex != nil:
		return fmt.Errorf(
			"pulumijournal: entry %s: migration layout item %d refers to a base resource and an inserted state: %w",
			p.name,
			position,
			ErrStateMigration,
		)
	case item.BaseIndex != nil:
		return p.placeBase(*item.BaseIndex, position)
	case item.StateIndex != nil:
		return p.placeInserted(*item.StateIndex)
	default:
		return fmt.Errorf(
			"pulumijournal: entry %s: migration layout item %d refers to no resource: %w",
			p.name,
			position,
			ErrStateMigration,
		)
	}
}

// placeBase maps one retained base index to its position in the migrated base.
func (p *layoutPlacer) placeBase(index int64, position int) error {
	if index < 0 || index >= int64(p.baseLen) {
		return fmt.Errorf(
			"pulumijournal: entry %s: migration layout refers to base index %d outside the base with %d resources: %w",
			p.name,
			index,
			p.baseLen,
			ErrStateMigration,
		)
	}
	if _, duplicate := p.positions[index]; duplicate {
		return fmt.Errorf(
			"pulumijournal: entry %s: migration layout places base resource %d more than once: %w",
			p.name,
			index,
			ErrStateMigration,
		)
	}
	p.positions[index] = int64(position)
	return nil
}

// placeInserted records one placed inserted state.
func (p *layoutPlacer) placeInserted(index int64) error {
	if index < 0 || index >= int64(p.stateLen) {
		return fmt.Errorf(
			"pulumijournal: entry %s: migration layout refers to inserted state %d of %d states: %w",
			p.name,
			index,
			p.stateLen,
			ErrStateMigration,
		)
	}
	if _, duplicate := p.placed[index]; duplicate {
		return fmt.Errorf(
			"pulumijournal: entry %s: migration layout places inserted state %d more than once: %w",
			p.name,
			index,
			ErrStateMigration,
		)
	}
	p.placed[index] = struct{}{}
	return nil
}

// stageNewPatches replaces the resources of earlier operations that a state
// migration rewrites. A patch that names a removed operation or an operation
// that no applied entry created is refused with ErrMissingReference, as for
// the other entries that name new resources. Port difference: upstream reads
// the operation index without an error, while this port refuses a missing
// reference on purpose (see lookup).
func stageNewPatches(staged *replayState, entry JournalEntry, name string) error {
	patched := make(map[int64]struct{}, len(entry.NewStatePatches))
	for _, patch := range entry.NewStatePatches {
		if _, removed := staged.toRemove[patch.OperationID]; removed {
			return fmt.Errorf(
				"pulumijournal: entry %s: migration new-state patch references removed operation %d: %w",
				name,
				patch.OperationID,
				ErrMissingReference,
			)
		}
		index, err := staged.lookup(patch.OperationID, name, "newStatePatches")
		if err != nil {
			return err
		}
		if index < 0 || index >= int64(len(staged.newRecords)) {
			return fmt.Errorf(
				"pulumijournal: entry %s: migration new-state patch for operation %d is outside the new resources: %w",
				name,
				patch.OperationID,
				ErrStateMigration,
			)
		}
		if _, duplicate := patched[patch.OperationID]; duplicate {
			return fmt.Errorf(
				"pulumijournal: entry %s: migration holds a new-state patch for operation %d more than once: %w",
				name,
				patch.OperationID,
				ErrStateMigration,
			)
		}
		rec, rerr := newStateRecord(patch.State)
		if rerr != nil {
			return fmt.Errorf("pulumijournal: entry %s: %w", name, rerr)
		}
		patched[patch.OperationID] = struct{}{}
		staged.newRecords[index] = rec
	}
	return nil
}

// checkInsertedStates checks the resources that a state migration inserts, as
// upstream does: each URN is valid and matches its type, no inserted state is
// marked for deletion or a view, each custom state has a physical ID, each
// extension reference names a known extension, and no URN repeats.
func checkInsertedStates(
	entry JournalEntry,
	members map[string]jsontext.Value,
	extensions map[string]jsontext.Value,
	name string,
) error {
	known, err := baseExtensionKeys(members)
	if err != nil {
		return fmt.Errorf("pulumijournal: entry %s: %w", name, err)
	}
	inserted := make(map[string]struct{}, len(entry.States))
	for i, state := range entry.States {
		view, verr := migrationViewOf(state)
		if verr != nil {
			return fmt.Errorf("pulumijournal: entry %s: %w", name, verr)
		}
		if rerr := requireURN(view.URN); rerr != nil {
			return fmt.Errorf(
				"pulumijournal: entry %s: migration inserted state %d has invalid URN: %w",
				name,
				i,
				rerr,
			)
		}
		if cerr := checkInsertedState(view, known, extensions, name); cerr != nil {
			return cerr
		}
		if _, duplicate := inserted[view.URN]; duplicate {
			return fmt.Errorf(
				"pulumijournal: entry %s: migration inserts resource %s more than once: %w",
				name,
				view.URN,
				ErrStateMigration,
			)
		}
		inserted[view.URN] = struct{}{}
	}
	return nil
}

// checkInsertedState checks one inserted resource state: its type matches its
// URN, it is no deletion and no view, a custom state has a physical ID, and
// its extension reference names a known extension.
func checkInsertedState(
	view migrationStateView,
	known map[string]struct{},
	extensions map[string]jsontext.Value,
	name string,
) error {
	if view.Type != urnType(view.URN) {
		return fmt.Errorf(
			"pulumijournal: entry %s: migration inserted state %s has type %s: %w",
			name,
			view.URN,
			view.Type,
			ErrStateMigration,
		)
	}
	if view.Delete {
		return fmt.Errorf(
			"pulumijournal: entry %s: migration inserted state %s is marked for deletion: %w",
			name,
			view.URN,
			ErrStateMigration,
		)
	}
	if view.ViewOf != "" {
		return fmt.Errorf(
			"pulumijournal: entry %s: migration inserted state %s is a view of %s: %w",
			name,
			view.URN,
			view.ViewOf,
			ErrStateMigration,
		)
	}
	if view.Custom && view.ID == "" {
		return fmt.Errorf(
			"pulumijournal: entry %s: migration inserted custom state %s has no physical ID: %w",
			name,
			view.URN,
			ErrStateMigration,
		)
	}
	if view.ExtensionRef != "" {
		if _, ok := known[view.ExtensionRef]; !ok {
			if _, ok := extensions[view.ExtensionRef]; !ok {
				return fmt.Errorf(
					"pulumijournal: entry %s: migration inserted state %s references unknown extension: %w",
					name,
					view.URN,
					ErrStateMigration,
				)
			}
		}
	}
	return nil
}
