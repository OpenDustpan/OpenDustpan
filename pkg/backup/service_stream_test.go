package backup_test

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // The test computes the MD5 values the manifest records.
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/maintenance"
)

// errSealedOpen is the injected failure of opening a tampered sealed blob.
var errSealedOpen = errors.New("open sealed test blob")

// streamOpener is a fake backup.StateOpener: it opens sealed test keys to
// their plain bytes, or fails them with a canned error.
type streamOpener struct {
	plain map[string][]byte
	fail  map[string]error
}

func (o *streamOpener) OpenStateBlob(_ context.Context, key blob.Key) (io.ReadCloser, error) {
	if err, ok := o.fail[key.String()]; ok {
		return nil, err
	}

	plain, ok := o.plain[key.String()]
	if !ok {
		return nil, fmt.Errorf("open test blob %q: %w", key.String(), blob.ErrNotFound)
	}

	return io.NopCloser(bytes.NewReader(plain)), nil
}

// streamBlobKey is the blob key every stream test copies.
const streamBlobKey = "namespaces/ns-aaaaaaaaaaaaaaaaaaaa/tofu/ws1/sv1/a1"

// largeStreamBytes answers size deterministic pseudo-random bytes, over the
// 32 KiB copy buffer, so the stream tests move multi-chunk blobs.
func largeStreamBytes(size int) []byte {
	out := make([]byte, size)
	state := uint64(0x9E3779B97F4A7C15)

	for i := range out {
		state = state*6364136223846793005 + 1442695040888963407
		out[i] = byte(state >> 33) //nolint:gosec // Test data fills each byte with low bits on purpose.
	}

	return out
}

// streamSummary answers the export summary of one stream test: the SQLite
// dialect with the baseline schema version, so the restore checks pass it.
func streamSummary(refs ...backup.BlobRef) backup.Summary {
	return backup.Summary{
		Dialect:              backup.DialectSQLite,
		PrimaryInstallations: []string{"eu-west"},
		WrapperIDs:           []string{"kek-1"},
		RowCounts:            map[string]int64{"namespaces": 1},
		Blobs:                refs,
		SchemaVersion:        backup.BaselineSchemaVersion,
		Unverified:           nil,
	}
}

// sealedBlobRef names the sealed stream test blob: the size and MD5 stay
// those of the plain bytes, as the state version stores them.
func sealedBlobRef(plain []byte) backup.BlobRef {
	sum := md5.Sum(plain) //nolint:gosec // The test computes the MD5 values the manifest records.

	return backup.BlobRef{Key: streamBlobKey, Size: int64(len(plain)), MD5: sum, Sealed: true}
}

// streamFixture wires a Service with an in-memory source and target and the
// given state opener.
type streamFixture struct {
	service  *backup.Service
	registry *fakeRegistry
	source   *memStore
	target   *memStore
}

// newStreamFixture wires a Service with an in-memory source and target and
// the given state opener.
func newStreamFixture(
	t *testing.T, exporter backup.PortableExporter, opener backup.StateOpener,
) streamFixture {
	t.Helper()

	now := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)
	registry := newFakeRegistry()
	source := newMemStore()
	target := newMemStore()

	service, err := backup.NewService(backup.Config{
		Exporter:    exporter,
		Kinds:       fixedKind(installation.KindProduction),
		Registry:    registry,
		Operations:  registry.ops,
		Blobs:       source,
		StateOpener: opener,
		Target:      target,
		TargetName:  "mem:target",
		Now:         func() time.Time { return now },
		Version:     "test-1.0",
		MaxSQLBytes: 0,
		Owner:       "",
		Lease:       0,
		Logger:      nil,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	return streamFixture{service: service, registry: registry, source: source, target: target}
}

// restoreStreamedBackup restores backup id from target into a fresh live
// blob store through the RestoreService, and returns the live store. It
// proves the restore carries the large blobs byte for byte.
func restoreStreamedBackup(
	t *testing.T, target *memStore, manifest backup.Manifest, id backup.ID,
) *memStore {
	t.Helper()

	liveChecks := &checkLiveStore{
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
	regs := newRestoreServiceRegistry()
	order := &orderLog{mu: sync.Mutex{}, events: nil}
	maint := &restoreServiceMaintenance{
		mu:           sync.Mutex{},
		enterErr:     nil,
		enters:       0,
		leaves:       0,
		order:        order,
		state:        maintenance.StateNormal,
		held:         false,
		cancelScript: nil,
	}
	backupRegs := newFakeRegistry()
	blobs := newMemStore()
	live := newRestoreServiceLive(liveChecks, regs)
	live.maint = maint
	backups, err := backup.NewService(backup.Config{
		Exporter: &stubBackupExporter{
			sum: testSummary(), data: []byte("pre-backup-sql"), order: order, hook: atomic.Pointer[func()]{},
		},
		Kinds:       fixedKind(installation.KindProduction),
		Registry:    backupRegs,
		Operations:  backupRegs.ops,
		Blobs:       newMemStore(),
		StateOpener: nil,
		Target:      target,
		TargetName:  "mem:target",
		Now:         nil,
		Version:     "test-1.0",
		MaxSQLBytes: 0,
		Owner:       "",
		Lease:       0,
		Logger:      nil,
	})
	if err != nil {
		t.Fatalf("new backup service: %v", err)
	}

	service, err := backup.NewRestoreService(backup.RestoreConfig{
		Live:            live,
		Operations:      live.ops,
		Backups:         backups,
		Maintenance:     maint,
		Blobs:           blobs,
		Target:          target,
		Key:             testWrapper(),
		Installation:    "eu-west",
		AfterApply:      nil,
		Admit:           nil,
		Now:             nil,
		Logger:          nil,
		AllowDevBackups: false,
		AllowDevHint:    "",
		Owner:           "",
		Lease:           0,
		CleanupLockWait: 0,
		RetryBase:       time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new restore service: %v", err)
	}

	regs.setWorker(service)

	restoreID := backup.MintRestoreID()

	startErr := service.Start(t.Context(), restoreID, backup.RestoreRequest{
		BackupID:         id,
		PreBackupID:      "",
		CheckOnly:        false,
		Force:            false,
		RequestedBy:      "tester",
		KeepAccessTables: false,
	})
	if startErr != nil {
		t.Fatalf("start restore: %v", startErr)
	}

	if row := waitRestoreEnd(t, regs, restoreID); row.State != backup.RestoreSucceeded {
		t.Fatalf("restore state = %q, want succeeded (%q)", row.State, row.Error)
	}

	return blobs
}

func TestRunStreamsLargePlainBlob(t *testing.T) {
	t.Parallel()

	plain := largeStreamBytes(256 << 10)
	sql := []byte("stream-test-sql")
	fix := newStreamFixture(
		t, &stubExporter{sum: streamSummary(blobRef(streamBlobKey, plain)), data: sql, err: nil}, nil,
	)
	seedSource(t, fix.source, map[string][]byte{streamBlobKey: plain})

	done := mustRun(t, fix.service, "bkp-stream-plain")

	if done.State != backup.StateSucceeded {
		t.Fatalf("finished row = %+v, want succeeded", done)
	}

	if err := backup.Check(t.Context(), fix.target, "bkp-stream-plain"); err != nil {
		t.Fatalf("check streamed backup: %v", err)
	}

	if got := fix.target.readObject(t, "blobs/"+streamBlobKey); !bytes.Equal(got, plain) {
		t.Fatal("target blob differs from the source blob, want it byte for byte")
	}

	manifest, err := fix.service.ReadManifest(t.Context(), "bkp-stream-plain")
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	live := restoreStreamedBackup(t, fix.target, manifest, "bkp-stream-plain")

	if got := live.readObject(t, streamBlobKey); !bytes.Equal(got, plain) {
		t.Fatal("restored blob differs from the source blob, want it byte for byte")
	}
}

func TestRunStreamsLargeSealedBlob(t *testing.T) {
	t.Parallel()

	plain := largeStreamBytes(256 << 10)
	sealed := append([]byte("sealed-v1:"), plain...)
	sql := []byte("stream-test-sql")
	opener := &streamOpener{plain: map[string][]byte{streamBlobKey: plain}, fail: map[string]error{}}
	fix := newStreamFixture(
		t, &stubExporter{sum: streamSummary(sealedBlobRef(plain)), data: sql, err: nil}, opener,
	)
	seedSource(t, fix.source, map[string][]byte{streamBlobKey: sealed})

	done := mustRun(t, fix.service, "bkp-stream-sealed")

	if done.State != backup.StateSucceeded {
		t.Fatalf("finished row = %+v, want succeeded", done)
	}

	if err := backup.Check(t.Context(), fix.target, "bkp-stream-sealed"); err != nil {
		t.Fatalf("check streamed backup: %v", err)
	}

	if got := fix.target.readObject(t, "blobs/"+streamBlobKey); !bytes.Equal(got, sealed) {
		t.Fatal("target blob differs from the sealed source blob, want it byte for byte")
	}

	manifest, err := fix.service.ReadManifest(t.Context(), "bkp-stream-sealed")
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	if len(manifest.Blobs) != 1 || !manifest.Blobs[0].Sealed || manifest.Blobs[0].Size != int64(len(sealed)) {
		t.Fatalf("manifest blobs = %+v, want one sealed entry of %d bytes", manifest.Blobs, len(sealed))
	}

	sealedMD5 := md5.Sum(sealed) //nolint:gosec // The test recomputes the MD5 the manifest records.

	if manifest.Blobs[0].MD5 != hex.EncodeToString(sealedMD5[:]) {
		t.Fatalf("manifest MD5 = %q, want the MD5 of the sealed bytes", manifest.Blobs[0].MD5)
	}

	live := restoreStreamedBackup(t, fix.target, manifest, "bkp-stream-sealed")

	if got := live.readObject(t, streamBlobKey); !bytes.Equal(got, sealed) {
		t.Fatal("restored blob differs from the sealed source blob, want it byte for byte")
	}
}

// seedSealedBackup wires one sealed backup with stored as the live sealed
// bytes and ref as the SQL data reference.
func seedSealedBackup(
	t *testing.T, opener backup.StateOpener, stored []byte, ref backup.BlobRef,
) (*backup.Service, *fakeRegistry, *memStore) {
	t.Helper()

	sql := []byte("stream-test-sql")
	fix := newStreamFixture(t, &stubExporter{sum: streamSummary(ref), data: sql, err: nil}, opener)
	seedSource(t, fix.source, map[string][]byte{ref.Key: stored})

	return fix.service, fix.registry, fix.target
}

// assertBackupFailedWithNoCopy proves the failed backup left no manifest and
// no blob copy in the target.
func assertBackupFailedWithNoCopy(
	t *testing.T, registry *fakeRegistry, target *memStore, id backup.ID,
) {
	t.Helper()

	assertFailed(t, registry, id)
	assertNoManifest(t, target, id)

	if target.has("blobs/" + streamBlobKey) {
		t.Fatalf("target holds a copy of the bad blob %q, want none", streamBlobKey)
	}
}

func TestRunFailsOnSealedBlobMismatch(t *testing.T) {
	t.Parallel()

	plain := []byte("original-plain-state")
	sealed := append([]byte("sealed-v1:"), plain...)

	t.Run("wrong plain size", func(t *testing.T) {
		t.Parallel()

		opener := &streamOpener{plain: map[string][]byte{streamBlobKey: plain}, fail: map[string]error{}}
		declare := sealedBlobRef([]byte("a longer plain state"))
		service, registry, target := seedSealedBackup(t, opener, sealed, declare)

		_, err := service.Run(t.Context(), "bkp-sealed-size")
		assertRunFailed(t, err, backup.ErrCorrupt)

		assertBackupFailedWithNoCopy(t, registry, target, "bkp-sealed-size")
	})

	t.Run("wrong plain md5", func(t *testing.T) {
		t.Parallel()

		opener := &streamOpener{plain: map[string][]byte{streamBlobKey: plain}, fail: map[string]error{}}
		declare := sealedBlobRef([]byte("tampered-plain-state"))
		service, registry, target := seedSealedBackup(t, opener, sealed, declare)

		_, err := service.Run(t.Context(), "bkp-sealed-md5")
		assertRunFailed(t, err, backup.ErrCorrupt)

		assertBackupFailedWithNoCopy(t, registry, target, "bkp-sealed-md5")
	})

	t.Run("tampered sealed blob", func(t *testing.T) {
		t.Parallel()

		opener := &streamOpener{plain: map[string][]byte{}, fail: map[string]error{streamBlobKey: errSealedOpen}}
		service, registry, target := seedSealedBackup(t, opener, []byte("tampered-sealed"), sealedBlobRef(plain))

		_, err := service.Run(t.Context(), "bkp-sealed-tampered")
		if err == nil {
			t.Fatal("run with a tampered sealed blob succeeded, want an error")
		}

		if !strings.Contains(err.Error(), streamBlobKey) {
			t.Fatalf("open error = %q, want it to name the blob key", err)
		}

		assertBackupFailedWithNoCopy(t, registry, target, "bkp-sealed-tampered")
	})

	t.Run("sealed blob without opener", func(t *testing.T) {
		t.Parallel()

		service, registry, target := seedSealedBackup(t, nil, sealed, sealedBlobRef(plain))

		_, err := service.Run(t.Context(), "bkp-sealed-noopener")
		assertRunFailed(t, err, backup.ErrCorrupt)

		assertBackupFailedWithNoCopy(t, registry, target, "bkp-sealed-noopener")
	})
}
