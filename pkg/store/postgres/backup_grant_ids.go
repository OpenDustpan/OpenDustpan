package postgres

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/backup"
)

// checkStagedGrantIDs refuses a staged backup with a grant whose ID is not a
// grant ID (access.ValidGrantID). It returns backup.ErrCorrupt. The error
// text holds no part of the ID.
//
// Defense in depth: the CHECK constraint of the grants table refuses such a
// row when the restore copies it, and the service and the store refuse to
// read or end a grant by such an ID. This check stays so that a crafted
// backup fails before the restore changes the live database, and it does not
// rely on the constraint.
func checkStagedGrantIDs(ctx context.Context, pool pgQuerier, staging string) error {
	rows, err := pool.Query(ctx, "SELECT id FROM "+stagingTable(staging, backup.TableGrants))
	if err != nil {
		return fmt.Errorf("read staged grant IDs: %w", backup.ErrExportRead)
	}

	defer rows.Close()

	for rows.Next() {
		var id string

		if scanErr := rows.Scan(&id); scanErr != nil {
			return fmt.Errorf("read staged grant IDs: %w", backup.ErrExportRead)
		}

		if !access.ValidGrantID(id) {
			return fmt.Errorf("staged grant has an ID that is not a grant ID: %w", backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged grant IDs: %w", backup.ErrExportRead)
	}

	return nil
}
