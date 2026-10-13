package storetest

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
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
	workspace.Store
	workspace.StateStore
	backup.Exporter
	backup.Importer
	backup.KeyPeeker
	backup.Registry
	backup.RestoreRegistry
	backup.Inspector
	maintenance.Store
	backup.Stager
	audit.Store
}

// BackupFactory returns a fresh, empty store for one subtest.
type BackupFactory func(t *testing.T) BackupStore

// ExportTamperer is an optional BackupStore capability for the foreign-key
// refusal case: it empties the namespaces table of an export stream, leaving
// orphan stacks. No port writes an orphan, so each store tampers its own
// stream format in its test wrapper, and the fake tampers its frames.
type ExportTamperer interface {
	BackupStore
	// TamperNamespaces empties the namespaces of the export and returns the
	// tampered bytes with a manifest whose namespace count matches them, so
	// only the foreign-key check of the stage can fail.
	TamperNamespaces(t *testing.T, data []byte, sum backup.Summary) ([]byte, backup.Summary)
}

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
		{name: "round trip over every table keeps every row", test: testBackupRoundTrip},
		{name: "export names checkpoint blobs", test: testBackupCheckpointBlob},
		{name: "export names journal blobs with their MD5 and size", test: testBackupJournalBlob},
		{name: "table list covers every table of the schema", test: testBackupTablesCoverSchema},
		{name: "export does not tear a concurrent stack write", test: testBackupExportDoesNotTear},
		{name: "import refuses a destination that is not empty", test: testBackupImportRefusesNonEmpty},
		{name: "import refuses another dialect", test: testBackupImportRefusesDialect},
		{name: "import refuses another schema version", test: testBackupImportRefusesVersion},
		{name: "a failed import leaves an empty store", test: testBackupFailedImportLeavesEmpty},
		{name: "import loads the row counts that export returns", test: testBackupRowCountsMatch},
		{name: "peek returns one key of the wrapper without loading", test: testBackupPeekKey},
		{name: "peek refuses an unknown wrapper", test: testBackupPeekUnknownWrapper},
		{name: "peek refuses a corrupt stream", test: testBackupPeekCorrupt},
		{name: "backups table creates, finishes, gets and lists", test: testBackupRegistry},
		{name: "restart marks every running backup failed", test: testBackupRestartFailsRunning},
		{name: "a backup row survives close and reopen", test: testBackupRowSurvivesReopen},
		{name: "staging loads a backup without touching live data", test: testBackupStagingKeepsLive},
		{name: "staging keeps a checkpoint held in blobs", test: testBackupStagingKeepsCheckpointBlobs},
		{name: "a new stage discards the previous staging area", test: testBackupStagingReplacesPrevious},
		{name: "staging refuses a manifest that omits a table", test: testBackupStagingRefusesOmittedTable},
		{name: "staging refuses another dialect", test: testBackupStagingRefusesDialect},
		{name: "staging refuses another schema version", test: testBackupStagingRefusesVersion},
		{name: "staging refuses a corrupt stream", test: testBackupStagingRefusesCorrupt},
		{name: "staging refuses row counts that differ from the manifest", test: testBackupStagingRefusesCounts},
		{name: "staging refuses blob references that differ from the manifest", test: testBackupStagingRefusesBlobs},
		{name: "staging refuses stacks without their namespace", test: testBackupStagingRefusesForeignKeys},
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
// version, and one finished backup row. Inventory rows follow from the tool
// writes. The finished update carries a creator, the waiting update is a
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
	}

	if err := s.CreateNamespace(ctx, other); err != nil {
		t.Fatalf("create namespace %q: %v", other.Name, err)
	}

	acme := mustGetNamespace(t, s, "acme")
	dev := mustCreateStack(t, s, acme, "dev")
	otherProd := mustCreateStack(t, s, other, "prod")

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

	net := createTestWorkspace(t, s, acme, "network")
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

	plain := createTestWorkspace(t, s, other, "plain")
	backedUp := seedBackupRecord(t, s)
	entries := seedBackupAudit(t, s, acme)

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

// seedBackupAudit appends a reveal entry and a decrypt entry with a value
// count to acme, so the round trip compares the audit table row by row,
// carries the nullable value count both ways, and proves the chain survives
// it.
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

// exportBackup exports s into memory, failing on error.
func exportBackup(t *testing.T, s BackupStore) (backup.Summary, []byte) {
	t.Helper()

	var buf bytes.Buffer

	sum, err := s.Export(t.Context(), &buf)
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	return sum, buf.Bytes()
}

// importBackup loads data with want into dst, failing on error.
func importBackup(t *testing.T, dst BackupStore, data []byte, want backup.Summary) backup.Summary {
	t.Helper()

	got, err := dst.Import(t.Context(), bytes.NewReader(data), want)
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	return got
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

// mustListStacks lists the stacks of ns, failing on error.
func mustListStacks(t *testing.T, s BackupStore, ns namespace.Name) []stack.Stack {
	t.Helper()

	got, err := s.ListStacks(t.Context(), stack.Filter{Namespace: ns, Project: ""})
	if err != nil {
		t.Fatalf("list stacks in %q: %v", ns, err)
	}

	return got
}

// mustGetUpdate reads one update, failing on error.
func mustGetUpdate(t *testing.T, s BackupStore, ref stack.Ref, id stack.UpdateID) stack.Update {
	t.Helper()

	got, err := s.GetUpdate(t.Context(), ref, id)
	if err != nil {
		t.Fatalf("get update %s: %v", id, err)
	}

	return got
}

// mustLatestCheckpoint reads the latest checkpoint, failing on error.
func mustLatestCheckpoint(t *testing.T, s BackupStore, ref stack.Ref) stack.Checkpoint {
	t.Helper()

	got, err := s.LatestCheckpoint(t.Context(), ref)
	if err != nil {
		t.Fatalf("latest checkpoint of %s: %v", ref, err)
	}

	return got
}

// mustCheckpointByVersion reads one checkpoint by version, failing on error.
func mustCheckpointByVersion(
	t *testing.T, s BackupStore, ref stack.Ref, version int64,
) stack.Checkpoint {
	t.Helper()

	got, err := s.CheckpointByVersion(t.Context(), ref, version)
	if err != nil {
		t.Fatalf("checkpoint of version %d of %s: %v", version, ref, err)
	}

	return got
}

// mustListEvents lists the events of one update, failing on error.
func mustListEvents(t *testing.T, s BackupStore, ref stack.Ref, id stack.UpdateID) []stack.Event {
	t.Helper()

	got, err := s.ListEvents(t.Context(), ref, id, -1, 100)
	if err != nil {
		t.Fatalf("list events of %s: %v", id, err)
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

// mustListWrapperIDs lists the wrapper IDs, failing on error.
func mustListWrapperIDs(t *testing.T, s BackupStore) []string {
	t.Helper()

	got, err := s.ListWrapperIDs(t.Context())
	if err != nil {
		t.Fatalf("list wrapper IDs: %v", err)
	}

	return got
}

// mustListUnits lists the inventory entries of ns, failing on error.
func mustListUnits(t *testing.T, s BackupStore, ns namespace.Name) []inventory.Entry {
	t.Helper()

	got, err := s.ListUnits(t.Context(), ns)
	if err != nil {
		t.Fatalf("list inventory of %q: %v", ns, err)
	}

	return got
}

// mustGetWorkspace reads one workspace by reference, failing on error.
func mustGetWorkspace(t *testing.T, s BackupStore, ref workspace.Ref) workspace.Workspace {
	t.Helper()

	got, err := s.GetWorkspace(t.Context(), ref)
	if err != nil {
		t.Fatalf("get workspace %s: %v", ref, err)
	}

	return got
}

// mustGetWorkspaceByID reads one workspace by ID, failing on error.
func mustGetWorkspaceByID(t *testing.T, s BackupStore, id workspace.ID) workspace.Workspace {
	t.Helper()

	got, err := s.GetWorkspaceByID(t.Context(), id)
	if err != nil {
		t.Fatalf("get workspace %s: %v", id, err)
	}

	return got
}

// mustGetStateVersion reads one state version, failing on error.
func mustGetStateVersion(t *testing.T, s BackupStore, id workspace.StateVersionID) workspace.StateVersion {
	t.Helper()

	got, err := s.GetStateVersion(t.Context(), id)
	if err != nil {
		t.Fatalf("get state version %s: %v", id, err)
	}

	return got
}

// mustCurrentStateVersion reads the current state version, failing on error.
func mustCurrentStateVersion(t *testing.T, s BackupStore, id workspace.ID) workspace.StateVersion {
	t.Helper()

	got, err := s.CurrentStateVersion(t.Context(), id)
	if err != nil {
		t.Fatalf("current state of %s: %v", id, err)
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

// testBackupRoundTrip fills a store through the real ports, exports it,
// imports it into an empty store, and compares every table row by row. It
// enters maintenance mode before the export, so the round trip also proves
// the installation state travels with the backup.
func testBackupRoundTrip(t *testing.T, newStore BackupFactory) {
	t.Helper()

	src := newStore(t)
	seed := seedBackupStore(t, src)

	if err := src.SetState(t.Context(), maintenance.StateMaintenance); err != nil {
		t.Fatalf("enter maintenance mode: %v", err)
	}

	sum, data := exportBackup(t, src)
	assertExportGroundTruth(t, src, newStore, &seed, sum)

	dst := newStore(t)
	got := importBackup(t, dst, data, sum)

	assertSummary(t, got, sum)
	assertExportGroundTruth(t, dst, newStore, &seed, got)
	compareBackupStores(t, dst, src, &seed)
}

// testBackupCheckpointBlob covers a checkpoint kept in blobs: the export
// summary names its blob with the stored size and MD5, and the import keeps
// the row with its key.
func testBackupCheckpointBlob(t *testing.T, newStore BackupFactory) {
	t.Helper()

	src := newStore(t)
	f := newFixture(t, src)
	u := f.run(t, f.ref, "u1", mutating())

	st := mustGetStack(t, src, f.ref)

	key, err := blob.ParseKey("pulumi/" + st.ID.String() + "/" + u.ID.String() + "/0")
	if err != nil {
		t.Fatalf("parse blob key: %v", err)
	}

	want := newBlobCheckpoint(u, key, 0, `{"v":1}`)

	if putErr := src.PutCheckpoint(t.Context(), want, f.fence); putErr != nil {
		t.Fatalf("put checkpoint: %v", putErr)
	}

	f.end(t, u, stack.StatusSucceeded)

	sum, data := exportBackup(t, src)

	wantBlobs := []backup.BlobRef{{MD5: want.BlobMD5, Key: key.String(), Size: want.BlobSize, Sealed: false}}
	if !slices.EqualFunc(sum.Blobs, wantBlobs, sameBlobRef) {
		t.Fatalf("export blobs = %+v, want %+v", sum.Blobs, wantBlobs)
	}

	dst := newStore(t)
	importBackup(t, dst, data, sum)

	got, err := dst.CheckpointByUpdate(t.Context(), f.ref, u.ID)
	if err != nil {
		t.Fatalf("checkpoint of update %s after import: %v", u.ID, err)
	}

	if len(got.Deployment) != 0 {
		t.Fatalf("checkpoint deployment after import = %q, want empty: the content lives in blobs", got.Deployment)
	}

	if got.BlobKey != key || got.BlobSize != want.BlobSize || got.BlobMD5 != want.BlobMD5 ||
		got.Sequence != want.Sequence || got.Digest != want.Digest {
		t.Fatalf("checkpoint after import = %+v, want key %q with the stored size and digest", got, key)
	}
}

// testBackupStagingKeepsCheckpointBlobs stages an export that holds a
// checkpoint kept in blobs into an empty store: the staged summary names the
// blob, like the export does. A stager that reads blob references from state
// versions alone reports corrupt here, while the export names the checkpoint
// blob.
func testBackupStagingKeepsCheckpointBlobs(t *testing.T, newStore BackupFactory) {
	t.Helper()

	src := newStore(t)
	f := newFixture(t, src)
	u := f.run(t, f.ref, "u1", mutating())

	st := mustGetStack(t, src, f.ref)

	key, err := blob.ParseKey("pulumi/" + st.ID.String() + "/" + u.ID.String() + "/0")
	if err != nil {
		t.Fatalf("parse blob key: %v", err)
	}

	want := newBlobCheckpoint(u, key, 0, `{"v":1}`)

	if putErr := src.PutCheckpoint(t.Context(), want, f.fence); putErr != nil {
		t.Fatalf("put checkpoint: %v", putErr)
	}

	f.end(t, u, stack.StatusSucceeded)

	sum, data := exportBackup(t, src)

	wantBlobs := []backup.BlobRef{{MD5: want.BlobMD5, Key: key.String(), Size: want.BlobSize, Sealed: false}}
	if !slices.EqualFunc(sum.Blobs, wantBlobs, sameBlobRef) {
		t.Fatalf("export blobs = %+v, want %+v", sum.Blobs, wantBlobs)
	}

	dst := newStore(t)
	staged := stageBackup(t, dst, data, sum)
	assertSummary(t, staged, sum)
	discardStaging(t, dst)
}

// assertSummary compares two summaries field by field.
func assertSummary(t *testing.T, got, want backup.Summary) {
	t.Helper()

	if got.Dialect != want.Dialect || got.SchemaVersion != want.SchemaVersion ||
		!slices.Equal(got.HomeRegions, want.HomeRegions) || !slices.Equal(got.WrapperIDs, want.WrapperIDs) ||
		!maps.Equal(got.RowCounts, want.RowCounts) || !slices.EqualFunc(got.Blobs, want.Blobs, sameBlobRef) {
		t.Fatalf("summary = %+v, want %+v", got, want)
	}
}

// sameBlobRef compares two blob references.
func sameBlobRef(a, b backup.BlobRef) bool {
	return a.Key == b.Key && a.Size == b.Size && a.MD5 == b.MD5
}

// assertExportGroundTruth checks the export summary against values read
// through the real ports, not against another summary, so an exporter that
// returns empty or constant fields fails. Dialect and schema version are
// store properties: they must be valid and match a fresh store of the same
// factory, and the refuse cases prove a mismatch is detected.
func assertExportGroundTruth(
	t *testing.T, s BackupStore, newStore BackupFactory, seed *backupSeed, sum backup.Summary,
) {
	t.Helper()

	assertExportRegions(t, s, sum)
	assertExportWrappers(t, s, sum)
	assertExportBlobs(t, s, seed, sum)
	assertExportCounts(t, s, seed, sum)
	assertExportDialect(t, newStore, sum)
}

// assertExportRegions checks the home regions against the namespaces.
func assertExportRegions(t *testing.T, s BackupStore, sum backup.Summary) {
	t.Helper()

	namespaces := mustListNamespaces(t, s)

	regions := map[string]bool{}
	for i := range namespaces {
		regions[namespaces[i].HomeRegion.String()] = true
	}

	wantRegions := slices.Sorted(maps.Keys(regions))
	if !slices.Equal(wantRegions, []string{"eu-west", "us-east"}) {
		t.Fatalf("home regions of the seed = %v, want [eu-west us-east]", wantRegions)
	}

	if !slices.Equal(sum.HomeRegions, wantRegions) {
		t.Fatalf("export home regions = %v, want %v", sum.HomeRegions, wantRegions)
	}
}

// assertExportWrappers checks the wrapper IDs against the key store.
func assertExportWrappers(t *testing.T, s BackupStore, sum backup.Summary) {
	t.Helper()

	wrappers := mustListWrapperIDs(t, s)
	if !slices.Equal(wrappers, []string{"kek-1", "kek-2"}) {
		t.Fatalf("wrapper IDs of the seed = %v, want [kek-1 kek-2]", wrappers)
	}

	if !slices.Equal(sum.WrapperIDs, wrappers) {
		t.Fatalf("export wrapper IDs = %v, want %v", sum.WrapperIDs, wrappers)
	}
}

// assertExportBlobs checks the blob references against the finalized state.
func assertExportBlobs(t *testing.T, s BackupStore, seed *backupSeed, sum backup.Summary) {
	t.Helper()

	current := mustGetStateVersion(t, s, seed.current.ID)
	if current.Content.String() == "" {
		t.Fatalf("finalized state %s holds no blob key", current.ID)
	}

	wantBlobs := []backup.BlobRef{{MD5: current.MD5, Key: current.Content.String(), Size: current.Size, Sealed: false}}
	if !slices.EqualFunc(sum.Blobs, wantBlobs, sameBlobRef) {
		t.Fatalf("export blobs = %+v, want %+v", sum.Blobs, wantBlobs)
	}
}

// assertExportCounts checks the row counts against the seeded rows.
func assertExportCounts(t *testing.T, s BackupStore, seed *backupSeed, sum backup.Summary) {
	t.Helper()

	wantCounts := expectedRowCounts(t, s, seed)
	if !maps.Equal(sum.RowCounts, wantCounts) {
		t.Fatalf("export row counts = %v, want %v", sum.RowCounts, wantCounts)
	}
}

// assertExportDialect checks the dialect and schema version against a fresh
// store of the same factory.
func assertExportDialect(t *testing.T, newStore BackupFactory, sum backup.Summary) {
	t.Helper()

	if sum.Dialect != backup.DialectSQLite && sum.Dialect != backup.DialectPostgres {
		t.Fatalf("export dialect = %q, want sqlite or postgres", sum.Dialect)
	}

	if sum.SchemaVersion <= 0 {
		t.Fatalf("export schema version = %d, want above zero", sum.SchemaVersion)
	}

	empty := newStore(t)

	var buf bytes.Buffer

	emptySum, err := empty.Export(t.Context(), &buf)
	if err != nil {
		t.Fatalf("export an empty store: %v", err)
	}

	if sum.Dialect != emptySum.Dialect || sum.SchemaVersion != emptySum.SchemaVersion {
		t.Fatalf(
			"export dialect/version = %q/%d, want the store properties %q/%d",
			sum.Dialect,
			sum.SchemaVersion,
			emptySum.Dialect,
			emptySum.SchemaVersion,
		)
	}
}

// expectedRowCounts counts the seeded rows through the real ports where a
// list port exists, and from the seed where no port lists the table, so the
// export counts meet values the exporter did not produce. The hardcoded
// numbers document the seed shape: two namespaces, three stacks with one tag
// each, two updates, one checkpoint, two secret keys, two workspaces, two
// state versions, one backup row, two audit entries and the one seeded
// installation row.
func expectedRowCounts(t *testing.T, s BackupStore, seed *backupSeed) map[string]int64 {
	t.Helper()

	ctx := t.Context()
	stacks := append(mustListStacks(t, s, seed.acme.Name), mustListStacks(t, s, seed.other.Name)...)

	tags := 0

	for i := range stacks {
		tags += len(stacks[i].Tags)
	}

	acmeUnits := mustListUnits(t, s, seed.acme.Name)
	otherUnits := mustListUnits(t, s, seed.other.Name)

	acmeWorkspaces, err := s.ListWorkspaces(ctx, seed.acme.Name)
	if err != nil {
		t.Fatalf("list workspaces of %q: %v", seed.acme.Name, err)
	}

	otherWorkspaces, err := s.ListWorkspaces(ctx, seed.other.Name)
	if err != nil {
		t.Fatalf("list workspaces of %q: %v", seed.other.Name, err)
	}

	auditEntries, err := s.List(ctx, seed.acme.Name, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list audit entries of %q: %v", seed.acme.Name, err)
	}

	got := map[string]int64{
		"namespaces":    int64(len(mustListNamespaces(t, s))),
		"stacks":        int64(len(stacks)),
		"stack_tags":    int64(tags),
		"updates":       2,
		"checkpoints":   1,
		"update_events": int64(len(seed.events)),
		// The seed writes no journal batch: the journal has no read port yet,
		// and item 8b adds the blobs that a backup must copy with its rows.
		"update_journal":           0,
		"update_journal_sequences": 0,
		"secret_keys":              int64(len(seed.scopes)),
		"inventory":                int64(len(acmeUnits) + len(otherUnits)),
		"workspaces":               int64(len(acmeWorkspaces) + len(otherWorkspaces)),
		"state_versions":           2,
		"backups":                  int64(len(mustListBackups(t, s))),
		"restores":                 int64(len(mustListRestores(t, s))),
		"audit_entries":            auditEntries.Total,
		// The installation table holds its one seeded row in every store:
		// no port lists it, so the count is the schema constant.
		"installation": 1,
	}

	want := map[string]int64{
		"namespaces":               2,
		"stacks":                   3,
		"stack_tags":               3,
		"updates":                  2,
		"checkpoints":              1,
		"update_events":            int64(len(seed.events)),
		"update_journal":           0,
		"update_journal_sequences": 0,
		"secret_keys":              2,
		"inventory":                5,
		"workspaces":               2,
		"state_versions":           2,
		"backups":                  1,
		"restores":                 0,
		"audit_entries":            int64(len(seed.audit)),
		"installation":             1,
	}

	if !maps.Equal(got, want) {
		t.Fatalf("seeded row counts = %v, want %v", got, want)
	}

	return want
}

// compareBackupStores reads every seeded row from want and dst and compares
// them table by table.
func compareBackupStores(t *testing.T, dst, want BackupStore, seed *backupSeed) {
	t.Helper()

	compareBackupNamespaces(t, dst, want)
	compareBackupStacks(t, dst, want, seed)
	compareBackupUpdates(t, dst, want, seed)
	compareBackupCheckpoint(t, dst, seed)
	compareBackupEvents(t, dst, seed)
	compareBackupKeys(t, dst, want, seed)
	compareBackupInventory(t, dst, want, seed)
	compareBackupWorkspaces(t, dst, want, seed)
	compareBackupStates(t, dst, want, seed)
	compareBackupRecord(t, dst, seed)
	compareBackupRestores(t, dst, want)
	compareBackupInstallation(t, dst, want)
	compareBackupAudit(t, dst, want, seed)
}

// compareBackupAudit compares the audit entries after import and proves
// the chain is still valid: a backup and a restore keep the entries and a
// valid chain.
func compareBackupAudit(t *testing.T, dst, want BackupStore, seed *backupSeed) {
	t.Helper()

	got, err := dst.List(t.Context(), seed.acme.Name, audit.Page{Number: 1, Size: len(seed.audit)})
	if err != nil {
		t.Fatalf("list audit entries after import: %v", err)
	}

	all, err := want.List(t.Context(), seed.acme.Name, audit.Page{Number: 1, Size: len(seed.audit)})
	if err != nil {
		t.Fatalf("list audit entries of the source: %v", err)
	}

	if !slices.EqualFunc(got.Entries, all.Entries, sameAuditEntry) {
		t.Fatalf("audit entries after import = %+v, want %+v", got.Entries, all.Entries)
	}

	if got.Total != int64(len(seed.audit)) {
		t.Fatalf("audit total after import = %d, want %d", got.Total, len(seed.audit))
	}

	check, err := dst.Check(t.Context(), seed.acme.Name)
	if err != nil {
		t.Fatalf("check audit chain after import: %v", err)
	}

	if !check.Valid || check.Checked != int64(len(seed.audit)) {
		t.Fatalf("audit check after import = %+v, want valid with %d entries", check, len(seed.audit))
	}
}

// sameAuditEntry compares two audit entries field by field.
func sameAuditEntry(a, b audit.Entry) bool {
	return sameAuditWho(a, b) && sameAuditWhat(a, b) && sameAuditWhere(a, b) && sameAuditChain(a, b)
}

// sameAuditWho compares the identity, time and sequence of two entries.
func sameAuditWho(a, b audit.Entry) bool {
	return a.AtMs() == b.AtMs() &&
		a.Namespace == b.Namespace &&
		a.Sequence == b.Sequence &&
		a.Subject == b.Subject &&
		a.IDPSubject == b.IDPSubject &&
		a.TokenID == b.TokenID
}

// sameAuditWhat compares the action, object, version, value path and value
// count of two entries.
func sameAuditWhat(a, b audit.Entry) bool {
	return a.Action == b.Action &&
		a.Object == b.Object &&
		a.Version == b.Version &&
		a.ValuePath == b.ValuePath &&
		sameValueCount(a.ValueCount, b.ValueCount)
}

// sameValueCount compares two nullable value counts: both nil, or both set
// to the same number.
func sameValueCount(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}

	return *a == *b
}

// sameAuditWhere compares the source, channel, kind, outcome and request of
// two entries.
func sameAuditWhere(a, b audit.Entry) bool {
	return a.SourceIP == b.SourceIP &&
		a.UserAgent == b.UserAgent &&
		a.Channel == b.Channel &&
		a.Kind == b.Kind &&
		a.Outcome == b.Outcome &&
		a.RequestID == b.RequestID
}

// sameAuditChain compares the chain fields of two entries.
func sameAuditChain(a, b audit.Entry) bool {
	return a.PrevHash == b.PrevHash && a.Hash == b.Hash
}

// compareBackupNamespaces compares the namespaces after import.
func compareBackupNamespaces(t *testing.T, dst, want BackupStore) {
	t.Helper()

	got := mustListNamespaces(t, dst)
	all := mustListNamespaces(t, want)

	if !slices.Equal(got, all) {
		t.Fatalf("namespaces after import = %+v, want %+v", got, all)
	}
}

// compareBackupStacks compares the stacks with their tags after import.
func compareBackupStacks(t *testing.T, dst, want BackupStore, seed *backupSeed) {
	t.Helper()

	for _, ref := range []stack.Ref{seed.prod.Ref, seed.dev.Ref, seed.otherProd.Ref} {
		assertStack(t, mustGetStack(t, dst, ref), mustGetStack(t, want, ref))
	}

	for _, name := range []namespace.Name{seed.acme.Name, seed.other.Name} {
		got := mustListStacks(t, dst, name)
		all := mustListStacks(t, want, name)

		if len(got) != len(all) {
			t.Fatalf("stacks in %q after import = %d, want %d", name, len(got), len(all))
		}

		for i := range all {
			assertStack(t, got[i], all[i])
		}
	}
}

// compareBackupUpdates compares the finished and waiting updates after import.
func compareBackupUpdates(t *testing.T, dst, want BackupStore, seed *backupSeed) {
	t.Helper()

	for _, id := range []stack.UpdateID{seed.finished.ID, seed.waiting.ID} {
		got := mustGetUpdate(t, dst, seed.prod.Ref, id)
		all := mustGetUpdate(t, want, seed.prod.Ref, id)

		if !sameUpdate(got, all) {
			t.Fatalf("update %s after import = %+v, want %+v", id, got, all)
		}
	}
}

// compareBackupCheckpoint compares the checkpoint by latest and by version.
func compareBackupCheckpoint(t *testing.T, dst BackupStore, seed *backupSeed) {
	t.Helper()

	got := mustLatestCheckpoint(t, dst, seed.prod.Ref)
	assertSameCheckpoint(t, got, seed.checkpoint, "checkpoint after import")

	byVersion := mustCheckpointByVersion(t, dst, seed.prod.Ref, seed.finished.Version)
	assertSameCheckpoint(t, byVersion, seed.checkpoint, "checkpoint of version after import")
}

// compareBackupEvents compares the events of the finished update.
func compareBackupEvents(t *testing.T, dst BackupStore, seed *backupSeed) {
	t.Helper()

	got := mustListEvents(t, dst, seed.prod.Ref, seed.finished.ID)

	if !sameEvents(got, seed.events) {
		t.Fatalf("events after import = %s, want %s", eventsString(got), eventsString(seed.events))
	}
}

// compareBackupKeys compares the secret keys and their wrapper IDs.
func compareBackupKeys(t *testing.T, dst, want BackupStore, seed *backupSeed) {
	t.Helper()

	for _, scope := range seed.scopes {
		got := mustGetKey(t, dst, scope)
		all := mustGetKey(t, want, scope)

		if got.Scope != all.Scope || got.WrapperID != all.WrapperID ||
			!got.CreatedAt.Equal(all.CreatedAt) || !bytes.Equal(got.Ciphertext, all.Ciphertext) {
			t.Fatalf("key of %s after import = %+v, want %+v", scope, got, all)
		}
	}

	gotWrappers := mustListWrapperIDs(t, dst)
	allWrappers := mustListWrapperIDs(t, want)

	if !slices.Equal(gotWrappers, allWrappers) {
		t.Fatalf("wrapper IDs after import = %v, want %v", gotWrappers, allWrappers)
	}
}

// compareBackupInventory compares the inventory entries of each namespace.
func compareBackupInventory(t *testing.T, dst, want BackupStore, seed *backupSeed) {
	t.Helper()

	for _, name := range []namespace.Name{seed.acme.Name, seed.other.Name} {
		got := mustListUnits(t, dst, name)
		all := mustListUnits(t, want, name)

		if !slices.EqualFunc(got, all, sameEntry) {
			t.Fatalf("inventory of %q after import = %+v, want %+v", name, got, all)
		}
	}
}

// compareBackupWorkspaces compares the workspaces by reference and by ID.
func compareBackupWorkspaces(t *testing.T, dst, want BackupStore, seed *backupSeed) {
	t.Helper()

	for _, ref := range []workspace.Ref{seed.net.Ref, seed.plain.Ref} {
		all := mustGetWorkspace(t, want, ref)
		assertWorkspace(t, mustGetWorkspace(t, dst, ref), all)
		assertWorkspace(t, mustGetWorkspaceByID(t, dst, all.ID), all)
	}
}

// compareBackupStates compares the state versions and the current pointer.
func compareBackupStates(t *testing.T, dst, want BackupStore, seed *backupSeed) {
	t.Helper()

	for _, id := range []workspace.StateVersionID{seed.current.ID, seed.pending.ID} {
		assertStateVersion(t, mustGetStateVersion(t, dst, id), mustGetStateVersion(t, want, id))
	}

	assertStateVersion(
		t,
		mustCurrentStateVersion(t, dst, seed.net.ID),
		mustCurrentStateVersion(t, want, seed.net.ID),
	)
}

// compareBackupRecord compares the seeded backup row after import.
func compareBackupRecord(t *testing.T, dst BackupStore, seed *backupSeed) {
	t.Helper()

	if got := mustGetBackup(t, dst, seed.backedUp.ID); got != seed.backedUp {
		t.Fatalf("backup after import = %+v, want %+v", got, seed.backedUp)
	}
}

// compareBackupRestores compares the restore rows after import: the seed
// holds none, so the destination must hold none either. The dedicated
// RunRestore suite covers the restores table itself.
func compareBackupRestores(t *testing.T, dst, want BackupStore) {
	t.Helper()

	got := mustListRestores(t, dst)
	all := mustListRestores(t, want)

	if len(got) != len(all) {
		t.Fatalf("restores after import = %+v, want %+v", got, all)
	}

	for i := range all {
		if got[i] != all[i] {
			t.Fatalf("restore %d after import = %+v, want %+v", i, got[i], all[i])
		}
	}
}

// compareBackupInstallation compares the installation state after import: the
// round trip carries the maintenance mode the source entered, not just its
// row count.
func compareBackupInstallation(t *testing.T, dst, want BackupStore) {
	t.Helper()

	got, err := dst.GetState(t.Context())
	if err != nil {
		t.Fatalf("get installation state after import: %v", err)
	}

	all, err := want.GetState(t.Context())
	if err != nil {
		t.Fatalf("get installation state of the source: %v", err)
	}

	if got != all {
		t.Fatalf("installation state after import = %q, want %q", got, all)
	}
}

// sameEntry compares two inventory entries, including the resource count.
func sameEntry(a, b inventory.Entry) bool {
	switch {
	case a.Tool != b.Tool, a.Namespace != b.Namespace, a.Name != b.Name, a.UnitID != b.UnitID,
		a.DisplayName != b.DisplayName, !a.LastChanged.Equal(b.LastChanged), a.Status != b.Status:
		return false
	case a.ResourceCount == nil || b.ResourceCount == nil:
		return a.ResourceCount == nil && b.ResourceCount == nil
	default:
		return *a.ResourceCount == *b.ResourceCount
	}
}

// testBackupTablesCoverSchema fails when a table of the schema is missing
// from backup.Tables. It reads the schema tables through the Inspector,
// never through Tables itself, so the two cannot drift silently.
func testBackupTablesCoverSchema(t *testing.T, newStore BackupFactory) {
	t.Helper()

	s := newStore(t)

	schema, err := s.SchemaTables(t.Context())
	if err != nil {
		t.Fatalf("list schema tables: %v", err)
	}

	for _, table := range schema {
		if !slices.Contains(backup.Tables, table) {
			t.Fatalf("schema table %q is missing from the backup table list", table)
		}
	}

	for _, table := range backup.Tables {
		if !slices.Contains(schema, table) {
			t.Fatalf("backup table %q is missing from the schema", table)
		}
	}

	sum, _ := exportBackup(t, s)

	for _, table := range backup.Tables {
		if _, ok := sum.RowCounts[table]; !ok {
			t.Fatalf("export misses the row count of table %q", table)
		}
	}

	for table := range sum.RowCounts {
		if !slices.Contains(backup.Tables, table) {
			t.Fatalf("export counts unknown table %q", table)
		}
	}
}

// testBackupExportDoesNotTear exports while writers add stacks, each in one
// store transaction with its tags and inventory entry. Every writer writes
// its first stack before the export starts, so the capture is non-empty,
// and keeps writing the rest while the export runs, so the writes overlap
// the capture. Every stack the import holds must equal its source row: all
// in it or all out of it.
func testBackupExportDoesNotTear(t *testing.T, newStore BackupFactory) {
	t.Helper()

	src := newStore(t)
	ns := newNamespace(t, src, "acme")

	const writers = 8

	const perWriter = 8

	var wg sync.WaitGroup

	errs := make([]error, writers)
	firstDone := make(chan struct{}, writers)

	for w := range writers {
		wg.Go(func() {
			errs[w] = writeBackupStacksTask(t, backupWriteTask{
				src: src, ns: ns, worker: w, total: perWriter, signal: func() { firstDone <- struct{}{} },
			})
		})
	}

	for range writers {
		<-firstDone
	}

	sum, data := exportBackup(t, src)
	wg.Wait()

	for w, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", w, err)
		}
	}

	dst := newStore(t)
	importBackup(t, dst, data, sum)
	verifyBackupNoTear(t, src, dst, ns)
}

// backupWriteTask is one concurrent writer of the no-tear case: worker
// numbers the writer, total counts its stacks, and signal runs after its
// first stack so the caller can start the export while the rest is pending.
type backupWriteTask struct {
	src    BackupStore
	ns     namespace.Namespace
	signal func()
	worker int
	total  int
}

// writeBackupStacksTask adds total stacks for one writer, calling signal
// after the first stack so the caller can start the export while the
// remaining writes are still pending. It returns the first error.
func writeBackupStacksTask(t *testing.T, task backupWriteTask) error {
	t.Helper()

	project := fmt.Sprintf("w%d", task.worker)
	signaled := false

	for j := range task.total {
		st := newStack(task.ns.Name, project, fmt.Sprintf("s%d", j))
		st.Tags = map[string]string{"writer": project}

		if err := task.src.CreateStack(t.Context(), st, task.ns.Fence()); err != nil {
			if !signaled {
				task.signal()
			}

			return fmt.Errorf("create stack %s: %w", st.Ref, err)
		}

		if !signaled {
			signaled = true
			task.signal()
		}
	}

	return nil
}

// verifyBackupNoTear checks that the import holds a non-empty snapshot and
// that every imported stack equals its source row and carries its inventory
// entry. The non-empty check keeps a fast store from passing vacuously with
// an empty capture taken before any writer wrote.
func verifyBackupNoTear(t *testing.T, src, dst BackupStore, ns namespace.Namespace) {
	t.Helper()

	imported := mustListStacks(t, dst, ns.Name)

	if len(imported) == 0 {
		t.Fatal("imported stacks after a concurrent export = 0, want a non-empty snapshot")
	}

	for i := range imported {
		assertStack(t, imported[i], mustGetStack(t, src, imported[i].Ref))
	}

	entries := mustListUnits(t, dst, ns.Name)

	if len(entries) != len(imported) {
		t.Fatalf("inventory entries after import = %d, want one per stack (%d)", len(entries), len(imported))
	}

	byUnit := make(map[string]inventory.Entry, len(entries))
	for i := range entries {
		byUnit[entries[i].UnitID] = entries[i]
	}

	for i := range imported {
		e, ok := byUnit[imported[i].ID.String()]
		if !ok {
			t.Fatalf("stack %s misses its inventory entry after import", imported[i].Ref)
		}

		if e.Name != imported[i].Ref.Project.String()+"/"+imported[i].Ref.Stack.String() {
			t.Fatalf("inventory entry of %s = %q, want the stack name", imported[i].Ref, e.Name)
		}
	}
}

// testBackupImportRefusesNonEmpty imports into a store that holds a namespace
// and expects ErrNotEmpty, with the destination unchanged.
func testBackupImportRefusesNonEmpty(t *testing.T, newStore BackupFactory) {
	t.Helper()

	src := newStore(t)
	seedBackupStore(t, src)
	sum, data := exportBackup(t, src)

	dst := newStore(t)
	ns := newNamespace(t, dst, "acme")

	if _, err := dst.Import(t.Context(), bytes.NewReader(data), sum); !errors.Is(err, backup.ErrNotEmpty) {
		t.Fatalf("import into a non-empty store: err = %v, want ErrNotEmpty", err)
	}

	got := mustGetNamespace(t, dst, ns.Name)
	if got != ns {
		t.Fatalf("namespace after a refused import = %+v, want %+v", got, ns)
	}

	if stacks := mustListStacks(t, dst, ns.Name); len(stacks) != 0 {
		t.Fatalf("stacks after a refused import = %+v, want none", stacks)
	}
}

// otherDialect returns the dialect that differs from d.
func otherDialect(d backup.Dialect) backup.Dialect {
	if d == backup.DialectSQLite {
		return backup.DialectPostgres
	}

	return backup.DialectSQLite
}

// testBackupImportRefusesDialect imports with a manifest of another dialect
// and expects ErrDialectMismatch, with the destination still empty.
func testBackupImportRefusesDialect(t *testing.T, newStore BackupFactory) {
	t.Helper()

	src := newStore(t)
	seedBackupStore(t, src)
	sum, data := exportBackup(t, src)

	sum.Dialect = otherDialect(sum.Dialect)

	dst := newStore(t)

	if _, err := dst.Import(t.Context(), bytes.NewReader(data), sum); !errors.Is(err, backup.ErrDialectMismatch) {
		t.Fatalf("import of another dialect: err = %v, want ErrDialectMismatch", err)
	}

	assertBackupEmpty(t, dst)
}

// testBackupImportRefusesVersion imports with a manifest of another schema
// version and expects ErrSchemaMismatch, with the destination still empty.
func testBackupImportRefusesVersion(t *testing.T, newStore BackupFactory) {
	t.Helper()

	src := newStore(t)
	seedBackupStore(t, src)
	sum, data := exportBackup(t, src)

	sum.SchemaVersion++

	dst := newStore(t)

	if _, err := dst.Import(t.Context(), bytes.NewReader(data), sum); !errors.Is(err, backup.ErrSchemaMismatch) {
		t.Fatalf("import of another schema version: err = %v, want ErrSchemaMismatch", err)
	}

	assertBackupEmpty(t, dst)
}

// testBackupFailedImportLeavesEmpty imports truncated bytes and expects an
// error, with the destination still empty: no partial rows.
func testBackupFailedImportLeavesEmpty(t *testing.T, newStore BackupFactory) {
	t.Helper()

	src := newStore(t)
	seedBackupStore(t, src)
	sum, data := exportBackup(t, src)

	dst := newStore(t)

	if _, err := dst.Import(t.Context(), bytes.NewReader(data[:len(data)/2]), sum); err == nil {
		t.Fatal("import of truncated data succeeded, want an error")
	}

	assertBackupEmpty(t, dst)
}

// testBackupRowCountsMatch checks that the row counts the export returns
// equal the rows the import loads and the seeded rows.
func testBackupRowCountsMatch(t *testing.T, newStore BackupFactory) {
	t.Helper()

	src := newStore(t)
	seed := seedBackupStore(t, src)
	sum, data := exportBackup(t, src)
	assertExportGroundTruth(t, src, newStore, &seed, sum)

	dst := newStore(t)
	got := importBackup(t, dst, data, sum)

	if !maps.Equal(got.RowCounts, sum.RowCounts) {
		t.Fatalf("imported row counts = %v, want %v", got.RowCounts, sum.RowCounts)
	}

	assertSummary(t, got, sum)
	assertExportGroundTruth(t, dst, newStore, &seed, got)
}

// testBackupPeekKey peeks one wrapped data key per seeded wrapper from the
// export through an empty store: the key must equal the stored row, and the
// peeking store must stay empty, so PeekKey reads the stream without loading
// the destination.
func testBackupPeekKey(t *testing.T, newStore BackupFactory) {
	t.Helper()

	src := newStore(t)
	seed := seedBackupStore(t, src)
	_, data := exportBackup(t, src)

	dst := newStore(t)

	for i, wrapper := range []string{"kek-1", "kek-2"} {
		got, err := dst.PeekKey(t.Context(), bytes.NewReader(data), wrapper)
		if err != nil {
			t.Fatalf("peek key of wrapper %q: %v", wrapper, err)
		}

		want := mustGetKey(t, src, seed.scopes[i])

		if got.Scope != want.Scope || got.WrapperID != want.WrapperID ||
			!got.CreatedAt.Equal(want.CreatedAt) || !bytes.Equal(got.Ciphertext, want.Ciphertext) {
			t.Fatalf("peeked key of wrapper %q = %+v, want %+v", wrapper, got, want)
		}
	}

	assertBackupEmpty(t, dst)
}

// testBackupPeekUnknownWrapper peeks a wrapper the export holds no key of
// and expects ErrCorrupt, with the peeking store still empty.
func testBackupPeekUnknownWrapper(t *testing.T, newStore BackupFactory) {
	t.Helper()

	src := newStore(t)
	seedBackupStore(t, src)
	_, data := exportBackup(t, src)

	dst := newStore(t)

	if _, err := dst.PeekKey(t.Context(), bytes.NewReader(data), "kek-unknown"); !errors.Is(
		err, backup.ErrCorrupt,
	) {
		t.Fatalf("peek key of an unknown wrapper: err = %v, want ErrCorrupt", err)
	}

	assertBackupEmpty(t, dst)
}

// testBackupPeekCorrupt peeks truncated and empty streams and expects
// ErrCorrupt, with the peeking store still empty.
func testBackupPeekCorrupt(t *testing.T, newStore BackupFactory) {
	t.Helper()

	src := newStore(t)
	seedBackupStore(t, src)
	_, data := exportBackup(t, src)

	dst := newStore(t)

	if _, err := dst.PeekKey(t.Context(), bytes.NewReader(data[:len(data)/2]), "kek-1"); !errors.Is(
		err, backup.ErrCorrupt,
	) {
		t.Fatalf("peek key of truncated data: err = %v, want ErrCorrupt", err)
	}

	if _, err := dst.PeekKey(t.Context(), bytes.NewReader(nil), "kek-1"); !errors.Is(
		err, backup.ErrCorrupt,
	) {
		t.Fatalf("peek key of empty data: err = %v, want ErrCorrupt", err)
	}

	assertBackupEmpty(t, dst)
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

// stageBackup stages data with want into dst, failing on error.
func stageBackup(t *testing.T, dst BackupStore, data []byte, want backup.Summary) backup.Summary {
	t.Helper()

	staged, err := dst.StageRestore(t.Context(), bytes.NewReader(data), want)
	if err != nil {
		t.Fatalf("stage backup: %v", err)
	}

	return staged
}

// discardStaging removes the staging area of dst, failing on error.
func discardStaging(t *testing.T, dst BackupStore) {
	t.Helper()

	if err := dst.DiscardStaging(t.Context()); err != nil {
		t.Fatalf("discard staging area: %v", err)
	}
}

// liveSummary exports the live data of s into a summary, failing on error. The
// staging cases compare it before and after, so any write to the live data fails.
func liveSummary(t *testing.T, s BackupStore) backup.Summary {
	t.Helper()

	var buf bytes.Buffer

	sum, err := s.Export(t.Context(), &buf)
	if err != nil {
		t.Fatalf("export live data: %v", err)
	}

	return sum
}

// testBackupStagingKeepsLive stages a backup into a store that holds live rows:
// the staged summary equals the manifest, the live rows stay, and discarding
// the staging area keeps them too. The foreign-key check runs on the staged
// data: only a consistent backup stages. The refusal case below covers a
// violation through the ExportTamperer hook.
func testBackupStagingKeepsLive(t *testing.T, newStore BackupFactory) {
	t.Helper()

	src := newStore(t)
	seed := seedBackupStore(t, src)
	sum, data := exportBackup(t, src)
	assertExportGroundTruth(t, src, newStore, &seed, sum)

	dst := newStore(t)
	live := seedBackupStore(t, dst)
	before := liveSummary(t, dst)
	beforeNS := mustGetNamespace(t, dst, live.acme.Name)

	staged := stageBackup(t, dst, data, sum)
	assertSummary(t, staged, sum)

	assertSummary(t, liveSummary(t, dst), before)

	if got := mustGetNamespace(t, dst, live.acme.Name); got != beforeNS {
		t.Fatalf("namespace after staging = %+v, want %+v", got, beforeNS)
	}

	discardStaging(t, dst)
	assertSummary(t, liveSummary(t, dst), before)

	if got := mustGetNamespace(t, dst, live.acme.Name); got != beforeNS {
		t.Fatalf("namespace after discarding the staging area = %+v, want %+v", got, beforeNS)
	}

	// The staging area is gone: the same bytes stage cleanly again.
	stageBackup(t, dst, data, sum)
	discardStaging(t, dst)
	assertSummary(t, liveSummary(t, dst), before)
}

// testBackupStagingReplacesPrevious stages two backups without an
// intervening discard: only one staging area exists at a time, so the second
// stage discards the first. The staged summary equals the second manifest,
// the live data is unchanged, and discarding cleans the second staging area.
// Stores with the StagingWitness capability also prove the first staging area
// is gone after the second stage: a mutant that forgets to drop it keeps it
// alive and fails.
func testBackupStagingReplacesPrevious(t *testing.T, newStore BackupFactory) {
	t.Helper()

	exports := twoStagingExports(t, newStore)

	dst := newStore(t)
	live := seedBackupStore(t, dst)
	before := liveSummary(t, dst)
	beforeNS := mustGetNamespace(t, dst, live.acme.Name)

	assertSummary(t, stageBackup(t, dst, exports.first.data, exports.first.sum), exports.first.sum)
	assertSummary(t, liveSummary(t, dst), before)

	firstID := requireStaging(t, dst)

	assertSummary(t, stageBackup(t, dst, exports.second.data, exports.second.sum), exports.second.sum)
	assertSummary(t, liveSummary(t, dst), before)

	if got := mustGetNamespace(t, dst, live.acme.Name); got != beforeNS {
		t.Fatalf("namespace after the second stage = %+v, want %+v", got, beforeNS)
	}

	secondID := requireReplacedStaging(t, dst, firstID)

	discardStaging(t, dst)
	requireNoStaging(t, dst, secondID)
	assertSummary(t, liveSummary(t, dst), before)

	if got := mustGetNamespace(t, dst, live.acme.Name); got != beforeNS {
		t.Fatalf("namespace after discarding the staging area = %+v, want %+v", got, beforeNS)
	}
}

// stagedExport pairs an export stream with its manifest.
type stagedExport struct {
	sum  backup.Summary
	data []byte
}

// stagingPair holds two exports of one store with an extra stack between
// them, so the second backup holds one more stack than the first.
type stagingPair struct {
	first  stagedExport
	second stagedExport
}

// twoStagingExports exports one store twice with an extra stack between the
// exports, so the second backup holds one more stack than the first.
func twoStagingExports(t *testing.T, newStore BackupFactory) stagingPair {
	t.Helper()

	src := newStore(t)
	seed := seedBackupStore(t, src)
	sum1, data1 := exportBackup(t, src)
	mustCreateStack(t, src, seed.acme, "extra")
	sum2, data2 := exportBackup(t, src)

	if sum2.RowCounts["stacks"] != sum1.RowCounts["stacks"]+1 {
		t.Fatalf(
			"second export stacks = %d, want one more than %d",
			sum2.RowCounts["stacks"],
			sum1.RowCounts["stacks"],
		)
	}

	return stagingPair{
		first:  stagedExport{sum: sum1, data: data1},
		second: stagedExport{sum: sum2, data: data2},
	}
}

// requireStaging returns the ID of the current staging area, failing when a
// witness reports none. Stores without the StagingWitness capability return
// empty: the summary checks above already prove the stage landed.
func requireStaging(t *testing.T, dst BackupStore) string {
	t.Helper()

	witness, ok := dst.(StagingWitness)
	if !ok {
		return ""
	}

	id := witness.CurrentStagingID()

	if id == "" {
		t.Fatal("current staging ID after the first stage is empty, want the staging area")
	}

	if !witness.StagingAlive(id) {
		t.Fatalf("staging area %q is not alive after the first stage, want it alive", id)
	}

	return id
}

// requireReplacedStaging returns the ID of the current staging area after the
// second stage, failing when it equals the first or the first is still alive.
// A mutant that forgets to discard the previous staging area keeps it alive
// and fails here. Stores without the capability return empty.
func requireReplacedStaging(t *testing.T, dst BackupStore, firstID string) string {
	t.Helper()

	witness, ok := dst.(StagingWitness)
	if !ok {
		return ""
	}

	secondID := witness.CurrentStagingID()

	if secondID == "" {
		t.Fatal("current staging ID after the second stage is empty, want the staging area")
	}

	if secondID == firstID {
		t.Fatalf("staging ID after the second stage = %q, want a new staging area", secondID)
	}

	if witness.StagingAlive(firstID) {
		t.Fatalf("staging area %q is still alive after the second stage, want it discarded", firstID)
	}

	if !witness.StagingAlive(secondID) {
		t.Fatalf("staging area %q is not alive after the second stage, want it alive", secondID)
	}

	return secondID
}

// requireNoStaging fails when the staging area with id is still alive or a
// witness reports a current staging area after the discard.
func requireNoStaging(t *testing.T, dst BackupStore, id string) {
	t.Helper()

	witness, ok := dst.(StagingWitness)
	if !ok {
		return
	}

	if witness.StagingAlive(id) {
		t.Fatalf("staging area %q is still alive after discard, want it discarded", id)
	}

	if got := witness.CurrentStagingID(); got != "" {
		t.Fatalf("current staging ID after discard = %q, want none", got)
	}
}

// testBackupStagingRefusesOmittedTable stages with a manifest that omits an
// empty table and expects ErrCorrupt, with the live data still empty. An
// omitted empty table reads as zero rows, so only the explicit omission check
// refuses it: the count comparison alone passes.
func testBackupStagingRefusesOmittedTable(t *testing.T, newStore BackupFactory) {
	t.Helper()

	src := newStore(t)
	sum, data := exportBackup(t, src)

	omitted := ""
	for _, table := range backup.Tables {
		if sum.RowCounts[table] == 0 {
			omitted = table

			break
		}
	}

	if omitted == "" {
		t.Fatal("export names no empty table, want one table to omit")
	}

	sum.RowCounts = maps.Clone(sum.RowCounts)
	delete(sum.RowCounts, omitted)

	dst := newStore(t)

	if _, err := dst.StageRestore(t.Context(), bytes.NewReader(data), sum); !errors.Is(
		err, backup.ErrCorrupt,
	) {
		t.Fatalf("stage backup that omits empty table %q: err = %v, want ErrCorrupt", omitted, err)
	}

	assertBackupEmpty(t, dst)
	discardStaging(t, dst)
	assertBackupEmpty(t, dst)
}

// testBackupStagingRefusesDialect stages with a manifest of another dialect and
// expects ErrDialectMismatch, with the live data still empty and no staging left.
func testBackupStagingRefusesDialect(t *testing.T, newStore BackupFactory) {
	t.Helper()

	src := newStore(t)
	seedBackupStore(t, src)
	sum, data := exportBackup(t, src)

	sum.Dialect = otherDialect(sum.Dialect)

	dst := newStore(t)

	if _, err := dst.StageRestore(t.Context(), bytes.NewReader(data), sum); !errors.Is(
		err, backup.ErrDialectMismatch,
	) {
		t.Fatalf("stage backup of another dialect: err = %v, want ErrDialectMismatch", err)
	}

	assertBackupEmpty(t, dst)
	discardStaging(t, dst)

	sum.Dialect = otherDialect(sum.Dialect)
	stageBackup(t, dst, data, sum)
	discardStaging(t, dst)
	assertBackupEmpty(t, dst)
}

// testBackupStagingRefusesVersion stages with a manifest of another schema
// version and expects ErrSchemaMismatch, with the live data still empty.
func testBackupStagingRefusesVersion(t *testing.T, newStore BackupFactory) {
	t.Helper()

	src := newStore(t)
	seedBackupStore(t, src)
	sum, data := exportBackup(t, src)

	sum.SchemaVersion++

	dst := newStore(t)

	if _, err := dst.StageRestore(t.Context(), bytes.NewReader(data), sum); !errors.Is(
		err, backup.ErrSchemaMismatch,
	) {
		t.Fatalf("stage backup of another schema version: err = %v, want ErrSchemaMismatch", err)
	}

	assertBackupEmpty(t, dst)
	discardStaging(t, dst)
}

// testBackupStagingRefusesCorrupt stages truncated bytes and expects an error,
// with the live data still empty and no staging left.
func testBackupStagingRefusesCorrupt(t *testing.T, newStore BackupFactory) {
	t.Helper()

	src := newStore(t)
	seedBackupStore(t, src)
	sum, data := exportBackup(t, src)

	dst := newStore(t)

	if _, err := dst.StageRestore(t.Context(), bytes.NewReader(data[:len(data)/2]), sum); !errors.Is(
		err, backup.ErrCorrupt,
	) {
		t.Fatalf("stage backup of truncated data: err = %v, want ErrCorrupt", err)
	}

	assertBackupEmpty(t, dst)
	discardStaging(t, dst)

	stageBackup(t, dst, data, sum)
	discardStaging(t, dst)
	assertBackupEmpty(t, dst)
}

// testBackupStagingRefusesForeignKeys stages an export whose stacks miss
// their namespace and expects ErrCorrupt, with the live data still empty.
// Stores without the ExportTamperer capability skip this case: no port writes
// an orphan, so the suite tampers the stream through the hook.
func testBackupStagingRefusesForeignKeys(t *testing.T, newStore BackupFactory) {
	t.Helper()

	src := newStore(t)
	seedBackupStore(t, src)
	sum, data := exportBackup(t, src)

	dst := newStore(t)

	tamperer, ok := dst.(ExportTamperer)
	if !ok {
		t.Skip("store cannot tamper an export stream")
	}

	tampered, want := tamperer.TamperNamespaces(t, data, sum)

	if _, err := dst.StageRestore(t.Context(), bytes.NewReader(tampered), want); !errors.Is(
		err, backup.ErrCorrupt,
	) {
		t.Fatalf("stage backup of stacks without their namespace: err = %v, want ErrCorrupt", err)
	}

	assertBackupEmpty(t, dst)
	discardStaging(t, dst)
}

// testBackupStagingRefusesCounts stages with a manifest whose row counts differ
// and expects ErrCorrupt, with the live data still empty.
func testBackupStagingRefusesCounts(t *testing.T, newStore BackupFactory) {
	t.Helper()

	src := newStore(t)
	seedBackupStore(t, src)
	sum, data := exportBackup(t, src)

	sum.RowCounts["namespaces"]++

	dst := newStore(t)

	if _, err := dst.StageRestore(t.Context(), bytes.NewReader(data), sum); !errors.Is(
		err, backup.ErrCorrupt,
	) {
		t.Fatalf("stage backup of wrong row counts: err = %v, want ErrCorrupt", err)
	}

	assertBackupEmpty(t, dst)
	discardStaging(t, dst)
}

// testBackupStagingRefusesBlobs stages with a manifest whose blob references
// differ and expects ErrCorrupt, with the live data still empty. The seed holds
// one finalized state version, so the manifest always names one blob.
func testBackupStagingRefusesBlobs(t *testing.T, newStore BackupFactory) {
	t.Helper()

	src := newStore(t)
	seedBackupStore(t, src)
	sum, data := exportBackup(t, src)

	if len(sum.Blobs) == 0 {
		t.Fatal("export names no blob, want the blob of the finalized state version")
	}

	sum.Blobs[0].Size++

	dst := newStore(t)

	if _, err := dst.StageRestore(t.Context(), bytes.NewReader(data), sum); !errors.Is(
		err, backup.ErrCorrupt,
	) {
		t.Fatalf("stage backup of wrong blob references: err = %v, want ErrCorrupt", err)
	}

	assertBackupEmpty(t, dst)
	discardStaging(t, dst)
}
