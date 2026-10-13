package backup_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
)

// TestNativeFormatManifestIsRefused proves that a backup whose manifest is in
// the native format of ADR 30 fails the check with ErrNativeFormat and names a
// new backup as the way out, before any object is read (ADR 42, decision 4).
func TestNativeFormatManifestIsRefused(t *testing.T) {
	t.Parallel()

	const id = backup.ID("bkp-native")

	target := newMemStore()

	manifest := backup.Manifest{
		CreatedAt:     time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC),
		ID:            id,
		Dialect:       backup.DialectSQLite,
		HomeRegions:   nil,
		WrapperIDs:    nil,
		Blobs:         nil,
		Version:       "test-1.0",
		SQLSHA256:     "00",
		Format:        1,
		SchemaVersion: 10,
		SQLSize:       1,
		RowCounts:     map[string]int64{},
		SQLFormat:     "",
		SQLFiles:      nil,
	}

	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}

	target.storeObject(t, "backups/"+id.String()+"/manifest.json", data)

	err = backup.Check(t.Context(), target, id)
	if err == nil {
		t.Fatal("check a native-format backup: err = nil, want ErrNativeFormat")
	}

	if !errors.Is(err, backup.ErrNativeFormat) {
		t.Fatalf("check a native-format backup: err = %v, want ErrNativeFormat", err)
	}

	if !strings.Contains(err.Error(), "a new backup is necessary") {
		t.Fatalf("native-format error = %q, want it to say a new backup is necessary", err)
	}
}

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
