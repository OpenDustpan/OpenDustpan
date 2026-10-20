package backup_test

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // The test computes the MD5 values the manifest records.
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/installation"
)

// Static test errors. The suite injects them as target and export failures.
var (
	errConnectionReset   = errors.New("connection reset")
	errTargetUnavailable = errors.New("target unavailable")
	errExport            = errors.New("export failed")
)

// memStore is an in-memory blob.Store. failPut injects a Put error per key.
// attempts records every Put key, including failed ones.
type memStore struct {
	mu       sync.Mutex
	objects  map[string][]byte
	failPut  map[string]error
	attempts []string
}

func newMemStore() *memStore {
	return &memStore{mu: sync.Mutex{}, objects: map[string][]byte{}, failPut: map[string]error{}, attempts: nil}
}

func (m *memStore) Put(ctx context.Context, key blob.Key, r io.Reader) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.attempts = append(m.attempts, key.String())

	if err, ok := m.failPut[key.String()]; ok {
		return err
	}

	if _, ok := m.objects[key.String()]; ok {
		return fmt.Errorf("put %s: %w", key, blob.ErrExists)
	}

	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}

	m.objects[key.String()] = data

	return nil
}

func (m *memStore) Get(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("get %s: %w", key, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	data, ok := m.objects[key.String()]
	if !ok {
		return nil, fmt.Errorf("get %s: %w", key, blob.ErrNotFound)
	}

	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *memStore) Delete(ctx context.Context, key blob.Key) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("delete %s: %w", key, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.objects, key.String())

	return nil
}

// List implements blob.Store: the backup service never lists, so the
// in-memory store lists nothing.
func (m *memStore) List(_ context.Context, _ string) ([]blob.Object, error) {
	return nil, nil
}

// Ping implements blob.Store: the in-memory store always answers.
func (m *memStore) Ping(_ context.Context) error {
	return nil
}

func (m *memStore) readObject(t *testing.T, key string) []byte {
	t.Helper()

	data, err := io.ReadAll(mustGet(t, m, key))
	if err != nil {
		t.Fatalf("read %q: %v", key, err)
	}

	return data
}

func (m *memStore) has(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, ok := m.objects[key]

	return ok
}

func (m *memStore) storeObject(t *testing.T, key string, data []byte) {
	t.Helper()

	if err := m.Put(t.Context(), mustKey(t, key), bytes.NewReader(data)); err != nil {
		t.Fatalf("put %q: %v", key, err)
	}
}

// putAttempts counts the Put calls for key, including failed ones.
func (m *memStore) putAttempts(key string) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	attempts := 0

	for _, got := range m.attempts {
		if got == key {
			attempts++
		}
	}

	return attempts
}

func mustGet(t *testing.T, s blob.Store, key string) io.ReadCloser {
	t.Helper()

	reader, err := s.Get(t.Context(), mustKey(t, key))
	if err != nil {
		t.Fatalf("get %q: %v", key, err)
	}

	return reader
}

func mustKey(t *testing.T, s string) blob.Key {
	t.Helper()

	key, err := blob.ParseKey(s)
	if err != nil {
		t.Fatalf("parse key %q: %v", s, err)
	}

	return key
}

// stubExporter is a fake backup.PortableExporter with canned bytes and summary.
type stubExporter struct {
	sum  backup.Summary
	data []byte
	err  error
}

func (f *stubExporter) ExportPortable(
	_ context.Context, sink backup.PortableSink,
) (backup.Summary, []backup.PortableFile, error) {
	if f.err != nil {
		return backup.Summary{}, nil, f.err
	}

	return writeTestTableFile(sink, f.data, f.sum)
}

// writeTestTableFile writes data as the table file of every table of a test
// export, as a real export writes every table, and returns the summary with the
// descriptors.
func writeTestTableFile(
	sink backup.PortableSink, data []byte, sum backup.Summary,
) (backup.Summary, []backup.PortableFile, error) {
	files := make([]backup.PortableFile, 0, len(backup.Tables))

	for _, table := range backup.Tables {
		path := backup.PortablePath(table)

		w, err := sink(path)
		if err != nil {
			return backup.Summary{}, nil, fmt.Errorf("open test table file: %w", err)
		}

		if _, err := w.Write(data); err != nil {
			return backup.Summary{}, nil, fmt.Errorf("write test SQL data: %w", err)
		}

		if err := w.Close(); err != nil {
			return backup.Summary{}, nil, fmt.Errorf("close test table file: %w", err)
		}

		digest := sha256.Sum256(data)
		files = append(files, backup.PortableFile{
			Table:   table,
			Path:    path,
			SHA256:  hex.EncodeToString(digest[:]),
			Columns: nil,
			Rows:    0,
			Size:    int64(len(data)),
		})
	}

	return sum, files, nil
}

// blockingExporter blocks inside Export until release closes, so a test can
// hold one backup running while it starts another.
type blockingExporter struct {
	sum     backup.Summary
	data    []byte
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (f *blockingExporter) ExportPortable(
	ctx context.Context, sink backup.PortableSink,
) (backup.Summary, []backup.PortableFile, error) {
	f.once.Do(func() { close(f.started) })

	select {
	case <-f.release:
	case <-ctx.Done():
		return backup.Summary{}, nil, fmt.Errorf("wait for test release: %w", ctx.Err())
	}

	return writeTestTableFile(sink, f.data, f.sum)
}

// fakeRegistry is an in-memory backup.Registry.
type fakeRegistry struct {
	mu      sync.Mutex
	rows    map[backup.ID]backup.Record
	failGet error
}

func newFakeRegistry() *fakeRegistry {
	return &fakeRegistry{mu: sync.Mutex{}, rows: map[backup.ID]backup.Record{}, failGet: nil}
}

func (f *fakeRegistry) CreateBackup(_ context.Context, r backup.Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.rows[r.ID]; ok {
		return fmt.Errorf("create backup %s: %w", r.ID, backup.ErrAlreadyExists)
	}

	f.rows[r.ID] = r

	return nil
}

func (f *fakeRegistry) FinishBackup(
	_ context.Context, id backup.ID, state backup.State, errText string, endedAt time.Time,
) (backup.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	row, ok := f.rows[id]
	if !ok {
		return backup.Record{}, fmt.Errorf("finish backup %s: %w", id, backup.ErrNotFound)
	}

	if row.State != backup.StateRunning || (state != backup.StateSucceeded && state != backup.StateFailed) {
		return backup.Record{}, fmt.Errorf("finish backup %s: %w", id, backup.ErrNotRunning)
	}

	row.State = state
	row.Error = errText
	row.EndedAt = endedAt
	f.rows[id] = row

	return row, nil
}

func (f *fakeRegistry) GetBackup(_ context.Context, id backup.ID) (backup.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.failGet != nil {
		return backup.Record{}, f.failGet
	}

	row, ok := f.rows[id]
	if !ok {
		return backup.Record{}, fmt.Errorf("get backup %s: %w", id, backup.ErrNotFound)
	}

	return row, nil
}

func (f *fakeRegistry) ListBackups(_ context.Context) ([]backup.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]backup.Record, 0, len(f.rows))
	for _, row := range f.rows {
		out = append(out, row)
	}

	slices.SortFunc(out, func(a, b backup.Record) int { return b.StartedAt.Compare(a.StartedAt) })

	return out, nil
}

func (f *fakeRegistry) FailRunningBackups(_ context.Context, endedAt time.Time, errText string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for id, row := range f.rows {
		if row.State == backup.StateRunning {
			row.State = backup.StateFailed
			row.Error = errText
			row.EndedAt = endedAt
			f.rows[id] = row
		}
	}

	return nil
}

// fixture wires a Service with an in-memory source and target.
type fixture struct {
	service  *backup.Service
	registry *fakeRegistry
	source   *memStore
	target   *memStore
	now      time.Time
}

func newFixture(t *testing.T, exporter backup.PortableExporter, maxSQL int64) *fixture {
	t.Helper()

	now := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)
	registry := newFakeRegistry()
	source := newMemStore()
	target := newMemStore()

	service, err := backup.NewService(backup.Config{
		Exporter:    exporter,
		Kinds:       fixedKind(installation.KindProduction),
		Registry:    registry,
		Blobs:       source,
		StateOpener: nil,
		Target:      target,
		TargetName:  "mem:target",
		Now:         func() time.Time { return now },
		Version:     "test-1.0",
		MaxSQLBytes: maxSQL,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	return &fixture{service: service, registry: registry, source: source, target: target, now: now}
}

func blobRef(key string, data []byte) backup.BlobRef {
	sum := md5.Sum(data) //nolint:gosec // The manifest records MD5 per ADR 30, for integrity, not security.

	return backup.BlobRef{Key: key, Size: int64(len(data)), MD5: sum, Sealed: false}
}

func testSummary(refs ...backup.BlobRef) backup.Summary {
	return backup.Summary{
		Dialect:       backup.DialectSQLite,
		HomeRegions:   []string{"eu-west"},
		WrapperIDs:    []string{"kek-1"},
		RowCounts:     map[string]int64{"namespaces": 1},
		Blobs:         refs,
		SchemaVersion: 7,
	}
}

func seedSource(t *testing.T, source *memStore, blobs map[string][]byte) {
	t.Helper()

	for key, data := range blobs {
		source.storeObject(t, key, data)
	}
}

func mustRun(t *testing.T, service *backup.Service, id backup.ID) backup.Record {
	t.Helper()

	done, err := service.Run(t.Context(), id)
	if err != nil {
		t.Fatalf("run backup %s: %v", id, err)
	}

	return done
}

func mustGetRecord(t *testing.T, registry *fakeRegistry, id backup.ID) backup.Record {
	t.Helper()

	row, err := registry.GetBackup(t.Context(), id)
	if err != nil {
		t.Fatalf("get backup %s: %v", id, err)
	}

	return row
}

func assertFailed(t *testing.T, registry *fakeRegistry, id backup.ID) {
	t.Helper()

	row := mustGetRecord(t, registry, id)

	if row.State != backup.StateFailed || row.Error == "" {
		t.Fatalf("backup %s = %+v, want failed with error text", id, row)
	}
}

func assertNoManifest(t *testing.T, target *memStore, id backup.ID) {
	t.Helper()

	if target.has("backups/" + id.String() + "/manifest.json") {
		t.Fatalf("target holds a manifest of failed backup %s, want none", id)
	}

	if err := backup.Check(t.Context(), target, id); err == nil {
		t.Fatalf("check of failed backup %s succeeded, want an error", id)
	}
}

func TestRunWritesWholeBackup(t *testing.T) {
	t.Parallel()

	first := []byte(`{"state":1}`)
	second := []byte(`{"state":2}`)
	sql := []byte("sqlite-image-bytes")
	sum := testSummary(blobRef("tofu/ws1/sv1/a1", first), blobRef("tofu/ws1/sv2/a1", second))
	fix := newFixture(t, &stubExporter{sum: sum, data: sql, err: nil}, 0)
	seedSource(t, fix.source, map[string][]byte{"tofu/ws1/sv1/a1": first, "tofu/ws1/sv2/a1": second})

	done := mustRun(t, fix.service, "bkp-whole")

	if done.State != backup.StateSucceeded || done.ID != "bkp-whole" || done.Target != "mem:target" {
		t.Fatalf("finished row = %+v, want succeeded bkp-whole on mem:target", done)
	}

	assertBackupObjects(t, fix.target, "bkp-whole", sql, map[string][]byte{
		"tofu/ws1/sv1/a1": first,
		"tofu/ws1/sv2/a1": second,
	})
	assertWholeManifest(t, fix.target, "bkp-whole", wantManifest(fix.now, "bkp-whole", sql, first, second))

	if err := backup.Check(t.Context(), fix.target, "bkp-whole"); err != nil {
		t.Fatalf("check whole backup: %v", err)
	}
}

// assertBackupObjects checks the SQL part and each named blob in the target.
func assertBackupObjects(
	t *testing.T, target *memStore, id backup.ID, sql []byte, blobs map[string][]byte,
) {
	t.Helper()

	if got := target.readObject(t, sqlPartKey(id)); !bytes.Equal(got, sql) {
		t.Fatalf("backup db = %q, want %q", got, sql)
	}

	for key, want := range blobs {
		if got := target.readObject(t, "blobs/"+key); !bytes.Equal(got, want) {
			t.Fatalf("backup blob %q = %q, want %q", key, got, want)
		}
	}
}

// wantManifest builds the manifest a whole backup of the SQL part with the
// two blobs must write.
func wantManifest(now time.Time, id backup.ID, sql, first, second []byte) backup.Manifest {
	files := sqlPartAllFiles(sql)
	sqlSize, sqlSHA := sqlPartAggregate(files)
	firstMD5 := md5.Sum(first)   //nolint:gosec // The test recomputes the MD5 the manifest records.
	secondMD5 := md5.Sum(second) //nolint:gosec // The test recomputes the MD5 the manifest records.

	return backup.Manifest{
		CreatedAt:   now,
		ID:          id,
		Dialect:     backup.DialectSQLite,
		HomeRegions: []string{"eu-west"},
		WrapperIDs:  []string{"kek-1"},
		Blobs: []backup.ManifestBlob{
			{Key: "tofu/ws1/sv1/a1", Size: int64(len(first)), MD5: hex.EncodeToString(firstMD5[:]), Sealed: false},
			{Key: "tofu/ws1/sv2/a1", Size: int64(len(second)), MD5: hex.EncodeToString(secondMD5[:]), Sealed: false},
		},
		Version:       "test-1.0",
		SQLSHA256:     sqlSHA,
		Format:        backup.FormatVersion,
		SchemaVersion: 7,
		SQLSize:       sqlSize,
		RowCounts:     map[string]int64{"namespaces": 1},
		SQLFormat:     backup.PortableFormat,
		SQLFiles:      files,

		InstallationKind: installation.KindProduction,
	}
}

// assertWholeManifest checks the manifest bytes against the want manifest and
// pins the exact on-disk JSON keys.
func assertWholeManifest(t *testing.T, target *memStore, id backup.ID, want backup.Manifest) {
	t.Helper()

	wantData, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("encode want manifest: %v", err)
	}

	if got := target.readObject(t, "backups/"+id.String()+"/manifest.json"); !bytes.Equal(got, wantData) {
		t.Fatalf("manifest = %s, want %s", got, wantData)
	}

	assertManifestKeys(t, target, id)
}

// assertManifestKeys checks that the on-disk manifest holds exactly the
// stable keys, so a field rename cannot slip into the format unnoticed.
func assertManifestKeys(t *testing.T, target *memStore, id backup.ID) {
	t.Helper()

	var raw map[string]any

	if err := json.Unmarshal(target.readObject(t, "backups/"+id.String()+"/manifest.json"), &raw); err != nil {
		t.Fatalf("parse manifest as keys: %v", err)
	}

	keys := make([]string, 0, len(raw))
	for key := range raw {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	want := []string{
		"blobs",
		"createdAt",
		"dialect",
		"format",
		"homeRegions",
		"id",
		"installationKind",
		"rowCounts",
		"schemaVersion",
		"sqlFiles",
		"sqlFormat",
		"sqlSha256",
		"sqlSize",
		"version",
		"wrapperIds",
	}

	if !slices.Equal(keys, want) {
		t.Fatalf("manifest keys = %v, want %v", keys, want)
	}

	assertManifestBlobKeys(t, raw)
}

// assertManifestBlobKeys checks that each manifest blob entry holds exactly
// the key, size and MD5 keys.
func assertManifestBlobKeys(t *testing.T, raw map[string]any) {
	t.Helper()

	entries, ok := raw["blobs"].([]any)
	if !ok {
		t.Fatalf("manifest blobs = %v, want a list", raw["blobs"])
	}

	for _, entry := range entries {
		fields, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("manifest blob = %v, want an object", entry)
		}

		keys := make([]string, 0, len(fields))
		for key := range fields {
			keys = append(keys, key)
		}

		slices.Sort(keys)

		if !slices.Equal(keys, []string{"key", "md5", "size"}) {
			t.Fatalf("manifest blob keys = %v, want [key md5 size]", keys)
		}
	}
}

func TestManifestFormatIsPinned(t *testing.T) {
	t.Parallel()

	if backup.FormatVersion != 3 {
		t.Fatalf(
			"format version = %d, want 3: the portable format is the manifest format, and it changes only with a new decision",
			backup.FormatVersion,
		)
	}
}

func TestRunSecondBackupReusesBlobs(t *testing.T) {
	t.Parallel()

	content := []byte(`{"state":1}`)
	sql := []byte("sqlite-image-bytes")
	sum := testSummary(blobRef("tofu/ws1/sv1/a1", content))
	fix := newFixture(t, &stubExporter{sum: sum, data: sql, err: nil}, 0)
	seedSource(t, fix.source, map[string][]byte{"tofu/ws1/sv1/a1": content})

	mustRun(t, fix.service, "bkp-first")
	mustRun(t, fix.service, "bkp-second")

	if got := fix.target.readObject(t, "blobs/tofu/ws1/sv1/a1"); !bytes.Equal(got, content) {
		t.Fatalf("reused blob = %q, want the first copy %q", got, content)
	}

	if got := fix.target.readObject(t, sqlPartKey("bkp-second")); !bytes.Equal(got, sql) {
		t.Fatalf("second backup db = %q, want %q", got, sql)
	}

	if got := fix.target.putAttempts("blobs/tofu/ws1/sv1/a1"); got != 2 {
		t.Fatalf("blob put attempts = %d, want 2: the second backup tries the blob again and keeps the first copy", got)
	}

	if err := backup.Check(t.Context(), fix.target, "bkp-second"); err != nil {
		t.Fatalf("check second backup: %v", err)
	}
}

func TestRunFailsOnBlobMismatch(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		stored  []byte
		declare backup.BlobRef
	}{
		{name: "wrong size", stored: []byte("short"), declare: blobRef("tofu/ws1/sv1/a1", []byte("a longer state"))},
		{
			name:    "wrong md5",
			stored:  []byte("tampered-state!!"),
			declare: blobRef("tofu/ws1/sv1/a1", []byte("original-state!!")),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sql := []byte("sqlite-image-bytes")
			fix := newFixture(t, &stubExporter{sum: testSummary(tc.declare), data: sql, err: nil}, 0)
			seedSource(t, fix.source, map[string][]byte{"tofu/ws1/sv1/a1": tc.stored})

			if _, err := fix.service.Run(t.Context(), "bkp-bad"); !errors.Is(err, backup.ErrCorrupt) {
				t.Fatalf("run with a mismatched blob: err = %v, want ErrCorrupt", err)
			}

			assertFailed(t, fix.registry, "bkp-bad")
			assertNoManifest(t, fix.target, "bkp-bad")

			if fix.target.has("blobs/tofu/ws1/sv1/a1") {
				t.Fatal("target holds a copy of the mismatched blob, want none")
			}
		})
	}
}

func TestRunSkipsBlobsTheSQLDataDoesNotName(t *testing.T) {
	t.Parallel()

	named := []byte(`{"state":1}`)
	extra := []byte(`{"state":2}`)
	sql := []byte("sqlite-image-bytes")
	fix := newFixture(t, &stubExporter{sum: testSummary(blobRef("tofu/ws1/sv1/a1", named)), data: sql, err: nil}, 0)
	seedSource(t, fix.source, map[string][]byte{"tofu/ws1/sv1/a1": named, "tofu/ws1/sv9/a1": extra})

	mustRun(t, fix.service, "bkp-skip")

	if fix.target.has("blobs/tofu/ws1/sv9/a1") {
		t.Fatal("target holds a blob the SQL data does not name, want it skipped")
	}

	if err := backup.Check(t.Context(), fix.target, "bkp-skip"); err != nil {
		t.Fatalf("check backup: %v", err)
	}
}

func TestRunFailsWhenExportFails(t *testing.T) {
	t.Parallel()

	fix := newFixture(t, &stubExporter{sum: testSummary(), data: nil, err: errConnectionReset}, 0)

	if _, err := fix.service.Run(t.Context(), "bkp-export"); err == nil {
		t.Fatal("run with a failing export succeeded, want an error")
	}

	assertFailed(t, fix.registry, "bkp-export")
	assertNoManifest(t, fix.target, "bkp-export")
}

// TestRunFailsWhenDatabaseWriteFails fails the db Put at once. The export
// blocks on its next Write until the run closes the pipe, so the run must
// still return promptly with a failed row and no manifest.
func TestRunFailsWhenDatabaseWriteFails(t *testing.T) {
	t.Parallel()

	content := []byte(`{"state":1}`)
	sql := []byte("sqlite-image-bytes")
	fix := newFixture(t, &stubExporter{sum: testSummary(blobRef("tofu/ws1/sv1/a1", content)), data: sql, err: nil}, 0)
	seedSource(t, fix.source, map[string][]byte{"tofu/ws1/sv1/a1": content})
	fix.target.failPut[sqlPartKey("bkp-db")] = errTargetUnavailable

	finished := make(chan error, 1)

	go func() {
		_, err := fix.service.Run(context.Background(), "bkp-db")
		finished <- err
	}()

	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("run with a failing database write succeeded, want an error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run with a failing database write did not return")
	}

	assertFailed(t, fix.registry, "bkp-db")
	assertNoManifest(t, fix.target, "bkp-db")
}

func TestRunWritesManifestLast(t *testing.T) {
	t.Parallel()

	content := []byte(`{"state":1}`)
	sql := []byte("sqlite-image-bytes")
	fix := newFixture(t, &stubExporter{sum: testSummary(blobRef("tofu/ws1/sv1/a1", content)), data: sql, err: nil}, 0)
	seedSource(t, fix.source, map[string][]byte{"tofu/ws1/sv1/a1": content})
	fix.target.failPut["backups/bkp-partial/manifest.json"] = errTargetUnavailable

	if _, err := fix.service.Run(t.Context(), "bkp-partial"); err == nil {
		t.Fatal("run with a failing manifest write succeeded, want an error")
	}

	assertFailed(t, fix.registry, "bkp-partial")

	if got := fix.target.readObject(t, sqlPartKey("bkp-partial")); !bytes.Equal(got, sql) {
		t.Fatalf("backup db = %q, want %q: the manifest is last, so earlier objects stay", got, sql)
	}

	if err := backup.Check(t.Context(), fix.target, "bkp-partial"); !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("check of a backup without a manifest: err = %v, want ErrCorrupt", err)
	}
}

func TestRunRejectsASecondBackupWhileOneRuns(t *testing.T) {
	t.Parallel()

	content := []byte(`{"state":1}`)
	sql := []byte("sqlite-image-bytes")
	exporter := &blockingExporter{
		sum:     testSummary(blobRef("tofu/ws1/sv1/a1", content)),
		data:    sql,
		started: make(chan struct{}),
		release: make(chan struct{}),
		once:    sync.Once{},
	}
	fix := newFixture(t, exporter, 0)
	seedSource(t, fix.source, map[string][]byte{"tofu/ws1/sv1/a1": content})

	firstDone := make(chan error, 1)

	go func() {
		_, err := fix.service.Run(context.Background(), "bkp-first")
		firstDone <- err
	}()

	select {
	case <-exporter.started:
	case <-time.After(10 * time.Second):
		t.Fatal("first backup did not start exporting")
	}

	if _, err := fix.service.Run(t.Context(), "bkp-second"); !errors.Is(err, backup.ErrAlreadyRunning) {
		t.Fatalf("second backup while one runs: err = %v, want ErrAlreadyRunning", err)
	}

	close(exporter.release)

	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first backup: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("first backup did not finish")
	}

	if _, err := fix.registry.GetBackup(t.Context(), "bkp-second"); !errors.Is(err, backup.ErrNotFound) {
		t.Fatalf("second backup row: err = %v, want ErrNotFound", err)
	}
}

func TestRunRefusesSQLPartOverTheTargetLimit(t *testing.T) {
	t.Parallel()

	if backup.S3ObjectLimit != 5*1024*1024*1024 {
		t.Fatalf("S3 object limit = %d, want 5 GiB", backup.S3ObjectLimit)
	}

	content := []byte(`{"state":1}`)
	sql := []byte("a SQL part over the fake limit")
	fix := newFixture(t, &stubExporter{sum: testSummary(blobRef("tofu/ws1/sv1/a1", content)), data: sql, err: nil}, 8)
	seedSource(t, fix.source, map[string][]byte{"tofu/ws1/sv1/a1": content})

	_, err := fix.service.Run(t.Context(), "bkp-big")
	if !errors.Is(err, backup.ErrTooLarge) {
		t.Fatalf("run over the target limit: err = %v, want ErrTooLarge", err)
	}

	if !strings.Contains(err.Error(), "8") {
		t.Fatalf("oversized error = %q, want it to name the limit", err)
	}

	assertFailed(t, fix.registry, "bkp-big")
	assertNoManifest(t, fix.target, "bkp-big")
}

// openExporter writes data to the first table file and returns err without a
// Close or an Abort of the file, as a custom exporter with a bug can (ADR 35).
type openExporter struct {
	data []byte
	err  error
}

func (f *openExporter) ExportPortable(
	_ context.Context, sink backup.PortableSink,
) (backup.Summary, []backup.PortableFile, error) {
	w, err := sink(backup.PortablePath(backup.Tables[0]))
	if err != nil {
		return backup.Summary{}, nil, fmt.Errorf("open test table file: %w", err)
	}

	if _, err := w.Write(f.data); err != nil {
		return backup.Summary{}, nil, fmt.Errorf("write test SQL data: %w", err)
	}

	return backup.Summary{}, nil, f.err
}

// putTracker counts the target Puts that have not returned.
type putTracker struct {
	*memStore

	running atomic.Int64
}

func (p *putTracker) Put(ctx context.Context, key blob.Key, r io.Reader) error {
	p.running.Add(1)
	defer p.running.Add(-1)

	return p.memStore.Put(ctx, key, r)
}

// TestRunFinishesTableFilesTheExporterLeavesOpen proves that Run never returns
// while a target Put still waits on a table file (issue 476). A file that the
// exporter leaves open is aborted, so the target keeps nothing for it.
func TestRunFinishesTableFilesTheExporterLeavesOpen(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
	}{
		{name: "export fails", err: errExport},
		{name: "export succeeds", err: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			target := &putTracker{memStore: newMemStore(), running: atomic.Int64{}}
			registry := newFakeRegistry()

			service, err := backup.NewService(backup.Config{
				Exporter:    &openExporter{data: []byte("a part of a table file"), err: tc.err},
				Kinds:       fixedKind(installation.KindProduction),
				Registry:    registry,
				Blobs:       newMemStore(),
				StateOpener: nil,
				Target:      target,
				TargetName:  "mem:target",
				Now:         time.Now,
				Version:     "test-1.0",
				MaxSQLBytes: 0,
			})
			if err != nil {
				t.Fatalf("new service: %v", err)
			}

			if _, err := service.Run(t.Context(), "bkp-open"); err == nil {
				t.Fatal("run with a table file left open: err = nil, want an error")
			}

			if n := target.running.Load(); n != 0 {
				t.Fatalf("target Puts running after Run returned = %d, want 0", n)
			}

			key := "backups/bkp-open/" + backup.PortablePath(backup.Tables[0])
			if target.has(key) {
				t.Fatalf("target holds %s, want no object for an aborted file", key)
			}

			assertFailed(t, registry, "bkp-open")
		})
	}
}

func TestCheck(t *testing.T) {
	t.Parallel()

	newComplete := func(t *testing.T) (*memStore, backup.ID) {
		t.Helper()

		content := []byte(`{"state":1}`)
		sql := []byte("sqlite-image-bytes")
		fix := newFixture(
			t,
			&stubExporter{sum: testSummary(blobRef("tofu/ws1/sv1/a1", content)), data: sql, err: nil},
			0,
		)
		seedSource(t, fix.source, map[string][]byte{"tofu/ws1/sv1/a1": content})
		mustRun(t, fix.service, "bkp-check")

		return fix.target, "bkp-check"
	}

	cases := []struct {
		name    string
		breakIt func(t *testing.T, target *memStore)
		wantKey string
	}{
		{
			name:    "missing blob",
			breakIt: func(t *testing.T, target *memStore) { t.Helper(); mustDelete(t, target, "blobs/tofu/ws1/sv1/a1") },
			wantKey: "blobs/tofu/ws1/sv1/a1",
		},
		{
			name: "wrong blob size",
			breakIt: func(t *testing.T, target *memStore) {
				t.Helper()
				mustDelete(t, target, "blobs/tofu/ws1/sv1/a1")
				target.storeObject(t, "blobs/tofu/ws1/sv1/a1", []byte("short"))
			},
			wantKey: "blobs/tofu/ws1/sv1/a1",
		},
		{
			name:    "missing database",
			breakIt: func(t *testing.T, target *memStore) { t.Helper(); mustDelete(t, target, sqlPartKey("bkp-check")) },
			wantKey: sqlPartKey("bkp-check"),
		},
		{
			name: "missing manifest",
			breakIt: func(t *testing.T, target *memStore) {
				t.Helper()
				mustDelete(t, target, "backups/bkp-check/manifest.json")
			},
			wantKey: "manifest.json",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			target, id := newComplete(t)
			tc.breakIt(t, target)

			err := backup.Check(t.Context(), target, id)
			if !errors.Is(err, backup.ErrCorrupt) {
				t.Fatalf("check: err = %v, want ErrCorrupt", err)
			}

			if !strings.Contains(err.Error(), tc.wantKey) {
				t.Fatalf("check error = %q, want it to name %q", err, tc.wantKey)
			}
		})
	}
}

func mustDelete(t *testing.T, target *memStore, key string) {
	t.Helper()

	if err := target.Delete(t.Context(), mustKey(t, key)); err != nil {
		t.Fatalf("delete %q: %v", key, err)
	}
}

func TestNewServiceRefusesMissingDependencies(t *testing.T) {
	t.Parallel()

	registry := newFakeRegistry()
	source := newMemStore()
	target := newMemStore()
	exporter := &stubExporter{sum: testSummary(), data: nil, err: nil}

	cases := []struct {
		name   string
		change func(cfg *backup.Config)
	}{
		{name: "no exporter", change: func(cfg *backup.Config) { cfg.Exporter = nil }},
		{name: "no registry", change: func(cfg *backup.Config) { cfg.Registry = nil }},
		{name: "no blob store", change: func(cfg *backup.Config) { cfg.Blobs = nil }},
		{name: "no target", change: func(cfg *backup.Config) { cfg.Target = nil }},
		{name: "no kind reader", change: func(cfg *backup.Config) { cfg.Kinds = nil }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := backup.Config{
				Exporter:    exporter,
				Kinds:       fixedKind(installation.KindProduction),
				Registry:    registry,
				Blobs:       source,
				StateOpener: nil,
				Target:      target,
				TargetName:  "",
				Now:         nil,
				Version:     "",
				MaxSQLBytes: 0,
			}
			tc.change(&cfg)

			if _, err := backup.NewService(cfg); !errors.Is(err, backup.ErrMissingDependency) {
				t.Fatalf("new service: err = %v, want ErrMissingDependency", err)
			}
		})
	}
}

func TestCheckRefusesNoTarget(t *testing.T) {
	t.Parallel()

	if err := backup.Check(t.Context(), nil, "bkp-aaa"); !errors.Is(err, backup.ErrMissingDependency) {
		t.Fatalf("check without a target: err = %v, want ErrMissingDependency", err)
	}
}
