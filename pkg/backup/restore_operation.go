package backup

// The restore operation: a restore of the whole installation is a durable
// operation. A worker of any instance claims it and runs its steps. The
// instance that took the request only starts the operation. Any instance
// answers the state of a restore from the restores row, which lives in the
// control tables that a swap never replaces.
//
// # Start
//
// RestoreService.Start admits the restore, refuses a dev backup into a
// production installation, and calls RestoreOperations.StartRestore. That
// call stores the restores row in the checking state and starts the
// operation of RestoreOperationKind under RestoreOperationKey, in one
// transaction. It refuses a start while another restore row is checking or
// applying (ErrRestoreRunning). The input of the operation holds the request:
// the backup ID, the pre-backup ID that Start mints, check-only, force, the
// requester and whether the restore keeps the access tables. The route
// answers 202 with the restore ID.
//
// # Steps
//
// Each step does one fenced write that records the step in the transaction
// of the write, with the rules of durable.Store.RecordStep. The other work of
// a step is safe to do again, and safe when a holder that lost its claim
// does it late. The runner skips a recorded step after a takeover.
//
//	step               work                                       fenced write that records the step
//	stage              read and pin the manifest, verify the SQL  the runner records the staging area
//	                   part, stage it into the staging area of     and the pinned manifest as the result
//	                   this restore and this claim token
//	check              key check, blob check of the target, dev   RecordRestoreReport: the dev report
//	                   data detect (and remove, on production)    on the restores row
//	                   on the staging area of the stage result
//	enter-maintenance  maintenance.Service.EnterFenced, with      FencedStore.SetStateIfFenced: the
//	                   force when the request asks for it          compare-and-set to maintenance
//	pre-backup         Service.Run of the pre-backup ID of the     MarkRestoreApplying: applying, with
//	                   input; a second run waits for that backup   the pre-backup ID
//	copy-blobs         pin the manifest again, copy each blob     the runner records the step
//	                   to the live store; a key that exists is
//	                   verified, not copied
//	swap               the swap of the staging area of the stage  RestoreStaging.ApplyRestoreArea, in
//	                   result into the live data, the fence of    the swap transaction
//	                   every namespace, the control rows kept
//	after-apply        the after-apply hook, the audit entry of   RecordRestoreReport: the dev report
//	                   the restore, the final dev report
//	cleanup            drop the old live data and each staging    RestoreStaging.CleanRestoreAreas, in
//	                   area of this restore, and the areas of     the drop transaction (PostgreSQL), or
//	                   each restore that ended                    after the removals (SQLite)
//	leave-maintenance  maintenance.Service.LeaveFenced            FencedStore.SetStateIfFenced: the
//	                                                              compare-and-set to normal
//	finish             the end of the restores row                FinishRestore: succeeded
//
// A check-only restore runs stage and check. Each later step except cleanup
// and finish returns at once with no write, and the runner records it.
// Cleanup drops the staging area. Finish marks the row succeeded.
//
// # Staging areas
//
// A StagingArea is named by the restore ID and the claim token of the holder
// that staged it. The stage result names the area, so each later step finds
// it on any instance. A holder that loses its claim during stage writes only
// into its own area, never into the area of the new holder. An area that no
// stage result names is an orphan. Cleanup drops the orphans of its own
// restore, and the areas of each restore whose row ended. It keeps an area
// whose restore has no row in this registry: another installation can share
// the database.
//
// # Failure, cancel and the end of the operation
//
// A step that fails for a reason that no retry can fix (a corrupt backup, a
// refused enter, a dev backup) ends the restore with FinishRestore, with the
// fence of the finish step and RestoreEnd.LeaveMaintenance when the restore
// entered maintenance mode and did not leave it. It returns the error as
// permanent. Any other error of a step is a retry with backoff, as for every
// durable operation.
//
// While the maintenance hold of the server is true, LeaveFenced answers
// maintenance.ErrHeld and changes nothing. The leave-maintenance step then
// records itself with no change, as the leave of a restore did before: the
// hold keeps maintenance mode.
//
// RestoreService.Cancel goes through durable.StepCanceler.CancelBefore with the swap step:
// before the swap it stops the operation, and the swap transaction refuses
// to commit after it. After the swap it changes nothing and the service
// answers ErrRestoreSwapped.
//
// An operation can end with no finish write: a cancel, a step that panicked,
// or too many holders that stopped. RestoreService.Work then calls
// FailEndedRestores once in each lease period. It marks the row failed, and
// leaves maintenance mode when the operation recorded enter-maintenance and
// not leave-maintenance, in one transaction for each row. While the row is
// checking or applying, no other restore can start, so that leave can never
// end the maintenance mode of a later restore.
//
// # What a takeover does at each step
//
//	stage              stages again into a new area; the area of the old holder is an orphan
//	check              checks the area of the stage result again; the dev strip removes nothing more
//	enter-maintenance  enters again; a second enter keeps maintenance mode
//	pre-backup         waits for the backup of the pre-backup ID, or starts it
//	copy-blobs         verifies the blobs that the old holder copied
//	swap               the swap committed with its step record, or it did not commit at all
//	after-apply        runs the hook again; the audit entry can occur two times
//	cleanup            drops again; a drop of an area that does not exist does nothing
//	leave-maintenance  leaves again; a second leave keeps normal operation
//	finish             finishes the row, which is still applying
//
// A start of another instance does nothing to a running restore: it claims
// no operation with a live claim, and FailEndedRestores fails only rows whose
// operation ended.

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
)

var (
	// ErrInvalidRestoreID reports a restore ID, or a staging area, that does
	// not have the form that MintRestoreID and StagingArea.String make.
	ErrInvalidRestoreID = errors.New("invalid restore ID")
	// ErrRestoreSwapped reports a cancel of a restore that already swapped
	// the staged data into the live data. Such a restore runs to its end.
	ErrRestoreSwapped = errors.New("restore already swapped the data and cannot be canceled")
)

// RestoreOperationKind is the durable kind of a restore.
const RestoreOperationKind durable.Kind = "restore"

// RestoreOperationVersion is the version of the steps of
// RestoreOperationKind.
const RestoreOperationVersion = 1

// The steps of the restore operation, in order. RestoreSteps returns the
// list.
const (
	RestoreStepStage            = "stage"
	RestoreStepCheck            = "check"
	RestoreStepEnterMaintenance = "enter-maintenance"
	RestoreStepPreBackup        = "pre-backup"
	RestoreStepCopyBlobs        = "copy-blobs"
	RestoreStepSwap             = "swap"
	RestoreStepAfterApply       = "after-apply"
	RestoreStepCleanup          = "cleanup"
	RestoreStepLeaveMaintenance = "leave-maintenance"
	RestoreStepFinish           = "finish"
)

// RestoreSteps returns the step names of the restore operation, in the order
// that the runner runs them.
func RestoreSteps() []string {
	return []string{
		RestoreStepStage,
		RestoreStepCheck,
		RestoreStepEnterMaintenance,
		RestoreStepPreBackup,
		RestoreStepCopyBlobs,
		RestoreStepSwap,
		RestoreStepAfterApply,
		RestoreStepCleanup,
		RestoreStepLeaveMaintenance,
		RestoreStepFinish,
	}
}

// RestoreOperationKeyPrefix starts the start key of each restore operation.
// The key is the prefix and the restore ID, so one restore has one
// operation.
const RestoreOperationKeyPrefix = "restore:"

// RestoreOperationKey returns the start key of the operation that runs
// restore id.
func RestoreOperationKey(id RestoreID) string {
	return RestoreOperationKeyPrefix + id.String()
}

// restoreIDPattern is the form of a restore ID that MintRestoreID makes: the
// prefix and 20 characters of the lowercase base32 alphabet. A store adapter
// puts the ID into a schema name or a file name, so the pattern is an
// allowlist.
var restoreIDPattern = regexp.MustCompile(`^rst-[a-z2-7]{20}$`)

// ParseRestoreID returns s as a RestoreID when it has the form that
// MintRestoreID makes. It returns ErrInvalidRestoreID for any other value.
func ParseRestoreID(s string) (RestoreID, error) {
	if !restoreIDPattern.MatchString(s) {
		return "", fmt.Errorf("%w: %q", ErrInvalidRestoreID, s)
	}

	return RestoreID(s), nil
}

// StagingArea names one staging area of a restore: the restore and the
// claim token of the holder that staged it. A store adapter derives the
// name of its schema or its file from the area, and from nothing else.
type StagingArea struct {
	Restore RestoreID
	Token   int64
}

// stagingAreaSeparator separates the restore ID and the token in the text
// form of a StagingArea. A restore ID never holds it.
const stagingAreaSeparator = "."

// NewStagingArea returns the area of restore id and the claim token. It
// returns ErrInvalidRestoreID for an ID that ParseRestoreID refuses, and for
// a token that is not positive.
func NewStagingArea(id RestoreID, token int64) (StagingArea, error) {
	if _, err := ParseRestoreID(id.String()); err != nil {
		return StagingArea{}, err
	}

	if token <= 0 {
		return StagingArea{}, fmt.Errorf("%w: staging token %d is not positive", ErrInvalidRestoreID, token)
	}

	return StagingArea{Restore: id, Token: token}, nil
}

// String returns the text form of a: the restore ID, a dot and the token.
// The stage step stores it as its result.
func (a StagingArea) String() string {
	return a.Restore.String() + stagingAreaSeparator + strconv.FormatInt(a.Token, 10)
}

// ParseStagingArea returns the area of the text form s. It returns
// ErrInvalidRestoreID for any text that String does not make.
func ParseStagingArea(s string) (StagingArea, error) {
	id, token, ok := strings.Cut(s, stagingAreaSeparator)
	if !ok {
		return StagingArea{}, fmt.Errorf("%w: staging area %q", ErrInvalidRestoreID, s)
	}

	n, err := strconv.ParseInt(token, 10, 64)
	if err != nil || strconv.FormatInt(n, 10) != token {
		return StagingArea{}, fmt.Errorf("%w: staging area %q", ErrInvalidRestoreID, s)
	}

	return NewStagingArea(RestoreID(id), n)
}

// RestoreEnd is the end of a restores row.
type RestoreEnd struct {
	ID RestoreID
	// State is RestoreSucceeded or RestoreFailed.
	State RestoreState
	// Error is the text for an operator. It is empty for RestoreSucceeded.
	Error   string
	EndedAt time.Time
	// LeaveMaintenance moves the installation to normal operation in the
	// transaction of the end, when it is in maintenance mode. A failed
	// restore that entered maintenance mode and did not leave it sets it.
	LeaveMaintenance bool
}

// RestoreOperations is the registry of the restores table for restores that
// run as durable operations. Each SQL store adapter implements it on the
// database of its durable.Store, and runs the conformance cases of package
// storetest. It replaces RestoreRegistry: the rows carry no fence of a
// namespace, and every write of a running restore carries the StepFence of
// its operation.
//
// A method that takes a StepFence checks it with the rules of
// durable.Store.RecordStep and records its step, in the transaction of its
// own write. It checks the fence first: a stale claim changes nothing and
// learns nothing about the row. It returns durable.ErrClaimLost too when the
// operation of the fence is not the operation of the restore.
type RestoreOperations interface {
	// StartRestore stores r as a checking restore, with r.DevReport when it
	// is not nil, and starts the operation start, in one transaction. It
	// returns ErrInvalidRestoreID for an ID that ParseRestoreID refuses,
	// ErrRestoreExists when the ID is taken, ErrRestoreRunning when another
	// restore is checking or applying, and durable.ErrKeyConflict when start
	// is not of RestoreOperationKind under RestoreOperationKey(r.ID), or when
	// its key already names an operation. On an error it stores nothing.
	StartRestore(ctx context.Context, r RestoreRecord, start durable.StartRequest) error

	// GetRestore returns the restore row id, or ErrRestoreNotFound.
	GetRestore(ctx context.Context, id RestoreID) (RestoreRecord, error)

	// ListRestores returns every restore row, newest first.
	ListRestores(ctx context.Context) ([]RestoreRecord, error)

	// MarkRestoreApplying moves the restore id from checking to applying,
	// records the pre-backup ID and the step of f. It returns
	// ErrRestoreNotFound when id does not exist, and ErrRestoreNotRunning when
	// the row is not checking.
	MarkRestoreApplying(ctx context.Context, f durable.StepFence, id RestoreID, preBackupID ID) error

	// RecordRestoreReport stores report on the running restore id and records
	// the step of f. It returns ErrRestoreNotFound when id does not exist, and
	// ErrRestoreNotRunning when the row ended.
	RecordRestoreReport(ctx context.Context, f durable.StepFence, id RestoreID, report DevStripReport) error

	// FinishRestore moves the running restore end.ID to end.State, with the
	// error text and the end time, and records the step of f. With
	// end.LeaveMaintenance it also moves the installation to normal operation
	// when it is in maintenance mode. It returns the finished row. It returns
	// ErrRestoreNotRunning for a state other than RestoreSucceeded and
	// RestoreFailed, and for a row that ended, and ErrRestoreNotFound when the
	// row does not exist.
	FinishRestore(ctx context.Context, f durable.StepFence, end RestoreEnd) (RestoreRecord, error)

	// FailEndedRestores marks failed each checking or applying row whose
	// operation ended canceled or failed, with the error text
	// RestoreCanceledText or RestoreStoppedText and the end time endedAt.
	// When the operation recorded RestoreStepEnterMaintenance and not
	// RestoreStepLeaveMaintenance, and leave is true, it moves the
	// installation to normal operation in the transaction of that row. It
	// keeps a row whose operation runs, also with an expired claim. It
	// returns the rows that it marked failed.
	FailEndedRestores(ctx context.Context, endedAt time.Time, leave bool) ([]RestoreRecord, error)
}

// The error texts that FailEndedRestores stores.
const (
	// RestoreCanceledText is the error text of a restore whose operation a
	// cancel stopped.
	RestoreCanceledText = "restore canceled before the swap"
	// RestoreStoppedText is the error text of a restore whose operation ended
	// with no end write: a step that panicked, or too many holders that
	// stopped.
	RestoreStoppedText = "restore stopped with no end; start the restore again"
)

// RestoreStaging is the staging port of a restore that runs as a durable
// operation. Each area has a name that a StagingArea gives, so every
// instance on the database finds the area that a stage result names. It
// replaces the staging area that a store adapter held in its memory. Each SQL
// store adapter implements it, and runs the conformance cases of package
// storetest.
//
// A method that takes a StagingArea derives the name of its schema or its
// file from the area only, and checks the name against the prefix of its
// staging areas before it changes anything.
type RestoreStaging interface {
	// StageRestore removes the area a when it exists, then stages the table
	// files into a with the checks of PortableStager.StagePortable. A failed
	// stage removes a. The live data stays unchanged.
	StageRestore(
		ctx context.Context, a StagingArea, open PortableOpener, want Summary, files []PortableFile, opts StageOptions,
	) (Summary, error)

	// StripRestoreArea runs the dev data strip of DevStripper.StripDevStaged
	// on the area a. It returns ErrCorrupt when a does not exist.
	StripRestoreArea(ctx context.Context, a StagingArea, mode DevStripMode) (DevStripReport, error)

	// ApplyRestoreArea swaps the area a into the live data, as
	// Applier.ApplyStaging does, or as AccessKeepingApplier does when
	// keepAccess is true. It checks f and records its step in the swap
	// transaction, before the first change. On PostgreSQL the old live data
	// stays as the previous schema of the restore of a, for CleanRestoreAreas.
	// It returns ErrCorrupt when a does not exist, and the errors of
	// durable.Store.RecordStep, with no change.
	ApplyRestoreArea(
		ctx context.Context, f durable.StepFence, a StagingArea, keepAccess bool,
	) (KeepAccessResult, error)

	// CleanRestoreAreas drops the old live data and every staging area of the
	// restore of f, and every area of a restore whose row in this registry
	// ended. It keeps the areas of a restore that has no row in this
	// registry, and of a running restore other than its own. Each drop waits
	// for its locks for lockWait at most, and then fails with no change. It
	// checks f before the first drop and records its step: on PostgreSQL in
	// the transaction of the drops, and on SQLite after the file removals. It
	// returns the areas that it dropped. It returns durable.ErrClaimLost when
	// the operation of f is not the operation of restore id.
	CleanRestoreAreas(
		ctx context.Context, f durable.StepFence, id RestoreID, lockWait time.Duration,
	) ([]StagingArea, error)

	// ListRestoreAreas returns the staging areas of this database, also the
	// areas of a restore that has no row in this registry. A schema or a file
	// whose name has the staging prefix but does not parse is not listed.
	ListRestoreAreas(ctx context.Context) ([]StagingArea, error)
}
