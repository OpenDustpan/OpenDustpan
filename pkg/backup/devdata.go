package backup

import (
	"context"
	"errors"
	"fmt"
	"time"

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
	// RestoreAt is the time of the restore, from the clock of the restore
	// service. A staged alert marker with a later time is corrupt.
	RestoreAt time.Time
}

// CheckStageKind is the check that a stage call makes before it loads a row.
// Every store adapter calls it with the options of the stage call and the live
// kind that it read from its own database. It returns
// ErrManifestKindInvalid when opts.BackupKind is not a known kind,
// ErrDevBackupRefused when the live installation is a production installation,
// the backup claims the kind dev and opts.AllowDev is false, and nil
// otherwise. A live kind that is not a known kind returns
// installation.ErrUnavailable: the check never passes because it could not
// read.
func CheckStageKind(opts StageOptions, live installation.Kind) error {
	if !live.Valid() {
		return fmt.Errorf("stage portable backup: read the live installation kind: %w", installation.ErrUnavailable)
	}

	if !opts.BackupKind.Valid() {
		return fmt.Errorf("stage portable backup: %w", ErrManifestKindInvalid)
	}

	if live == installation.KindProduction && opts.BackupKind == installation.KindDev && !opts.AllowDev {
		return fmt.Errorf("stage portable backup: %w", ErrDevBackupRefused)
	}

	return nil
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
	// RemovedSessions counts sessions of a removed principal, and sessions
	// that the reserved provider ID made.
	RemovedSessions DevRemovalClass = "sessions"
	// RemovedAccessTokens counts access tokens of a removed principal.
	RemovedAccessTokens DevRemovalClass = "access-tokens"
	// RemovedPreferences counts the preferences rows of a removed principal.
	RemovedPreferences DevRemovalClass = "preferences"
	// RemovedForgeLinks counts the Git host account links of a removed
	// principal.
	RemovedForgeLinks DevRemovalClass = "git-host-links"
	// RemovedForgeLinkRequests counts the link requests of a removed principal.
	RemovedForgeLinkRequests DevRemovalClass = "git-host-link-requests"
	// RemovedSignInRequests counts open sign-in requests that name the
	// reserved provider ID. A sign-in request holds no principal, so the class
	// has no principal.
	RemovedSignInRequests DevRemovalClass = "sign-in-requests"
	// RemovedProviders counts sign-in providers and issuer records with the
	// reserved provider ID.
	RemovedProviders DevRemovalClass = "providers"
	// RemovedTempAdmins counts temp_admins rows, matched by the principal ID
	// of a removed principal and by a subject with the reserved prefix.
	RemovedTempAdmins DevRemovalClass = "temp-admins"
	// RemovedTrustRules counts the trust rules whose principal is a removed
	// principal. A rule lets a workload act as its principal, so it goes
	// with the principal.
	RemovedTrustRules DevRemovalClass = "trust-rules"
	// RemovedLocalUsers counts the local user records of a removed
	// principal, with the user name and the password hash. The demo person
	// of a dev installation has one.
	RemovedLocalUsers DevRemovalClass = "local-users"
	// RemovedSessionGenerations counts the session generation rows of a
	// removed principal.
	RemovedSessionGenerations DevRemovalClass = "session-generations"
	// RemovedPasskeys counts the passkeys of a removed principal.
	RemovedPasskeys DevRemovalClass = "passkeys"
	// RemovedApprovers counts dev subjects removed from Approvers.Users of
	// approval rules, approval defaults and ownership rules. The rule stays.
	// A rule that loses its last approver gets Users with the value
	// gate.NoApprover, so nobody can meet it and the gate refuses the change
	// that it covers. The report names each such object in
	// DevStripReport.Approvers.
	RemovedApprovers DevRemovalClass = "approvers"
	// RemovedDangling counts grants, group memberships, sessions, tokens,
	// preferences, trust rules, local user records, session generations and
	// passkeys that name a principal
	// that does not exist, or a subject with the reserved prefix and no
	// principal row. Data that was already wrong
	// before the restore is in this class.
	RemovedDangling DevRemovalClass = "dangling"
)

// MaxReportedApprovers is the most objects that DevStripReport.Approvers
// names. The count of RemovedApprovers in DevStripReport.Classes holds the
// total.
const MaxReportedApprovers = 100

// DevRemoval is one removed principal with the counts of the rows that went
// with it. The audit entry of the restore names the subject, never a token or
// a session ID.
type DevRemoval struct {
	// Subject has no control character and at most MaxReportedSubjectBytes.
	Subject string `json:"subject"`
	// SubjectCut is true when the cleaning cut Subject.
	SubjectCut bool `json:"subjectCut"`
	// Counts holds the count of removed rows for each class that a principal
	// owns: RemovedGrants, RemovedGroupMembers, RemovedSessions,
	// RemovedAccessTokens, RemovedPreferences, RemovedForgeLinks,
	// RemovedForgeLinkRequests, RemovedTempAdmins, RemovedTrustRules,
	// RemovedLocalUsers, RemovedSessionGenerations and RemovedPasskeys. The
	// other classes are not in it.
	Counts map[DevRemovalClass]int64 `json:"counts"`
}

// DevApproverRemoval names one object of the policy model that lost a dev
// subject from Approvers.Users.
type DevApproverRemoval struct {
	// Kind is the kind of the object: approval-rule, approval-default or
	// ownership-rule.
	Kind string `json:"kind"`
	// Scope is the scope key of the object.
	Scope string `json:"scope"`
	// ID is the ID of the object, and empty for an approval default.
	ID string `json:"id"`
	// Replaced is true when the strip removed the last approver and wrote
	// Users with the value gate.NoApprover, so the gate refuses the change
	// that the object covers.
	Replaced bool `json:"replaced"`
}

// DevStripReport is what a strip removed, or, for a check-only restore, what
// it would remove.
type DevStripReport struct {
	// Principals holds one entry for each removed principal, up to
	// MaxAuditedPrincipals.
	Principals []DevRemoval `json:"principals"`
	// OtherPrincipals is the number of removed principals beyond the entries
	// of Principals. OtherCounts is the sum of their row counts. One summary
	// entry in the audit log holds both.
	OtherPrincipals int64                     `json:"otherPrincipals"`
	OtherCounts     map[DevRemovalClass]int64 `json:"otherCounts"`
	// Classes holds the total count of each class, also of the rows that
	// belong to no removed principal.
	Classes map[DevRemovalClass]int64 `json:"classes"`
	// Approvers names the objects that lost a dev subject, up to
	// MaxReportedApprovers. The count of RemovedApprovers in Classes holds
	// the total.
	Approvers []DevApproverRemoval `json:"approvers"`
	// Warnings holds the codes of the warnings that the answer of the restore
	// carries. The restore service sets them. A strip leaves them empty.
	Warnings []RestoreWarningCode `json:"warnings"`
	// PreBackupFound is what the backup before the restore found at the live
	// blobs. It is nil until the restore took that backup, and nil when the
	// backup found every live blob whole. The restore service sets it.
	PreBackupFound *BlobFindings `json:"preBackupFound,omitempty"`
	// NotHeld is the damaged list of the restored backup: the blobs that its
	// data names and that it holds no copy of. The restore leaves those live
	// keys as they are. It is nil for a backup with no such blob. The restore
	// service sets it.
	NotHeld *BlobFindings `json:"notHeld,omitempty"`
	// ErrorCode is the catalog code of the refusal that ended the restore,
	// empty for a restore that succeeded or that ended with an error that
	// has no code. The restore service sets it.
	ErrorCode RestoreErrorCode `json:"errorCode,omitempty"`
}

// HasDev reports whether the report names dev data: a principal, or a class
// other than RemovedDangling with a count above zero. Dangling rows alone are
// not dev data: the strip removes them only together with dev data, and a
// restore of a backup that has them and no dev data changes no row.
func (r DevStripReport) HasDev() bool {
	if len(r.Principals) > 0 || r.OtherPrincipals > 0 {
		return true
	}

	for class, n := range r.Classes {
		if n > 0 && class != RemovedDangling {
			return true
		}
	}

	return false
}

// Empty reports whether the report names no removal: no principal and no
// class with a count above zero.
func (r DevStripReport) Empty() bool {
	if len(r.Principals) > 0 || r.OtherPrincipals > 0 {
		return false
	}

	for _, n := range r.Classes {
		if n > 0 {
			return false
		}
	}

	return true
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
	// WarnDevBackupWouldRemove says that a check-only restore would remove the
	// dev people and their grants, and removed nothing.
	WarnDevBackupWouldRemove RestoreWarningCode = "dev-backup-would-remove"
	// WarnProductionBackupIntoDev is an info warning: a dev installation
	// restored a production backup, so production tokens and sessions now
	// sit in a dev data directory.
	WarnProductionBackupIntoDev RestoreWarningCode = "production-backup-into-dev"
	// WarnAuditAppendFailed says that the restore finished but an audit
	// entry could not be written. The restore record holds the report.
	WarnAuditAppendFailed RestoreWarningCode = "audit-append-failed"
	// WarnMissingLiveBlobs says that the backup before the restore found live
	// blobs missing, within the bound of the restore. The restore wrote the
	// checked bytes of the backup at each of those keys that the backup holds.
	WarnMissingLiveBlobs RestoreWarningCode = "missing-live-blobs"
	// WarnBlobsNotHeld says that the restored backup holds no copy of some
	// blobs that its data names: it is a backup before an earlier restore,
	// and that backup found them missing or damaged. The restore leaves those
	// live keys as they are.
	WarnBlobsNotHeld RestoreWarningCode = "blobs-not-held"
)

// RestoreWarning is a warning in the answer of the restore API. The restore
// goes on after it. Text is plain words, then the flag and the IDs in code
// quotes.
type RestoreWarning struct {
	Code RestoreWarningCode
	Text string
}
