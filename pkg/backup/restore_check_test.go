package backup_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/secret"
)

// errStageBoom is an injected staging failure.
var errStageBoom = errors.New("stage boom")

// checkLiveStore is a fake backup.CheckStore: a canned live summary and key
// with a canned staged summary. It counts stage and discard calls and tracks
// staging presence, so the tests prove that a check stages once, then removes
// the staging area and writes nothing else.
type checkLiveStore struct {
	mu           sync.Mutex
	live         backup.Summary
	key          secret.WrappedKey
	keyErr       error
	staged       backup.Summary
	stageErr     error
	stageCalls   int
	discardCalls int
	present      bool
}

func (s *checkLiveStore) ExportPortable(
	_ context.Context, _ backup.PortableSink,
) (backup.Summary, []backup.PortableFile, error) {
	return s.live, nil, nil
}

func (s *checkLiveStore) PeekPortableKey(
	_ context.Context, _ backup.PortableOpener, _ backup.PortableFile, wrapperID string,
) (secret.WrappedKey, error) {
	if s.keyErr != nil {
		return secret.WrappedKey{}, s.keyErr
	}

	if s.key.WrapperID != wrapperID {
		return secret.WrappedKey{}, fmt.Errorf(
			"backup holds no data key of wrapper %q: %w", wrapperID, backup.ErrCorrupt,
		)
	}

	return s.key, nil
}

func (s *checkLiveStore) StagePortable(
	_ context.Context, open backup.PortableOpener, _ backup.Summary, files []backup.PortableFile, _ backup.StageOptions,
) (backup.Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.stageCalls++

	if s.stageErr != nil {
		return backup.Summary{}, s.stageErr
	}

	for _, file := range files {
		reader, err := open(file.Path)
		if err != nil {
			return backup.Summary{}, err
		}

		if _, err := io.Copy(io.Discard, reader); err != nil {
			return backup.Summary{}, fmt.Errorf("drain backup file: %w", err)
		}

		if err := reader.Close(); err != nil {
			return backup.Summary{}, fmt.Errorf("close backup file: %w", err)
		}
	}

	s.present = true

	return s.staged, nil
}

func (s *checkLiveStore) DiscardStaging(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.discardCalls++
	s.present = false

	return nil
}

func (s *checkLiveStore) calls() liveCalls {
	s.mu.Lock()
	defer s.mu.Unlock()

	return liveCalls{stage: s.stageCalls, discard: s.discardCalls, present: s.present}
}

// liveCalls counts the staging calls of the fake: stage and discard calls with
// staging presence.
type liveCalls struct {
	stage   int
	discard int
	present bool
}

// checkSetup wires a target with one backup and a live check store.
type checkSetup struct {
	target   *memStore
	live     *checkLiveStore
	manifest backup.Manifest
	id       backup.ID
	db       []byte
	blobs    map[string][]byte
}

func newCheckSetup(t *testing.T) *checkSetup {
	t.Helper()

	target := newMemStore()
	blobs := map[string][]byte{
		"namespaces/ns-aaaaaaaaaaaaaaaaaaaa/tofu/ws1/sv1/a1": []byte(`{"state":1}`),
		"namespaces/ns-aaaaaaaaaaaaaaaaaaaa/tofu/ws1/sv2/a1": []byte(`{"state":2}`),
	}
	dump := restoreDump{
		Keys: []secret.WrappedKey{testWrappedKey(t)},
		Rows: map[string]int64{"namespaces": 1, "stacks": 2},
	}
	db := encodeRestoreDump(t, dump)
	manifest := writeRestoreManifest(t, target, blobs, restoreBlobsInput{
		db:            db,
		dump:          dump,
		installations: []string{"eu-west"},
	})

	live := &checkLiveStore{
		mu: sync.Mutex{},
		live: backup.Summary{
			Dialect:              backup.DialectSQLite,
			PrimaryInstallations: []string{"eu-west"},
			WrapperIDs:           nil,
			RowCounts:            map[string]int64{},
			Blobs:                nil,
			SchemaVersion:        backup.BaselineSchemaVersion,
			Unverified:           nil,
		},
		key:          testWrappedKey(t),
		keyErr:       nil,
		staged:       stagedSummary(t, manifest),
		stageErr:     nil,
		stageCalls:   0,
		discardCalls: 0,
		present:      false,
	}

	return &checkSetup{target: target, live: live, manifest: manifest, id: "bkp-restore", db: db, blobs: blobs}
}

// stagedSummary answers the staged summary a successful stage returns: the
// manifest values the stage checks.
func stagedSummary(t *testing.T, manifest backup.Manifest) backup.Summary {
	t.Helper()

	refs := make([]backup.BlobRef, 0, len(manifest.Blobs))

	for _, ref := range manifest.Blobs {
		raw, err := hex.DecodeString(ref.MD5)
		if err != nil {
			t.Fatalf("decode blob MD5: %v", err)
		}

		var sum [16]byte

		copy(sum[:], raw)
		refs = append(refs, backup.BlobRef{MD5: sum, Key: ref.Key, Size: ref.Size, Sealed: false})
	}

	return backup.Summary{
		Dialect:              manifest.Dialect,
		PrimaryInstallations: manifest.PrimaryInstallations,
		WrapperIDs:           manifest.WrapperIDs,
		RowCounts:            maps.Clone(manifest.RowCounts),
		Blobs:                refs,
		SchemaVersion:        manifest.SchemaVersion,
		Unverified:           nil,
	}
}

// rewriteCheckManifest re-encodes the setup manifest after a test edits it.
func (s *checkSetup) rewriteCheckManifest(t *testing.T) {
	t.Helper()

	data, err := json.Marshal(s.manifest)
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}

	if err := s.target.Delete(t.Context(), mustKey(t, "backups/"+s.id.String()+"/manifest.json")); err != nil {
		t.Fatalf("delete manifest: %v", err)
	}

	s.target.storeObject(t, "backups/"+s.id.String()+"/manifest.json", data)
	s.live.staged = stagedSummary(t, s.manifest)
}

// check runs a server-side restore check with the given key and installation.
func (s *checkSetup) check(t *testing.T, key secret.Wrapper, installation string) (backup.Summary, string, error) {
	t.Helper()

	return backup.CheckRestore(t.Context(), backup.CheckInput{
		Target:       s.target,
		Live:         s.live,
		Key:          key,
		ID:           s.id,
		Installation: installation,
		Stage:        backup.StageOptions{BackupKind: "", AllowDev: false, RestoreAt: time.Time{}},
		AfterStage:   nil,
	})
}

// assertCheckSummary compares two summaries field by field.
func assertCheckSummary(t *testing.T, got, want backup.Summary) {
	t.Helper()

	if got.Dialect != want.Dialect || got.SchemaVersion != want.SchemaVersion ||
		!maps.Equal(got.RowCounts, want.RowCounts) || !equalCheckBlobs(got.Blobs, want.Blobs) ||
		!maps.Equal(sliceSet(got.PrimaryInstallations), sliceSet(want.PrimaryInstallations)) ||
		!maps.Equal(sliceSet(got.WrapperIDs), sliceSet(want.WrapperIDs)) {
		t.Fatalf("staged summary = %+v, want %+v", got, want)
	}
}

// sliceSet answers the set of values, for order-free comparison.
func sliceSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}

	return out
}

// equalCheckBlobs compares two blob reference lists by key.
func equalCheckBlobs(a, b []backup.BlobRef) bool {
	if len(a) != len(b) {
		return false
	}

	byKey := make(map[string]backup.BlobRef, len(a))
	for _, ref := range a {
		byKey[ref.Key] = ref
	}

	for _, ref := range b {
		got, ok := byKey[ref.Key]
		if !ok || got.Size != ref.Size || got.MD5 != ref.MD5 {
			return false
		}
	}

	return true
}

// assertTargetIntact proves that a check-only restore changed no target object:
// the SQL part and every blob still hold their bytes.
func (s *checkSetup) assertTargetIntact(t *testing.T) {
	t.Helper()

	if got := s.target.readObject(t, sqlPartKey(s.id)); !bytes.Equal(got, s.db) {
		t.Fatal("target SQL part changed during a check-only restore, want it untouched")
	}

	for key, want := range s.blobs {
		if got := s.target.readObject(t, "blobs/"+key); !bytes.Equal(got, want) {
			t.Fatalf("target blob %q changed during a check-only restore, want it untouched", key)
		}
	}
}

// assertStagedOnce proves that the check staged once, then removed the staging
// area.
func (s *checkSetup) assertStagedOnce(t *testing.T) {
	t.Helper()

	got := s.live.calls()

	if got != (liveCalls{stage: 1, discard: 1, present: false}) {
		t.Fatalf("staging calls = %+v, want one stage, one discard and no staging area", got)
	}
}

// assertNoStage proves that the check stopped before staging.
func (s *checkSetup) assertNoStage(t *testing.T) {
	t.Helper()

	got := s.live.calls()

	if got != (liveCalls{stage: 0, discard: 0, present: false}) {
		t.Fatalf("staging calls = %+v, want no stage, no discard and no staging area", got)
	}
}

func TestCheckRestoreGood(t *testing.T) {
	t.Parallel()

	s := newCheckSetup(t)

	staged, warning, err := s.check(t, testWrapper(), "eu-west")
	if err != nil {
		t.Fatalf("check restore: %v", err)
	}

	if warning != "" {
		t.Fatalf("warning = %q, want empty for a primary installation", warning)
	}

	assertCheckSummary(t, staged, s.live.staged)
	s.assertStagedOnce(t)
	s.assertTargetIntact(t)
}

func TestCheckRestoreInstallationWarning(t *testing.T) {
	t.Parallel()

	s := newCheckSetup(t)

	_, warning, err := s.check(t, testWrapper(), "us-east")
	if err != nil {
		t.Fatalf("check restore: %v", err)
	}

	if !strings.Contains(warning, "us-east") {
		t.Fatalf("warning = %q, want it to name the installation", warning)
	}

	s.assertStagedOnce(t)
	s.assertTargetIntact(t)
}

func TestCheckRestoreMissingManifest(t *testing.T) {
	t.Parallel()

	s := newCheckSetup(t)
	mustDelete(t, s.target, "backups/"+s.id.String()+"/manifest.json")

	_, _, err := s.check(t, testWrapper(), "")
	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("err = %v, want ErrCorrupt", err)
	}

	s.assertNoStage(t)
}

func TestCheckRestoreUnknownFormat(t *testing.T) {
	t.Parallel()

	s := newCheckSetup(t)
	s.manifest.Format = 99
	s.rewriteCheckManifest(t)

	_, _, err := s.check(t, testWrapper(), "")
	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("err = %v, want ErrCorrupt", err)
	}

	s.assertNoStage(t)
}

// TestCheckRestoreAcrossDialects checks that a portable backup restores into the
// other dialect (ADR 42, decision 9). The manifest dialect names the source only.
func TestCheckRestoreAcrossDialects(t *testing.T) {
	t.Parallel()

	s := newCheckSetup(t)
	s.live.live.Dialect = backup.DialectPostgres

	_, _, err := s.check(t, testWrapper(), "")
	if err != nil {
		t.Fatalf("check across dialects: err = %v, want nil", err)
	}
}

// TestCheckRestoreSchemaBeforeBaseline checks that a backup from before the
// baseline is refused by name, also when the live schema version is the same:
// no binary can restore it.
func TestCheckRestoreSchemaBeforeBaseline(t *testing.T) {
	t.Parallel()

	const old = 76

	s := newCheckSetup(t)
	s.manifest.SchemaVersion = old
	s.rewriteCheckManifest(t)
	s.live.live.SchemaVersion = old

	_, _, err := s.check(t, testWrapper(), "")
	if !errors.Is(err, backup.ErrSchemaBeforeBaseline) {
		t.Fatalf("err = %v, want ErrSchemaBeforeBaseline", err)
	}

	s.assertNoStage(t)
}

func TestCheckRestoreSchemaMismatch(t *testing.T) {
	t.Parallel()

	s := newCheckSetup(t)
	s.live.live.SchemaVersion = backup.BaselineSchemaVersion + 1

	_, _, err := s.check(t, testWrapper(), "")
	if !errors.Is(err, backup.ErrSchemaMismatch) {
		t.Fatalf("err = %v, want ErrSchemaMismatch", err)
	}

	live, baseline := strconv.Itoa(backup.BaselineSchemaVersion+1), strconv.Itoa(backup.BaselineSchemaVersion)
	if !strings.Contains(err.Error(), live) || !strings.Contains(err.Error(), baseline) {
		t.Fatalf("error = %q, want it to name both versions", err)
	}

	s.assertNoStage(t)
	s.assertTargetIntact(t)
}

func TestCheckRestoreWrongKey(t *testing.T) {
	t.Parallel()

	s := newCheckSetup(t)
	wrong := stubWrapper{id: "kek-2", ciphertext: []byte("wrapped-2"), opened: []byte("opened-2"), fail: false}

	_, _, err := s.check(t, wrong, "")
	assertCheckKeyErr(t, err)
	s.assertNoStage(t)
	s.assertTargetIntact(t)
}

func TestCheckRestoreUnopenableKey(t *testing.T) {
	t.Parallel()

	s := newCheckSetup(t)
	bad := stubWrapper{id: "kek-1", ciphertext: []byte("wrapped-1"), opened: nil, fail: true}

	_, _, err := s.check(t, bad, "")
	assertCheckKeyErr(t, err)

	if !errors.Is(err, secret.ErrDecrypt) {
		t.Fatalf("err = %v, want ErrDecrypt", err)
	}

	s.assertNoStage(t)
	s.assertTargetIntact(t)
}

func TestCheckRestoreNoKey(t *testing.T) {
	t.Parallel()

	s := newCheckSetup(t)

	_, _, err := s.check(t, nil, "")
	assertCheckKeyErr(t, err)
	s.assertNoStage(t)
	s.assertTargetIntact(t)
}

// TestCheckRestoreInstallationKeysUnchecked proves that the check refuses a
// backup whose key is not in secret_keys when the store cannot read the
// installation keys, before any write.
func TestCheckRestoreInstallationKeysUnchecked(t *testing.T) {
	t.Parallel()

	s := newCheckSetup(t)
	s.live.keyErr = fmt.Errorf("secret_keys of the backup holds no data key: %w", backup.ErrNoDataKey)

	_, _, err := s.check(t, testWrapper(), "")
	if !errors.Is(err, backup.ErrKeyUnchecked) {
		t.Fatalf("err = %v, want ErrKeyUnchecked", err)
	}

	s.assertNoStage(t)
	s.assertTargetIntact(t)
}

// assertCheckKeyErr proves that a key error names the key-encryption key setting.
func assertCheckKeyErr(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Fatal("check with a wrong key succeeded, want an error")
	}

	if !strings.Contains(err.Error(), "DUSTPAN_KEY_ENCRYPTION_KEY") {
		t.Fatalf("error = %q, want it to name DUSTPAN_KEY_ENCRYPTION_KEY", err)
	}
}

func TestCheckRestoreMissingBlob(t *testing.T) {
	t.Parallel()

	s := newCheckSetup(t)
	mustDelete(t, s.target, "blobs/namespaces/ns-aaaaaaaaaaaaaaaaaaaa/tofu/ws1/sv2/a1")

	_, _, err := s.check(t, testWrapper(), "")
	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("err = %v, want ErrCorrupt", err)
	}

	if !strings.Contains(err.Error(), "blobs/namespaces/ns-aaaaaaaaaaaaaaaaaaaa/tofu/ws1/sv2/a1") {
		t.Fatalf("error = %q, want it to name the missing blob", err)
	}

	s.assertNoStage(t)
}

func TestCheckRestoreTamperedBlob(t *testing.T) {
	t.Parallel()

	s := newCheckSetup(t)
	mustDelete(t, s.target, "blobs/namespaces/ns-aaaaaaaaaaaaaaaaaaaa/tofu/ws1/sv2/a1")
	s.target.storeObject(t, "blobs/namespaces/ns-aaaaaaaaaaaaaaaaaaaa/tofu/ws1/sv2/a1", []byte(`{"state":3}`))

	_, _, err := s.check(t, testWrapper(), "")
	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("err = %v, want ErrCorrupt", err)
	}

	s.assertNoStage(t)
}

func TestCheckRestoreShortBlob(t *testing.T) {
	t.Parallel()

	s := newCheckSetup(t)
	mustDelete(t, s.target, "blobs/namespaces/ns-aaaaaaaaaaaaaaaaaaaa/tofu/ws1/sv1/a1")
	s.target.storeObject(t, "blobs/namespaces/ns-aaaaaaaaaaaaaaaaaaaa/tofu/ws1/sv1/a1", []byte("short"))

	_, _, err := s.check(t, testWrapper(), "")
	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("err = %v, want ErrCorrupt", err)
	}

	s.assertNoStage(t)
}

func TestCheckRestoreCorruptSQLPart(t *testing.T) {
	t.Parallel()

	s := newCheckSetup(t)
	mustDelete(t, s.target, sqlPartKey(s.id))
	s.target.storeObject(t, sqlPartKey(s.id), append(bytes.Clone(s.db), 0x00))

	_, _, err := s.check(t, testWrapper(), "")
	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("err = %v, want ErrCorrupt", err)
	}

	s.assertNoStage(t)
}

func TestCheckRestoreStageFailureRemovesStaging(t *testing.T) {
	t.Parallel()

	s := newCheckSetup(t)
	s.live.stageErr = errStageBoom

	_, _, err := s.check(t, testWrapper(), "")
	if !errors.Is(err, errStageBoom) {
		t.Fatalf("err = %v, want the staging failure", err)
	}

	if got := s.live.calls(); got != (liveCalls{stage: 1, discard: 1, present: false}) {
		t.Fatalf("staging calls = %+v, want one stage, one discard and no staging area", got)
	}

	s.assertTargetIntact(t)
}

// TestCheckRefusesForeignDescriptorPath proves Check and CheckRestore refuse a
// descriptor whose path is not the path of its table, before any object is
// opened, with ErrCorrupt.
func TestCheckRefusesForeignDescriptorPath(t *testing.T) {
	t.Parallel()

	s := newCheckSetup(t)
	renameSecretKeysPath(t, &s.manifest, "manifest.json")
	s.rewriteCheckManifest(t)

	if err := backup.Check(t.Context(), s.target, s.id); !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("check: err = %v, want ErrCorrupt", err)
	}

	_, _, err := s.check(t, testWrapper(), "")
	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("check restore: err = %v, want ErrCorrupt", err)
	}

	s.assertNoStage(t)
}
