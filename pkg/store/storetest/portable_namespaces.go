package storetest

import (
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

// PortableNamespaceRows exports s in the portable format, stages the export
// back, discards the staging area, and returns the rows of the table
// namespaces that the export holds, by namespace name. Each row maps a column
// to its value as the JSON of the export gives it. It fails the test when the
// export or the stage fails. A test of a column of the namespace row uses it to
// prove that the value reaches the export and that the stage accepts it.
func PortableNamespaceRows(t *testing.T, s PortableStore) map[string]map[string]any {
	t.Helper()

	sink, sum, files := exportPortable(t, s)

	staged, err := s.StagePortable(t.Context(), sink.read, sum, files, StageAs(t, s))
	if err != nil {
		t.Fatalf("stage portable backup: %v", err)
	}

	if staged.RowCounts["namespaces"] != sum.RowCounts["namespaces"] {
		t.Fatalf("staged rows of namespaces = %d, want %d", staged.RowCounts["namespaces"], sum.RowCounts["namespaces"])
	}

	if err = s.DiscardStaging(t.Context()); err != nil {
		t.Fatalf("discard staging: %v", err)
	}

	path := backup.PortablePath("namespaces")
	rows := map[string]map[string]any{}

	for _, row := range readPortableLines(t, path, sink.files[path].Bytes()) {
		name, isText := row["name"].(string)
		if !isText {
			t.Fatalf("a namespaces row has the name %v (%T), want text", row["name"], row["name"])
		}

		rows[name] = row
	}

	return rows
}

// StagePortableExport exports s in the portable format, stages the export back
// and returns the error of the stage, which is nil when the stage accepts the
// export. It discards the staging area after a stage that succeeds. It fails
// the test when the export fails.
func StagePortableExport(t *testing.T, s PortableStore) error {
	t.Helper()

	sink, sum, files := exportPortable(t, s)

	if _, err := s.StagePortable(t.Context(), sink.read, sum, files, StageAs(t, s)); err != nil {
		return fmt.Errorf("stage portable backup: %w", err)
	}

	if err := s.DiscardStaging(t.Context()); err != nil {
		t.Fatalf("discard staging: %v", err)
	}

	return nil
}
