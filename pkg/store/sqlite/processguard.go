package sqlite

import (
	"errors"
	"fmt"
	"os"
)

// guardSuffix names the guard file next to the database file. SQLite names
// its own sidecar files the same way: "-wal", "-shm".
const guardSuffix = "-lock"

// processGuard holds the guard file of one open store. The operating system
// releases the lock when the file closes or the process ends, so a crash
// leaves no stale lock.
type processGuard struct {
	file *os.File
}

// acquireGuard takes the lock on the guard file of the database at abs. It
// returns ErrInUse when another store holds the lock, in this process or in
// another one.
func acquireGuard(abs string) (*processGuard, error) {
	path := abs + guardSuffix

	file, err := lockFile(path)
	if err != nil {
		if errors.Is(err, errLocked) {
			return nil, fmt.Errorf("%w: %s: stop the other server that uses this file", ErrInUse, abs)
		}

		return nil, fmt.Errorf("lock database file %s: %w", abs, err)
	}

	return &processGuard{file: file}, nil
}

// release closes the guard file, which releases the lock. The file stays on
// disk, so a later store locks the same file.
func (g *processGuard) release() error {
	if g == nil || g.file == nil {
		return nil
	}

	err := g.file.Close()
	g.file = nil

	if err != nil {
		return fmt.Errorf("release database lock: %w", err)
	}

	return nil
}
