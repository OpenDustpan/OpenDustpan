//go:build unix

package sqlite

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// guardReleaseCase names one case of TestReleaseUnlocksTheGuardFile.
type guardReleaseCase struct {
	name      string
	duplicate bool
}

// TestReleaseUnlocksTheGuardFile proves that release unlocks the guard file
// before it closes it: a copy of the descriptor, which a forked child holds
// before its exec, does not keep the lock, so acquireGuard of the same
// database path succeeds after the release.
func TestReleaseUnlocksTheGuardFile(t *testing.T) {
	t.Parallel()

	cases := []guardReleaseCase{
		{name: "no copy", duplicate: false},
		{name: "descriptor copy", duplicate: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkReleaseUnlocks(t, tc)
		})
	}
}

// checkReleaseUnlocks releases a guard of a fresh database path, with a copy
// of the descriptor when the case asks for it, and fails unless the release
// closed the file and a new guard of the path follows.
func checkReleaseUnlocks(t *testing.T, tc guardReleaseCase) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	guard, err := acquireGuard(path)
	if err != nil {
		t.Fatalf("acquire guard: %v", err)
	}

	if tc.duplicate {
		fd, dupErr := syscall.Dup(int(guard.file.Fd()))
		if dupErr != nil {
			t.Fatalf("dup guard descriptor: %v", dupErr)
		}

		t.Cleanup(func() {
			if closeErr := syscall.Close(fd); closeErr != nil {
				t.Errorf("close descriptor copy: %v", closeErr)
			}
		})
	}

	file := guard.file

	if err = guard.release(); err != nil {
		t.Fatalf("release guard: %v", err)
	}

	// The release must close the file too, not only unlock it: a second
	// close reports that the file is already closed.
	if closeErr := file.Close(); !errors.Is(closeErr, os.ErrClosed) {
		t.Fatalf("close after release = %v, want os.ErrClosed", closeErr)
	}

	second, err := acquireGuard(path)
	if err != nil {
		t.Fatalf("acquire guard after release: %v", err)
	}

	t.Cleanup(func() {
		if releaseErr := second.release(); releaseErr != nil {
			t.Errorf("release second guard: %v", releaseErr)
		}
	})
}
