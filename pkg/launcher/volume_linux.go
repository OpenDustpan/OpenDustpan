package launcher

import (
	"fmt"
	"os"
	"syscall"
)

// The types of the file systems that hold data in memory only.
const (
	tmpfsMagic = 0x01021994
	ramfsMagic = 0x858458f6
	// magicBits keeps the 32 bits of a magic number.
	magicBits = 0xffffffff
)

// prepareVolume checks that dir is on tmpfs or ramfs, and creates the
// directory of the launcher in it.
func prepareVolume(dir string) (string, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return "", fmt.Errorf("%w: read the file system of the secrets directory: %w", ErrSandbox, err)
	}

	if t := magicOf(st.Type); t != tmpfsMagic && t != ramfsMagic {
		return "", fmt.Errorf("%w: the secrets directory is not on tmpfs or ramfs", ErrSandbox)
	}

	return makeOwnDir(dir, os.Getuid())
}

// magicOf returns the 32 bits of the type of a file system. The type of the
// field depends on the architecture, and a 32-bit signed field holds a
// magic number above 2^31 as a negative value.
func magicOf[T int32 | int64 | uint32](t T) int64 {
	return int64(t) & magicBits
}
