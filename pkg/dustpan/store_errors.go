package dustpan

import (
	"context"

	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/internal/store/postgres"
	"opendustpan.org/opendustpan/internal/store/schema"
	"opendustpan.org/opendustpan/internal/store/sqlite"
)

// The errors below are the errors of the built-in stores. They are the same
// values that the store packages return, so errors.Is matches an error from
// OpenStore with them. They let a program that embeds the SDK, such as the
// binary, tell the refusals apart without importing a store package.
var (
	// ErrStoreInUse reports a SQLite file that another process holds.
	ErrStoreInUse = sqlite.ErrInUse
	// ErrStoreFailoverSetting reports a failover setting of a PostgreSQL
	// store that is not valid.
	ErrStoreFailoverSetting = postgres.ErrFailoverSetting
	// ErrStoreServerVersion reports a PostgreSQL server version that the
	// store does not support.
	ErrStoreServerVersion = postgres.ErrServerVersion
	// ErrStoreSchemaDiverged reports a database whose schema differs from the
	// schema of the binary.
	ErrStoreSchemaDiverged = schema.ErrSchemaDiverged
	// ErrStoreSchemaBeforeBaseline reports a database whose schema is older
	// than the baseline schema of the binary.
	ErrStoreSchemaBeforeBaseline = schema.ErrSchemaBeforeBaseline
	// ErrStoreMigrationsPending reports a database that has migrations to
	// apply, when the open does not migrate.
	ErrStoreMigrationsPending = schema.ErrMigrationsPending
)

// StoredInstallationKind reads the installation kind that the SQLite file at
// path records, without a migration. It fails with ErrStoreInUse when another
// process holds the file, and with an error for a value that is not a known
// kind.
func StoredInstallationKind(ctx context.Context, path string) (installation.Kind, error) {
	return sqlite.StoredKind(ctx, path)
}

// IsDevBuildVersion reports whether version names a dev build. A dev build
// counts as newer than every release.
func IsDevBuildVersion(version string) bool {
	return schema.IsDevBuild(version)
}

// HighestMinimumBinaryVersion returns the highest of the minimum binary
// versions that values hold, or an empty string when values has none.
func HighestMinimumBinaryVersion(values []string) (string, error) {
	return schema.HighestMinimum(values)
}
