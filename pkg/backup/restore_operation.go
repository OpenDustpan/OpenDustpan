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
// Each step does exactly one fenced write that records the step in the
// transaction of the write, with the rules of durable.Store.RecordStep. So
// "the step is recorded" means "the effect of the step committed". The other
// work of a step is safe to do again, and safe when a holder that lost its
// claim does it late. The runner skips a recorded step after a takeover.
//
//	step               work                                       fenced write that records the step
//	stage              read and pin the manifest, verify the SQL  the runner records the staging area,
//	                   part, drop the areas of earlier tokens of   the pinned manifest and the dev report
//	                   this restore, stage into the area of this
//	                   claim token, run the dev strip in it
//	check              key check, blob check of the target; it    RecordRestoreReport: the dev report of
//	                   only reads the staging area                 the stage result on the restores row
//	enter-maintenance  maintenance.Service.EnterWith a writer     ConfirmRestoreMaintenance: the state is
//	                   bound to the claim (see below)              maintenance; the result says "entered"
//	pre-backup         start the backup of the pre-backup ID as   MarkRestoreApplying: applying, with the
//	                   its own durable operation, and wait for it  pre-backup ID
//	copy-blobs         pin the manifest again, copy each blob     the runner records the step
//	                   with the rules of "Blobs" below
//	swap               the swap of the staging area of the stage  RestoreStaging.ApplyRestoreArea, in the
//	                   result into the live data, the fence of     swap transaction
//	                   every namespace, the control rows kept
//	after-apply        the after-apply hook; check that each blob AppendRestoreAudit: the audit entry of
//	                   the restored data names exists, and copy    the restore, in one transaction with
//	                   a missing one again from the pinned backup  the step record
//	leave-maintenance  when the enter result says "entered":      ConfirmRestoreMaintenance: the state is
//	                   maintenance.Service.LeaveWith the writer    normal, or the hold keeps maintenance
//	cleanup            drop the old live data and each staging    RestoreStaging.CleanRestoreAreas, in the
//	                   area of this restore, the areas of each     drop transaction (PostgreSQL), or after
//	                   restore that ended, and the temporary       the removals (SQLite)
//	                   blob keys of this restore
//	finish             the end of the restores row with the       FinishRestore: succeeded
//	                   final dev report
//
// Cleanup runs after leave-maintenance: the previous schema is not live, so
// its drop needs no maintenance mode, and a long read that holds a lock on
// the old tables does not keep the installation in maintenance mode. The row
// stays applying until finish, so no other restore starts before the cleanup
// ends.
//
// A check-only restore runs stage and check. Each later step except cleanup
// and finish returns at once with no write, and the runner records it.
// Cleanup drops the staging area. Finish marks the row succeeded.
//
// # Staging areas
//
// A StagingArea is named by the restore ID and the claim token of the holder
// that staged it. The stage result names the area, so each later step finds
// it on any instance. Stage writes only into the area of its own token: a
// holder that loses its claim during stage never writes into the area of the
// new holder. Stage first drops the areas of the earlier tokens of its
// restore, which no stage result names, so a series of takeovers keeps no
// series of copies. The dev strip runs in stage, before the runner records
// the stage result. After that record, the area is read-only until the swap.
// Cleanup drops each area of its own restore, and the areas of each restore
// whose row ended. It keeps an area whose restore has no row in this
// registry: another installation can share the database.
//
// # Maintenance mode
//
// maintenance.Service.Enter writes maintenance with a compare-and-set, lists
// the active work again, and moves back to normal when work raced the write,
// up to its attempt limit. Each such write of the restore goes through a
// maintenance.StateWriter that the restore binds to its claim:
// RestoreOperations.SetRestoreMaintenance checks the fence, changes the
// state with a compare-and-set, and sets the maintenance flag of the restores
// row, in one transaction. It records no step. When the second list of the
// active work finds nothing, ConfirmRestoreMaintenance checks the fence,
// checks that the state is still maintenance, and records the step with the
// flag as the result "entered". So a takeover after the confirm never enters
// again, and a takeover before it sees the flag of the row, not a guess.
//
// When the state is already maintenance at the start of the step, and the
// flag of the row is not set, another party entered it, for example an
// operator. The restore then confirms with "entered" false. Leave-maintenance,
// RestoreEnd.LeaveMaintenance and FailEndedRestores leave maintenance mode
// only when the flag says that this restore entered it. They never leave the
// maintenance mode of an operator.
//
// With force, Enter cancels the active work. That is a change outside the
// fence, so a holder that lost its claim can still cancel work. Three
// measures limit it: the runner cancels the step context when a renewal of
// the lease fails, the writer checks the fence again just before each cancel
// (StateWriter.Check), and each cancel writes an audit entry that names the
// restore ID and the claim token (StateWriter.Actor). The new holder cancels
// the same work in any case. The risk that stays is work that started after
// the fence check and before the stale cancel.
//
// While the maintenance hold of the server is true, LeaveWith answers
// maintenance.ErrHeld and changes nothing. The leave-maintenance step then
// confirms the state maintenance: the hold keeps maintenance mode.
//
// # Blobs
//
// The blob store can lack conditional writes: a Put can overwrite a key and
// never answer blob.ErrExists. So copy-blobs and after-apply follow these
// rules:
//
//   - The restore verifies the bytes of the backup before any Put to a live
//     key. It copies each object to a temporary key of the restore and the
//     claim token, with the check of the size and the checksum, and then
//     copies the checked bytes to the live key.
//   - The restore never deletes a live key. A failed check deletes only the
//     temporary key. Cleanup deletes the temporary keys of the restore.
//   - A live key that exists is verified. When its bytes differ, the restore
//     stores the refusal as an audit event under the fence and fails with
//     ErrCorrupt. It writes nothing over the key, on every store.
//   - The restore reads and checks the staged copy before it writes the live key.
//     A staged copy that differs stops the restore with ErrCorrupt and writes
//     nothing to the live key.
//   - The claim is checked, with no write, just before each Put to a live key,
//     and the live key is read once more after that check. A store that ignores
//     conditional writes can still take a write of another writer between the
//     last read and the Put. Maintenance mode stops the other writers.
//   - The copy from the temporary key to the live key reads the temporary key
//     a second time, and checks those bytes after the Put. A temporary key
//     that answers other bytes the second time leaves a live key with bytes
//     that do not match, and the step fails. A retry finds that live key,
//     sees that its bytes differ, and fails the restore with ErrCorrupt, on
//     every store.
//   - After the swap, after-apply checks that each blob that the restored
//     data names exists, and copies a missing one again from the pinned
//     backup, before leave-maintenance.
//
// A restore that is checking or applying counts as running work for the guard
// of the blob deletes (blobguard.Watch): the store answers it from the
// restores row, in the same read that finds a running backup. So the deleters
// of the blobs that a backup or restored data can name wait while the restore
// runs. A deleter that read the guard before the restore started, and stalls
// until after the check of after-apply, can still delete a blob that the
// restored data names. The check of after-apply cannot close that window.
//
// # Failure, cancel and the end of the operation
//
// A step that fails for a reason that no retry can fix (a corrupt backup, a
// refused enter, a dev backup) ends the restore with FinishRestore, with the
// fence of the finish step and RestoreEnd.LeaveMaintenance. It returns the
// error as permanent. Any other error of a step is a retry with backoff, as
// for every durable operation.
//
// RestoreService.Cancel, and the route POST /api/server/restores/{id}/cancel,
// go through durable.StepCanceler.CancelBefore with the swap step: before the
// swap it stops the operation, and the swap transaction refuses to commit
// after it. After the swap it changes nothing, and the answer is
// ErrRestoreSwapped (409). A restore that ended answers 409 too, a restore
// that a cancel already stopped answers 202 again, and an unknown ID answers
// 404. Each cancel writes an audit entry.
//
// An operation can end with no finish write: a cancel, a step that panicked,
// or too many holders that stopped. RestoreService.Work then calls
// FailEndedRestores once in each lease period. It marks the row failed, and
// leaves maintenance mode when the flag of the row says that this restore
// entered it, in one transaction for each row. Before that it fences every
// namespace when such a restore is not check-only and recorded
// enter-maintenance, holds the maintenance flag of an enter that it did not
// confirm, or stored audit events (each forced cancel stores one first), and
// logs the restore IDs. A restore that stopped in stage or check changed no
// live data, so it fences nothing. While the row is checking or
// applying, no other restore can start, so that leave can never end the
// maintenance mode of a later restore.
//
// # What a takeover does at each step
//
// A takeover after the fenced write of a step skips the step. A takeover
// before it runs the step again:
//
//	stage              stages again into the area of the new token, after it drops the earlier areas
//	check              checks again; the area of the stage result is read-only
//	enter-maintenance  the flag of the row tells whether this restore entered; with the flag set and the state
//	                   maintenance it lists the active work again; with no flag, or the flag set and the state
//	                   normal, it enters; the confirm records the flag
//	pre-backup         waits for the backup operation of the pre-backup ID, which runs on its own
//	copy-blobs         verifies the live keys that the old holder copied
//	swap               the swap committed with its step record, or it did not commit at all
//	after-apply        runs the hook and the blob check again; the audit entry commits once, with the record
//	leave-maintenance  the state is normal, or the hold keeps maintenance; the confirm records it
//	cleanup            drops again; a drop of an area that does not exist does nothing
//	finish             finishes the row, which is still applying
//
// A restore that fails records the cleanup step, and after the swap the
// after-apply step, before the finish step, out of the order of the steps.
// The runner skips a recorded step, so the order does not matter.
//
// A holder that lost its claim and continues its work changes nothing that
// the fence protects: each of its fenced writes fails. Outside the fence it
// can write into its own staging area, copy checked bytes to a live key,
// copy a temporary key, and, with force, cancel active work, as above. Each
// cancel and each refusal to overwrite a live key is stored as an audit event of the restore under
// the fence before it happens, so a holder that stops after it leaves the
// record. The after-apply step, or the end of a failed restore, writes the
// audit entries of the events.
//
// A start of another instance does nothing to a running restore: it claims
// no operation with a live claim, and FailEndedRestores fails only rows whose
// operation ended.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/maintenance"
)

var (
	// ErrInvalidRestoreID reports a restore ID, or a staging area, that does
	// not have the form that MintRestoreID and StagingArea.String make.
	ErrInvalidRestoreID = errors.New("invalid restore ID")
	// ErrRestoreSwapped reports a cancel of a restore that already swapped
	// the staged data into the live data. Such a restore runs to its end.
	ErrRestoreSwapped = errors.New("restore already swapped the data and cannot be canceled")
	// ErrRestoreEventInvalid reports an audit event of a restore that is
	// empty, too long, holds a line break, or does not fit the row.
	ErrRestoreEventInvalid = errors.New("invalid restore audit event")
)

// RestoreOperationKind is the durable kind of a restore.
const RestoreOperationKind durable.Kind = "restore"

// RestoreOperationVersion is the version of the steps of
// RestoreOperationKind. A change of the step names or their order raises it.
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
	RestoreStepLeaveMaintenance = "leave-maintenance"
	RestoreStepCleanup          = "cleanup"
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
		RestoreStepLeaveMaintenance,
		RestoreStepCleanup,
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
	// DevReport, when it is not nil, replaces the dev report of the row: the
	// final report of the restore.
	DevReport *DevStripReport
	// LeaveMaintenance moves the installation to normal operation in the
	// transaction of the end, but only when the maintenance flag of the row
	// says that this restore entered maintenance mode and the state is still
	// maintenance. A failed restore whose maintenance hold is false sets it.
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
// durable.Store.RecordStep, in the transaction of its own write, and, unless
// it says otherwise, records its step there. It checks the fence first, so a
// stale claim changes nothing and learns nothing about the row: it returns
// durable.ErrClaimLost when f is not the live claim, also when the operation
// of f is not the operation of the restore, durable.ErrCanceled when a cancel
// is requested, and durable.ErrStepDone when the step of f is recorded. Then
// it returns ErrRestoreNotFound when the row does not exist, and
// ErrRestoreNotRunning when the row is not in the state that the method
// needs. On an error it changes nothing.
//
// The restores row has a maintenance flag: true while this restore holds the
// maintenance mode that it entered. Only SetRestoreMaintenance sets it.
//
//nolint:interfacebloat // One contract for the restores row of a running restore: every method reads or changes that row.
type RestoreOperations interface {
	// StartRestore stores r as a checking restore, with r.DevReport when it
	// is not nil, and starts the operation start, in one transaction. It
	// returns ErrInvalidRestoreID for an ID that ParseRestoreID refuses,
	// ErrRestoreExists when the ID is taken, ErrRestoreRunning when another
	// restore is checking or applying, and durable.ErrKeyConflict when start
	// is not of RestoreOperationKind under RestoreOperationKey(r.ID), or when
	// its key already names an operation. It returns durable.ErrInvalid for
	// a start that fails Validate. On an error it stores nothing.
	StartRestore(ctx context.Context, r RestoreRecord, start durable.StartRequest) error

	// GetRestore returns the restore row id, or ErrRestoreNotFound.
	GetRestore(ctx context.Context, id RestoreID) (RestoreRecord, error)

	// ListRestores returns every restore row, newest first.
	ListRestores(ctx context.Context) ([]RestoreRecord, error)

	// RecordRestoreReport stores report on the checking or applying restore
	// id and records the step of f.
	RecordRestoreReport(ctx context.Context, f durable.StepFence, id RestoreID, report DevStripReport) error

	// SetRestoreMaintenance stores the installation state next only when the
	// stored state is expected, with the rules of maintenance.Store.SetStateIf,
	// and sets the maintenance flag of the checking or applying restore id to
	// next == maintenance.StateMaintenance, in one transaction. It checks f
	// and records no step. It returns the errors of SetStateIf after the
	// errors of the fence and the row.
	SetRestoreMaintenance(
		ctx context.Context, f durable.StepFence, id RestoreID, expected, next maintenance.State,
	) error

	// ConfirmRestoreMaintenance checks that the installation state is want,
	// and records the step of f in one transaction. The result is
	// EncodeRestoreMaintenance of the maintenance flag of the checking or
	// applying restore id, of canceled, the units that the enter canceled,
	// and of the token of f. It returns the flag. It returns
	// maintenance.ErrStateChanged when the state is not want.
	ConfirmRestoreMaintenance(
		ctx context.Context, f durable.StepFence, id RestoreID, want maintenance.State, canceled []string,
	) (entered bool, err error)

	// MarkRestoreApplying moves the restore id from checking to applying,
	// records the pre-backup ID and the step of f with result, in one
	// transaction. A nil result records the step with no result.
	MarkRestoreApplying(ctx context.Context, f durable.StepFence, id RestoreID, preBackupID ID, result []byte) error

	// AppendRestoreAudit appends entries, in order, to the audit log of the
	// installation and records the step of f, in one transaction, so the
	// entries commit once. It returns the stored entries. The restore id must
	// be applying. It returns the error of an entry that is not valid, with
	// no change.
	AppendRestoreAudit(
		ctx context.Context, f durable.StepFence, id RestoreID, entries []audit.Entry,
	) ([]audit.Entry, error)

	// RecordRestoreEvent adds object, the object text of one audit entry,
	// to the audit events of the checking or applying restore id, under the
	// fence f, and records no step. The events live on the restores row, so
	// they survive the swap, which replaces the audit log; the restore
	// writes their audit entries after the swap, or at its end. An object
	// that the row already holds changes nothing. It checks f with the rules
	// of durable.Store.RecordStep, and checks object with
	// ValidateRestoreEvent. It returns the errors of the fence,
	// ErrRestoreNotFound, ErrRestoreNotRunning and ErrRestoreEventInvalid.
	RecordRestoreEvent(ctx context.Context, f durable.StepFence, id RestoreID, object string) error

	// CheckRestoreFence checks, with no write, that f is the live claim of
	// the operation of the checking or applying restore id. It records no
	// step and changes no row. A restore calls it before it writes a live
	// blob, because the write changes a store that the fence does not guard.
	// It checks f with the rules of durable.Store.RecordStep. It returns the
	// errors of the fence, ErrRestoreNotFound and ErrRestoreNotRunning.
	CheckRestoreFence(ctx context.Context, f durable.StepFence, id RestoreID) error

	// RestoreEvents returns the audit events of the restore id in the order
	// that RecordRestoreEvent added them, with no fence. It returns
	// ErrRestoreNotFound when id does not exist.
	RestoreEvents(ctx context.Context, id RestoreID) ([]string, error)

	// RestoreMaintenanceEntered returns the maintenance flag of the restore
	// id, with no fence: true while the restore holds the maintenance mode
	// that it entered. It returns ErrRestoreNotFound when id does not exist.
	RestoreMaintenanceEntered(ctx context.Context, id RestoreID) (bool, error)

	// FinishRestore moves the checking or applying restore end.ID to
	// end.State, with the error text, the end time and end.DevReport, and
	// records the step of f, in one transaction. With end.LeaveMaintenance
	// it leaves maintenance mode as RestoreEnd says, and clears the flag. It
	// returns the finished row. It returns ErrRestoreNotRunning for a state
	// other than RestoreSucceeded and RestoreFailed.
	FinishRestore(ctx context.Context, f durable.StepFence, end RestoreEnd) (RestoreRecord, error)

	// FailEndedRestores marks failed each checking or applying row whose
	// operation ended canceled or failed, with the error text
	// RestoreCanceledText or RestoreStoppedText and the end time endedAt.
	// When leave is true and the maintenance flag of the row is set, it moves
	// the installation to normal operation, if the state is maintenance, and
	// clears the flag, in the transaction of that row. It keeps a row whose
	// operation is pending or runs, also with an expired claim. It returns
	// the rows that it marked failed. It takes no fence: it changes only rows
	// whose operation ended.
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

// RestoreStageInput is what StageRestore loads into a staging area and how
// it removes dev data from it.
type RestoreStageInput struct {
	Open    PortableOpener
	Want    Summary
	Files   []PortableFile
	Options StageOptions
	// Strip runs the dev data strip with StripMode on the staged area, before
	// StageRestore returns. A restore into a production installation sets it.
	Strip     bool
	StripMode DevStripMode
}

// IssuerKeysDropped names a namespace whose signing keys of the OIDC issuer a
// restore did not restore, and how many keys it dropped. A namespace that held
// only the record of a copy check has Keys 0.
type IssuerKeysDropped struct {
	Namespace string
	Keys      int64
}

// RestoreStaging is the staging port of a restore that runs as a durable
// operation. Each area has a name that a StagingArea gives, so every
// instance on the database finds the area that a stage result names. It
// replaces the staging area that a store adapter held in its memory. Each SQL
// store adapter implements it, and runs the conformance cases of package
// storetest.
//
// A method that takes a StagingArea derives the name of its schema or its
// file from the area only. Before it changes anything, it checks the area
// with NewStagingArea and returns ErrInvalidRestoreID for an area that fails.
// It never reads a name from the area in another way.
type RestoreStaging interface {
	// StageRestore drops each area of a.Restore with a token up to a.Token,
	// a itself included, then stages in.Files into a with the checks of
	// PortableStager.StagePortable, and, with in.Strip, runs the dev data
	// strip of DevStripper.StripDevStaged on a. It returns the summary and
	// the report of the strip, which is empty without in.Strip. A failed
	// stage drops a. The live data stays unchanged. It never touches an area
	// of a higher token: that is the area of a newer holder. It returns
	// ErrInvalidRestoreID for an area that is not valid, the errors of
	// CheckStageKind and CheckStageSchema, ErrCorrupt when the staged data
	// does not match in.Want, ErrDevBackupRefused and ErrDevStripIncomplete
	// from the strip, and the error of a table file that open fails to read.
	// The stage result of the runner names a after it returns. From then on
	// the area is read-only until ApplyRestoreArea.
	StageRestore(ctx context.Context, a StagingArea, in RestoreStageInput) (Summary, DevStripReport, error)

	// DropStagedIssuerKeys removes from the staged area a the signing keys of
	// the OIDC issuers, and the record of the last copy check, of every
	// namespace whose primary installation is not keep, as the staged data
	// says it. An empty keep keeps no namespace, and a key whose namespace is
	// not in the staged data is not kept. The decision is for each namespace:
	// a backup of two installations keeps the keys of one, so two
	// installations never sign with one key. It returns one entry for each
	// namespace that lost keys or a copy check, ordered by name. The settings
	// of the issuers stay. ErrInvalidRestoreID for an area that is not valid,
	// ErrCorrupt when a does not exist. The live data stays unchanged.
	DropStagedIssuerKeys(ctx context.Context, a StagingArea, keep string) ([]IssuerKeysDropped, error)

	// ApplyRestoreArea swaps the area a into the live data, as
	// Applier.ApplyStaging does, or as AccessKeepingApplier does when
	// keepAccess is true. It checks f in the swap transaction, before the
	// first change, with the rules of durable.Store.RecordStep, so a cancel
	// with CancelBefore and the swap have one winner, and records the step in
	// the same transaction with EncodeRestoreSwap of its result. On PostgreSQL the old live data stays as the previous
	// schema of the restore of a, for CleanRestoreAreas. It returns
	// ErrInvalidRestoreID for an area that is not valid, ErrCorrupt when a does
	// not exist, the errors of the fence, ErrDevBackupRefused and
	// ErrRestoreAccessMismatch, with no change.
	ApplyRestoreArea(
		ctx context.Context, f durable.StepFence, a StagingArea, keepAccess bool,
	) (KeepAccessResult, error)

	// CleanRestoreAreas drops the old live data and every staging area of the
	// restore id, and every area of a restore whose row in this registry
	// ended. It keeps the areas of a restore that has no row in this
	// registry, and of a running restore other than id. Each drop waits for
	// its locks for lockWait at most. It checks f before the first drop and
	// records its step: on PostgreSQL in the transaction of the drops, and on
	// SQLite after the file removals. It returns the areas that it dropped. It
	// returns ErrInvalidRestoreID for an id that is not valid, the errors of
	// the fence, also durable.ErrClaimLost when the operation of f is not the
	// operation of restore id, and ErrRestoreBusy when a drop waited lockWait,
	// with no drop on PostgreSQL. The caller retries ErrRestoreBusy.
	CleanRestoreAreas(
		ctx context.Context, f durable.StepFence, id RestoreID, lockWait time.Duration,
	) ([]StagingArea, error)

	// ListRestoreAreas returns the staging areas of this database, also the
	// areas of a restore that has no row in this registry, ordered by restore
	// and token. A schema or a file whose name has the staging prefix but
	// does not parse is not listed. It returns the read error of the store.
	ListRestoreAreas(ctx context.Context) ([]StagingArea, error)
}

// ErrRestoreBusy reports a drop of the cleanup that waited for its locks
// longer than the limit. The cleanup step tries again with backoff.
var ErrRestoreBusy = errors.New("restore cleanup waits for a lock on the old data")

// RestoreMaintenanceResult is the result that ConfirmRestoreMaintenance
// records with the step: whether this restore entered maintenance mode, the
// units that the enter canceled, and the claim token of the step. The swap
// replaces the audit log, so the after-apply step writes the audit entry of
// the canceled units again from this result.
type RestoreMaintenanceResult struct {
	Entered  bool     `json:"entered"`
	Canceled []string `json:"canceled"`
	Token    int64    `json:"token"`
}

// maxCanceledUnits bounds the units that one maintenance result names.
const maxCanceledUnits = 10000

// EncodeRestoreMaintenance returns the step result that
// ConfirmRestoreMaintenance records for r.
func EncodeRestoreMaintenance(r RestoreMaintenanceResult) ([]byte, error) {
	if r.Canceled == nil {
		r.Canceled = []string{}
	}

	if len(r.Canceled) > maxCanceledUnits {
		return nil, fmt.Errorf("%w: the maintenance result names %d canceled units, more than %d",
			ErrCorrupt, len(r.Canceled), maxCanceledUnits)
	}

	out, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("encode the result of the maintenance step: %w", err)
	}

	return out, nil
}

// DecodeRestoreMaintenance returns the result that EncodeRestoreMaintenance
// made. It returns ErrCorrupt for any other result.
func DecodeRestoreMaintenance(result []byte) (RestoreMaintenanceResult, error) {
	var out RestoreMaintenanceResult

	decoder := json.NewDecoder(bytes.NewReader(result))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&out); err != nil || len(out.Canceled) > maxCanceledUnits || out.Token < 0 {
		return RestoreMaintenanceResult{Entered: false, Canceled: nil, Token: 0},
			fmt.Errorf("%w: the result of the maintenance step does not decode", ErrCorrupt)
	}

	return out, nil
}

// restoreSwapResult is the result that ApplyRestoreArea records with the
// swap step.
type restoreSwapResult struct {
	DroppedTempAdmins int64 `json:"droppedTempAdmins"`
}

// EncodeRestoreSwap returns the step result that ApplyRestoreArea records
// for kept.
func EncodeRestoreSwap(kept KeepAccessResult) []byte {
	out, err := json.Marshal(restoreSwapResult(kept))
	if err != nil {
		// A struct of one integer always encodes.
		return []byte(`{"droppedTempAdmins":0}`)
	}

	return out
}

// DecodeRestoreSwap returns the result of a step result that
// EncodeRestoreSwap made. It returns ErrCorrupt for any other result.
func DecodeRestoreSwap(result []byte) (KeepAccessResult, error) {
	var out restoreSwapResult

	decoder := json.NewDecoder(bytes.NewReader(result))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&out); err != nil || out.DroppedTempAdmins < 0 {
		return KeepAccessResult{DroppedTempAdmins: 0}, fmt.Errorf("%w: the result of the swap step does not decode",
			ErrCorrupt)
	}

	return KeepAccessResult(out), nil
}

// MaxRestoreEvents bounds the audit events of one restore.
const MaxRestoreEvents = 1000

// maxRestoreEventBytes bounds the object text of one audit event.
const maxRestoreEventBytes = 2048

// ValidateRestoreEvent returns ErrRestoreEventInvalid for an object text that
// is empty, longer than 2048 bytes, or holds a carriage return or a line feed.
func ValidateRestoreEvent(object string) error {
	if object == "" || len(object) > maxRestoreEventBytes || strings.ContainsAny(object, "\r\n") {
		return fmt.Errorf("%w: %d bytes", ErrRestoreEventInvalid, len(object))
	}

	return nil
}

// AddRestoreEvent returns the encoded events of the stored text with object
// added at the end, and whether it changed them. An object that the events
// already hold changes nothing. The last of the MaxRestoreEvents places holds
// a summary event: once the other places are full, each new object adds one
// to the count of that summary instead, so a forced enter that cancels more
// units than the limit still records each cancel, and never fails for the
// limit. The count is an upper bound: an omitted event that a caller sends
// again adds one again. It returns ErrRestoreEventInvalid for an object that
// ValidateRestoreEvent refuses, and ErrCorrupt for stored text that
// DecodeRestoreEvents refuses.
func AddRestoreEvent(stored, object string) (string, bool, error) {
	if err := ValidateRestoreEvent(object); err != nil {
		return "", false, err
	}

	if strings.HasPrefix(object, restoreEventOverflowPrefix) {
		return "", false, fmt.Errorf("%w: the object has the form of the summary event", ErrRestoreEventInvalid)
	}

	events, err := DecodeRestoreEvents(stored)
	if err != nil {
		return "", false, err
	}

	if slices.Contains(events, object) {
		return stored, false, nil
	}

	switch {
	case len(events) < MaxRestoreEvents-1:
		events = append(events, object)
	case len(events) == MaxRestoreEvents-1:
		events = append(events, restoreEventOverflow(1))
	default:
		count, ok := restoreEventOverflowCount(events[len(events)-1])
		if !ok {
			return "", false, fmt.Errorf("%w: the last restore audit event is not the summary", ErrCorrupt)
		}

		events[len(events)-1] = restoreEventOverflow(count + 1)
	}

	out, err := json.Marshal(events)
	if err != nil {
		return "", false, fmt.Errorf("encode the restore audit events: %w", err)
	}

	return string(out), true, nil
}

// restoreEventOverflowPrefix starts every summary event of the events past
// the limit, with or without the upper bound that follows it.
const restoreEventOverflowPrefix = "restore audit events past the limit of 999: "

// restoreEventOverflowHead starts the summary event of the events past the
// limit. The count that follows is an upper bound on the omitted events.
const restoreEventOverflowHead = restoreEventOverflowPrefix + "up to "

// restoreEventOverflow returns the summary event of count events past the
// limit. count is an upper bound: an omitted event that a caller sends again
// adds one again.
func restoreEventOverflow(count int64) string {
	return restoreEventOverflowHead + strconv.FormatInt(count, 10) + " more cancels or refusals, in the log lines"
}

// restoreEventOverflowCount returns the count of a summary event, and
// whether event is one. The count is an upper bound on the omitted events.
func restoreEventOverflowCount(event string) (int64, bool) {
	rest, ok := strings.CutPrefix(event, restoreEventOverflowHead)
	if !ok {
		return 0, false
	}

	digits, _, ok := strings.Cut(rest, " ")
	if !ok {
		return 0, false
	}

	count, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || count < 1 {
		return 0, false
	}

	return count, true
}

// DecodeRestoreEvents returns the events of the stored text of a restores
// row. It returns ErrCorrupt for text that is not a JSON list of strings, or
// for an event that ValidateRestoreEvent refuses.
func DecodeRestoreEvents(stored string) ([]string, error) {
	var events []string

	if err := json.Unmarshal([]byte(stored), &events); err != nil {
		return nil, fmt.Errorf("%w: the restore audit events do not decode", ErrCorrupt)
	}

	if len(events) > MaxRestoreEvents {
		return nil, fmt.Errorf("%w: the restore holds %d audit events", ErrCorrupt, len(events))
	}

	for _, event := range events {
		// Defense in depth: the record checks each event before it stores
		// it; this check stays so that a row of another version, or a
		// restored backup, puts no other text into an audit entry.
		if err := ValidateRestoreEvent(event); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrCorrupt, err)
		}
	}

	if events == nil {
		events = []string{}
	}

	return events, nil
}
