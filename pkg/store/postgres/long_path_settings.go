package postgres

import "opendustpan.org/opendustpan/pkg/setting"

// Keys of the installation settings that show the bounds of the store. Each
// bound is a code rule: no flag, variable or file key changes it, and the
// settings resource shows the value of the code with the source "default".
const (
	// SettingStatementTimeout is the key of the statement timeout of each
	// connection of the pool.
	SettingStatementTimeout = "database.postgres.statementTimeout"
	// SettingIdleInTransactionTimeout is the key of the idle-in-transaction
	// timeout of each connection of the pool.
	SettingIdleInTransactionTimeout = "database.postgres.idleInTransactionTimeout"
	// SettingClientConnectionCheckInterval is the key of the interval at
	// which the PostgreSQL server checks that a client of a connection of the
	// store is still there.
	SettingClientConnectionCheckInterval = "database.postgres.clientConnectionCheckInterval"
	// SettingBackupStatementTimeout is the key of the statement timeout of a
	// backup, a restore and the paths that copy the rows of a backup.
	SettingBackupStatementTimeout = "database.postgres.backup.statementTimeout"
	// SettingBackupIdleInTransactionTimeout is the key of the
	// idle-in-transaction timeout of a backup, a restore and the paths that
	// copy the rows of a backup.
	SettingBackupIdleInTransactionTimeout = "database.postgres.backup.idleInTransactionTimeout"
	// SettingSchemaChangeStatementTimeout is the key of the statement timeout
	// of the creation and the removal of a staging schema.
	SettingSchemaChangeStatementTimeout = "database.postgres.schemaChange.statementTimeout"
	// SettingSchemaChangeIdleInTransactionTimeout is the key of the
	// idle-in-transaction timeout of the creation and the removal of a
	// staging schema.
	SettingSchemaChangeIdleInTransactionTimeout = "database.postgres.schemaChange.idleInTransactionTimeout"
	// SettingMigrationTimeout is the key of the statement timeout and the
	// idle-in-transaction timeout of the migration transaction.
	SettingMigrationTimeout = "database.postgres.migration.timeout"
)

// SettingDeclarations returns the declarations of the bounds of the store,
// for the settings resource of the installation. Each declaration is read
// only: it shows the value of the code with the source "default", and the
// database layer holds no value. Register the declarations only for a
// PostgreSQL store.
func SettingDeclarations() []setting.Declaration {
	return []setting.Declaration{
		{
			Key: SettingStatementTimeout, Level: setting.LevelInstallation, Kind: setting.KindString,
			Default: serverStatementTimeout.String(), Secret: false, Database: false,
			StaticCredentialHost: false, Apply: setting.ApplyRestart, Check: nil, ReducesSecurity: nil,
			Doc: "The statement timeout of each connection of the PostgreSQL store.",
		},
		{
			Key: SettingIdleInTransactionTimeout, Level: setting.LevelInstallation, Kind: setting.KindString,
			Default: serverIdleInTxTimeout.String(), Secret: false, Database: false,
			StaticCredentialHost: false, Apply: setting.ApplyRestart, Check: nil, ReducesSecurity: nil,
			Doc: "The idle-in-transaction timeout of each connection of the PostgreSQL store.",
		},
		{
			Key: SettingClientConnectionCheckInterval, Level: setting.LevelInstallation, Kind: setting.KindString,
			Default: clientCheckInterval.String(), Secret: false, Database: false,
			StaticCredentialHost: false, Apply: setting.ApplyRestart, Check: nil, ReducesSecurity: nil,
			Doc: "The interval at which the PostgreSQL server checks that a client of a connection of the store " +
				"is still there.",
		},
		{
			Key: SettingBackupStatementTimeout, Level: setting.LevelInstallation, Kind: setting.KindString,
			Default: backupStatementTimeout.String(), Secret: false, Database: false,
			StaticCredentialHost: false, Apply: setting.ApplyRestart, Check: nil, ReducesSecurity: nil,
			Doc: "The statement timeout of a backup, a restore and the copy of the rows of a backup in the " +
				"PostgreSQL store.",
		},
		{
			Key: SettingBackupIdleInTransactionTimeout, Level: setting.LevelInstallation, Kind: setting.KindString,
			Default: backupIdleTimeout.String(), Secret: false, Database: false,
			StaticCredentialHost: false, Apply: setting.ApplyRestart, Check: nil, ReducesSecurity: nil,
			Doc: "The idle-in-transaction timeout of a backup, a restore and the copy of the rows of a backup in " +
				"the PostgreSQL store.",
		},
		{
			Key: SettingSchemaChangeStatementTimeout, Level: setting.LevelInstallation, Kind: setting.KindString,
			Default: schemaStatementTimeout.String(), Secret: false, Database: false,
			StaticCredentialHost: false, Apply: setting.ApplyRestart, Check: nil, ReducesSecurity: nil,
			Doc: "The statement timeout of the creation and the removal of a staging schema in the PostgreSQL store.",
		},
		{
			Key: SettingSchemaChangeIdleInTransactionTimeout, Level: setting.LevelInstallation,
			Kind:    setting.KindString,
			Default: schemaIdleTimeout.String(), Secret: false, Database: false,
			StaticCredentialHost: false, Apply: setting.ApplyRestart, Check: nil, ReducesSecurity: nil,
			Doc: "The idle-in-transaction timeout of the creation and the removal of a staging schema in the " +
				"PostgreSQL store.",
		},
		{
			Key: SettingMigrationTimeout, Level: setting.LevelInstallation, Kind: setting.KindString,
			Default: migrationDeadline.String(), Secret: false, Database: false,
			StaticCredentialHost: false, Apply: setting.ApplyRestart, Check: nil, ReducesSecurity: nil,
			Doc: "The statement timeout and the idle-in-transaction timeout of the migration transaction of the " +
				"PostgreSQL store.",
		},
	}
}
