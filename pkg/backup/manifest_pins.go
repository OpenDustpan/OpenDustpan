package backup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// checkManifestPins refuses a manifest whose totals do not match its table
// file descriptors: the SQL size, the SQL checksum (the SHA-256 of the JSON of
// the descriptors), and the rows of each table file, which must equal the row
// count of its table. A table with no row count has no rows. Each row count
// must name a table file. The refusal wraps ErrCorrupt. Backup check and
// every restore path read the manifest through parseManifest, so they all
// refuse such a manifest before they open an object.
func checkManifestPins(manifest Manifest) error {
	sum, err := hex.DecodeString(manifest.SQLSHA256)
	if err != nil || len(sum) != sha256.Size {
		return fmt.Errorf("the SQL checksum is not valid: %w", ErrCorrupt)
	}

	size, sha, err := sqlAggregate(manifest.SQLFiles)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrCorrupt, err)
	}

	if size != manifest.SQLSize {
		return fmt.Errorf("the SQL size is %d, its table files hold %d: %w", manifest.SQLSize, size, ErrCorrupt)
	}

	if !bytes.Equal(sha[:], sum) {
		return fmt.Errorf("the SQL checksum does not match its table files: %w", ErrCorrupt)
	}

	rows := make(map[string]int64, len(manifest.SQLFiles))
	for _, file := range manifest.SQLFiles {
		rows[file.Table] = file.Rows

		// A table with no row count has no rows, as in the staged summary.
		if count := manifest.RowCounts[file.Table]; count != file.Rows {
			return fmt.Errorf("the row count of table %q is %d, its table file holds %d rows: %w",
				file.Table, count, file.Rows, ErrCorrupt)
		}
	}

	for table := range manifest.RowCounts {
		if _, ok := rows[table]; !ok {
			return fmt.Errorf("the row counts name table %q, which has no table file: %w", table, ErrCorrupt)
		}
	}

	return nil
}
