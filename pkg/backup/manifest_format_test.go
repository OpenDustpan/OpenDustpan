package backup_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

// TestIndexPortableFilesRefusesForeignPath proves the shared check refuses a
// descriptor whose path is not the path of its table, and names the table
// (ADR 42, decision 4). Both dialects and the manifest read run this check.
func TestIndexPortableFilesRefusesForeignPath(t *testing.T) {
	t.Parallel()

	tables := backup.PortableTables()
	files := make([]backup.PortableFile, 0, len(tables))
	rows := make(map[string]int64, len(tables))

	for _, table := range tables {
		path := backup.PortablePath(table.Table)
		if table.Table == "secret_keys" {
			path = "manifest.json"
		}

		files = append(files, backup.PortableFile{
			Table:   table.Table,
			Path:    path,
			SHA256:  "",
			Columns: nil,
			Rows:    0,
			Size:    0,
		})
		rows[table.Table] = 0
	}

	_, err := backup.IndexPortableFiles(files, rows)
	if err == nil {
		t.Fatal("index a descriptor with a foreign path: err = nil, want ErrCorrupt")
	}

	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("index a descriptor with a foreign path: err = %v, want ErrCorrupt", err)
	}

	if !strings.Contains(err.Error(), `"secret_keys"`) {
		t.Fatalf("foreign path error = %q, want it to name the table secret_keys", err)
	}
}
