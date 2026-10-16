//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// procSelfFD lists the open file descriptors of the test process on Linux.
const procSelfFD = "/proc/self/fd"

// checkNoOwnSQLiteHandle fails tb when the test process holds the SQLite
// file at dbPath, or one of its -wal, -shm or -journal files, open. The
// binary server owns that file: a handle in the test process makes it a
// second writer. Other platforms have no /proc/self/fd, so the check skips
// there.
func checkNoOwnSQLiteHandle(tb testing.TB, dbPath string) {
	tb.Helper()

	if runtime.GOOS != "linux" {
		return
	}

	want := dbPath
	if dir, err := filepath.EvalSymlinks(filepath.Dir(dbPath)); err == nil {
		want = filepath.Join(dir, filepath.Base(dbPath))
	}

	fds, err := os.ReadDir(procSelfFD)
	if err != nil {
		tb.Fatalf("list open files of the test process: %v", err)
	}

	for _, fd := range fds {
		// A descriptor can close between the list and the read; skip it.
		target, readErr := os.Readlink(filepath.Join(procSelfFD, fd.Name()))
		if readErr != nil {
			continue
		}

		if target == want || strings.HasPrefix(target, want+"-") {
			tb.Errorf("the test process holds %s open while the server binary owns it", target)
		}
	}
}
