//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// errDevOwner names a path that another user owns.
var errDevOwner = errors.New("the path belongs to another user")

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

// devOneLink reports whether the file that info describes has one name
// only. It answers false when the platform does not tell it, so a caller
// fails closed.
func devOneLink(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)

	return ok && stat.Nlink == 1
}
