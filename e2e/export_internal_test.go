//go:build e2e

package e2e

import "maps"

// SetBeforeLock sets the function that Install runs after it finds no record
// of a check and before it takes the lock of the entry.
func SetBeforeLock(f *Fetcher, fn func()) {
	f.beforeLock = fn
}

// CacheChecks returns the number of checks of cache entries that f did.
func CacheChecks(f *Fetcher) int64 {
	return f.checks.Load()
}

// SetPinnedSum replaces the pinned checksum of archive in tool, so a test
// can serve a fake archive under the real archive name.
func SetPinnedSum(tool *Tool, archive, sum string) {
	tool.pinned[archive] = sum
}

// PinnedSums returns a copy of the pinned checksums of tool, keyed by archive
// name, so a test can check each pinned platform and the pinned version
// without changing the map.
func PinnedSums(tool Tool) map[string]string {
	return maps.Clone(tool.pinned)
}
