package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// KeyFileMode is the mode of a key file that a command makes: owner only.
const KeyFileMode = 0o600

// ErrKeyFileExists reports a new key file at a path where a file exists. The
// command never overwrites it.
var ErrKeyFileExists = errors.New("key file exists")

// WriteKeyContents writes contents to file. It is the write step of
// WriteKeyFile; tests give WriteKeyFile a step that fails.
func WriteKeyContents(file *os.File, contents string) error {
	if _, err := file.WriteString(contents); err != nil {
		return fmt.Errorf("write key file: %w", err)
	}

	return nil
}

// WriteKeyFile creates a new file at path with owner-only mode, writes
// contents with write, and syncs the file and its directory before it
// returns: a server must never wrap secrets with a key that a crash can
// lose. It never overwrites a file that exists, and it never follows a
// symbolic link at path: it returns ErrKeyFileExists. It does not make the
// directory of path. On any error after the create, it removes the file, so
// that no half-written key file stays for the next start to read.
func WriteKeyFile(path, contents string, write func(*os.File, string) error) error {
	// Defense in depth: key generate checks the form of the path too. This
	// check stays so that a new caller cannot write a key into a directory
	// with the path of the directory. It does not rely on that check.
	if namesDirectory(path) {
		return fmt.Errorf("%w: %q", ErrKeyOutDirectory, path)
	}

	// The root keeps every later step inside the directory of the key file,
	// also if a link in the name tries to leave it.
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("open key directory: %w", err)
	}

	writeErr := writeNewKeyFile(root, filepath.Base(path), contents, write)

	return errors.Join(writeErr, root.Close())
}

// writeNewKeyFile makes the key file name in root. It removes the file on an
// error after the create.
func writeNewKeyFile(root *os.Root, name, contents string, write func(*os.File, string) error) error {
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, KeyFileMode)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("%w: refusing to overwrite: %w", ErrKeyFileExists, err)
		}

		return fmt.Errorf("write key file: %w", err)
	}

	err = fillKeyFile(root, file, name, contents, write)
	if err == nil {
		err = syncRoot(root)
	}

	if err != nil {
		return errors.Join(err, root.Remove(name))
	}

	return nil
}

// fillKeyFile writes contents with write, then syncs, closes and protects
// the new key file. It closes file on every path. It returns the error of
// write as it is.
func fillKeyFile(root *os.Root, file *os.File, name, contents string, write func(*os.File, string) error) error {
	if err := write(file, contents); err != nil {
		return errors.Join(err, file.Close())
	}

	if err := file.Sync(); err != nil {
		return errors.Join(fmt.Errorf("sync key file: %w", err), file.Close())
	}

	if err := file.Close(); err != nil {
		return fmt.Errorf("write key file: %w", err)
	}

	if err := root.Chmod(name, KeyFileMode); err != nil {
		return fmt.Errorf("protect key file: %w", err)
	}

	return nil
}

// syncRoot syncs the directory of root, so that a new entry in it survives a
// crash.
func syncRoot(root *os.Root) error {
	handle, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("open key directory: %w", err)
	}

	if err := handle.Sync(); err != nil {
		return errors.Join(fmt.Errorf("sync key directory: %w", err), handle.Close())
	}

	if err := handle.Close(); err != nil {
		return fmt.Errorf("close key directory: %w", err)
	}

	return nil
}
