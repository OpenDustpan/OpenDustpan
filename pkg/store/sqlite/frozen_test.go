package sqlite_test

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/releaserecord"
	"opendustpan.org/opendustpan/pkg/store/internal/migrate"
	"opendustpan.org/opendustpan/pkg/store/schema"
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

	// Off until the release record lists a shipped migration. Until then the
	// schema of each dialect is one baseline file, and a change to the schema
	// edits it in place.
	if why := currentRecord(t).Off("TestFrozenMigrations"); why != "" {
		t.Skip(why)
	}

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

// TestMigrationFilesFollowTheRecord checks the migrations directory against
// the release record. Before the first tag, it must hold the baseline alone.
// After the first tag, each shipped file stays as it shipped, and a change to
// the schema adds a new numbered file above them.
func TestMigrationFilesFollowTheRecord(t *testing.T) {
	t.Parallel()

	all, err := migrate.Load(frozenMigrations, "migrations")
	if err != nil {
		t.Fatalf("load embedded migrations: %v", err)
	}

	files := make(map[string]string, len(all))
	for _, m := range all {
		files[m.Name] = m.SQL
	}

	for _, problem := range currentRecord(t).MigrationFiles("sqlite", schema.BaselineName, files) {
		t.Error(problem)
	}
}

// currentRecord returns the release record of the tree. A record that does
// not parse fails the test: it never counts as "not released".
func currentRecord(t *testing.T) releaserecord.Record {
	t.Helper()

	rec, err := releaserecord.Current()
	if err != nil {
		t.Fatal(err)
	}

	return rec
}
