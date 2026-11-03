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

// errDevOwnerUnknown names a path whose owner the platform does not tell.
var errDevOwnerUnknown = errors.New("the owner of the path cannot be read")

// checkDevOwner refuses a path that another user owns. The error names the
// path, its owner and the one command that helps. It also refuses a path
// whose owner it cannot read.
func checkDevOwner(path string, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		// Defense in depth: the callers do not rely on this check to refuse an
		// unknown owner. The other checks of devdir.go stay.
		return fmt.Errorf("%s: %w", path, errDevOwnerUnknown)
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
