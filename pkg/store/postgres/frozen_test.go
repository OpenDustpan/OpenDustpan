package postgres_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFrozenMigrations checks the frozen files of the store. Until the first
// tag, the store has one schema file, schema.sql, and no numbered migration:
// a change to the schema edits the schema file, and the server refuses a
// database that another version of the file made. The test fails when the
// migrations directory holds a file, so a change cannot add a numbered
// migration that the server would not apply. It also proves that each tag
// copy in testdata/tag-migrations matches the hash file next to it.
func TestFrozenMigrations(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir("migrations")

	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		t.Fatalf("read the migrations directory: %v", err)
	default:
		for _, entry := range entries {
			t.Errorf("migrations/%s: until the first tag, change the schema in schema.sql "+
				"and add no numbered migration", entry.Name())
		}
	}

	checkTagCopies(t, "testdata/tag-migrations")
}

// checkTagCopies proves every tag migration copy matches the hash file next to
// it: testdata/tag-migrations/<tag>/NAME.sql.sha256 holds the SHA-256 of
// NAME.sql as one hex line. The release process copies each tag's migrations
// and their hash files there at tag time. It fails when a copy changes, when a
// copy has no hash file, and when a hash file has no copy. It logs the number
// of tags it checked, which is 0 before the first release.
func checkTagCopies(t *testing.T, tagDir string) {
	t.Helper()

	tags, err := os.ReadDir(tagDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			t.Log("tag copy check ran over 0 tags")

			return
		}

		t.Fatalf("read tag migration dir: %v", err)
	}

	count := 0

	for _, tag := range tags {
		if !tag.IsDir() {
			continue
		}

		count++

		checkTagDir(t, filepath.Join(tagDir, tag.Name()))
	}

	t.Logf("tag copy check ran over %d tags", count)
}

// checkTagDir checks the copies and the hash files of one tag directory.
func checkTagDir(t *testing.T, dir string) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read tag migration dir %s: %v", dir, err)
	}

	for _, entry := range entries {
		name := entry.Name()

		if entry.IsDir() {
			continue
		}

		if strings.HasSuffix(name, ".sql") {
			checkTagCopy(t, dir, name)

			continue
		}

		if strings.HasSuffix(name, ".sql.sha256") {
			copyName := strings.TrimSuffix(name, ".sha256")
			if _, err := os.Stat(filepath.Join(dir, copyName)); err != nil {
				t.Errorf(
					"hash file %s has no tag migration file: never edit a tag copy",
					filepath.Join(dir, name),
				)
			}
		}
	}
}

// checkTagCopy proves one tag migration copy matches the hash file next to it.
func checkTagCopy(t *testing.T, dir, name string) {
	t.Helper()

	copyPath := filepath.Join(dir, name)

	//nolint:gosec // The test reads its own tag migration copies.
	hashData, err := os.ReadFile(copyPath + ".sha256")
	if err != nil {
		t.Errorf(
			"tag migration %s has no hash file: add %s.sha256 next to it; never edit a tag copy",
			copyPath,
			copyPath,
		)

		return
	}

	//nolint:gosec // The test reads its own tag migration copies.
	data, err := os.ReadFile(copyPath)
	if err != nil {
		t.Fatalf("read tag migration %s: %v", copyPath, err)
	}

	want := strings.TrimSpace(string(hashData))

	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		t.Errorf(
			"tag migration %s hash = %s, want %s: never edit a tag copy",
			copyPath,
			got,
			want,
		)
	}
}
