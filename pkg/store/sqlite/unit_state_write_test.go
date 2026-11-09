package sqlite_test

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
	"opendustpan.org/opendustpan/pkg/workspace"
)

const (
	// unitStateWriteAtMs is the time of the writes of these tests.
	unitStateWriteAtMs = int64(1_700_000_000_000)
	// unitStateAttempt is the attempt ID of the checkpoint and journal keys
	// of these tests.
	unitStateAttempt = "attempt0000"
)

// unitStateWriteCase is one row of TestUnitStateWritePoints: the name of the
// caller, the unit that it writes, and the two writes of its path. write makes
// the intermediate write, and final makes the write that releases the state
// of the unit.
type unitStateWriteCase struct {
	name  string
	unit  func(t *testing.T, s *sqlite.Store) (namespace.Fence, identity.Object)
	write func(t *testing.T, s *sqlite.Store, fence namespace.Fence, unit identity.Object) string
	final func(t *testing.T, s *sqlite.Store, fence namespace.Fence, unit identity.Object) string
}

// TestUnitStateWritePoints proves that each caller of a state write publishes
// the state version that the job store reads: the intermediate write changes
// the current version and leaves the final version as it was, and the final
// write records the new final version.
func TestUnitStateWritePoints(t *testing.T) {
	t.Parallel()

	tests := []unitStateWriteCase{
		{
			name: "OpenTofu finalize", unit: unitStateTofuUnit,
			write: unitStateTofuFinalize, final: unitStateTofuRelease,
		},
		{
			name: "OpenTofu restore", unit: unitStateTofuUnit,
			write: unitStateTofuRestore, final: unitStateTofuRelease,
		},
		{
			name: "OpenTofu rollback", unit: unitStateTofuUnit,
			write: unitStateTofuRollback, final: unitStateTofuRelease,
		},
		{
			name: "Pulumi PutCheckpoint", unit: unitStatePulumiUnit,
			write: unitStatePulumiCheckpoint, final: unitStatePulumiEnd,
		},
		{
			name: "Pulumi PutBuiltCheckpoint", unit: unitStatePulumiUnit,
			write: unitStatePulumiBuiltCheckpoint, final: unitStatePulumiEnd,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkUnitStateWritePoint(t, tc)
		})
	}
}

// checkUnitStateWritePoint runs the writes of tc and checks the version that
// the job store reads at each step.
func checkUnitStateWritePoint(t *testing.T, tc unitStateWriteCase) {
	t.Helper()

	s := open(t)

	fence, unit := tc.unit(t, s)
	ns, unitID := fence.Namespace, unit.UnitID()

	if got := unitStateVersion(t, s, ns, unitID); got != "" {
		t.Fatalf("state version before the intermediate write = %q, want none", got)
	}

	assertNoFinalVersion(t, s, ns, unitID)

	intermediate := tc.write(t, s, fence, unit)

	if got := unitStateVersion(t, s, ns, unitID); got != intermediate {
		t.Fatalf("state version after the intermediate write = %q, want %q", got, intermediate)
	}

	assertNoFinalVersion(t, s, ns, unitID)

	final := tc.final(t, s, fence, unit)

	if got := unitStateVersion(t, s, ns, unitID); got != final {
		t.Fatalf("state version after the final write = %q, want %q", got, final)
	}

	got, ok, err := s.Jobs().FinalVersion(t.Context(), ns, unitID)
	if err != nil || !ok {
		t.Fatalf("final version after the final write = %+v, %v, %v; want one", got, ok, err)
	}

	if got.VersionID != final {
		t.Fatalf("final version after the final write = %q, want %q", got.VersionID, final)
	}
}

// unitStateVersion returns the current state version of the unit, as the job
// store reads it.
func unitStateVersion(t *testing.T, s *sqlite.Store, ns namespace.Name, unitID string) string {
	t.Helper()

	version, err := s.UnitStateVersionForTest(t.Context(), ns, unitID)
	if err != nil {
		t.Fatalf("state version of unit %s = %v", unitID, err)
	}

	return version
}

// assertNoFinalVersion fails t when the job store holds a final state version
// of the unit.
func assertNoFinalVersion(t *testing.T, s *sqlite.Store, ns namespace.Name, unitID string) {
	t.Helper()

	got, ok, err := s.Jobs().FinalVersion(t.Context(), ns, unitID)
	if err != nil || ok {
		t.Fatalf("final version of unit %s = %+v, %v, %v; want none", unitID, got, ok, err)
	}
}

// unitStateTofuUnit creates the workspace network of the namespace acme.
func unitStateTofuUnit(t *testing.T, s *sqlite.Store) (namespace.Fence, identity.Object) {
	t.Helper()

	return stateWriteUnit(t, s)
}

// unitStateTofuFinalize finalizes a state version under the lock of the
// workspace, as an upload does before the release.
func unitStateTofuFinalize(
	t *testing.T, s *sqlite.Store, fence namespace.Fence, unit identity.Object,
) string {
	t.Helper()

	w := unitStateTofuLock(t, s, fence, unit)

	return finalizeStateWrite(t, s, unitStateTofuPending(t, s, fence, w), fence).ID.String()
}

// unitStateTofuRestore restores a finalized state version under the lock of
// the workspace, as the restore of a state version does.
func unitStateTofuRestore(
	t *testing.T, s *sqlite.Store, fence namespace.Fence, unit identity.Object,
) string {
	t.Helper()

	w := unitStateTofuLock(t, s, fence, unit)
	source := finalizeStateWrite(t, s, unitStateTofuPending(t, s, fence, w), fence)
	restored := unitStateTofuRestored(t, s, fence, w, source)

	return finalizeStateWrite(t, s, restored, fence).ID.String()
}

// unitStateTofuRollback restores a finalized state version and checks the
// current version in the finalize, as a rollback does.
func unitStateTofuRollback(
	t *testing.T, s *sqlite.Store, fence namespace.Fence, unit identity.Object,
) string {
	t.Helper()

	w := unitStateTofuLock(t, s, fence, unit)
	source := finalizeStateWrite(t, s, unitStateTofuPending(t, s, fence, w), fence)
	restored := unitStateTofuRestored(t, s, fence, w, source)

	return finalizeStateWriteExpecting(t, s, restored, fence, source.ID).ID.String()
}

// unitStateTofuRelease releases the lock of the workspace, which makes the
// current state version final.
func unitStateTofuRelease(
	t *testing.T, s *sqlite.Store, fence namespace.Fence, unit identity.Object,
) string {
	t.Helper()

	w, err := s.GetWorkspaceByID(t.Context(), workspace.ID(unit.UnitID()))
	if err != nil {
		t.Fatalf("get workspace = %v", err)
	}

	at := time.UnixMilli(unitStateWriteAtMs + 2000).UTC()
	lock := w.Lock

	w.Lock = workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""}
	w.LastRelease = workspace.Release{
		At: at, LockID: lock.ID, Holder: lock.Holder, By: writeStateHolder, Kind: workspace.ReleaseNormal,
	}
	w.Revision++
	w.UpdatedAt = at

	if err = s.SaveWorkspace(t.Context(), w, fence); err != nil {
		t.Fatalf("release workspace = %v", err)
	}

	current, err := s.CurrentStateVersion(t.Context(), w.ID)
	if err != nil {
		t.Fatalf("current state version = %v", err)
	}

	return current.ID.String()
}

// unitStateTofuLock locks the workspace of unit at the fixed clock.
func unitStateTofuLock(
	t *testing.T, s *sqlite.Store, fence namespace.Fence, unit identity.Object,
) workspace.Workspace {
	t.Helper()

	w, err := s.GetWorkspaceByID(t.Context(), workspace.ID(unit.UnitID()))
	if err != nil {
		t.Fatalf("get workspace = %v", err)
	}

	at := time.UnixMilli(unitStateWriteAtMs).UTC()

	w.Lock = workspace.Lock{LockedAt: at, ID: ident.NewID("lock-"), Holder: writeStateHolder, Reason: "state write"}
	w.Revision++
	w.UpdatedAt = at

	if err = s.SaveWorkspace(t.Context(), w, fence); err != nil {
		t.Fatalf("lock workspace = %v", err)
	}

	return w
}

// unitStateTofuPending declares a pending state version of the locked
// workspace w. The finalize of the version is not final: the release marks it.
func unitStateTofuPending(
	t *testing.T, s *sqlite.Store, fence namespace.Fence, w workspace.Workspace,
) workspace.StateVersion {
	t.Helper()

	sv := pendingStateWrite(t, w, "")
	sv.Holder = w.Lock.Holder
	sv.LockID = w.Lock.ID
	sv.UnlockedWrite = false

	if err := s.CreateStateVersion(t.Context(), sv, fence, stateWriteCap); err != nil {
		t.Fatalf("CreateStateVersion = %v", err)
	}

	return sv
}

// unitStateTofuRestored declares a pending version that restores source, as a
// restore builds it.
func unitStateTofuRestored(
	t *testing.T, s *sqlite.Store, fence namespace.Fence, w workspace.Workspace, source workspace.StateVersion,
) workspace.StateVersion {
	t.Helper()

	sv := unitStateTofuPending(t, s, fence, w)
	sv.RestoredFrom = source.ID
	sv.Force = true
	sv.Lineage = source.Lineage
	sv.Serial = source.Serial
	sv.MD5 = source.MD5

	return sv
}

// finalizeStateWriteExpecting records the attempt of sv and finalizes it with
// the check of the current version.
func finalizeStateWriteExpecting(
	t *testing.T, s *sqlite.Store, sv workspace.StateVersion, fence namespace.Fence,
	expect workspace.StateVersionID,
) workspace.StateVersion {
	t.Helper()

	ns, err := s.GetNamespace(t.Context(), fence.Namespace)
	if err != nil {
		t.Fatalf("GetNamespace = %v", err)
	}

	sv.Status = workspace.StatusFinalized
	sv.FinalizedAt = time.UnixMilli(unitStateWriteAtMs + 1000).UTC()
	sv.Content = blob.Key("namespaces/" + ns.ID.String() + "/tofu/" + sv.Workspace.String() + "/" +
		sv.ID.String() + "/a1")
	sv.Size = int64(len(stateWriteContent))

	if _, err := s.RecordUploadAttempt(t.Context(), sv.ID, sv.Content, fence); err != nil {
		t.Fatalf("RecordUploadAttempt = %v", err)
	}

	if err := s.FinalizeStateVersionExpecting(t.Context(), sv, fence, expect); err != nil {
		t.Fatalf("FinalizeStateVersionExpecting = %v", err)
	}

	return sv
}

// unitStatePulumiUnit creates the stack web/prod of the namespace acme.
func unitStatePulumiUnit(t *testing.T, s *sqlite.Store) (namespace.Fence, identity.Object) {
	t.Helper()

	d := &jobDomain{s: s}

	fence, err := d.CreateNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("CreateNamespace = %v", err)
	}

	unit, err := d.CreateUnit(t.Context(), fence, "pulumi", "web/prod")
	if err != nil {
		t.Fatalf("CreateUnit = %v", err)
	}

	return fence, unit
}

// unitStatePulumiCheckpoint saves a checkpoint of a running update, as the
// checkpoint route of the Pulumi CLI does.
func unitStatePulumiCheckpoint(
	t *testing.T, s *sqlite.Store, fence namespace.Fence, unit identity.Object,
) string {
	t.Helper()

	u := unitStatePulumiStart(t, s, fence, unit, 0)
	cp := unitStatePulumiCheckpointOf(t, s, u, 0, `{"v":1}`)

	if _, err := s.PutCheckpoint(t.Context(), cp, fence); err != nil {
		t.Fatalf("PutCheckpoint = %v", err)
	}

	return unitStateDigest(cp.Digest)
}

// unitStatePulumiBuiltCheckpoint builds the checkpoint of a journaled update,
// as the journal build does.
func unitStatePulumiBuiltCheckpoint(
	t *testing.T, s *sqlite.Store, fence namespace.Fence, unit identity.Object,
) string {
	t.Helper()

	u := unitStatePulumiStart(t, s, fence, unit, 1)

	if _, err := s.PutJournalRow(t.Context(), unitStatePulumiJournal(t, s, u), fence); err != nil {
		t.Fatalf("PutJournalRow = %v", err)
	}

	cp := unitStatePulumiCheckpointOf(t, s, u, 0, `{"v":1}`)

	if _, err := s.PutBuiltCheckpoint(t.Context(), cp, fence); err != nil {
		t.Fatalf("PutBuiltCheckpoint = %v", err)
	}

	return unitStateDigest(cp.Digest)
}

// unitStatePulumiEnd ends the running update of the stack, as the end of an
// update does, which makes the current checkpoint version final.
func unitStatePulumiEnd(
	t *testing.T, s *sqlite.Store, fence namespace.Fence, unit identity.Object,
) string {
	t.Helper()

	ref, err := pulumiRef(fence.Namespace, unit)
	if err != nil {
		t.Fatalf("pulumi reference = %v", err)
	}

	u, err := s.ActiveUpdate(t.Context(), ref)
	if err != nil {
		t.Fatalf("ActiveUpdate = %v", err)
	}

	u.Status = stack.StatusSucceeded
	u.EndedAt = u.StartedAt.Add(time.Minute)
	u.Lease = stack.Lease{ExpiresAt: time.Time{}, Digest: token.Digest{}}
	u.Revision++

	if err = s.SaveUpdate(t.Context(), u, fence); err != nil {
		t.Fatalf("SaveUpdate = %v", err)
	}

	cp, err := s.LatestCheckpoint(t.Context(), ref)
	if err != nil {
		t.Fatalf("LatestCheckpoint = %v", err)
	}

	return unitStateDigest(cp.Digest)
}

// unitStatePulumiStart creates and starts a mutating update of the stack of
// unit, with the journal version.
func unitStatePulumiStart(
	t *testing.T, s *sqlite.Store, fence namespace.Fence, unit identity.Object, journal int64,
) stack.Update {
	t.Helper()

	ref, err := pulumiRef(fence.Namespace, unit)
	if err != nil {
		t.Fatalf("pulumi reference = %v", err)
	}

	now, err := s.DatabaseNow(t.Context())
	if err != nil {
		t.Fatalf("DatabaseNow = %v", err)
	}

	tok, err := token.New()
	if err != nil {
		t.Fatalf("new lease token = %v", err)
	}

	u := stack.Update{
		CreatedAt: now, StartedAt: time.Time{}, EndedAt: time.Time{},
		Lease: stack.Lease{ExpiresAt: time.Time{}, Digest: token.Digest{}},
		ID:    stack.UpdateID(ident.NewID("upd-")), Kind: stack.KindUpdate, Status: stack.StatusNotStarted,
		Ref: ref, DryRun: false, Version: 0, Revision: 1, CreatedBy: "", TokenID: "", JobID: "",
		GitHead: "", RestoredFrom: 0, JournalVersion: journal, CheckpointBytes: 0,
	}

	if err = s.CreateUpdate(t.Context(), u, storetest.UnrecordedKey(), fence); err != nil {
		t.Fatalf("CreateUpdate = %v", err)
	}

	u.Status = stack.StatusRunning
	u.StartedAt = now
	u.Lease = stack.Lease{ExpiresAt: now.Add(stack.DefaultLease), Digest: tok.Digest()}
	u.Revision++

	started, err := s.StartUpdate(t.Context(), u, fence)
	if err != nil {
		t.Fatalf("StartUpdate = %v", err)
	}

	return started
}

// unitStatePulumiCheckpointOf returns a checkpoint of the running update u
// under the blob key of its write attempt.
func unitStatePulumiCheckpointOf(
	t *testing.T, s *sqlite.Store, u stack.Update, seq int64, deployment string,
) stack.Checkpoint {
	t.Helper()

	now, err := s.DatabaseNow(t.Context())
	if err != nil {
		t.Fatalf("DatabaseNow = %v", err)
	}

	st, err := s.GetStack(t.Context(), u.Ref)
	if err != nil {
		t.Fatalf("GetStack = %v", err)
	}

	ns, err := s.GetNamespace(t.Context(), u.Ref.Namespace)
	if err != nil {
		t.Fatalf("GetNamespace = %v", err)
	}

	key, err := blob.ParseKey("namespaces/" + ns.ID.String() + "/pulumi/" + st.ID.String() + "/" +
		u.ID.String() + "/" + strconv.FormatInt(seq, 10) + "-" + unitStateAttempt)
	if err != nil {
		t.Fatalf("parse checkpoint key = %v", err)
	}

	return stack.Checkpoint{
		NamespaceID: "", StackID: "",
		SavedAt: now, Features: []string{"deltaCheckpointUploadsV2", "verbatim"},
		Deployment: []byte(deployment), Update: u.ID, Ref: u.Ref, Lease: u.Lease.Digest,
		SchemaVersion: 3, Sequence: seq, Digest: sha256.Sum256([]byte(deployment)), Invalid: false,
		BlobKey: key, BlobSize: 1, BlobMD5: [16]byte{1}, AttemptStartedAt: now,
		Origin: identity.StateOrigin{Job: "", Commit: ""},
	}
}

// unitStatePulumiJournal returns one journal batch of the running update u.
func unitStatePulumiJournal(t *testing.T, s *sqlite.Store, u stack.Update) stack.JournalRow {
	t.Helper()

	now, err := s.DatabaseNow(t.Context())
	if err != nil {
		t.Fatalf("DatabaseNow = %v", err)
	}

	st, err := s.GetStack(t.Context(), u.Ref)
	if err != nil {
		t.Fatalf("GetStack = %v", err)
	}

	ns, err := s.GetNamespace(t.Context(), u.Ref.Namespace)
	if err != nil {
		t.Fatalf("GetNamespace = %v", err)
	}

	key, err := blob.ParseKey("namespaces/" + ns.ID.String() + "/pulumi/" + st.ID.String() + "/" +
		u.ID.String() + "/journal/1-" + unitStateAttempt)
	if err != nil {
		t.Fatalf("parse journal key = %v", err)
	}

	return stack.JournalRow{
		Ref: u.Ref, Update: u.ID, Lease: u.Lease.Digest, BlobKey: key,
		SHA256: [sha256.Size]byte{1}, MD5: [16]byte{1}, Size: 1,
		SequenceIDs: []int64{1}, OperationIDs: nil, HasRebuild: false,
		Verify:           func([]int64, bool) (bool, error) { return false, nil },
		AttemptStartedAt: now,
	}
}

// unitStateDigest returns the state version of a checkpoint digest.
func unitStateDigest(digest [sha256.Size]byte) string {
	return hex.EncodeToString(digest[:])
}
