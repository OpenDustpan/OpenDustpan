// Package backup defines the store side of backups (ADR 30): the ports that
// capture the SQL data of an installation and load it back, and the registry
// of the backups table.
//
// A backup holds wrapped data keys but never the key-encryption key that
// unwraps them, so a backup alone cannot decrypt secrets. OpenTofu state
// content travels sealed, as the server stores it: protect the target and
// keep the key apart from it.
//
// The package holds no SQL and no HTTP. Each store adapter (SQLite,
// PostgreSQL) implements these ports in a later change. The conformance suite
// store/storetest.RunBackup exercises them before any adapter does.
//
// Naming: PortableExporter reads, PortableStager stages, Registry tracks the
// backups table, Inspector lists the schema tables, and Summary is the manifest
// subset that the SQL data alone determines. The names say what each port moves, not
// which tool the data belongs to, so the ports stay tool-neutral.
package backup

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/durable"
)

// Dialect names the SQL dialect that holds the data.
type Dialect string

// Supported dialects. An export restores into the same dialect only.
const (
	// DialectSQLite is the single-file store for single-node installs.
	DialectSQLite Dialect = "sqlite"
	// DialectPostgres is the PostgreSQL store for larger installs.
	DialectPostgres Dialect = "postgres"
)

// The tables of durable operations: one row for each operation, one row for
// each recorded step, and one row for each schedule. The sign-in failure
// budget is a windowed count in the database: the live rows stay across the
// swap of a restore, as the claim tokens and the next runs do. So does the
// row of the last audit chain check and the marks of checkpoint mismatches
// do too: a restore must not clear a mark. The counts of the lockout by name
// and the key of their digests stay too: a restore must not end a lock.
const (
	TableDurableOperations = "durable_operations"
	TableDurableSteps      = "durable_steps"
	TableDurableSchedules  = "durable_schedules"
	TableSignInFailures    = "sign_in_failures"
	TableAuditChainCheck   = "audit_chain_check"
	TableAuditChainMark    = "audit_chain_mark"
	TableNameLocks         = "name_locks"
	TableNameLockKey       = "name_lock_key"
	// TableUsedTickets holds the records of the CAS tickets that a server used.
	// A restore keeps the live rows: a backup that brought back older rows
	// would let a ticket that a server used after the backup work again.
	TableUsedTickets = "used_tickets"
)

// The tables of the replay records of the Idempotency-Key header, the server
// key of their fingerprints, the heartbeats of the workers, and the state
// version of each unit. See EmptiedTables.
const (
	TableIdempotencyRecords   = "idempotency_records"
	TableIdempotencyServerKey = "idempotency_server_key"
	TableDurableHeartbeats    = "durable_heartbeats"
	TableLocalUserResets      = "local_user_resets"
	TablePasskeyChallenges    = "passkey_challenges"
	TableUnitState            = "unit_state"
)

// The control tables that a backup leaves out and a restore keeps, besides
// the durable tables of ControlTables: the installation state and the
// backup and restore registries. The live rows stay across the swap.
const (
	TableBackups      = "backups"
	TableRestores     = "restores"
	TableInstallation = "installation"
	// TableMaintenanceRevocations journals the writes that only reduce access
	// and that ran in maintenance mode. A restore applies them again after
	// the swap.
	TableMaintenanceRevocations = "maintenance_revocations"
	// TableInstanceVersions holds the version row of each running server
	// process. The rows describe the processes that run now, so a restore
	// keeps the live rows.
	TableInstanceVersions = "instance_versions"
	// TableStoreChecks holds the rows of the store checks of the installation.
	// A check describes the store of this installation at the time of the
	// check, so a restore keeps the live rows and a backup leaves them out.
	TableStoreChecks = "store_checks"
)

// Tables lists every table that an export covers, in foreign-key order, so an
// import can load parents before children. Both dialects share the table
// names, so both adapters use this list as the single source: they must not
// keep a copy in their own store package. Treat the list as a constant and
// never mutate it; call TablesCopy for a copy to read or sort. The list
// excludes schema_migrations: it is rebuildable bookkeeping, not data. It
// excludes the control tables of ControlTables: a backup leaves them out and
// a restore keeps the live rows across the swap. access_token_policy and
// session_policy are seeded, and the restore stage clears the seeded rows
// before it loads the backup. A new
// migration that adds a table must add its name to Tables or to ControlTables,
// and the conformance suite fails while the schema and these lists differ.
//
//nolint:gochecknoglobals // The shared frozen table list: Go has no constant slices, and both adapters use this one source.
var Tables = []string{
	portTableNamespaces,
	"stacks",
	"stack_tags",
	"updates",
	"checkpoints",
	"update_events",
	"update_journal",
	"update_journal_sequences",
	"update_journal_refusals",
	"secret_keys",
	"inventory",
	"inventory_labels",
	"inventory_deleted",
	"inventory_change_heads",
	"inventory_changes",
	"collections",
	"collection_members",
	"placement_rules",
	"object_context",
	"object_context_owners",
	"gate_objects",
	"gate_references",
	"gate_records",
	"blob_purges",
	"workspaces",
	"state_versions",
	"audit_entries",
	TablePrincipals,
	TablePrincipalGroups,
	TableGroupMembers,
	TableGroupMappings,
	"member_removal_alerts",
	"member_removals",
	TableTempAdmins,
	TableAccessTokens,
	TableSessions,
	TableSignInRequests,
	TableOIDCProviders,
	TableInstallationSecretKeys,
	TableSignInProviders,
	TableRoles,
	TableGrants,
	"audit_sink_cursors",
	"audit_sink_leases",
	TableAccessTokenPolicy,
	TableSessionPolicy,
	"imports",
	"imports_purged",
	"import_units",
	"import_ledger",
	"import_upload_commits",
	"workspace_blame_cursors",
	"workspace_blame_items",
	"workspace_resource_changes",
	"runner_pools",
	"runner_join_tokens",
	"runners",
	"jobs",
	"job_freezes",
	"job_plans",
	"job_logs",
	"job_log_chunks",
	"profiles",
	"profile_revisions",
	"issuer_keys",
	"issuer_settings",
	"manual_locks",
	TablePersonPreferences,
	TableForgeAccountLinks,
	TableForgeLinkRequests,
	TableLocalUsers,
	TableSessionGenerations,
	TablePasskeys,
	"saved_views",
	"drift_results",
	"unit_settings",
	"unit_defaults",
	TableGitHostAppRegistrations,
	TableGitHostInstallations,
	"git_host_connections",
	"registry_releases",
	"registry_keys",
	"notification_sinks",
	"notification_sink_secrets",
	"notification_sink_refs",
	"notifications",
	"notification_sequences",
	"notification_cursors",
	"notification_subscriptions",
	"notification_deliveries",
	"triggers",
	"item_keys",
	"settings",
	"settings_state",
	"self_service_blueprints",
	"self_service_blueprint_versions",
	TableTrustRules,
	"break_glass_reviews",
}

// TablesCopy returns a copy of Tables. Callers that sort or filter the list
// must use the copy, so the shared list never changes.
func TablesCopy() []string {
	return slices.Clone(Tables)
}

// State is where a backup is in its run.
type State string

// Backup states.
const (
	// StateRunning is a backup that started and has not finished yet.
	StateRunning State = "running"
	// StateSucceeded is a backup that captured every table.
	StateSucceeded State = "succeeded"
	// StateFailed is a backup that stopped early. Error names the cause.
	StateFailed State = "failed"
)

// ID identifies one backup. IDs carry the prefix "bkp-".
type ID string

// String returns the ID as a plain string.
func (id ID) String() string {
	return string(id)
}

// backupIDPrefix prefixes every backup ID the server mints.
const backupIDPrefix = "bkp-"

// maxBackupIDLength bounds a backup ID: the minted form is far shorter, and
// longer input is never a real ID.
const maxBackupIDLength = 128

// ParseID validates s as a backup ID: the bkp- prefix and at least one more
// identifier character. It returns ErrInvalidID for anything else, so callers
// answer 400 with a fixed message instead of sending a malformed ID to the
// target.
func ParseID(s string) (ID, error) {
	if !ident.Valid(s, maxBackupIDLength) || !strings.HasPrefix(s, backupIDPrefix) || len(s) == len(backupIDPrefix) {
		return "", fmt.Errorf("%w: %q", ErrInvalidID, s)
	}

	return ID(s), nil
}

// Record is one row of the backups table: the backups that this installation
// made, newest first. Target names where the backup went, for example
// "dir:/var/backups" or "s3://bucket/prefix". Error holds the failure text
// for an operator and stays empty otherwise. The row survives a restart.
type Record struct {
	StartedAt time.Time
	EndedAt   time.Time
	ID        ID
	Target    string
	Error     string
	State     State
}

// BlobRef names one blob that the SQL data references: the content key of a
// finalized state version with its size and MD5. Key holds a blob key. Size
// and MD5 stay those of the plain bytes, as the state version stores them.
// Sealed reports that the stored bytes are sealed with the data key of the
// workspace scope: the backup opens them to check the plain bytes and stores
// the sealed bytes. The backup copies each named blob after the SQL capture.
type BlobRef struct {
	MD5  [16]byte
	Key  string
	Size int64
	// Sealed reports sealed stored bytes (ADR 41 decision 10).
	Sealed bool
}

// Summary is the manifest subset that the SQL data alone determines. The
// backup writes the full manifest last; restore checks it before writing
// anything. PrimaryInstallations holds the distinct primary installations of the namespaces,
// ordered. WrapperIDs holds the distinct wrapper IDs of the stored data keys
// from secret.KeyStore.ListWrapperIDs, ordered. RowCounts holds the row
// count of each table in Tables. Blobs holds the blob references of the SQL
// data, ordered by key.
type Summary struct {
	Dialect              Dialect
	PrimaryInstallations []string
	WrapperIDs           []string
	RowCounts            map[string]int64
	Blobs                []BlobRef
	// Unverified holds the keys of the blobs that rows name and that the
	// backup does not copy, because the rows hold no size and MD5 of the
	// stored bytes (a row from an earlier version of the server). It is
	// sorted and has no key twice. A backup never copies a blob that it cannot
	// check.
	Unverified    []string
	SchemaVersion int
}

// BaselineSchemaVersion is the schema version of the baseline migration of
// the store adapters. The baseline replaced every migration before it, so a
// backup with a lower schema version cannot be restored by any binary. It
// equals schema.BaselineVersion of the store package, which this package
// cannot import; a test pins the two.
const BaselineSchemaVersion = 168

// EndedWithoutFinishText is the error text of a backup row that
// Registry.FailEndedBackups marks failed when its operation holds no error
// text, for example after a cancel.
const EndedWithoutFinishText = "the backup operation ended before the backup finished"

// RestoredRunningText is the error text of a backup row that a restore
// brought back as running: the backup was running when it took the
// snapshot that the restore applied, so no process runs it here.
const RestoredRunningText = "the backup was running in the restored data"

// RestoredRunningRestoreText is the error text of a restore row that a
// restore brought back as checking or applying: a restore takes a backup of
// the live data before it applies, and that backup holds the restore row as
// running, so no process runs it here.
const RestoredRunningRestoreText = "the restore was running in the restored data"

// Sentinel errors returned by this package and by its ports.
var (
	// ErrNotFound reports that a backup row does not exist.
	ErrNotFound = errors.New("backup not found")
	// ErrAlreadyExists reports that a backup row with the same ID exists.
	ErrAlreadyExists = errors.New("backup already exists")
	// ErrNotRunning reports that a backup is not running, so it cannot finish.
	ErrNotRunning = errors.New("backup is not running")
	// ErrSchemaMismatch reports that the backup schema version differs from
	// the schema version of the destination. A backup restores into the same
	// schema version only.
	ErrSchemaMismatch = errors.New("backup schema version differs from destination schema version")
	// ErrSchemaBeforeBaseline reports a backup with a schema version from
	// before BaselineSchemaVersion. No binary can restore it.
	ErrSchemaBeforeBaseline = errors.New(
		"backup schema version is older than the baseline schema version: no binary can restore this backup",
	)
	// ErrCorrupt reports that the backup data is incomplete, or that it does
	// not match its descriptors, row counts or manifest.
	ErrCorrupt = errors.New("backup data is corrupt")
	// ErrInvalidID reports a backup ID that fails validation: empty,
	// overlong, without the bkp- prefix, or with characters outside the
	// identifier set.
	ErrInvalidID = errors.New("invalid backup ID")
)

// OperationKind is the kind of the durable operation that runs one backup.
// The api component starts it with the backup row, and the worker component
// runs it.
const OperationKind durable.Kind = "backup"

// OperationVersion is the version of the steps of OperationKind.
const OperationVersion = 1

// The steps of the backup operation, in order. StepWrite writes the SQL part,
// the blobs and the manifest to the target. StepFinish records the end of the
// backup row.
const (
	StepWrite  = "write"
	StepFinish = "finish"
)

// OperationKeyPrefix starts the start key of each backup operation. The key
// is the prefix and the backup ID, so one backup has one operation.
const OperationKeyPrefix = "backup:"

// OperationKey returns the start key of the operation that runs backup id.
func OperationKey(id ID) string {
	return OperationKeyPrefix + id.String()
}

// Registry tracks the backups table: one row for each backup that this
// installation made. Rows carry no fence: they describe the installation,
// not one namespace. A backup in progress is a durable operation: its row
// ends only through a write that carries the fence of its claim, so an
// instance that lost the claim cannot change the row.
type Registry interface {
	// CreateBackup stores r as it is. It returns ErrAlreadyExists if the ID
	// is taken, and ErrAlreadyRunning when r is running and another backup of
	// the installation is running: one backup runs at a time, across every
	// instance on the database. It starts no operation: a running row that
	// this method stores has no process that finishes it.
	CreateBackup(ctx context.Context, r Record) error
	// StartBackup stores r as a running backup and starts the operation start
	// in one transaction, so a running row always has an operation, and an
	// operation of a refused row never exists. It returns ErrAlreadyExists
	// if the ID is taken, and ErrAlreadyRunning when another backup is
	// running. A failed start changes nothing.
	StartBackup(ctx context.Context, r Record, start durable.StartRequest) error
	// StartBackupClaimed stores r and starts the operation start as
	// StartBackup does, and claims the new operation for claim in the same
	// transaction, with the rules of durable.StartClaim. It returns the
	// claimed operation. It returns durable.ErrInvalid for a claim that
	// fails Validate, and the errors of StartBackup. A failed start changes
	// nothing.
	StartBackupClaimed(
		ctx context.Context, r Record, start durable.StartRequest, claim durable.StartClaim,
	) (durable.Operation, error)
	// FinishBackup moves the running backup id to state, which is
	// StateSucceeded or StateFailed, with the error text and the end time,
	// and records the step f.Step, in one transaction that checks the claim
	// of f first. It returns the finished row. It returns durable.ErrClaimLost
	// when f is not the live claim, durable.ErrCanceled after a cancel, and
	// durable.ErrStepDone when the operation already recorded the step; then
	// it changes nothing. It returns ErrNotFound if id does not exist, and
	// ErrNotRunning if the row is not running or state is another state; then
	// it records no step. Callers must handle the result.
	FinishBackup(
		ctx context.Context, f durable.StepFence, id ID, state State, errText string, endedAt time.Time,
	) (Record, error)
	// GetBackup returns the backup row id, or ErrNotFound.
	GetBackup(ctx context.Context, id ID) (Record, error)
	// ListBackups returns every backup row, newest first.
	ListBackups(ctx context.Context) ([]Record, error)
	// FailEndedBackups marks failed each running row whose operation ended
	// failed or canceled without a finish write, for example after too many
	// holders stopped. The error text is the last error of the operation. It
	// returns the number of rows that it changed. A row whose operation runs
	// or waits stays as it is.
	FailEndedBackups(ctx context.Context, endedAt time.Time) (int, error)
}

// Inspector lists the tables that the schema holds. The conformance suite
// reads this list, not Tables, so a table missing from Tables fails the
// suite. Implementations read the catalog: sqlite_master for SQLite, the
// information schema for PostgreSQL.
type Inspector interface {
	// SchemaTables returns the data tables that the schema holds, excluding
	// bookkeeping such as schema_migrations.
	SchemaTables(ctx context.Context) ([]string, error)
}
