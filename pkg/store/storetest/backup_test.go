// Fake backup store: an in-memory BackupStore for the RunBackup conformance
// suite. The SQL adapters run the suite once they implement the ports (issues
// #143 and #144); until then this fake runs it. Its knobs (tear, dropTable,
// extraSchemaTable, omitSchemaTable, skipEmptyCheck, skipReferences,
// ignoreManifest, allowDuplicate, allowRefinish, oldestFirst,
// noopFailRunning, dropOnClose, peekTampered, peekAnyWrapper, peekNoCorrupt)
// break the store on purpose, so the tests below prove that each suite case
// can fail.
package storetest_test

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/storetest"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// errFakeClosed reports use of a fake handle after Close.
var errFakeClosed = errors.New("fake store is closed")

// errFakeUpdateExists reports a duplicate update ID in the fake. The update
// ports define no duplicate error, and the suite never creates one, so the
// fake carries its own.
var errFakeUpdateExists = errors.New("fake: update exists")

// errFakeStateExists reports a duplicate state version ID in the fake, for
// the same reason.
var errFakeStateExists = errors.New("fake: state version exists")

// fakeTime is the fixed instant the fake stamps on rows.
func fakeTime() time.Time {
	return time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)
}

// fakeData holds every table of the fake.
type fakeData struct {
	namespaces   map[string]namespace.Namespace
	stacks       map[string]stack.Stack
	stackIDs     map[string]string
	updates      map[string]map[string]stack.Update
	checkpoints  map[string]map[string]stack.Checkpoint
	events       map[string]map[string][]stack.Event
	keys         map[string]secret.WrappedKey
	entries      map[string]inventory.Entry
	workspaces   map[string]workspace.Workspace
	workspaceIDs map[string]string
	states       map[string]workspace.StateVersion
	current      map[string]string
	backups      map[string]backup.Record
	// installation is the installation state. It starts normal, as the
	// seeded row of the SQL adapters does.
	installation maintenance.State
}

// newFakeData returns empty tables.
func newFakeData() *fakeData {
	return &fakeData{
		namespaces:   map[string]namespace.Namespace{},
		stacks:       map[string]stack.Stack{},
		stackIDs:     map[string]string{},
		updates:      map[string]map[string]stack.Update{},
		checkpoints:  map[string]map[string]stack.Checkpoint{},
		events:       map[string]map[string][]stack.Event{},
		keys:         map[string]secret.WrappedKey{},
		entries:      map[string]inventory.Entry{},
		workspaces:   map[string]workspace.Workspace{},
		workspaceIDs: map[string]string{},
		states:       map[string]workspace.StateVersion{},
		current:      map[string]string{},
		backups:      map[string]backup.Record{},
		installation: maintenance.StateNormal,
	}
}

// fakeBackupStore is an in-memory storetest.BackupStore. Writes hold an
// exclusive lock, so each port call lands atomically. The tear knob releases
// the lock between tables while exporting, which tears the capture.
type fakeBackupStore struct {
	mu               sync.RWMutex
	data             *fakeData
	staged           *fakeData
	stageNext        int
	stageCurrent     int
	stageLive        map[int]bool
	betweenTables    func(table string)
	dialect          backup.Dialect
	version          int
	dropTable        string
	extraSchemaTable string
	omitSchemaTable  string
	tear             bool
	skipEmptyCheck   bool
	skipReferences   bool
	ignoreManifest   bool
	allowDuplicate   bool
	allowRefinish    bool
	oldestFirst      bool
	noopFailRunning  bool
	dropOnClose      bool
	peekTampered     bool
	peekAnyWrapper   bool
	peekNoCorrupt    bool
	closed           bool
}

var (
	_ storetest.BackupStore    = (*fakeBackupStore)(nil)
	_ storetest.Reopener       = (*fakeBackupStore)(nil)
	_ storetest.ExportTamperer = (*fakeBackupStore)(nil)
	_ storetest.StagingWitness = (*fakeBackupStore)(nil)
	_ backup.KeyPeeker         = (*fakeBackupStore)(nil)
	_ backup.RestoreStore      = (*fakeBackupStore)(nil)
	_ backup.Stager            = (*fakeBackupStore)(nil)
)

// newFakeBackupStore returns a fresh, empty fake of the SQLite dialect at
// schema version 10.
func newFakeBackupStore() *fakeBackupStore {
	return &fakeBackupStore{
		data:             newFakeData(),
		staged:           nil,
		stageNext:        0,
		stageCurrent:     0,
		stageLive:        map[int]bool{},
		dialect:          backup.DialectSQLite,
		version:          10,
		mu:               sync.RWMutex{},
		betweenTables:    nil,
		dropTable:        "",
		extraSchemaTable: "",
		omitSchemaTable:  "",
		tear:             false,
		skipEmptyCheck:   false,
		skipReferences:   false,
		ignoreManifest:   false,
		allowDuplicate:   false,
		allowRefinish:    false,
		oldestFirst:      false,
		noopFailRunning:  false,
		dropOnClose:      false,
		peekTampered:     false,
		peekAnyWrapper:   false,
		peekNoCorrupt:    false,
		closed:           false,
	}
}

// inventoryKey keys one inventory entry.
func inventoryKey(ns namespace.Name, tool, name string) string {
	return ns.String() + "\x00" + tool + "\x00" + name
}

// CreateNamespace implements namespace.Store.
func (s *fakeBackupStore) CreateNamespace(_ context.Context, ns namespace.Namespace) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errFakeClosed
	}

	if _, ok := s.data.namespaces[ns.Name.String()]; ok {
		return namespace.ErrAlreadyExists
	}

	s.data.namespaces[ns.Name.String()] = ns

	return nil
}

// GetNamespace implements namespace.Store.
func (s *fakeBackupStore) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return namespace.Namespace{}, errFakeClosed
	}

	ns, ok := s.data.namespaces[name.String()]
	if !ok {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return ns, nil
}

// ListNamespaces implements namespace.Store, ordered by name.
func (s *fakeBackupStore) ListNamespaces(_ context.Context) ([]namespace.Namespace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, errFakeClosed
	}

	out := make([]namespace.Namespace, 0, len(s.data.namespaces))
	for _, ns := range s.data.namespaces {
		out = append(out, ns)
	}

	slices.SortFunc(out, func(a, b namespace.Namespace) int {
		switch {
		case a.Name < b.Name:
			return -1
		case a.Name > b.Name:
			return 1
		default:
			return 0
		}
	})

	return out, nil
}

// CreateStack implements stack.Store with its tags and inventory entry in one
// locked step.
func (s *fakeBackupStore) CreateStack(_ context.Context, st stack.Stack, fence namespace.Fence) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errFakeClosed
	}

	if err := s.checkFence(fence); err != nil {
		return err
	}

	if _, ok := s.data.stacks[st.Ref.String()]; ok {
		return stack.ErrAlreadyExists
	}

	st.Tags = maps.Clone(st.Tags)
	s.data.stacks[st.Ref.String()] = st
	s.data.stackIDs[st.ID.String()] = st.Ref.String()
	s.publishStack(st.Ref, st.ID.String(), st.UpdatedAt)

	return nil
}

// GetStack implements stack.Store.
func (s *fakeBackupStore) GetStack(_ context.Context, ref stack.Ref) (stack.Stack, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return stack.Stack{}, errFakeClosed
	}

	st, ok := s.data.stacks[ref.String()]
	if !ok {
		return stack.Stack{}, stack.ErrNotFound
	}

	st.Tags = maps.Clone(st.Tags)

	return st, nil
}

// ListStacks implements stack.Store, ordered by project then name.
func (s *fakeBackupStore) ListStacks(_ context.Context, filter stack.Filter) ([]stack.Stack, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, errFakeClosed
	}

	out := make([]stack.Stack, 0)

	for ref := range s.data.stacks {
		st := s.data.stacks[ref]
		if st.Ref.Namespace != filter.Namespace {
			continue
		}

		if filter.Project != "" && st.Ref.Project != filter.Project {
			continue
		}

		st.Tags = maps.Clone(st.Tags)
		out = append(out, st)
	}

	slices.SortFunc(out, func(a, b stack.Stack) int {
		if a.Ref.Project != b.Ref.Project {
			if a.Ref.Project < b.Ref.Project {
				return -1
			}

			return 1
		}

		switch {
		case a.Ref.Stack < b.Ref.Stack:
			return -1
		case a.Ref.Stack > b.Ref.Stack:
			return 1
		default:
			return 0
		}
	})

	return out, nil
}

// DeleteStack implements stack.Store with its children, key and entry.
// When the latest checkpoint manages resources, it returns
// ErrHasResources and removes nothing.
func (s *fakeBackupStore) DeleteStack(_ context.Context, ref stack.Ref, fence namespace.Fence) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errFakeClosed
	}

	if err := s.checkFence(fence); err != nil {
		return err
	}

	st, ok := s.data.stacks[ref.String()]
	if !ok {
		return stack.ErrNotFound
	}

	for id := range s.data.updates[st.ID.String()] {
		if u := s.data.updates[st.ID.String()][id]; u.Status == stack.StatusRunning && u.Mutates() {
			return stack.ErrUpdateConflict
		}
	}

	if s.latestResourceCount(st.ID.String()) > 0 {
		return stack.ErrHasResources
	}

	s.removeStack(ref, st)

	return nil
}

// ForceDeleteStack implements stack.Store with its children, key and entry,
// even when the latest checkpoint manages resources.
func (s *fakeBackupStore) ForceDeleteStack(_ context.Context, ref stack.Ref, fence namespace.Fence) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errFakeClosed
	}

	if err := s.checkFence(fence); err != nil {
		return err
	}

	st, ok := s.data.stacks[ref.String()]
	if !ok {
		return stack.ErrNotFound
	}

	for id := range s.data.updates[st.ID.String()] {
		if u := s.data.updates[st.ID.String()][id]; u.Status == stack.StatusRunning && u.Mutates() {
			return stack.ErrUpdateConflict
		}
	}

	s.removeStack(ref, st)

	return nil
}

// UpdateStackTags implements stack.Store.
func (s *fakeBackupStore) UpdateStackTags(
	_ context.Context, ref stack.Ref, tags map[string]string, updatedAt time.Time, fence namespace.Fence,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errFakeClosed
	}

	if err := s.checkFence(fence); err != nil {
		return err
	}

	st, ok := s.data.stacks[ref.String()]
	if !ok {
		return stack.ErrNotFound
	}

	if len(tags) == 0 {
		tags = map[string]string{}
	}

	st.Tags = maps.Clone(tags)
	st.UpdatedAt = updatedAt
	s.data.stacks[ref.String()] = st
	s.publishStack(ref, st.ID.String(), updatedAt)

	return nil
}

// RenameStack implements stack.Store. Children key on the stable stack ID,
// so only the stack row and the inventory entry move.
func (s *fakeBackupStore) RenameStack(
	_ context.Context, oldRef stack.Ref, newName stack.Name, fence namespace.Fence,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errFakeClosed
	}

	if err := s.checkFence(fence); err != nil {
		return err
	}

	st, ok := s.data.stacks[oldRef.String()]
	if !ok {
		return stack.ErrNotFound
	}

	if newName == oldRef.Stack {
		return nil
	}

	newRef := stack.Ref{Namespace: oldRef.Namespace, Project: oldRef.Project, Stack: newName}

	if _, taken := s.data.stacks[newRef.String()]; taken {
		return stack.ErrAlreadyExists
	}

	delete(s.data.stacks, oldRef.String())
	st.Ref = newRef
	s.data.stacks[newRef.String()] = st
	s.data.stackIDs[st.ID.String()] = newRef.String()
	delete(s.data.entries, inventoryKey(oldRef.Namespace, "pulumi", oldRef.Project.String()+"/"+oldRef.Stack.String()))
	s.publishStack(newRef, st.ID.String(), st.UpdatedAt)

	return nil
}

// CreateUpdate implements stack.UpdateStore.
func (s *fakeBackupStore) CreateUpdate(_ context.Context, u stack.Update, fence namespace.Fence) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errFakeClosed
	}

	if err := s.checkFence(fence); err != nil {
		return err
	}

	id, err := s.stackIDOf(u.Ref)
	if err != nil {
		return err
	}

	byStack, ok := s.data.updates[id]
	if !ok {
		byStack = map[string]stack.Update{}
		s.data.updates[id] = byStack
	}

	if _, ok := byStack[u.ID.String()]; ok {
		return fmt.Errorf("create update %s: %w", u.ID, errFakeUpdateExists)
	}

	byStack[u.ID.String()] = u

	return nil
}

// GetUpdate implements stack.UpdateStore.
func (s *fakeBackupStore) GetUpdate(
	_ context.Context, ref stack.Ref, id stack.UpdateID,
) (stack.Update, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return stack.Update{}, errFakeClosed
	}

	stackID, err := s.stackIDOf(ref)
	if err != nil {
		return stack.Update{}, stack.ErrUpdateNotFound
	}

	u, ok := s.data.updates[stackID][id.String()]
	if !ok {
		return stack.Update{}, stack.ErrUpdateNotFound
	}

	return u, nil
}

// StartUpdate implements stack.UpdateStore with version assignment.
func (s *fakeBackupStore) StartUpdate(
	_ context.Context, u stack.Update, fence namespace.Fence,
) (stack.Update, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return stack.Update{}, errFakeClosed
	}

	if err := s.checkFence(fence); err != nil {
		return stack.Update{}, err
	}

	stackID, err := s.stackIDOf(u.Ref)
	if err != nil {
		return stack.Update{}, stack.ErrUpdateNotFound
	}

	stored, ok := s.data.updates[stackID][u.ID.String()]
	if !ok {
		return stack.Update{}, stack.ErrUpdateNotFound
	}

	if stored.Revision != u.Revision-1 {
		return stack.Update{}, stack.ErrConcurrentModification
	}

	if stored.Status != stack.StatusNotStarted {
		return stack.Update{}, stack.ErrWrongUpdateState
	}

	if u.Mutates() {
		for id := range s.data.updates[stackID] {
			if other := s.data.updates[stackID][id]; other.Status == stack.StatusRunning && other.Mutates() {
				return stack.Update{}, stack.ErrUpdateConflict
			}
		}
	}

	if u.Mutates() {
		u.Version = s.highestUpdateVersion(stackID) + 1
	} else {
		u.Version = s.data.stacks[u.Ref.String()].Version
	}

	s.data.updates[stackID][u.ID.String()] = u

	return u, nil
}

// SaveUpdate implements stack.UpdateStore. An ended mutating update moves the
// stack version and the inventory entry in the same locked step.
func (s *fakeBackupStore) SaveUpdate(_ context.Context, u stack.Update, fence namespace.Fence) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errFakeClosed
	}

	if err := s.checkFence(fence); err != nil {
		return err
	}

	stackID, err := s.stackIDOf(u.Ref)
	if err != nil {
		return stack.ErrUpdateNotFound
	}

	stored, ok := s.data.updates[stackID][u.ID.String()]
	if !ok {
		return stack.ErrUpdateNotFound
	}

	if stored.Revision != u.Revision-1 {
		return stack.ErrConcurrentModification
	}

	s.data.updates[stackID][u.ID.String()] = u

	if u.Mutates() && u.Status.Terminal() && u.Version != 0 {
		st := s.data.stacks[u.Ref.String()]
		st.Version = u.Version
		st.UpdatedAt = u.EndedAt
		s.data.stacks[u.Ref.String()] = st
		s.publishStack(u.Ref, stackID, u.EndedAt)
	}

	return nil
}

// ActiveUpdate implements stack.UpdateStore.
func (s *fakeBackupStore) ActiveUpdate(_ context.Context, ref stack.Ref) (stack.Update, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return stack.Update{}, errFakeClosed
	}

	stackID, err := s.stackIDOf(ref)
	if err != nil {
		return stack.Update{}, stack.ErrUpdateNotFound
	}

	for id := range s.data.updates[stackID] {
		if u := s.data.updates[stackID][id]; u.Status == stack.StatusRunning && u.Mutates() {
			return u, nil
		}
	}

	return stack.Update{}, stack.ErrUpdateNotFound
}

// ListUpdates implements stack.UpdateStore: started mutating updates, newest
// first.
func (s *fakeBackupStore) ListUpdates(
	_ context.Context, ref stack.Ref, page stack.Page,
) ([]stack.Update, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, errFakeClosed
	}

	stackID, err := s.stackIDOf(ref)
	if err != nil {
		return []stack.Update{}, nil
	}

	out := make([]stack.Update, 0)

	for id := range s.data.updates[stackID] {
		if u := s.data.updates[stackID][id]; u.Status != stack.StatusNotStarted && u.Mutates() {
			out = append(out, u)
		}
	}

	slices.SortFunc(out, func(a, b stack.Update) int {
		switch {
		case a.Version > b.Version:
			return -1
		case a.Version < b.Version:
			return 1
		default:
			return 0
		}
	})

	if page.Offset >= len(out) {
		return []stack.Update{}, nil
	}

	out = out[page.Offset:]

	if page.Limit < len(out) {
		out = out[:page.Limit]
	}

	return out, nil
}

// PutCheckpoint implements stack.CheckpointStore.
func (s *fakeBackupStore) PutCheckpoint(
	_ context.Context, cp stack.Checkpoint, fence namespace.Fence,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errFakeClosed
	}

	if err := s.checkFence(fence); err != nil {
		return err
	}

	if err := s.checkRunningUpdate(cp.Ref, cp.Update, cp.Lease); err != nil {
		return err
	}

	stackID, err := s.stackIDOf(cp.Ref)
	if err != nil {
		return stack.ErrNoState
	}

	byStack, ok := s.data.checkpoints[stackID]
	if !ok {
		byStack = map[string]stack.Checkpoint{}
		s.data.checkpoints[stackID] = byStack
	}

	if retried, err := checkCheckpointSequence(byStack, cp); err != nil {
		return err
	} else if retried {
		return nil
	}

	if cp.BlobKey != "" {
		// Rows with a blob key keep the metadata only, as the SQL adapters
		// do: the content lives in blobs.
		cp.Deployment = nil
	}

	byStack[cp.Update.String()] = cp

	return nil
}

// checkCheckpointSequence applies the sequence rules of PutCheckpoint to cp
// against the stored checkpoints of its update: it reports whether a repeated
// sequence with the same digest writes nothing. Another digest conflicts, and
// an older sequence is stale.
func checkCheckpointSequence(
	byStack map[string]stack.Checkpoint, cp stack.Checkpoint,
) (bool, error) {
	stored, ok := byStack[cp.Update.String()]
	if !ok || cp.Sequence == 0 {
		return false, nil
	}

	switch {
	case stored.Sequence == cp.Sequence && stored.Digest == cp.Digest:
		return true, nil
	case stored.Sequence == cp.Sequence:
		return false, stack.ErrCheckpointConflict
	case cp.Sequence < stored.Sequence:
		return false, stack.ErrStaleCheckpoint
	default:
		return false, nil
	}
}

// LatestCheckpoint implements stack.CheckpointStore: the checkpoint of the
// highest version that has one.
func (s *fakeBackupStore) LatestCheckpoint(
	_ context.Context, ref stack.Ref,
) (stack.Checkpoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return stack.Checkpoint{}, errFakeClosed
	}

	stackID, err := s.stackIDOf(ref)
	if err != nil {
		return stack.Checkpoint{}, stack.ErrNoState
	}

	var best stack.Checkpoint

	found := false

	for key := range s.data.checkpoints[stackID] {
		cp := s.data.checkpoints[stackID][key]
		u := s.data.updates[stackID][cp.Update.String()]
		if !found || u.Version > s.data.updates[stackID][best.Update.String()].Version {
			best, found = cp, true
		}
	}

	if !found {
		return stack.Checkpoint{}, stack.ErrNoState
	}

	return best, nil
}

// CheckpointByVersion implements stack.CheckpointStore.
func (s *fakeBackupStore) CheckpointByVersion(
	_ context.Context, ref stack.Ref, version int64,
) (stack.Checkpoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return stack.Checkpoint{}, errFakeClosed
	}

	stackID, err := s.stackIDOf(ref)
	if err != nil {
		return stack.Checkpoint{}, stack.ErrNoState
	}

	for id := range s.data.updates[stackID] {
		if u := s.data.updates[stackID][id]; u.Version == version {
			if cp, ok := s.data.checkpoints[stackID][u.ID.String()]; ok {
				return cp, nil
			}
		}
	}

	return stack.Checkpoint{}, stack.ErrNoState
}

// CheckpointByUpdate implements stack.CheckpointStore.
func (s *fakeBackupStore) CheckpointByUpdate(
	_ context.Context, ref stack.Ref, id stack.UpdateID,
) (stack.Checkpoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return stack.Checkpoint{}, errFakeClosed
	}

	stackID, err := s.stackIDOf(ref)
	if err != nil {
		return stack.Checkpoint{}, stack.ErrNoState
	}

	if cp, ok := s.data.checkpoints[stackID][id.String()]; ok {
		return cp, nil
	}

	return stack.Checkpoint{}, stack.ErrNoState
}

// ReferencedCheckpointKeys implements stack.CheckpointStore.
func (s *fakeBackupStore) ReferencedCheckpointKeys(
	_ context.Context, ref stack.Ref,
) ([]blob.Key, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, errFakeClosed
	}

	stackID, err := s.stackIDOf(ref)
	if err != nil {
		return nil, nil
	}

	var out []blob.Key

	for id := range s.data.checkpoints[stackID] {
		if cp := s.data.checkpoints[stackID][id]; cp.BlobKey != "" {
			out = append(out, cp.BlobKey)
		}
	}

	slices.Sort(out)

	return out, nil
}

// AppendEvents implements stack.EventStore, skipping stored sequences.
func (s *fakeBackupStore) AppendEvents(_ context.Context, batch stack.EventBatch, fence namespace.Fence) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errFakeClosed
	}

	if err := s.checkFence(fence); err != nil {
		return err
	}

	if err := s.checkRunningUpdate(batch.Ref, batch.Update, batch.Lease); err != nil {
		return err
	}

	stackID, err := s.stackIDOf(batch.Ref)
	if err != nil {
		return stack.ErrUpdateNotFound
	}

	byStack, ok := s.data.events[stackID]
	if !ok {
		byStack = map[string][]stack.Event{}
		s.data.events[stackID] = byStack
	}

	stored := byStack[batch.Update.String()]
	seen := make(map[int64]bool, len(stored))

	for _, e := range stored {
		seen[e.Sequence] = true
	}

	for _, e := range batch.Events {
		if !seen[e.Sequence] {
			stored = append(stored, e)
			seen[e.Sequence] = true
		}
	}

	slices.SortFunc(stored, func(a, b stack.Event) int {
		switch {
		case a.Sequence < b.Sequence:
			return -1
		case a.Sequence > b.Sequence:
			return 1
		default:
			return 0
		}
	})
	byStack[batch.Update.String()] = stored

	return nil
}

// ListEvents implements stack.EventStore.
func (s *fakeBackupStore) ListEvents(
	_ context.Context, ref stack.Ref, id stack.UpdateID, after int64, limit int,
) ([]stack.Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, errFakeClosed
	}

	stackID, err := s.stackIDOf(ref)
	if err != nil {
		return nil, stack.ErrUpdateNotFound
	}

	if _, ok := s.data.updates[stackID][id.String()]; !ok {
		return nil, stack.ErrUpdateNotFound
	}

	out := make([]stack.Event, 0)

	for _, e := range s.data.events[stackID][id.String()] {
		if e.Sequence > after {
			out = append(out, e)
		}
	}

	if limit < len(out) {
		out = out[:limit]
	}

	return out, nil
}

// CreateKey implements secret.KeyStore, keeping the first key of a scope.
func (s *fakeBackupStore) CreateKey(
	_ context.Context, k secret.WrappedKey, fence namespace.Fence,
) (secret.WrappedKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return secret.WrappedKey{}, errFakeClosed
	}

	if err := s.checkFence(fence); err != nil {
		return secret.WrappedKey{}, err
	}

	if stored, ok := s.data.keys[k.Scope.String()]; ok {
		return stored, nil
	}

	s.data.keys[k.Scope.String()] = k

	return k, nil
}

// GetKey implements secret.KeyStore.
func (s *fakeBackupStore) GetKey(_ context.Context, scope secret.Scope) (secret.WrappedKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return secret.WrappedKey{}, errFakeClosed
	}

	k, ok := s.data.keys[scope.String()]
	if !ok {
		return secret.WrappedKey{}, secret.ErrKeyNotFound
	}

	return k, nil
}

// ListWrapperIDs implements secret.KeyStore, ordered.
func (s *fakeBackupStore) ListWrapperIDs(_ context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, errFakeClosed
	}

	seen := map[string]bool{}

	for _, k := range s.data.keys {
		seen[k.WrapperID] = true
	}

	out := slices.Sorted(maps.Keys(seen))
	if out == nil {
		out = []string{}
	}

	return out, nil
}

// ListUnits implements inventory.Reader, ordered by tool then name.
func (s *fakeBackupStore) ListUnits(_ context.Context, ns namespace.Name) ([]inventory.Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, errFakeClosed
	}

	out := make([]inventory.Entry, 0)

	for key := range s.data.entries {
		if e := s.data.entries[key]; e.Namespace == ns {
			out = append(out, e)
		}
	}

	slices.SortFunc(out, func(a, b inventory.Entry) int {
		if a.Tool != b.Tool {
			if a.Tool < b.Tool {
				return -1
			}

			return 1
		}

		switch {
		case a.Name < b.Name:
			return -1
		case a.Name > b.Name:
			return 1
		default:
			return 0
		}
	})

	return out, nil
}

// CreateWorkspace implements workspace.Store.
func (s *fakeBackupStore) CreateWorkspace(
	_ context.Context, w workspace.Workspace, fence namespace.Fence,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errFakeClosed
	}

	if err := s.checkFence(fence); err != nil {
		return err
	}

	if _, ok := s.data.workspaceIDs[w.Ref.String()]; ok {
		return workspace.ErrAlreadyExists
	}

	s.data.workspaces[w.ID.String()] = w
	s.data.workspaceIDs[w.Ref.String()] = w.ID.String()
	s.publishWorkspace(w.Ref, w.ID.String(), w.UpdatedAt)

	return nil
}

// GetWorkspace implements workspace.Store.
func (s *fakeBackupStore) GetWorkspace(_ context.Context, ref workspace.Ref) (workspace.Workspace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return workspace.Workspace{}, errFakeClosed
	}

	id, ok := s.data.workspaceIDs[ref.String()]
	if !ok {
		return workspace.Workspace{}, workspace.ErrNotFound
	}

	return s.data.workspaces[id], nil
}

// GetWorkspaceByID implements workspace.Store.
func (s *fakeBackupStore) GetWorkspaceByID(_ context.Context, id workspace.ID) (workspace.Workspace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return workspace.Workspace{}, errFakeClosed
	}

	w, ok := s.data.workspaces[id.String()]
	if !ok {
		return workspace.Workspace{}, workspace.ErrNotFound
	}

	return w, nil
}

// ListWorkspaces implements workspace.Store, ordered by name.
func (s *fakeBackupStore) ListWorkspaces(
	_ context.Context, ns namespace.Name,
) ([]workspace.Workspace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, errFakeClosed
	}

	out := make([]workspace.Workspace, 0)

	for id := range s.data.workspaces {
		if w := s.data.workspaces[id]; w.Ref.Namespace == ns {
			out = append(out, w)
		}
	}

	slices.SortFunc(out, func(a, b workspace.Workspace) int {
		switch {
		case a.Ref.Name < b.Ref.Name:
			return -1
		case a.Ref.Name > b.Ref.Name:
			return 1
		default:
			return 0
		}
	})

	return out, nil
}

// SaveWorkspace implements workspace.Store with a revision compare-and-swap.
func (s *fakeBackupStore) SaveWorkspace(
	_ context.Context, w workspace.Workspace, fence namespace.Fence,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errFakeClosed
	}

	if err := s.checkFence(fence); err != nil {
		return err
	}

	stored, ok := s.data.workspaces[w.ID.String()]
	if !ok {
		return workspace.ErrNotFound
	}

	if stored.Revision != w.Revision-1 {
		return workspace.ErrConcurrentModification
	}

	s.data.workspaces[w.ID.String()] = w
	s.publishWorkspace(w.Ref, w.ID.String(), w.UpdatedAt)

	return nil
}

// CreateStateVersion implements workspace.StateStore.
func (s *fakeBackupStore) CreateStateVersion(
	_ context.Context, sv workspace.StateVersion, fence namespace.Fence, maxPending int,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errFakeClosed
	}

	if err := s.checkFence(fence); err != nil {
		return err
	}

	if _, ok := s.data.workspaces[sv.Workspace.String()]; !ok {
		return workspace.ErrNotFound
	}

	if _, ok := s.data.states[sv.ID.String()]; ok {
		return fmt.Errorf("create state version %s: %w", sv.ID, errFakeStateExists)
	}

	pending := 0

	for key := range s.data.states {
		if v := s.data.states[key]; v.Workspace == sv.Workspace && v.Status == workspace.StatusPending {
			pending++
		}
	}

	if pending >= maxPending {
		return fmt.Errorf(
			"%w: %d pending, limit %d",
			workspace.ErrTooManyPending,
			pending,
			maxPending,
		)
	}

	s.data.states[sv.ID.String()] = sv

	return nil
}

// GetStateVersion implements workspace.StateStore.
func (s *fakeBackupStore) GetStateVersion(
	_ context.Context, id workspace.StateVersionID,
) (workspace.StateVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return workspace.StateVersion{}, errFakeClosed
	}

	sv, ok := s.data.states[id.String()]
	if !ok {
		return workspace.StateVersion{}, workspace.ErrStateVersionNotFound
	}

	return sv, nil
}

// FinalizeStateVersion implements workspace.StateStore with the lock,
// lineage and serial checks.
func (s *fakeBackupStore) FinalizeStateVersion(
	_ context.Context, sv workspace.StateVersion, fence namespace.Fence,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errFakeClosed
	}

	if err := s.checkFence(fence); err != nil {
		return err
	}

	stored, ok := s.data.states[sv.ID.String()]
	if !ok {
		return workspace.ErrStateVersionNotFound
	}

	if stored.Status != workspace.StatusPending {
		return workspace.ErrUploadDenied
	}

	// The write matches only the recorded attempt key: a superseded
	// attempt never finalizes over a newer one.
	if stored.Content != sv.Content {
		return workspace.ErrUploadDenied
	}

	w, ok := s.data.workspaces[sv.Workspace.String()]
	if !ok {
		return workspace.ErrNotFound
	}

	if err := checkBackupFinalize(s.data, w, sv); err != nil {
		return err
	}

	s.data.states[sv.ID.String()] = sv
	s.data.current[w.ID.String()] = sv.ID.String()
	w.UpdatedAt = sv.FinalizedAt
	s.data.workspaces[w.ID.String()] = w
	s.publishWorkspace(w.Ref, w.ID.String(), sv.FinalizedAt)

	return nil
}

// checkBackupFinalize runs the lock, lineage and serial checks of a
// finalize. The caller holds the store lock.
func checkBackupFinalize(data *fakeData, w workspace.Workspace, sv workspace.StateVersion) error {
	switch {
	case !w.Lock.Locked():
		return workspace.ErrNotLocked
	case w.Lock.ID != sv.LockID:
		return workspace.ErrLockedByOther
	}

	if currentID, ok := data.current[w.ID.String()]; ok && !sv.Force {
		current := data.states[currentID]

		switch {
		case sv.Lineage != current.Lineage:
			return workspace.ErrLineageMismatch
		case sv.Serial <= current.Serial:
			return workspace.ErrStaleSerial
		}
	}

	return nil
}

// CurrentStateVersion implements workspace.StateStore.
func (s *fakeBackupStore) CurrentStateVersion(
	_ context.Context, id workspace.ID,
) (workspace.StateVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return workspace.StateVersion{}, errFakeClosed
	}

	currentID, ok := s.data.current[id.String()]
	if !ok {
		return workspace.StateVersion{}, workspace.ErrNoState
	}

	return s.data.states[currentID], nil
}

// ListStateVersions implements workspace.StateStore: the finalized versions
// of id, newest first by finalize time, ties by ID ascending, with the total.
func (s *fakeBackupStore) ListStateVersions(
	_ context.Context, id workspace.ID, page workspace.Page,
) ([]workspace.StateVersion, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, 0, errFakeClosed
	}

	all := []workspace.StateVersion{}

	for key := range s.data.states {
		if sv := s.data.states[key]; sv.Workspace == id && sv.Status == workspace.StatusFinalized {
			all = append(all, sv)
		}
	}

	slices.SortFunc(all, func(a, b workspace.StateVersion) int {
		if c := b.FinalizedAt.Compare(a.FinalizedAt); c != 0 {
			return c
		}

		return strings.Compare(a.ID.String(), b.ID.String())
	})

	if page.Limit <= 0 {
		page.Limit = workspace.DefaultStatePageSize
	}

	page.Limit = min(page.Limit, workspace.MaxStatePageSize)
	start := min(max(page.Offset, 0), len(all))
	end := min(start+page.Limit, len(all))

	return all[start:end], len(all), nil
}

// CountPendingStateVersions implements workspace.StateStore.
func (s *fakeBackupStore) CountPendingStateVersions(
	_ context.Context, id workspace.ID,
) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return 0, errFakeClosed
	}

	n := 0

	for key := range s.data.states {
		if sv := s.data.states[key]; sv.Workspace == id && sv.Status == workspace.StatusPending {
			n++
		}
	}

	return n, nil
}

// ReferencedContentKeys implements workspace.StateStore.
func (s *fakeBackupStore) ReferencedContentKeys(
	_ context.Context, id workspace.ID,
) ([]blob.Key, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, errFakeClosed
	}

	var out []blob.Key

	for key := range s.data.states {
		if sv := s.data.states[key]; sv.Workspace == id && sv.Content != "" {
			out = append(out, sv.Content)
		}
	}

	return out, nil
}

// RecordUploadAttempt implements workspace.StateStore.
func (s *fakeBackupStore) RecordUploadAttempt(
	_ context.Context, id workspace.StateVersionID, key blob.Key, fence namespace.Fence,
) (blob.Key, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return "", errFakeClosed
	}

	if err := s.checkFence(fence); err != nil {
		return "", err
	}

	sv, ok := s.data.states[id.String()]
	if !ok {
		return "", workspace.ErrStateVersionNotFound
	}

	if sv.Status != workspace.StatusPending {
		return "", workspace.ErrUploadDenied
	}

	superseded := sv.Content
	sv.Content = key
	s.data.states[id.String()] = sv

	return superseded, nil
}

// DeleteExpiredPendingStateVersions implements workspace.StateStore: it
// deletes the expired pending versions of id, oldest expiry first.
func (s *fakeBackupStore) DeleteExpiredPendingStateVersions(
	_ context.Context, id workspace.ID, before time.Time, fence namespace.Fence,
) ([]workspace.StateVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, errFakeClosed
	}

	if err := s.checkFence(fence); err != nil {
		return nil, err
	}

	var out []workspace.StateVersion

	for key := range s.data.states {
		if sv := s.data.states[key]; sv.Workspace == id && sv.Status == workspace.StatusPending &&
			sv.UploadExpiresAt.Before(before) {
			delete(s.data.states, key)

			out = append(out, sv)
		}
	}

	slices.SortFunc(out, func(a, b workspace.StateVersion) int {
		if c := a.UploadExpiresAt.Compare(b.UploadExpiresAt); c != 0 {
			return c
		}

		return strings.Compare(a.ID.String(), b.ID.String())
	})

	return out, nil
}

// Ping implements namespace.Store: it returns namespace.ErrUnavailable once
// the fake is closed.
func (s *fakeBackupStore) Ping(_ context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return namespace.ErrUnavailable
	}

	return nil
}

// GetState implements maintenance.Store.
func (s *fakeBackupStore) GetState(context.Context) (maintenance.State, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return "", errFakeClosed
	}

	return s.data.installation, nil
}

// SetState implements maintenance.Store.
func (s *fakeBackupStore) SetState(_ context.Context, state maintenance.State) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errFakeClosed
	}

	if state != maintenance.StateNormal && state != maintenance.StateMaintenance {
		return maintenance.ErrInvalidState
	}

	s.data.installation = state

	return nil
}

// SetStateIf implements maintenance.Store.
func (s *fakeBackupStore) SetStateIf(
	_ context.Context, expected, next maintenance.State,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errFakeClosed
	}

	if expected != maintenance.StateNormal && expected != maintenance.StateMaintenance {
		return maintenance.ErrInvalidState
	}

	if next != maintenance.StateNormal && next != maintenance.StateMaintenance {
		return maintenance.ErrInvalidState
	}

	if s.data.installation != expected {
		return maintenance.ErrStateChanged
	}

	s.data.installation = next

	return nil
}

// CreateBackup implements backup.Registry.
func (s *fakeBackupStore) CreateBackup(_ context.Context, r backup.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errFakeClosed
	}

	if _, ok := s.data.backups[r.ID.String()]; ok && !s.allowDuplicate {
		return backup.ErrAlreadyExists
	}

	s.data.backups[r.ID.String()] = r

	return nil
}

// FinishBackup implements backup.Registry.
func (s *fakeBackupStore) FinishBackup(
	_ context.Context, id backup.ID, state backup.State, errText string, endedAt time.Time,
) (backup.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return backup.Record{}, errFakeClosed
	}

	r, ok := s.data.backups[id.String()]
	if !ok {
		return backup.Record{}, backup.ErrNotFound
	}

	if state != backup.StateSucceeded && state != backup.StateFailed {
		return backup.Record{}, backup.ErrNotRunning
	}

	if r.State != backup.StateRunning && !s.allowRefinish {
		return backup.Record{}, backup.ErrNotRunning
	}

	r.State = state
	r.Error = errText
	r.EndedAt = endedAt
	s.data.backups[id.String()] = r

	return r, nil
}

// GetBackup implements backup.Registry.
func (s *fakeBackupStore) GetBackup(_ context.Context, id backup.ID) (backup.Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return backup.Record{}, errFakeClosed
	}

	r, ok := s.data.backups[id.String()]
	if !ok {
		return backup.Record{}, backup.ErrNotFound
	}

	return r, nil
}

// ListBackups implements backup.Registry, newest first, or oldest first
// when the oldestFirst knob breaks the order on purpose.
func (s *fakeBackupStore) ListBackups(_ context.Context) ([]backup.Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, errFakeClosed
	}

	out := make([]backup.Record, 0, len(s.data.backups))
	for _, r := range s.data.backups {
		out = append(out, r)
	}

	oldestFirst := s.oldestFirst

	slices.SortFunc(out, func(a, b backup.Record) int {
		if a.StartedAt.Equal(b.StartedAt) {
			switch {
			case a.ID < b.ID:
				return -1
			case a.ID > b.ID:
				return 1
			default:
				return 0
			}
		}

		after := a.StartedAt.After(b.StartedAt)
		if oldestFirst {
			after = !after
		}

		if after {
			return -1
		}

		return 1
	})

	return out, nil
}

// FailRunningBackups implements backup.Registry. The noopFailRunning knob
// leaves running rows alone on purpose.
func (s *fakeBackupStore) FailRunningBackups(_ context.Context, endedAt time.Time, errText string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errFakeClosed
	}

	if s.noopFailRunning {
		return nil
	}

	for id, r := range s.data.backups {
		if r.State == backup.StateRunning {
			r.State = backup.StateFailed
			r.Error = errText
			r.EndedAt = endedAt
			s.data.backups[id] = r
		}
	}

	return nil
}

// SchemaTables implements backup.Inspector. It reports the tables the fake
// implements, derived from the same table set the exporter covers, so both
// drift directions can fail: extraSchemaTable adds a table the backup list
// does not cover, and omitSchemaTable removes one it does cover.
func (s *fakeBackupStore) SchemaTables(_ context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, errFakeClosed
	}

	out := backup.TablesCopy()

	if s.extraSchemaTable != "" {
		out = append(out, s.extraSchemaTable)
	}

	if s.omitSchemaTable != "" {
		out = slices.DeleteFunc(out, func(table string) bool { return table == s.omitSchemaTable })
	}

	return out, nil
}

// Close implements storetest.Reopener: it releases the handle. The data stays
// for Reopen, unless the dropOnClose knob drops it on purpose.
func (s *fakeBackupStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.closed = true

	if s.dropOnClose {
		s.data = newFakeData()
	}

	return nil
}

// Reopen implements storetest.Reopener: it returns a new handle to the same
// data.
func (s *fakeBackupStore) Reopen() storetest.BackupStore { //nolint:ireturn // Reopener returns a new handle as the interface.
	return &fakeBackupStore{
		data:             s.data,
		staged:           nil,
		stageNext:        s.stageNext,
		stageCurrent:     0,
		stageLive:        map[int]bool{},
		dialect:          s.dialect,
		version:          s.version,
		mu:               sync.RWMutex{},
		betweenTables:    s.betweenTables,
		dropTable:        s.dropTable,
		extraSchemaTable: s.extraSchemaTable,
		omitSchemaTable:  s.omitSchemaTable,
		tear:             s.tear,
		skipEmptyCheck:   s.skipEmptyCheck,
		skipReferences:   s.skipReferences,
		ignoreManifest:   s.ignoreManifest,
		allowDuplicate:   s.allowDuplicate,
		allowRefinish:    s.allowRefinish,
		oldestFirst:      s.oldestFirst,
		noopFailRunning:  s.noopFailRunning,
		dropOnClose:      s.dropOnClose,
		peekTampered:     s.peekTampered,
		peekAnyWrapper:   s.peekAnyWrapper,
		peekNoCorrupt:    s.peekNoCorrupt,
		closed:           false,
	}
}

// fakeTableFrame is one table of the export stream.
type fakeTableFrame struct {
	Table string
	Rows  []byte
}

// fakeWorkspaceRows carries the workspaces table with its current-state
// pointer, which lives on the workspace rows.
type fakeWorkspaceRows struct {
	Workspaces map[string]workspace.Workspace
	Current    map[string]string
}

// fakeSummaryOf builds the summary of data. The caller holds the store lock.
func fakeSummaryOf(
	data *fakeData, dialect backup.Dialect, version int, counts map[string]int64,
) backup.Summary {
	regions := map[string]bool{}
	for _, ns := range data.namespaces {
		regions[ns.HomeRegion.String()] = true
	}

	wrappers := map[string]bool{}
	for _, k := range data.keys {
		wrappers[k.WrapperID] = true
	}

	blobs := make([]backup.BlobRef, 0)

	for id := range data.states {
		sv := data.states[id]
		if sv.Content == "" || sv.Status != workspace.StatusFinalized {
			continue
		}

		blobs = append(blobs, backup.BlobRef{MD5: sv.MD5, Key: sv.Content.String(), Size: sv.Size})
	}

	for _, byStack := range data.checkpoints {
		for id := range byStack {
			if cp := byStack[id]; cp.BlobKey != "" {
				blobs = append(blobs, backup.BlobRef{MD5: cp.BlobMD5, Key: cp.BlobKey.String(), Size: cp.BlobSize})
			}
		}
	}

	slices.SortFunc(blobs, func(a, b backup.BlobRef) int {
		switch {
		case a.Key < b.Key:
			return -1
		case a.Key > b.Key:
			return 1
		default:
			return 0
		}
	})

	return backup.Summary{
		Dialect:       dialect,
		HomeRegions:   slices.Sorted(maps.Keys(regions)),
		WrapperIDs:    slices.Sorted(maps.Keys(wrappers)),
		RowCounts:     counts,
		Blobs:         blobs,
		SchemaVersion: version,
	}
}

// rowCountsOf counts the rows of data per table.
func rowCountsOf(data *fakeData) map[string]int64 {
	tags := 0

	for ref := range data.stacks {
		tags += len(data.stacks[ref].Tags)
	}

	updates := 0

	for _, byStack := range data.updates {
		updates += len(byStack)
	}

	checkpoints := 0

	for _, byStack := range data.checkpoints {
		checkpoints += len(byStack)
	}

	events := 0

	for _, byStack := range data.events {
		for _, list := range byStack {
			events += len(list)
		}
	}

	return map[string]int64{
		"namespaces":     int64(len(data.namespaces)),
		"stacks":         int64(len(data.stacks)),
		"stack_tags":     int64(tags),
		"updates":        int64(updates),
		"checkpoints":    int64(checkpoints),
		"update_events":  int64(events),
		"secret_keys":    int64(len(data.keys)),
		"inventory":      int64(len(data.entries)),
		"workspaces":     int64(len(data.workspaces)),
		"state_versions": int64(len(data.states)),
		"backups":        int64(len(data.backups)),
		// The installation table holds its one seeded row in every store,
		// as in the SQL adapters.
		"installation": 1,
	}
}

// Export implements backup.Exporter. Without the tear knob it encodes every
// table under one read lock, so the capture is consistent. With the knob it
// releases the lock between tables and calls betweenTables, so a concurrent
// write lands in some tables but not others.
func (s *fakeBackupStore) Export(_ context.Context, w io.Writer) (backup.Summary, error) {
	if s.isClosed() {
		return backup.Summary{}, errFakeClosed
	}

	counts := make(map[string]int64, len(backup.Tables))

	if s.tear {
		if err := s.exportTorn(w, counts); err != nil {
			return backup.Summary{}, err
		}
	} else if err := s.exportLocked(w, counts); err != nil {
		return backup.Summary{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return fakeSummaryOf(s.data, s.dialect, s.version, counts), nil
}

// Import implements backup.Importer: manifest checks first, then an empty
// destination, then the load in one locked step. The empty check runs again
// under the write lock before the swap, so a concurrent writer cannot slip
// between the check and the load. A failed import changes nothing.
func (s *fakeBackupStore) Import(
	_ context.Context, r io.Reader, want backup.Summary,
) (backup.Summary, error) {
	if s.isClosed() {
		return backup.Summary{}, errFakeClosed
	}

	if err := s.checkManifest(want); err != nil {
		return backup.Summary{}, err
	}

	if !s.skipEmptyCheck && !s.empty() {
		return backup.Summary{}, backup.ErrNotEmpty
	}

	loaded, err := decodeBackup(r)
	if err != nil {
		return backup.Summary{}, err
	}

	if countErr := checkRowCounts(loaded, want); countErr != nil {
		return backup.Summary{}, countErr
	}

	return s.swapLoaded(loaded)
}

// StageRestore implements backup.Stager: it decodes the stream into a staging
// copy and checks it (foreign keys, row counts, blob references), without
// touching the live tables. A failed stage keeps no staging copy.
func (s *fakeBackupStore) StageRestore(
	_ context.Context, r io.Reader, want backup.Summary,
) (backup.Summary, error) {
	if s.isClosed() {
		return backup.Summary{}, errFakeClosed
	}

	if err := s.checkManifest(want); err != nil {
		return backup.Summary{}, err
	}

	loaded, err := decodeBackup(r)
	if err != nil {
		return backup.Summary{}, err
	}

	if !s.skipReferences {
		if err := checkStagedReferences(loaded); err != nil {
			return backup.Summary{}, err
		}
	}

	if err := checkRowCounts(loaded, want); err != nil {
		return backup.Summary{}, err
	}

	staged := fakeSummaryOf(loaded, s.dialect, s.version, rowCountsOf(loaded))

	if err := checkStagedBlobRefs(staged.Blobs, want.Blobs); err != nil {
		return backup.Summary{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.stageCurrent != 0 {
		delete(s.stageLive, s.stageCurrent)
	}

	s.stageNext++
	s.stageCurrent = s.stageNext

	if s.stageLive == nil {
		s.stageLive = map[int]bool{}
	}

	s.stageLive[s.stageCurrent] = true
	s.staged = loaded

	return staged, nil
}

// DiscardStaging implements backup.Stager: it drops the staging copy, if any.
// It leaves the live tables unchanged.
func (s *fakeBackupStore) DiscardStaging(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errFakeClosed
	}

	if s.stageCurrent != 0 {
		delete(s.stageLive, s.stageCurrent)
		s.stageCurrent = 0
	}

	s.staged = nil

	return nil
}

// CurrentStagingID implements storetest.StagingWitness: the generation of the
// current staging copy, or empty when none exists.
func (s *fakeBackupStore) CurrentStagingID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.stageCurrent == 0 {
		return ""
	}

	return fmt.Sprintf("stage-%d", s.stageCurrent)
}

// StagingAlive implements storetest.StagingWitness: whether the staging copy
// with id still exists. Only the current staging area is alive: a new stage
// discards the previous one.
func (s *fakeBackupStore) StagingAlive(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for live := range s.stageLive {
		if fmt.Sprintf("stage-%d", live) == id {
			return true
		}
	}

	return false
}

// TamperNamespaces implements storetest.ExportTamperer: it empties the
// namespaces frame of the export and returns the tampered bytes with a
// manifest whose namespace count matches them, so only the foreign-key check
// of the stage can fail.
func (s *fakeBackupStore) TamperNamespaces(
	t *testing.T, data []byte, sum backup.Summary,
) ([]byte, backup.Summary) {
	t.Helper()

	frames := decodeFrames(t, data)

	var empty bytes.Buffer

	if err := gob.NewEncoder(&empty).Encode(map[string]namespace.Namespace{}); err != nil {
		t.Fatalf("encode empty namespaces: %v", err)
	}

	var out bytes.Buffer

	enc := gob.NewEncoder(&out)

	for _, frame := range frames {
		if frame.Table == "namespaces" {
			frame.Rows = empty.Bytes()
		}

		if err := enc.Encode(frame); err != nil {
			t.Fatalf("write table %s: %v", frame.Table, err)
		}
	}

	sum.RowCounts = maps.Clone(sum.RowCounts)
	sum.RowCounts["namespaces"] = 0

	return out.Bytes(), sum
}

// decodeFrames decodes every frame of the export stream in order, failing on
// error.
func decodeFrames(t *testing.T, data []byte) []fakeTableFrame {
	t.Helper()

	var frames []fakeTableFrame

	dec := gob.NewDecoder(bytes.NewReader(data))

	for {
		var frame fakeTableFrame

		if err := dec.Decode(&frame); err != nil {
			if errors.Is(err, io.EOF) {
				return frames
			}

			t.Fatalf("decode export: %v", err)
		}

		frames = append(frames, frame)
	}
}

// checkStagedReferences reports a child row without its parent as corruption:
// stacks need their namespace, updates their stack, checkpoints and events
// their update, keys their namespace and stack, inventory and workspaces their
// namespace, state versions their workspace, and the current pointer both.
func checkStagedReferences(loaded *fakeData) error {
	stackIDs, err := checkStagedStacks(loaded)
	if err != nil {
		return err
	}

	if err := checkStagedUpdates(loaded, stackIDs); err != nil {
		return err
	}

	if err := checkStagedKeys(loaded, stackIDs); err != nil {
		return err
	}

	if err := checkStagedUnits(loaded); err != nil {
		return err
	}

	return checkStagedStates(loaded)
}

// checkStagedStacks reports a staged stack without its namespace as corruption.
// It returns the staged stack IDs for the child checks.
func checkStagedStacks(loaded *fakeData) (map[string]bool, error) {
	stackIDs := make(map[string]bool, len(loaded.stacks))

	for ref := range loaded.stacks {
		st := loaded.stacks[ref]
		stackIDs[st.ID.String()] = true

		if _, ok := loaded.namespaces[st.Ref.Namespace.String()]; !ok {
			return nil, fmt.Errorf(
				"staged stack %s misses namespace %q: %w",
				st.Ref,
				st.Ref.Namespace,
				backup.ErrCorrupt,
			)
		}
	}

	return stackIDs, nil
}

// checkStagedUpdates reports staged updates without their stack and staged
// checkpoints and events without their update as corruption.
func checkStagedUpdates(loaded *fakeData, stackIDs map[string]bool) error {
	for stackID, byUpdate := range loaded.updates {
		if !stackIDs[stackID] {
			return fmt.Errorf("staged updates miss stack %q: %w", stackID, backup.ErrCorrupt)
		}

		if err := checkStagedHistory(loaded, stackID, byUpdate); err != nil {
			return err
		}
	}

	return nil
}

// checkStagedKeys reports a staged data key without its namespace or stack as
// corruption.
func checkStagedKeys(loaded *fakeData, stackIDs map[string]bool) error {
	for scope := range loaded.keys {
		k := loaded.keys[scope]

		if _, ok := loaded.namespaces[k.Scope.Namespace.String()]; !ok {
			return fmt.Errorf("staged key %s misses namespace %q: %w",
				scope, k.Scope.Namespace, backup.ErrCorrupt)
		}

		if !stackIDs[k.Scope.ID] {
			return fmt.Errorf("staged key %s misses stack %q: %w", scope, k.Scope.ID, backup.ErrCorrupt)
		}
	}

	return nil
}

// checkStagedUnits reports a staged inventory entry or workspace without its
// namespace as corruption.
func checkStagedUnits(loaded *fakeData) error {
	for key := range loaded.entries {
		if _, ok := loaded.namespaces[loaded.entries[key].Namespace.String()]; !ok {
			return fmt.Errorf(
				"staged inventory entry %q misses namespace %q: %w",
				key,
				loaded.entries[key].Namespace,
				backup.ErrCorrupt,
			)
		}
	}

	for id := range loaded.workspaces {
		if _, ok := loaded.namespaces[loaded.workspaces[id].Ref.Namespace.String()]; !ok {
			return fmt.Errorf(
				"staged workspace %s misses namespace %q: %w",
				loaded.workspaces[id].Ref,
				loaded.workspaces[id].Ref.Namespace,
				backup.ErrCorrupt,
			)
		}
	}

	return nil
}

// checkStagedStates reports a staged state version without its workspace and a
// staged current pointer without its workspace or state version as corruption.
func checkStagedStates(loaded *fakeData) error {
	for id := range loaded.states {
		if _, ok := loaded.workspaces[loaded.states[id].Workspace.String()]; !ok {
			return fmt.Errorf(
				"staged state version %s misses workspace %q: %w",
				loaded.states[id].ID,
				loaded.states[id].Workspace,
				backup.ErrCorrupt,
			)
		}
	}

	for workspaceID, stateID := range loaded.current {
		if _, ok := loaded.workspaces[workspaceID]; !ok {
			return fmt.Errorf("staged current pointer misses workspace %q: %w", workspaceID, backup.ErrCorrupt)
		}

		if _, ok := loaded.states[stateID]; !ok {
			return fmt.Errorf("staged current pointer misses state version %q: %w", stateID, backup.ErrCorrupt)
		}
	}

	return nil
}

// checkStagedHistory reports the checkpoints and events of one staged stack
// without their update row as corruption.
func checkStagedHistory(loaded *fakeData, stackID string, byUpdate map[string]stack.Update) error {
	for id := range loaded.checkpoints[stackID] {
		if _, ok := byUpdate[id]; !ok {
			return fmt.Errorf("staged checkpoints miss update %q: %w", id, backup.ErrCorrupt)
		}
	}

	for id := range loaded.events[stackID] {
		if _, ok := byUpdate[id]; !ok {
			return fmt.Errorf("staged events miss update %q: %w", id, backup.ErrCorrupt)
		}
	}

	return nil
}

// checkStagedBlobRefs compares the blob references of the staged data with the
// manifest: the staged data must name exactly the keys of the manifest, each
// with its size and checksum.
func checkStagedBlobRefs(staged, want []backup.BlobRef) error {
	byKey := func(a, b backup.BlobRef) int { return strings.Compare(a.Key, b.Key) }

	ordered := slices.Clone(staged)
	slices.SortFunc(ordered, byKey)

	wanted := slices.Clone(want)
	slices.SortFunc(wanted, byKey)

	same := func(a, b backup.BlobRef) bool {
		return a.Key == b.Key && a.Size == b.Size && a.MD5 == b.MD5
	}

	if !slices.EqualFunc(ordered, wanted, same) {
		return fmt.Errorf(
			"staged backup names %d blob references, the manifest names %d: %w",
			len(ordered),
			len(wanted),
			backup.ErrCorrupt,
		)
	}

	return nil
}

// PeekKey implements backup.KeyPeeker: it decodes the export stream frame by
// frame and returns one data key of wrapperID from secret_keys. The
// peekTampered knob returns the key with a tampered ciphertext on purpose.
// The peekAnyWrapper knob returns the first key whatever wrapperID holds on
// purpose. The peekNoCorrupt knob returns no error on a corrupt stream on
// purpose.
func (s *fakeBackupStore) PeekKey(_ context.Context, r io.Reader, wrapperID string) (secret.WrappedKey, error) {
	var none secret.WrappedKey

	keys, err := decodePeekKeys(r)
	switch {
	case err != nil && s.peekNoCorrupt:
		return none, nil
	case err != nil:
		return none, err
	case s.peekAnyWrapper:
		if first, ok := firstPeekKey(keys); ok {
			return s.peekResult(first), nil
		}

		return none, fmt.Errorf(
			"backup holds no data key of wrapper %q: %w", wrapperID, backup.ErrCorrupt,
		)
	default:
		for _, scope := range slices.Sorted(maps.Keys(keys)) {
			if keys[scope].WrapperID == wrapperID {
				return s.peekResult(keys[scope]), nil
			}
		}

		return none, fmt.Errorf(
			"backup holds no data key of wrapper %q: %w", wrapperID, backup.ErrCorrupt,
		)
	}
}

// latestResourceCount returns the managed resource count of the latest
// checkpoint of the stack with stackID, or 0 when it has none. Callers hold
// the write lock, so the check runs in the same step as the delete.
func (s *fakeBackupStore) latestResourceCount(stackID string) int64 {
	var (
		best  stack.Checkpoint
		found bool
	)

	for key := range s.data.checkpoints[stackID] {
		cp := s.data.checkpoints[stackID][key]
		u := s.data.updates[stackID][cp.Update.String()]

		if !found || u.Version > s.data.updates[stackID][best.Update.String()].Version {
			best, found = cp, true
		}
	}

	if !found {
		return 0
	}

	return stack.CountManagedResources(best.Deployment)
}

// removeStack deletes ref with the stack st, its children, key and entry.
// Callers hold the write lock.
func (s *fakeBackupStore) removeStack(ref stack.Ref, st stack.Stack) {
	delete(s.data.stacks, ref.String())
	delete(s.data.stackIDs, st.ID.String())
	delete(s.data.updates, st.ID.String())
	delete(s.data.checkpoints, st.ID.String())
	delete(s.data.events, st.ID.String())
	delete(s.data.entries, inventoryKey(ref.Namespace, "pulumi", ref.Project.String()+"/"+ref.Stack.String()))

	for scope, key := range s.data.keys {
		if key.Scope.Namespace == ref.Namespace && key.Scope.ID == st.ID.String() {
			delete(s.data.keys, scope)
		}
	}
}

// decodePeekKeys decodes the export stream frame by frame and returns the
// secret_keys rows, or an empty map when the stream holds no secret_keys
// frame. A corrupt stream reports ErrCorrupt.
func decodePeekKeys(r io.Reader) (map[string]secret.WrappedKey, error) {
	dec := gob.NewDecoder(r)

	for {
		var frame fakeTableFrame

		if err := dec.Decode(&frame); err != nil {
			if errors.Is(err, io.EOF) {
				return map[string]secret.WrappedKey{}, nil
			}

			return nil, fmt.Errorf("decode backup stream: %w", backup.ErrCorrupt)
		}

		if frame.Table != "secret_keys" {
			continue
		}

		var keys map[string]secret.WrappedKey

		if err := gob.NewDecoder(bytes.NewReader(frame.Rows)).Decode(&keys); err != nil {
			return nil, fmt.Errorf("decode backup table %q: %w", frame.Table, backup.ErrCorrupt)
		}

		return keys, nil
	}
}

// firstPeekKey returns the first key by scope, for the knob that ignores the
// wrapper on purpose.
func firstPeekKey(keys map[string]secret.WrappedKey) (secret.WrappedKey, bool) {
	for _, scope := range slices.Sorted(maps.Keys(keys)) {
		return keys[scope], true
	}

	var none secret.WrappedKey

	return none, false
}

// peekResult returns k, tampering its ciphertext when the peekTampered knob
// breaks the peek on purpose.
func (s *fakeBackupStore) peekResult(k secret.WrappedKey) secret.WrappedKey {
	if !s.peekTampered {
		return k
	}

	k.Ciphertext = append(bytes.Clone(k.Ciphertext), 0x00)

	return k
}

// checkManifest refuses a manifest of another dialect or schema version,
// unless the ignoreManifest knob accepts any manifest on purpose.
func (s *fakeBackupStore) checkManifest(want backup.Summary) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.ignoreManifest {
		return nil
	}
	if want.Dialect != s.dialect {
		return fmt.Errorf(
			"import backup of dialect %q into %q: %w",
			want.Dialect,
			s.dialect,
			backup.ErrDialectMismatch,
		)
	}

	if want.SchemaVersion != s.version {
		return fmt.Errorf(
			"import backup of schema version %d into version %d: %w",
			want.SchemaVersion,
			s.version,
			backup.ErrSchemaMismatch,
		)
	}

	return nil
}

// decodeBackup decodes every frame of the stream into fresh tables.
func decodeBackup(r io.Reader) (*fakeData, error) {
	loaded := newFakeData()
	seen := map[string]bool{}
	dec := gob.NewDecoder(r)

	for {
		var frame fakeTableFrame

		if err := dec.Decode(&frame); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return nil, fmt.Errorf("decode backup stream: %w", backup.ErrCorrupt)
		}

		if !slices.Contains(backup.Tables, frame.Table) || seen[frame.Table] {
			return nil, fmt.Errorf("decode backup table %q: %w", frame.Table, backup.ErrCorrupt)
		}

		seen[frame.Table] = true

		if err := decodeTable(loaded, frame); err != nil {
			return nil, err
		}
	}

	return loaded, nil
}

// checkRowCounts compares the loaded row counts with the manifest. A table
// the export does not cover reads as zero rows, so an exporter that drops a
// table still loads: the round trip then shows the missing rows. A count that
// differs from the manifest, a table outside backup.Tables, or a table the
// manifest omits is corruption: an omitted empty table cannot slip through,
// matching both SQL adapters.
func checkRowCounts(loaded *fakeData, want backup.Summary) error {
	counts := rowCountsOf(loaded)

	for _, table := range backup.Tables {
		if _, ok := want.RowCounts[table]; !ok {
			return fmt.Errorf("import manifest omits table %q: %w", table, backup.ErrCorrupt)
		}

		if counts[table] != want.RowCounts[table] {
			return fmt.Errorf(
				"import row counts %v differ from manifest %v: %w",
				counts,
				want.RowCounts,
				backup.ErrCorrupt,
			)
		}
	}

	for table := range want.RowCounts {
		if !slices.Contains(backup.Tables, table) {
			return fmt.Errorf("import manifest names unknown table %q: %w", table, backup.ErrCorrupt)
		}
	}

	return nil
}

// swapLoaded replaces the store data with the loaded tables in one locked
// step. It rechecks emptiness under the write lock, so a writer that lands
// after the first check is refused instead of silently overwritten. Real
// adapters do the check plus the load in one transaction.
func (s *fakeBackupStore) swapLoaded(loaded *fakeData) (backup.Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return backup.Summary{}, errFakeClosed
	}

	if !s.skipEmptyCheck {
		for table, n := range rowCountsOf(s.data) {
			// The seeded installation row is bookkeeping, not data.
			if table == "installation" {
				continue
			}

			if n != 0 {
				return backup.Summary{}, backup.ErrNotEmpty
			}
		}
	}

	s.data = loaded
	s.rebuildIndexes()

	return fakeSummaryOf(s.data, s.dialect, s.version, rowCountsOf(s.data)), nil
}

// tableRows gob-encodes one table and counts its rows. The caller holds the
// store lock.
func (s *fakeBackupStore) tableRows(table string) ([]byte, int64, error) {
	value, ok := s.snapshotTables()[table]
	if !ok {
		return nil, 0, fmt.Errorf("export table %s: %w", table, backup.ErrCorrupt)
	}

	var buf bytes.Buffer

	if err := gob.NewEncoder(&buf).Encode(value); err != nil {
		return nil, 0, fmt.Errorf("encode table %s: %w", table, err)
	}

	return buf.Bytes(), rowCountsOf(s.data)[table], nil
}

// snapshotTables returns the encodable value of every table. The caller holds
// the store lock.
func (s *fakeBackupStore) snapshotTables() map[string]any {
	return map[string]any{
		"namespaces":     s.data.namespaces,
		"stacks":         s.data.stacks,
		"stack_tags":     s.stackTags(),
		"updates":        s.data.updates,
		"checkpoints":    s.data.checkpoints,
		"update_events":  s.data.events,
		"secret_keys":    s.data.keys,
		"inventory":      s.data.entries,
		"workspaces":     fakeWorkspaceRows{Workspaces: s.data.workspaces, Current: s.data.current},
		"state_versions": s.data.states,
		"backups":        s.data.backups,
		"installation":   s.data.installation,
	}
}

// stackTags returns the tags of every stack keyed by stack ID. The caller
// holds the store lock.
func (s *fakeBackupStore) stackTags() map[string]map[string]string {
	tags := make(map[string]map[string]string, len(s.data.stacks))

	for ref := range s.data.stacks {
		st := s.data.stacks[ref]
		tags[st.ID.String()] = maps.Clone(st.Tags)
	}

	return tags
}

// exportLocked encodes every table under one read lock. The counts map
// gathers the row count of each table.
func (s *fakeBackupStore) exportLocked(w io.Writer, counts map[string]int64) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	enc := gob.NewEncoder(w)

	for _, table := range backup.Tables {
		if table == s.dropTable {
			continue
		}

		if err := s.writeTable(enc, counts, table); err != nil {
			return err
		}
	}

	return nil
}

// writeTable encodes one table and records its row count. The caller holds
// the store lock.
func (s *fakeBackupStore) writeTable(enc *gob.Encoder, counts map[string]int64, table string) error {
	rows, count, err := s.tableRows(table)
	if err != nil {
		return err
	}

	if err := enc.Encode(fakeTableFrame{Table: table, Rows: rows}); err != nil {
		return fmt.Errorf("write table %s: %w", table, err)
	}

	counts[table] = count

	return nil
}

// exportTorn encodes one table per read lock and calls betweenTables after
// each, so a concurrent write lands in some tables but not others.
func (s *fakeBackupStore) exportTorn(w io.Writer, counts map[string]int64) error {
	enc := gob.NewEncoder(w)

	for _, table := range backup.Tables {
		if table == s.dropTable {
			continue
		}

		s.mu.RLock()
		rows, count, err := s.tableRows(table)
		hook := s.betweenTables
		s.mu.RUnlock()

		if err != nil {
			return err
		}

		if err := enc.Encode(fakeTableFrame{Table: table, Rows: rows}); err != nil {
			return fmt.Errorf("write table %s: %w", table, err)
		}

		counts[table] = count

		if hook != nil {
			hook(table)
		}
	}

	return nil
}

// checkFence reports a missing namespace or a stale failover version. The
// caller holds the store lock.
func (s *fakeBackupStore) checkFence(fence namespace.Fence) error {
	ns, ok := s.data.namespaces[fence.Namespace.String()]
	if !ok {
		return namespace.ErrNotFound
	}

	if ns.FailoverVersion != fence.FailoverVersion {
		return namespace.ErrFenced
	}

	return nil
}

// stackIDOf returns the stable ID of ref. The caller holds the store lock.
func (s *fakeBackupStore) stackIDOf(ref stack.Ref) (string, error) {
	st, ok := s.data.stacks[ref.String()]
	if !ok {
		return "", stack.ErrNotFound
	}

	return st.ID.String(), nil
}

// publishStack writes the Pulumi inventory entry of ref. The caller holds the
// store lock.
func (s *fakeBackupStore) publishStack(ref stack.Ref, unitID string, changed time.Time) {
	name := ref.Project.String() + "/" + ref.Stack.String()
	s.data.entries[inventoryKey(ref.Namespace, "pulumi", name)] = inventory.Entry{
		LastChanged:   changed,
		Namespace:     ref.Namespace,
		Tool:          "pulumi",
		Name:          name,
		UnitID:        unitID,
		DisplayName:   name,
		Status:        "",
		ResourceCount: nil,
		Lock:          nil,
	}
}

// publishWorkspace writes the OpenTofu inventory entry of ref. The caller
// holds the store lock.
func (s *fakeBackupStore) publishWorkspace(ref workspace.Ref, unitID string, changed time.Time) {
	name := ref.Name.String()
	s.data.entries[inventoryKey(ref.Namespace, "opentofu", name)] = inventory.Entry{
		LastChanged:   changed,
		Namespace:     ref.Namespace,
		Tool:          "opentofu",
		Name:          name,
		UnitID:        unitID,
		DisplayName:   name,
		Status:        "",
		ResourceCount: nil,
		Lock:          nil,
	}
}

// highestUpdateVersion returns the highest version of any update of the
// stack. The caller holds the store lock.
func (s *fakeBackupStore) highestUpdateVersion(stackID string) int64 {
	var highest int64

	for id := range s.data.updates[stackID] {
		if version := s.data.updates[stackID][id].Version; version > highest {
			highest = version
		}
	}

	return highest
}

// checkRunningUpdate reports whether the update may accept a checkpoint or
// events. The caller holds the store lock.
func (s *fakeBackupStore) checkRunningUpdate(ref stack.Ref, id stack.UpdateID, lease [32]byte) error {
	stackID, err := s.stackIDOf(ref)
	if err != nil {
		return stack.ErrUpdateNotFound
	}

	u, ok := s.data.updates[stackID][id.String()]
	if !ok {
		return stack.ErrUpdateNotFound
	}

	if u.Status != stack.StatusRunning || u.Lease.Digest != lease || !u.Mutates() {
		return stack.ErrWrongUpdateState
	}

	return nil
}

// isClosed reports whether the handle is closed.
func (s *fakeBackupStore) isClosed() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.closed
}

// empty reports whether the store holds no row. The seeded installation row
// is bookkeeping, not data, so it never counts: a fresh store is empty.
func (s *fakeBackupStore) empty() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for table, n := range rowCountsOf(s.data) {
		if table == "installation" {
			continue
		}

		if n != 0 {
			return false
		}
	}

	return true
}

// rebuildIndexes rebuilds the reference indexes from the rows. The caller
// holds the store lock.
func (s *fakeBackupStore) rebuildIndexes() {
	s.data.stackIDs = map[string]string{}
	for ref := range s.data.stacks {
		s.data.stackIDs[s.data.stacks[ref].ID.String()] = ref
	}

	s.data.workspaceIDs = map[string]string{}
	for id := range s.data.workspaces {
		s.data.workspaceIDs[s.data.workspaces[id].Ref.String()] = id
	}
}

// decodeTable decodes one frame into loaded.
func decodeTable(loaded *fakeData, frame fakeTableFrame) error {
	dec := gob.NewDecoder(bytes.NewReader(frame.Rows))

	if frame.Table == "stack_tags" {
		var v map[string]map[string]string
		if err := dec.Decode(&v); err != nil {
			return fmt.Errorf("decode backup table %q: %w", frame.Table, backup.ErrCorrupt)
		}

		// Tags ride with their stack rows; apply them to the stacks frame,
		// which arrives first in foreign-key order.
		applyTags(loaded, v)

		return nil
	}

	if frame.Table == "workspaces" {
		var v fakeWorkspaceRows
		if err := dec.Decode(&v); err != nil {
			return fmt.Errorf("decode backup table %q: %w", frame.Table, backup.ErrCorrupt)
		}

		loaded.workspaces = v.Workspaces
		loaded.current = v.Current

		return nil
	}

	target, ok := backupDecodeTargets(loaded)[frame.Table]
	if !ok {
		return fmt.Errorf("decode backup table %q: %w", frame.Table, backup.ErrCorrupt)
	}

	if err := dec.Decode(target); err != nil {
		return fmt.Errorf("decode backup table %q: %w", frame.Table, backup.ErrCorrupt)
	}

	return nil
}

// backupDecodeTargets returns the decode target of every table stored
// directly: a pointer to its field in loaded. The stack_tags and workspaces
// frames need post-processing, so decodeTable handles them separately.
func backupDecodeTargets(loaded *fakeData) map[string]any {
	return map[string]any{
		"namespaces":     &loaded.namespaces,
		"stacks":         &loaded.stacks,
		"updates":        &loaded.updates,
		"checkpoints":    &loaded.checkpoints,
		"update_events":  &loaded.events,
		"secret_keys":    &loaded.keys,
		"inventory":      &loaded.entries,
		"state_versions": &loaded.states,
		"backups":        &loaded.backups,
		"installation":   &loaded.installation,
	}
}

// applyTags sets the tags of each stack from the stack_tags frame. Stack IDs
// in the frame key the stacks of loaded, which must already hold the stacks
// frame: frames arrive in foreign-key order. A tag without its stack row is
// torn data; the import still succeeds and the comparison shows the orphan.
func applyTags(loaded *fakeData, tags map[string]map[string]string) {
	byID := make(map[string]string, len(loaded.stacks))
	for ref := range loaded.stacks {
		byID[loaded.stacks[ref].ID.String()] = ref
	}

	for stackID, tagMap := range tags {
		ref, ok := byID[stackID]
		if !ok {
			continue
		}

		st := loaded.stacks[ref]
		st.Tags = tagMap
		loaded.stacks[ref] = st
	}
}

// fakeFactory returns a fresh fake for one subtest.
func fakeFactory(t *testing.T) storetest.BackupStore { //nolint:ireturn // Factory signature returns the interface.
	t.Helper()

	return newFakeBackupStore()
}

// TestBackupConformance runs the backup suite against the fake. The SQL
// adapters run the same suite once they implement the ports.
func TestBackupConformance(t *testing.T) {
	t.Parallel()

	storetest.RunBackup(t, fakeFactory)
}

// fakeNamespace creates a namespace homed in region.
func fakeNamespace(t *testing.T, s storetest.BackupStore, name, region string) namespace.Namespace {
	t.Helper()

	ns := namespace.Namespace{
		CreatedAt:       fakeTime(),
		Name:            namespace.Name(name),
		HomeRegion:      namespace.Region(region),
		FailoverVersion: 1,
	}

	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace %q: %v", name, err)
	}

	return ns
}

// fakeStack creates a stack with tags.
func fakeStack(
	t *testing.T, s storetest.BackupStore, ns namespace.Namespace, name string, tags map[string]string,
) stack.Stack {
	t.Helper()

	st := stack.Stack{
		CreatedAt: fakeTime(),
		UpdatedAt: fakeTime(),
		Tags:      tags,
		Ref: stack.Ref{
			Namespace: ns.Name,
			Project:   "web",
			Stack:     stack.Name(name),
		},
		ID:      stack.ID(ident.NewID("stk-")),
		Version: 0,
	}

	if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
		t.Fatalf("create stack %s: %v", st.Ref, err)
	}

	return st
}

// fakeKey stores a wrapped data key for the stack.
func fakeKey(t *testing.T, s storetest.BackupStore, ns namespace.Namespace, st stack.Stack, wrapper string) {
	t.Helper()

	scope, err := secret.NewScope(ns.Name, st.ID.String())
	if err != nil {
		t.Fatalf("new scope of %s: %v", st.Ref, err)
	}

	key := secret.WrappedKey{
		CreatedAt:  fakeTime(),
		Scope:      scope,
		WrapperID:  wrapper,
		Ciphertext: []byte("wrapped-" + st.Ref.Stack.String()),
	}

	if _, err := s.CreateKey(t.Context(), key, ns.Fence()); err != nil {
		t.Fatalf("create key of %s: %v", st.Ref, err)
	}
}

// fakeExport exports s into memory.
func fakeExport(t *testing.T, s storetest.BackupStore) (backup.Summary, []byte) {
	t.Helper()

	var buf bytes.Buffer

	sum, err := s.Export(t.Context(), &buf)
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	return sum, buf.Bytes()
}

// fakeScope returns the secret scope of st.
func fakeScope(t *testing.T, ns namespace.Namespace, st stack.Stack) secret.Scope {
	t.Helper()

	scope, err := secret.NewScope(ns.Name, st.ID.String())
	if err != nil {
		t.Fatalf("new scope of %s: %v", st.Ref, err)
	}

	return scope
}

// TestBackupDetectsDroppedTable breaks the exporter so it skips secret_keys.
// The export then misses the table from its row counts, and the import
// refuses the omitted table with ErrCorrupt: the table-list case and the
// round-trip case of RunBackup both fail on this fake.
func TestBackupDetectsDroppedTable(t *testing.T) {
	t.Parallel()

	src := newFakeBackupStore()
	src.dropTable = "secret_keys"

	ns := fakeNamespace(t, src, "acme", "eu-west")
	st := fakeStack(t, src, ns, "prod", map[string]string{"team": "platform"})
	fakeKey(t, src, ns, st, "kek-1")

	sum, data := fakeExport(t, src)

	if _, ok := sum.RowCounts["secret_keys"]; ok {
		t.Fatalf("export row counts = %v, want no secret_keys", sum.RowCounts)
	}

	dst := newFakeBackupStore()

	if _, err := dst.Import(t.Context(), bytes.NewReader(data), sum); !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("import of an export that drops secret_keys: err = %v, want ErrCorrupt", err)
	}

	if _, err := dst.GetKey(t.Context(), fakeScope(t, ns, st)); !errors.Is(err, secret.ErrKeyNotFound) {
		t.Fatalf("get key after a refused import: err = %v, want ErrKeyNotFound", err)
	}
}

// TestBackupDetectsTornExport releases the export lock between tables and
// writes a stack after the stacks table but before stack_tags. The import
// then holds the inventory entry of a stack it does not hold: the no-tear
// case of RunBackup fails on this fake.
func TestBackupDetectsTornExport(t *testing.T) {
	t.Parallel()

	src := newFakeBackupStore()
	src.tear = true

	ns := fakeNamespace(t, src, "acme", "eu-west")
	first := fakeStack(t, src, ns, "prod", map[string]string{"team": "platform"})

	var added stack.Stack

	src.betweenTables = func(table string) {
		if table != "stacks" {
			return
		}

		// Empty tags keep the tag count stable, so the import loads: the
		// stacks table misses the stack while the inventory names it.
		added = fakeStack(t, src, ns, "dev", map[string]string{})
	}

	sum, data := fakeExport(t, src)

	dst := newFakeBackupStore()

	if _, err := dst.Import(t.Context(), bytes.NewReader(data), sum); err != nil {
		t.Fatalf("import: %v", err)
	}

	if _, err := dst.GetStack(t.Context(), added.Ref); !errors.Is(err, stack.ErrNotFound) {
		t.Fatalf("get torn stack %s: err = %v, want ErrNotFound", added.Ref, err)
	}

	entries, err := dst.ListUnits(t.Context(), ns.Name)
	if err != nil {
		t.Fatalf("list inventory after import: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("inventory entries after import = %d, want 2 with the torn stack", len(entries))
	}

	got, err := dst.GetStack(t.Context(), first.Ref)
	if err != nil {
		t.Fatalf("get stack %s after import: %v", first.Ref, err)
	}

	if !maps.Equal(got.Tags, first.Tags) {
		t.Fatalf("tags after import = %v, want %v", got.Tags, first.Tags)
	}
}

// TestBackupDetectsUnknownSchemaTable adds a table to the schema that the
// backup table list does not cover. The table-list case of RunBackup fails
// on this fake, which forces a new migration to add its table to the list.
func TestBackupDetectsUnknownSchemaTable(t *testing.T) {
	t.Parallel()

	s := newFakeBackupStore()
	s.extraSchemaTable = "ghost_table"

	schema, err := s.SchemaTables(t.Context())
	if err != nil {
		t.Fatalf("list schema tables: %v", err)
	}

	if !slices.Contains(schema, "ghost_table") {
		t.Fatalf("schema tables = %v, want ghost_table", schema)
	}

	if slices.Contains(backup.Tables, "ghost_table") {
		t.Fatal("backup table list covers ghost_table, want it missing")
	}
}

// TestBackupDetectsMissingSchemaTable removes a table from the schema that
// the backup table list covers. The table-list case of RunBackup fails on
// this fake in the other direction: the list names a table the schema does
// not hold.
func TestBackupDetectsMissingSchemaTable(t *testing.T) {
	t.Parallel()

	s := newFakeBackupStore()
	s.omitSchemaTable = "secret_keys"

	schema, err := s.SchemaTables(t.Context())
	if err != nil {
		t.Fatalf("list schema tables: %v", err)
	}

	if slices.Contains(schema, "secret_keys") {
		t.Fatal("schema tables cover secret_keys, want it missing")
	}

	if !slices.Contains(backup.Tables, "secret_keys") {
		t.Fatal("backup table list misses secret_keys, want it covered")
	}
}

// TestBackupDetectsMissingEmptyCheck breaks the importer so it loads into a
// store that holds rows. The import then overwrites the destination: the
// pre-existing namespace is gone. The non-empty case of RunBackup fails on
// this fake, because the import succeeds instead of refusing.
func TestBackupDetectsMissingEmptyCheck(t *testing.T) {
	t.Parallel()

	src := newFakeBackupStore()
	ns := fakeNamespace(t, src, "acme", "eu-west")
	fakeStack(t, src, ns, "prod", map[string]string{})

	sum, data := fakeExport(t, src)

	dst := newFakeBackupStore()
	dst.skipEmptyCheck = true

	other := fakeNamespace(t, dst, "other", "us-east")

	if _, err := dst.Import(t.Context(), bytes.NewReader(data), sum); err != nil {
		t.Fatalf("import without the empty check: %v", err)
	}

	if _, err := dst.GetNamespace(t.Context(), other.Name); !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("get namespace %q after an overwriting import: err = %v, want ErrNotFound", other.Name, err)
	}

	if _, err := dst.GetNamespace(t.Context(), ns.Name); err != nil {
		t.Fatalf("get namespace %q after import: %v", ns.Name, err)
	}
}

// TestBackupRejectsTruncatedData imports half an export and expects an
// error, with the destination still empty: no partial rows.
func TestBackupRejectsTruncatedData(t *testing.T) {
	t.Parallel()

	src := newFakeBackupStore()
	ns := fakeNamespace(t, src, "acme", "eu-west")
	fakeStack(t, src, ns, "prod", map[string]string{})

	sum, data := fakeExport(t, src)

	dst := newFakeBackupStore()

	if _, err := dst.Import(t.Context(), bytes.NewReader(data[:len(data)/2]), sum); !errors.Is(
		err, backup.ErrCorrupt,
	) {
		t.Fatalf("import of truncated data: err = %v, want ErrCorrupt", err)
	}

	if namespaces, err := dst.ListNamespaces(t.Context()); err != nil || len(namespaces) != 0 {
		t.Fatalf("namespaces after a failed import = %+v, %v; want none", namespaces, err)
	}
}

// fakeBackupRecord returns a running backup record for the broken-registry
// tests, mirroring the suite helper.
func fakeBackupRecord(id backup.ID, startedAt time.Time) backup.Record {
	return backup.Record{
		StartedAt: startedAt,
		EndedAt:   time.Time{},
		ID:        id,
		Target:    "dir:/var/backups",
		Error:     "",
		State:     backup.StateRunning,
	}
}

// TestBackupDetectsIgnoredDialect breaks the importer so it accepts a
// manifest of another dialect. The import succeeds instead of reporting
// ErrDialectMismatch, so the "import refuses another dialect" case of
// RunBackup fails on this fake.
func TestBackupDetectsIgnoredDialect(t *testing.T) {
	t.Parallel()

	src := newFakeBackupStore()
	ns := fakeNamespace(t, src, "acme", "eu-west")
	fakeStack(t, src, ns, "prod", map[string]string{})

	sum, data := fakeExport(t, src)
	sum.Dialect = backup.DialectPostgres

	dst := newFakeBackupStore()
	dst.ignoreManifest = true

	if _, err := dst.Import(t.Context(), bytes.NewReader(data), sum); err != nil {
		t.Fatalf("import of another dialect without the manifest check: %v", err)
	}

	if _, err := dst.GetNamespace(t.Context(), ns.Name); err != nil {
		t.Fatalf("get namespace %q after an accepting import: %v", ns.Name, err)
	}
}

// TestBackupDetectsIgnoredVersion breaks the importer so it accepts a
// manifest of another schema version. The import succeeds instead of
// reporting ErrSchemaMismatch, so the "import refuses another schema
// version" case of RunBackup fails on this fake.
func TestBackupDetectsIgnoredVersion(t *testing.T) {
	t.Parallel()

	src := newFakeBackupStore()
	ns := fakeNamespace(t, src, "acme", "eu-west")
	fakeStack(t, src, ns, "prod", map[string]string{})

	sum, data := fakeExport(t, src)
	sum.SchemaVersion++

	dst := newFakeBackupStore()
	dst.ignoreManifest = true

	if _, err := dst.Import(t.Context(), bytes.NewReader(data), sum); err != nil {
		t.Fatalf("import of another schema version without the manifest check: %v", err)
	}

	if _, err := dst.GetNamespace(t.Context(), ns.Name); err != nil {
		t.Fatalf("get namespace %q after an accepting import: %v", ns.Name, err)
	}
}

// TestBackupDetectsDuplicateBackup breaks the registry so creating a row
// with a taken ID overwrites it. The second create succeeds instead of
// reporting ErrAlreadyExists, so the "backups table creates, finishes, gets
// and lists" case of RunBackup fails on this fake.
func TestBackupDetectsDuplicateBackup(t *testing.T) {
	t.Parallel()

	s := newFakeBackupStore()
	s.allowDuplicate = true

	first := fakeBackupRecord(backup.ID(ident.NewID("bkp-")), fakeTime())
	if err := s.CreateBackup(t.Context(), first); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	second := first
	second.Target = "dir:/other"

	if err := s.CreateBackup(t.Context(), second); err != nil {
		t.Fatalf("create duplicate backup without the taken-ID check: %v", err)
	}

	got, err := s.GetBackup(t.Context(), first.ID)
	if err != nil {
		t.Fatalf("get backup: %v", err)
	}

	if got.Target != second.Target {
		t.Fatalf("backup target = %q, want the duplicate %q", got.Target, second.Target)
	}
}

// TestBackupDetectsRefinishAllowed breaks the registry so finishing a
// finished row succeeds. The second finish succeeds instead of reporting
// ErrNotRunning, so the "backups table creates, finishes, gets and lists"
// case of RunBackup fails on this fake.
func TestBackupDetectsRefinishAllowed(t *testing.T) {
	t.Parallel()

	s := newFakeBackupStore()
	s.allowRefinish = true

	r := fakeBackupRecord(backup.ID(ident.NewID("bkp-")), fakeTime())
	if err := s.CreateBackup(t.Context(), r); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	if _, err := s.FinishBackup(t.Context(), r.ID, backup.StateSucceeded, "", fakeTime()); err != nil {
		t.Fatalf("finish backup: %v", err)
	}

	redone, err := s.FinishBackup(t.Context(), r.ID, backup.StateFailed, "again", fakeTime())
	if err != nil {
		t.Fatalf("finish a finished backup without the running check: %v", err)
	}

	if redone.State != backup.StateFailed {
		t.Fatalf("refinished backup state = %q, want failed", redone.State)
	}
}

// TestBackupDetectsOldestFirst breaks the registry so ListBackups returns
// oldest first. The order differs from the newest-first order the "backups
// table creates, finishes, gets and lists" case of RunBackup checks, so that
// case fails on this fake.
func TestBackupDetectsOldestFirst(t *testing.T) {
	t.Parallel()

	s := newFakeBackupStore()
	s.oldestFirst = true

	ids := make([]backup.ID, 0, 3)

	for i := range 3 {
		r := fakeBackupRecord(
			backup.ID(ident.NewID("bkp-")),
			fakeTime().Add(time.Duration(i)*time.Hour),
		)

		if err := s.CreateBackup(t.Context(), r); err != nil {
			t.Fatalf("create backup: %v", err)
		}

		ids = append(ids, r.ID)
	}

	list, err := s.ListBackups(t.Context())
	if err != nil {
		t.Fatalf("list backups: %v", err)
	}

	if len(list) != 3 || list[0].ID != ids[0] || list[2].ID != ids[2] {
		t.Fatalf("backups oldest first = %v, want the broken order", list)
	}
}

// TestBackupDetectsNoopFailRunning breaks the registry so FailRunningBackups
// leaves running rows alone. The row stays running instead of failing, so
// the "restart marks every running backup failed" case of RunBackup fails on
// this fake.
func TestBackupDetectsNoopFailRunning(t *testing.T) {
	t.Parallel()

	s := newFakeBackupStore()
	s.noopFailRunning = true

	r := fakeBackupRecord(backup.ID(ident.NewID("bkp-")), fakeTime())
	if err := s.CreateBackup(t.Context(), r); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	if err := s.FailRunningBackups(t.Context(), fakeTime(), "server stopped during backup"); err != nil {
		t.Fatalf("fail running backups: %v", err)
	}

	got, err := s.GetBackup(t.Context(), r.ID)
	if err != nil {
		t.Fatalf("get backup: %v", err)
	}

	if got.State != backup.StateRunning {
		t.Fatalf("backup state after a no-op fail = %q, want running", got.State)
	}
}

// TestBackupDetectsDropOnClose breaks the store so Close drops every backup
// row. The row is gone after reopen instead of surviving, so the "a backup
// row survives close and reopen" case of RunBackup fails on this fake.
func TestBackupDetectsDropOnClose(t *testing.T) {
	t.Parallel()

	s := newFakeBackupStore()
	s.dropOnClose = true

	r := fakeBackupRecord(backup.ID(ident.NewID("bkp-")), fakeTime())
	if err := s.CreateBackup(t.Context(), r); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reopened := s.Reopen()

	if _, err := reopened.GetBackup(t.Context(), r.ID); !errors.Is(err, backup.ErrNotFound) {
		t.Fatalf("get backup after a dropping close: err = %v, want ErrNotFound", err)
	}
}

// TestBackupDetectsPeekTamper breaks the peek so it returns the key with a
// tampered ciphertext. The peeked bytes differ from the stored row, so the
// "peek returns one key of the wrapper without loading" case of RunBackup
// fails on this fake.
func TestBackupDetectsPeekTamper(t *testing.T) {
	t.Parallel()

	src := newFakeBackupStore()
	ns := fakeNamespace(t, src, "acme", "eu-west")
	st := fakeStack(t, src, ns, "prod", map[string]string{})
	fakeKey(t, src, ns, st, "kek-1")

	_, data := fakeExport(t, src)

	dst := newFakeBackupStore()
	dst.peekTampered = true

	got, err := dst.PeekKey(t.Context(), bytes.NewReader(data), "kek-1")
	if err != nil {
		t.Fatalf("peek key with a tampering peek: %v", err)
	}

	want, err := src.GetKey(t.Context(), fakeScope(t, ns, st))
	if err != nil {
		t.Fatalf("get key: %v", err)
	}

	if bytes.Equal(got.Ciphertext, want.Ciphertext) {
		t.Fatal("peeked ciphertext equals the stored row, want the tampered bytes")
	}
}

// TestBackupDetectsPeekAnyWrapper breaks the peek so it returns the first
// key whatever wrapperID holds. Peeking an unknown wrapper succeeds instead
// of reporting ErrCorrupt, so the "peek refuses an unknown wrapper" case of
// RunBackup fails on this fake.
func TestBackupDetectsPeekAnyWrapper(t *testing.T) {
	t.Parallel()

	src := newFakeBackupStore()
	ns := fakeNamespace(t, src, "acme", "eu-west")
	st := fakeStack(t, src, ns, "prod", map[string]string{})
	fakeKey(t, src, ns, st, "kek-1")

	_, data := fakeExport(t, src)

	dst := newFakeBackupStore()
	dst.peekAnyWrapper = true

	broken, err := dst.PeekKey(t.Context(), bytes.NewReader(data), "kek-unknown")
	if err != nil {
		t.Fatalf("peek key of an unknown wrapper with the knob: err = %v, want a key", err)
	}

	if broken.WrapperID == "kek-unknown" {
		t.Fatalf("peeked wrapper = %q, want the wrong row", broken.WrapperID)
	}
}

// TestBackupDetectsPeekNoCorrupt breaks the peek so it returns no error on a
// corrupt stream. Peeking truncated bytes succeeds instead of reporting
// ErrCorrupt, so the "peek refuses a corrupt stream" case of RunBackup fails
// on this fake.
func TestBackupDetectsPeekNoCorrupt(t *testing.T) {
	t.Parallel()

	src := newFakeBackupStore()
	ns := fakeNamespace(t, src, "acme", "eu-west")
	fakeStack(t, src, ns, "prod", map[string]string{})

	_, data := fakeExport(t, src)

	dst := newFakeBackupStore()
	dst.peekNoCorrupt = true

	if _, err := dst.PeekKey(t.Context(), bytes.NewReader(data[:len(data)/2]), "kek-1"); err != nil {
		t.Fatalf("peek key of truncated data with the knob: err = %v, want no error", err)
	}
}

// TestBackupDetectsSkippedReferences breaks the stager so it skips the
// reference checks. Staging an export whose stacks miss their namespace
// succeeds instead of reporting ErrCorrupt, so the "staging refuses stacks
// without their namespace" case of RunBackup fails on this fake.
func TestBackupDetectsSkippedReferences(t *testing.T) {
	t.Parallel()

	src := newFakeBackupStore()
	ns := fakeNamespace(t, src, "acme", "eu-west")
	fakeStack(t, src, ns, "prod", map[string]string{})

	sum, data := fakeExport(t, src)

	dst := newFakeBackupStore()
	dst.skipReferences = true

	tampered, want := dst.TamperNamespaces(t, data, sum)

	staged, err := dst.StageRestore(t.Context(), bytes.NewReader(tampered), want)
	if err != nil {
		t.Fatalf("stage backup without the reference checks: %v", err)
	}

	// The orphan stacks stage: the check is what refuses them.
	if staged.RowCounts["stacks"] != 1 || staged.RowCounts["namespaces"] != 0 {
		t.Fatalf("staged row counts = %v, want the orphan stack without its namespace", staged.RowCounts)
	}
}

// TestFakeStagingRefusesForeignKeys stages a stream whose stack misses its
// namespace and expects ErrCorrupt, with the live tables still empty. No port
// writes an orphan, so this test encodes the stream by hand. The conformance
// suite covers the same check through the ExportTamperer hook.
func TestFakeStagingRefusesForeignKeys(t *testing.T) {
	t.Parallel()

	st := stack.Stack{
		CreatedAt: fakeTime(),
		UpdatedAt: fakeTime(),
		Tags:      map[string]string{},
		Ref: stack.Ref{
			Namespace: namespace.Name("acme"),
			Project:   "web",
			Stack:     stack.Name("prod"),
		},
		ID:      stack.ID(ident.NewID("stk-")),
		Version: 0,
	}

	var buf bytes.Buffer

	enc := gob.NewEncoder(&buf)
	encodeFakeFrame(t, enc, "namespaces", map[string]namespace.Namespace{})
	encodeFakeFrame(t, enc, "stacks", map[string]stack.Stack{st.Ref.String(): st})

	want := backup.Summary{
		Dialect:     backup.DialectSQLite,
		HomeRegions: []string{},
		WrapperIDs:  []string{},
		RowCounts: map[string]int64{
			"namespaces": 0, "stacks": 1, "stack_tags": 0, "updates": 0,
			"checkpoints": 0, "update_events": 0, "secret_keys": 0,
			"inventory": 0, "workspaces": 0, "state_versions": 0, "backups": 0,
		},
		Blobs:         nil,
		SchemaVersion: 10,
	}

	s := newFakeBackupStore()

	if _, err := s.StageRestore(t.Context(), &buf, want); !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("stage backup of a stack without its namespace: err = %v, want ErrCorrupt", err)
	}

	if namespaces, err := s.ListNamespaces(t.Context()); err != nil || len(namespaces) != 0 {
		t.Fatalf("namespaces after a refused stage = %+v, %v; want none", namespaces, err)
	}

	if err := s.DiscardStaging(t.Context()); err != nil {
		t.Fatalf("discard staging area after a refused stage: %v", err)
	}
}

// encodeFakeFrame gob-encodes one export frame of table with value.
func encodeFakeFrame(t *testing.T, enc *gob.Encoder, table string, value any) {
	t.Helper()

	var rows bytes.Buffer

	if err := gob.NewEncoder(&rows).Encode(value); err != nil {
		t.Fatalf("encode table %s: %v", table, err)
	}

	if err := enc.Encode(fakeTableFrame{Table: table, Rows: rows.Bytes()}); err != nil {
		t.Fatalf("write table %s: %v", table, err)
	}
}
