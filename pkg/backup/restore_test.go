package backup_test

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // The test computes the MD5 values the manifest records.
	"crypto/rand"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// errRestoreBoom is an injected load failure.
var errRestoreBoom = errors.New("restore load boom")

// restoreDump is the test-only SQL part: the wrapped keys and the row counts.
// The fake store gob-encodes it as backups/<id>/db.
type restoreDump struct {
	Keys []secret.WrappedKey
	Rows map[string]int64
}

// fakeRestoreStore is a fake backup.RestoreStore: an empty SQL destination
// that loads restoreDump bytes. importCalls counts Import calls, so tests
// prove that a refused restore never loads.
type fakeRestoreStore struct {
	mu          sync.Mutex
	dialect     backup.Dialect
	version     int
	rows        map[string]int64
	loaded      bool
	importCalls int
	failImport  error
}

func newFakeRestoreStore() *fakeRestoreStore {
	return &fakeRestoreStore{
		mu:          sync.Mutex{},
		dialect:     backup.DialectSQLite,
		version:     10,
		rows:        map[string]int64{},
		loaded:      false,
		importCalls: 0,
		failImport:  nil,
	}
}

func (s *fakeRestoreStore) Export(_ context.Context, _ io.Writer) (backup.Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return backup.Summary{
		Dialect:       s.dialect,
		HomeRegions:   []string{},
		WrapperIDs:    []string{},
		RowCounts:     maps.Clone(s.rows),
		Blobs:         nil,
		SchemaVersion: s.version,
	}, nil
}

func (s *fakeRestoreStore) PeekKey(_ context.Context, r io.Reader, wrapperID string) (secret.WrappedKey, error) {
	var dump restoreDump

	if err := gob.NewDecoder(r).Decode(&dump); err != nil {
		return secret.WrappedKey{}, fmt.Errorf("decode backup stream: %w", backup.ErrCorrupt)
	}

	for _, k := range dump.Keys {
		if k.WrapperID == wrapperID {
			return k, nil
		}
	}

	return secret.WrappedKey{}, fmt.Errorf("backup holds no data key of wrapper %q: %w", wrapperID, backup.ErrCorrupt)
}

func (s *fakeRestoreStore) Import(_ context.Context, r io.Reader, want backup.Summary) (backup.Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.importCalls++

	if s.failImport != nil {
		return backup.Summary{}, s.failImport
	}

	if want.Dialect != s.dialect {
		return backup.Summary{}, fmt.Errorf("import backup of dialect %q: %w", want.Dialect, backup.ErrDialectMismatch)
	}

	if want.SchemaVersion != s.version {
		return backup.Summary{}, fmt.Errorf(
			"import backup of schema version %d: %w", want.SchemaVersion, backup.ErrSchemaMismatch,
		)
	}

	if len(s.rows) != 0 {
		return backup.Summary{}, backup.ErrNotEmpty
	}

	var dump restoreDump

	if err := gob.NewDecoder(r).Decode(&dump); err != nil {
		return backup.Summary{}, fmt.Errorf("decode backup stream: %w", backup.ErrCorrupt)
	}

	if !maps.Equal(dump.Rows, want.RowCounts) {
		return backup.Summary{}, fmt.Errorf("import row counts %v differ from manifest %v: %w",
			dump.Rows, want.RowCounts, backup.ErrCorrupt)
	}

	s.rows = maps.Clone(dump.Rows)
	s.loaded = true

	wrappers := map[string]bool{}
	for _, k := range dump.Keys {
		wrappers[k.WrapperID] = true
	}

	return backup.Summary{
		Dialect:       s.dialect,
		HomeRegions:   slices.Clone(want.HomeRegions),
		WrapperIDs:    slices.Sorted(maps.Keys(wrappers)),
		RowCounts:     maps.Clone(s.rows),
		Blobs:         slices.Clone(want.Blobs),
		SchemaVersion: s.version,
	}, nil
}

func (s *fakeRestoreStore) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.importCalls
}

func (s *fakeRestoreStore) state() (map[string]int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return maps.Clone(s.rows), s.loaded
}

// stubWrapper is a fake secret.Wrapper with one fixed wrapping. Unwrap
// ignores the associated data and opens only the fixed ciphertext.
type stubWrapper struct {
	id         string
	ciphertext []byte
	opened     []byte
	fail       bool
}

func (w stubWrapper) ID() string { return w.id }

func (w stubWrapper) Wrap(_ context.Context, _, _ []byte) ([]byte, error) {
	return bytes.Clone(w.ciphertext), nil
}

func (w stubWrapper) Unwrap(_ context.Context, ciphertext, _ []byte) ([]byte, error) {
	if w.fail || !bytes.Equal(ciphertext, w.ciphertext) {
		return nil, secret.ErrDecrypt
	}

	return bytes.Clone(w.opened), nil
}

// restoreSetup wires a target with one backup and an empty destination.
type restoreSetup struct {
	target    *memStore
	destBlobs *memStore
	dest      *fakeRestoreStore
	wrapper   secret.Wrapper
	manifest  backup.Manifest
	db        []byte
	blobs     map[string][]byte
}

func testWrapper() secret.Wrapper { //nolint:ireturn // Test helper returns the interface.
	return stubWrapper{
		id:         "kek-1",
		ciphertext: []byte("wrapped-1"),
		opened:     bytes.Repeat([]byte{7}, 32),
		fail:       false,
	}
}

func testScope(t *testing.T) secret.Scope {
	t.Helper()

	scope, err := secret.NewScope(namespace.Name("acme"), "stk-test01")
	if err != nil {
		t.Fatalf("new scope: %v", err)
	}

	return scope
}

func testWrappedKey(t *testing.T) secret.WrappedKey {
	t.Helper()

	return secret.WrappedKey{
		CreatedAt:  time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC),
		Scope:      testScope(t),
		WrapperID:  "kek-1",
		Ciphertext: []byte("wrapped-1"),
	}
}

func encodeRestoreDump(t *testing.T, dump restoreDump) []byte {
	t.Helper()

	var buf bytes.Buffer

	if err := gob.NewEncoder(&buf).Encode(dump); err != nil {
		t.Fatalf("encode dump: %v", err)
	}

	return buf.Bytes()
}

func newRestoreSetup(t *testing.T) *restoreSetup {
	t.Helper()

	target := newMemStore()
	blobs := map[string][]byte{
		"tofu/ws1/sv1/a1": []byte(`{"state":1}`),
		"tofu/ws1/sv2/a1": []byte(`{"state":2}`),
	}
	dump := restoreDump{
		Keys: []secret.WrappedKey{testWrappedKey(t)},
		Rows: map[string]int64{"namespaces": 1, "stacks": 2},
	}
	db := encodeRestoreDump(t, dump)
	manifest := writeRestoreManifest(t, target, blobs, restoreBlobsInput{
		db:      db,
		dump:    dump,
		regions: []string{"eu-west"},
	})

	return &restoreSetup{
		target:    target,
		destBlobs: newMemStore(),
		dest:      newFakeRestoreStore(),
		wrapper:   testWrapper(),
		manifest:  manifest,
		db:        db,
		blobs:     blobs,
	}
}

// restoreBlobsInput groups what writeManifest stores alongside the SQL part.
type restoreBlobsInput struct {
	db      []byte
	dump    restoreDump
	regions []string
}

// writeRestoreManifest stores the SQL part, the blobs and the manifest in the
// target and returns the manifest.
func writeRestoreManifest(
	t *testing.T, target *memStore, blobs map[string][]byte, in restoreBlobsInput,
) backup.Manifest {
	t.Helper()

	const id = backup.ID("bkp-restore")

	target.storeObject(t, "backups/"+id.String()+"/db", in.db)

	entries := make([]backup.ManifestBlob, 0, len(blobs))

	for _, key := range slices.Sorted(maps.Keys(blobs)) {
		target.storeObject(t, "blobs/"+key, blobs[key])

		sum := md5.Sum(blobs[key]) //nolint:gosec // The test recomputes the MD5 the manifest records.

		entries = append(entries, backup.ManifestBlob{
			Key:  key,
			Size: int64(len(blobs[key])),
			MD5:  hex.EncodeToString(sum[:]),
		})
	}

	sha := sha256.Sum256(in.db)
	wrappers := []string{}

	for _, k := range in.dump.Keys {
		if !slices.Contains(wrappers, k.WrapperID) {
			wrappers = append(wrappers, k.WrapperID)
		}
	}

	slices.Sort(wrappers)

	manifest := backup.Manifest{
		CreatedAt:     time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC),
		ID:            id,
		Dialect:       backup.DialectSQLite,
		HomeRegions:   in.regions,
		WrapperIDs:    wrappers,
		Blobs:         entries,
		Version:       "test-1.0",
		SQLSHA256:     hex.EncodeToString(sha[:]),
		Format:        backup.FormatVersion,
		SchemaVersion: 10,
		SQLSize:       int64(len(in.db)),
		RowCounts:     maps.Clone(in.dump.Rows),
	}

	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}

	target.storeObject(t, "backups/"+id.String()+"/manifest.json", data)

	return manifest
}

func (s *restoreSetup) destArg(region string) backup.RestoreDest {
	return backup.RestoreDest{Blobs: s.destBlobs, Store: s.dest, Region: region}
}

func (s *restoreSetup) assertZeroWrites(t *testing.T) {
	t.Helper()

	assertRestoreZeroWrites(t, s.dest, s.destBlobs)
}

// assertRestoreZeroWrites proves that no destination write happened: no SQL
// load, no blob and no row.
func assertRestoreZeroWrites(t *testing.T, dest *fakeRestoreStore, destBlobs *memStore) {
	t.Helper()

	if dest.calls() != 0 {
		t.Fatalf("destination loads = %d, want 0: the restore must stop before the first write", dest.calls())
	}

	for _, key := range []string{"tofu/ws1/sv1/a1", "tofu/ws1/sv2/a1"} {
		if destBlobs.has(key) {
			t.Fatalf("destination holds blob %q, want none: the restore must stop before the first write", key)
		}
	}

	if rows, loaded := dest.state(); loaded || len(rows) != 0 {
		t.Fatalf("destination rows = %v loaded %v, want empty", rows, loaded)
	}
}

func mustRestore(t *testing.T, s *restoreSetup, region string, key secret.Wrapper) string {
	t.Helper()

	warning, err := backup.Restore(t.Context(), s.target, "bkp-restore", s.destArg(region), key)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}

	return warning
}

func TestRestoreGood(t *testing.T) {
	t.Parallel()

	s := newRestoreSetup(t)

	if warning := mustRestore(t, s, "eu-west", s.wrapper); warning != "" {
		t.Fatalf("warning = %q, want empty for a home region", warning)
	}

	for key, want := range s.blobs {
		if got := s.destBlobs.readObject(t, key); !bytes.Equal(got, want) {
			t.Fatalf("blob %q = %q, want %q", key, got, want)
		}
	}

	rows, loaded := s.dest.state()
	if !loaded || !maps.Equal(rows, s.manifest.RowCounts) {
		t.Fatalf("destination rows = %v loaded %v, want %v loaded", rows, loaded, s.manifest.RowCounts)
	}

	if got := s.dest.calls(); got != 1 {
		t.Fatalf("destination loads = %d, want 1", got)
	}
}

func TestRestoreWrongKeyStopsBeforeFirstWrite(t *testing.T) {
	t.Parallel()

	t.Run("unknown id", func(t *testing.T) {
		t.Parallel()

		s := newRestoreSetup(t)
		wrong := stubWrapper{id: "kek-2", ciphertext: []byte("wrapped-2"), opened: []byte("opened-2"), fail: false}

		err := checkRestoreErr(t, s, wrong)
		assertKeyErr(t, err)
		s.assertZeroWrites(t)
	})

	t.Run("key does not open", func(t *testing.T) {
		t.Parallel()

		s := newRestoreSetup(t)
		bad := stubWrapper{id: "kek-1", ciphertext: []byte("wrapped-1"), opened: nil, fail: true}

		err := checkRestoreErr(t, s, bad)
		assertKeyErr(t, err)
		s.assertZeroWrites(t)
	})
}

func checkRestoreErr(t *testing.T, s *restoreSetup, key secret.Wrapper) error {
	t.Helper()

	_, err := backup.Restore(t.Context(), s.target, "bkp-restore", s.destArg(""), key)
	if err == nil {
		t.Fatal("restore with a wrong key succeeded, want an error")
	}

	return err
}

func assertKeyErr(t *testing.T, err error) {
	t.Helper()

	if !strings.Contains(err.Error(), "DUSTPAN_KEY_ENCRYPTION_KEY") {
		t.Fatalf("error = %q, want it to name DUSTPAN_KEY_ENCRYPTION_KEY", err)
	}
}

func TestRestoreNoKeyWithWrapperIDs(t *testing.T) {
	t.Parallel()

	s := newRestoreSetup(t)

	assertKeyErr(t, checkRestoreErr(t, s, nil))
	s.assertZeroWrites(t)
}

func TestRestoreNoKeyNeededWithoutWrapperIDs(t *testing.T) {
	t.Parallel()

	s := newRestoreSetup(t)

	dump := restoreDump{Keys: nil, Rows: map[string]int64{"namespaces": 1, "stacks": 2}}
	db := encodeRestoreDump(t, dump)
	sha := sha256.Sum256(db)

	mustDelete(t, s.target, "backups/bkp-restore/db")
	s.target.storeObject(t, "backups/bkp-restore/db", db)

	s.manifest.WrapperIDs = nil
	s.manifest.SQLSHA256 = hex.EncodeToString(sha[:])
	s.manifest.SQLSize = int64(len(db))
	s.manifest.RowCounts = maps.Clone(dump.Rows)
	rewriteManifest(t, s)

	if warning := mustRestore(t, s, "", nil); warning != "" {
		t.Fatalf("warning = %q, want empty", warning)
	}

	if _, loaded := s.dest.state(); !loaded {
		t.Fatal("destination did not load, want rows")
	}
}

// rewriteManifest re-encodes the setup manifest after a test edits it. Tests
// that replace the db bytes must fix the checksum first.
func rewriteManifest(t *testing.T, s *restoreSetup) {
	t.Helper()

	data, err := json.Marshal(s.manifest)
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}

	if err := s.target.Delete(t.Context(), mustKey(t, "backups/bkp-restore/manifest.json")); err != nil {
		t.Fatalf("delete manifest: %v", err)
	}

	s.target.storeObject(t, "backups/bkp-restore/manifest.json", data)
}

func TestRestoreNonEmptyDestination(t *testing.T) {
	t.Parallel()

	s := newRestoreSetup(t)
	s.dest.rows = map[string]int64{"namespaces": 1}

	err := checkRestoreErr(t, s, s.wrapper)
	if !errors.Is(err, backup.ErrNotEmpty) {
		t.Fatalf("err = %v, want ErrNotEmpty", err)
	}

	if s.dest.calls() != 0 {
		t.Fatalf("destination loads = %d, want 0", s.dest.calls())
	}

	for _, key := range []string{"tofu/ws1/sv1/a1", "tofu/ws1/sv2/a1"} {
		if s.destBlobs.has(key) {
			t.Fatalf("destination holds blob %q, want none", key)
		}
	}

	if rows, _ := s.dest.state(); !maps.Equal(rows, map[string]int64{"namespaces": 1}) {
		t.Fatalf("destination rows = %v, want the untouched seed", rows)
	}
}

func TestRestoreSchemaMismatch(t *testing.T) {
	t.Parallel()

	s := newRestoreSetup(t)
	s.manifest.SchemaVersion = 11
	rewriteManifest(t, s)

	err := checkRestoreErr(t, s, s.wrapper)
	if !errors.Is(err, backup.ErrSchemaMismatch) {
		t.Fatalf("err = %v, want ErrSchemaMismatch", err)
	}

	if !strings.Contains(err.Error(), "11") || !strings.Contains(err.Error(), "10") {
		t.Fatalf("error = %q, want it to name both versions", err)
	}

	s.assertZeroWrites(t)
}

func TestRestoreDialectMismatch(t *testing.T) {
	t.Parallel()

	s := newRestoreSetup(t)
	s.manifest.Dialect = backup.DialectPostgres
	rewriteManifest(t, s)

	err := checkRestoreErr(t, s, s.wrapper)
	if !errors.Is(err, backup.ErrDialectMismatch) {
		t.Fatalf("err = %v, want ErrDialectMismatch", err)
	}

	s.assertZeroWrites(t)
}

func TestRestoreMissingManifest(t *testing.T) {
	t.Parallel()

	s := newRestoreSetup(t)
	mustDelete(t, s.target, "backups/bkp-restore/manifest.json")

	err := checkRestoreErr(t, s, s.wrapper)
	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("err = %v, want ErrCorrupt", err)
	}

	s.assertZeroWrites(t)
}

func TestRestoreUnknownFormat(t *testing.T) {
	t.Parallel()

	s := newRestoreSetup(t)
	s.manifest.Format = 99
	rewriteManifest(t, s)

	err := checkRestoreErr(t, s, s.wrapper)
	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("err = %v, want ErrCorrupt", err)
	}

	s.assertZeroWrites(t)
}

func TestRestoreMissingDatabase(t *testing.T) {
	t.Parallel()

	s := newRestoreSetup(t)
	mustDelete(t, s.target, "backups/bkp-restore/db")

	err := checkRestoreErr(t, s, s.wrapper)
	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("err = %v, want ErrCorrupt", err)
	}

	s.assertZeroWrites(t)
}

func TestRestoreCorruptSQLPart(t *testing.T) {
	t.Parallel()

	s := newRestoreSetup(t)

	mustDelete(t, s.target, "backups/bkp-restore/db")
	s.target.storeObject(t, "backups/bkp-restore/db", append(bytes.Clone(s.db), 0x00))

	err := checkRestoreErr(t, s, s.wrapper)
	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("err = %v, want ErrCorrupt", err)
	}

	s.assertZeroWrites(t)
}

func TestRestoreWrongBlobMD5(t *testing.T) {
	t.Parallel()

	s := newRestoreSetup(t)
	mustDelete(t, s.target, "blobs/tofu/ws1/sv2/a1")
	s.target.storeObject(t, "blobs/tofu/ws1/sv2/a1", []byte("tampered-state!!"))

	err := checkRestoreErr(t, s, s.wrapper)
	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("err = %v, want ErrCorrupt", err)
	}

	if s.destBlobs.has("tofu/ws1/sv2/a1") {
		t.Fatal("destination holds the corrupt blob, want it deleted")
	}

	if s.dest.calls() != 0 {
		t.Fatalf("destination loads = %d, want 0: the SQL load must not run after a blob mismatch", s.dest.calls())
	}
}

func TestRestoreBlobExists(t *testing.T) {
	t.Parallel()

	s := newRestoreSetup(t)
	s.destBlobs.storeObject(t, "tofu/ws1/sv1/a1", []byte(`{"state":1}`))

	err := checkRestoreErr(t, s, s.wrapper)
	if !errors.Is(err, blob.ErrExists) {
		t.Fatalf("err = %v, want ErrExists", err)
	}

	if _, loaded := s.dest.state(); loaded {
		t.Fatal("destination loaded, want an empty database")
	}
}

func TestRestoreRegionWarning(t *testing.T) {
	t.Parallel()

	s := newRestoreSetup(t)

	warning := mustRestore(t, s, "us-east", s.wrapper)
	if !strings.Contains(warning, "us-east") {
		t.Fatalf("warning = %q, want it to name the region", warning)
	}

	empty := newRestoreSetup(t)
	if warning := mustRestore(t, empty, "", empty.wrapper); warning != "" {
		t.Fatalf("warning = %q, want empty without a region", warning)
	}
}

func TestRestoreLoadFailureLeavesEmpty(t *testing.T) {
	t.Parallel()

	s := newRestoreSetup(t)
	s.dest.failImport = errRestoreBoom

	err := checkRestoreErr(t, s, s.wrapper)
	if !errors.Is(err, errRestoreBoom) {
		t.Fatalf("err = %v, want the load failure", err)
	}

	if rows, loaded := s.dest.state(); loaded || len(rows) != 0 {
		t.Fatalf("destination rows = %v loaded %v, want empty", rows, loaded)
	}
}

func TestRestoreRowCountMismatch(t *testing.T) {
	t.Parallel()

	s := newRestoreSetup(t)
	s.manifest.RowCounts = map[string]int64{"namespaces": 9, "stacks": 2}
	rewriteManifest(t, s)

	err := checkRestoreErr(t, s, s.wrapper)
	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("err = %v, want ErrCorrupt", err)
	}

	if _, loaded := s.dest.state(); loaded {
		t.Fatal("destination loaded, want an empty database")
	}
}

// mintNamespaces is a namespace.Store stub homed in "local" for minting
// scope-bound keys through the secret service.
type mintNamespaces struct{}

func (mintNamespaces) CreateNamespace(context.Context, namespace.Namespace) error {
	return namespace.ErrAlreadyExists
}

func (mintNamespaces) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	return namespace.Namespace{CreatedAt: time.Time{}, Name: name, HomeRegion: "local", FailoverVersion: 1}, nil
}

func (mintNamespaces) ListNamespaces(_ context.Context) ([]namespace.Namespace, error) {
	return nil, nil
}

func (mintNamespaces) Ping(_ context.Context) error {
	return nil
}

// mintKeys is a secret.KeyStore in memory for minting scope-bound keys.
type mintKeys struct {
	mu   sync.Mutex
	keys map[secret.Scope]secret.WrappedKey
}

func (m *mintKeys) CreateKey(_ context.Context, k secret.WrappedKey, _ namespace.Fence) (secret.WrappedKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, ok := m.keys[k.Scope]; ok {
		return existing, nil
	}

	m.keys[k.Scope] = k

	return k, nil
}

func (m *mintKeys) GetKey(_ context.Context, scope secret.Scope) (secret.WrappedKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k, ok := m.keys[scope]
	if !ok {
		return secret.WrappedKey{}, secret.ErrKeyNotFound
	}

	return k, nil
}

func (m *mintKeys) ListWrapperIDs(_ context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	seen := map[string]bool{}
	for _, k := range m.keys {
		seen[k.WrapperID] = true
	}

	return slices.Sorted(maps.Keys(seen)), nil
}

// testScopeID returns the secret scope of id in the test namespace.
func testScopeID(t *testing.T, id string) secret.Scope {
	t.Helper()

	scope, err := secret.NewScope(namespace.Name("acme"), id)
	if err != nil {
		t.Fatalf("new scope: %v", err)
	}

	return scope
}

// mintBoundKey encrypts one value in scope through the secret service and
// returns the stored wrapped key, whose ciphertext is bound to scope by the
// real wrapper.
func mintBoundKey(t *testing.T, wrapper secret.Wrapper, scope secret.Scope) secret.WrappedKey {
	t.Helper()

	ctx := t.Context()
	namespaces, err := namespace.NewService(namespace.Config{Store: mintNamespaces{}, Now: nil, Region: "local"})
	if err != nil {
		t.Fatalf("namespaces: %v", err)
	}

	keys := &mintKeys{mu: sync.Mutex{}, keys: map[secret.Scope]secret.WrappedKey{}}

	svc, err := secret.NewService(secret.Config{
		Keys: keys, Wrapper: wrapper, Namespaces: namespaces, Now: nil, MaxBatchBytes: 4 << 20,
	})
	if err != nil {
		t.Fatalf("secret service: %v", err)
	}

	if _, encryptErr := svc.Encrypt(ctx, scope, [][]byte{[]byte("s3cr3t")}); encryptErr != nil {
		t.Fatalf("encrypt: %v", encryptErr)
	}

	stored, err := keys.GetKey(ctx, scope)
	if err != nil {
		t.Fatalf("get key: %v", err)
	}

	return stored
}

// TestRestoreScopeSwappedKeyRefuses proves that the key check binds the
// ciphertext to its scope: a wrapped key moved to another scope does not
// open, even though the wrapper ID matches and the ciphertext is genuine.
// The restore refuses before the first write. A stub wrapper that ignores
// the associated data would open the swapped key, so this test fails on it.
func TestRestoreScopeSwappedKeyRefuses(t *testing.T) {
	t.Parallel()

	kek := make([]byte, 32)
	if _, err := rand.Read(kek); err != nil {
		t.Fatalf("generate key-encryption key: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper(kek)
	if err != nil {
		t.Fatalf("builtin wrapper: %v", err)
	}

	stored := mintBoundKey(t, wrapper, testScopeID(t, "stk-test01"))

	swapped := stored
	swapped.Scope = testScopeID(t, "stk-test02")

	dump := restoreDump{
		Keys: []secret.WrappedKey{swapped},
		Rows: map[string]int64{"namespaces": 1, "stacks": 2},
	}
	db := encodeRestoreDump(t, dump)
	target := newMemStore()
	blobs := map[string][]byte{
		"tofu/ws1/sv1/a1": []byte(`{"state":1}`),
		"tofu/ws1/sv2/a1": []byte(`{"state":2}`),
	}
	manifest := writeRestoreManifest(t, target, blobs, restoreBlobsInput{
		db:      db,
		dump:    dump,
		regions: []string{"eu-west"},
	})

	if !slices.Contains(manifest.WrapperIDs, wrapper.ID()) {
		t.Fatalf("manifest wrappers = %v, want %q", manifest.WrapperIDs, wrapper.ID())
	}

	dest := newFakeRestoreStore()
	destBlobs := newMemStore()

	_, restoreErr := backup.Restore(
		t.Context(),
		target,
		"bkp-restore",
		backup.RestoreDest{Blobs: destBlobs, Store: dest, Region: ""},
		wrapper,
	)
	if restoreErr == nil {
		t.Fatal("restore with a scope-swapped key succeeded, want an error")
	}

	if !errors.Is(restoreErr, secret.ErrDecrypt) {
		t.Fatalf("err = %v, want ErrDecrypt", restoreErr)
	}

	assertKeyErr(t, restoreErr)

	assertRestoreZeroWrites(t, dest, destBlobs)
}
