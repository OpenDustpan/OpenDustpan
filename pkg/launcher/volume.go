package launcher

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// ErrSandbox reports a place that is not safe to run in.
var ErrSandbox = errors.New("the place is not safe to run in")

// TmpfsVolume is the MemoryVolume of the launcher.
type TmpfsVolume struct{}

var _ MemoryVolume = TmpfsVolume{}

// Prepare checks that dir is on tmpfs or ramfs, creates in it a new
// directory with mode ModeDir that the user of the launcher owns, checks
// its mode and owner after the create, and returns its path. It returns an
// error that wraps ErrSandbox when a check fails.
func (TmpfsVolume) Prepare(dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("%w: the secrets directory is not an absolute path", ErrSandbox)
	}

	return prepareVolume(dir)
}

// makeOwnDir creates a new directory in dir and checks that it has the mode
// ModeDir and the owner uid.
func makeOwnDir(dir string, uid int) (string, error) {
	made, err := os.MkdirTemp(dir, "launcher-")
	if err != nil {
		return "", fmt.Errorf("%w: create the directory of the launcher: %w", ErrSandbox, err)
	}

	if err = checkOwnDir(made, uid); err != nil {
		if rmErr := os.Remove(made); rmErr != nil {
			return "", fmt.Errorf("%w; remove it: %w", err, rmErr)
		}

		return "", err
	}

	return made, nil
}

// checkOwnDir checks that p is a directory, not a link, with the mode
// ModeDir and the owner uid.
func checkOwnDir(p string, uid int) error {
	info, err := os.Lstat(p)
	if err != nil {
		return fmt.Errorf("%w: read the directory of the launcher: %w", ErrSandbox, err)
	}

	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm() != ModeDir || int64(st.Uid) != int64(uid) {
		return fmt.Errorf("%w: the directory of the launcher does not have the mode 0700 and the user of the launcher",
			ErrSandbox)
	}

	return nil
}
