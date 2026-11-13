// Fake backup store: an in-memory BackupStore for the RunBackup conformance
// suite. The SQL adapters run the suite once they implement the ports (issues
// #143 and #144); until then this fake runs it. Its knobs (tear, dropTable,
// extraSchemaTable, omitSchemaTable, skipEmptyCheck, skipReferences,
// allowDuplicate, allowRefinish, oldestFirst, noopFailEnded, ignoreFence,
// failLive, dropOnClose)
// break the store on purpose, so the tests below prove that each suite case
// can fail.
package storetest_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/backup/blobguard"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/durable/durabletest"
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
	// journal and journalSequences hold the journal tables. The fake keeps no
	// journal rows, so both stay empty; they exist so the tables round trip.
	journal          []int
	journalSequences []int
	namespaces       map[string]namespace.Namespace
	stacks           map[string]stack.Stack
	stackIDs         map[string]string
	updates          map[string]map[string]stack.Update
	checkpoints      map[string]map[string]stack.Checkpoint
	events           map[string]map[string][]stack.Event
	keys             map[string]secret.WrappedKey
	entries          map[string]inventory.Entry
	audit            map[string][]audit.Entry
	workspaces       map[string]workspace.Workspace
	workspaceIDs     map[string]string
	states           map[string]workspace.StateVersion
	current          map[string]string
	backups          map[string]backup.Record
	restores         map[string]backup.RestoreRecord
	// installation is the installation state. It starts normal, as the
	// seeded row of the SQL adapters does.
	installation maintenance.State
}

// newFakeData returns empty tables.
func newFakeData() *fakeData {
	return &fakeData{
		journal:          nil,
		journalSequences: nil,
		namespaces:       map[string]namespace.Namespace{},
		stacks:           map[string]stack.Stack{},
		stackIDs:         map[string]string{},
		updates:          map[string]map[string]stack.Update{},
		checkpoints:      map[string]map[string]stack.Checkpoint{},
		events:           map[string]map[string][]stack.Event{},
		keys:             map[string]secret.WrappedKey{},
		entries:          map[string]inventory.Entry{},
		audit:            map[string][]audit.Entry{},
		workspaces:       map[string]workspace.Workspace{},
		workspaceIDs:     map[string]string{},
		states:           map[string]workspace.StateVersion{},
		current:          map[string]string{},
		backups:          map[string]backup.Record{},
		restores:         map[string]backup.RestoreRecord{},
		installation:     maintenance.StateNormal,
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
	allowDuplicate   bool
	allowRefinish    bool
	oldestFirst      bool
	noopFailEnded    bool
	ignoreFence      bool
	failLive         bool
	dropOnClose      bool
	closed           bool
	// mem is the store of durable operations of the fake. Reopened handles
	// share it, as handles on one database do.
	mem *durabletest.Mem
}

var (
	_ storetest.BackupStore      = (*fakeBackupStore)(nil)
	_ storetest.RestoreStore     = (*fakeBackupStore)(nil)
	_ storetest.Reopener         = (*fakeBackupStore)(nil)
	_ storetest.StagingWitness   = (*fakeBackupStore)(nil)
	_ storetest.BreakGlassSeeder = (*fakeBackupStore)(nil)
	_ backup.KeyPeeker           = (*fakeBackupStore)(nil)
	_ backup.Applier             = (*fakeBackupStore)(nil)
	_ backup.Fencer              = (*fakeBackupStore)(nil)
)

// BreakGlassPorts implements storetest.BreakGlassSeeder. The fake keeps no
// break-glass reviews.
func (*fakeBackupStore) BreakGlassPorts() storetest.BreakGlassPorts {
	return storetest.BreakGlassPorts{Store: nil}
}

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
		allowDuplicate:   false,
		allowRefinish:    false,
		oldestFirst:      false,
		noopFailEnded:    false,
		ignoreFence:      false,
		failLive:         false,
		dropOnClose:      false,
		closed:           false,
		mem:              durabletest.NewMem(),
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

// QueryStacks implements stack.Store over the lists of ListStacks.
func (s *fakeBackupStore) QueryStacks(ctx context.Context, q stack.PageQuery) ([]stack.Stack, error) {
	return stack.ListPage(ctx, s.ListStacks, q)
}

// DeleteStack implements stack.Store with its children, key and entry.
// When the latest checkpoint manages resources, it returns
// ErrHasResources and removes nothing. It ignores the delete guard and
// counts the stored deployment instead: the backup suite only writes inline
// checkpoints, so the guard path for blob rows never applies here. A nil
// deployment of a blob row would count as resourced, unlike the real
// adapters, which answer the guard on that path.
func (s *fakeBackupStore) DeleteStack(
	_ context.Context,
	ref stack.Ref,
	fence namespace.Fence,
	_ stack.DeleteGuard, _ inventory.Deletion,
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
func (s *fakeBackupStore) ForceDeleteStack(
	_ context.Context,
	ref stack.Ref,
	fence namespace.Fence,
	_ inventory.Deletion,
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

	for id := range s.data.updates[st.ID.String()] {
		if u := s.data.updates[st.ID.String()][id]; u.Status == stack.StatusRunning && u.Mutates() {
			return stack.ErrUpdateConflict
		}
	}

	s.removeStack(ref, st)

	return nil
}

// RestoreStack implements stack.Store; this fake holds no deleted stack.
func (*fakeBackupStore) RestoreStack(
	_ context.Context, _ stack.ID, _ stack.Name, _ namespace.Fence, _ audit.Entry,
) (stack.Ref, error) {
	return stack.Ref{}, stack.ErrNotFound
}

// PurgeStack implements stack.Store; this fake holds no deleted stack.
func (*fakeBackupStore) PurgeStack(_ context.Context, _ stack.ID, _ namespace.Fence, _ time.Time, _ audit.Entry) error {
	return stack.ErrNotFound
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
	_ context.Context, oldRef stack.Ref, newName stack.Name, fence namespace.Fence, _ audit.Entry,
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
func (s *fakeBackupStore) CreateUpdate(
	_ context.Context, u stack.Update, _ stack.UpdateKey, fence namespace.Fence,
) error {
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
) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return false, errFakeClosed
	}

	if err := s.checkFence(fence); err != nil {
		return false, err
	}

	if err := s.checkRunningUpdate(cp.Ref, cp.Update, cp.Lease); err != nil {
		return false, err
	}

	stackID, err := s.stackIDOf(cp.Ref)
	if err != nil {
		return false, stack.ErrNoState
	}

	byStack, ok := s.data.checkpoints[stackID]
	if !ok {
		byStack = map[string]stack.Checkpoint{}
		s.data.checkpoints[stackID] = byStack
	}

	if retried, err := checkCheckpointSequence(byStack, cp); err != nil {
		return false, err
	} else if retried {
		return true, nil
	}

	if cp.BlobKey != "" {
		// Rows with a blob key keep the metadata only, as the SQL adapters
		// do: the content lives in blobs.
		cp.Deployment = nil
	}

	byStack[cp.Update.String()] = cp

	return false, nil
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

// ReferencedBlobKeys implements stack.CheckpointStore.
func (s *fakeBackupStore) ReferencedBlobKeys(
	_ context.Context, id stack.ID,
) ([]blob.Key, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, errFakeClosed
	}

	stackID := id.String()

	found := false

	for key := range s.data.stacks {
		found = found || s.data.stacks[key].ID == id
	}

	if !found {
		return nil, stack.ErrNotFound
	}

	var out []blob.Key

	for cpID := range s.data.checkpoints[stackID] {
		if cp := s.data.checkpoints[stackID][cpID]; cp.BlobKey != "" {
			out = append(out, cp.BlobKey)
		}
	}

	slices.Sort(out)

	return out, nil
}

// JournalBatches implements stack.JournalStore. The backup fake keeps no journal rows.
func (s *fakeBackupStore) JournalBatches(context.Context, stack.ID, stack.UpdateID) ([]stack.JournalBatchRef, error) {
	return nil, nil
}

// JournalKeys implements stack.JournalStore. The backup fake keeps no journal rows.
func (s *fakeBackupStore) JournalKeys(context.Context, stack.ID, stack.UpdateID) ([]blob.Key, error) {
	return nil, nil
}

// JournalSweepable implements stack.JournalStore. The backup fake keeps no journal rows,
// so nothing is sweepable.
func (s *fakeBackupStore) JournalSweepable(context.Context, stack.ID, stack.UpdateID) (bool, error) {
	return false, nil
}

// PutBuiltCheckpoint implements stack.BuiltCheckpointStore. The backup fake keeps no
// journal rows, so a build through it is an error, not a silent drop.
func (s *fakeBackupStore) PutBuiltCheckpoint(context.Context, stack.Checkpoint, namespace.Fence) ([]blob.Key, error) {
	return nil, errors.ErrUnsupported
}

// PutJournalRow implements stack.JournalStore. The backup fake keeps no journal
// rows, so a write through it is an error, not a silent drop.
func (s *fakeBackupStore) PutJournalRow(context.Context, stack.JournalRow, namespace.Fence) (bool, error) {
	return false, errors.ErrUnsupported
}

// RecordJournalRefusal implements stack.JournalStore. The backup fake keeps no
// journal rows, so a write through it is an error, not a silent drop.
func (s *fakeBackupStore) RecordJournalRefusal(
	context.Context, stack.JournalRefusal, namespace.Fence,
) error {
	return errors.ErrUnsupported
}

// JournalRefusals implements stack.JournalStore. The backup fake keeps no
// journal rows, so it holds no refusals.
func (s *fakeBackupStore) JournalRefusals(
	context.Context, stack.ID, stack.UpdateID,
) ([]stack.JournalRefusal, error) {
	return nil, nil
}

// JournalSequences implements stack.JournalStore. The backup fake keeps no
// journal rows, so it holds no sequences.
func (s *fakeBackupStore) JournalSequences(
	context.Context, stack.ID, stack.UpdateID,
) ([]int64, error) {
	return nil, nil
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

// Append implements audit.Store: it chains the entry after the last one of
// the namespace.
func (s *fakeBackupStore) Append(
	_ context.Context, fence namespace.Fence, e audit.Entry,
) (audit.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return audit.Entry{}, errFakeClosed
	}

	if err := e.Validate(); err != nil {
		return audit.Entry{}, err
	}

	if err := s.checkFence(fence); err != nil {
		return audit.Entry{}, err
	}

	chain := s.data.audit[fence.Namespace.String()]

	var prevHash string

	if len(chain) > 0 {
		prevHash = chain[len(chain)-1].Hash
	}

	e.Sequence = int64(len(chain)) + 1
	e.PrevHash = prevHash
	e.Hash = audit.ComputeHash(e, prevHash)
	s.data.audit[fence.Namespace.String()] = append(chain, e)

	return e, nil
}

// List implements audit.Store: one page newest first with the total count.
func (s *fakeBackupStore) List(
	_ context.Context, ns namespace.Name, page audit.Page,
) (audit.Listing, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return audit.Listing{}, errFakeClosed
	}

	p := page.Normalized()
	chain := s.data.audit[ns.String()]

	entries := make([]audit.Entry, 0, min(int64(len(chain)), int64(p.Size)))

	for i := len(chain) - 1; i >= 0 && len(entries) < p.Size; i-- {
		if int64(len(chain)-1-i) >= p.Offset() {
			entries = append(entries, chain[i])
		}
	}

	return audit.Listing{Entries: entries, Total: int64(len(chain))}, nil
}

// Check implements audit.Store: it walks the chain oldest first.
func (s *fakeBackupStore) Check(_ context.Context, ns namespace.Name) (audit.CheckResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return audit.CheckResult{}, errFakeClosed
	}

	chain := s.data.audit[ns.String()]

	var prevHash string

	var wantSeq int64 = 1

	for i := range chain {
		e := &chain[i]
		if e.Sequence != wantSeq || e.PrevHash != prevHash || audit.ComputeHash(*e, e.PrevHash) != e.Hash {
			return audit.CheckResult{
				Checked:     int64(len(chain)),
				BadSequence: e.Sequence,
				Unchained:   0,
				Valid:       false,
			}, nil
		}

		prevHash = e.Hash
		wantSeq++
	}

	return audit.CheckResult{Checked: int64(len(chain)), BadSequence: 0, Unchained: 0, Valid: true}, nil
}

// ListDeletedUnits implements inventory.DeletedReader. The fake removes a
// deleted stack with its entry, so it never holds a deleted entry.
func (s *fakeBackupStore) ListDeletedUnits(_ context.Context, _ namespace.Name) ([]inventory.DeletedEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, errFakeClosed
	}

	return []inventory.DeletedEntry{}, nil
}

// QueryDeletedUnits implements inventory.DeletedReader. The fake removes a
// deleted stack with its entry, so every page is empty.
func (s *fakeBackupStore) QueryDeletedUnits(
	_ context.Context,
	q inventory.DeletedQuery,
) (inventory.DeletedPage, error) {
	return inventory.PageDeleted(nil, q), nil
}

// GetDeletedUnit implements inventory.DeletedReader. The fake holds no
// deleted unit.
func (s *fakeBackupStore) GetDeletedUnit(
	_ context.Context, _ namespace.Name, _, _ string,
) (inventory.DeletedEntry, error) {
	return inventory.DeletedEntry{}, inventory.ErrNotFound
}

// GetUnit implements inventory.Reader.
func (s *fakeBackupStore) GetUnit(_ context.Context, unitID string) (inventory.Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return inventory.Entry{}, errFakeClosed
	}

	for key := range s.data.entries {
		if e := s.data.entries[key]; e.UnitID == unitID {
			return e, nil
		}
	}

	return inventory.Entry{}, inventory.ErrUnitNotFound
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

// QueryUnits implements inventory.Reader over the entries of ListUnits.
func (s *fakeBackupStore) QueryUnits(ctx context.Context, q inventory.UnitQuery) ([]inventory.Entry, error) {
	all, err := s.ListUnits(ctx, q.Namespace)
	if err != nil {
		return nil, err
	}

	return inventory.PageUnits(all, q), nil
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

// QueryWorkspaces implements workspace.Store over the list of ListWorkspaces.
func (s *fakeBackupStore) QueryWorkspaces(ctx context.Context, q workspace.ListQuery) (workspace.ListPage, error) {
	all, err := s.ListWorkspaces(ctx, q.Namespace)
	if err != nil {
		return workspace.ListPage{}, err
	}

	return workspace.PageWorkspaces(all, q), nil
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

// DeleteWorkspace implements workspace.Store: it removes the workspace with
// its state versions, its wrapped data key and its inventory entry.
func (s *fakeBackupStore) DeleteWorkspace(
	_ context.Context, ref workspace.Ref, fence namespace.Fence, _ inventory.Deletion,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errFakeClosed
	}

	if err := s.checkFence(fence); err != nil {
		return err
	}

	id, ok := s.data.workspaceIDs[ref.String()]
	if !ok {
		return workspace.ErrNotFound
	}

	delete(s.data.keys, ref.Namespace.String()+"/"+id)

	for key := range s.data.states {
		if s.data.states[key].Workspace.String() == id {
			delete(s.data.states, key)
		}
	}

	delete(s.data.current, id)
	delete(s.data.workspaces, id)
	delete(s.data.workspaceIDs, ref.String())
	delete(s.data.entries, inventoryKey(ref.Namespace, "opentofu", ref.Name.String()))

	return nil
}

// RestoreWorkspace implements workspace.Store; this fake holds no deleted
// workspace.
func (*fakeBackupStore) RestoreWorkspace(
	_ context.Context, _ workspace.ID, _ workspace.Name, _ namespace.Fence, _ audit.Entry,
) (workspace.Ref, error) {
	return workspace.Ref{}, workspace.ErrNotFound
}

// PurgeWorkspace implements workspace.Store; this fake holds no deleted
// workspace.
func (*fakeBackupStore) PurgeWorkspace(
	_ context.Context,
	_ workspace.ID,
	_ namespace.Fence,
	_ time.Time,
	_ audit.Entry,
) error {
	return workspace.ErrNotFound
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
//
//nolint:revive // The signature of the workspace.StateStore port.
func (s *fakeBackupStore) FenceAndUnlock(
	_ context.Context, w workspace.Workspace, sv workspace.StateVersion,
	_ workspace.StateVersionID, fence namespace.Fence, _ audit.Entry,
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

	if !stored.Lock.Locked() {
		return workspace.ErrNotLocked
	}

	if stored.Revision != w.Revision-1 {
		return workspace.ErrConcurrentModification
	}

	if staged, found := s.data.states[sv.ID.String()]; !found ||
		staged.Status != workspace.StatusPending || staged.Content != sv.Content {
		return workspace.ErrUploadDenied
	}

	s.data.states[sv.ID.String()] = sv
	s.data.current[w.ID.String()] = sv.ID.String()
	s.data.workspaces[w.ID.String()] = w
	s.publishWorkspace(w.Ref, w.ID.String(), w.UpdatedAt)

	return nil
}

func (s *fakeBackupStore) FinalizeStateVersion(
	_ context.Context, sv workspace.StateVersion, fence namespace.Fence,
) error {
	return backupFinalize(s, sv, fence, nil)
}

// FinalizeStateVersionExpecting implements workspace.StateStore.
func (s *fakeBackupStore) FinalizeStateVersionExpecting(
	_ context.Context, sv workspace.StateVersion, fence namespace.Fence, expect workspace.StateVersionID,
) error {
	return backupFinalize(s, sv, fence, &expect)
}

// backupFinalize finalizes sv, and checks the current version when expect is set.
func backupFinalize(
	s *fakeBackupStore, sv workspace.StateVersion, fence namespace.Fence, expect *workspace.StateVersionID,
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

	if expect != nil && s.data.current[w.ID.String()] != expect.String() {
		return workspace.ErrConcurrentModification
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

// SaveSensitiveMarks implements workspace.StateStore.
func (s *fakeBackupStore) SaveSensitiveMarks(
	_ context.Context, id workspace.StateVersionID, marks workspace.SensitiveMarks, fence namespace.Fence,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errFakeClosed
	}

	if err := s.checkFence(fence); err != nil {
		return err
	}

	sv, ok := s.data.states[id.String()]
	if !ok {
		return workspace.ErrStateVersionNotFound
	}

	sv.SensitiveMarks = marks
	s.data.states[id.String()] = sv

	return nil
}

// DiscardStateVersion implements workspace.StateStore; the backup fake keeps no
// pending rows to discard.
func (s *fakeBackupStore) DiscardStateVersion(
	context.Context, workspace.StateVersionID, namespace.Fence,
) error {
	return nil
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

// DurableOperationsPort implements storetest.BackupStore: the store of durable
// operations of the fake.
//
//nolint:ireturn // The port returns the interface, as the store adapters do.
func (s *fakeBackupStore) DurableOperationsPort() durable.Store {
	return s.mem.Store()
}

// CreateBackup implements backup.Registry.
func (s *fakeBackupStore) CreateBackup(_ context.Context, r backup.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errFakeClosed
	}

	return s.createBackupLocked(r)
}

// StartBackup implements backup.Registry. The row check runs inside the
// transaction of the operation start, so a refused row rolls the start back.
func (s *fakeBackupStore) StartBackup(ctx context.Context, r backup.Record, start durable.StartRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errFakeClosed
	}

	// The check of the adapters: the request must belong to this backup.
	if err := start.Validate(); err != nil {
		return fmt.Errorf("start backup operation: %w", err)
	}

	if start.Key != backup.OperationKey(r.ID) || start.Kind != backup.OperationKind {
		return fmt.Errorf("start backup %s: operation %q: %w", r.ID, start.Key, durable.ErrKeyConflict)
	}

	hooks := durabletest.Hooks{Before: nil, After: func() error { return s.createBackupLocked(r) }}

	if _, err := s.mem.Harness().Domain.StartWith(ctx, start, "", hooks); err != nil {
		return fmt.Errorf("start backup operation: %w", err)
	}

	return nil
}

// StartBackupClaimed implements backup.Registry as StartBackup does, and
// claims the new operation for claim in the same write.
func (s *fakeBackupStore) StartBackupClaimed(
	ctx context.Context, r backup.Record, start durable.StartRequest, claim durable.StartClaim,
) (durable.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return durable.Operation{}, errFakeClosed
	}

	if err := start.Validate(); err != nil {
		return durable.Operation{}, fmt.Errorf("start backup operation: %w", err)
	}

	if start.Key != backup.OperationKey(r.ID) || start.Kind != backup.OperationKind {
		return durable.Operation{}, fmt.Errorf("start backup %s: operation %q: %w", r.ID, start.Key,
			durable.ErrKeyConflict)
	}

	hooks := durabletest.Hooks{Before: nil, After: func() error { return s.createBackupLocked(r) }}

	op, err := s.mem.StartClaimedWith(ctx, start, claim, hooks)
	if err != nil {
		return durable.Operation{}, fmt.Errorf("start backup operation: %w", err)
	}

	return op, nil
}

// FinishBackup implements backup.Registry. The row change runs inside the
// step write of the durable harness, which checks the claim first and records
// the step only when the row change succeeds. The ignoreFence knob skips the
// claim check on purpose.
//
//nolint:revive // The signature of the backup.Registry port.
func (s *fakeBackupStore) FinishBackup(
	ctx context.Context, f durable.StepFence, id backup.ID, state backup.State, errText string, endedAt time.Time,
) (backup.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return backup.Record{}, errFakeClosed
	}

	// The check runs before the domain write: the write holds the lock of the
	// durable store while its hook runs, and the check reads that store.
	if !s.fenceOwnsRow(ctx, f, id) {
		return backup.Record{}, durable.ErrClaimLost
	}

	var out backup.Record

	finish := func() error {
		r, ok := s.data.backups[id.String()]
		if !ok {
			return backup.ErrNotFound
		}

		if state != backup.StateSucceeded && state != backup.StateFailed {
			return backup.ErrNotRunning
		}

		if r.State != backup.StateRunning && !s.allowRefinish {
			return backup.ErrNotRunning
		}

		r.State = state
		r.Error = errText
		r.EndedAt = endedAt
		s.data.backups[id.String()] = r
		out = r

		return nil
	}

	if s.ignoreFence {
		if err := finish(); err != nil {
			return backup.Record{}, err
		}

		return out, nil
	}

	hooks := durabletest.Hooks{Before: nil, After: finish}

	zero := namespace.Fence{Namespace: "", FailoverVersion: 0}

	if err := s.mem.Harness().Domain.WriteStep(ctx, f, zero, "", hooks); err != nil {
		return backup.Record{}, fmt.Errorf("finish backup step: %w", err)
	}

	return out, nil
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

// Running implements blobguard.Watch: a running backup first, else a checking
// or applying restore, the smallest ID first.
func (s *fakeBackupStore) Running(_ context.Context) (blobguard.Work, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return blobguard.Work{Kind: "", ID: ""}, false, errFakeClosed
	}

	var ids []string

	for id, r := range s.data.backups {
		if r.State == backup.StateRunning {
			ids = append(ids, id)
		}
	}

	if len(ids) > 0 {
		return blobguard.Work{Kind: blobguard.Backup, ID: slices.Min(ids)}, true, nil
	}

	for id := range s.data.restores {
		if r := s.data.restores[id]; r.State == backup.RestoreChecking || r.State == backup.RestoreApplying {
			ids = append(ids, id)
		}
	}

	if len(ids) > 0 {
		return blobguard.Work{Kind: blobguard.Restore, ID: slices.Min(ids)}, true, nil
	}

	return blobguard.Work{Kind: "", ID: ""}, false, nil
}

// FailEndedBackups implements backup.Registry. The noopFailEnded knob leaves
// the rows alone on purpose. The failLive knob also fails the rows of
// operations that run or wait.
func (s *fakeBackupStore) FailEndedBackups(ctx context.Context, endedAt time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return 0, errFakeClosed
	}

	if s.noopFailEnded {
		return 0, nil
	}

	ops, err := s.mem.Store().List(ctx, durable.Filter{Kind: backup.OperationKind, States: nil, Limit: 0})
	if err != nil {
		return 0, fmt.Errorf("list backup operations: %w", err)
	}

	byKey := make(map[string]durable.Operation, len(ops))
	for i := range ops {
		byKey[ops[i].Key] = ops[i]
	}

	changed := 0

	for id, r := range s.data.backups {
		op, ok := byKey[backup.OperationKey(r.ID)]
		if !ok || r.State != backup.StateRunning {
			continue
		}

		ended := op.State == durable.StateFailed || op.State == durable.StateCanceled
		if !ended && !s.failLive {
			continue
		}

		r.State = backup.StateFailed
		r.Error = op.LastError

		if r.Error == "" {
			r.Error = backup.EndedWithoutFinishText
		}

		r.EndedAt = endedAt
		s.data.backups[id] = r
		changed++
	}

	return changed, nil
}

// GetRestore implements backup.RestoreRegistry.
func (s *fakeBackupStore) GetRestore(_ context.Context, id backup.RestoreID) (backup.RestoreRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return backup.RestoreRecord{}, errFakeClosed
	}

	r, ok := s.data.restores[id.String()]
	if !ok {
		return backup.RestoreRecord{}, backup.ErrRestoreNotFound
	}

	return r, nil
}

// ListRestores implements backup.RestoreRegistry, newest first.
func (s *fakeBackupStore) ListRestores(_ context.Context) ([]backup.RestoreRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, errFakeClosed
	}

	out := make([]backup.RestoreRecord, 0, len(s.data.restores))
	for i := range s.data.restores {
		out = append(out, s.data.restores[i])
	}

	slices.SortFunc(out, func(a, b backup.RestoreRecord) int {
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

		if a.StartedAt.After(b.StartedAt) {
			return -1
		}

		return 1
	})

	return out, nil
}

// ApplyStaging implements backup.Applier: it swaps the staged copy into the
// live tables in one locked step, fences every namespace by one version, and
// drops the staging area. With no staged copy it reports ErrCorrupt and
// changes nothing.
func (s *fakeBackupStore) ApplyStaging(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errFakeClosed
	}

	if s.staged == nil {
		return backup.ErrCorrupt
	}

	s.data = s.staged
	s.staged = nil

	if s.stageCurrent != 0 {
		delete(s.stageLive, s.stageCurrent)
		s.stageCurrent = 0
	}

	for name, ns := range s.data.namespaces {
		ns.FailoverVersion++
		s.data.namespaces[name] = ns
	}

	s.rebuildIndexes()

	return nil
}

// ApplyStagingKeepingAccess implements backup.AccessKeepingApplier: the fake
// keeps no access tables, so it swaps like ApplyStaging.
//
//nolint:unparam // The port returns the result, and the fake keeps no access tables.
func (s *fakeBackupStore) ApplyStagingKeepingAccess(ctx context.Context) (backup.KeepAccessResult, error) {
	return backup.KeepAccessResult{}, s.ApplyStaging(ctx)
}

// BumpFailoverVersions implements backup.Fencer: it increments the failover
// version of every namespace by one, so writes with an older fence fail
// fenced.
func (s *fakeBackupStore) BumpFailoverVersions(_ context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return 0, errFakeClosed
	}

	var n int64

	for name, ns := range s.data.namespaces {
		ns.FailoverVersion++
		s.data.namespaces[name] = ns
		n++
	}

	return n, nil
}

// SchemaTables implements backup.Inspector. It reports the tables the fake
// implements, derived from the same table set the exporter covers, so both
// drift directions can fail: extraSchemaTable adds a table the backup list
// does not cover, and omitSchemaTable removes one it does cover.
// PeekPortableKey is not used by the fake: the portable suite runs on the real
// stores only.
func (s *fakeBackupStore) PeekPortableKey(
	_ context.Context, _ backup.PortableOpener, _ backup.PortableFile, _ string,
) (secret.WrappedKey, error) {
	return secret.WrappedKey{}, fmt.Errorf("fake peeks no portable key: %w", backup.ErrCorrupt)
}

func (s *fakeBackupStore) CheckCoverage(_ context.Context) error {
	return nil
}

func (s *fakeBackupStore) SchemaTables(_ context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, errFakeClosed
	}

	out := append(backup.TablesCopy(), backup.ControlTables...)
	out = append(out, backup.EmptiedTables...)

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
		allowDuplicate:   s.allowDuplicate,
		allowRefinish:    s.allowRefinish,
		oldestFirst:      s.oldestFirst,
		noopFailEnded:    s.noopFailEnded,
		ignoreFence:      s.ignoreFence,
		failLive:         s.failLive,
		dropOnClose:      s.dropOnClose,
		closed:           false,
		mem:              s.mem,
	}
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

// TestRestoreConformance runs the restore suite against the fake. The SQL
// adapters run the same suite once they implement the ports.
func TestRestoreConformance(t *testing.T) {
	t.Parallel()

	storetest.RunRestore(t, func(t *testing.T) storetest.RestoreStore {
		t.Helper()

		return newFakeBackupStore()
	})
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

// fakeOperationClaim starts a bare operation under the start key of the row
// r and claims it for owner with lease, as the worker of a backup does.
func fakeOperationClaim(
	t *testing.T, s *fakeBackupStore, r backup.Record, owner string, lease time.Duration,
) durable.Claim {
	t.Helper()

	req := durable.StartRequest{
		Key: backup.OperationKey(r.ID), Kind: backup.OperationKind, Version: backup.OperationVersion, Input: nil,
	}

	if _, _, err := s.DurableOperationsPort().Start(t.Context(), req); err != nil {
		t.Fatalf("start operation: %v", err)
	}

	op, err := s.DurableOperationsPort().Claim(t.Context(), durable.ClaimRequest{
		Owner: owner, Kinds: []durable.Kind{backup.OperationKind}, MaxVersions: nil, Lease: lease,
		TakeCanceled: false,
	})
	if err != nil {
		t.Fatalf("claim operation: %v", err)
	}

	return op.Claim
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

	claim := fakeOperationClaim(t, s, r, "test", time.Hour)

	_, finishErr := s.FinishBackup(
		t.Context(), claim.Step(backup.StepFinish), r.ID, backup.StateSucceeded, "", fakeTime(),
	)
	if finishErr != nil {
		t.Fatalf("finish backup: %v", finishErr)
	}

	redone, err := s.FinishBackup(t.Context(), claim.Step("probe"), r.ID, backup.StateFailed, "again", fakeTime())
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

		// One backup runs at a time: each ends before the next starts.
		claim := fakeOperationClaim(t, s, r, "test", time.Hour)

		_, finishErr := s.FinishBackup(
			t.Context(), claim.Step(backup.StepFinish), r.ID, backup.StateSucceeded, "", fakeTime(),
		)
		if finishErr != nil {
			t.Fatalf("finish backup: %v", finishErr)
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

// TestBackupDetectsNoopFailEnded breaks the registry so FailEndedBackups
// leaves the rows of failed operations alone. The row stays running instead of
// failing, so the "ended operations fail their running rows" case of
// RunBackup fails on this fake.
func TestBackupDetectsNoopFailEnded(t *testing.T) {
	t.Parallel()

	s := newFakeBackupStore()
	s.noopFailEnded = true

	r := fakeBackupRecord(backup.ID(ident.NewID("bkp-")), fakeTime())
	if err := s.CreateBackup(t.Context(), r); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	claim := fakeOperationClaim(t, s, r, "test", time.Hour)

	failure := durable.Failure{Message: "boom", Final: true, RetryAfter: 0}

	err := s.DurableOperationsPort().Fail(t.Context(), claim, failure)
	if err != nil {
		t.Fatalf("fail operation: %v", err)
	}

	if n, failErr := s.FailEndedBackups(t.Context(), fakeTime()); failErr != nil || n != 0 {
		t.Fatalf("fail ended backups = %d, %v, want 0 and no error", n, failErr)
	}

	got, err := s.GetBackup(t.Context(), r.ID)
	if err != nil {
		t.Fatalf("get backup: %v", err)
	}

	if got.State != backup.StateRunning {
		t.Fatalf("backup state after a no-op fail = %q, want running", got.State)
	}
}

// TestBackupDetectsFailLive breaks the registry so FailEndedBackups also
// fails the row of an operation that runs. The row of a live backup ends
// failed, so the "live operations keep their running rows" case of RunBackup
// fails on this fake.
func TestBackupDetectsFailLive(t *testing.T) {
	t.Parallel()

	s := newFakeBackupStore()
	s.failLive = true

	r := fakeBackupRecord(backup.ID(ident.NewID("bkp-")), fakeTime())
	if err := s.CreateBackup(t.Context(), r); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	fakeOperationClaim(t, s, r, "test", time.Hour)

	if n, err := s.FailEndedBackups(t.Context(), fakeTime()); err != nil || n != 1 {
		t.Fatalf("fail ended backups = %d, %v, want 1 and no error", n, err)
	}

	got, err := s.GetBackup(t.Context(), r.ID)
	if err != nil {
		t.Fatalf("get backup: %v", err)
	}

	if got.State != backup.StateFailed {
		t.Fatalf("backup state of a live operation = %q, want failed", got.State)
	}
}

// staleFinish takes an operation over after its first claim expired, and
// finishes the row with the stale claim. It returns the error of that finish.
func staleFinish(t *testing.T, s *fakeBackupStore) error {
	t.Helper()

	r := fakeBackupRecord(backup.ID(ident.NewID("bkp-")), fakeTime())
	if err := s.CreateBackup(t.Context(), r); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	stale := fakeOperationClaim(t, s, r, "first", 20*time.Millisecond)

	deadline := time.Now().Add(10 * time.Second)

	for {
		_, err := s.DurableOperationsPort().Claim(t.Context(), durable.ClaimRequest{
			Owner: "second", Kinds: []durable.Kind{backup.OperationKind}, MaxVersions: nil, Lease: time.Hour,
			TakeCanceled: false,
		})
		if err == nil {
			break
		}

		if !errors.Is(err, durable.ErrNoWork) || time.Now().After(deadline) {
			t.Fatalf("take the operation over: %v", err)
		}

		select {
		case <-t.Context().Done():
			t.Fatal("the test ended while it waited for the claim to expire")
		case <-time.After(10 * time.Millisecond):
		}
	}

	_, err := s.FinishBackup(t.Context(), stale.Step(backup.StepFinish), r.ID, backup.StateSucceeded, "", fakeTime())

	return err
}

// TestBackupFakeRefusesStaleFinish is the control of the next test: the fake
// without a knob refuses a finish with a stale claim.
func TestBackupFakeRefusesStaleFinish(t *testing.T) {
	t.Parallel()

	if err := staleFinish(t, newFakeBackupStore()); !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("stale finish: err = %v, want durable.ErrClaimLost", err)
	}
}

// TestBackupDetectsIgnoredFence breaks the registry so FinishBackup skips the
// claim check. A holder that lost its claim then writes the row, so the "a
// stale claim cannot finish a backup" case of RunBackup fails on this fake.
func TestBackupDetectsIgnoredFence(t *testing.T) {
	t.Parallel()

	s := newFakeBackupStore()
	s.ignoreFence = true

	if err := staleFinish(t, s); err != nil {
		t.Fatalf("stale finish without the claim check: err = %v, want nil", err)
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

// SetOverrides is not used by this fake.
func (*fakeBackupStore) SetOverrides(context.Context, namespace.Name, namespace.Overrides, namespace.Fence) error {
	return nil
}

// ReleaseUnfenced is not used by this fake: it saves like SaveWorkspace.
func (s *fakeBackupStore) ReleaseUnfenced(
	ctx context.Context, w workspace.Workspace, fence namespace.Fence, _ audit.Entry,
) error {
	return s.SaveWorkspace(ctx, w, fence)
}

// SaveReleased is not used by this fake: it saves like SaveWorkspace.
func (s *fakeBackupStore) SaveReleased(
	ctx context.Context, w workspace.Workspace, fence namespace.Fence, _ audit.Entry,
) error {
	return s.SaveWorkspace(ctx, w, fence)
}

// fenceOwnsRow reports whether the operation of f is the operation of backup
// id: the operation of the fence has to be the operation of the row.
func (s *fakeBackupStore) fenceOwnsRow(ctx context.Context, f durable.StepFence, id backup.ID) bool {
	op, err := s.mem.Store().Get(ctx, f.Operation)

	return err == nil && op.Key == backup.OperationKey(id)
}

// createBackupLocked stores r with the rules of CreateBackup. The caller
// holds the lock.
func (s *fakeBackupStore) createBackupLocked(r backup.Record) error {
	if _, ok := s.data.backups[r.ID.String()]; ok && !s.allowDuplicate {
		return backup.ErrAlreadyExists
	}

	if r.State == backup.StateRunning && !s.allowDuplicate {
		for id := range s.data.backups {
			if s.data.backups[id].State == backup.StateRunning {
				return backup.ErrAlreadyRunning
			}
		}
	}

	s.data.backups[r.ID.String()] = r

	return nil
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
		LastRelease:   nil,
		ManualLock:    nil,
		Collection:    "",
		LastChanged:   changed,
		Namespace:     ref.Namespace,
		Tool:          "pulumi",
		Name:          name,
		UnitID:        unitID,
		DisplayName:   name,
		Status:        "",
		ResourceCount: nil,
		Lock:          nil,
		Labels:        nil,
	}
}

// publishWorkspace writes the OpenTofu inventory entry of ref. The caller
// holds the store lock.
func (s *fakeBackupStore) publishWorkspace(ref workspace.Ref, unitID string, changed time.Time) {
	name := ref.Name.String()
	s.data.entries[inventoryKey(ref.Namespace, "opentofu", name)] = inventory.Entry{
		LastRelease:   nil,
		ManualLock:    nil,
		Collection:    "",
		LastChanged:   changed,
		Namespace:     ref.Namespace,
		Tool:          "opentofu",
		Name:          name,
		UnitID:        unitID,
		DisplayName:   name,
		Status:        "",
		ResourceCount: nil,
		Lock:          nil,
		Labels:        nil,
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
