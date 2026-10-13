package storetest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// BackupStore is every port the backup suite exercises: the domain stores it
// fills and compares through, and the backup ports under test. RunBackup
// exercises them the same way Run covers the namespace and stack ports.
// Every store adapter runs the suite once it implements the ports (issues
// #143 and #144); until then a fake in backup_test.go runs it and proves
// each case can fail.
type BackupStore interface { //nolint:interfacebloat // A whole-store round trip fills every table, so the union holds every port.
	namespace.Store
	stack.Store
	stack.UpdateStore
	stack.CheckpointStore
	stack.EventStore
	stack.JournalStore
	stack.BuiltCheckpointStore
	secret.KeyStore
	inventory.Reader
	inventory.DeletedReader
	workspace.Store
	workspace.StateStore
	backup.KeyPeeker
	backup.Registry
	backup.RestoreRegistry
	backup.Inspector
	backup.Coverage
	maintenance.Store
	// DiscardStaging removes the staging area, if any. It leaves the live data
	// unchanged.
	DiscardStaging(ctx context.Context) error
	audit.Store
}

// BackupFactory returns a fresh, empty store for one subtest.
type BackupFactory func(t *testing.T) BackupStore

// Reopener is an optional BackupStore capability for the close-and-reopen
// case: Close releases the store and Reopen returns a new handle to the same
// backing storage, as reopening the database file or reconnecting does.
type Reopener interface {
	BackupStore
	// Close releases the store.
	Close() error
	// Reopen returns a new handle to the same backing storage.
	Reopen() BackupStore
}

// StagingWitness is an optional BackupStore capability for the
// stage-replaces case: it exposes the identity of the current staging area,
// so the suite can prove a new stage discards the previous one. IDs are
// opaque: a file path for SQLite, a schema name for PostgreSQL, a generation
// for the fake. An empty ID means no staging area exists.
type StagingWitness interface {
	BackupStore
	// CurrentStagingID returns the ID of the current staging area, or empty.
	CurrentStagingID() string
	// StagingAlive reports whether the staging area with id still exists.
	StagingAlive(id string) bool
}

// RunBackup executes the conformance suite of the backup ports against stores
// produced by newStore.
func RunBackup(t *testing.T, newStore BackupFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, newStore BackupFactory)
		name string
	}{
		{name: "the table list names every table of the schema", test: testBackupTablesCoverSchema},
		{name: "column lists cover every column of the schema", test: testBackupColumnsCoverSchema},
		{name: "backups table creates, finishes, gets and lists", test: testBackupRegistry},
		{name: "restart marks every running backup failed", test: testBackupRestartFailsRunning},
		{name: "a backup row survives close and reopen", test: testBackupRowSurvivesReopen},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore)
		})
	}
}

// backupSeed holds the rows a fill wrote, so the comparison knows what to read.
type backupSeed struct {
	acme       namespace.Namespace
	other      namespace.Namespace
	prod       stack.Stack
	dev        stack.Stack
	otherProd  stack.Stack
	finished   stack.Update
	waiting    stack.Update
	checkpoint stack.Checkpoint
	events     []stack.Event
	scopes     []secret.Scope
	net        workspace.Workspace
	plain      workspace.Workspace
	current    workspace.StateVersion
	pending    workspace.StateVersion
	backedUp   backup.Record
	audit      []audit.Entry
}

// seedBackupStore fills s through the real ports: namespaces, stacks with
// tags, a finished and a waiting update, one checkpoint, events, secret keys
// under two wrappers, workspaces with a finalized and a pending state
// version, one deleted stack, and one finished backup row. Inventory rows
// follow from the tool writes. The finished update carries a creator, the waiting update is a
// restore with a creator and a restore source, the checkpoint manages one
// resource, and the pending state version is a restore of the finalized one,
// so the round trip proves the columns that later migrations added survive
// an export and an import.
func seedBackupStore(t *testing.T, s BackupStore) backupSeed {
	t.Helper()

	ctx := t.Context()
	f := newFixture(t, s)
	prod := mustGetStack(t, s, f.ref)

	other := namespace.Namespace{
		CreatedAt:       now(),
		Name:            "other",
		HomeRegion:      "us-east",
		FailoverVersion: 1,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := s.CreateNamespace(ctx, other); err != nil {
		t.Fatalf("create namespace %q: %v", other.Name, err)
	}

	// The switches are on in both namespaces, and on in one unit of each
	// kind, so the round trip carries every switch column with a value that
	// is not the default (ADR 44, decision 6).
	if err := s.SetOverrides(ctx, "acme", namespace.Overrides{Forced: true, Unlocked: false}, f.fence); err != nil {
		t.Fatalf("set acme switches: %v", err)
	}

	err := s.SetOverrides(
		ctx,
		other.Name,
		namespace.Overrides{Forced: false, Unlocked: true},
		other.Fence(),
	)
	if err != nil {
		t.Fatalf("set other switches: %v", err)
	}

	acme := mustGetNamespace(t, s, "acme")
	other = mustGetNamespace(t, s, "other")
	dev := mustCreateStack(t, s, acme, "dev")
	otherProd := mustCreateStack(t, s, other, "prod")

	// A deleted stack keeps its row and moves its inventory entry to the
	// deleted units (ADR 47), so the round trip carries both.
	gone := mustCreateStack(t, s, acme, "gone")
	if err := s.ForceDeleteStack(ctx, gone.Ref, acme.Fence(), deleteEntry(acme.Name)); err != nil {
		t.Fatalf("delete stack %s: %v", gone.Ref, err)
	}

	running := f.run(t, f.ref, "u1", stack.Plan{
		Kind:         stack.KindUpdate,
		DryRun:       false,
		CreatedBy:    "alice@example.com",
		RestoredFrom: 0,
	})
	checkpoint := newCheckpoint(running, 0, resourcedDeployment)

	if err := s.PutCheckpoint(ctx, checkpoint, f.fence); err != nil {
		t.Fatalf("put checkpoint: %v", err)
	}

	batch := newEvents(running, 1, 0, 1)
	if err := s.AppendEvents(ctx, batch, f.fence); err != nil {
		t.Fatalf("append events: %v", err)
	}

	finished := f.end(t, running, stack.StatusSucceeded)
	waiting := f.create(t, f.ref, "u2", stack.Plan{
		Kind:         stack.KindRestore,
		DryRun:       false,
		CreatedBy:    "bob@example.com",
		RestoredFrom: finished.Version,
	})

	scopes := []secret.Scope{
		seedBackupKey(t, s, acme, prod, "kek-1"),
		seedBackupKey(t, s, acme, dev, "kek-2"),
	}

	net := setUnitSwitchesOnly(t, s, acme, createTestWorkspace(t, s, acme, "network"),
		namespace.Overrides{Forced: true, Unlocked: true})
	locked := lockTestWorkspace(t, s, acme, net, "alice")
	declared := newPendingVersion(t, locked, locked.Lock.ID, stateInput("l1", "v1", 1))
	createPendingVersion(t, s, acme, declared)

	current := finalizedVersion(declared, now().Add(2*time.Hour), "v1")
	if _, err := s.RecordUploadAttempt(ctx, current.ID, current.Content, acme.Fence()); err != nil {
		t.Fatalf("record upload attempt: %v", err)
	}

	if err := s.FinalizeStateVersion(ctx, current, acme.Fence()); err != nil {
		t.Fatalf("finalize state version: %v", err)
	}

	pending := newPendingVersion(t, locked, locked.Lock.ID, stateInput("l1", "v2", 2))
	pending.RestoredFrom = current.ID
	createPendingVersion(t, s, acme, pending)

	plain := setUnitSwitchesOnly(t, s, other, createTestWorkspace(t, s, other, "plain"),
		namespace.Overrides{Forced: false, Unlocked: true})
	backedUp := seedBackupRecord(t, s)
	entries := seedBackupAudit(t, s, acme)
	seedBackupPrincipals(t, s)

	return backupSeed{
		acme:       acme,
		other:      other,
		prod:       prod,
		dev:        dev,
		otherProd:  otherProd,
		finished:   finished,
		waiting:    waiting,
		checkpoint: checkpoint,
		events:     batch.Events,
		scopes:     scopes,
		net:        net,
		plain:      plain,
		current:    current,
		pending:    pending,
		backedUp:   backedUp,
		audit:      entries,
	}
}

// seedBackupPrincipals writes a person, a service, a deleted principal, a
// group, a current membership and an ended one when s keeps principals, so
// the round trip compares the principal tables row by row, nullable end
// times included (ADR 52). A store with no principal port skips it: the
// fake of this package's own tests is one.
func seedBackupPrincipals(t *testing.T, s BackupStore) {
	t.Helper()

	ps, ok := s.(principal.Store)
	if !ok {
		return
	}

	ctx := t.Context()

	principals := []principal.Principal{
		{
			CreatedAt: now(), UpdatedAt: now().Add(time.Minute), ID: "prn-alice",
			Subject: "alice@example.com", DisplayName: "Alice", Kind: principal.KindPerson,
			Source: principal.SourceLocal, GroupCount: 0,
		},
		{
			CreatedAt: now(), UpdatedAt: now(), ID: "prn-deploy",
			Subject: "ci-deploy", DisplayName: "", Kind: principal.KindService,
			Source: principal.SourceLocal, GroupCount: 0,
		},
		{
			CreatedAt: now(), UpdatedAt: now(), ID: "prn-gone",
			Subject: "gone", DisplayName: "", Kind: principal.KindWorkload,
			Source: principal.SourceSCIM, GroupCount: 0,
		},
	}
	for i := range principals {
		e := ServerChange(now(), "principal:"+principals[i].ID)
		if err := ps.CreatePrincipal(ctx, principals[i], e); err != nil {
			t.Fatalf("create principal %q: %v", principals[i].Subject, err)
		}
	}

	g := principal.Group{
		CreatedAt: now(), UpdatedAt: now(), ID: "grp-ops", Name: "Ops", Description: "Runs the platform",
		Source: principal.SourceLocal, MemberCount: 0,
	}
	if err := ps.CreateGroup(ctx, g, ServerChange(now(), "group:grp-ops")); err != nil {
		t.Fatalf("create group: %v", err)
	}

	for _, id := range []string{"prn-alice", "prn-deploy"} {
		m := principal.Membership{AddedAt: now().Add(time.Hour), GroupID: g.ID, PrincipalID: id}
		if err := ps.AddMember(ctx, m, ServerChange(m.AddedAt, "group:grp-ops#member:"+id)); err != nil {
			t.Fatalf("add member %q: %v", id, err)
		}
	}

	later := now().Add(2 * time.Hour)
	removal := ServerChange(later, "group:grp-ops#member:prn-alice")
	if err := ps.RemoveMember(ctx, g.ID, "prn-alice", removal); err != nil {
		t.Fatalf("remove member: %v", err)
	}

	if err := ps.DeletePrincipal(ctx, "prn-gone", ServerChange(later, "principal:prn-gone")); err != nil {
		t.Fatalf("delete principal: %v", err)
	}
}

// ServerChange returns a valid audit entry of the kind change in the scope
// @server, for the record object at the time at: the entry that a write to
// an installation store takes.
func ServerChange(at time.Time, object string) audit.Entry {
	return audit.Entry{
		At:         at,
		Namespace:  audit.ServerScope,
		Subject:    "admin",
		IDPSubject: "",
		TokenID:    "",
		Action:     "server:manage-principals",
		Object:     object,
		Version:    "",
		ValuePath:  "",
		ValueCount: nil,
		SourceIP:   "192.0.2.1",
		UserAgent:  "storetest",
		Channel:    audit.ChannelAPI,
		Kind:       audit.KindChange,
		Outcome:    audit.OutcomeSuccess,
		RequestID:  "",
		PrevHash:   "",
		Hash:       "",
		Sequence:   0,
	}
}

// seedBackupAudit appends a reveal entry and a decrypt entry with a value
// count to acme, so the round trip compares the audit table row by row,
// carries the nullable value count both ways, and proves the chain survives
// it. A store that implements audit.Feed also gets one sink cursor row.
func seedBackupAudit(t *testing.T, s BackupStore, acme namespace.Namespace) []audit.Entry {
	t.Helper()

	out := make([]audit.Entry, 0, 2)

	for i := 1; i <= 2; i++ {
		kind, count := audit.KindReveal, (*int64)(nil)
		if i == 2 {
			n := int64(3)
			kind, count = audit.KindToolOperation, &n
		}

		stored, err := s.Append(t.Context(), acme.Fence(), audit.Entry{
			At:         now(),
			Namespace:  acme.Name,
			Subject:    "admin",
			IDPSubject: "",
			TokenID:    "",
			Action:     "unit:decrypt",
			Object:     "pulumi:" + acme.Name.String() + "/web/prod",
			Version:    "",
			ValuePath:  "",
			ValueCount: count,
			SourceIP:   "192.0.2.1",
			UserAgent:  "test",
			Channel:    audit.ChannelAPI,
			Kind:       kind,
			Outcome:    audit.OutcomeSuccess,
			RequestID:  fmt.Sprintf("req-%d", i),
			PrevHash:   "",
			Hash:       "",
			Sequence:   0,
		})
		if err != nil {
			t.Fatalf("append audit entry %d: %v", i, err)
		}

		out = append(out, stored)
	}

	// A store that ships entries to log sinks also keeps a cursor row, so
	// the round trip carries the cursor table too (ADR 60 decision 13).
	if feed, ok := s.(audit.Feed); ok {
		if err := feed.AdvanceSinkCursor(t.Context(), acme.Fence(), "siem", out[len(out)-1].Sequence); err != nil {
			t.Fatalf("advance audit sink cursor: %v", err)
		}
	}

	return out
}

// mustCreateStack creates one stack called name in ns, failing on error.
func mustCreateStack(t *testing.T, s BackupStore, ns namespace.Namespace, name string) stack.Stack {
	t.Helper()

	st := newStack(ns.Name, "web", name)
	if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
		t.Fatalf("create stack %s: %v", st.Ref, err)
	}

	return st
}

// seedBackupKey stores one wrapped data key and returns its scope.
func seedBackupKey(
	t *testing.T, s BackupStore, acme namespace.Namespace, st stack.Stack, wrapper string,
) secret.Scope {
	t.Helper()

	scope, err := secret.NewScope(acme.Name, st.ID.String())
	if err != nil {
		t.Fatalf("new scope of %s: %v", st.Ref, err)
	}

	key := secret.WrappedKey{
		CreatedAt:  now(),
		Scope:      scope,
		WrapperID:  wrapper,
		Ciphertext: []byte("wrapped-" + st.Ref.Stack.String()),
	}

	stored, err := s.CreateKey(t.Context(), key, acme.Fence())
	if err != nil {
		t.Fatalf("create key of %s: %v", st.Ref, err)
	}

	_ = stored

	return scope
}

// seedBackupRecord stores one finished backup row, so the round trip compares
// the backups table row by row too, not only through its row count.
func seedBackupRecord(t *testing.T, s BackupStore) backup.Record {
	t.Helper()

	ctx := t.Context()
	seeded := newBackupRecord(backup.ID(ident.NewID("bkp-")), now())

	if err := s.CreateBackup(ctx, seeded); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	backedUp, err := s.FinishBackup(ctx, seeded.ID, backup.StateSucceeded, "", now().Add(time.Hour))
	if err != nil {
		t.Fatalf("finish backup: %v", err)
	}

	return backedUp
}

// assertBackupEmpty fails when s holds any namespace, secret key, backup
// row or restore row. Namespaces own every other row through foreign keys,
// so an empty namespace and backup list means an empty store; the wrapper-ID
// check names the secret_keys table directly, matching the fake's
// full-table empty check.
func assertBackupEmpty(t *testing.T, s BackupStore) {
	t.Helper()

	namespaces, err := s.ListNamespaces(t.Context())
	if err != nil {
		t.Fatalf("list namespaces: %v", err)
	}

	if len(namespaces) != 0 {
		t.Fatalf("namespaces = %+v, want none", namespaces)
	}

	wrappers, err := s.ListWrapperIDs(t.Context())
	if err != nil {
		t.Fatalf("list wrapper IDs: %v", err)
	}

	if len(wrappers) != 0 {
		t.Fatalf("wrapper IDs = %+v, want none", wrappers)
	}

	rows, err := s.ListBackups(t.Context())
	if err != nil {
		t.Fatalf("list backups: %v", err)
	}

	if len(rows) != 0 {
		t.Fatalf("backups = %+v, want none", rows)
	}

	restores, err := s.ListRestores(t.Context())
	if err != nil {
		t.Fatalf("list restores: %v", err)
	}

	if len(restores) != 0 {
		t.Fatalf("restores = %+v, want none", restores)
	}
}

// mustGetNamespace reads one namespace, failing on error.
func mustGetNamespace(t *testing.T, s BackupStore, name namespace.Name) namespace.Namespace {
	t.Helper()

	got, err := s.GetNamespace(t.Context(), name)
	if err != nil {
		t.Fatalf("get namespace %q: %v", name, err)
	}

	return got
}

// mustListNamespaces lists every namespace, failing on error.
func mustListNamespaces(t *testing.T, s BackupStore) []namespace.Namespace {
	t.Helper()

	got, err := s.ListNamespaces(t.Context())
	if err != nil {
		t.Fatalf("list namespaces: %v", err)
	}

	return got
}

// mustGetStack reads one stack, failing on error.
func mustGetStack(t *testing.T, s BackupStore, ref stack.Ref) stack.Stack {
	t.Helper()

	got, err := s.GetStack(t.Context(), ref)
	if err != nil {
		t.Fatalf("get stack %s: %v", ref, err)
	}

	return got
}

// mustGetKey reads one wrapped data key, failing on error.
func mustGetKey(t *testing.T, s BackupStore, scope secret.Scope) secret.WrappedKey {
	t.Helper()

	got, err := s.GetKey(t.Context(), scope)
	if err != nil {
		t.Fatalf("get key of %s: %v", scope, err)
	}

	return got
}

// mustGetBackup reads one backup row, failing on error.
func mustGetBackup(t *testing.T, s BackupStore, id backup.ID) backup.Record {
	t.Helper()

	got, err := s.GetBackup(t.Context(), id)
	if err != nil {
		t.Fatalf("get backup %s: %v", id, err)
	}

	return got
}

// mustListBackups lists every backup row, failing on error.
func mustListBackups(t *testing.T, s BackupStore) []backup.Record {
	t.Helper()

	got, err := s.ListBackups(t.Context())
	if err != nil {
		t.Fatalf("list backups: %v", err)
	}

	return got
}

// mustListRestores lists every restore row, failing on error.
func mustListRestores(t *testing.T, s BackupStore) []backup.RestoreRecord {
	t.Helper()

	got, err := s.ListRestores(t.Context())
	if err != nil {
		t.Fatalf("list restores: %v", err)
	}

	return got
}

// mustCreateBackup stores a running backup row, failing on error.
func mustCreateBackup(t *testing.T, s BackupStore, r backup.Record) backup.Record {
	t.Helper()

	if err := s.CreateBackup(t.Context(), r); err != nil {
		t.Fatalf("create backup %s: %v", r.ID, err)
	}

	return r
}

// backupFinish holds how to finish a running backup row: its ID, the end
// state with the error text, and the end time.
type backupFinish struct {
	ID      backup.ID
	State   backup.State
	Error   string
	EndedAt time.Time
}

// mustFinishBackup finishes a running backup row, failing on error.
func mustFinishBackup(t *testing.T, s BackupStore, finish backupFinish) backup.Record {
	t.Helper()

	got, err := s.FinishBackup(t.Context(), finish.ID, finish.State, finish.Error, finish.EndedAt)
	if err != nil {
		t.Fatalf("finish backup %s: %v", finish.ID, err)
	}

	return got
}

// mustFailRunningBackups marks every running row failed, failing on error.
func mustFailRunningBackups(t *testing.T, s BackupStore, endedAt time.Time, errText string) {
	t.Helper()

	if err := s.FailRunningBackups(t.Context(), endedAt, errText); err != nil {
		t.Fatalf("fail running backups: %v", err)
	}
}

// sameBlobRef compares two blob references.
func sameBlobRef(a, b backup.BlobRef) bool {
	return a.Key == b.Key && a.Size == b.Size && a.MD5 == b.MD5
}

// testBackupTablesCoverSchema checks that the data tables of the migrated
// schema are exactly the tables of backup.Tables. It reads the schema tables
// through the Inspector, never through Tables itself, so a migration that
// adds a table fails here until its author adds the table to Tables.
func testBackupTablesCoverSchema(t *testing.T, newStore BackupFactory) {
	t.Helper()

	s := newStore(t)

	schema, err := s.SchemaTables(t.Context())
	if err != nil {
		t.Fatalf("schema tables: %v", err)
	}

	want := backup.TablesCopy()
	slices.Sort(want)

	got := slices.Clone(schema)
	slices.Sort(got)

	if !slices.Equal(got, want) {
		t.Fatalf("schema tables = %q, want backup.Tables %q", got, want)
	}
}

// testBackupColumnsCoverSchema checks that the column list of each table of
// backup.Tables matches the live columns of the migrated schema (ADR 42,
// decision 5). A column that a migration adds fails here until its list names it.
func testBackupColumnsCoverSchema(t *testing.T, newStore BackupFactory) {
	t.Helper()

	s := newStore(t)

	if err := s.CheckCoverage(t.Context()); err != nil {
		t.Fatalf("column lists and schema differ: %v", err)
	}
}

// newBackupRecord returns a running backup record with the given start time.
func newBackupRecord(id backup.ID, startedAt time.Time) backup.Record {
	return backup.Record{
		StartedAt: startedAt,
		EndedAt:   time.Time{},
		ID:        id,
		Target:    "dir:/var/backups",
		Error:     "",
		State:     backup.StateRunning,
	}
}

// testBackupRegistry covers the backups table: create a running row, finish
// it succeeded or failed with the error text, get it, and list newest first.
func testBackupRegistry(t *testing.T, newStore BackupFactory) {
	t.Helper()

	s := newStore(t)
	ctx := t.Context()

	if empty := mustListBackups(t, s); len(empty) != 0 {
		t.Fatalf("backups = %+v, want none", empty)
	}

	first := mustCreateBackup(t, s, newBackupRecord(backup.ID(ident.NewID("bkp-")), now()))

	if err := s.CreateBackup(ctx, first); !errors.Is(err, backup.ErrAlreadyExists) {
		t.Fatalf("create duplicate backup: err = %v, want ErrAlreadyExists", err)
	}

	if got := mustGetBackup(t, s, first.ID); got != first {
		t.Fatalf("backup = %+v, want %+v", got, first)
	}

	if _, err := s.GetBackup(ctx, backup.ID(ident.NewID("bkp-"))); !errors.Is(err, backup.ErrNotFound) {
		t.Fatalf("get missing backup: err = %v, want ErrNotFound", err)
	}

	endedAt := now().Add(time.Hour)
	finished := mustFinishBackup(t, s, backupFinish{
		ID:      first.ID,
		State:   backup.StateSucceeded,
		Error:   "",
		EndedAt: endedAt,
	})

	if finished.State != backup.StateSucceeded || !finished.EndedAt.Equal(endedAt) || finished.Error != "" {
		t.Fatalf("finished backup = %+v, want succeeded at %v without error", finished, endedAt)
	}

	checkBackupFinishErrors(t, s, first.ID, endedAt)

	secondDone := failSecondBackup(t, s)
	third := mustCreateBackup(t, s, newBackupRecord(backup.ID(ident.NewID("bkp-")), now().Add(4*time.Hour)))

	checkBackupList(t, s, first.ID, secondDone, third.ID)
}

// checkBackupFinishErrors checks that finishing a finished row reports
// ErrNotRunning and finishing a missing row reports ErrNotFound.
func checkBackupFinishErrors(t *testing.T, s BackupStore, id backup.ID, endedAt time.Time) {
	t.Helper()

	ctx := t.Context()

	if _, err := s.FinishBackup(ctx, id, backup.StateFailed, "again", endedAt); !errors.Is(
		err, backup.ErrNotRunning,
	) {
		t.Fatalf("finish a finished backup: err = %v, want ErrNotRunning", err)
	}

	if _, err := s.FinishBackup(ctx, backup.ID(ident.NewID("bkp-")), backup.StateFailed, "no", endedAt); !errors.Is(
		err, backup.ErrNotFound,
	) {
		t.Fatalf("finish a missing backup: err = %v, want ErrNotFound", err)
	}
}

// checkBackupList checks that ListBackups returns every row newest first with
// its state, error text and end time.
func checkBackupList(t *testing.T, s BackupStore, firstID backup.ID, secondDone backup.Record, thirdID backup.ID) {
	t.Helper()

	list := mustListBackups(t, s)

	if len(list) != 3 || list[0].ID != thirdID || list[1].ID != secondDone.ID || list[2].ID != firstID {
		t.Fatalf("backups newest first = %v, want [%s %s %s]", backupIDs(list), thirdID, secondDone.ID, firstID)
	}

	if list[1] != secondDone {
		t.Fatalf("listed failed backup = %+v, want %+v", list[1], secondDone)
	}
}

// failSecondBackup creates a second backup row and finishes it failed with
// the error text.
func failSecondBackup(t *testing.T, s BackupStore) backup.Record {
	t.Helper()

	second := mustCreateBackup(t, s, newBackupRecord(backup.ID(ident.NewID("bkp-")), now().Add(2*time.Hour)))
	failedAt := now().Add(3 * time.Hour)

	secondDone := mustFinishBackup(t, s, backupFinish{
		ID:      second.ID,
		State:   backup.StateFailed,
		Error:   "copy blobs: connection reset",
		EndedAt: failedAt,
	})

	if secondDone.State != backup.StateFailed || secondDone.Error != "copy blobs: connection reset" ||
		!secondDone.EndedAt.Equal(failedAt) {
		t.Fatalf("failed backup = %+v, want the error text and end time", secondDone)
	}

	return secondDone
}

// backupIDs returns the IDs of rows for failure messages.
func backupIDs(rows []backup.Record) []backup.ID {
	ids := make([]backup.ID, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].ID)
	}

	return ids
}

// testBackupRestartFailsRunning marks a running row failed at the next start,
// with the error text, and leaves finished rows alone.
func testBackupRestartFailsRunning(t *testing.T, newStore BackupFactory) {
	t.Helper()

	s := newStore(t)

	mustFailRunningBackups(t, s, now(), "no backup is running")

	running := mustCreateBackup(t, s, newBackupRecord(backup.ID(ident.NewID("bkp-")), now()))
	done := mustCreateBackup(t, s, newBackupRecord(backup.ID(ident.NewID("bkp-")), now().Add(time.Hour)))
	endedAt := now().Add(2 * time.Hour)
	finished := mustFinishBackup(t, s, backupFinish{
		ID:      done.ID,
		State:   backup.StateSucceeded,
		Error:   "",
		EndedAt: endedAt,
	})

	restartedAt := now().Add(3 * time.Hour)
	mustFailRunningBackups(t, s, restartedAt, "server stopped during backup")

	checkRestartedBackup(t, s, running, restartedAt)

	if untouched := mustGetBackup(t, s, done.ID); untouched != finished {
		t.Fatalf("finished backup after restart = %+v, want %+v", untouched, finished)
	}
}

// checkRestartedBackup checks that the running row failed with the error text
// and the end time, keeping its start time and target.
func checkRestartedBackup(t *testing.T, s BackupStore, running backup.Record, restartedAt time.Time) {
	t.Helper()

	got := mustGetBackup(t, s, running.ID)

	if got.State != backup.StateFailed || got.Error != "server stopped during backup" ||
		!got.EndedAt.Equal(restartedAt) || !got.StartedAt.Equal(running.StartedAt) || got.Target != running.Target {
		t.Fatalf("restarted backup = %+v, want failed with the error text and end time", got)
	}
}

// testBackupRowSurvivesReopen closes the store and reopens the same backing
// storage: every backup row stays. Stores without the Reopener capability
// skip this case; the SQL adapters implement it.
func testBackupRowSurvivesReopen(t *testing.T, newStore BackupFactory) {
	t.Helper()

	s := newStore(t)

	reopenable, ok := s.(Reopener)
	if !ok {
		t.Skip("store does not support close and reopen")
	}

	running := mustCreateBackup(t, s, newBackupRecord(backup.ID(ident.NewID("bkp-")), now()))
	done := mustCreateBackup(t, s, newBackupRecord(backup.ID(ident.NewID("bkp-")), now().Add(time.Hour)))
	finished := mustFinishBackup(t, s, backupFinish{
		ID:      done.ID,
		State:   backup.StateSucceeded,
		Error:   "",
		EndedAt: now().Add(2 * time.Hour),
	})

	if err := reopenable.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reopened := reopenable.Reopen()
	checkReopenedBackups(t, reopened, running, finished)
	mustFailRunningBackups(t, reopened, now().Add(3*time.Hour), "server stopped during backup")

	if after := mustGetBackup(t, reopened, running.ID); after.State != backup.StateFailed {
		t.Fatalf("restarted backup after reopen = %+v, want failed", after)
	}
}

// checkReopenedBackups checks that both backup rows survived close and reopen.
func checkReopenedBackups(
	t *testing.T, reopened BackupStore, running, finished backup.Record,
) {
	t.Helper()

	if gotRunning := mustGetBackup(t, reopened, running.ID); gotRunning != running {
		t.Fatalf("running backup after reopen = %+v, want %+v", gotRunning, running)
	}

	if gotFinished := mustGetBackup(t, reopened, finished.ID); gotFinished != finished {
		t.Fatalf("finished backup after reopen = %+v, want %+v", gotFinished, finished)
	}
}
