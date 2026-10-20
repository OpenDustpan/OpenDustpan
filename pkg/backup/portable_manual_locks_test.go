package backup_test

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
)

// readManualLockRow writes one manual_locks row with the reason value reason
// to a table file and reads the file back. It returns the number of rows that
// reached the caller and the error of the read. The test sends the row to the
// import check as if no other check had run.
func readManualLockRow(t *testing.T, reason any) (int, error) {
	t.Helper()

	var table backup.PortableTable

	for _, candidate := range backup.PortableTables() {
		if candidate.Table == "manual_locks" {
			table = candidate
		}
	}

	if table.Table == "" {
		t.Fatal("portable tables have no manual_locks table")
	}

	var buf bytes.Buffer

	tw, err := backup.NewTableWriter(&buf, table)
	if err != nil {
		t.Fatalf("new table writer: %v", err)
	}

	row := []any{"unit", "u-1", "acme", "alice", reason, int64(1000)}
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

// TestReadPortableManualLockReasons proves that a restored manual lock row
// with a reason that the lock service would refuse fails with ErrCorrupt, and
// that a valid reason reaches the caller.
func TestReadPortableManualLockReasons(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		reason any
		valid  bool
	}{
		{name: "plain reason", reason: "freeze for the audit", valid: true},
		{name: "unicode letters", reason: "gel pour la revue été", valid: true},
		{name: "empty", reason: "", valid: false},
		{name: "blanks only", reason: "  ", valid: false},
		{name: "null", reason: nil, valid: false},
		{name: "escape sequence", reason: "ok\x1b[2J", valid: false},
		{name: "carriage return", reason: "ok\rforged line", valid: false},
		{name: "line feed", reason: "ok\nforged line", valid: false},
		{name: "bidi override", reason: "ok\u202egnirts", valid: false},
		{name: "zero width joiner", reason: "ok\u200d", valid: false},
		{name: "too long", reason: strings.Repeat("a", audit.MaxReasonLen+1), valid: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rows, err := readManualLockRow(t, tc.reason)
			if tc.valid {
				if err != nil || rows != 1 {
					t.Fatalf("read: rows = %d, err = %v; want 1 row and no error", rows, err)
				}

				return
			}

			if !errors.Is(err, backup.ErrCorrupt) || rows != 0 {
				t.Fatalf("read: rows = %d, err = %v; want ErrCorrupt and no row", rows, err)
			}
		})
	}
}
