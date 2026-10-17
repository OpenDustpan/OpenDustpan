//go:build unix

package sqlite

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// errLocked reports that another open file holds the lock.
var errLocked = errors.New("file is locked")

// guardFileMode lets only the owner read and write the guard file.
const guardFileMode = 0o600

// lockFile opens path and takes an exclusive flock on it without waiting.
// A flock belongs to the open file, so two opens in one process conflict
// as well as two processes.
func lockFile(path string) (*os.File, error) {
	file, err := os.OpenFile(filepath.Clean(path), os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, guardFileMode)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}

	conn, err := file.SyscallConn()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("lock %s: %w", path, err), file.Close())
	}

	var lockErr error

	controlErr := conn.Control(func(fd uintptr) {
		lockErr = syscall.Flock(int(fd), syscall.LOCK_EX|syscall.LOCK_NB)
	})
	if controlErr != nil {
		return nil, errors.Join(fmt.Errorf("lock %s: %w", path, controlErr), file.Close())
	}

	if lockErr != nil {
		if errors.Is(lockErr, syscall.EWOULDBLOCK) {
			return nil, errors.Join(errLocked, file.Close())
		}

		return nil, errors.Join(fmt.Errorf("lock %s: %w", path, lockErr), file.Close())
	}

	return file, nil
}
