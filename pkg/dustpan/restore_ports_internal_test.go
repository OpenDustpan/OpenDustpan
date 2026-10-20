package dustpan

import (
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// baseRestoreStore is a store with the restore ports that existed before the
// dev data rules, and none of the three ports of the rules.
type baseRestoreStore interface {
	namespace.Store
	backup.CheckStore
	backup.Applier
	backup.Fencer
	backup.RestoreRegistry
	backup.Registry
	backup.PortableExporter
}

// The three stores below hold the base ports and two of the three dev data
// ports. An embedded interface promotes only its own methods, so each store
// lacks exactly one port, and a type assertion to backup.RestoreLive fails.
type (
	withoutKindReader struct {
		baseRestoreStore
		backup.DevStripper
		backup.DevReportRecorder
	}
	withoutStripper struct {
		baseRestoreStore
		backup.KindReader
		backup.DevReportRecorder
	}
	withoutRecorder struct {
		baseRestoreStore
		backup.KindReader
		backup.DevStripper
	}
)

// TestNewRestoreServiceNeedsDevPorts builds a store that lacks each dev data
// port in turn. With a backup target, the start fails with errRestoreNeedsLive
// and the text names the missing port. A production restore never skips the
// kind check, the strip or the report because a port is missing, so the
// server does not start a restore service without them (layer 4c).
func TestNewRestoreServiceNeedsDevPorts(t *testing.T) {
	t.Parallel()

	base := openRestoreStore(t)

	tests := []struct {
		name    string
		store   namespace.Store
		missing string
	}{
		{
			name:    "no KindReader",
			store:   withoutKindReader{baseRestoreStore: base, DevStripper: base, DevReportRecorder: base},
			missing: "KindReader",
		},
		{
			name:    "no DevStripper",
			store:   withoutStripper{baseRestoreStore: base, KindReader: base, DevReportRecorder: base},
			missing: "DevStripper",
		},
		{
			name:    "no DevReportRecorder",
			store:   withoutRecorder{baseRestoreStore: base, KindReader: base, DevStripper: base},
			missing: "DevReportRecorder",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			service, err := newRestoreService(
				addBackupTarget(t, restoreSettings(t, tc.store)),
				restoreDeps(t, base),
				nil,
			)
			checkMissingPort(t, service, err, tc.missing)
		})
	}
}

// checkMissingPort proves that the start failed with errRestoreNeedsLive,
// that the text names the missing port, and that it names no other port.
func checkMissingPort(t *testing.T, service *backup.RestoreService, err error, missing string) {
	t.Helper()

	if err == nil || service != nil {
		t.Fatalf("restore service = %v, %v, want a start error and no service", service, err)
	}

	if !errors.Is(err, errRestoreNeedsLive) || !strings.Contains(err.Error(), missing) {
		t.Fatalf("error = %q, want errRestoreNeedsLive naming %s", err, missing)
	}

	// Another port must not appear in the list.
	for _, other := range []string{"KindReader", "DevStripper", "DevReportRecorder"} {
		if other != missing && strings.Contains(err.Error(), other) {
			t.Fatalf("error = %q names %s, which the store holds", err, other)
		}
	}
}

// TestNewRestoreServiceWithEveryPort proves that a store with every port and
// a backup target gets a restore service.
func TestNewRestoreServiceWithEveryPort(t *testing.T) {
	t.Parallel()

	base := openRestoreStore(t)

	// The service only holds the maintenance service; the test never enters it.
	service, err := newRestoreService(
		addBackupTarget(t, restoreSettings(t, base)),
		restoreDeps(t, base),
		new(maintenance.Service),
	)
	if err != nil || service == nil {
		t.Fatalf("restore service = %v, %v, want a service", service, err)
	}
}

// TestNewRestoreServiceWithoutTargetNeedsNoPort proves that no backup target
// needs no port.
func TestNewRestoreServiceWithoutTargetNeedsNoPort(t *testing.T) {
	t.Parallel()

	base := openRestoreStore(t)
	store := withoutKindReader{baseRestoreStore: base, DevStripper: base, DevReportRecorder: base}

	service, err := newRestoreService(restoreSettings(t, store), restoreDeps(t, base), nil)
	if err != nil || service != nil {
		t.Fatalf("restore service = %v, %v, want none and no error", service, err)
	}
}

// openRestoreStore opens a SQLite store that holds every restore port.
func openRestoreStore(t *testing.T) *sqlite.Store {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	return st
}

// restoreSettings returns the settings that newRestoreService reads, with no
// backup target.
func restoreSettings(t *testing.T, store namespace.Store) *settings {
	t.Helper()

	cfg := defaultSettings()
	cfg.namespaceStore = store
	cfg.logger = slog.New(slog.DiscardHandler)

	return cfg
}

// openRestoreBlobs opens a file blob store in a new directory and closes it
// at the end of the test.
func openRestoreBlobs(t *testing.T, name string) *fs.Store {
	t.Helper()

	store, err := fs.Open(filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}

	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("close %s: %v", name, closeErr)
		}
	})

	return store
}

// addBackupTarget gives the settings a backup target and a live blob store.
func addBackupTarget(t *testing.T, cfg *settings) *settings {
	t.Helper()

	cfg.backupTarget = openRestoreBlobs(t, "target")
	cfg.blobStore = openRestoreBlobs(t, "live-blobs")

	return cfg
}

// restoreDeps returns installation dependencies with a backup service over
// the store.
func restoreDeps(t *testing.T, store *sqlite.Store) installationDeps {
	t.Helper()

	backups, err := backup.NewService(backup.Config{
		Exporter:    store,
		Kinds:       store,
		Registry:    store,
		Operations:  store.DurableOperationsPort(),
		Owner:       "",
		Lease:       0,
		Logger:      nil,
		Blobs:       openRestoreBlobs(t, "blobs"),
		StateOpener: nil,
		Target:      openRestoreBlobs(t, "backup-target"),
		TargetName:  "dir:test",
		Now:         nil,
		Version:     "test",
		MaxSQLBytes: 0,
	})
	if err != nil {
		t.Fatalf("backup service: %v", err)
	}

	var deps installationDeps

	deps.backups = backups

	return deps
}
