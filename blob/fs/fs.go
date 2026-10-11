// Package fs is a blob.Store backed by a local directory. It suits single-node
// installs; writes are atomic (temporary file plus rename) and every path is
// resolved through os.Root, so keys cannot escape the directory.
package fs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"

	"opendustpan.org/opendustpan/blob"
)

const (
	dirPerm     = 0o750
	filePerm    = 0o640
	tmpDir      = ".tmp"
	tmpNameSize = 16
)

// errPing reports that the blob directory does not answer. It holds no
// credential and no path, so callers can log it.
var errPing = errors.New("ping blob store: store not reachable")

// Store is a blob.Store rooted at a local directory.
type Store struct {
	root *os.Root
}

var _ blob.Store = (*Store)(nil)

// Open returns a Store rooted at dir, creating the directory if needed.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return nil, fmt.Errorf("create blob directory: %w", err)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open blob directory: %w", err)
	}

	if err := root.MkdirAll(tmpDir, dirPerm); err != nil {
		return nil, errors.Join(fmt.Errorf("create temporary directory: %w", err), root.Close())
	}

	return &Store{root: root}, nil
}

// Close releases the directory handle.
func (s *Store) Close() error {
	if err := s.root.Close(); err != nil {
		return fmt.Errorf("close blob directory: %w", err)
	}

	return nil
}

// Put writes r to a temporary file and links it to key. It returns
// blob.ErrExists when the key already holds an object.
func (s *Store) Put(ctx context.Context, key blob.Key, r io.Reader) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}

	if dir := path.Dir(key.String()); dir != "." {
		if err := s.root.MkdirAll(dir, dirPerm); err != nil {
			return fmt.Errorf("put %s: %w", key, err)
		}
	}

	tmp, err := tempName()
	if err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}

	if err := s.writeFile(tmp, r); err != nil {
		return errors.Join(fmt.Errorf("put %s: %w", key, err), s.removeIfExists(tmp))
	}

	if err := s.root.Link(tmp, key.String()); err != nil {
		cleanup := s.removeIfExists(tmp)
		if errors.Is(err, fs.ErrExist) {
			return errors.Join(fmt.Errorf("put %s: %w", key, blob.ErrExists), cleanup)
		}

		return errors.Join(fmt.Errorf("put %s: %w", key, err), cleanup)
	}

	if err := s.removeIfExists(tmp); err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}

	return nil
}

// Get opens the object under key.
func (s *Store) Get(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("get %s: %w", key, err)
	}

	f, err := s.root.Open(key.String())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("get %s: %w", key, blob.ErrNotFound)
	}

	if err != nil {
		return nil, fmt.Errorf("get %s: %w", key, err)
	}

	return f, nil
}

// Delete removes the object under key.
func (s *Store) Delete(ctx context.Context, key blob.Key) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("delete %s: %w", key, err)
	}

	if err := s.removeIfExists(key.String()); err != nil {
		return fmt.Errorf("delete %s: %w", key, err)
	}

	return nil
}

// Ping reports whether the directory exists and accepts writes. It writes a
// temporary file and removes it, so no file is left behind.
func (s *Store) Ping(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("ping blob store: %w", err)
	}

	name, err := tempName()
	if err != nil {
		return errPing
	}

	f, err := s.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePerm)
	if err != nil {
		return errPing
	}

	closeErr := f.Close()
	removeErr := s.removeIfExists(name)

	if closeErr != nil || removeErr != nil {
		return errPing
	}

	return nil
}

func (s *Store) writeFile(name string, r io.Reader) error {
	f, err := s.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePerm)
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}

	if _, err := io.Copy(f, r); err != nil {
		return errors.Join(fmt.Errorf("write temporary file: %w", err), f.Close())
	}

	if err := f.Sync(); err != nil {
		return errors.Join(fmt.Errorf("sync temporary file: %w", err), f.Close())
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}

	return nil
}

func (s *Store) removeIfExists(name string) error {
	err := s.root.Remove(name)
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	return fmt.Errorf("remove %s: %w", name, err)
}

func tempName() (string, error) {
	b := make([]byte, tmpNameSize)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate temporary name: %w", err)
	}

	return path.Join(tmpDir, hex.EncodeToString(b)), nil
}
