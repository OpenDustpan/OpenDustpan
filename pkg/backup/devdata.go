package backup

import (
	"context"
	"errors"

	"opendustpan.org/opendustpan/pkg/installation"
)

// MaxReportedSubjectBytes is the longest subject that an audit entry, the
// restore record, a log line or an API answer carries. It equals the longest
// subject that the API makes. A subject from a backup is untrusted: the code
// removes CR, LF and every other control character from it and cuts it to
// this length before it writes it.
const MaxReportedSubjectBytes = 255

// MaxAuditedPrincipals is the most removed principals that get an entry of
// their own in the audit log of one restore. One summary entry holds the rest.
const MaxAuditedPrincipals = 100

// Sentinel errors of the dev data rules of a restore.
var (
	// ErrDevBackupRefused reports that a production installation refuses to
	// restore a backup that holds dev data, because the server did not start
	// with the setting that allows it. The backup claims the kind dev, or the
	// staged data holds a dev subject. The restore changes nothing. The API
	// answers with status 409.
	ErrDevBackupRefused = errors.New("a production installation refuses a backup from a dev installation")
	// ErrManifestKindInvalid reports a manifest with no installation kind, or
	// with a value that is not a known kind. The backup is not valid and the
	// restore refuses it. No conversion exists for it.
	ErrManifestKindInvalid = errors.New("the backup manifest has no valid installation kind")
	// ErrDevStripIncomplete reports that dev data remains in the staged data
	// after the strip, or that the strip could not finish. The restore
	// discards the staged data and changes nothing.
	ErrDevStripIncomplete = errors.New("the strip of dev data did not finish")
)

// KindReader reads the installation kind without a claim. The backup export
// reads the kind of its own database through it, so a backup never records a
// kind that a caller passed in. A store adapter implements it after the
// conformance suite in pkg/store/storetest checks it.
type KindReader interface {
	// ReadKind returns the kind that the database holds. It returns
	// installation.ErrUnavailable when no kind is claimed yet, when the row
	// is missing, when the kind cannot be read, and when the stored value is
	// not a known kind. The export then fails and writes no backup. It
	// changes nothing.
	ReadKind(ctx context.Context) (installation.Kind, error)
}

// StageOptions carries to the stage call what the stage call needs to refuse
// a dev backup by itself. A caller cannot skip the check: the stage call
// takes the options as an argument, and the adapter compares them with the
// live kind that it reads from its own database.
type StageOptions struct {
	// BackupKind is the kind that the manifest claims. The manifest is not
	// authenticated, so the claim is never the only input to the decision.
	BackupKind installation.Kind
	// AllowDev is true when the server started with the setting that allows a
	// dev backup in a production installation.
	AllowDev bool
}

// DevReportRecorder stores the removal report in the restore record before
// the swap. The restores table survives the swap, so the report is still
// there if the audit entries cannot be written.
type DevReportRecorder interface {
	// RecordDevReport stores report in the record of restore id. It returns
	// ErrRestoreNotFound for an unknown ID.
	RecordDevReport(ctx context.Context, id RestoreID, report DevStripReport) error
}

// DevRemovalClass names one class of data that the strip removes.
type DevRemovalClass string

// Classes of removed data. Each class gets its own warning in the log and its
// own count in the audit entry of the restore.
const (
	// RemovedPrincipals counts principals with a subject that starts with the
	// reserved prefix. The match is the one of principal.IsDevSubject: the
	// first 4 bytes, ASCII case-insensitive.
	RemovedPrincipals DevRemovalClass = "principals"
	// RemovedGrants counts grants of a removed principal.
	RemovedGrants DevRemovalClass = "grants"
	// RemovedGroupMembers counts group memberships of a removed principal.
	RemovedGroupMembers DevRemovalClass = "group-members"
	// RemovedSessions counts sessions of a removed principal.
	RemovedSessions DevRemovalClass = "sessions"
	// RemovedAccessTokens counts access tokens of a removed principal.
	RemovedAccessTokens DevRemovalClass = "access-tokens"
	// RemovedSignInRequests counts open sign-in requests of a removed
	// principal.
	RemovedSignInRequests DevRemovalClass = "sign-in-requests"
	// RemovedProviders counts sign-in providers with the reserved provider ID.
	RemovedProviders DevRemovalClass = "providers"
	// RemovedTempAdmins counts temp_admins rows, matched by the principal ID
	// of a removed principal and by a subject with the reserved prefix.
	RemovedTempAdmins DevRemovalClass = "temp-admins"
	// RemovedApprovers counts dev subjects removed from the approvers and
	// owners of approval rules and ownership rules. The rule stays, so a
	// rule with no approver left cannot be met and the gate refuses.
	RemovedApprovers DevRemovalClass = "approvers"
	// RemovedDangling counts grants, group memberships, sessions and tokens
	// that name a principal that does not exist, or a subject with the
	// reserved prefix and no principal row. Data that was already wrong
	// before the restore is in this class.
	RemovedDangling DevRemovalClass = "dangling"
)

// DevRemoval is one removed principal with the counts of the rows that went
// with it. The audit entry of the restore names the subject, never a token or
// a session ID.
type DevRemoval struct {
	// Subject has no control character and at most MaxReportedSubjectBytes.
	Subject string
	// SubjectCut is true when the cleaning cut Subject.
	SubjectCut bool
	// Counts holds the count of removed rows for each class, without
	// RemovedPrincipals and RemovedDangling.
	Counts map[DevRemovalClass]int64
}

// DevStripReport is what a strip removed, or, for a check-only restore, what
// it would remove.
type DevStripReport struct {
	// Principals holds one entry for each removed principal, up to
	// MaxAuditedPrincipals.
	Principals []DevRemoval
	// OtherPrincipals is the number of removed principals beyond the entries
	// of Principals. OtherCounts is the sum of their row counts. One summary
	// entry in the audit log holds both.
	OtherPrincipals int64
	OtherCounts     map[DevRemovalClass]int64
	// Classes holds the total count for each class, with the classes that
	// have no principal: RemovedProviders and RemovedDangling.
	Classes map[DevRemovalClass]int64
}

// DevStripMode tells StripDevStaged to look or to remove.
type DevStripMode int

// Modes of the strip.
const (
	// DevStripDetect reports what the staging area holds and removes nothing.
	// The restore service runs it on every restore of a production
	// installation, and for a check-only restore.
	DevStripDetect DevStripMode = iota
	// DevStripRemove removes the data, then proves that none remains.
	DevStripRemove
)

// DevStripper removes dev principals and everything that depends on them from
// the staged restore data. The restore service calls it after the stage step
// and its count checks, and before the apply step. It never touches a live
// table, so a failure leaves the live data unchanged. It runs on the restore
// that keeps the access tables too, for the staged tables that the swap takes.
type DevStripper interface {
	// StripDevStaged works on the staging area. The classes are those of
	// DevRemovalClass. The match of a subject is the one of
	// principal.IsDevSubject, in SQL, for both adapters. It never trusts the
	// foreign keys of the staged data: it also finds rows that name a missing
	// principal. With DevStripDetect it removes nothing and returns the
	// report. With DevStripRemove it removes, then proves that the staging
	// area holds no principal with the reserved prefix and no row that names
	// one. It returns ErrDevStripIncomplete when data remains or a step
	// fails, and it leaves the staging area for the caller to discard. It
	// never removes an audit entry or a decision record.
	StripDevStaged(ctx context.Context, mode DevStripMode) (DevStripReport, error)
}

// RestoreWarningCode identifies a warning that the answer of a restore
// carries, so a client can show its own text and still test the code.
type RestoreWarningCode string

// Codes of restore warnings.
const (
	// WarnDevBackupIntoProduction says that the restore removed the dev
	// people and their grants, that this may break things that depend on
	// them, and that we do not recommend this restore.
	WarnDevBackupIntoProduction RestoreWarningCode = "dev-backup-into-production"
	// WarnProductionBackupIntoDev is an info warning: a dev installation
	// restored a production backup, so production tokens and sessions now
	// sit in a dev data directory.
	WarnProductionBackupIntoDev RestoreWarningCode = "production-backup-into-dev"
	// WarnAuditAppendFailed says that the restore finished but an audit
	// entry could not be written. The restore record holds the report.
	WarnAuditAppendFailed RestoreWarningCode = "audit-append-failed"
)

// RestoreWarning is a warning in the answer of the restore API. The restore
// goes on after it. Text is plain words, then the flag and the IDs in code
// quotes.
type RestoreWarning struct {
	Code RestoreWarningCode
	Text string
}
