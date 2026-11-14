package sqlite_test

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
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

		if !entry.Type().IsRegular() {
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

	copies, countErr := countTagCopies(dir)
	if countErr != nil {
		t.Fatal(countErr)
	}

	if copies == 0 {
		t.Errorf(
			"tag migration dir %s holds no .sql copy: run scripts/copy-tag-migrations.sh <tag>",
			dir,
		)
	}
}

// countTagCopies returns the number of regular files in dir whose name ends in
// .sql. A hash file (NAME.sql.sha256) and a subdirectory do not count.
func countTagCopies(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("read tag migration dir %s: %w", dir, err)
	}

	count := 0

	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}

		if strings.HasSuffix(entry.Name(), ".sql") {
			count++
		}
	}

	return count, nil
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

// TestTagCopiesHaveTheirLines proves that each tag directory under
// testdata/tag-migrations has its lines in the release record, with the
// SHA-256 of each copy. It runs before and after the first tag.
//
// Defense in depth: the release build refuses a tag without its lines, and
// TestMigrationFilesFollowTheRecord holds the shipped files to the record.
// This check stays so that a later removal of the lines of a tag fails even
// when both of those layers see an empty record.
func TestTagCopiesHaveTheirLines(t *testing.T) {
	t.Parallel()

	rec := currentRecord(t)
	copies := os.DirFS("testdata/tag-migrations")

	tags, err := fs.ReadDir(copies, ".")
	if err != nil {
		t.Fatalf("read the tag copies: %v", err)
	}

	for _, tag := range tags {
		if !tag.IsDir() {
			continue
		}

		files := map[string]string{}

		sqlFiles, err := fs.Glob(copies, tag.Name()+"/*.sql")
		if err != nil {
			t.Fatalf("list the copies of %s: %v", tag.Name(), err)
		}

		for _, name := range sqlFiles {
			data, err := fs.ReadFile(copies, name)
			if err != nil {
				t.Fatalf("read the copy %s: %v", name, err)
			}

			files[path.Base(name)] = string(data)
		}

		for _, problem := range rec.TagCopies("sqlite", tag.Name(), files) {
			t.Error(problem)
		}
	}
}

// TestCountTagCopies pins the count of tag migration copies. checkTagDir fails
// on a count of 0, so this test is the check of what counts as a copy.
func TestCountTagCopies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		files []string
		dirs  []string
		want  int
	}{
		{name: "empty", files: nil, dirs: nil, want: 0},
		{name: "hash file only", files: []string{"0001_x.sql.sha256"}, dirs: nil, want: 0},
		{name: "copy only", files: []string{"0001_x.sql"}, dirs: nil, want: 1},
		{name: "copy and hash file", files: []string{"0001_x.sql", "0001_x.sql.sha256"}, dirs: nil, want: 1},
		{name: "subdirectory named like a copy", files: nil, dirs: []string{"x.sql"}, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			checkCountTagCopies(t, tt.files, tt.dirs, tt.want)
		})
	}
}

// checkCountTagCopies builds the fixture of one case in a new directory and
// checks the count that countTagCopies returns for it.
func checkCountTagCopies(t *testing.T, files, dirs []string, want int) {
	t.Helper()

	dir := t.TempDir()

	for _, name := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	for _, name := range dirs {
		if err := os.Mkdir(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	got, err := countTagCopies(dir)
	if err != nil {
		t.Fatal(err)
	}

	if got != want {
		t.Errorf("countTagCopies() = %d, want %d", got, want)
	}
}

// TestCheckTagDirSkipsNonRegularEntries proves that checkTagDir skips an entry
// that is not a regular file. A symlink named like a copy or like a hash file
// does not count as a copy and does not fail the check.
func TestCheckTagDirSkipsNonRegularEntries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		link   string
		target string
	}{
		{
			name:   "a symlink named like a copy",
			link:   "0002_link.sql",
			target: "0001_baseline.sql",
		},
		{
			name:   "a symlink named like a hash file",
			link:   "0002_link.sql.sha256",
			target: "0001_baseline.sql.sha256",
		},
		{
			name:   "a dangling symlink named like a copy",
			link:   "0003_gone.sql",
			target: "gone.sql",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			writeTagCopy(t, dir, "0001_baseline.sql", "select 1;")

			if err := os.Symlink(tt.target, filepath.Join(dir, tt.link)); err != nil {
				t.Fatal(err)
			}

			checkTagDir(t, dir)
		})
	}
}

// writeTagCopy writes one tag migration copy into dir with the hash file that
// checkTagCopy expects next to it.
func writeTagCopy(t *testing.T, dir, name, data string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256([]byte(data))
	hash := []byte(hex.EncodeToString(sum[:]) + "\n")

	if err := os.WriteFile(filepath.Join(dir, name+".sha256"), hash, 0o600); err != nil {
		t.Fatal(err)
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
