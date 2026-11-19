package dustpan_test

import (
	"errors"
	"path/filepath"
	"testing"

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
