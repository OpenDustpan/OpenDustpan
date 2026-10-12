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
// Naming: Exporter reads, Importer loads, Registry tracks the backups table,
// Inspector lists the schema tables, and Summary is the manifest subset that
// the SQL data alone determines. The names say what each port moves, not
// which tool the data belongs to, so the ports stay tool-neutral.
package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
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

// Tables lists every table that an export covers, in foreign-key order, so an
// import can load parents before children. Both dialects share the table
// names, so both adapters use this list as the single source: they must not
// keep a copy in their own store package. ADR 30 decision 4 calls this the
// store constant; the backup package holds it so the shared core stays
// tool-neutral and both dialects use one source. Treat the list as a constant and
// never mutate it; call TablesCopy for a copy to read or sort. The list
// excludes schema_migrations: it is rebuildable bookkeeping, not data. It
// holds installation last: the migration seeds its one row, and the export
// carries it like data, so a restore loads the state the backup holds. The
// empty checks skip it: a fresh database holds the seeded row and still
// counts as empty. A new migration that adds a table must add its name here,
// and the conformance suite fails while the schema and this list differ.
//
//nolint:gochecknoglobals // The shared frozen table list: Go has no constant slices, and both adapters use this one source.
var Tables = []string{
	"namespaces",
	"stacks",
	"stack_tags",
	"updates",
	"checkpoints",
	"update_events",
	"update_journal",
	"update_journal_sequences",
	"secret_keys",
	"inventory",
	"workspaces",
	"state_versions",
	"audit_entries",
	"backups",
	"restores",
	"installation",
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

// ParseID validates s as a backup ID: the bkp- prefix with identifier
// characters. It returns ErrInvalidID for anything else, so callers answer
// 400 with a fixed message instead of sending a malformed ID to the target.
func ParseID(s string) (ID, error) {
	if !ident.Valid(s, maxBackupIDLength) || !strings.HasPrefix(s, backupIDPrefix) {
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
// anything. HomeRegions holds the distinct home regions of the namespaces,
// ordered. WrapperIDs holds the distinct wrapper IDs of the stored data keys
// from secret.KeyStore.ListWrapperIDs, ordered. RowCounts holds the row
// count of each table in Tables. Blobs holds the blob references of the SQL
// data, ordered by key.
type Summary struct {
	Dialect       Dialect
	HomeRegions   []string
	WrapperIDs    []string
	RowCounts     map[string]int64
	Blobs         []BlobRef
	SchemaVersion int
}

// Sentinel errors returned by this package and by its ports.
var (
	// ErrNotFound reports that a backup row does not exist.
	ErrNotFound = errors.New("backup not found")
	// ErrAlreadyExists reports that a backup row with the same ID exists.
	ErrAlreadyExists = errors.New("backup already exists")
	// ErrNotRunning reports that a backup is not running, so it cannot finish.
	ErrNotRunning = errors.New("backup is not running")
	// ErrNotEmpty reports that a restore destination holds rows, so loading
	// would mix two installations. Restore needs an empty store.
	ErrNotEmpty = errors.New("restore destination is not empty")
	// ErrDialectMismatch reports that the backup dialect differs from the
	// dialect of the destination. A backup restores into the same dialect only.
	ErrDialectMismatch = errors.New("backup dialect differs from destination dialect")
	// ErrSchemaMismatch reports that the backup schema version differs from
	// the schema version of the destination. A backup restores into the same
	// schema version only.
	ErrSchemaMismatch = errors.New("backup schema version differs from destination schema version")
	// ErrCorrupt reports that the stream does not hold a complete export, or
	// that its row counts differ from the manifest.
	ErrCorrupt = errors.New("backup data is corrupt")
	// ErrInvalidID reports a backup ID that fails validation: empty,
	// overlong, without the bkp- prefix, or with characters outside the
	// identifier set.
	ErrInvalidID = errors.New("invalid backup ID")
)

// Exporter captures the SQL data in one read transaction and writes it to w.
// The bytes stay opaque: SQLite writes its file image, PostgreSQL its table
// dump. The returned Summary describes what was captured: the dialect, the
// schema version, the home regions, the wrapper IDs, the row count of each
// table in Tables, and the blob references. It returns storage errors
// without a dedicated value. Export stops and returns promptly when w.Write
// reports an error, so a failed target unblocks the backup instead of
// hanging it.
type Exporter interface {
	// Export captures the SQL data in one read transaction and writes it to
	// w, so concurrent writes land all in the capture or all out of it.
	Export(ctx context.Context, w io.Writer) (Summary, error)
}

// Importer loads an export into an empty store of the same dialect and
// schema version. The want Summary is the manifest of the export: Import
// checks it before reading r and compares the loaded row counts with it
// after. It returns the Summary of what it loaded.
type Importer interface {
	// Import loads the export in r into an empty store in one transaction.
	// It returns ErrNotEmpty if the destination holds any row,
	// ErrDialectMismatch if want.Dialect differs from the destination
	// dialect, ErrSchemaMismatch if want.SchemaVersion differs from the
	// destination schema version, and ErrCorrupt if r does not hold a
	// complete export or its row counts differ from want. A failed import
	// leaves an empty store: it never leaves partial rows.
	Import(ctx context.Context, r io.Reader, want Summary) (Summary, error)
}

// Stager stages an export for an online restore (ADR 38 decision 3): it loads
// the export into a staging area without touching the live data, then checks
// the staged data. SQLite stages into a separate file, PostgreSQL into a
// separate schema. A later change applies the staged data; this port only
// checks it.
type Stager interface {
	// StageRestore loads the export in r into the staging area and checks it:
	// the dialect and schema version against want, the row counts against
	// want.RowCounts, the foreign keys, and the blob references against
	// want.Blobs with size and checksum. It returns the Summary of the staged
	// data. It returns ErrDialectMismatch, ErrSchemaMismatch or ErrCorrupt. A
	// failed stage removes the staging area: the live data stays unchanged.
	// Only one staging exists at a time: a new stage discards the previous one.
	StageRestore(ctx context.Context, r io.Reader, want Summary) (Summary, error)
	// DiscardStaging removes the staging area, if any. It leaves the live
	// data unchanged. A check-only restore calls it after the checks.
	DiscardStaging(ctx context.Context) error
}

// Registry tracks the backups table: one row for each backup that this
// installation made. Rows carry no fence: they describe the installation,
// not one namespace. A server that starts marks every running row failed.
type Registry interface {
	// CreateBackup stores r as a running backup. It returns ErrAlreadyExists
	// if the ID is taken.
	CreateBackup(ctx context.Context, r Record) error
	// FinishBackup moves the running backup id to state, which is
	// StateSucceeded or StateFailed, with the error text and the end time.
	// Callers send only those two states; any other state reports
	// ErrNotRunning. It returns the finished row, ErrNotFound if id does not exist, or
	// ErrNotRunning if the row is not running.
	FinishBackup(ctx context.Context, id ID, state State, errText string, endedAt time.Time) (Record, error)
	// GetBackup returns the backup row id, or ErrNotFound.
	GetBackup(ctx context.Context, id ID) (Record, error)
	// ListBackups returns every backup row, newest first.
	ListBackups(ctx context.Context) ([]Record, error)
	// FailRunningBackups marks every running row failed with the error text
	// and the end time. The server calls it at start, after a stop during a
	// backup.
	FailRunningBackups(ctx context.Context, endedAt time.Time, errText string) error
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
