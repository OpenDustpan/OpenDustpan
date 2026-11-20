package dustpan_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/internal/store/postgres"
	"opendustpan.org/opendustpan/internal/store/schema"
	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/installation"
)

// TestStoreErrorsMatchOpenFailures proves that an error of OpenStore matches
// the exported store errors, and that the exported helpers answer.
func TestStoreErrorsMatchOpenFailures(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	store, err := dustpan.OpenStore(
		t.Context(), "sqlite:"+path, dustpan.WithStoreInstallationKind(installation.KindDev),
	)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	if _, err = dustpan.OpenStore(t.Context(), "sqlite:"+path); !errors.Is(err, dustpan.ErrStoreInUse) {
		t.Fatalf("second open = %v, want ErrStoreInUse", err)
	}

	if _, err = dustpan.StoredInstallationKind(t.Context(), path); !errors.Is(err, dustpan.ErrStoreInUse) {
		t.Fatalf("StoredInstallationKind on a held file = %v, want ErrStoreInUse", err)
	}

	if !dustpan.IsDevBuildVersion("dev") {
		t.Fatal(`IsDevBuildVersion("dev") = false, want true`)
	}

	if got, err := dustpan.HighestMinimumBinaryVersion(nil); err != nil || got != "" {
		t.Fatalf("HighestMinimumBinaryVersion(nil) = %q, %v, want empty", got, err)
	}
}

// TestStoreErrorsAreTheStoreSentinels proves that each exported store error
// is the sentinel of the store package that returns it. The binary maps these
// errors to exit codes, so a mapping that drifts from its sentinel is a bug.
func TestStoreErrorsAreTheStoreSentinels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		exported error
		sentinel error
	}{
		{"in use", dustpan.ErrStoreInUse, sqlite.ErrInUse},
		{"failover setting", dustpan.ErrStoreFailoverSetting, postgres.ErrFailoverSetting},
		{"server version", dustpan.ErrStoreServerVersion, postgres.ErrServerVersion},
		{"schema diverged", dustpan.ErrStoreSchemaDiverged, schema.ErrSchemaDiverged},
		{"schema before baseline", dustpan.ErrStoreSchemaBeforeBaseline, schema.ErrSchemaBeforeBaseline},
		{"migrations pending", dustpan.ErrStoreMigrationsPending, schema.ErrMigrationsPending},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.exported == nil || tt.sentinel == nil {
				t.Fatal("exported or store sentinel is nil")
			}

			if !errors.Is(tt.exported, tt.sentinel) {
				t.Errorf("errors.Is(exported, store sentinel) = false for %v", tt.sentinel)
			}

			if !errors.Is(fmt.Errorf("open: %w", tt.sentinel), tt.exported) {
				t.Errorf("a wrapped store sentinel does not match the exported error for %v", tt.sentinel)
			}
		})
	}
}
