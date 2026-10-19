package workspace_test

import (
	"bytes"
	"testing"

	"opendustpan.org/opendustpan/pkg/workspace"
)

// A force-unlock of a sealed state with outputs keeps the outputs readable:
// the fence seals them under its own version, so the current version, the
// version read by ID and the history each carry the pushed outputs.
func TestForceUnlockKeepsSealedOutputsReadable(t *testing.T) {
	t.Parallel()

	f := newSealFixture(t)

	content := `{"version":4,"lineage":"lin-old","serial":1,` +
		`"outputs":{"greeting":{"value":"out-fence","type":"string"}},` +
		`"resources":[{"type":"null_resource","name":"a"}]}`

	f.pushSealed(t, content, sealedOutputsDoc("out-fence"), 1)

	want := fenceOutputsBefore(t, f)

	lockForFence(t, f)
	fenceSealed(t, f)
	assertFenceOutputs(t, f, want)
}

// fenceOutputsBefore returns the plain outputs of the pushed state.
func fenceOutputsBefore(t *testing.T, f sealFixture) []byte {
	t.Helper()

	before, beforeErr := f.sealed.Current(t.Context(), f.ws.ID)
	if beforeErr != nil {
		t.Fatalf("current before the fence: %v", beforeErr)
	}

	if len(before.Outputs) == 0 {
		t.Fatal("pushed state holds no outputs, want the body outputs")
	}

	return append([]byte(nil), before.Outputs...)
}

// lockForFence holds the fixture workspace for the force-unlock.
func lockForFence(t *testing.T, f sealFixture) {
	t.Helper()

	if _, lockErr := f.sealed.Lock(t.Context(), f.ws.ID, "alice", "apply"); lockErr != nil {
		t.Fatalf("lock: %v", lockErr)
	}
}

// fenceSealed force-unlocks the fixture workspace.
func fenceSealed(t *testing.T, f sealFixture) {
	t.Helper()

	_, _, fenceErr := f.sealed.ForceUnlock(t.Context(), f.ws.ID, "admin", noOverrides(), nil, forceEntry())
	if fenceErr != nil {
		t.Fatalf("force unlock: %v", fenceErr)
	}
}

// assertFenceOutputs proves the fence version carries the pushed outputs
// through the current, by-ID and history reads.
func assertFenceOutputs(t *testing.T, f sealFixture, want []byte) {
	t.Helper()

	cur, curErr := f.sealed.Current(t.Context(), f.ws.ID)
	if curErr != nil {
		t.Fatalf("current: %v", curErr)
	}

	checkOutputsEqual(t, "current", cur.Outputs, want)

	byID, byIDErr := f.sealed.ReadState(t.Context(), cur.ID)
	if byIDErr != nil {
		t.Fatalf("read state: %v", byIDErr)
	}

	checkOutputsEqual(t, "state", byID.Outputs, want)
	checkHistoryOutputs(t, f, cur.ID, want)
}

// checkHistoryOutputs proves the history holds the fence with the outputs.
func checkHistoryOutputs(t *testing.T, f sealFixture, id workspace.StateVersionID, want []byte) {
	t.Helper()

	list, total, historyErr := f.sealed.History(t.Context(), f.ws.ID, workspace.Page{Limit: 10, Offset: 0})
	if historyErr != nil {
		t.Fatalf("history: %v", historyErr)
	}

	if total != 2 {
		t.Fatalf("history total = %d, want 2", total)
	}

	for i := range list {
		if list[i].ID == id {
			checkOutputsEqual(t, "history", list[i].Outputs, want)

			return
		}
	}

	t.Fatalf("history holds %d versions without the fence %s", len(list), id)
}

// checkOutputsEqual proves one read carries the pushed outputs.
func checkOutputsEqual(t *testing.T, what string, got, want []byte) {
	t.Helper()

	if !bytes.Equal(got, want) {
		t.Fatalf("%s outputs = %q, want %q", what, got, want)
	}
}
