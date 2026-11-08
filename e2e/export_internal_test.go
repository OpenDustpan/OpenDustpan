//go:build e2e

package e2e

// SetBeforeLock sets the function that Install runs after it finds no record
// of a check and before it takes the lock of the entry.
func SetBeforeLock(f *Fetcher, fn func()) {
	f.beforeLock = fn
}

// CacheChecks returns the number of checks of cache entries that f did.
func CacheChecks(f *Fetcher) int64 {
	return f.checks.Load()
}
