package backup

// Restore records of the online restore (ADR 38 decisions 8 and 9): one row
// for each restore the installation runs, with its state, the backup it
// restores, the backup taken before it, who asked, and the times.

import (
	"context"
	"errors"
	"time"
)

// Restore states.
const (
	// RestoreChecking runs the restore checks before it changes live data.
	RestoreChecking RestoreState = "checking"
	// RestoreApplying applies the staged data after the checks pass.
	RestoreApplying RestoreState = "applying"
	// RestoreSucceeded is a restore that replaced the live data.
	RestoreSucceeded RestoreState = "succeeded"
	// RestoreFailed is a restore that stopped early. Error names the cause.
	RestoreFailed RestoreState = "failed"
)

// RestoreID identifies one restore. IDs carry the prefix "rst-".
type RestoreID string

// String returns the ID as a plain string.
func (id RestoreID) String() string {
	return string(id)
}

// RestoreState is where a restore is in its run.
type RestoreState string

// String returns the state as a plain string.
func (s RestoreState) String() string {
	return string(s)
}

// RestoreRecord is one row of the restores table. BackupID is the backup the
// restore applies; PreBackupID is the backup of the live data taken before
// the apply, empty when the restore stopped before it. RequestedBy is the
// subject that asked. Error holds the failure text for an operator.
//
// DevReport is nil until RecordDevReport stores a report. The report holds the
// dev data that the restore removed, or, for a check-only restore, the dev
// data that it would remove, and the codes of the warnings that the answer
// carries. CreateRestore ignores it.
type RestoreRecord struct {
	StartedAt   time.Time
	EndedAt     time.Time
	ID          RestoreID
	BackupID    ID
	PreBackupID ID
	RequestedBy string
	Error       string
	State       RestoreState
	DevReport   *DevStripReport
}

// Sentinel errors returned by the restore registry.
var (
	// ErrRestoreNotFound reports that a restore row does not exist.
	ErrRestoreNotFound = errors.New("restore not found")
	// ErrRestoreExists reports that a restore row with the same ID exists.
	ErrRestoreExists = errors.New("restore already exists")
	// ErrRestoreNotRunning reports that a restore is not running, so it
	// cannot finish.
	ErrRestoreNotRunning = errors.New("restore is not running")
	// ErrRestoreRunning reports a second restore started while one runs.
	// Only one restore runs at a time, across every instance on the
	// database.
	ErrRestoreRunning = errors.New("restore already running")
)

// Applier swaps the staged restore data into the live data in one step and
// fences it (ADR 38 decisions 6 and 7): a file rename plus a fence for
// SQLite, a schema swap plus a fence in one transaction for PostgreSQL. The
// staging area holds checked data from the stage step; the apply step moves it
// live and increments the failover version of every namespace, so leases,
// locks and update tokens from before the restore stop working even when the
// server crashes right after the swap.
type Applier interface {
	// ApplyStaging swaps the staged restore data into the live data, fences
	// every namespace, and removes the staging area. It reports ErrCorrupt
	// when no staged data exists.
	//
	// Before it changes anything, it reads the live installation kind from its
	// own database. When it cannot read the kind, it refuses the swap and
	// changes nothing. On a production installation it checks the whole
	// staging area, and refuses the swap with ErrDevBackupRefused when the
	// staging area holds a dev principal or a row that names one, whatever the
	// stage call, the manifest or the strip reported.
	ApplyStaging(ctx context.Context) error
}

// AccessKeepingApplier swaps staged data while it keeps the current access
// tables. A restore by a caller without server:restore-access uses it, so
// the access tables stay as they are and the other tables come from the
// backup, in the same step. It reports ErrRestoreAccessMismatch when the
// backup names access data that the kept tables do not hold, with no change.
type AccessKeepingApplier interface {
	// ApplyStagingKeepingAccess swaps the staged restore data into the live
	// data, keeps the current rows of the access tables, fences every
	// namespace, and removes the staging area. It reports ErrCorrupt when
	// no staged data exists. It makes the same dev data checks as ApplyStaging,
	// on the whole staging area, also on the access tables that it does not
	// take.
	//
	// The swap takes temp_admins from the backup and keeps the live
	// principals. So it drops, in the swap transaction, each staged
	// temp_admins row that names a principal that the live principals table
	// does not hold, and returns the count in the result.
	ApplyStagingKeepingAccess(ctx context.Context) (KeepAccessResult, error)
}

// KeepAccessResult is what a swap that keeps the access tables changed beyond
// the plain swap.
type KeepAccessResult struct {
	// DroppedTempAdmins is the count of staged temp_admins rows that the swap
	// dropped because they name a principal that the live principals table
	// does not hold.
	DroppedTempAdmins int64
}

// Fencer increments the failover version of every namespace (ADR 38
// decision 7), so leases, update tokens, locks and upload capabilities from
// before the restore stop working.
type Fencer interface {
	// BumpFailoverVersions increments the failover version of every
	// namespace by one and returns the count of namespaces bumped.
	BumpFailoverVersions(ctx context.Context) (int64, error)
}
