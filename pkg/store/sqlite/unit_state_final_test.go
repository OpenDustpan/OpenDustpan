package sqlite_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// tofuFinalCase is one row of TestOpenTofuFinalVersion: the name of the case
// and its check.
type tofuFinalCase struct {
	name  string
	check func(t *testing.T, s *sqlite.Store)
}

// TestOpenTofuFinalVersion proves the final rule of the OpenTofu state: a lock
// and an unlock with no state write make no new final version, an allowed
// unlocked write is final at once, and a release after a locked write makes
// the version of the write final with the origin of the releasing request.
func TestOpenTofuFinalVersion(t *testing.T) {
	t.Parallel()

	tests := []tofuFinalCase{
		{"a lock and an unlock with no write keep the final version", checkLockUnlockKeepsFinal},
		{"an allowed unlocked write is final at once", checkUnlockedWriteIsFinal},
		{"a release after a write publishes it with the origin of the release", checkReleasePublishesWrite},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.check(t, open(t))
		})
	}
}

// checkLockUnlockKeepsFinal writes a state version final with one release,
// then locks and releases the workspace with no state write: the stored final
// version stays as it was, with its origin and its time.
func checkLockUnlockKeepsFinal(t *testing.T, s *sqlite.Store) {
	t.Helper()

	fence, unit := stateWriteUnit(t, s)
	unitStateTofuFinalize(t, s, fence, unit)
	unitStateTofuRelease(t, s, fence, unit)

	before, ok, err := s.Jobs().FinalVersion(t.Context(), fence.Namespace, unit.UnitID())
	if err != nil || !ok {
		t.Fatalf("final version before the empty release = %+v, %v, %v; want one", before, ok, err)
	}

	unitStateTofuLock(t, s, fence, unit)
	releaseStateWriteAs(t, s, fence, unit, identity.StateOrigin{Job: "", Commit: "release-2"})

	after, ok, err := s.Jobs().FinalVersion(t.Context(), fence.Namespace, unit.UnitID())
	if err != nil || !ok {
		t.Fatalf("final version after the empty release = %+v, %v, %v; want one", after, ok, err)
	}

	if after != before {
		t.Fatalf("final version after a release with no write = %+v, want %+v unchanged", after, before)
	}
}

// checkUnlockedWriteIsFinal finalizes a state version that the workspace
// allows without a lock: the write itself is final, with the empty origin of
// the write.
func checkUnlockedWriteIsFinal(t *testing.T, s *sqlite.Store) {
	t.Helper()

	fence, unit := stateWriteUnit(t, s)
	w := unlockedForWrites(t, s, fence, unit)
	sv := pendingStateWrite(t, w, "")

	if err := s.CreateStateVersion(t.Context(), sv, fence, stateWriteCap); err != nil {
		t.Fatalf("CreateStateVersion = %v", err)
	}

	finalizeStateWrite(t, s, sv, fence)

	got, ok, err := s.Jobs().FinalVersion(t.Context(), fence.Namespace, unit.UnitID())
	if err != nil || !ok {
		t.Fatalf("final version of an allowed unlocked write = %+v, %v, %v; want one", got, ok, err)
	}

	if got.VersionID != sv.ID.String() || got.Job != "" || got.Commit != "" {
		t.Fatalf("final version of an allowed unlocked write = %+v, want version %s with an empty origin",
			got, sv.ID)
	}
}

// checkReleasePublishesWrite finalizes a state version under the lock and
// releases the lock with an origin: the release records the version of the
// write as final, with the origin of the releasing request.
func checkReleasePublishesWrite(t *testing.T, s *sqlite.Store) {
	t.Helper()

	fence, unit := stateWriteUnit(t, s)
	w := unitStateTofuLock(t, s, fence, unit)
	sv := unitStateTofuPending(t, s, fence, w)

	finalizeStateWrite(t, s, sv, fence)

	origin := identity.StateOrigin{Job: "", Commit: "release-rc"}
	releaseStateWriteAs(t, s, fence, unit, origin)

	got, ok, err := s.Jobs().FinalVersion(t.Context(), fence.Namespace, unit.UnitID())
	if err != nil || !ok {
		t.Fatalf("final version after the release = %+v, %v, %v; want one", got, ok, err)
	}

	if got.VersionID != sv.ID.String() || got.Job != "" || got.Commit != origin.Commit {
		t.Fatalf("final version after the release = %+v, want version %s with commit %q",
			got, sv.ID, origin.Commit)
	}
}

// releaseStateWriteAs releases the lock of the workspace of unit with origin,
// as the request that releases the lock does.
func releaseStateWriteAs(
	t *testing.T, s *sqlite.Store, fence namespace.Fence, unit identity.Object, origin identity.StateOrigin,
) {
	t.Helper()

	w, err := s.GetWorkspaceByID(t.Context(), workspace.ID(unit.UnitID()))
	if err != nil {
		t.Fatalf("get workspace = %v", err)
	}

	at := time.UnixMilli(unitStateWriteAtMs + 3000).UTC()
	lock := w.Lock

	w.Lock = workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""}
	w.LastRelease = workspace.Release{
		At: at, LockID: lock.ID, Holder: lock.Holder, By: writeStateHolder, Kind: workspace.ReleaseNormal,
	}
	w.Origin = origin
	w.Revision++
	w.UpdatedAt = at

	if err = s.SaveWorkspace(t.Context(), w, fence); err != nil {
		t.Fatalf("release workspace = %v", err)
	}
}
