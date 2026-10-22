package postgres

// Test-only helpers for the black-box staging tests in package postgres_test.
// This file compiles into the test binary only: nothing here ships in
// production. Each helper wraps one unexported staging step, so the tests can
// edit an export stream and a staging schema by hand. No port writes an
// orphan, so the foreign-key tests need this surgery. The _internal_test.go
// name keeps the white-box package under the testpackage exception in
// .golangci.yml; the staging tests themselves stay in package postgres_test.

import (
	"context"
	"fmt"
	"slices"

	"opendustpan.org/opendustpan/pkg/backup"
)

// StagingSchemaPrefixForTest is the prefix of every staging schema name.
const StagingSchemaPrefixForTest = stagingSchemaPrefix

// CopyColumnsForTest returns the COPY column list of every table of
// pgCopyTables, by table name.
func CopyColumnsForTest() map[string][]string {
	out := make(map[string][]string, len(backup.Tables))

	for _, spec := range pgCopyTables() {
		out[spec.table] = slices.Clone(spec.columns)
	}

	return out
}

// CheckCopyColumnsForTest compares the COPY column list of table with the
// live columns of the table. It names the table and the column of a
// mismatch, so the coverage test fails loudly.
func CheckCopyColumnsForTest(table string, listed, live []string) error {
	return backup.CheckColumns(table, listed, live)
}

// NewStagingSchemaForTest returns a fresh staging schema name.
func NewStagingSchemaForTest() (string, error) {
	return newStagingSchema()
}

// CreateStagingSchemaForTest creates the staging schema with its migrated
// tables, without loading any data.
func (s *Store) CreateStagingSchemaForTest(ctx context.Context, staging string) error {
	return s.createStagingSchema(ctx, staging)
}

// SummarizeStagingForTest reads the summary of the staging schema against
// want, as StageRestore does after the load.
func (s *Store) SummarizeStagingForTest(
	ctx context.Context, staging string, want backup.Summary,
) (backup.Summary, error) {
	return s.summarizeStaging(ctx, staging, want)
}

// DeleteStagedMigrationForTest removes one migration record from the staging
// schema_migrations, so the staged schema version differs from the manifest.
func (s *Store) DeleteStagedMigrationForTest(ctx context.Context, staging string, version int) error {
	_, err := s.pool.Exec(
		ctx,
		"DELETE FROM "+stagingTable(staging, "schema_migrations")+" WHERE version = $1",
		version,
	)
	if err != nil {
		return fmt.Errorf("delete staged migration record: %w", err)
	}

	return nil
}

// DropStagingSchemaForTest removes the staging schema with its tables.
func (s *Store) DropStagingSchemaForTest(ctx context.Context, staging string) error {
	return s.dropStagingSchema(ctx, staging)
}

// SchemaVersionForTest returns the schema version of the live database.
func (s *Store) SchemaVersionForTest(ctx context.Context) (int, error) {
	return s.schemaVersion(ctx)
}

// StagingSchemaForTest returns the name of the kept staging schema, or empty
// when no staging area exists.
func (s *Store) StagingSchemaForTest() string {
	s.stagingMu.Lock()
	defer s.stagingMu.Unlock()

	return s.stagingSchema
}

// InterleaveProfileRead implements storetest.ProfileReadInterleaver: fn runs
// once in the next read of a profile revision, between the read of the
// profile and the read of the revision.
func (s *Store) InterleaveProfileRead(fn func()) {
	s.betweenProfileReads = fn
}
