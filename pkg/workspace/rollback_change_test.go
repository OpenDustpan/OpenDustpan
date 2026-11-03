package workspace_test

import (
	"errors"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/workspace"
)

const (
	stateAB = `{"resources":[{"type":"t","name":"a"},{"type":"t","name":"b"}]}`
	stateA  = `{"resources":[{"type":"t","name":"a"}]}`
)

// pushStates pushes the contents in order and returns the versions.
func pushStates(t *testing.T, f fixture, contents ...string) []workspace.StateVersion {
	t.Helper()

	mustLock(t, f, "alice")

	out := make([]workspace.StateVersion, 0, len(contents))
	for i, c := range contents {
		out = append(out, pushVersion(t, f, int64(i+1), c))
	}

	mustUnlock(t, f)

	return out
}

// TestReadRollbackChange proves that the change comes from the two states: the
// count of resources in the restored state, and the addresses that only the
// current state holds.
func TestReadRollbackChange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		first, last string
		wantRes     int64
		wantRemoved []string
	}{
		{"a restore removes a resource", stateA, stateAB, 1, []string{"t.b"}},
		{"a restore adds a resource and removes none", stateAB, stateA, 2, []string{}},
		{"a restore of the same resources removes none", stateA, stateA, 1, []string{}},
		{"a restore to a state with no resource removes all", `{"resources":[]}`, stateAB, 0, []string{"t.a", "t.b"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			versions := pushStates(t, f, tc.first, tc.last)

			got, err := f.svc.ReadRollbackChange(t.Context(), f.ws.ID, versions[0].ID)
			if err != nil {
				t.Fatalf("ReadRollbackChange() = %v", err)
			}

			if got.Current != versions[1].ID || got.Resources != tc.wantRes || !slices.Equal(got.Removed, tc.wantRemoved) {
				t.Fatalf("change = %+v, want current %s resources %d removed %v",
					got, versions[1].ID, tc.wantRes, tc.wantRemoved)
			}
		})
	}
}

// TestReadRollbackChangeFailsClosed sends a state that the server cannot read
// to the read. Each one fails, and none gives a partial change.
func TestReadRollbackChangeFailsClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		first, last string
		want        error
	}{
		{"a resource entry that the server cannot read", `{"resources":[{"x":1}]}`, stateA, workspace.ErrInvalidState},
		{"a resource list of the wrong type", `{"resources":"x"}`, stateA, workspace.ErrInvalidState},
		{"a restored state that the client encrypted", `{"encrypted_data":"x"}`, stateA, workspace.ErrClientEncrypted},
		{"a current state that the client encrypted", stateA, `{"encrypted_data":"x"}`, workspace.ErrClientEncrypted},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			versions := pushStates(t, f, tc.first, tc.last)

			got, err := f.svc.ReadRollbackChange(t.Context(), f.ws.ID, versions[0].ID)
			if !errors.Is(err, tc.want) {
				t.Fatalf("ReadRollbackChange() = %+v, %v, want %v", got, err, tc.want)
			}

			if got.Current != "" || got.Resources != 0 || got.Removed != nil {
				t.Fatalf("change = %+v, want the zero value on an error", got)
			}
		})
	}
}

// TestReadRollbackChangeRefusesOtherWorkspaceAndMissingState proves the read
// never answers for a version of another workspace, and that a workspace with
// no current state fails.
func TestReadRollbackChangeRefusesOtherWorkspaceAndMissingState(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	versions := pushStates(t, f, stateA, stateAB)

	name, err := workspace.ParseName("other")
	if err != nil {
		t.Fatal(err)
	}

	other, err := f.svc.Create(t.Context(), workspace.Ref{Namespace: "acme", Name: name})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err = f.svc.ReadRollbackChange(t.Context(), other.ID, versions[0].ID); !errors.Is(
		err, workspace.ErrStateVersionNotFound,
	) {
		t.Fatalf("ReadRollbackChange() of another workspace = %v, want ErrStateVersionNotFound", err)
	}

	if _, err = f.svc.ReadRollbackChange(t.Context(), f.ws.ID, "sv-unknown"); err == nil {
		t.Fatal("ReadRollbackChange() of an unknown version = nil, want an error")
	}
}

// TestRestoreExpecting proves a restore writes only if the current version is
// the one the caller named, and that a mismatch writes nothing.
func TestRestoreExpecting(t *testing.T) {
	t.Parallel()

	t.Run("the expected current version restores", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		versions := pushStates(t, f, stateA, stateAB)

		restored, err := f.svc.RestoreExpecting(t.Context(), f.ws.ID, versions[0].ID, "alice", versions[1].ID)
		if err != nil {
			t.Fatalf("RestoreExpecting() = %v", err)
		}

		checkCurrentID(t, f, restored.ID)
		checkUnlocked(t, f)
	})

	t.Run("another current version writes nothing and releases the lock", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		versions := pushStates(t, f, stateA, stateAB, stateA)

		// The decision read versions[1] as current; versions[2] became current after.
		_, err := f.svc.RestoreExpecting(t.Context(), f.ws.ID, versions[0].ID, "alice", versions[1].ID)
		if !errors.Is(err, workspace.ErrConcurrentModification) {
			t.Fatalf("RestoreExpecting() = %v, want ErrConcurrentModification", err)
		}

		checkCurrentID(t, f, versions[2].ID)
		checkUnlocked(t, f)
	})

	t.Run("an empty expected version is refused", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		versions := pushStates(t, f, stateA, stateAB)

		if _, err := f.svc.RestoreExpecting(t.Context(), f.ws.ID, versions[0].ID, "alice", ""); !errors.Is(
			err, workspace.ErrInvalidState,
		) {
			t.Fatalf("RestoreExpecting() = %v, want ErrInvalidState", err)
		}

		checkCurrentID(t, f, versions[1].ID)
	})
}
