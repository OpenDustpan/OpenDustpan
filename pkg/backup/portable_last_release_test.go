package backup_test

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

// TestReadPortableLastReleaseKind proves the check of the restore alone: a
// portable workspaces table with a kind of the last release that is not
// empty, normal or forced fails with ErrCorrupt, and no row reaches the
// caller. Each valid kind reaches the caller.
func TestReadPortableLastReleaseKind(t *testing.T) {
	t.Parallel()

	cases := []struct {
		kind any
		name string
		want bool
	}{
		{name: "no record", kind: "", want: true},
		{name: "normal", kind: "normal", want: true},
		{name: "forced", kind: "forced", want: true},
		{name: "null", kind: nil, want: false},
		{name: "unknown", kind: "expired", want: false},
		{name: "upper case", kind: "Normal", want: false},
		{name: "trailing space", kind: "forced ", want: false},
		{name: "line feed", kind: "normal\n", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rows, err := readWorkspaceRowWith(t, tc.kind)

			switch {
			case tc.want && (err != nil || rows != 1):
				t.Fatalf("read: rows = %d, err = %v, want 1 row", rows, err)
			case !tc.want && (!errors.Is(err, backup.ErrCorrupt) || rows != 0):
				t.Fatalf("read: rows = %d, err = %v, want ErrCorrupt and no row", rows, err)
			default:
				// The read answered as the case wants.
			}
		})
	}
}

// workspacesTable returns the portable spec of the workspaces table.
func workspacesTable(t *testing.T) backup.PortableTable {
	t.Helper()

	for _, candidate := range backup.PortableTables() {
		if candidate.Table == "workspaces" {
			return candidate
		}
	}

	t.Fatal("portable tables have no workspaces table")

	return backup.PortableTable{Table: "", OrderBy: nil, Columns: nil}
}

// readWorkspaceRowWith writes one workspaces row with the kind of the last
// release kind to a table file and reads the file back. It returns the number
// of rows that reached the caller and the error of the read.
func readWorkspaceRowWith(t *testing.T, kind any) (int, error) {
	t.Helper()

	table := workspacesTable(t)

	var buf bytes.Buffer

	tw, err := backup.NewTableWriter(&buf, table)
	if err != nil {
		t.Fatalf("new table writer: %v", err)
	}

	row := []any{
		"ws-1", "acme", "network", int64(2), "", "", "", int64(0), "", int64(1000), int64(2000),
		false, false, nil, nil, "lock-1", "alice", "alice", kind, int64(1500),
	}
	if len(row) != len(table.Columns) {
		t.Fatalf("row has %d values, the workspaces table has %d columns", len(row), len(table.Columns))
	}

	if writeErr := tw.WriteRow(row); writeErr != nil {
		t.Fatalf("write row: %v", writeErr)
	}

	file, err := tw.Close()
	if err != nil {
		t.Fatalf("close table writer: %v", err)
	}

	open := func(string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(buf.Bytes())), nil
	}

	rows := 0
	err = backup.ReadPortableTable(t.Context(), open, file, table, func([]any) error {
		rows++

		return nil
	})

	return rows, err
}
