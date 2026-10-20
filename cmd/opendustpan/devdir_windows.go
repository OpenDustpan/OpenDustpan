//go:build windows

package main

import "os"

// checkDevOwner does nothing on Windows: the mode bits of a Windows file say
// nothing about its owner. The ACL check of dev on Windows is not done yet.
func checkDevOwner(string, os.FileInfo) error {
	return nil
}
