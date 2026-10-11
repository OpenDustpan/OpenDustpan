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
	"slices"
	"strings"

	"opendustpan.org/opendustpan/pkg/blob"
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

	// The link is durable only after the directory entry reaches the disk,
	// so sync the parent directory before removing the temporary name.
	if err := s.syncDir(path.Dir(key.String())); err != nil {
		return errors.Join(
			fmt.Errorf("put %s: %w", key, err),
			s.removeIfExists(key.String()),
			s.removeIfExists(tmp),
		)
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

// List implements blob.Store: the objects under prefix, ordered by key,
// with their modification times in UTC. A prefix that names no directory
// yields an empty list. The temporary directory never lists: in-flight
// uploads stay invisible until their link lands.
func (s *Store) List(ctx context.Context, prefix string) ([]blob.Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("list %s: %w", prefix, err)
	}

	dir := strings.TrimSuffix(prefix, "/")
	if dir == "" {
		dir = "."
	}

	info, err := s.root.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("list %s: %w", prefix, err)
	}

	if !info.IsDir() {
		// A prefix that names a file lists just it; anything else
		// yields an empty list.
		if key, keyErr := blob.ParseKey(dir); keyErr == nil {
			return []blob.Object{{ModifiedAt: info.ModTime().UTC(), Key: key}}, nil
		}

		return nil, nil
	}

	var out []blob.Object

	if err := s.listDir(dir, &out); err != nil {
		return nil, err
	}

	slices.SortFunc(out, func(a, b blob.Object) int {
		return strings.Compare(a.Key.String(), b.Key.String())
	})

	return out, nil
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

// listDir appends every object under dir to out.
func (s *Store) listDir(dir string, out *[]blob.Object) error {
	entries, err := s.readDirEntries(dir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if err := s.listEntry(dir, entry, out); err != nil {
			return err
		}
	}

	return nil
}

// readDirEntries returns the entries of dir.
func (s *Store) readDirEntries(dir string) ([]fs.DirEntry, error) {
	f, err := s.root.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", dir, err)
	}

	entries, err := f.ReadDir(-1)
	closeErr := f.Close()

	if err != nil {
		return nil, errors.Join(fmt.Errorf("list %s: %w", dir, err), closeErr)
	}

	if closeErr != nil {
		return nil, fmt.Errorf("list %s: %w", dir, closeErr)
	}

	return entries, nil
}

// listEntry appends entry to out, descending into directories. The
// temporary directory never lists: in-flight uploads stay invisible until
// their link lands.
func (s *Store) listEntry(dir string, entry fs.DirEntry, out *[]blob.Object) error {
	name := path.Join(dir, entry.Name())

	if entry.IsDir() {
		if name == tmpDir {
			return nil
		}

		return s.listDir(name, out)
	}

	key, keyErr := blob.ParseKey(name)
	if keyErr == nil {
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("list %s: %w", dir, err)
		}

		*out = append(*out, blob.Object{ModifiedAt: info.ModTime().UTC(), Key: key})
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

// syncDir fsyncs the directory dir, so a link just created in it survives a
// crash. dir is "." for the root itself.
func (s *Store) syncDir(dir string) error {
	f, err := s.root.Open(dir)
	if err != nil {
		return fmt.Errorf("open directory: %w", err)
	}

	syncErr := f.Sync()
	closeErr := f.Close()

	if syncErr != nil {
		return errors.Join(fmt.Errorf("sync directory: %w", syncErr), closeErr)
	}

	if closeErr != nil {
		return fmt.Errorf("close directory: %w", closeErr)
	}

	return nil
}

func tempName() (string, error) {
	b := make([]byte, tmpNameSize)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate temporary name: %w", err)
	}

	return path.Join(tmpDir, hex.EncodeToString(b)), nil
}
