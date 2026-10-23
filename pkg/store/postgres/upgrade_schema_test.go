package postgres_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/store/internal/migrate"
	"opendustpan.org/opendustpan/pkg/store/postgres"
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

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL schema upgrade suite", envURL)
	}

	for _, tag := range tags {
		t.Run(tag, func(t *testing.T) {
			t.Parallel()
			checkUpgradeSchemaFromTag(t, base, tag)
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

// checkUpgradeSchemaFromTag migrates one schema from the copies of tag to
// the current schema and proves it matches a fresh install.
func checkUpgradeSchemaFromTag(t *testing.T, base, tag string) {
	t.Helper()

	ctx := t.Context()

	tagMigrations := loadTagMigrations(t, tag)
	if len(tagMigrations) == 0 {
		t.Fatalf("tag %s holds no migrations", tag)
	}

	upgradedURL := emptySchemaURL(t, base)
	applyTagMigrations(ctx, t, upgradedURL, tagMigrations)

	upgraded, err := postgres.Open(ctx, upgradedURL)
	if err != nil {
		t.Fatalf("open upgraded store from tag %s: %v", tag, err)
	}

	upgraded.Close()

	freshURL := emptySchemaURL(t, base)

	fresh, err := postgres.Open(ctx, freshURL)
	if err != nil {
		t.Fatalf("open fresh store: %v", err)
	}

	fresh.Close()

	want := readSchemaSnapshot(t, freshURL)
	got := readSchemaSnapshot(t, upgradedURL)

	compareSnapshots(t, tag, want, got)
}

// loadTagMigrations returns the ordered migration copies of tag.
func loadTagMigrations(t *testing.T, tag string) []migrate.Migration {
	t.Helper()

	all, err := migrate.Load(os.DirFS(filepath.Join("testdata", "tag-migrations", tag)), ".")
	if err != nil {
		t.Fatalf("load tag migrations %s: %v", tag, err)
	}

	return all
}

// applyTagMigrations runs the tag migrations on the schema at schemaURL, one
// transaction per migration, and records each one as the store does.
func applyTagMigrations(ctx context.Context, t *testing.T, schemaURL string, tagMigrations []migrate.Migration) {
	t.Helper()

	conn, err := pgx.Connect(ctx, schemaURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(ctx); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	for _, m := range tagMigrations {
		err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			// No arguments, so pgx uses the simple protocol, which allows several statements.
			if _, err := tx.Exec(ctx, m.SQL); err != nil {
				return fmt.Errorf("apply tag migration %s: %w", m.Name, err)
			}

			return recordTagMigration(ctx, tx, m)
		})
		if err != nil {
			t.Fatalf("apply tag migrations: %v", err)
		}
	}
}

// recordTagMigration records m in schema_migrations of tx, after its SQL
// ran. It records the minimum binary version when the column exists.
func recordTagMigration(ctx context.Context, tx pgx.Tx, m migrate.Migration) error {
	var exists bool

	err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'schema_migrations'
			AND column_name = 'min_binary_version'
	)`).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check schema_migrations columns: %w", err)
	}

	if !exists {
		const record = `INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`
		if _, err := tx.Exec(ctx, record, m.Version, m.Name); err != nil {
			return fmt.Errorf("record tag migration %s: %w", m.Name, err)
		}

		return nil
	}

	const record = `INSERT INTO schema_migrations (version, name, min_binary_version) VALUES ($1, $2, $3)`
	if _, err := tx.Exec(ctx, record, m.Version, m.Name, m.MinBinaryVersion); err != nil {
		return fmt.Errorf("record tag migration %s: %w", m.Name, err)
	}

	return nil
}

// schemaSnapshot is the comparable schema of one database: the recorded
// versions, the table names, the columns of each table, the index
// definitions and the constraint definitions.
type schemaSnapshot struct {
	versions    []int64
	tables      []string
	columns     map[string][]string
	indexes     []string
	constraints []string
}

// readSchemaSnapshot reads the snapshot of the schema at schemaURL.
func readSchemaSnapshot(t *testing.T, schemaURL string) schemaSnapshot {
	t.Helper()

	ctx := t.Context()

	conn, err := pgx.Connect(ctx, schemaURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(ctx); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	return schemaSnapshot{
		versions:    readSnapshotVersions(ctx, t, conn),
		tables:      readSnapshotTables(ctx, t, conn),
		columns:     readSnapshotColumns(ctx, t, conn),
		indexes:     readSnapshotIndexes(ctx, t, conn),
		constraints: readSnapshotConstraints(ctx, t, conn),
	}
}

// readSnapshotVersions reads the recorded migration versions, ordered.
func readSnapshotVersions(ctx context.Context, t *testing.T, conn *pgx.Conn) []int64 {
	t.Helper()

	rows, err := conn.Query(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("read schema versions: %v", err)
	}

	defer rows.Close()

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

// readSnapshotTables reads the table names of the current schema, ordered.
func readSnapshotTables(ctx context.Context, t *testing.T, conn *pgx.Conn) []string {
	t.Helper()

	rows, err := conn.Query(ctx,
		`SELECT tablename FROM pg_tables WHERE schemaname = current_schema() ORDER BY tablename`,
	)
	if err != nil {
		t.Fatalf("read tables: %v", err)
	}

	defer rows.Close()

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
func readSnapshotColumns(ctx context.Context, t *testing.T, conn *pgx.Conn) map[string][]string {
	t.Helper()

	tables := readSnapshotTables(ctx, t, conn)
	out := make(map[string][]string, len(tables))

	const query = `SELECT column_name, data_type, is_nullable, COALESCE(column_default, '')` +
		` FROM information_schema.columns` +
		` WHERE table_schema = current_schema() AND table_name = $1 ORDER BY ordinal_position`

	for _, table := range tables {
		rows, err := conn.Query(ctx, query, table)
		if err != nil {
			t.Fatalf("read columns of %s: %v", table, err)
		}

		var columns []string

		for rows.Next() {
			var name, dataType, nullable, dflt string
			if err := rows.Scan(&name, &dataType, &nullable, &dflt); err != nil {
				rows.Close()
				t.Fatalf("scan column of %s: %v", table, err)
			}

			columns = append(columns, name+"|"+dataType+"|"+nullable+"|"+dflt)
		}

		rows.Close()

		if err := rows.Err(); err != nil {
			t.Fatalf("read columns of %s: %v", table, err)
		}

		out[table] = columns
	}

	return out
}

// currentSchema returns the current schema of conn.
func currentSchema(ctx context.Context, t *testing.T, conn *pgx.Conn) string {
	t.Helper()

	var schema string
	if err := conn.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatalf("read current schema: %v", err)
	}

	return schema
}

// stripSchema removes the schema qualifier from a definition, so two
// snapshots from schemas with other names compare equal.
func stripSchema(definition, schema string) string {
	stripped := strings.ReplaceAll(definition, `"`+schema+`".`, "")
	stripped = strings.ReplaceAll(stripped, schema+".", "")

	return stripped
}

// readSnapshotIndexes reads the index definitions of the current schema,
// ordered by name, with the schema qualifier removed.
func readSnapshotIndexes(ctx context.Context, t *testing.T, conn *pgx.Conn) []string {
	t.Helper()

	schema := currentSchema(ctx, t, conn)

	rows, err := conn.Query(ctx,
		`SELECT indexname, tablename, indexdef FROM pg_indexes WHERE schemaname = current_schema() ORDER BY indexname`,
	)
	if err != nil {
		t.Fatalf("read indexes: %v", err)
	}

	defer rows.Close()

	var indexes []string

	for rows.Next() {
		var name, table, definition string
		if err := rows.Scan(&name, &table, &definition); err != nil {
			t.Fatalf("scan index: %v", err)
		}

		indexes = append(indexes, name+"|"+table+"|"+stripSchema(definition, schema))
	}

	if err := rows.Err(); err != nil {
		t.Fatalf("read indexes: %v", err)
	}

	return indexes
}

// readSnapshotConstraints reads the constraint definitions of the current
// schema, ordered by name, with the schema qualifier removed.
func readSnapshotConstraints(ctx context.Context, t *testing.T, conn *pgx.Conn) []string {
	t.Helper()

	schema := currentSchema(ctx, t, conn)

	rows, err := conn.Query(ctx, `SELECT c.conname, c.contype, pg_get_constraintdef(c.oid)`+
		` FROM pg_constraint c JOIN pg_namespace n ON n.oid = c.connamespace`+
		` WHERE n.nspname = current_schema() ORDER BY c.conname`)
	if err != nil {
		t.Fatalf("read constraints: %v", err)
	}

	defer rows.Close()

	var constraints []string

	for rows.Next() {
		var name string

		var kind byte

		var definition string

		if err := rows.Scan(&name, &kind, &definition); err != nil {
			t.Fatalf("scan constraint: %v", err)
		}

		constraints = append(constraints, name+"|"+string(kind)+"|"+stripSchema(definition, schema))
	}

	if err := rows.Err(); err != nil {
		t.Fatalf("read constraints: %v", err)
	}

	return constraints
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

	if !slices.Equal(got.constraints, want.constraints) {
		t.Fatalf("tag %s: constraints %v, want fresh %v", tag, got.constraints, want.constraints)
	}
}
