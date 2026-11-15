package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/blob"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
)

// TestRegistryFileNamespacesRefusesHostileSectionKeys sends a hostile section
// key straight to the check of the section keys, as if the manifest checks
// before it had passed. The manifest is not signed, so each key is untrusted.
func TestRegistryFileNamespacesRefusesHostileSectionKeys(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"", "a/b", "ns-a/../ns-b", "ns-a\nlevel=ERROR", "ns-a\r", "not-a-namespace", "ns-"} {
		t.Run(strings.NewReplacer("/", "_", "\n", "LF", "\r", "CR").Replace(key), func(t *testing.T) {
			t.Parallel()

			var manifest Manifest

			manifest.RegistryFiles = map[string]RegistryFileSection{
				key: {Version: RegistryFileSectionVersion, Files: nil},
			}

			if _, err := registryFileNamespaces(manifest); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("registryFileNamespaces(%q) = %v, want ErrCorrupt", key, err)
			}
		})
	}
}

// TestRegistryFileBlobKeyRefusesHostileNamespaceIDs sends a hostile namespace
// ID straight to the key builder, as if the callers had not checked it.
func TestRegistryFileBlobKeyRefusesHostileNamespaceIDs(t *testing.T) {
	t.Parallel()

	hash := strings.Repeat("a", 64)

	for _, id := range []string{"", "a/b", "ns-a/../ns-b", "ns-a\nx", "ns-a\r", "x"} {
		if _, err := RegistryFileBlobKey(id, hash); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("RegistryFileBlobKey(%q) = %v, want ErrCorrupt", id, err)
		}
	}
}

// countingRegistryStore is a RegistryFileStore that counts every call.
type countingRegistryStore struct {
	calls int
}

func (c *countingRegistryStore) Has(context.Context, string) (bool, error) {
	c.calls++

	return false, nil
}

func (c *countingRegistryStore) Read(context.Context, string) ([]byte, error) {
	c.calls++

	return nil, nil
}

func (c *countingRegistryStore) Write(context.Context, string, []byte) error {
	c.calls++

	return nil
}

func (c *countingRegistryStore) Remove(context.Context, string) error {
	c.calls++

	return nil
}

// TestRestoreRegistryFilesRefusesHostileNamespaceIDs sends a hostile target
// namespace ID straight to the exported restore. It writes nothing.
func TestRestoreRegistryFilesRefusesHostileNamespaceIDs(t *testing.T) {
	t.Parallel()

	section := &RegistryFileSection{Version: RegistryFileSectionVersion, Files: nil}

	for _, id := range []string{"", "a/b", "ns-a/../ns-b", "ns-a\nx"} {
		store := &countingRegistryStore{calls: 0}

		warning, err := RestoreRegistryFiles(t.Context(), section, nil, id, store)
		if !errors.Is(err, ErrCorrupt) || warning.Code != "" || store.calls != 0 {
			t.Fatalf("RestoreRegistryFiles(%q) = %v, %v, %d store calls, want ErrCorrupt, no warning, 0 calls",
				id, warning, err, store.calls)
		}
	}
}

// TestRegistryFileCollectorRefusesHostileNamespaceIDs checks the collector.
func TestRegistryFileCollectorRefusesHostileNamespaceIDs(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"", "a/b", "ns-a\nx"} {
		if _, err := NewRegistryFileCollector(id); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("NewRegistryFileCollector(%q) = %v, want ErrCorrupt", id, err)
		}
	}
}

// TestRegistryFileCollectorRefusesAboveTheLimits checks the limits at backup:
// a file above the per-file limit, a negative size, and a total above the
// limit of a namespace each stop the collector with an error that names the
// release and the version.
func TestRegistryFileCollectorRefusesAboveTheLimits(t *testing.T) {
	t.Parallel()

	hashA, hashC := strings.Repeat("a", 64), strings.Repeat("c", 64)

	newCollector := func(t *testing.T) *RegistryFileCollector {
		t.Helper()

		c, err := NewRegistryFileCollector("ns-abc")
		if err != nil {
			t.Fatalf("NewRegistryFileCollector = %v", err)
		}

		return c
	}

	t.Run("one file above the limit", func(t *testing.T) {
		t.Parallel()

		err := newCollector(t).AddRelease("aws", "1.0.0", []string{hashA}, []int64{MaxRegistryFileBytes + 1})
		if !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), `"aws"`) {
			t.Fatalf("AddRelease = %v, want ErrCorrupt that names the release", err)
		}
	})

	t.Run("a negative size", func(t *testing.T) {
		t.Parallel()

		err := newCollector(t).AddRelease("aws", "1.0.0", []string{hashA}, []int64{-1})
		if !errors.Is(err, ErrCorrupt) {
			t.Fatalf("AddRelease = %v, want ErrCorrupt", err)
		}
	})

	t.Run("the total above the limit", func(t *testing.T) {
		t.Parallel()

		c := newCollector(t)

		var hashes []string

		var sizes []int64

		for left, i := MaxRegistryFilesTotalBytes, 0; left > 0; left, i = left-MaxRegistryFileBytes, i+1 {
			hashes = append(hashes, fileSHA([]byte{byte(i)}))
			sizes = append(sizes, MaxRegistryFileBytes)
		}

		if err := c.AddRelease("aws", "1.0.0", hashes, sizes); err != nil {
			t.Fatalf("AddRelease up to the limit = %v, want nil", err)
		}

		if err := c.AddRelease("gcp", "1.0.0", []string{hashC}, []int64{1}); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("AddRelease above the total = %v, want ErrCorrupt", err)
		}
	})
}

// TestReadRegistryFilesRefusesHostileSizes checks the limits at restore: the
// read refuses a size above the limit before it reads, a file that is longer
// than the size of the manifest, and a file that is shorter.
func TestReadRegistryFilesRefusesHostileSizes(t *testing.T) {
	t.Parallel()

	content := []byte("registry-file-content")
	hash := fileSHA(content)

	store, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open = %v", err)
	}

	key, err := blob.ParseKey("blobs/namespaces/ns-abc/registry/sha256/" + hash)
	if err != nil {
		t.Fatalf("ParseKey = %v", err)
	}

	if err := store.Put(t.Context(), key, bytes.NewReader(content)); err != nil {
		t.Fatalf("Put = %v", err)
	}

	svc := keyService(nil)
	svc.target = store

	for _, tc := range []struct {
		name string
		size int64
	}{
		{"size above the file limit", MaxRegistryFileBytes + 1},
		{"negative size", -1},
		{"size shorter than the file", int64(len(content)) - 1},
		{"size longer than the file", int64(len(content)) + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			section := RegistryFileSection{
				Version: RegistryFileSectionVersion,
				Files:   []RegistryFileEntry{{SHA256: hash, Size: tc.size, Release: "aws", Version: "1.0.0"}},
			}

			_, err := svc.readRegistryFiles(t.Context(), keyInput(), "ns-abc", section)
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("readRegistryFiles = %v, want ErrCorrupt", err)
			}
		})
	}

	t.Run("the exact size reads", func(t *testing.T) {
		t.Parallel()

		section := RegistryFileSection{
			Version: RegistryFileSectionVersion,
			Files:   []RegistryFileEntry{{SHA256: hash, Size: int64(len(content)), Release: "aws", Version: "1.0.0"}},
		}

		files, err := svc.readRegistryFiles(t.Context(), keyInput(), "ns-abc", section)
		if err != nil || !bytes.Equal(files[hash], content) {
			t.Fatalf("readRegistryFiles = %v, %v, want the content", files, err)
		}
	})
}

// fileSHA returns the SHA-256 of content in lowercase hex.
func fileSHA(content []byte) string {
	sum := sha256.Sum256(content)

	return hex.EncodeToString(sum[:])
}

// endlessStore returns a reader that never ends and counts the bytes read.
type endlessStore struct {
	blob.Store

	read   int64
	closed bool
}

type endlessReader struct {
	store *endlessStore
}

func (r *endlessReader) Read(p []byte) (int, error) {
	r.store.read += int64(len(p))

	return len(p), nil
}

func (r *endlessReader) Close() error {
	r.store.closed = true

	return nil
}

func (s *endlessStore) Get(context.Context, blob.Key) (io.ReadCloser, error) {
	return &endlessReader{store: s}, nil
}

// TestReadLimitedKeyBoundsMemory sends an endless stream straight to the read
// layer. The limit alone must stop the read, before the later length check.
func TestReadLimitedKeyBoundsMemory(t *testing.T) {
	t.Parallel()

	const size = 1024

	store := &endlessStore{Store: nil, read: 0, closed: false}

	content, err := readLimitedKey(t.Context(), store, blob.Key("namespaces/ns-a/registry/sha256/x"), size)
	if !errors.Is(err, ErrCorrupt) || content != nil {
		t.Fatalf("readLimitedKey(endless) = %d bytes, %v, want ErrCorrupt", len(content), err)
	}

	if store.read > size+1 {
		t.Fatalf("readLimitedKey read %d bytes, want at most %d", store.read, size+1)
	}

	if !store.closed {
		t.Fatal("readLimitedKey did not close the reader")
	}
}
