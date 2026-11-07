package main

import (
	"errors"
	"os"
	"strconv"
	"syscall"
)

// errNoFDList reports a system with no list of open descriptors.
var errNoFDList = errors.New("no directory lists the open descriptors")

// fdDirs returns the directories that list the open descriptors of the
// process: /proc/self/fd on Linux, /dev/fd on macOS.
func fdDirs() []string {
	return []string{"/proc/self/fd", "/dev/fd"}
}

// closeOnExec sets close-on-exec on each open descriptor from first up, so
// that a step gets only the descriptors that the launcher gives it. Go
// opens its own descriptors with close-on-exec, but a descriptor that the
// runner left open would else reach every step. A listed descriptor that
// is closed by now, such as the one of the directory read, is no error:
// the call changes nothing on it.
func closeOnExec(first int) error {
	for _, dir := range fdDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}

		for _, e := range entries {
			if fd, convErr := strconv.Atoi(e.Name()); convErr == nil && fd >= first {
				syscall.CloseOnExec(fd)
			}
		}

		return nil
	}

	return errNoFDList
}
