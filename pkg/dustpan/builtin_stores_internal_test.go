package dustpan

import (
	"path/filepath"
	"testing"
)

// TestOpenStoreWiresTheJobClaimer proves that the built-in SQLite store sets
// the job store and the job claimer. Without the claimer, a poll of a runner
// of a pool for jobs finds no work.
func TestOpenStoreWiresTheJobClaimer(t *testing.T) {
	t.Parallel()

	st, err := OpenStore(t.Context(), "sqlite:"+filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close the database: %v", closeErr)
		}
	})

	cfg := defaultSettings()
	WithBuiltinStore(st)(cfg)

	if applyErr := applyBuiltinStore(cfg); applyErr != nil {
		t.Fatalf("apply the built-in store: %v", applyErr)
	}

	if cfg.jobStore == nil || cfg.jobClaimer == nil {
		t.Fatalf("job store = %v, job claimer = %v; want both", cfg.jobStore, cfg.jobClaimer)
	}
}
