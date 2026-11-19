package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/stack"
)

// checkStagedStackTags checks the value of each staged tag row with the rule of
// a new tag value (stack.CheckTagValues): at most 256 bytes of valid UTF-8,
// with no hidden character other than a line break or a tab. A row that fails
// makes the backup corrupt, so a restore never brings in a value that the
// server would not have stored. It returns backup.ErrCorrupt. The error names
// the stack ID and never holds the value.
//
// Defense in depth: the handler, the service and the stores check a tag value
// when a person writes it. This check stays so that a changed backup cannot
// bring in a value that one of those layers refuses; it does not rely on any of
// them.
func checkStagedStackTags(ctx context.Context, conn *sql.DB) error {
	rows, err := conn.QueryContext(ctx, `SELECT stack_id, name, value FROM stack_tags`)
	if err != nil {
		return fmt.Errorf("read staged stack tags: %w", backup.ErrExportRead)
	}

	defer func() {
		_ = rows.Close() //nolint:errcheck // Read-only check handle.
	}()

	for rows.Next() {
		var stackID, name, value string

		if scanErr := rows.Scan(&stackID, &name, &value); scanErr != nil {
			return fmt.Errorf("read staged stack tags: %w", backup.ErrExportRead)
		}

		if valueErr := stack.CheckTagValues(map[string]string{name: value}); valueErr != nil {
			return fmt.Errorf("staged stack %q has a tag value that is not valid: %w: %w", stackID, valueErr,
				backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged stack tags: %w", backup.ErrExportRead)
	}

	return nil
}
