//go:build !linux

package main

// harden does nothing: the launcher runs steps on Linux only, and the mode
// run refuses to start on another system (launcher.TmpfsVolume).
func harden() error {
	return nil
}
