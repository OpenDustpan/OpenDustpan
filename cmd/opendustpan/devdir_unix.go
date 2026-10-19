//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

// checkDevOwner refuses a path that another user owns. The error names the
// path, its owner and the one command that helps.
func checkDevOwner(path string, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}

	current := os.Getuid()
	if int(stat.Uid) != current {
		return fmt.Errorf(
			"%s: %w (user ID %d, not %d): run chown %d %s", path, errDevOwner, stat.Uid, current, current, path,
		)
	}

	return nil
}
