package backup

import (
	"context"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/principal"
)

// ErrPreBackupForeign reports that the backup before a restore is not one
// that this restore started: a pre-backup ID that names a backup that
// exists, a backup that another restore or a person started, or a restore
// step that finds no stored result of its own pre-backup step. The restore
// stops before any change of the live data. It does not wrap ErrCorrupt:
// the backup that the restore reads is not at fault.
var ErrPreBackupForeign = errors.New("the backup before the restore is not one that this restore started")

// ErrDamagedListForeign reports a manifest with a damaged list in a backup
// that no restore of this installation started. Only a backup before a
// restore records damage, so such a manifest is corrupt. It wraps
// ErrCorrupt.
var ErrDamagedListForeign = errors.New("the backup names damaged live blobs, but no restore of this installation " +
	"started it")

// RestoreErrorCode is the catalog code of a restore refusal. The restore row
// stores it next to the error text, and the restore answer shows it, so a
// client chooses its help from the code and not from the text.
type RestoreErrorCode string

// The codes of the restore refusals that carry one. Each is in the error
// catalog of the API.
const (
	// CodeBlobsNotBacked is the code of a backup whose data names blobs that
	// it does not hold.
	CodeBlobsNotBacked RestoreErrorCode = "restore.blobs-missing"
	// CodeLiveBlobsDamaged is the code of a restore that stopped at live
	// blobs whose bytes do not match their records or do not open.
	CodeLiveBlobsDamaged RestoreErrorCode = "restore.live-blobs-damaged"
	// CodeLiveBlobsMissing is the code of a restore that stopped at more
	// missing live blobs than the bound allows.
	CodeLiveBlobsMissing RestoreErrorCode = "restore.live-blobs-missing"
	// CodePreBackupForeign is the code of a restore that stopped because the
	// backup before it is not one that it started.
	CodePreBackupForeign RestoreErrorCode = "restore.pre-backup-foreign"
	// CodeNoWorkingBreakGlass is the code of a restore that stopped because
	// it would leave too few working break-glass accounts.
	CodeNoWorkingBreakGlass RestoreErrorCode = "principal.no-working-break-glass"
)

// RestoreErrorCodes lists every code that a restore row can hold.
func RestoreErrorCodes() []RestoreErrorCode {
	return []RestoreErrorCode{
		CodeBlobsNotBacked, CodeLiveBlobsDamaged, CodeLiveBlobsMissing, CodePreBackupForeign,
		CodeNoWorkingBreakGlass,
	}
}

// restoreErrorCode returns the code of the refusal err, or an empty code for
// an error that has none.
func restoreErrorCode(err error) RestoreErrorCode {
	switch {
	case errors.Is(err, ErrPreBackupForeign):
		return CodePreBackupForeign
	case errors.Is(err, ErrLiveBlobsDamaged):
		return CodeLiveBlobsDamaged
	case errors.Is(err, ErrLiveBlobsMissing):
		return CodeLiveBlobsMissing
	case errors.Is(err, ErrBlobsNotBacked):
		return CodeBlobsNotBacked
	case errors.Is(err, principal.ErrNoWorkingBreakGlass):
		return CodeNoWorkingBreakGlass
	default:
		return ""
	}
}

// checkDamagedOrigin refuses a manifest with a damaged list unless a restore
// of this installation started its backup, as the input of the durable
// operation of the backup names it. A manifest with no damaged list passes.
// A backups service that is not there, and a list of operations that cannot
// be read, refuse: fail closed.
func checkDamagedOrigin(ctx context.Context, backups *Service, manifest Manifest) error {
	if len(manifest.Damaged) == 0 {
		return nil
	}

	if backups == nil {
		return fmt.Errorf("backup %s: %w: %w", manifest.ID, ErrDamagedListForeign, ErrCorrupt)
	}

	owner, err := backups.StartedForRestore(ctx, manifest.ID)
	if err != nil {
		return fmt.Errorf("check the origin of the damaged list of backup %s: %w", manifest.ID, err)
	}

	if owner == "" {
		return fmt.Errorf("backup %s: %w: %w", manifest.ID, ErrDamagedListForeign, ErrCorrupt)
	}

	return nil
}

// checkPreBackupFree refuses a pre-backup ID that a caller gives and that
// names a backup that exists: the restore must take a new backup of the live
// data. A read that fails refuses too: fail closed.
func (s *RestoreService) checkPreBackupFree(ctx context.Context, pre ID) error {
	if pre == "" {
		return nil
	}

	if s.backups == nil {
		return fmt.Errorf("pre-backup %s: %w", pre, ErrPreBackupForeign)
	}

	_, err := s.backups.Get(ctx, pre)

	switch {
	case err == nil:
		return fmt.Errorf("pre-backup %s exists: %w", pre, ErrPreBackupForeign)
	case errors.Is(err, ErrNotFound):
		return nil
	default:
		return fmt.Errorf("pre-backup %s: %w: %w", pre, ErrPreBackupForeign, err)
	}
}
