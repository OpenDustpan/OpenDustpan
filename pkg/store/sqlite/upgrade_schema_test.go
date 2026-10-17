package sqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/internal/migrate"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// TestUpgradeSchemaFromReleasedTags applies the migration copies of each
// released tag, then the current migrations, and compares the schema with a
// fresh install. The copies live in testdata/tag-migrations, one directory
// per tag, and the frozen hash test protects them. With no tag the test logs
// that it ran over 0 tags and passes.
func TestUpgradeSchemaFromReleasedTags(t *testing.T) {
	t.Parallel()

	tags := listReleasedTags(t)

	t.Logf("schema upgrade ran over %d tags: %s", len(tags), strings.Join(tags, ", "))

	for _, tag := range tags {
		t.Run(tag, func(t *testing.T) {
			t.Parallel()
			checkUpgradeSchemaFromTag(t, tag)
		})
	}
}

// listReleasedTags returns the sorted names of the tag directories in
// testdata/tag-migrations that hold at least one .sql file. It returns
// nothing when the directory is missing or holds no tag.
func listReleasedTags(t *testing.T) []string {
	t.Helper()

	const tagDir = "testdata/tag-migrations"

	entries, err := os.ReadDir(tagDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		t.Fatalf("read tag migration dir: %v", err)
	}

	var tags []string

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		files, err := os.ReadDir(filepath.Join(tagDir, entry.Name()))
		if err != nil {
			t.Fatalf("read tag migration dir %s: %v", entry.Name(), err)
		}

		for _, file := range files {
			if !file.IsDir() && filepath.Ext(file.Name()) == ".sql" {
				tags = append(tags, entry.Name())

				break
			}
		}
	}

	slices.Sort(tags)

	return tags
}

// checkUpgradeSchemaFromTag migrates one database from the copies of tag to
// the current schema and proves it matches a fresh install.
func checkUpgradeSchemaFromTag(t *testing.T, tag string) {
	t.Helper()

	ctx := t.Context()

	tagMigrations := loadTagMigrations(t, tag)
	if len(tagMigrations) == 0 {
		t.Fatalf("tag %s holds no migrations", tag)
	}

	dir := t.TempDir()
	upgradedPath := filepath.Join(dir, "upgraded.db")
	freshPath := filepath.Join(dir, "fresh.db")

	applyTagMigrations(ctx, t, upgradedPath, tagMigrations)

	upgraded, err := sqlite.Open(ctx, upgradedPath)
	if err != nil {
		t.Fatalf("open upgraded store from tag %s: %v", tag, err)
	}

	if err := upgraded.Close(); err != nil {
		t.Fatalf("close upgraded store from tag %s: %v", tag, err)
	}

	fresh, err := sqlite.Open(ctx, freshPath)
	if err != nil {
		t.Fatalf("open fresh store: %v", err)
	}

	if err := fresh.Close(); err != nil {
		t.Fatalf("close fresh store: %v", err)
	}

	want := readSchemaSnapshot(t, freshPath)
	got := readSchemaSnapshot(t, upgradedPath)

	compareSnapshots(t, tag, want, got)
}

// loadTagMigrations returns the ordered migration copies of tag.
func loadTagMigrations(t *testing.T, tag string) []migrate.Migration {
	t.Helper()

	all, err := migrate.Load(os.DirFS(filepath.Join("testdata/tag-migrations", tag)), ".")
	if err != nil {
		t.Fatalf("load tag migrations %s: %v", tag, err)
	}

	return all
}

// applyTagMigrations runs the tag migrations on a new database at path, one
// transaction per migration, and records each one as the store does.
func applyTagMigrations(ctx context.Context, t *testing.T, path string, tagMigrations []migrate.Migration) {
	t.Helper()

	conn, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	}()

	for _, m := range tagMigrations {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("begin tag migration %s: %v", m.Name, err)
		}

		if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
			_ = tx.Rollback() //nolint:errcheck // The test fails on the statement error.

			t.Fatalf("apply tag migration %s: %v", m.Name, err)
		}

		if err := recordTagMigration(ctx, t, tx, m); err != nil {
			_ = tx.Rollback() //nolint:errcheck // The test fails on the record error.

			t.Fatalf("record tag migration %s: %v", m.Name, err)
		}

		if err := tx.Commit(); err != nil {
			t.Fatalf("commit tag migration %s: %v", m.Name, err)
		}
	}
}

// recordTagMigration records m in schema_migrations of tx, after its SQL
// ran. It records the minimum binary version when the column exists.
func recordTagMigration(ctx context.Context, t *testing.T, tx *sql.Tx, m migrate.Migration) error {
	t.Helper()

	var count int64

	err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('schema_migrations') WHERE name = 'min_binary_version'`,
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("check schema_migrations columns: %w", err)
	}

	if count == 0 {
		const record = `INSERT INTO schema_migrations (version, name) VALUES (?, ?)`
		if _, err := tx.ExecContext(ctx, record, m.Version, m.Name); err != nil {
			return fmt.Errorf("record tag migration %s: %w", m.Name, err)
		}

		return nil
	}

	const record = `INSERT INTO schema_migrations (version, name, min_binary_version) VALUES (?, ?, ?)`
	if _, err := tx.ExecContext(ctx, record, m.Version, m.Name, m.MinBinaryVersion); err != nil {
		return fmt.Errorf("record tag migration %s: %w", m.Name, err)
	}

	return nil
}

// schemaSnapshot is the comparable schema of one database: the recorded
// versions, the table names, the columns of each table and the index
// definitions.
type schemaSnapshot struct {
	versions []int64
	tables   []string
	columns  map[string][]string
	indexes  []string
}

// readSchemaSnapshot reads the snapshot of the database at path.
func readSchemaSnapshot(t *testing.T, path string) schemaSnapshot {
	t.Helper()

	conn, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	}()

	ctx := t.Context()

	return schemaSnapshot{
		versions: readSnapshotVersions(ctx, t, conn),
		tables:   readSnapshotTables(ctx, t, conn),
		columns:  readSnapshotColumns(ctx, t, conn),
		indexes:  readSnapshotIndexes(ctx, t, conn),
	}
}

// readSnapshotVersions reads the recorded migration versions, ordered.
func readSnapshotVersions(ctx context.Context, t *testing.T, conn *sql.DB) []int64 {
	t.Helper()

	rows, err := conn.QueryContext(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("read schema versions: %v", err)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("close schema versions: %v", closeErr)
		}
	}()

	var versions []int64

	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			t.Fatalf("scan schema version: %v", err)
		}

		versions = append(versions, version)
	}

	if err := rows.Err(); err != nil {
		t.Fatalf("read schema versions: %v", err)
	}

	return versions
}

// readSnapshotTables reads the table names, ordered, without the internal
// tables of SQLite.
func readSnapshotTables(ctx context.Context, t *testing.T, conn *sql.DB) []string {
	t.Helper()

	rows, err := conn.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`,
	)
	if err != nil {
		t.Fatalf("read tables: %v", err)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("close tables: %v", closeErr)
		}
	}()

	var tables []string

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table: %v", err)
		}

		tables = append(tables, name)
	}

	if err := rows.Err(); err != nil {
		t.Fatalf("read tables: %v", err)
	}

	return tables
}

// readSnapshotColumns reads the columns of each table, keyed by table name.
func readSnapshotColumns(ctx context.Context, t *testing.T, conn *sql.DB) map[string][]string {
	t.Helper()

	tables := readSnapshotTables(ctx, t, conn)
	out := make(map[string][]string, len(tables))

	for _, table := range tables {
		//nolint:gosec // The table name comes from the schema of a database the test made.
		rows, err := conn.QueryContext(ctx,
			`SELECT cid, name, type, "notnull", dflt_value, pk FROM pragma_table_info("`+
				strings.ReplaceAll(table, `"`, `""`)+`") ORDER BY cid`,
		)
		if err != nil {
			t.Fatalf("read columns of %s: %v", table, err)
		}

		var columns []string

		for rows.Next() {
			var cid, notnull, pk int64

			var name, colType string

			var dflt sql.NullString

			if err := rows.Scan(&cid, &name, &colType, &notnull, &dflt, &pk); err != nil {
				_ = rows.Close() //nolint:errcheck // The test fails on the scan error.

				t.Fatalf("scan column of %s: %v", table, err)
			}

			def := "NULL"
			if dflt.Valid {
				def = dflt.String
			}

			columns = append(columns, fmt.Sprintf("%d|%s|%s|%d|%s|%d", cid, name, colType, notnull, def, pk))
		}

		if err := rows.Err(); err != nil {
			_ = rows.Close() //nolint:errcheck // The test fails on the read error.

			t.Fatalf("read columns of %s: %v", table, err)
		}

		if err := rows.Close(); err != nil {
			t.Fatalf("close columns of %s: %v", table, err)
		}

		out[table] = columns
	}

	return out
}

// readSnapshotIndexes reads the index definitions, ordered by name, without
// the automatic indexes of SQLite, whose SQL is empty.
func readSnapshotIndexes(ctx context.Context, t *testing.T, conn *sql.DB) []string {
	t.Helper()

	rows, err := conn.QueryContext(ctx,
		`SELECT name, tbl_name, sql FROM sqlite_master WHERE type = 'index' AND sql IS NOT NULL ORDER BY name`,
	)
	if err != nil {
		t.Fatalf("read indexes: %v", err)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("close indexes: %v", closeErr)
		}
	}()

	var indexes []string

	for rows.Next() {
		var name, table, definition string
		if err := rows.Scan(&name, &table, &definition); err != nil {
			t.Fatalf("scan index: %v", err)
		}

		indexes = append(indexes, name+"|"+table+"|"+definition)
	}

	if err := rows.Err(); err != nil {
		t.Fatalf("read indexes: %v", err)
	}

	return indexes
}

// compareSnapshots proves the upgraded schema from tag matches the fresh
// install.
func compareSnapshots(t *testing.T, tag string, want, got schemaSnapshot) {
	t.Helper()

	if !slices.Equal(got.versions, want.versions) {
		t.Fatalf("tag %s: versions %v, want fresh %v", tag, got.versions, want.versions)
	}

	if !slices.Equal(got.tables, want.tables) {
		t.Fatalf("tag %s: tables %v, want fresh %v", tag, got.tables, want.tables)
	}

	for _, table := range want.tables {
		if !slices.Equal(got.columns[table], want.columns[table]) {
			t.Fatalf("tag %s: columns of %s %v, want fresh %v", tag, table, got.columns[table], want.columns[table])
		}
	}

	if !slices.Equal(got.indexes, want.indexes) {
		t.Fatalf("tag %s: indexes %v, want fresh %v", tag, got.indexes, want.indexes)
	}
}
