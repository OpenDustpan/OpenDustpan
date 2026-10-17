package sqlite_test

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"maps"
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
		"0100_baseline.sql": "ca7a884be85b4c000d5db6f1ea6d5de04e33eb65faba283685363142a7bcc16d",
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

	got := readTagCopies(t, tagDir, want)
	checkTagHashesAgainst(t, want, got)
}

// readTagCopies reads the content of every tag migration copy, keyed by tag
// and file name as tag/file. It returns nothing when the tag directory is
// missing.
func readTagCopies(t *testing.T, tagDir string, want map[string]string) map[string][]byte {
	t.Helper()

	entries, err := os.ReadDir(tagDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if len(want) != 0 {
				t.Errorf("tag migration dir %s is missing, want %d hash entries", tagDir, len(want))
			}

			return nil
		}
		t.Fatalf("read tag migration dir: %v", err)
	}

	got := make(map[string][]byte)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		maps.Copy(got, readOneTagDir(t, tagDir, entry.Name()))
	}

	return got
}

// readOneTagDir reads the migration copies of one tag, keyed by tag and file
// name as tag/file.
func readOneTagDir(t *testing.T, tagDir, tag string) map[string][]byte {
	t.Helper()

	files, err := os.ReadDir(filepath.Join(tagDir, tag))
	if err != nil {
		t.Fatalf("read tag migration dir %s: %v", tag, err)
	}

	got := make(map[string][]byte)

	for _, file := range files {
		if skipTagFile(file) {
			continue
		}

		key := tag + "/" + file.Name()

		//nolint:gosec // The test reads its own tag migration copies.
		data, err := os.ReadFile(filepath.Join(tagDir, tag, file.Name()))
		if err != nil {
			t.Fatalf("read tag migration %s: %v", key, err)
		}

		got[key] = data
	}

	return got
}

// skipTagFile reports whether a tag directory entry holds no migration.
func skipTagFile(file os.DirEntry) bool {
	return file.IsDir() || filepath.Ext(file.Name()) != ".sql"
}

// checkTagHashesAgainst proves every copy matches its hash entry and every
// hash entry has a copy.
func checkTagHashesAgainst(t *testing.T, want map[string]string, got map[string][]byte) {
	t.Helper()

	seen := make(map[string]bool, len(want))

	for key, data := range got {
		checkOneTagHash(t, key, data, want)
		if _, ok := want[key]; ok {
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

// checkOneTagHash proves one tag migration copy matches its hash entry.
func checkOneTagHash(t *testing.T, key string, data []byte, want map[string]string) {
	t.Helper()

	wantHash, ok := want[key]
	if !ok {
		t.Errorf(
			"tag migration %s has no hash entry: add its hash in TestFrozenMigrations; "+
				"never edit a tag copy",
			key,
		)

		return
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
}
