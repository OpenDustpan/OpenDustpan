package postgres_test

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
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
		"0100_baseline.sql": "9b4ff3513eed7fd9b9e417e2bf8303de23d9337c4087e3f3776e30e57c612a9e",
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

	// wantTagHashes holds the SHA-256 of each tag migration copy in
	// testdata/tag-migrations, keyed by tag and file name as tag/file. The
	// release process copies the migration set of each tag there at tag time
	// and adds its hashes here in the same change. The check below fails when
	// a copy changes, when a copy has no hash entry, and when a hash entry
	// has no copy. Never edit a copy.
	wantTagHashes := map[string]string{}

	checkTagHashes(t, wantTagHashes)
}

// checkTagHashes proves every tag migration copy matches its hash entry.
func checkTagHashes(t *testing.T, want map[string]string) {
	t.Helper()

	const tagDir = "testdata/tag-migrations"

	entries, err := os.ReadDir(tagDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if len(want) != 0 {
				t.Errorf("tag migration dir %s is missing, want %d hash entries", tagDir, len(want))
			}

			return
		}
		t.Fatalf("read tag migration dir: %v", err)
	}

	seen := make(map[string]bool, len(want))

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		tag := entry.Name()

		files, err := os.ReadDir(filepath.Join(tagDir, tag))
		if err != nil {
			t.Fatalf("read tag migration dir %s: %v", tag, err)
		}

		for _, file := range files {
			if file.IsDir() || filepath.Ext(file.Name()) != ".sql" {
				continue
			}

			key := tag + "/" + file.Name()

			//nolint:gosec // The test reads its own tag migration copies.
			data, err := os.ReadFile(filepath.Join(tagDir, tag, file.Name()))
			if err != nil {
				t.Fatalf("read tag migration %s: %v", key, err)
			}

			wantHash, ok := want[key]
			if !ok {
				t.Errorf(
					"tag migration %s has no hash entry: add its hash in TestFrozenMigrations; "+
						"never edit a tag copy",
					key,
				)

				continue
			}

			sum := sha256.Sum256(data)
			if got := hex.EncodeToString(sum[:]); got != wantHash {
				t.Errorf(
					"tag migration %s hash = %s, want %s: never edit a tag copy",
					key,
					got,
					wantHash,
				)
			}

			seen[key] = true
		}
	}

	for key := range want {
		if !seen[key] {
			t.Errorf(
				"hash entry %s has no tag migration file: never edit a tag copy",
				key,
			)
		}
	}
}
