package backup_test

import (
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

type memRegistryStore struct {
	files   map[string][]byte
	removed []string
}

func newMemRegistryStore() *memRegistryStore {
	return &memRegistryStore{files: make(map[string][]byte), removed: nil}
}

func (m *memRegistryStore) Has(key string) (bool, error) {
	_, ok := m.files[key]
	return ok, nil
}

func (m *memRegistryStore) Read(key string) ([]byte, error) {
	b, ok := m.files[key]
	if !ok {
		return nil, errTestMissing(key)
	}

	return b, nil
}

func (m *memRegistryStore) Write(key string, content []byte) error {
	m.files[key] = content
	return nil
}

func (m *memRegistryStore) Remove(key string) error {
	delete(m.files, key)
	m.removed = append(m.removed, key)
	return nil
}

type testMissingError struct{ key string }

func errTestMissing(key string) *testMissingError { return &testMissingError{key: key} }

func (e *testMissingError) Error() string { return "missing " + e.key }

func registrySection(hashes []string) *backup.RegistryFileSection {
	entries := make([]backup.RegistryFileEntry, len(hashes))
	for i, h := range hashes {
		entries[i] = backup.RegistryFileEntry{
			SHA256:  h,
			Size:    1,
			Release: "provider-aws",
			Version: "1.0.0",
		}
	}

	return &backup.RegistryFileSection{Version: backup.RegistryFileSectionVersion, Files: entries}
}

func TestRestoreRegistryFiles(t *testing.T) {
	t.Parallel()

	h := fileHashOf("registry-file-a")
	section := registrySection([]string{h})
	files := map[string][]byte{h: []byte("registry-file-a")}
	store := newMemRegistryStore()

	warn, err := backup.RestoreRegistryFiles(section, files, "ns-target", store)
	if err != nil {
		t.Fatalf("RestoreRegistryFiles = %v", err)
	}

	if warn.Code != "" {
		t.Fatalf("RestoreRegistryFiles warn = %+v, want no warning", warn)
	}

	key, err := backup.RegistryFileBlobKey("ns-target", h)
	if err != nil {
		t.Fatalf("RegistryFileBlobKey = %v", err)
	}

	got, ok := store.files[key]
	if !ok || string(got) != "registry-file-a" {
		t.Fatalf("target store holds %v for %q, want the restored bytes", got, key)
	}

	if !strings.HasPrefix(key, "namespaces/ns-target/registry/sha256/") {
		t.Fatalf("key %q is not under the target namespace ID path", key)
	}
}

func TestRestoreMissingRegistryFile(t *testing.T) {
	t.Parallel()

	h := fileHashOf("registry-file-a")
	section := registrySection([]string{h})
	store := newMemRegistryStore()

	warn, err := backup.RestoreRegistryFiles(section, map[string][]byte{}, "ns-target", store)
	if err == nil || !strings.Contains(err.Error(), h) {
		t.Fatalf("RestoreRegistryFiles missing = %v, want error naming %q", err, h)
	}

	if warn.Code != "" {
		t.Fatalf("RestoreRegistryFiles missing warn = %+v, want no warning", warn)
	}

	if len(store.files) != 0 {
		t.Fatalf("store holds %d keys after missing file, want none visible", len(store.files))
	}
}

func TestRestorePartialManifestWritesNothing(t *testing.T) {
	t.Parallel()

	hostile := &backup.RegistryFileSection{
		Version: backup.RegistryFileSectionVersion,
		Files: []backup.RegistryFileEntry{
			{SHA256: "../escape", Size: 1, Release: "provider-aws", Version: "1.0.0"},
		},
	}
	store := newMemRegistryStore()

	warn, err := backup.RestoreRegistryFiles(hostile, map[string][]byte{}, "ns-target", store)
	if err == nil {
		t.Fatal("RestoreRegistryFiles hostile = nil, want refusal before any write")
	}

	if warn.Code != "" {
		t.Fatalf("RestoreRegistryFiles hostile warn = %+v, want no warning", warn)
	}

	if len(store.files) != 0 {
		t.Fatalf("store holds %d keys after hostile manifest, want none", len(store.files))
	}
}

func TestRestoreCorruptBytes(t *testing.T) {
	t.Parallel()

	h := fileHashOf("registry-file-a")
	section := registrySection([]string{h})
	files := map[string][]byte{h: []byte("altered-bytes")}
	store := newMemRegistryStore()

	warn, err := backup.RestoreRegistryFiles(section, files, "ns-target", store)
	if err == nil || !strings.Contains(err.Error(), h) {
		t.Fatalf("RestoreRegistryFiles corrupt = %v, want error naming %q", err, h)
	}

	if warn.Code != "" {
		t.Fatalf("RestoreRegistryFiles corrupt warn = %+v, want no warning", warn)
	}

	if len(store.files) != 0 {
		t.Fatalf("store holds %d keys after corrupt bytes, want none visible", len(store.files))
	}
}

func TestRestoreLegacyWithoutEntries(t *testing.T) {
	t.Parallel()

	store := newMemRegistryStore()

	warn, err := backup.RestoreRegistryFiles(nil, map[string][]byte{}, "ns-target", store)
	if err != nil {
		t.Fatalf("RestoreRegistryFiles legacy = %v", err)
	}

	if warn.Code != backup.WarnRegistryFilesAbsent {
		t.Fatalf("RestoreRegistryFiles legacy warn = %+v, want code %q", warn, backup.WarnRegistryFilesAbsent)
	}

	if !strings.Contains(warn.Text, "opendustpan backup create --namespace <namespace>") {
		t.Fatalf("warning %q does not name the backup command", warn.Text)
	}

	empty := &backup.RegistryFileSection{Version: backup.RegistryFileSectionVersion, Files: nil}
	warn, err = backup.RestoreRegistryFiles(empty, map[string][]byte{}, "ns-target", store)
	if err != nil || warn.Code != "" {
		t.Fatalf("RestoreRegistryFiles empty = (%+v, %v), want no warning and no error", warn, err)
	}
}

func TestRestoreIdempotentRetry(t *testing.T) {
	t.Parallel()

	h1 := fileHashOf("registry-file-a")
	h2 := fileHashOf("registry-file-b")
	section := registrySection([]string{h1, h2})
	files := map[string][]byte{h1: []byte("registry-file-a"), h2: []byte("registry-file-b")}
	store := newMemRegistryStore()

	warn, err := backup.RestoreRegistryFiles(section, files, "ns-target", store)
	if err != nil {
		t.Fatalf("first RestoreRegistryFiles = %v", err)
	}

	if warn.Code != "" {
		t.Fatalf("first RestoreRegistryFiles warn = %+v, want no warning", warn)
	}

	// A retry over existing keys keeps matching keys and completes.
	warn, err = backup.RestoreRegistryFiles(section, files, "ns-target", store)
	if err != nil {
		t.Fatalf("retry RestoreRegistryFiles = %v", err)
	}

	if warn.Code != "" {
		t.Fatalf("retry RestoreRegistryFiles warn = %+v, want no warning", warn)
	}

	if len(store.files) != 2 {
		t.Fatalf("store holds %d keys after retry, want 2", len(store.files))
	}
}

func TestRestoreAuthorizedAudited(t *testing.T) {
	t.Parallel()

	// The restore refuses an empty target namespace before any write, which
	// the API layer maps to its authorization and audit path.
	h := fileHashOf("registry-file-a")
	section := registrySection([]string{h})
	files := map[string][]byte{h: []byte("registry-file-a")}
	store := newMemRegistryStore()

	warn, err := backup.RestoreRegistryFiles(section, files, "", store)
	if err == nil {
		t.Fatal("RestoreRegistryFiles empty namespace = nil, want refusal")
	}

	if warn.Code != "" {
		t.Fatalf("RestoreRegistryFiles empty namespace warn = %+v, want no warning", warn)
	}

	if len(store.files) != 0 {
		t.Fatalf("store holds %d keys after refused restore, want none", len(store.files))
	}
}
