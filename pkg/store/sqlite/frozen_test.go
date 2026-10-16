package sqlite_test

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/internal/migrate"
)

//go:embed migrations/*.sql
var frozenMigrations embed.FS

// TestFrozenMigrations holds the SHA-256 of each released migration file. It
// fails when a file changes, when a file has no hash entry, and when a hash
// entry has no file. Never edit an applied migration. Add a new numbered file
// and its hash in the same change.
func TestFrozenMigrations(t *testing.T) {
	t.Parallel()

	// wantFrozenHashes holds the SHA-256 of each released migration file. A new
	// migration adds its hash here in the same change. The baseline replaced
	// every migration before it once, before the first release; the hashes
	// start again from it.
	wantFrozenHashes := map[string]string{
		"0100_baseline.sql": "3807356e4ec410075b982f0c651707ef399f0625c440bbd5ebac01d1bd435ee8",
	}

	all, err := migrate.Load(frozenMigrations, "migrations")
	if err != nil {
		t.Fatalf("load embedded migrations: %v", err)
	}

	for _, m := range all {
		want, ok := wantFrozenHashes[m.Name]
		if !ok {
			t.Errorf(
				"migration %s has no hash entry: add its hash in TestFrozenMigrations; "+
					"never edit an applied migration, add a new numbered file for a change",
				m.Name,
			)

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

	for name := range wantFrozenHashes {
		found := false

		for _, m := range all {
			if m.Name == name {
				found = true

				break
			}
		}

		if !found {
			t.Errorf(
				"hash entry %s has no migration file: "+
					"never edit an applied migration; add a new numbered file and its hash",
				name,
			)
		}
	}
}
