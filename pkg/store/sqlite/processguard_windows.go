//go:build windows

package sqlite

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// errLocked reports that another open file holds the lock.
var errLocked = errors.New("file is locked")

// errorSharingViolation is the Windows error for a file that another handle
// opened without sharing.
const errorSharingViolation syscall.Errno = 32

// lockFile opens path with no sharing, so a second open fails until the
// first handle closes, in this process or in another one.
func lockFile(path string) (*os.File, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}

	handle, err := syscall.CreateFile(
		name,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		0,
		nil,
		syscall.OPEN_ALWAYS,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		if errors.Is(err, errorSharingViolation) {
			return nil, errLocked
		}

		return nil, fmt.Errorf("open %s: %w", path, err)
	}

	return os.NewFile(uintptr(handle), path), nil
}

// unlockFile does nothing: the handle has no sharing, and the close releases
// it.
func unlockFile(_ *os.File) error {
	return nil
}
