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

// ListPage implements blob.Store: one page of the objects under the prefix,
// ordered by key, with their modification times in UTC. A prefix that names
// no directory yields an empty page. The temporary directory never lists:
// in-flight uploads stay invisible until their link lands.
//
// The adapter walks the tree in the byte order of the keys and stops after
// Limit objects past After. A directory is read and sorted as a whole, but a
// directory that sorts wholly before After is not read, and no directory
// after the page is read. It stats only the files that it lists.
func (s *Store) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	if err := req.Validate(); err != nil {
		return blob.Page{}, fmt.Errorf("list %s: %w", req.Prefix, err)
	}

	if err := ctx.Err(); err != nil {
		return blob.Page{}, fmt.Errorf("list %s: %w", req.Prefix, err)
	}

	dir := strings.TrimSuffix(req.Prefix, "/")
	if dir == "" {
		dir = "."
	}

	info, err := s.root.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return blob.Page{Objects: nil, Next: ""}, nil
	}

	if err != nil {
		return blob.Page{}, fmt.Errorf("list %s: %w", req.Prefix, err)
	}

	if !info.IsDir() {
		// A prefix that names a file lists just it; anything else
		// yields an empty page.
		if key, keyErr := blob.ParseKey(dir); keyErr == nil {
			one := []blob.Object{{ModifiedAt: info.ModTime().UTC(), Key: key}}

			return blob.PageOf(one, req), nil
		}

		return blob.Page{Objects: nil, Next: ""}, nil
	}

	// One object more than the limit shows whether the store holds more.
	w := &pageWalk{after: req.After.String(), max: req.Limit + 1, out: nil}

	if err := s.descend(ctx, dir, w); err != nil {
		return blob.Page{}, err
	}

	page := blob.Page{Objects: w.out, Next: ""}

	if len(w.out) > req.Limit {
		page.Objects = w.out[:req.Limit]
		page.Next = w.out[req.Limit-1].Key
	}

	return page, nil
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

// pageWalk is the state of one ListPage: the key to start after, the count of
// objects at which the walk stops, and the objects so far.
type pageWalk struct {
	after string
	out   []blob.Object
	max   int
}

// full reports whether the walk holds all the objects that it needs.
func (w *pageWalk) full() bool { return len(w.out) >= w.max }

// descend adds the objects under dir to w in the byte order of their keys,
// and stops when w is full. The entries of a directory sort by the key text:
// a file by its name and a directory by its name and a slash, so "a-c" comes
// before "a/b".
func (s *Store) descend(ctx context.Context, dir string, w *pageWalk) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("list %s: %w", dir, err)
	}

	entries, err := s.readDirEntries(dir)
	if err != nil {
		return err
	}

	slices.SortFunc(entries, func(a, b fs.DirEntry) int {
		return strings.Compare(entrySortName(a), entrySortName(b))
	})

	for _, entry := range entries {
		if w.full() {
			return nil
		}

		if err := s.listEntry(ctx, dir, entry, w); err != nil {
			return err
		}
	}

	return nil
}

// entrySortName returns the text that orders entry among the entries of its
// directory: the keys below a directory all start with its name and a slash.
func entrySortName(entry fs.DirEntry) string {
	if entry.IsDir() {
		return entry.Name() + "/"
	}

	return entry.Name()
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

// listEntry adds entry to w, descending into directories. A directory whose
// keys all sort at or before the key to start after is not read. The
// temporary directory never lists: in-flight uploads stay invisible until
// their link lands.
func (s *Store) listEntry(ctx context.Context, dir string, entry fs.DirEntry, w *pageWalk) error {
	name := path.Join(dir, entry.Name())

	if entry.IsDir() {
		if name == tmpDir {
			return nil
		}

		sub := name + "/"
		if w.after != "" && sub < w.after && !strings.HasPrefix(w.after, sub) {
			return nil
		}

		return s.descend(ctx, name, w)
	}

	if w.after != "" && name <= w.after {
		return nil
	}

	key, keyErr := blob.ParseKey(name)
	if keyErr == nil {
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("list %s: %w", dir, err)
		}

		w.out = append(w.out, blob.Object{ModifiedAt: info.ModTime().UTC(), Key: key})
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
