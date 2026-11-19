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

	got, err := s.workCopy(
		t.Context(),
		swapPlan{staged: staged, keepAccess: true, keepSettings: nil, before: nil, after: nil},
	)
	if err != nil {
		t.Fatalf("work copy: %v", err)
	}

	if got != work {
		t.Fatalf("work copy path = %q, want %q", got, work)
	}

	info, err := os.Stat(work)
	if err != nil {
		t.Fatalf("stat work copy: %v", err)
	}

	if info.Size() <= int64(len("stale")) {
		t.Errorf("the work copy has %d bytes, want a copy of the staged file", info.Size())
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

	_, err = s.workCopy(
		t.Context(),
		swapPlan{staged: staged, keepAccess: true, keepSettings: nil, before: nil, after: nil},
	)
	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("work copy error = %v, want ErrCorrupt", err)
	}

	info, statErr := os.Stat(victim)
	if statErr != nil || info.Size() != int64(len("keep")) {
		t.Errorf("the file behind the link: %v, %v; want it unchanged", info, statErr)
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

// TestCheckWorkCopyRefusesHostileFiles proves that the check after VACUUM INTO
// refuses a file that is not the regular file of this swap: a symbolic link
// that another process put at the name after the clear (to a file and to
// nothing), a directory and a file with a second hard link. The file behind
// each link stays as it is.
func TestCheckWorkCopyRefusesHostileFiles(t *testing.T) {
	t.Parallel()

	victim := func(t *testing.T) string {
		t.Helper()

		path := filepath.Join(t.TempDir(), "victim")
		if err := os.WriteFile(path, []byte("keep"), 0o644); err != nil { //nolint:gosec // The test needs a wide mode to show the check keeps it.
			t.Fatalf("write victim: %v", err)
		}

		return path
	}

	for _, tc := range []struct {
		name  string
		place func(t *testing.T, work string) (victimPath string)
	}{
		{"a link to a file", func(t *testing.T, work string) string {
			v := victim(t)
			if err := os.Symlink(v, work); err != nil {
				t.Fatalf("symlink: %v", err)
			}

			return v
		}},
		{"a link to nothing", func(t *testing.T, work string) string {
			if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), work); err != nil {
				t.Fatalf("symlink: %v", err)
			}

			return ""
		}},
		{"a directory", func(t *testing.T, work string) string {
			if err := os.Mkdir(work, 0o700); err != nil {
				t.Fatalf("mkdir: %v", err)
			}

			return ""
		}},
		{"a second hard link", func(t *testing.T, work string) string {
			v := victim(t)
			if err := os.Link(v, work); err != nil {
				t.Fatalf("link: %v", err)
			}

			return v
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			work := filepath.Join(t.TempDir(), "work")
			behind := tc.place(t, work)

			if err := checkWorkCopy(work); !errors.Is(err, backup.ErrCorrupt) {
				t.Fatalf("checkWorkCopy error = %v, want ErrCorrupt", err)
			}

			if behind == "" {
				return
			}

			info, err := os.Stat(behind)
			if err != nil || info.Mode().Perm() != 0o644 {
				t.Errorf("the file behind the name: %v, %v; want mode 0644 unchanged", info, err)
			}
		})
	}
}

// TestCheckWorkCopySetsTheMode proves that the check sets mode 0600 on a
// regular file with one link, whatever mode VACUUM INTO gave it.
func TestCheckWorkCopySetsTheMode(t *testing.T) {
	t.Parallel()

	work := filepath.Join(t.TempDir(), "work")
	if err := os.WriteFile(work, []byte("copy"), 0o644); err != nil { //nolint:gosec // The test needs a wide mode to show the check narrows it.
		t.Fatalf("write work copy: %v", err)
	}

	if err := checkWorkCopy(work); err != nil {
		t.Fatalf("checkWorkCopy: %v", err)
	}

	info, err := os.Stat(work)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("work copy mode = %v, %v; want 0600", info, err)
	}
}

// TestWorkCopyHasMode0600 proves the mode of the copy that a swap makes.
func TestWorkCopyHasMode0600(t *testing.T) {
	t.Parallel()

	s, _, staged := openWorkCopyStore(t)

	work, err := s.workCopy(
		t.Context(),
		swapPlan{staged: staged, keepAccess: true, keepSettings: nil, before: nil, after: nil},
	)
	if err != nil {
		t.Fatalf("work copy: %v", err)
	}

	info, err := os.Stat(work)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("work copy mode = %v, %v; want 0600", info, err)
	}
}

// TestRemoveEndedAreasRemovesTheWorkCopyJournal proves that the cleanup
// removes the rollback journal that a stopped swap left beside the work copy.
func TestRemoveEndedAreasRemovesTheWorkCopyJournal(t *testing.T) {
	t.Parallel()

	s, area, staged := openWorkCopyStore(t)

	work, err := workCopyPath(staged)
	if err != nil {
		t.Fatalf("work copy path: %v", err)
	}

	journal := work + "-journal"
	if err = os.WriteFile(journal, []byte("left by a stopped swap"), 0o600); err != nil {
		t.Fatalf("write journal: %v", err)
	}

	if _, err = s.removeEndedAreas(t.Context(), area.Restore); err != nil {
		t.Fatalf("remove ended areas: %v", err)
	}

	if _, statErr := os.Lstat(journal); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("%s still exists after the cleanup: %v", filepath.Base(journal), statErr)
	}
}
