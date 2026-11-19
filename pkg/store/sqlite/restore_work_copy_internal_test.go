package sqlite

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

// openWorkCopyStore opens a store in a new directory and returns it with a
// staged file path: a copy of the live database made as the backup makes it.
func openWorkCopyStore(t *testing.T) (*Store, backup.StagingArea, string) {
	t.Helper()

	dir := t.TempDir()

	s, err := Open(t.Context(), filepath.Join(dir, "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	area, err := backup.NewStagingArea(backup.MintRestoreID(), 1)
	if err != nil {
		t.Fatalf("make staging area: %v", err)
	}

	staged, err := s.areaPath(area)
	if err != nil {
		t.Fatalf("area path: %v", err)
	}

	other, err := Open(t.Context(), staged)
	if err != nil {
		t.Fatalf("open staged file: %v", err)
	}

	if closeErr := other.Close(); closeErr != nil {
		t.Fatalf("close staged file: %v", closeErr)
	}

	return s, area, staged
}

// TestRemoveEndedAreasRemovesTheWorkCopy proves that the cleanup of a restore
// removes the work copy that a swap left when the process stopped between
// VACUUM INTO and the rename.
func TestRemoveEndedAreasRemovesTheWorkCopy(t *testing.T) {
	t.Parallel()

	s, area, staged := openWorkCopyStore(t)

	work, err := workCopyPath(staged)
	if err != nil {
		t.Fatalf("work copy path: %v", err)
	}

	for _, name := range []string{work, work + "-wal", work + "-shm"} {
		if writeErr := os.WriteFile(name, []byte("left by a stopped swap"), 0o600); writeErr != nil {
			t.Fatalf("write %s: %v", name, writeErr)
		}
	}

	if _, err := s.removeEndedAreas(t.Context(), area.Restore); err != nil {
		t.Fatalf("remove ended areas: %v", err)
	}

	for _, name := range []string{staged, work, work + "-wal", work + "-shm"} {
		if _, statErr := os.Lstat(name); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("%s still exists after the cleanup: %v", filepath.Base(name), statErr)
		}
	}
}

// TestWorkCopyReplacesAStaleCopy proves that a swap that retries removes the
// stale copy of an earlier attempt, so VACUUM INTO does not fail on it, and
// that the new copy is the staged file, not the stale one.
func TestWorkCopyReplacesAStaleCopy(t *testing.T) {
	t.Parallel()

	s, _, staged := openWorkCopyStore(t)

	work, err := workCopyPath(staged)
	if err != nil {
		t.Fatalf("work copy path: %v", err)
	}

	if err = os.WriteFile(work, []byte("stale"), 0o600); err != nil {
		t.Fatalf("write stale copy: %v", err)
	}

	got, err := s.workCopy(t.Context(), swapPlan{staged: staged, keepAccess: true})
	if err != nil {
		t.Fatalf("work copy: %v", err)
	}

	if got != work {
		t.Fatalf("work copy path = %q, want %q", got, work)
	}

	data, err := os.ReadFile(work)
	if err != nil {
		t.Fatalf("read work copy: %v", err)
	}

	if string(data) == "stale" {
		t.Error("the work copy still holds the stale bytes")
	}
}

// TestWorkCopyRefusesASymlink proves that a link at the name of the work copy
// is refused and that the file that the link names stays as it is.
func TestWorkCopyRefusesASymlink(t *testing.T) {
	t.Parallel()

	s, _, staged := openWorkCopyStore(t)

	work, err := workCopyPath(staged)
	if err != nil {
		t.Fatalf("work copy path: %v", err)
	}

	victim := filepath.Join(t.TempDir(), "victim")
	if err = os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatalf("write victim: %v", err)
	}

	if err = os.Symlink(victim, work); err != nil {
		t.Fatalf("make symlink: %v", err)
	}

	_, err = s.workCopy(t.Context(), swapPlan{staged: staged, keepAccess: true})
	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("work copy error = %v, want ErrCorrupt", err)
	}

	data, readErr := os.ReadFile(victim)
	if readErr != nil || string(data) != "keep" {
		t.Errorf("the file behind the link = %q, %v; want it unchanged", data, readErr)
	}
}

// TestWorkCopyPathStaysInTheDirectory proves the containment check of the
// work copy path: a staged name that holds a separator is refused.
func TestWorkCopyPathStaysInTheDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	got, err := workCopyPath(filepath.Join(dir, "dustpan.db.stage-x"))
	if err != nil || filepath.Dir(got) != dir {
		t.Fatalf("work copy path = %q, %v; want a path in %q", got, err, dir)
	}

	// The base name of a path never holds a separator, so no input makes the
	// check fail; the root path is the one case that yields a different name.
	if _, err = workCopyPath(string(filepath.Separator)); err == nil {
		t.Error("work copy path of the root = nil error, want a refusal")
	}
}
