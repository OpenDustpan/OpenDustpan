package sqlite_test

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"path"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/internal/migrate"
)

//go:embed migrations/*.sql migrations/*.sql.sha256
var frozenMigrations embed.FS

// TestFrozenMigrations pins the SHA-256 of each migration file to the hash
// file next to it: migrations/NNNN_name.sql.sha256 holds the hash as one hex
// line. It fails when a file changes, when a SQL file has no hash file, and
// when a hash file has no SQL file. Never edit an applied migration. Add a
// new numbered SQL file and its hash file in the same change.
func TestFrozenMigrations(t *testing.T) {
	t.Parallel()

	all, err := migrate.Load(frozenMigrations, "migrations")
	if err != nil {
		t.Fatalf("load embedded migrations: %v", err)
	}

	seen := make(map[string]bool, len(all))

	for _, m := range all {
		seen[m.Name] = true

		want, ok := readFrozenHash(t, m.Name)
		if !ok {
			continue
		}

		sum := sha256.Sum256([]byte(m.SQL))
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Errorf(
				"migration %s hash = %s, want %s: "+
					"never edit an applied migration; add a new numbered file and its hash",
				m.Name,
				got,
				want,
			)
		}
	}

	entries, err := frozenMigrations.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sha256") {
			continue
		}

		name, _ := strings.CutSuffix(entry.Name(), ".sha256")
		if !seen[name] {
			t.Errorf(
				"hash file %s has no migration file: "+
					"never edit an applied migration; add a new numbered file and its hash",
				entry.Name(),
			)
		}
	}
}

// readFrozenHash returns the SHA-256 that the hash file of name records, or
// false after a failure when the file is missing or holds no single hex hash.
func readFrozenHash(t *testing.T, name string) (string, bool) {
	t.Helper()

	data, err := frozenMigrations.ReadFile(path.Join("migrations", name+".sha256"))
	if err != nil {
		t.Errorf(
			"migration %s has no hash file: add %s.sha256 with its SHA-256; "+
				"never edit an applied migration, add a new numbered file for a change",
			name,
			name,
		)

		return "", false
	}

	want := strings.TrimSpace(string(data))

	raw, err := hex.DecodeString(want)
	if err != nil || len(raw) != sha256.Size {
		t.Errorf(
			"hash file %s.sha256 must hold the SHA-256 of %s as one hex line",
			name,
			name,
		)

		return "", false
	}

	return want, true
}
