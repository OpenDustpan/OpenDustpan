package dustpan

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// The startup layer: openSQLiteStore refuses a wide database file with its own
// message, before sqlite.Open runs.
func TestOpenSQLiteStoreRefusesWideFiles(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		file string
		mode os.FileMode
	}{
		"database 0644": {"", 0o644},
		"database 0660": {"", 0o660},
		"wal 0644":      {"-wal", 0o644},
		"shm 0604":      {"-shm", 0o604},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			db := filepath.Join(t.TempDir(), "dustpan.db")
			makeFiles(t, db, tc.file, tc.mode)

			st, err := openSQLiteStore(t.Context(), db, newStoreConfig(nil))
			if st != nil {
				if closeErr := st.Close(); closeErr != nil {
					t.Errorf("close: %v", closeErr)
				}
			}

			if !errors.Is(err, sqlite.ErrFileMode) {
				t.Fatalf("openSQLiteStore = %v, want ErrFileMode", err)
			}

			if !strings.Contains(err.Error(), "check sqlite database file") {
				t.Fatalf("error %q does not come from the startup check", err)
			}
		})
	}
}

// makeFiles makes the database file with mode 0600 and the file db+suffix with mode.
func makeFiles(t *testing.T, db, suffix string, mode os.FileMode) {
	t.Helper()

	for _, f := range []string{db, db + suffix} {
		if err := os.WriteFile(f, nil, 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	if err := os.Chmod(db+suffix, mode); err != nil {
		t.Fatalf("chmod: %v", err)
	}
}
