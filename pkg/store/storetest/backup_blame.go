package storetest

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// blameTables lists the tables of the blame of workspaces.
func blameTables() []string {
	return []string{"workspace_blame_cursors", "workspace_blame_items", "workspace_resource_changes"}
}

// blameSeed names the workspace of seedBlameRows and its two state versions.
type blameSeed struct {
	ns     namespace.Namespace
	w      workspace.Workspace
	first  string
	second string
}

// seedBlameRows records the blame of the workspace w: a readable state version
// with two rows, one of them a restore, then an opaque state version with a
// lineage change. It writes nothing when s keeps no blame.
func seedBlameRows(t *testing.T, s BackupStore, seed blameSeed) {
	t.Helper()

	blames, ok := s.(workspace.BlameStore)
	if !ok {
		return
	}

	ns, w, first, second := seed.ns, seed.w, seed.first, seed.second
	at := now().Add(3 * time.Hour)
	row := func(addr string, kind workspace.ResourceChangeKind, restored bool) workspace.ResourceChange {
		return workspace.ResourceChange{
			At:             at,
			Workspace:      w.ID,
			StateVersion:   workspace.StateVersionID(first),
			Address:        addr,
			Kind:           kind,
			By:             history.Actor{Subject: "alice", TokenID: "tok-9", JobID: "job-9"},
			Ordinal:        1,
			ChangedPaths:   3,
			Restored:       restored,
			Forced:         false,
			LineageChanged: false,
		}
	}

	readable := workspace.ItemBlame{
		Workspace:    w.ID,
		StateVersion: workspace.StateVersionID(first),
		Changes: []workspace.ResourceChange{
			row("module.net.aws_vpc.main", workspace.ResourceCreated, false),
			row("module.net.aws_subnet.a[0]", workspace.ResourceReplaced, true),
		},
		Ordinal:        1,
		ResourceCount:  3,
		Opaque:         false,
		LineageChanged: false,
	}
	opaque := workspace.ItemBlame{
		Workspace: w.ID, StateVersion: workspace.StateVersionID(second), Changes: nil,
		Ordinal: 2, ResourceCount: 0, Opaque: true, LineageChanged: true,
	}

	for _, b := range []workspace.ItemBlame{readable, opaque} {
		if err := blames.RecordResourceChanges(t.Context(), ns.Name, b, ns.Fence()); err != nil {
			t.Fatalf("record blame of %s: %v", w.Ref, err)
		}
	}
}

// checkBlameRowCounts checks that sum holds the rows that seedBlameRows wrote
// for one workspace, when the store keeps a blame: one cursor, two items and
// two resource changes.
func checkBlameRowCounts(t *testing.T, s BackupStore, sum backup.Summary) {
	t.Helper()

	if _, ok := s.(workspace.BlameStore); !ok {
		return
	}

	want := map[string]int64{"workspace_blame_cursors": 1, "workspace_blame_items": 2, "workspace_resource_changes": 2}
	for _, table := range blameTables() {
		if got := sum.RowCounts[table]; got != want[table] {
			t.Errorf("exported rows of %q = %d, want %d", table, got, want[table])
		}
	}
}

// checkRestoredBlame checks that dst holds the blame that seedBlameRows wrote
// for the workspace w, after a restore.
func checkRestoredBlame(t *testing.T, dst RestoreStore, seed blameSeed) {
	t.Helper()

	blames, ok := dst.(workspace.BlameStore)
	if !ok {
		return
	}

	w, first, second := seed.w, seed.first, seed.second
	ctx := t.Context()

	if cursor, err := blames.BlameCursor(ctx, w.ID); err != nil || cursor != 2 {
		t.Fatalf("restored blame cursor = %d, %v, want 2", cursor, err)
	}

	got, err := blames.ItemChanges(ctx, w.ID, workspace.StateVersionID(first))
	if err != nil || got.Ordinal != 1 || got.ResourceCount != 3 || len(got.Changes) != 2 {
		t.Fatalf("restored blame of the first state version = %+v, %v, want 2 rows and resource count 3", got, err)
	}

	checkRestoredRow(t, got.Changes[0])

	opaque, err := blames.ItemChanges(ctx, w.ID, workspace.StateVersionID(second))
	if err != nil || !opaque.Opaque || !opaque.LineageChanged || len(opaque.Changes) != 0 {
		t.Errorf("restored blame of the opaque state version = %+v, %v", opaque, err)
	}
}

// checkRestoredRow checks the first row that seedBlameRows wrote, after a
// restore.
func checkRestoredRow(t *testing.T, r workspace.ResourceChange) {
	t.Helper()

	if r.Address != "module.net.aws_subnet.a[0]" || !r.Restored || r.Kind != workspace.ResourceReplaced ||
		r.ChangedPaths != 3 || r.By.JobID != "job-9" || !r.At.Equal(now().Add(3*time.Hour)) {
		t.Errorf("restored row = %+v", r)
	}
}
