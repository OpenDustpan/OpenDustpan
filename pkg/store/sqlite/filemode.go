package sqlite

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrFileMode reports a database file, or one of its -wal and -shm files,
// that group or other users can access. The database holds every secret,
// token hash and state of the installation, so only the owner may read it.
// The store refuses such a file and changes nothing: the operator decides
// what to do with the mode.
var ErrFileMode = errors.New("database file allows group or other access")

const (
	// databaseFileMode lets only the owner read and write a database file.
	databaseFileMode os.FileMode = 0o600

	// groupOtherBits are the permission bits that no database file may have.
	groupOtherBits os.FileMode = 0o077

	// walSuffix and shmSuffix name the files that SQLite makes next to the
	// database file in write-ahead log mode.
	walSuffix = "-wal"
	shmSuffix = "-shm"
)

// CheckFileMode checks the database file at path and its -wal and -shm files.
// A file that does not exist passes: the store makes it with mode 0600. A
// file that is not a regular file fails, and so does a file that group or
// other users can access, with an error that wraps ErrFileMode and names the
// command that fixes it. It changes no file. A symbolic link at path is
// followed, as Open does.
func CheckFileMode(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve database path: %w", err)
	}

	resolved, err := resolveDatabasePath(abs)
	if err != nil {
		return fmt.Errorf("resolve database path: %w", err)
	}

	return checkPrivateFiles(databaseFiles(resolved)...)
}

// databaseFiles returns the database file and the two files that SQLite makes
// next to it.
func databaseFiles(path string) []string {
	return []string{path, path + walSuffix, path + shmSuffix}
}

// checkPrivateFiles checks each file in names. It follows no symbolic link at
// the last element, so a link in place of a file fails.
func checkPrivateFiles(names ...string) error {
	for _, name := range names {
		info, err := os.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}

		if err != nil {
			return fmt.Errorf("check database file mode: %w", err)
		}

		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: %q is not a regular file", ErrFileMode, name)
		}

		if perm := info.Mode().Perm(); perm&groupOtherBits != 0 {
			return fmt.Errorf("%w %q (mode %04o): run chmod 600 on the file", ErrFileMode, name, perm)
		}
	}

	return nil
}

// prepareDatabaseFile makes the database file with mode 0600 when it does not
// exist, and then checks the file and its sidecar files. It never widens a
// mode and never tightens one: an existing file with a wider mode fails with
// ErrFileMode. The create is exclusive, so it does not follow a link that
// another process puts at path after the check.
//
// The umask can only narrow the requested mode, so the file never has more
// than 0600.
func prepareDatabaseFile(path string) error {
	_, err := os.Lstat(path)

	switch {
	case err == nil:
		// The file exists: the check below decides.
	case errors.Is(err, os.ErrNotExist):
		//nolint:gosec // The operator names the database path.
		file, createErr := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, databaseFileMode)
		if createErr != nil && !errors.Is(createErr, os.ErrExist) {
			return fmt.Errorf("create database file: %w", createErr)
		}

		if createErr == nil {
			if closeErr := file.Close(); closeErr != nil {
				return fmt.Errorf("create database file: %w", closeErr)
			}
		}
	default:
		return fmt.Errorf("check database file: %w", err)
	}

	// Defense in depth: Open and openDatabase check the mode too, and the
	// startup check of the command does as well. This check stays so that
	// a new caller of this function cannot skip it, and so that a file that
	// another process made between the Lstat and the create is checked.
	return checkPrivateFiles(databaseFiles(path)...)
}
