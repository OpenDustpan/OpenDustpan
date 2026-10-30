package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/backup"
)

// dropStagedIssuerKeys removes every signing key of the OIDC issuers from the
// staging schema, and the copy check of each issuer. The backup of another
// installation restores no key: two installations never sign with one key.
// The live data is untouched.
func (s *Store) dropStagedIssuerKeys(ctx context.Context, staging string) error {
	if !hasStagingPrefix(staging) {
		return fmt.Errorf("drop issuer keys in staging schema %q: %w", staging, backup.ErrCorrupt)
	}

	return inBackupTx(ctx, s.pool, readWriteOpts(), func(tx pgx.Tx) error {
		if _, err := setSearchPath(ctx, tx, staging); err != nil {
			return err
		}

		for _, stmt := range []string{
			`DELETE FROM issuer_keys`,
			`UPDATE issuer_settings SET copy_check = ''`,
		} {
			if _, err := tx.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("drop the issuer keys of the staging schema: %w", backup.ErrExportRead)
			}
		}

		return nil
	})
}
