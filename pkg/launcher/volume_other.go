//go:build !linux

package launcher

import "fmt"

// prepareVolume refuses every directory: the launcher runs steps on Linux
// only, where it can read the type of a file system.
func prepareVolume(string) (string, error) {
	return "", fmt.Errorf("%w: the launcher checks a memory-backed volume on Linux only", ErrSandbox)
}
