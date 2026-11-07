package launcher_test

import (
	"errors"
	"os"
	"runtime"
	"testing"

	"opendustpan.org/opendustpan/pkg/launcher"
)

func TestTmpfsVolumeRefusesADisk(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, dir string }{
		{"relative", "secrets"},
		{"missing", "/no/such/directory"},
	}

	// A test directory on tmpfs would pass: the case runs only on a disk.
	if !onTmpfs(t, t.TempDir()) {
		cases = append(cases, struct{ name, dir string }{"disk", t.TempDir()})
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got, err := (launcher.TmpfsVolume{}).Prepare(tc.dir); !errors.Is(err, launcher.ErrSandbox) || got != "" {
				t.Fatalf("Prepare(%q) = %q, %v; want ErrSandbox", tc.dir, got, err)
			}
		})
	}
}

func TestTmpfsVolumeOnMemory(t *testing.T) {
	t.Parallel()

	const shm = "/dev/shm"

	if runtime.GOOS != "linux" || !onTmpfs(t, shm) {
		t.Skip("needs /dev/shm on tmpfs")
	}

	dir, err := (launcher.TmpfsVolume{}).Prepare(shm)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	t.Cleanup(func() {
		if rmErr := os.Remove(dir); rmErr != nil {
			t.Errorf("remove: %v", rmErr)
		}
	})

	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != launcher.ModeDir {
		t.Fatalf("directory = %v, %v; want mode 0700", info, err)
	}
}

// onTmpfs reports whether a directory made in dir passes Prepare.
func onTmpfs(t *testing.T, dir string) bool {
	t.Helper()

	got, err := (launcher.TmpfsVolume{}).Prepare(dir)
	if err != nil {
		return false
	}

	if err := os.Remove(got); err != nil {
		t.Errorf("remove: %v", err)
	}

	return true
}
