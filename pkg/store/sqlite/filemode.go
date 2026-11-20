package sqlite

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// ErrFileMode reports a database file, or one of its -wal and -shm files,
// that group or other users can access. The database holds every secret,
// token hash and state of the installation, so only the owner may read it.
// The store refuses such a file and changes nothing: the operator decides
// what to do with the mode.
var ErrFileMode = errors.New("database file is not private to the owner")

// widePolicy says what a check does with a file that group or other users can
// access: refuse it, or list it and go on.
type widePolicy bool

const (
	refuseWide widePolicy = false
	listWide   widePolicy = true
)

// WideFile is a database file that group or other users can access, with its
// permission bits.
type WideFile struct {
	// Path is the path of the file.
	Path string
	// Mode is the permission bits of the file.
	Mode os.FileMode
}

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
	_, err := inspectDatabasePath(path, refuseWide)

	return err
}

// CheckFileModeAllowWide checks the database file at path and its -wal and
// -shm files for an operator who accepted a wider mode. A file that group or
// other users can access passes, and the answer lists it with its mode, so
// the caller can log it. A file that is not a regular file still fails with
// an error that wraps ErrFileMode, and so does a symbolic link in place of a
// -wal or -shm file. It changes no file.
func CheckFileModeAllowWide(path string) ([]WideFile, error) {
	return inspectDatabasePath(path, listWide)
}

// inspectDatabasePath resolves path as Open does and inspects the three
// database files.
func inspectDatabasePath(path string, policy widePolicy) ([]WideFile, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}

	resolved, err := resolveDatabasePath(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}

	return inspectFiles(policy, databaseFiles(resolved)...)
}

// databaseFiles returns the database file and the two files that SQLite makes
// next to it.
func databaseFiles(path string) []string {
	return []string{path, path + walSuffix, path + shmSuffix}
}

// checkPrivateFiles checks each file in names. It follows no symbolic link at
// the last element, so a link in place of a file fails. A file that group or
// other users can access fails too.
//
// Limit: the check is path-based (Lstat), not fstat on the descriptor that
// SQLite opens, because database/sql gives no descriptor. A process that can
// write to the directory can change a file between the check and the open.
// The directory permissions are the layer that stops it.
func checkPrivateFiles(names ...string) error {
	_, err := inspectFiles(refuseWide, names...)

	return err
}

// inspectFiles checks each file in names. A file that is not a regular file
// always fails. A file that group or other users can access fails, unless
// policy is listWide: the answer then lists that file with its mode and the
// check goes on.
func inspectFiles(policy widePolicy, names ...string) ([]WideFile, error) {
	var wide []WideFile

	for _, name := range names {
		info, err := os.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}

		if err != nil {
			return nil, fmt.Errorf("check database file mode: %w", err)
		}

		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf(
				"%w: %q is not a regular file: replace it with the database file or remove it", ErrFileMode, name,
			)
		}

		perm := info.Mode().Perm()
		if perm&groupOtherBits == 0 {
			continue
		}

		if policy == listWide {
			wide = append(wide, WideFile{Path: name, Mode: perm})

			continue
		}

		return nil, fmt.Errorf(
			"%w: group or other users can access %q (mode %04o): run chmod 600 %s, "+
				"or change the mount permissions of the volume (Docker or Kubernetes) so that only the owner can access the file",
			ErrFileMode, name, perm, name,
		)
	}

	return wide, nil
}

// prepareDatabaseFile makes the database file with mode 0600 when it does not
// exist, and then checks the file and its sidecar files. It never widens a
// mode and never tightens one: an existing file with a wider mode fails with
// ErrFileMode. With listWide, an existing file with a wider mode passes and
// the answer lists it, so the caller can log it. The create, the exclusive
// create and the refusal of a file that is not regular do not change with
// policy. The create is exclusive, so it does not follow a link that
// another process puts at path after the check.
//
// The umask can only narrow the requested mode, so the file never has more
// than 0600.
func prepareDatabaseFile(path string, policy widePolicy) ([]WideFile, error) {
	_, err := os.Lstat(path)

	switch {
	case err == nil:
		// The file exists: the check below decides.
	case errors.Is(err, os.ErrNotExist):
		file, createErr := os.OpenFile(filepath.Clean(path), os.O_RDWR|os.O_CREATE|os.O_EXCL, databaseFileMode)
		if createErr != nil && !errors.Is(createErr, os.ErrExist) {
			return nil, fmt.Errorf("create database file: %w", createErr)
		}

		if createErr == nil {
			if closeErr := file.Close(); closeErr != nil {
				return nil, fmt.Errorf("create database file: %w", closeErr)
			}
		}
	default:
		return nil, fmt.Errorf("check database file: %w", err)
	}

	// Defense in depth: Open and openDatabase check the mode too, and the
	// startup check of the command does as well. This check stays so that
	// a new caller of this function cannot skip it, and so that a file that
	// another process made between the Lstat and the create is checked.
	return inspectFiles(policy, databaseFiles(path)...)
}

// warnWideFiles writes one warning for each file in wide, with its path and
// mode. Open calls it when the operator allowed a wider mode, so each start
// shows which files the server uses with a mode that is not private.
func (s *Store) warnWideFiles(ctx context.Context, wide []WideFile) {
	for _, file := range wide {
		s.logger.WarnContext(ctx,
			"database file allows group or other access: the server starts because the operator allowed this",
			slog.String("file", file.Path),
			slog.String("mode", fmt.Sprintf("%04o", file.Mode)),
		)
	}
}

// widePolicy answers what the checks of the store do with a wide file: the
// store lists it only when the operator allowed it.
func (s *Store) widePolicy() widePolicy {
	if s.allowWideFileMode {
		return listWide
	}

	return refuseWide
}
