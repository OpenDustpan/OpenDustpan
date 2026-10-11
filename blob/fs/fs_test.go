package fs_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/blob"
	"opendustpan.org/opendustpan/blob/blobtest"
	"opendustpan.org/opendustpan/blob/fs"
)

func TestConformance(t *testing.T) {
	t.Parallel()

	blobtest.Run(t, func(t *testing.T) blob.Store {
		t.Helper()

		s, err := fs.Open(t.TempDir())
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		t.Cleanup(func() {
			if err := s.Close(); err != nil {
				t.Errorf("close store: %v", err)
			}
		})

		return s
	})
}

// TestPingLeavesNoFile proves Ping removes its probe file.
func TestPingLeavesNoFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	s, err := fs.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	if err := s.Ping(t.Context()); err != nil {
		t.Fatalf("ping: %v", err)
	}

	var files []string

	walkErr := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !entry.IsDir() {
			files = append(files, path)
		}

		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk blob directory: %v", walkErr)
	}

	if len(files) != 0 {
		t.Fatalf("ping left files behind: %v", files)
	}
}

// failReader returns data until failAfter bytes, then fails, as a reader the
// test closes early would.
type failReader struct {
	data      []byte
	failAfter int
	off       int
}

// errReadFailed is the failure failReader reports once it passes failAfter.
var errReadFailed = errors.New("simulated read failure")

func newFailReader(data []byte, failAfter int) *failReader {
	return &failReader{data: data, failAfter: failAfter, off: 0}
}

func (r *failReader) Read(p []byte) (int, error) {
	if r.off >= r.failAfter {
		return 0, errReadFailed
	}

	if r.off >= len(r.data) {
		return 0, io.EOF
	}

	n := copy(p, r.data[r.off:min(r.failAfter, len(r.data))])
	r.off += n

	return n, nil
}

// TestFailedPutLeavesNoFile proves a Put whose reader fails leaves no
// temporary file behind, leaves the key absent and lets a later Put succeed.
func TestFailedPutLeavesNoFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	s := openFSStore(t, dir)
	key := mustFSKey(t, "failed/put")
	data := []byte(strings.Repeat("partial-", 256))

	if err := s.Put(t.Context(), key, newFailReader(data, 16)); err == nil {
		t.Fatal("put with a failing reader = nil, want an error")
	}

	if _, err := s.Get(t.Context(), key); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("get after failed put: err = %v, want ErrNotFound", err)
	}

	if files := listDirFiles(t, dir); len(files) != 0 {
		t.Fatalf("failed put left files behind: %v", files)
	}

	putFSBytes(t, s, key, []byte("second"))

	if got := getFSBytes(t, s, key); string(got) != "second" {
		t.Fatalf("get after second put = %q, want %q", got, "second")
	}
}

func openFSStore(t *testing.T, dir string) *fs.Store {
	t.Helper()

	s, err := fs.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	return s
}

func mustFSKey(t *testing.T, name string) blob.Key {
	t.Helper()

	key, err := blob.ParseKey(name)
	if err != nil {
		t.Fatalf("parse key %q: %v", name, err)
	}

	return key
}

func listDirFiles(t *testing.T, dir string) []string {
	t.Helper()

	var files []string

	walkErr := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !entry.IsDir() {
			files = append(files, path)
		}

		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk blob directory: %v", walkErr)
	}

	return files
}

func putFSBytes(t *testing.T, s *fs.Store, key blob.Key, data []byte) {
	t.Helper()

	if err := s.Put(t.Context(), key, bytes.NewReader(data)); err != nil {
		t.Fatalf("put %s: %v", key, err)
	}
}

func getFSBytes(t *testing.T, s *fs.Store, key blob.Key) []byte {
	t.Helper()

	rc, err := s.Get(t.Context(), key)
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}

	data, err := io.ReadAll(rc)
	if closeErr := rc.Close(); closeErr != nil {
		t.Fatalf("close %s: %v", key, closeErr)
	}

	if err != nil {
		t.Fatalf("read %s: %v", key, err)
	}

	return data
}
