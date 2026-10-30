package sqlite

import (
	"context"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/backup"
)

// dropStagedIssuerKeys removes every signing key of the OIDC issuers from the
// staged file at path, and the copy check of each issuer. The backup of
// another installation restores no key: two installations never sign with
// one key. The live data is untouched.
func dropStagedIssuerKeys(ctx context.Context, path string) error {
	conn, err := openStagedAccess(ctx, path)
	if err != nil {
		return err
	}

	defer func() {
		_ = conn.Close() //nolint:errcheck // Best-effort close of the staging handle.
	}()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin the drop of the issuer keys: %w", backup.ErrExportRead)
	}

	for _, stmt := range []string{
		`DELETE FROM issuer_keys`,
		`UPDATE issuer_settings SET copy_check = ''`,
	} {
		if _, execErr := tx.ExecContext(ctx, stmt); execErr != nil {
			return errors.Join(fmt.Errorf("drop the issuer keys of the staged file: %w", backup.ErrExportRead), tx.Rollback())
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit the drop of the issuer keys: %w", backup.ErrExportRead)
	}

	return nil
}
