package backup_test

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

// readPortableRow writes one row of the portable table name to a table file and
// reads the file back. It returns the error of the read. The test sends the
// row to the import check as if no other check had run.
func readPortableRow(t *testing.T, name string, row []any) error {
	t.Helper()

	var table backup.PortableTable

	for _, candidate := range backup.PortableTables() {
		if candidate.Table == name {
			table = candidate
		}
	}

	if table.Table == "" {
		t.Fatalf("portable tables have no %s table", name)
	}

	var buf bytes.Buffer

	tw, err := backup.NewTableWriter(&buf, table)
	if err != nil {
		t.Fatalf("new table writer: %v", err)
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

	return backup.ReadPortableTable(t.Context(), open, file, table, func([]any) error { return nil })
}

// TestPortableUnitSettingsRowsAreChecked sends hostile rows straight to the
// import check of the unit settings tables.
func TestPortableUnitSettingsRowsAreChecked(t *testing.T) {
	t.Parallel()

	const nsID = "ns-aaaaaaaaaaaaaaaaaaaa"

	settings := func(body any, version int64) []any {
		return []any{"stk-1", nsID, version, body, int64(1000)}
	}
	defaults := func(pool any, version int64) []any {
		return []any{nsID, pool, version, int64(1000)}
	}

	cases := []struct {
		name  string
		table string
		row   []any
		want  bool
	}{
		{
			"settings that pass",
			"unit_settings",
			settings(`{"codeLocation":{"repository":"acme/infra","ref":"main"}}`, 1),
			false,
		},
		{"empty settings", "unit_settings", settings(`{}`, 1), false},
		{"unknown executor", "unit_settings", settings(`{"executor":"shell"}`, 1), true},
		{
			"directory that leaves the repository",
			"unit_settings",
			settings(`{"codeLocation":{"directory":"../x"}}`, 1),
			true,
		},
		{"ref with a leading dash", "unit_settings", settings(`{"codeLocation":{"ref":"--x"}}`, 1), true},
		{"body that is not JSON", "unit_settings", settings(`nope`, 1), true},
		{"body with an unknown field", "unit_settings", settings(`{"extra":1}`, 1), true},
		{"no body", "unit_settings", settings(nil, 1), true},
		{"version below one", "unit_settings", settings(`{}`, 0), true},
		{"defaults that pass", "unit_defaults", defaults("rp-0123456789abcdef", 1), false},
		{"defaults with no pool", "unit_defaults", defaults("", 1), false},
		{"pool that is not a pool ID", "unit_defaults", defaults("pool; drop", 1), true},
		{"no pool", "unit_defaults", defaults(nil, 1), true},
		{"defaults at version below one", "unit_defaults", defaults("", 0), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := readPortableRow(t, tc.table, tc.row)
			if tc.want != errors.Is(err, backup.ErrCorrupt) || (!tc.want && err != nil) {
				t.Fatalf("read = %v, want corrupt = %v", err, tc.want)
			}
		})
	}
}
