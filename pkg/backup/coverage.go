package backup

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// ErrCoverage reports that the column list of a backup table and the live
// schema of the store differ (ADR 42, decision 5). A column that the list does
// not hold, or a listed column that the schema does not hold, fails the export.
// The error names the table and the column.
var ErrCoverage = errors.New("backup column list and schema differ")

// ControlTables lists the tables of the schema that a backup leaves out and
// a restore keeps: the live rows stay across the swap. The tables of
// durable operations hold claim tokens that only grow. A restore that
// brought back older tokens would give a new claim the token of a live
// holder, and the writes of that holder would pass the fence. A restore
// that brought back an older next run of a schedule would fire a tick
// again. The installation, backups and restores tables hold the state of
// this installation: a restore keeps the live rows, so the running restore
// stays readable during the swap and the pre-backup row survives it. The
// revocation journal holds the writes that only reduce access and that ran
// in maintenance mode: the swap keeps it and applies it to the restored
// access tables. The instance version rows describe the server processes
// that run now, so the swap keeps them. The store check rows describe the
// store of this installation at the time of a check, so the swap keeps
// them too. The used CAS ticket records stay too: a restore must not let a
// ticket that a server used after the backup work again. The schema holds every table of Tables and of ControlTables,
// and no other table (schema_migrations and audit_chain_state aside, which
// the adapters keep as bookkeeping). Treat the list as a constant and never mutate it.
//
//nolint:gochecknoglobals // A shared frozen table list, as Tables is.
var ControlTables = []string{
	TableDurableOperations,
	TableDurableSteps,
	TableDurableSchedules,
	TableSignInFailures,
	TableNameLocks,
	TableNameLockKey,
	TableUsedTickets,
	TableAuditChainCheck,
	TableAuditChainMark,
	TableBackups,
	TableRestores,
	TableInstallation,
	TableMaintenanceRevocations,
	TableInstanceVersions,
	TableStoreChecks,
	"runner_claims",
	"runner_handoff_tokens",
	"runner_sessions",
	"runner_nonces",
}

// EmptiedTables lists the tables of the schema that a backup leaves out and
// a restore leaves empty: the replay records of the Idempotency-Key header and
// the server key of their fingerprints, the heartbeats of the workers, the
// reset codes and set-up codes of local users, the open passkey
// ceremonies, and the state version of each unit. A reset code that a restore
// brought back would set a password that an administrator gave out before
// the time of the restore. A ceremony lives 300 seconds, and a restore that
// brought one back would let an old challenge work again. A
// heartbeat describes the workers that run now, and the next beat of a worker
// makes its row again. A replay record answers for the state
// at the time of the first request, so a replay after a restore of older state
// would answer for state that the restore removed. A state version names the
// state that an installation holds now, so a restore derives it from the
// restored rows and brings no stored value back. The schema holds these
// tables and the tables of Tables and ControlTables, and no other table.
// Treat the list as a constant and never mutate it.
//
//nolint:gochecknoglobals // A shared frozen table list, as Tables is.
var EmptiedTables = []string{
	TableIdempotencyRecords,
	TableIdempotencyServerKey,
	TableDurableHeartbeats,
	TableLocalUserResets,
	TablePasskeyChallenges,
	TableUnitState,
}

// ErrSchemaRead reports that the store could not read its live schema to
// compare it with a column list. The driver error stays out of the value: an
// adapter translates it into this sentinel (CLAUDE.md, backend rules).
var ErrSchemaRead = errors.New("backup cannot read the live schema")

// Coverage is the port that compares the column list of each table in Tables
// with the live schema of the store. It returns ErrCoverage when they differ.
// The conformance suite runs it on both dialects.
type Coverage interface {
	CheckCoverage(ctx context.Context) error
}

// CheckColumns compares the column list of table with its live columns. The
// order of the names does not matter. It reports the first column that the
// list omits, and then the first listed column that the schema does not hold.
func CheckColumns(table string, listed, live []string) error {
	for _, column := range live {
		if !slices.Contains(listed, column) {
			return fmt.Errorf(
				"backup column list of table %q misses column %q: %w",
				table,
				column,
				ErrCoverage,
			)
		}
	}

	for _, column := range listed {
		if !slices.Contains(live, column) {
			return fmt.Errorf(
				"backup column list of table %q names unknown column %q: %w",
				table,
				column,
				ErrCoverage,
			)
		}
	}

	return nil
}
