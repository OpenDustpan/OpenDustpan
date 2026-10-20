package backup

import (
	"context"
	"errors"

	"opendustpan.org/opendustpan/pkg/installation"
)

// DevBackupFlag is the start flag that lets a production installation
// restore a backup that a dev installation made. It is a flag of the command
// line only: no environment variable, no configuration file and no setting of
// the API sets it. Each refusal names it after plain words.
const DevBackupFlag = "--unsafe-allow-restoring-dev-installation-backups-into-production"

// Sentinel errors of the dev data rules of a restore.
var (
	// ErrDevBackupRefused reports that a production installation refuses to
	// restore a backup that a dev installation made, because the server did
	// not start with DevBackupFlag. The restore changes nothing.
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

// StageKindChecker is the second refusal of a dev backup. The store adapter
// calls it inside its own stage step, with the kind that it reads from its own
// database. It does not rely on the check of the restore service or on the
// check of the manifest.
type StageKindChecker interface {
	// CheckStageKind returns ErrDevBackupRefused when the live database is of
	// the kind production, the backup is of the kind dev and allowDev is
	// false. It returns ErrManifestKindInvalid when backupKind is not a known
	// kind. It returns an error that wraps installation.ErrUnavailable when
	// the live kind cannot be read. It changes nothing.
	CheckStageKind(ctx context.Context, backupKind installation.Kind, allowDev bool) error
}

// DevRemovalClass names one class of data that the strip removes.
type DevRemovalClass string

// Classes of removed data. Each class gets its own warning in the log and its
// own count in the audit entry of the restore.
const (
	// RemovedPrincipals counts principals with a subject that starts with the
	// reserved prefix, without regard to case.
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
	Subject string
	// Counts holds the count of removed rows for each class, without
	// RemovedPrincipals and RemovedDangling.
	Counts map[DevRemovalClass]int64
}

// DevStripReport is what a strip removed, or, for a check-only restore, what
// it would remove.
type DevStripReport struct {
	// Principals holds one entry for each removed principal.
	Principals []DevRemoval
	// Classes holds the total count for each class, with the classes that
	// have no principal: RemovedProviders and RemovedDangling.
	Classes map[DevRemovalClass]int64
}

// DevStripper removes dev principals and everything that depends on them from
// the staged restore data. The restore service calls it after the stage step
// and before the apply step, so the live data never holds a dev principal
// from the backup, and a failure leaves the live data unchanged.
type DevStripper interface {
	// StripDevStaged removes from the staging area the data of the classes of
	// DevRemovalClass, then proves that the staging area holds no principal
	// with the reserved prefix and no row that names one. The reserved prefix
	// is compared without regard to case. It never trusts the
	// foreign keys of the staged data: it also removes rows that name a
	// missing principal. It returns ErrDevStripIncomplete when data remains
	// or when a step fails, and it leaves the staging area for the caller to
	// discard. With dryRun true it removes nothing and reports what it would
	// remove. It never removes an audit entry.
	StripDevStaged(ctx context.Context, dryRun bool) (DevStripReport, error)
}

// RestoreWarningCode identifies a warning that the answer of a restore
// carries, so a client can show its own text and still test the code.
type RestoreWarningCode string

// Codes of restore warnings.
const (
	// WarnDevBackupIntoProduction says that the restore removes the dev
	// people and their grants, that this may break things that depend on
	// them, and that restoring a dev backup into a production installation is
	// not recommended.
	WarnDevBackupIntoProduction RestoreWarningCode = "dev-backup-into-production"
)

// RestoreWarning is a warning in the answer of the restore API. The restore
// goes on after it. Text is plain words, then the flag and the IDs in code
// quotes.
type RestoreWarning struct {
	Code RestoreWarningCode
	Text string
}
