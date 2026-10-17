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
// This file is derived from pkg/backend/journal.go (JournalReplayer.Add,
// GenerateDeployment and the manifest of the deployment) and from
// pkg/resource/deploy/manifest.go (NewMagic) of pulumi/pulumi at commit
// 128d3f2ec94e147915a6a928fdf7449d561d560e (tag v3.268.0). The state migration
// entry is derived from pkg/backend/journal_state_migration.go
// (applyStateMigration) at the same commit; see migration.go. OpenDustpan
// changed the replayer in these ways, each marked in the code:
//
//   - The replay runs in two passes over the base (ADR 43, decision 7.3) and
//     writes its output to a writer. Upstream holds the whole base and the whole
//     output in memory.
//   - A reference to an entry that is not applied is an error (ErrMissingReference).
//     Upstream reads index 0 of a missing reference without an error.
//   - A write entry (kind 5), a rebuilt base state entry (kind 7) and a state
//     migration entry (kind 10) replace the base in the middle of a replay.
//     The rebuilt base and the migrated base are held in memory; a later change
//     moves them to a temporary blob.
//   - The operations that are pending are written in the order of their operation
//     IDs. Upstream writes them in the order of a map, which is not stable.
//   - The manifest time comes from the clock that the caller gives, and the
//     manifest version is the CLI version of the update, as upstream writes it.
//   - The replayer runs the integrity check on the output (integrity.go) instead of
//     copying the base metadata unchanged. It drops the integrity_error member when
//     the check passes, and sets it with the schema version and the error text when
//     the check fails. The server writes no command and no environment variables.

package pulumijournal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"time"
)

// Errors that Replay returns.
var (
	// ErrWriteOrder reports a write entry after an entry that changes the records.
	ErrWriteOrder = errors.New("pulumijournal: write entry follows an entry that changes the records")
	// ErrNoSnapshot reports a write entry without a snapshot, or a rebuilt document
	// without a deployment.
	ErrNoSnapshot = errors.New("pulumijournal: journal entry has no snapshot")
	// ErrUnsupportedKind reports an entry kind that applyBaseKind does not handle:
	// any kind other than snippets, extension-parameterize, write,
	// rebuilt-base-state and state-migration. Apply forwards only those kinds,
	// so the default guards future kinds.
	ErrUnsupportedKind = errors.New("pulumijournal: journal entry kind is not replayed yet")
	// ErrMissingReference reports an entry that refers to an entry no applied entry created (R16).
	ErrMissingReference = errors.New("pulumijournal: journal entry refers to an entry that is not applied")
)

// journalVersion is the entry version that the replayer negotiates with DecodeEntries.
const journalVersion = 2

// OpenBase opens a new reader of the base deployment document, the JSON object
// of the deployment. Replay calls it once for each pass, so each call must
// return a new reader.
type OpenBase = func(context.Context) (io.ReadCloser, error)

// Replayer replays the journal entries of one update. Its clock and the CLI
// version of the update come from the constructor.
type Replayer struct {
	now        func() time.Time
	cliVersion string
}

// NewReplayer returns a replayer. now gives the time of the manifest of each
// deployment it writes. cliVersion is the CLI version of the update, which the
// manifest records and whose SHA-256 is the manifest magic (as upstream writes it).
func NewReplayer(now func() time.Time, cliVersion string) *Replayer {
	return &Replayer{now: now, cliVersion: cliVersion}
}

// Replay applies the entries to the base deployment and writes the deployment
// that results to out. entries is a batch body (DecodeEntries); the entries are
// applied in the order of the body, as the replayer of the CLI does (ADR 43,
// decision 6.6).
//
// The replay runs in three steps. First it reads the whole entry stream, and
// applies each entry as it is read. Then the first pass reads the base for the
// features and the final URNs. Then the second pass reads the base again and
// writes the output to out, which is never held in full. The resources of the
// update are held in memory, because they are the replay state. The base
// resources are held one at a time.
func (r *Replayer) Replay(ctx context.Context, openBase OpenBase, entries io.Reader, out io.Writer) error {
	state := newReplayState(openBase, r)
	decode := func(entry JournalEntry) error {
		if err := state.countEntry(entry); err != nil {
			return err
		}
		return state.apply(ctx, entry)
	}

	if err := DecodeEntries(entries, journalVersion, decode); err != nil {
		// A bound error already names its counts and its limit, so it passes
		// through without a second prefix.
		if errors.Is(err, ErrTooManyEntries) || errors.Is(err, ErrNewStateTooLarge) {
			return err
		}
		return fmt.Errorf("pulumijournal: replay entries: %w", err)
	}

	scan, err := state.scanBase(ctx, state.base)
	if err != nil {
		return err
	}

	if err := state.checkIndex(scan); err != nil {
		return err
	}

	return state.write(ctx, state.base, out, scan, r)
}

// stateRecord is one resource state of the update, kept as raw JSON, with the
// parts that replay reads.
type stateRecord struct {
	raw     jsontext.Value
	view    resourceView
	links   linkFields
	deleted bool // set by a DeleteNew entry
	pending bool // set by a PendingReplacementNew entry
}

// newStateRecord reads the parts of a state that replay uses.
func newStateRecord(raw jsontext.Value) (stateRecord, error) {
	var rec stateRecord
	rec.raw = raw
	if err := json.Unmarshal(raw, &rec.view); err != nil {
		return stateRecord{}, fmt.Errorf("pulumijournal: read resource view: %w", err)
	}
	if err := json.Unmarshal(raw, &rec.links); err != nil {
		return stateRecord{}, fmt.Errorf("pulumijournal: read resource links: %w", err)
	}
	return rec, nil
}

// operationView is the part of an operation that replay reads.
type operationView struct {
	Resource resourceView `json:"resource"`
}

// replayState is the state of one replay. It is built from the entries in the
// order of arrival and read by the two passes over the base.
type replayState struct {
	// base opens the base deployment. A kind 5 entry, a kind 7 entry or a kind 10
	// entry replaces it with a source that can also be opened more than once.
	base OpenBase
	// replayer carries the clock and the CLI version of the update, which a kind 7
	// entry needs to write the deployment it rebuilds.
	replayer            *Replayer
	toRemove            map[int64]struct{}
	toDeleteInSnapshot  map[int64]struct{}
	toReplaceInSnapshot map[int64]stateRecord
	markAsDeletion      map[int64]struct{}
	markAsPending       map[int64]struct{}
	// opToIndex maps the operation ID of a new resource to its index in newRecords.
	opToIndex map[int64]int64
	// incomplete holds the operation of each begun operation. A nil operation
	// is kept too, as upstream does, and is not written.
	incomplete map[int64]jsontext.Value
	newRecords []stateRecord
	hasRefresh bool
	byteString bool
	// entryCount holds the entries applied so far. newStateBytes holds the
	// bytes of the resource states they carry.
	entryCount    int64
	newStateBytes int64
	// secrets, snippets and extensions replace the base values when set.
	secrets     jsontext.Value
	snippets    []jsontext.Value
	snippetsSet bool
	extensions  map[string]jsontext.Value
}

// newReplayState returns a replay state over the base, with the replayer that
// writes the rebuilt deployments of kind 7 entries.
func newReplayState(base OpenBase, replayer *Replayer) *replayState {
	return &replayState{
		base:                base,
		replayer:            replayer,
		toRemove:            make(map[int64]struct{}),
		toDeleteInSnapshot:  make(map[int64]struct{}),
		toReplaceInSnapshot: make(map[int64]stateRecord),
		markAsDeletion:      make(map[int64]struct{}),
		markAsPending:       make(map[int64]struct{}),
		opToIndex:           make(map[int64]int64),
		incomplete:          make(map[int64]jsontext.Value),
		newRecords:          nil,
		hasRefresh:          false,
		byteString:          false,
		entryCount:          0,
		newStateBytes:       0,
		secrets:             nil,
		snippets:            nil,
		snippetsSet:         false,
		extensions:          make(map[string]jsontext.Value),
	}
}

// present reports whether an optional member has a value. A member that is
// absent or null has none.
func present(v jsontext.Value) bool {
	return len(v) > 0 && string(v) != "null"
}

// entryLabel names an entry in an error by its sequence ID.
func entryLabel(entry JournalEntry) string {
	return fmt.Sprintf("with sequence ID %d", entry.SequenceID)
}

// lookup returns the index of the new resource that an operation created. It
// returns ErrMissingReference when no applied entry created the operation (R16).
// Upstream reads index 0 here without an error, so a missing reference gives the
// wrong resource. This port refuses on purpose.
func (s *replayState) lookup(op int64, name, field string) (int64, error) {
	idx, ok := s.opToIndex[op]
	if !ok {
		return 0, fmt.Errorf("pulumijournal: entry %s, field %s: %w", name, field, ErrMissingReference)
	}
	return idx, nil
}

// countEntry counts one entry toward the per-update bounds before it is
// applied. It refuses an update with more entries or more new state bytes
// than the bounds allow, with a BoundError that names the count and the limit.
func (s *replayState) countEntry(entry JournalEntry) error {
	s.entryCount++
	if s.entryCount > int64(MaxEntriesPerUpdate) {
		return &BoundError{
			Bound: "journal entries",
			Count: s.entryCount,
			Limit: int64(MaxEntriesPerUpdate),
			Cause: ErrTooManyEntries,
		}
	}
	s.newStateBytes += int64(EntryStateBytes(entry))
	if s.newStateBytes > int64(MaxNewStateBytesPerUpdate) {
		return &BoundError{
			Bound: "new state bytes",
			Count: s.newStateBytes,
			Limit: int64(MaxNewStateBytesPerUpdate),
			Cause: ErrNewStateTooLarge,
		}
	}
	return nil
}

// EntryStateBytes returns the bytes of the resource states one entry carries:
// its own state and every state of a migration entry.
func EntryStateBytes(entry JournalEntry) int {
	n := len(entry.State)
	for _, state := range entry.States {
		n += len(state)
	}
	for _, patch := range entry.BaseStatePatches {
		n += len(patch.State)
	}
	for _, patch := range entry.NewStatePatches {
		n += len(patch.State)
	}
	return n
}

// checkIndex checks the added links of the first pass against the bounds. It
// refuses an update that adds more links than the bound allows, and a build
// with more new-state plus added-link bytes than the bound allows. Both counts
// cover only what the update adds, so neither grows with the base resources,
// which the build streams one at a time.
func (s *replayState) checkIndex(scan *baseScan) error {
	if int64(scan.added) > int64(MaxLinkIndexEntriesPerUpdate) {
		return &BoundError{
			Bound: "added links",
			Count: int64(scan.added),
			Limit: int64(MaxLinkIndexEntriesPerUpdate),
			Cause: ErrLinkIndexTooLarge,
		}
	}
	counted := s.newStateBytes + scan.addedLinkBytes
	if counted > int64(MaxNewStatePlusLinkBytesPerUpdate) {
		return &BoundError{
			Bound: "new-state plus added-link bytes",
			Count: counted,
			Limit: int64(MaxNewStatePlusLinkBytesPerUpdate),
			Cause: ErrAddedBytesTooLarge,
		}
	}
	return nil
}

// linkSize returns the bytes of the link fields of one resource.
func linkSize(fields linkFields) int64 {
	n := int64(len(fields.URN) + len(fields.Parent) + len(fields.Provider) +
		len(fields.ViewOf) + len(fields.DeletedWith))
	for _, urn := range fields.Dependencies {
		n += int64(len(urn))
	}
	for _, urn := range fields.Aliases {
		n += int64(len(urn))
	}
	for _, urn := range fields.ReplaceWith {
		n += int64(len(urn))
	}
	for _, urns := range fields.PropertyDependencies {
		for _, urn := range urns {
			n += int64(len(urn))
		}
	}
	return n
}

// linkBytes returns the bytes of the link fields of the indexed resources.
func linkBytes(index []linkFields) int64 {
	var n int64
	for _, fields := range index { //nolint:gocritic // The struct holds only string headers, so the copy stays small.
		n += linkSize(fields)
	}
	return n
}

// apply applies one entry. It is the callback of DecodeEntries, so it runs for
// each entry before the later entries of the batch are checked.
func (s *replayState) apply(ctx context.Context, entry JournalEntry) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("pulumijournal: replay canceled: %w", err)
	}
	name := entryLabel(entry)
	if entry.RequiresByteString {
		s.byteString = true
	}
	switch entry.Kind {
	case JournalEntryKindBegin:
		s.incomplete[entry.OperationID] = entry.Operation
		return nil
	case JournalEntryKindFailure:
		delete(s.incomplete, entry.OperationID)
		return nil
	case JournalEntryKindSuccess:
		return s.applySuccess(entry, name)
	case JournalEntryKindRefreshSuccess:
		return s.applyRefreshSuccess(entry, name)
	case JournalEntryKindOutputs:
		return s.applyOutputs(entry, name)
	case JournalEntryKindSecretsManager:
		return s.applySecrets(entry, name)
	case JournalEntryKindSnippets, JournalEntryKindExtensionParameterize,
		JournalEntryKindWrite, JournalEntryKindRebuiltBaseState, JournalEntryKindStateMigration:
		return s.applyBaseKind(ctx, entry, name)
	default:
		return fmt.Errorf("pulumijournal: entry %s: kind %s: %w", name, entry.Kind, ErrUnknownKind)
	}
}

// applyWrite applies a write entry (kind 5). The base becomes the snapshot, as
// upstream does (backend/journal.go of github.com/pulumi/pulumi/pkg/v3 v3.268.0,
// lines 295 to 298). The secrets and snippets that earlier entries set are part of
// the base that the snapshot replaces, so they are dropped. The verifier refuses a
// write that follows an entry that changes the records (ErrWriteOrder), so the
// records are not cleared here. A write without a snapshot is refused.
func (s *replayState) applyWrite(entry JournalEntry, name string) error {
	if len(entry.NewSnapshot) == 0 {
		return fmt.Errorf("pulumijournal: entry %s: write without a snapshot: %w", name, ErrNoSnapshot)
	}

	s.base = snapshotOpener(entry.NewSnapshot)
	s.secrets = nil
	s.snippets = nil
	s.snippetsSet = false

	return nil
}

// applyRebuild applies a rebuilt base state entry (kind 7). The deployment of the
// replay so far becomes the base, and the records are dropped, as upstream does in
// backend/journal.go of github.com/pulumi/pulumi/pkg/v3 v3.268.0, lines 313
// to 329. The secrets and snippets stay, because the rebuilt
// base holds them. The rebuilt deployment is held in memory until issue #456
// adds a temporary blob for it.
func (s *replayState) applyRebuild(ctx context.Context, name string) error {
	scan, err := s.scanBase(ctx, s.base)
	if err != nil {
		return fmt.Errorf("pulumijournal: entry %s: rebuild base: %w", name, err)
	}

	var buf bytes.Buffer

	err = s.write(ctx, s.base, &buf, scan, s.replayer)
	if err != nil {
		return fmt.Errorf("pulumijournal: entry %s: rebuild base: %w", name, err)
	}

	deployment, err := deploymentOf(buf.Bytes())
	if err != nil {
		return fmt.Errorf("pulumijournal: entry %s: rebuild base: %w", name, err)
	}

	s.base = snapshotOpener(deployment)
	s.clearRecords()
	s.extensions = make(map[string]jsontext.Value)

	return nil
}

// clearRecords drops the records that the base replaces: the resources to remove,
// delete, replace or mark, the operation index, the incomplete operations and the
// new resources (upstream backend/journal.go of github.com/pulumi/pulumi/pkg/v3 v3.268.0, lines 321 to 328).
func (s *replayState) clearRecords() {
	s.toRemove = make(map[int64]struct{})
	s.toDeleteInSnapshot = make(map[int64]struct{})
	s.toReplaceInSnapshot = make(map[int64]stateRecord)
	s.markAsDeletion = make(map[int64]struct{})
	s.markAsPending = make(map[int64]struct{})
	s.opToIndex = make(map[int64]int64)
	s.incomplete = make(map[int64]jsontext.Value)
	s.newRecords = nil
}

// snapshotOpener returns an OpenBase over doc. Each call returns a new reader.
func snapshotOpener(doc []byte) OpenBase {
	return func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(doc)), nil
	}
}

// deploymentOf returns the deployment of a checkpoint document, which is the
// document the replayer writes: the format version and the deployment. A resource
// marked for deletion stays, with its delete mark, as upstream GenerateDeployment
// keeps it (backend/journal.go of github.com/pulumi/pulumi/pkg/v3 v3.268.0, lines 437 to 441).
func deploymentOf(document []byte) ([]byte, error) {
	var doc struct {
		Deployment jsontext.Value `json:"deployment"`
	}

	if err := json.Unmarshal(document, &doc); err != nil {
		return nil, fmt.Errorf("read rebuilt document: %w", err)
	}

	if len(doc.Deployment) == 0 {
		return nil, fmt.Errorf("rebuilt document has no deployment: %w", ErrNoSnapshot)
	}

	return doc.Deployment, nil
}

// applyBaseKind applies the entries that set or replace the base: snippets,
// extensions, a write (kind 5), a rebuilt base state (kind 7) and a state
// migration (kind 10, see migration.go).
func (s *replayState) applyBaseKind(ctx context.Context, entry JournalEntry, name string) error {
	//nolint:exhaustive // apply sends only the base kinds here, and default refuses the rest.
	switch entry.Kind {
	case JournalEntryKindSnippets:
		s.snippets = entry.Snippets
		s.snippetsSet = true

		return nil
	case JournalEntryKindExtensionParameterize:
		return s.applyExtension(entry, name)
	case JournalEntryKindWrite:
		return s.applyWrite(entry, name)
	case JournalEntryKindRebuiltBaseState:
		return s.applyRebuild(ctx, name)
	case JournalEntryKindStateMigration:
		return s.applyMigration(ctx, entry, name)
	default:
		return fmt.Errorf("pulumijournal: entry %s: kind %s: %w", name, entry.Kind, ErrUnsupportedKind)
	}
}

// applySecrets applies a secrets manager entry (upstream Add, kind 6).
func (s *replayState) applySecrets(entry JournalEntry, name string) error {
	if !present(entry.SecretsProvider) {
		return fmt.Errorf("pulumijournal: entry %s: secrets provider: %w", name, ErrMissingMember)
	}
	s.secrets = entry.SecretsProvider
	return nil
}

// applySuccess applies a success entry (upstream Add, kind 1).
func (s *replayState) applySuccess(entry JournalEntry, name string) error {
	delete(s.incomplete, entry.OperationID)
	if present(entry.State) {
		rec, err := newStateRecord(entry.State)
		if err != nil {
			return fmt.Errorf("pulumijournal: entry %s: %w", name, err)
		}
		s.opToIndex[entry.OperationID] = int64(len(s.newRecords))
		s.newRecords = append(s.newRecords, rec)
	}
	if err := s.applySuccessRemovals(entry, name); err != nil {
		return err
	}
	if err := s.applySuccessFlags(entry, name); err != nil {
		return err
	}
	if entry.IsRefresh {
		s.hasRefresh = true
	}
	return nil
}

// applySuccessRemovals applies the removals and the markings of a success entry
// for the base and for the new resources it removes.
func (s *replayState) applySuccessRemovals(entry JournalEntry, name string) error {
	if entry.RemoveOld != nil {
		s.toDeleteInSnapshot[*entry.RemoveOld] = struct{}{}
	}
	if entry.RemoveNew != nil {
		if _, err := s.lookup(*entry.RemoveNew, name, "removeNew"); err != nil {
			return err
		}
		s.toRemove[*entry.RemoveNew] = struct{}{}
	}
	if entry.DeleteOld != nil {
		s.markAsDeletion[*entry.DeleteOld] = struct{}{}
	}
	if entry.PendingReplacementOld != nil {
		s.markAsPending[*entry.PendingReplacementOld] = struct{}{}
	}
	return nil
}

// applySuccessFlags sets the flags of new resources that a success entry names.
func (s *replayState) applySuccessFlags(entry JournalEntry, name string) error {
	if entry.DeleteNew != nil {
		idx, err := s.lookup(*entry.DeleteNew, name, "deleteNew")
		if err != nil {
			return err
		}
		s.newRecords[idx].deleted = true
	}
	if entry.PendingReplacementNew != nil {
		idx, err := s.lookup(*entry.PendingReplacementNew, name, "pendingReplacementNew")
		if err != nil {
			return err
		}
		s.newRecords[idx].pending = true
	}
	return nil
}

// applyRefreshSuccess applies a refresh-success entry (upstream Add, kind 3).
func (s *replayState) applyRefreshSuccess(entry JournalEntry, name string) error {
	delete(s.incomplete, entry.OperationID)
	s.hasRefresh = true
	if entry.RemoveOld != nil {
		if err := s.applyRefreshBase(entry, name); err != nil {
			return err
		}
	}
	if entry.RemoveNew != nil {
		return s.applyRefreshNew(entry, name)
	}
	return nil
}

// applyRefreshBase deletes or replaces the base resource that a refresh names.
func (s *replayState) applyRefreshBase(entry JournalEntry, name string) error {
	if !present(entry.State) {
		s.toDeleteInSnapshot[*entry.RemoveOld] = struct{}{}
		return nil
	}
	rec, err := newStateRecord(entry.State)
	if err != nil {
		return fmt.Errorf("pulumijournal: entry %s: %w", name, err)
	}
	s.toReplaceInSnapshot[*entry.RemoveOld] = rec
	return nil
}

// applyRefreshNew removes or replaces the new resource that a refresh names.
func (s *replayState) applyRefreshNew(entry JournalEntry, name string) error {
	idx, err := s.lookup(*entry.RemoveNew, name, "removeNew")
	if err != nil {
		return err
	}
	if !present(entry.State) {
		s.toRemove[*entry.RemoveNew] = struct{}{}
		return nil
	}
	rec, rerr := newStateRecord(entry.State)
	if rerr != nil {
		return fmt.Errorf("pulumijournal: entry %s: %w", name, rerr)
	}
	s.newRecords[idx] = rec
	return nil
}

// applyOutputs applies an outputs entry (upstream Add, kind 4).
func (s *replayState) applyOutputs(entry JournalEntry, name string) error {
	if present(entry.State) && entry.RemoveOld != nil {
		rec, err := newStateRecord(entry.State)
		if err != nil {
			return fmt.Errorf("pulumijournal: entry %s: %w", name, err)
		}
		s.toReplaceInSnapshot[*entry.RemoveOld] = rec
	}
	if present(entry.State) && entry.RemoveNew != nil {
		idx, err := s.lookup(*entry.RemoveNew, name, "removeNew")
		if err != nil {
			return err
		}
		rec, rerr := newStateRecord(entry.State)
		if rerr != nil {
			return fmt.Errorf("pulumijournal: entry %s: %w", name, rerr)
		}
		s.newRecords[idx] = rec
	}
	return nil
}

// applyExtension applies an extension parameterize entry (upstream Add, kind 8).
// The key is the decoded reference, because the base holds the same references.
func (s *replayState) applyExtension(entry JournalEntry, name string) error {
	if !present(entry.ExtensionRef) || !present(entry.Extension) {
		return fmt.Errorf("pulumijournal: entry %s: extension: %w", name, ErrMissingMember)
	}
	var ref string
	if err := json.Unmarshal(entry.ExtensionRef, &ref); err != nil {
		return fmt.Errorf("pulumijournal: entry %s: extension reference: %w", name, err)
	}
	s.extensions[ref] = entry.Extension
	return nil
}

// baseScan holds what the first pass over the base finds: the features of the
// deployment, the URNs of its resources, and the base members that the output
// keeps (the ones that are not resources).
type baseScan struct {
	features map[string]bool
	// final holds the URNs of the resources of the output, which are the
	// resources that the output keeps (upstream undangleParentResources reads it).
	final   map[string]bool
	members map[string]jsontext.Value
	pending []jsontext.Value
	// index holds the links of the resources of the output, in the order of the
	// output. The second pass reads the base again and takes the normalized links
	// from it (ADR 43, decision 7.3).
	index []linkFields
	// added counts the links the update adds: the kept new resources and the
	// base resources a refresh or outputs entry replaces. addedLinkBytes holds
	// their link bytes. Kept base resources without a replacement add nothing.
	added          int
	addedLinkBytes int64
	// extras holds the integrity members of the resources of the output, in the
	// same order as index. The integrity check reads the links from index and the
	// other members from extras.
	extras []integrityExtra
	// integrityErr is the result of the integrity check on the output. The tail
	// of the deployment writes the integrity_error metadata from it.
	integrityErr error
	// integrityVersion is the schema version written into the integrity_error
	// metadata on failure, as decimal text.
	integrityVersion string
	// norm normalizes the URN references of the output. Its zero value changes
	// nothing.
	norm normalizer
}

// integrityExtra holds the members of a resource state that the integrity check
// reads besides the links: the type, the custom flag, the provider ID and the
// delete and pending marks of the output.
type integrityExtra struct {
	resourceType string
	custom       bool
	id           string
	deleted      bool
	pending      bool
}

// integrityStateView is the part of a resource state that the integrity check
// reads besides the links.
type integrityStateView struct {
	Type               string `json:"type"`
	Custom             bool   `json:"custom"`
	ID                 string `json:"id,omitempty"`
	Delete             bool   `json:"delete,omitempty"`
	PendingReplacement bool   `json:"pendingReplacement,omitempty"`
}

// integrityExtraOf reads the integrity members of one resource state. deleted and
// pending are the marks that the entries set besides the state itself.
func integrityExtraOf(raw jsontext.Value, deleted, pending bool) (integrityExtra, error) {
	var view integrityStateView
	if err := json.Unmarshal(raw, &view); err != nil {
		return integrityExtra{}, fmt.Errorf("pulumijournal: read resource integrity fields: %w", err)
	}

	return integrityExtra{
		resourceType: view.Type,
		custom:       view.Custom,
		id:           view.ID,
		deleted:      view.Delete || deleted,
		pending:      view.PendingReplacement || pending,
	}, nil
}

// addFeatures applies the rules of one resource to the feature set (upstream
// GenerateDeployment calls ApplyFeatures for each resource it keeps). The
// byteString feature is set for each resource once an entry reports byte strings.
func (s *replayState) addFeatures(features map[string]bool, view resourceView) {
	if s.byteString {
		markByteString(features)
	}
	applyFeatures(view, features)
}

// removedIndices returns the indices of the new resources that a remove entry deletes.
func (s *replayState) removedIndices() map[int64]struct{} {
	removed := make(map[int64]struct{}, len(s.toRemove))
	for op := range s.toRemove {
		if idx, ok := s.opToIndex[op]; ok {
			removed[idx] = struct{}{}
		}
	}
	return removed
}

// scanBase runs the first pass over the base. It gives the features and the
// final URNs, and it keeps the base members that the output needs.
func (s *replayState) scanBase(ctx context.Context, openBase OpenBase) (*baseScan, error) {
	scan := &baseScan{
		features:         make(map[string]bool),
		final:            make(map[string]bool),
		members:          make(map[string]jsontext.Value),
		pending:          nil,
		index:            nil,
		added:            0,
		addedLinkBytes:   0,
		extras:           nil,
		integrityErr:     nil,
		integrityVersion: "",
		norm:             normalizer{aliased: nil},
	}
	if err := s.scanNewResources(scan); err != nil {
		return nil, err
	}
	err := walkBase(ctx, openBase, func(i int, raw jsontext.Value) error {
		if cerr := ctx.Err(); cerr != nil {
			return fmt.Errorf("pulumijournal: replay canceled: %w", cerr)
		}
		return s.scanBaseResource(scan, i, raw)
	}, func(key string, raw jsontext.Value) error {
		if cerr := ctx.Err(); cerr != nil {
			return fmt.Errorf("pulumijournal: replay canceled: %w", cerr)
		}
		if key == "pending_operations" {
			return s.addBasePending(scan, raw)
		}
		scan.members[key] = raw
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, op := range s.sortedIncomplete() {
		var view operationView
		if err := json.Unmarshal(op, &view); err != nil {
			return nil, fmt.Errorf("pulumijournal: read pending operation: %w", err)
		}
		s.addFeatures(scan.features, view.Resource)
	}
	count, cerr := s.snippetCountOut(scan)
	if cerr != nil {
		return nil, cerr
	}
	if count > 0 {
		scan.features[snippetsFeature] = true
	}
	return scan, nil
}

// scanNewResources adds the features and the URNs of the new resources that the
// update keeps, with their integrity members. Each kept new resource counts as
// an added link.
func (s *replayState) scanNewResources(scan *baseScan) error {
	removed := s.removedIndices()
	for i := range s.newRecords {
		if _, gone := removed[int64(i)]; gone {
			continue
		}
		s.addFeatures(scan.features, s.newRecords[i].view)
		scan.final[s.newRecords[i].links.URN] = true
		scan.index = append(scan.index, s.newRecords[i].links.clone())
		scan.added++
		scan.addedLinkBytes += linkSize(s.newRecords[i].links)
		extra, err := integrityExtraOf(s.newRecords[i].raw, s.newRecords[i].deleted, s.newRecords[i].pending)
		if err != nil {
			return err
		}
		scan.extras = append(scan.extras, extra)
	}
	return nil
}

// scanBaseResource adds the features and the URN of one base resource that the
// update keeps. A resource that a refresh replaces counts as its replacement.
func (s *replayState) scanBaseResource(scan *baseScan, i int, raw jsontext.Value) error {
	idx := int64(i)
	if _, gone := s.toDeleteInSnapshot[idx]; gone {
		return nil
	}
	rec, err := newStateRecord(raw)
	if err != nil {
		return err
	}
	if repl, ok := s.toReplaceInSnapshot[idx]; ok {
		rec = repl
	}
	s.addFeatures(scan.features, rec.view)
	scan.final[rec.links.URN] = true
	scan.index = append(scan.index, rec.links.clone())
	if _, replaced := s.toReplaceInSnapshot[idx]; replaced {
		scan.added++
		scan.addedLinkBytes += linkSize(rec.links)
	}
	// A replaced resource counts as its replacement alone: the markings of the
	// base index do not apply to it.
	deleted, pending := false, false
	if _, ok := s.toReplaceInSnapshot[idx]; !ok {
		_, deleted = s.markAsDeletion[idx]
		_, pending = s.markAsPending[idx]
	}
	extra, xerr := integrityExtraOf(rec.raw, deleted, pending)
	if xerr != nil {
		return xerr
	}
	scan.extras = append(scan.extras, extra)
	return nil
}

// snippetCountOut returns the number of snippets in the output.
func (s *replayState) snippetCountOut(scan *baseScan) (int, error) {
	snippets, err := s.snippetsOut(scan)
	return len(snippets), err
}

// addBasePending keeps the pending operations of the base that are being
// created, and applies their features (upstream GenerateDeployment).
func (s *replayState) addBasePending(scan *baseScan, raw jsontext.Value) error {
	var ops []jsontext.Value
	if err := json.Unmarshal(raw, &ops); err != nil {
		return fmt.Errorf("pulumijournal: read base pending operations: %w", err)
	}
	for _, op := range ops {
		var view struct {
			operationView

			Type string `json:"type"`
		}
		if err := json.Unmarshal(op, &view); err != nil {
			return fmt.Errorf("pulumijournal: read base pending operation: %w", err)
		}
		if view.Type != creatingOperation {
			continue
		}
		scan.pending = append(scan.pending, op)
		s.addFeatures(scan.features, view.Resource)
	}
	return nil
}

// creatingOperation is the type of a pending operation that creates a resource
// (upstream apitype.OperationTypeCreating).
const creatingOperation = "creating"

// sortedIncomplete returns the operations that are begun and have an operation
// body, in the order of their operation IDs. Upstream takes them from a map,
// whose order is not defined, so the order here is a choice of this port.
func (s *replayState) sortedIncomplete() []jsontext.Value {
	ids := make([]int64, 0, len(s.incomplete))
	for id, op := range s.incomplete {
		if present(op) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	ops := make([]jsontext.Value, 0, len(ids))
	for _, id := range ids {
		ops = append(ops, s.incomplete[id])
	}
	return ops
}

// walkBase reads the base document once. It calls onResource for each element
// of the resources array, in order, and onMember for each other member, after
// its value has been read. Neither the resources nor the other members are
// held after their callback returns, except the members the caller keeps.
func walkBase(
	ctx context.Context,
	openBase OpenBase,
	onResource func(int, jsontext.Value) error,
	onMember func(string, jsontext.Value) error,
) (err error) {
	rc, oerr := openBase(ctx)
	if oerr != nil {
		return fmt.Errorf("pulumijournal: open base: %w", oerr)
	}
	defer func() {
		if cerr := rc.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("pulumijournal: close base: %w", cerr)
		}
	}()
	dec := jsontext.NewDecoder(rc)
	if err := readDelim(dec, '{'); err != nil {
		return err
	}
	for dec.PeekKind() != '}' {
		if err := walkBaseMember(dec, onResource, onMember); err != nil {
			return err
		}
	}
	return readDelim(dec, '}')
}

// walkBaseMember reads one member of the base document. The resources array is
// walked element by element, and any other member is read whole.
func walkBaseMember(
	dec *jsontext.Decoder,
	onResource func(int, jsontext.Value) error,
	onMember func(string, jsontext.Value) error,
) error {
	key, err := dec.ReadToken()
	if err != nil {
		return fmt.Errorf("pulumijournal: read base member name: %w", err)
	}
	if key.Kind() != '"' {
		return fmt.Errorf("pulumijournal: base member name is not a string: %w", ErrBatchShape)
	}
	// The token is void after the next read, so the name is copied first.
	name := key.String()
	if name == "resources" {
		// Upstream reads a null resources member as no resources.
		if dec.PeekKind() == 'n' {
			return readDelim(dec, 'n')
		}
		return walkResources(dec, onResource)
	}
	var raw jsontext.Value
	if err := json.UnmarshalDecode(dec, &raw); err != nil {
		return fmt.Errorf("pulumijournal: read base member: %w", err)
	}
	return onMember(name, raw)
}

// walkResources reads the resources array of the base one element at a time.
func walkResources(dec *jsontext.Decoder, onResource func(int, jsontext.Value) error) error {
	if err := readDelim(dec, '['); err != nil {
		return err
	}
	for i := 0; dec.PeekKind() != ']'; i++ {
		var raw jsontext.Value
		if err := json.UnmarshalDecode(dec, &raw); err != nil {
			return fmt.Errorf("pulumijournal: read base resource: %w", err)
		}
		if err := onResource(i, raw); err != nil {
			return err
		}
	}
	return readDelim(dec, ']')
}

// manifestJSON is the manifest of a deployment as upstream writes it
// (apitype.ManifestV1, without plugins).
type manifestJSON struct {
	Time    time.Time `json:"time"`
	Magic   string    `json:"magic"`
	Version string    `json:"version"`
}

// newManifest returns the manifest of a deployment. The magic is the hex SHA-256
// of the version, and it is empty when the version is empty (upstream
// deploy.Manifest.NewMagic).
func newManifest(now time.Time, version string) manifestJSON {
	magic := ""
	if version != "" {
		sum := sha256.Sum256([]byte(version))
		magic = hex.EncodeToString(sum[:])
	}
	return manifestJSON{Time: now, Magic: magic, Version: version}
}

// write runs the second pass over the base and writes the deployment to out.
// The version and the features come from the first pass, so the output can
// start with them. The resources come first, the new ones and then the base
// ones in the order of the base, as upstream GenerateDeployment writes them.
//
// The URN references are normalized before the first byte is written. Upstream
// calls deployment.NormalizeURNReferences at the end of GenerateDeployment
// (pkg/backend/journal.go, line 507). It needs the aliases of all resources, so
// the normalization runs over the link index of the first pass. A failure
// therefore writes nothing.
func (s *replayState) write(ctx context.Context, openBase OpenBase, out io.Writer, scan *baseScan, r *Replayer) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("pulumijournal: replay canceled: %w", err)
	}
	if s.hasRefresh {
		rebuildIndex(scan.index, scan.final)
	}
	norm, err := normalizeIndex(scan.index)
	if err != nil {
		return err
	}
	scan.norm = norm
	snap, err := s.integritySnapshot(scan, r)
	if err != nil {
		return err
	}
	scan.integrityErr = VerifyIntegrity(snap)
	scan.integrityVersion = strconv.Itoa(schemaVersion(scan.features))
	enc := jsontext.NewEncoder(out)
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return fmt.Errorf("pulumijournal: write deployment: %w", err)
	}
	if err := writeKeyValue(enc, "version", schemaVersion(scan.features)); err != nil {
		return err
	}
	if len(scan.features) > 0 {
		if err := writeKeyValue(enc, "features", slices.Sorted(maps.Keys(scan.features))); err != nil {
			return err
		}
	}
	if err := enc.WriteToken(jsontext.String("deployment")); err != nil {
		return fmt.Errorf("pulumijournal: write deployment: %w", err)
	}
	if err := s.writeDeployment(ctx, openBase, enc, scan, r); err != nil {
		return err
	}
	if err := enc.WriteToken(jsontext.EndObject); err != nil {
		return fmt.Errorf("pulumijournal: write deployment: %w", err)
	}
	return nil
}

// writeDeployment writes the deployment object. The caller has written its key.
func (s *replayState) writeDeployment(
	ctx context.Context,
	openBase OpenBase,
	enc *jsontext.Encoder,
	scan *baseScan,
	r *Replayer,
) error {
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return fmt.Errorf("pulumijournal: write deployment: %w", err)
	}
	if err := writeKeyValue(enc, "manifest", newManifest(r.now(), r.cliVersion)); err != nil {
		return err
	}
	if secrets := s.secretsOut(scan); present(secrets) {
		if err := writeKeyRaw(enc, "secrets_providers", secrets); err != nil {
			return err
		}
	}
	if err := s.writeResources(ctx, openBase, enc, scan); err != nil {
		return err
	}
	if err := s.writeTail(enc, scan); err != nil {
		return err
	}
	if err := enc.WriteToken(jsontext.EndObject); err != nil {
		return fmt.Errorf("pulumijournal: write deployment: %w", err)
	}
	return nil
}

// integritySnapshot builds the deployment that the integrity check reads from the
// link index of the first pass, which already has the rebuild and the URN
// normalization applied. The manifest is the manifest of the output.
func (s *replayState) integritySnapshot(scan *baseScan, r *Replayer) (*IntegrityDeployment, error) {
	if len(scan.index) != len(scan.extras) {
		return nil, errLinkIndex
	}
	manifest := newManifest(r.now(), r.cliVersion)
	resources := make([]IntegrityResource, len(scan.index))
	for i := range scan.index {
		links := scan.index[i]
		extra := scan.extras[i]
		resources[i] = IntegrityResource{
			URN:                  links.URN,
			ResourceType:         extra.resourceType,
			Custom:               extra.custom,
			ID:                   extra.id,
			Provider:             links.Provider,
			Parent:               links.Parent,
			Dependencies:         links.Dependencies,
			PropertyDependencies: links.PropertyDependencies,
			DeletedWith:          links.DeletedWith,
			Delete:               extra.deleted,
			PendingReplacement:   extra.pending,
		}
	}
	snippets, err := s.integritySnippets(scan)
	if err != nil {
		return nil, err
	}
	return &IntegrityDeployment{
		Manifest:  IntegrityManifest{Magic: manifest.Magic, Version: manifest.Version},
		Resources: resources,
		Snippets:  snippets,
		Metadata:  nil,
	}, nil
}

// integritySnippets returns the UUIDs of the snippets of the output.
func (s *replayState) integritySnippets(scan *baseScan) ([]IntegritySnippet, error) {
	raws, err := s.snippetsOut(scan)
	if err != nil {
		return nil, err
	}
	out := make([]IntegritySnippet, len(raws))
	for i, raw := range raws {
		var view struct {
			UUID string `json:"uuid"`
		}
		if err := json.Unmarshal(raw, &view); err != nil {
			return nil, fmt.Errorf("pulumijournal: read snippet uuid: %w", err)
		}
		out[i] = IntegritySnippet{UUID: view.UUID}
	}
	return out, nil
}

// writeTail writes the members of the deployment that follow the resources:
// the pending operations, the metadata, the snippets and the extensions.
func (s *replayState) writeTail(enc *jsontext.Encoder, scan *baseScan) error {
	if ops := append(s.sortedIncomplete(), scan.pending...); len(ops) > 0 {
		if err := writeKeyValue(enc, "pending_operations", ops); err != nil {
			return err
		}
	}
	metadata, merr := s.metadataOut(scan)
	if merr != nil {
		return merr
	}
	if present(metadata) {
		if err := writeKeyRaw(enc, "metadata", metadata); err != nil {
			return err
		}
	}
	snippets, serr := s.snippetsOut(scan)
	if serr != nil {
		return serr
	}
	if len(snippets) > 0 {
		if err := writeKeyValue(enc, "snippets", snippets); err != nil {
			return err
		}
	}
	ext, eerr := s.extensionsOut(scan)
	if eerr != nil {
		return eerr
	}
	if len(ext) > 0 {
		return writeKeyValue(enc, "extensions", ext)
	}
	return nil
}

// metadataOut returns the metadata member of the output. The other members of the
// base metadata are kept. The integrity_error member is dropped when the integrity
// check passed, and set with the schema version and the error text when it failed.
// An empty metadata is omitted.
func (s *replayState) metadataOut(scan *baseScan) (jsontext.Value, error) {
	var members map[string]jsontext.Value
	if raw := scan.members["metadata"]; present(raw) {
		if err := json.Unmarshal(raw, &members); err != nil {
			return nil, fmt.Errorf("pulumijournal: read base metadata: %w", err)
		}
	}
	if scan.integrityErr == nil {
		delete(members, "integrity_error")
	} else {
		if members == nil {
			members = make(map[string]jsontext.Value)
		}
		encoded, err := marshalDeterministic(IntegrityErrorMetadata{
			Version: scan.integrityVersion,
			Error:   scan.integrityErr.Error(),
		})
		if err != nil {
			return nil, err
		}
		members["integrity_error"] = encoded
	}
	if len(members) == 0 {
		return nil, nil
	}
	out, err := marshalDeterministic(members)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// writeResources writes the resources array: the new resources the update keeps,
// then the base resources that it keeps. It applies the dependency and parent
// rebuild when the update has a refresh.
func (s *replayState) writeResources(
	ctx context.Context,
	openBase OpenBase,
	enc *jsontext.Encoder,
	scan *baseScan,
) error {
	if err := enc.WriteToken(jsontext.String("resources")); err != nil {
		return fmt.Errorf("pulumijournal: write deployment: %w", err)
	}
	if err := enc.WriteToken(jsontext.BeginArray); err != nil {
		return fmt.Errorf("pulumijournal: write deployment: %w", err)
	}
	w := &resourceWriter{enc: enc, index: scan.index, next: 0}
	if err := s.writeNewRecords(w); err != nil {
		return err
	}
	if err := s.writeBaseRecords(ctx, openBase, w); err != nil {
		return err
	}
	if w.next != len(w.index) {
		return errLinkIndex
	}
	if err := enc.WriteToken(jsontext.EndArray); err != nil {
		return fmt.Errorf("pulumijournal: write deployment: %w", err)
	}
	return nil
}

// writeNewRecords writes the new resources that the update keeps.
func (s *replayState) writeNewRecords(w *resourceWriter) error {
	removed := s.removedIndices()
	for i := range s.newRecords {
		if _, gone := removed[int64(i)]; gone {
			continue
		}
		flags := resourceFlags{deleted: s.newRecords[i].deleted, pending: s.newRecords[i].pending}
		if err := w.emit(&s.newRecords[i], flags); err != nil {
			return err
		}
	}
	return nil
}

// writeBaseRecords writes the base resources that the update keeps, in the order of the base.
func (s *replayState) writeBaseRecords(ctx context.Context, openBase OpenBase, w *resourceWriter) error {
	return walkBase(ctx, openBase, func(i int, raw jsontext.Value) error {
		if cerr := ctx.Err(); cerr != nil {
			return fmt.Errorf("pulumijournal: replay canceled: %w", cerr)
		}
		idx := int64(i)
		if _, gone := s.toDeleteInSnapshot[idx]; gone {
			return nil
		}
		if repl, ok := s.toReplaceInSnapshot[idx]; ok {
			return w.emit(&repl, resourceFlags{deleted: false, pending: false})
		}
		rec, err := newStateRecord(raw)
		if err != nil {
			return err
		}
		_, deleted := s.markAsDeletion[idx]
		_, pending := s.markAsPending[idx]
		return w.emit(&rec, resourceFlags{deleted: deleted, pending: pending})
	}, func(string, jsontext.Value) error { return nil })
}

// secretsOut returns the secrets provider of the output: the entry value when an
// entry set one, else the base value.
func (s *replayState) secretsOut(scan *baseScan) jsontext.Value {
	if present(s.secrets) {
		return s.secrets
	}
	return scan.members["secrets_providers"]
}

// snippetsOut returns the snippets of the output: the entry list when an entry
// set one, else the base list. Their references are normalized (upstream
// NormalizeURNReferences rewrites them too).
func (s *replayState) snippetsOut(scan *baseScan) ([]jsontext.Value, error) {
	snippets := s.snippets
	if !s.snippetsSet {
		snippets = nil
		if raw := scan.members["snippets"]; present(raw) {
			if err := json.Unmarshal(raw, &snippets); err != nil {
				return nil, fmt.Errorf("pulumijournal: read base snippets: %w", err)
			}
		}
	}
	return scan.norm.snippets(snippets)
}

// extensionsOut merges the extensions of the entries with those of the base.
// The base value wins for a reference that both hold (upstream maps.Copy).
func (s *replayState) extensionsOut(scan *baseScan) (map[string]jsontext.Value, error) {
	merged := make(map[string]jsontext.Value, len(s.extensions))
	maps.Copy(merged, s.extensions)
	if raw := scan.members["extensions"]; present(raw) {
		var base map[string]jsontext.Value
		if err := json.Unmarshal(raw, &base); err != nil {
			return nil, fmt.Errorf("pulumijournal: read base extensions: %w", err)
		}
		maps.Copy(merged, base)
	}
	return merged, nil
}

// errLinkIndex is returned when the second pass finds more resources than the
// link index of the first pass holds. The two passes read the same base, so this
// does not happen unless the base changes between the passes.
var errLinkIndex = errors.New("pulumijournal: link index does not match the resources")

// resourceWriter writes the resources of the output. The links of each resource
// come from the index of the first pass, which has the rebuild and the URN
// normalization applied.
type resourceWriter struct {
	enc   *jsontext.Encoder
	index []linkFields
	// next is the position of the next resource in index.
	next int
}

// resourceFlags are the flags that an entry or a base marking sets on a resource.
type resourceFlags struct {
	deleted bool
	pending bool
}

// emit writes one resource. Its state stays compact and unchanged, unless a flag
// or a link changes; then only the members that change are rewritten.
func (w *resourceWriter) emit(rec *stateRecord, flags resourceFlags) error {
	if w.next >= len(w.index) {
		return errLinkIndex
	}
	next := w.index[w.next]
	w.next++
	patch := make(map[string]any)
	if flags.deleted {
		patch["delete"] = true
	}
	if flags.pending {
		patch["pendingReplacement"] = true
	}
	addLinkPatch(patch, rec.links, next)
	raw := rec.raw
	if len(patch) > 0 {
		var err error
		raw, err = patchObject(raw, patch)
		if err != nil {
			return err
		}
	}
	if err := w.enc.WriteValue(raw); err != nil {
		return fmt.Errorf("pulumijournal: write resource: %w", err)
	}
	return nil
}

// addLinkPatch adds the members that the rebuild or the normalization changed to
// the patch. A member with no value is removed, as the omitempty tags of upstream
// do.
func addLinkPatch(patch map[string]any, old, next linkFields) {
	if old.URN != next.URN {
		patch["urn"] = next.URN
	}
	if !slices.Equal(old.Aliases, next.Aliases) {
		patch["aliases"] = nilIfEmpty(next.Aliases)
	}
	if old.Provider != next.Provider {
		patch["provider"] = nilIfEmptyString(next.Provider)
	}
	if old.ViewOf != next.ViewOf {
		patch["viewOf"] = nilIfEmptyString(next.ViewOf)
	}
	if !slices.Equal(old.Dependencies, next.Dependencies) {
		patch["dependencies"] = nilIfEmpty(next.Dependencies)
	}
	if !maps.EqualFunc(old.PropertyDependencies, next.PropertyDependencies, slices.Equal) {
		patch["propertyDependencies"] = nilIfEmptyMap(next.PropertyDependencies)
	}
	if !slices.Equal(old.ReplaceWith, next.ReplaceWith) {
		patch["replaceWith"] = nilIfEmpty(next.ReplaceWith)
	}
	if old.DeletedWith != next.DeletedWith {
		patch["deletedWith"] = nilIfEmptyString(next.DeletedWith)
	}
	if old.Parent != next.Parent {
		patch["parent"] = nilIfEmptyString(next.Parent)
	}
}

// nilIfEmpty returns nil for an empty slice, so that a patch removes the member.
func nilIfEmpty(v []string) any {
	if len(v) == 0 {
		return nil
	}
	return v
}

// nilIfEmptyMap returns nil for an empty map, so that a patch removes the member.
func nilIfEmptyMap(v map[string][]string) any {
	if len(v) == 0 {
		return nil
	}
	return v
}

// nilIfEmptyString returns nil for an empty string, so that a patch removes the member.
func nilIfEmptyString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// patchObject returns the JSON object raw with the members of patch set. A nil
// value removes the member. The other members keep their raw values.
func patchObject(raw jsontext.Value, patch map[string]any) (jsontext.Value, error) {
	var members map[string]jsontext.Value
	if err := json.Unmarshal(raw, &members); err != nil {
		return nil, fmt.Errorf("pulumijournal: read resource for patch: %w", err)
	}
	if members == nil {
		members = make(map[string]jsontext.Value)
	}
	for key, value := range patch {
		if value == nil {
			delete(members, key)
			continue
		}
		encoded, err := marshalDeterministic(value)
		if err != nil {
			return nil, fmt.Errorf("pulumijournal: encode resource member: %w", err)
		}
		members[key] = encoded
	}
	out, err := marshalDeterministic(members)
	if err != nil {
		return nil, fmt.Errorf("pulumijournal: encode resource for patch: %w", err)
	}
	return out, nil
}

// writeKeyValue writes an object member whose value is encoded with json.
func writeKeyValue(enc *jsontext.Encoder, key string, value any) error {
	encoded, err := marshalDeterministic(value)
	if err != nil {
		return fmt.Errorf("pulumijournal: encode deployment member %s: %w", key, err)
	}
	return writeKeyRaw(enc, key, encoded)
}

// marshalDeterministic encodes a value with its map keys in sorted order, so that
// the same replay gives the same bytes (json.Deterministic).
func marshalDeterministic(value any) (jsontext.Value, error) {
	out, err := json.Marshal(value, json.Deterministic(true))
	if err != nil {
		return nil, fmt.Errorf("pulumijournal: encode value: %w", err)
	}
	return out, nil
}

// writeKeyRaw writes an object member whose value is already JSON.
func writeKeyRaw(enc *jsontext.Encoder, key string, raw jsontext.Value) error {
	if err := enc.WriteToken(jsontext.String(key)); err != nil {
		return fmt.Errorf("pulumijournal: write deployment member %s: %w", key, err)
	}
	if err := enc.WriteValue(raw); err != nil {
		return fmt.Errorf("pulumijournal: write deployment member %s: %w", key, err)
	}
	return nil
}
