package storetest

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// discardStaging removes the staging area of dst, failing on error.
func discardStaging(t *testing.T, dst BackupStore) {
	t.Helper()

	if err := dst.DiscardStaging(t.Context()); err != nil {
		t.Fatalf("discard staging area: %v", err)
	}
}

// portableSummaryOf exports s in the portable format and returns its summary.
func portableSummaryOf(t *testing.T, s PortableStore) backup.Summary {
	t.Helper()

	_, sum, _ := exportPortable(t, s)

	return sum
}

// assertSameSummary compares two summaries field by field.
func assertSameSummary(t *testing.T, got, want backup.Summary) {
	t.Helper()

	if got.Dialect != want.Dialect || got.SchemaVersion != want.SchemaVersion {
		t.Fatalf("summary dialect or version = %q %d, want %q %d",
			got.Dialect, got.SchemaVersion, want.Dialect, want.SchemaVersion)
	}

	if !slices.Equal(got.PrimaryInstallations, want.PrimaryInstallations) {
		t.Fatalf("summary primary installations = %v, want %v", got.PrimaryInstallations, want.PrimaryInstallations)
	}

	if !slices.Equal(got.WrapperIDs, want.WrapperIDs) {
		t.Fatalf("summary wrapper IDs = %v, want %v", got.WrapperIDs, want.WrapperIDs)
	}

	if !maps.Equal(got.RowCounts, want.RowCounts) {
		t.Fatalf("summary row counts = %v, want %v", got.RowCounts, want.RowCounts)
	}

	if !slices.EqualFunc(got.Blobs, want.Blobs, sameBlobRef) {
		t.Fatalf("summary blobs = %+v, want %+v", got.Blobs, want.Blobs)
	}
}

// testRestoreStagingKeepsLive stages a portable backup into a store that holds
// live rows: the staged summary equals the export, the live rows stay, and
// discarding the staging area keeps them too.
func testRestoreStagingKeepsLive(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	ps := portableRestoreStores(t, newStore(t), newStore(t))
	ctx := t.Context()

	seedBackupStore(t, ps.src)
	sink, sum, files := exportPortable(t, ps.src)

	live := seedBackupStore(t, ps.dst)
	before := portableSummaryOf(t, ps.dst)
	beforeNS := mustGetNamespace(t, ps.dst, live.acme.Name)

	staged, err := ps.dst.StagePortable(ctx, sink.read, sum, files, StageAs(t, ps.dst))
	if err != nil {
		t.Fatalf("stage portable backup: %v", err)
	}

	assertSameSummary(t, staged, sum)
	assertSameSummary(t, portableSummaryOf(t, ps.dst), before)

	if got := mustGetNamespace(t, ps.dst, live.acme.Name); got != beforeNS {
		t.Fatalf("namespace after staging = %+v, want %+v", got, beforeNS)
	}

	discardStaging(t, ps.dst)
	assertSameSummary(t, portableSummaryOf(t, ps.dst), before)

	// The staging area is gone: the same bytes stage cleanly again.
	if _, err := ps.dst.StagePortable(ctx, sink.read, sum, files, StageAs(t, ps.dst)); err != nil {
		t.Fatalf("stage portable backup again: %v", err)
	}

	discardStaging(t, ps.dst)
	assertSameSummary(t, portableSummaryOf(t, ps.dst), before)
}

// testRestoreStagingReplacesPrevious stages two portable backups without an
// intervening discard: only one staging area exists at a time, so the second
// stage discards the first. Stores with the StagingWitness capability also prove
// the first staging area is gone after the second stage.
func testRestoreStagingReplacesPrevious(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	ps := portableRestoreStores(t, newStore(t), newStore(t))
	ctx := t.Context()

	seed := seedBackupStore(t, ps.src)
	sink1, sum1, files1 := exportPortable(t, ps.src)
	mustCreateStack(t, ps.src, seed.acme, "extra")
	sink2, sum2, files2 := exportPortable(t, ps.src)

	if sum2.RowCounts["stacks"] != sum1.RowCounts["stacks"]+1 {
		t.Fatalf("second export stacks = %d, want one more than %d",
			sum2.RowCounts["stacks"], sum1.RowCounts["stacks"])
	}

	live := seedBackupStore(t, ps.dst)
	before := portableSummaryOf(t, ps.dst)
	beforeNS := mustGetNamespace(t, ps.dst, live.acme.Name)

	witness, hasWitness := ps.dst.(StagingWitness)

	if _, err := ps.dst.StagePortable(ctx, sink1.read, sum1, files1, StageAs(t, ps.dst)); err != nil {
		t.Fatalf("stage first backup: %v", err)
	}

	firstID := ""
	if hasWitness {
		firstID = witness.CurrentStagingID()
		if firstID == "" {
			t.Fatal("no staging area after the first stage")
		}
	}

	if _, err := ps.dst.StagePortable(ctx, sink2.read, sum2, files2, StageAs(t, ps.dst)); err != nil {
		t.Fatalf("stage second backup: %v", err)
	}

	assertSameSummary(t, portableSummaryOf(t, ps.dst), before)

	if got := mustGetNamespace(t, ps.dst, live.acme.Name); got != beforeNS {
		t.Fatalf("namespace after the second stage = %+v, want %+v", got, beforeNS)
	}

	secondID := ""
	if hasWitness {
		secondID = requireOnlySecondStaging(t, witness, firstID)
	}

	discardStaging(t, ps.dst)

	if hasWitness {
		requireNoStagingLeft(t, witness, secondID)
	}

	assertSameSummary(t, portableSummaryOf(t, ps.dst), before)
}

// requireOnlySecondStaging checks the staging area after a second stage: it is
// a new area, the first area is gone, and the second area is alive. It returns
// the ID of the second area.
func requireOnlySecondStaging(t *testing.T, witness StagingWitness, firstID string) string {
	t.Helper()

	secondID := witness.CurrentStagingID()
	if secondID == "" || secondID == firstID {
		t.Fatalf("staging area after the second stage = %q, want a new area", secondID)
	}

	if witness.StagingAlive(firstID) {
		t.Fatal("the first staging area is still alive after the second stage")
	}

	if !witness.StagingAlive(secondID) {
		t.Fatal("the second staging area is gone after its stage")
	}

	return secondID
}

// requireNoStagingLeft checks that a discard removed the second staging area.
func requireNoStagingLeft(t *testing.T, witness StagingWitness, secondID string) {
	t.Helper()

	if witness.StagingAlive(secondID) || witness.CurrentStagingID() != "" {
		t.Fatal("a staging area survives the discard")
	}
}

// testRestoreStagingRefusesUnsealedState stages a portable backup whose
// state versions file names an unsealed row: the stage fails with
// ErrUnsealedState, so plain state never enters through an import.
func testRestoreStagingRefusesUnsealedState(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	ps := portableRestoreStores(t, newStore(t), newStore(t))
	ctx := t.Context()

	ns := newWorkspaceNamespace(t, ps.src, "acme")
	w := createTestWorkspace(t, ps.src, ns, "network")
	svc := serviceOver(t, ps.src)

	if _, err := svc.Lock(ctx, w.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	pushThrough(t, svc, w.ID, 1, `{"version":4,"serial":1}`)

	sink, sum, files := exportPortable(t, ps.src)

	edited, descs := unsealStateVersionsFile(t, sink, files)

	if _, err := ps.dst.StagePortable(ctx, PortableOpener(edited), sum, descs, StageAs(t, ps.dst)); !errors.Is(
		err, workspace.ErrUnsealedState,
	) {
		t.Fatalf("stage backup with an unsealed state version: err = %v, want ErrUnsealedState", err)
	}

	discardStaging(t, ps.dst)
}

// unsealStateVersionsFile returns a copy of the export with every sealed flag
// of the state versions file cleared, with its descriptor fixed: the size and
// the SHA-256 of the rewritten file, with the row count unchanged. The
// summary still counts the rows, so only the sealed check of the stage can
// refuse it. No port writes an unsealed row, so the test edits the export by
// hand.
func unsealStateVersionsFile(
	t *testing.T, sink *portableSink, descs []backup.PortableFile,
) (map[string][]byte, []backup.PortableFile) {
	t.Helper()

	const sealedTrue = `"sealed":true`

	path := backup.PortablePath("state_versions")

	lines := readPortableLines(t, path, sink.files[path].Bytes())
	if len(lines) == 0 {
		t.Fatal("state versions file holds no rows, want the pushed version")
	}

	for i, row := range lines {
		sealed, ok := row["sealed"].(bool)
		if !ok || !sealed {
			t.Fatalf("state versions row %d sealed = %v, want true", i, row["sealed"])
		}
	}

	var rewritten bytes.Buffer

	zw := gzip.NewWriter(&rewritten)

	for _, row := range readPortableRawLines(t, path, sink.files[path].Bytes()) {
		edited := strings.ReplaceAll(row, sealedTrue, `"sealed":false`)
		if edited == row {
			t.Fatalf("state versions row lacks %s: %s", sealedTrue, row)
		}

		if _, err := zw.Write([]byte(edited + "\n")); err != nil {
			t.Fatalf("write unsealed row: %v", err)
		}
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("finish unsealed file: %v", err)
	}

	data := rewritten.Bytes()
	digest := sha256.Sum256(data)

	out := sinkFiles(sink)
	out[path] = data

	outDescs := make([]backup.PortableFile, 0, len(descs))

	for _, d := range descs {
		if d.Table == "state_versions" {
			d.Size = int64(len(data))
			d.SHA256 = hex.EncodeToString(digest[:])
		}

		outDescs = append(outDescs, d)
	}

	return out, outDescs
}

// readPortableRawLines returns the raw lines of one gzipped table file.
func readPortableRawLines(t *testing.T, path string, data []byte) []string {
	t.Helper()

	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("open gzip of %q: %v", path, err)
	}

	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}

	if err := zr.Close(); err != nil {
		t.Fatalf("close gzip of %q: %v", path, err)
	}

	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}

	return lines
}

// testRestoreStagingKeepsCheckpointBlobs stages a portable backup that holds a
// checkpoint kept in blobs: the staged summary names the blob, like the export
// does.
func testRestoreStagingKeepsCheckpointBlobs(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	ps := portableRestoreStores(t, newStore(t), newStore(t))

	f := newFixture(t, ps.src)
	u := f.run(t, f.ref, "u1", mutating())

	st := mustGetStack(t, ps.src, f.ref)

	key, err := blob.ParseKey(
		stackBlobPrefix(t, ps.src, f.ref.Namespace) + st.ID.String() + "/" + u.ID.String() + "/0-" + defaultAttempt,
	)
	if err != nil {
		t.Fatalf("parse blob key: %v", err)
	}

	want := newBlobCheckpoint(u, key, 0, `{"v":1}`)

	if _, putErr := ps.src.PutCheckpoint(t.Context(), want, f.fence); putErr != nil {
		t.Fatalf("put checkpoint: %v", putErr)
	}

	f.end(t, u, stack.StatusSucceeded)

	sink, sum, files := exportPortable(t, ps.src)

	wantBlobs := []backup.BlobRef{{MD5: want.BlobMD5, Key: key.String(), Size: want.BlobSize, Sealed: false}}
	if !slices.EqualFunc(sum.Blobs, wantBlobs, sameBlobRef) {
		t.Fatalf("export blobs = %+v, want %+v", sum.Blobs, wantBlobs)
	}

	staged, err := ps.dst.StagePortable(t.Context(), sink.read, sum, files, StageAs(t, ps.dst))
	if err != nil {
		t.Fatalf("stage portable backup: %v", err)
	}

	assertSameSummary(t, staged, sum)
	discardStaging(t, ps.dst)
}

// testRestoreJournalBlob shows that a restore keeps the journal rows of a
// running update, with the blob each batch names (ADR 43, decision 6.8). The
// update stays running after the export, so the rows belong to a running update.
func testRestoreJournalBlob(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	ps := portableRestoreStores(t, newStore(t), newStore(t))
	ctx := t.Context()

	f := newFixture(t, ps.src)
	u := f.runJournaled(t, f.ref, "u1")

	row := journalRow(t, ps.src, u, 7, 1)
	row.Size = 33

	if _, err := ps.src.PutJournalRow(ctx, row, f.fence); errors.Is(err, errors.ErrUnsupported) {
		t.Skip("the store keeps no journal rows")
	} else if err != nil {
		t.Fatalf("journal batch: %v", err)
	}

	sink, sum, files := exportPortable(t, ps.src)

	if _, err := ps.dst.StagePortable(ctx, sink.read, sum, files, StageAs(t, ps.dst)); err != nil {
		t.Fatalf("stage portable backup: %v", err)
	}

	if err := ps.dst.ApplyStaging(ctx); err != nil {
		t.Fatalf("apply staged backup: %v", err)
	}

	keys, err := ps.dst.JournalKeys(ctx, mustStackID(t, ps.dst, f.ref), u.ID)
	if err != nil || !slices.Equal(keys, []blob.Key{row.BlobKey}) {
		t.Fatalf("journal after the restore = %v, %v; want %s", keys, err, row.BlobKey)
	}
}

// testRestoreRoundTrip restores a portable export and exports the restored
// store again: every table holds the rows it held, and every table file except
// the namespaces file and the audit sink cursors is byte-identical. The
// namespaces file changes, because the apply moves every namespace to a new
// failover version. The apply resets the audit sink cursors, so the restored
// store holds none (ADR 60 decision 13).
func testRestoreRoundTrip(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	ps := portableRestoreStores(t, newStore(t), newStore(t))
	ctx := t.Context()

	seed := seedBackupStore(t, ps.src)
	sink, sum, files := exportPortable(t, ps.src)
	checkImportRowCounts(t, ps.src, sum)
	checkBreakGlassRowCounts(t, ps.src, sum)
	checkBlameRowCounts(t, ps.src, sum)
	checkProfileRowCounts(t, ps.src, sum)
	checkIssuerRowCounts(t, ps.src, sum)
	checkRunnerRowCounts(t, ps.src, sum)
	requireSeededGate(t, ps.src, sum)

	if _, err := ps.dst.StagePortable(ctx, sink.read, sum, files, StageAs(t, ps.dst)); err != nil {
		t.Fatalf("stage portable backup: %v", err)
	}

	if err := ps.dst.ApplyStaging(ctx); err != nil {
		t.Fatalf("apply staged backup: %v", err)
	}

	checkRestoredSuspendedMember(t, ps.dst)
	checkRestoredImports(t, ps.dst, seed.acme, seed.imports)
	checkRestoredBlame(t, ps.dst, blameSeed{
		ns: seed.acme, w: seed.net, first: seed.current.ID.String(), second: seed.pending.ID.String(),
	})
	checkRestoredProfiles(t, ps.dst, seed.acme, seed.profiles)
	checkRestoredIssuer(t, ps.dst, seed.acme, seed.issuer)
	checkRestoredRunners(t, ps.dst, seed.acme.Name, seed.runners)
	checkRunnerControlRowsAbsent(t, ps.dst)

	again, sum2, _ := exportPortable(t, ps.dst)
	requireSeededGate(t, ps.dst, sum2)

	wantRows := maps.Clone(sum.RowCounts)
	if _, ok := wantRows[sinkCursorTable]; ok {
		wantRows[sinkCursorTable] = 0
	}

	if !maps.Equal(sum2.RowCounts, wantRows) || !slices.EqualFunc(sum2.Blobs, sum.Blobs, sameBlobRef) {
		t.Fatalf("restored summary rows = %v, want %v", sum2.RowCounts, wantRows)
	}

	requireTablesRestored(t, sink, again)
}

// requireTablesRestored proves that every table of the second export holds the
// rows of the first export. The namespaces table and the sink cursors differ by
// design.
func requireTablesRestored(t *testing.T, sink, again *portableSink) {
	t.Helper()

	for _, table := range backup.Tables {
		path := backup.PortablePath(table)

		if table == "namespaces" || table == sinkCursorTable {
			continue
		}

		want := sink.files[path].Bytes()
		got := again.files[path].Bytes()

		// A restore releases the workspace locks, so the two tables that
		// hold them are compared with the locks of the export cleared.
		if table == "workspaces" || table == "inventory" {
			wantRows := releaseWorkspaceLocks(readPortableLines(t, path, want))
			gotRows := releaseWorkspaceLocks(readPortableLines(t, path, got))

			if !reflect.DeepEqual(gotRows, wantRows) {
				t.Fatalf("table %q after the restore = %v, want %v with the workspace locks released",
					table, gotRows, wantRows)
			}

			continue
		}

		if !bytes.Equal(got, want) {
			t.Fatalf("table %q after the restore differs from the export: %d bytes, want %d",
				table, len(got), len(want))
		}
	}
}

// checkRestoredSuspendedMember proves that the suspended member of the seed
// keeps its source, source ID, state, suspend time and pin after the restore.
// A store with no principal port skips it.
func checkRestoredSuspendedMember(t *testing.T, dst RestoreStore) {
	t.Helper()

	ps, ok := dst.(principal.Store)
	if !ok {
		return
	}

	members, _, err := ps.ListMembers(
		t.Context(), "grp-ops", principal.Query{After: "", Low: "", High: "", Limit: principal.MaxLimit + 1},
	)
	if err != nil {
		t.Fatalf("list restored members: %v", err)
	}

	for i := range members {
		m := members[i]
		if m.PrincipalID != "prn-suspended" {
			continue
		}

		if m.Source != principal.MembershipSourceSCIM || m.SourceID != "scim-token-1" ||
			m.State != principal.MembershipStateSuspended || m.SuspendedAt.IsZero() || !m.Pinned {
			t.Fatalf(
				"restored suspended member = %+v, want scim with its source ID, suspended, pinned",
				m,
			)
		}

		return
	}

	t.Fatal("restored members hold no suspended member, want prn-suspended")
}

// sinkCursorTable is the table of the audit sink cursors, which the apply
// of a restore empties.
const sinkCursorTable = "audit_sink_cursors"

// writeBackupStacksTask adds total stacks for one writer, calling signal after
// the first stack, so the caller can start the export while the remaining
// writes are still pending. It returns the first error.
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

// backupWriteTask is one concurrent writer of the no-tear case: worker numbers
// the writer, total counts its stacks, and signal runs after its first stack.
type backupWriteTask struct {
	src    BackupStore
	ns     namespace.Namespace
	signal func()
	worker int
	total  int
}

// testRestoreExportDoesNotTear exports while writers add stacks, each in one
// store transaction with its tags and inventory entry. The export is one
// snapshot: every stack the restore holds equals its source row and carries
// its inventory entry, all in it or all out of it.
func testRestoreExportDoesNotTear(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	ps := portableRestoreStores(t, newStore(t), newStore(t))
	ctx := t.Context()

	ns := newNamespace(t, ps.src, "acme")

	const writers = 8

	const perWriter = 8

	var wg sync.WaitGroup

	errs := make([]error, writers)
	firstDone := make(chan struct{}, writers)

	for w := range writers {
		wg.Go(func() {
			errs[w] = writeBackupStacksTask(t, backupWriteTask{
				src: ps.src, ns: ns, worker: w, total: perWriter, signal: func() { firstDone <- struct{}{} },
			})
		})
	}

	for range writers {
		<-firstDone
	}

	sink, sum, files := exportPortable(t, ps.src)
	wg.Wait()

	for w, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", w, err)
		}
	}

	if _, err := ps.dst.StagePortable(ctx, sink.read, sum, files, StageAs(t, ps.dst)); err != nil {
		t.Fatalf("stage portable backup: %v", err)
	}

	if err := ps.dst.ApplyStaging(ctx); err != nil {
		t.Fatalf("apply staged backup: %v", err)
	}

	verifyBackupNoTear(t, ps.src, ps.dst, ns)
}

// verifyBackupNoTear checks that the restored store holds a non-empty snapshot,
// that every stack equals its source row, and that every stack carries its
// inventory entry. The non-empty check keeps a fast store from passing with an
// empty capture taken before any writer wrote.
func verifyBackupNoTear(t *testing.T, src, dst BackupStore, ns namespace.Namespace) {
	t.Helper()

	imported, err := dst.ListStacks(t.Context(), stack.Filter{Namespace: ns.Name, Project: ""})
	if err != nil {
		t.Fatalf("list restored stacks: %v", err)
	}

	if len(imported) == 0 {
		t.Fatal("restored stacks after a concurrent export = 0, want a non-empty snapshot")
	}

	for i := range imported {
		assertStack(t, imported[i], mustGetStack(t, src, imported[i].Ref))
	}

	entries, err := dst.ListUnits(t.Context(), ns.Name)
	if err != nil {
		t.Fatalf("list restored inventory: %v", err)
	}

	if len(entries) != len(imported) {
		t.Fatalf("inventory entries after restore = %d, want one per stack (%d)", len(entries), len(imported))
	}

	byUnit := make(map[string]inventory.Entry, len(entries))
	for i := range entries {
		byUnit[entries[i].UnitID] = entries[i]
	}

	for i := range imported {
		e, ok := byUnit[imported[i].ID.String()]
		if !ok {
			t.Fatalf("stack %s misses its inventory entry after restore", imported[i].Ref)
		}

		if e.Name != imported[i].Ref.Project.String()+"/"+imported[i].Ref.Stack.String() {
			t.Fatalf("inventory entry of %s = %q, want the stack name", imported[i].Ref, e.Name)
		}
	}
}

// releaseWorkspaceLocks returns rows of the workspaces table or of the
// inventory table with the lock of every OpenTofu workspace cleared, as a
// restore leaves them. An adapter may export a zero lock time as null or as
// 0, so the result holds null for both.
func releaseWorkspaceLocks(rows []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(rows))

	for _, row := range rows {
		cleared := maps.Clone(row)

		if _, isWorkspace := cleared["lock_id"]; isWorkspace {
			cleared["lock_id"] = ""
			cleared["lock_holder"] = ""
			cleared["lock_reason"] = ""
			cleared["locked_at_ms"] = nil
		} else if cleared["tool"] == workspace.InventoryTool {
			cleared["lock_holder"] = nil
			cleared["lock_since_ms"] = nil
			cleared["lock_operation"] = nil
		}

		out = append(out, cleared)
	}

	return out
}
