package dustpan

import (
	"errors"
	"io"
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

	real := openRestoreStore(t)

	tests := []struct {
		name    string
		store   namespace.Store
		missing string
	}{
		{
			name:    "no KindReader",
			store:   withoutKindReader{baseRestoreStore: real, DevStripper: real, DevReportRecorder: real},
			missing: "KindReader",
		},
		{
			name:    "no DevStripper",
			store:   withoutStripper{baseRestoreStore: real, KindReader: real, DevReportRecorder: real},
			missing: "DevStripper",
		},
		{
			name:    "no DevReportRecorder",
			store:   withoutRecorder{baseRestoreStore: real, KindReader: real, DevStripper: real},
			missing: "DevReportRecorder",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			service, err := newRestoreService(restoreSettings(t, tc.store, true), restoreDeps(t, real), nil)
			if err == nil || service != nil {
				t.Fatalf("restore service = %v, %v, want a start error and no service", service, err)
			}

			if !errors.Is(err, errRestoreNeedsLive) || !strings.Contains(err.Error(), tc.missing) {
				t.Fatalf("error = %q, want errRestoreNeedsLive naming %s", err, tc.missing)
			}

			// Another port must not appear in the list.
			for _, other := range []string{"KindReader", "DevStripper", "DevReportRecorder"} {
				if other != tc.missing && strings.Contains(err.Error(), other) {
					t.Fatalf("error = %q names %s, which the store holds", err, other)
				}
			}
		})
	}

	t.Run("a store with every port and a target", func(t *testing.T) {
		t.Parallel()

		// The service only holds the maintenance service; the test never enters it.
		service, err := newRestoreService(restoreSettings(t, real, true), restoreDeps(t, real), new(maintenance.Service))
		if err != nil || service == nil {
			t.Fatalf("restore service = %v, %v, want a service", service, err)
		}
	})

	t.Run("no backup target needs no port", func(t *testing.T) {
		t.Parallel()

		service, err := newRestoreService(restoreSettings(t, tests[0].store, false), restoreDeps(t, real), nil)
		if err != nil || service != nil {
			t.Fatalf("restore service = %v, %v, want none and no error", service, err)
		}
	})
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

// restoreSettings returns the settings that newRestoreService reads.
func restoreSettings(t *testing.T, store namespace.Store, withTarget bool) *settings {
	t.Helper()

	cfg := defaultSettings()
	cfg.namespaceStore = store
	cfg.logger = slog.New(slog.NewTextHandler(io.Discard, nil))

	if withTarget {
		target, err := fs.Open(filepath.Join(t.TempDir(), "target"))
		if err != nil {
			t.Fatalf("open backup target: %v", err)
		}

		t.Cleanup(func() {
			if closeErr := target.Close(); closeErr != nil {
				t.Errorf("close backup target: %v", closeErr)
			}
		})

		cfg.backupTarget = target

		blobs, blobsErr := fs.Open(filepath.Join(t.TempDir(), "live-blobs"))
		if blobsErr != nil {
			t.Fatalf("open blob store: %v", blobsErr)
		}

		t.Cleanup(func() {
			if closeErr := blobs.Close(); closeErr != nil {
				t.Errorf("close blob store: %v", closeErr)
			}
		})

		cfg.blobStore = blobs
	}

	return cfg
}

// restoreDeps returns installation dependencies with a backup service over
// the real store.
func restoreDeps(t *testing.T, real *sqlite.Store) installationDeps {
	t.Helper()

	blobs, err := fs.Open(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	target, err := fs.Open(filepath.Join(t.TempDir(), "backup-target"))
	if err != nil {
		t.Fatalf("open backup target: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := target.Close(); closeErr != nil {
			t.Errorf("close backup target: %v", closeErr)
		}
	})

	backups, err := backup.NewService(backup.Config{
		Exporter: real, Kinds: real, Registry: real, Blobs: blobs, StateOpener: nil, Target: target,
		TargetName: "dir:test", Now: nil, Version: "test", MaxSQLBytes: 0,
	})
	if err != nil {
		t.Fatalf("backup service: %v", err)
	}

	return installationDeps{backups: backups}
}
