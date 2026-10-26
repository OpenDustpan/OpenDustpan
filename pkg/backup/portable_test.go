package backup_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
	"opendustpan.org/opendustpan/pkg/source"
)

// exampleTable has one column of each kind, so the writer's forms are all
// exercised by one row.
func exampleTable() backup.PortableTable {
	return backup.PortableTable{
		Table:   "example",
		OrderBy: []string{"text"},
		Columns: []backup.Column{
			{Name: "text", Kind: backup.KindText},
			{Name: "count", Kind: backup.KindInteger},
			{Name: "enabled", Kind: backup.KindBool},
			{Name: "at_ms", Kind: backup.KindTime},
			{Name: "digest", Kind: backup.KindBytes},
			{Name: "features", Kind: backup.KindJSON},
		},
	}
}

// writeExample writes the rows of rows to one table file and returns the
// decompressed text, the descriptor and the writer's error.
func writeExample(t *testing.T, rows [][]any) (string, backup.PortableFile, error) {
	t.Helper()

	var buf bytes.Buffer

	tw, err := backup.NewTableWriter(&buf, exampleTable())
	if err != nil {
		t.Fatalf("new table writer: %v", err)
	}

	for _, row := range rows {
		if writeErr := tw.WriteRow(row); writeErr != nil {
			return "", backup.PortableFile{}, writeErr
		}
	}

	file, err := tw.Close()
	if err != nil {
		t.Fatalf("close table writer: %v", err)
	}

	zr, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatalf("open gzip: %v", err)
	}

	text, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("read gzip: %v", err)
	}

	return string(text), file, nil
}

// TestTableWriterFormsValues proves each kind has one portable form: a boolean
// is true or false (SQLite stores 0 and 1), a missing time is null, bytes are
// base64, and a JSON value is written as JSON (ADR 42, decision 3).
func TestTableWriterFormsValues(t *testing.T) {
	t.Parallel()

	row := []any{"a<b", int64(7), int64(1), nil, []byte{0x01, 0x02}, []byte(`{"k":[1,2]}`)}

	text, file, err := writeExample(t, [][]any{row})
	if err != nil {
		t.Fatalf("write row: %v", err)
	}

	want := `{"at_ms":null,"count":7,"digest":"AQI=","enabled":true,"features":{"k":[1,2]},"text":"a<b"}` + "\n"
	if text != want {
		t.Fatalf("table text = %q, want %q", text, want)
	}

	if file.Rows != 1 || file.Path != "sql/example.jsonl.gz" {
		t.Fatalf("descriptor = %+v, want one row at sql/example.jsonl.gz", file)
	}

	if !slices.Equal(file.Columns, []string{"text", "count", "enabled", "at_ms", "digest", "features"}) {
		t.Fatalf("descriptor columns = %v", file.Columns)
	}
}

// TestTableWriterIsDeterministic proves that the same rows give the same bytes,
// and that the descriptor holds the size and the SHA-256 of the file (ADR 42,
// decision 2).
func TestTableWriterIsDeterministic(t *testing.T) {
	t.Parallel()

	rows := [][]any{{"b", int64(2), false, int64(9), []byte("x"), "[]"}, {"a", int64(1), true, int64(3), nil, "{}"}}

	first, firstFile, err := writeExample(t, rows)
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	second, secondFile, err := writeExample(t, rows)
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	if first != second || firstFile.SHA256 != secondFile.SHA256 || firstFile.Size != secondFile.Size {
		t.Fatalf("two writes differ: %+v and %+v", firstFile, secondFile)
	}

	if firstFile.Size <= 0 || len(firstFile.SHA256) != 64 {
		t.Fatalf(
			"descriptor size %d and sha256 %q, want a size and a 64-digit digest",
			firstFile.Size,
			firstFile.SHA256,
		)
	}
}

// TestTableWriterRefusesWhatDoesNotFitItsKind proves that a value that does
// not fit its kind, a row of the wrong width, and invalid JSON fail (ADR 42,
// decision 5).
func TestTableWriterRefusesWhatDoesNotFitItsKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		row  []any
		want error
	}{
		{name: "text as an integer", row: []any{int64(1), int64(1), true, nil, nil, "{}"}, want: backup.ErrCoverage},
		{name: "width", row: []any{"a"}, want: backup.ErrCoverage},
		{name: "bytes as text", row: []any{"a", int64(1), true, nil, "x", "{}"}, want: backup.ErrCoverage},
		{name: "invalid JSON", row: []any{"a", int64(1), true, nil, nil, "{"}, want: backup.ErrCorrupt},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := writeExample(t, [][]any{tt.row})
			if !errors.Is(err, tt.want) {
				t.Fatalf("write %s = %v, want %v", tt.name, err, tt.want)
			}
		})
	}
}

// TestWritePortableWritesEveryTableInOrder proves the export writes one file
// for each table of Tables, in order, and closes each file it opens (ADR 42,
// decision 2).
func TestWritePortableWritesEveryTableInOrder(t *testing.T) {
	t.Parallel()

	paths := make([]string, 0, len(backup.Tables))

	closed := 0

	sink := func(path string) (io.WriteCloser, error) {
		paths = append(paths, path)

		return &closeCounter{Writer: io.Discard, closed: &closed}, nil
	}

	read := func(_ context.Context, _ backup.PortableTable, _ func([]any) error) error {
		return nil
	}

	files, counts, err := backup.WritePortable(t.Context(), sink, read)
	if err != nil {
		t.Fatalf("write portable: %v", err)
	}

	if len(files) != len(backup.Tables) || len(counts) != len(backup.Tables) {
		t.Fatalf("files = %d and counts = %d, want %d", len(files), len(counts), len(backup.Tables))
	}

	for i, table := range backup.Tables {
		if paths[i] != backup.PortablePath(table) || files[i].Table != table {
			t.Fatalf("file %d = %q, want %q", i, paths[i], backup.PortablePath(table))
		}
	}

	if closed != len(backup.Tables) {
		t.Fatalf("closed files = %d, want %d", closed, len(backup.Tables))
	}
}

// closeCounter counts the files that the export closes.
type closeCounter struct {
	io.Writer

	closed *int
}

func (c *closeCounter) Close() error {
	*c.closed++

	return nil
}

// TestPortableSpecFollowsTables proves the portable spec lists every table of
// Tables in its order, and that each ordering column is a column of its table.
func TestPortableSpecFollowsTables(t *testing.T) {
	t.Parallel()

	specs := backup.PortableTables()

	if len(specs) != len(backup.Tables) {
		t.Fatalf("portable tables = %d, want %d", len(specs), len(backup.Tables))
	}

	for i, spec := range specs {
		if spec.Table != backup.Tables[i] {
			t.Fatalf("table %d = %q, want %q", i, spec.Table, backup.Tables[i])
		}

		names := make([]string, 0, len(spec.Columns))
		for _, column := range spec.Columns {
			names = append(names, column.Name)
		}

		for _, key := range spec.OrderBy {
			if !slices.Contains(names, key) {
				t.Fatalf("table %q orders by %q, which is not a column", spec.Table, key)
			}
		}
	}
}

// TestPortableManifestFormat proves the manifest of a portable backup names
// format 3 and the portable SQL format, and lists each table file (ADR 42,
// decision 4).
func TestPortableManifestFormat(t *testing.T) {
	t.Parallel()

	manifest := backup.Manifest{
		CreatedAt:     time.Time{},
		ID:            "bkp-1",
		Dialect:       backup.DialectSQLite,
		HomeRegions:   []string{"eu-west"},
		WrapperIDs:    []string{"kek-1"},
		Blobs:         nil,
		Version:       "test",
		SQLSHA256:     "00",
		Format:        backup.FormatVersion,
		SchemaVersion: 26,
		SQLSize:       9,
		RowCounts:     map[string]int64{"namespaces": 1},
		SQLFormat:     backup.PortableFormat,
		SQLFiles: []backup.PortableFile{
			{
				Table:   "namespaces",
				Path:    "sql/namespaces.jsonl.gz",
				SHA256:  "00",
				Columns: []string{"name"},
				Rows:    1,
				Size:    9,
			},
		},
		InstallationKind: installation.KindProduction,
		Unverified:       nil,
	}

	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	text := string(raw)
	for _, want := range []string{`"format":3`, `"installationKind":"production"`, `"sqlFormat":"portable"`, `"path":"sql/namespaces.jsonl.gz"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("manifest %s does not contain %s", text, want)
		}
	}
}

// namespacesTable returns the portable namespaces table.
func namespacesTable(t *testing.T) backup.PortableTable {
	t.Helper()

	for _, candidate := range backup.PortableTables() {
		if candidate.Table == "namespaces" {
			return candidate
		}
	}

	t.Fatal("portable tables have no namespaces table")

	return backup.PortableTable{Table: "", OrderBy: nil, Columns: nil}
}

// readNamespaceRow writes one namespaces row with the id value id to a table
// file and reads the file back. It returns the number of rows that reached
// the caller and the error of the read.
func readNamespaceRow(t *testing.T, id any) (int, error) {
	t.Helper()

	return readNamespaceRowWith(t, id, "nobody")
}

// readNamespaceRowWith writes one namespaces row with the id value id and the
// value setting of the output sharing setting to a table file and reads the file back. It
// returns the number of rows that reached the caller and the error of the
// read.
func readNamespaceRowWith(t *testing.T, id, setting any) (int, error) {
	t.Helper()

	return readNamespaceRowFilter(t, id, setting, "")
}

// readNamespaceRowFilter writes one namespaces row with the id value id, the
// output sharing value setting and the source filter value filter to a
// table file and reads the file back. It returns the number of rows that
// reached the caller and the error of the read.
func readNamespaceRowFilter(t *testing.T, id, setting, filter any) (int, error) {
	t.Helper()

	table := namespacesTable(t)

	var buf bytes.Buffer

	tw, err := backup.NewTableWriter(&buf, table)
	if err != nil {
		t.Fatalf("new table writer: %v", err)
	}

	row := []any{"acme", "eu-west", int64(1), int64(1000), false, false, "", id, setting, filter}
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

// TestReadPortableNamespaceIDs proves that a portable namespaces table with
// an ID that ParseID refuses fails with ErrCorrupt and ErrInvalidID, and that
// a valid ID reaches the caller.
func TestReadPortableNamespaceIDs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		id   any
		want bool
	}{
		{name: "valid", id: "ns-abcdefghij234567klmn", want: true},
		{name: "empty", id: "", want: false},
		{name: "no prefix", id: "acme", want: false},
		{name: "prefix only", id: "ns-", want: false},
		{name: "bad character", id: "ns-a/b", want: false},
		{name: "too long", id: "ns-" + strings.Repeat("a", 98), want: false},
		{name: "null", id: nil, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rows, err := readNamespaceRow(t, tc.id)
			if tc.want && (err != nil || rows != 1) {
				t.Fatalf("read: rows = %d, err = %v, want 1 row", rows, err)
			}

			if !tc.want && (!errors.Is(err, backup.ErrCorrupt) || !errors.Is(err, namespace.ErrInvalidID)) {
				t.Fatalf("read: err = %v, want ErrCorrupt and ErrInvalidID", err)
			}
		})
	}
}

// TestReadPortableOutputSharingDefault proves the check of the restore alone:
// a portable namespaces table with an output sharing setting that is not a
// value of the setting fails with ErrCorrupt and outputshare.ErrInvalid, and
// each value of the setting reaches the caller.
func TestReadPortableOutputSharingDefault(t *testing.T) {
	t.Parallel()

	const id = "ns-abcdefghij234567klmn"

	cases := []struct {
		setting any
		name    string
		want    bool
	}{
		{name: "nobody", setting: "nobody", want: true},
		{name: "namespace", setting: "namespace", want: true},
		{name: "empty", setting: "", want: false},
		{name: "null", setting: nil, want: false},
		{name: "unknown", setting: "everyone", want: false},
		{name: "upper case", setting: "Nobody", want: false},
		{name: "trailing space", setting: "namespace ", want: false},
		{name: "line feed", setting: "nobody\n", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rows, err := readNamespaceRowWith(t, id, tc.setting)
			if tc.want {
				checkSettingKept(t, rows, err)
			} else {
				checkSettingRefused(t, rows, err)
			}
		})
	}
}

// checkSettingKept fails the test unless the read of one namespaces row gave
// that row with no error.
func checkSettingKept(t *testing.T, rows int, err error) {
	t.Helper()

	if err != nil || rows != 1 {
		t.Fatalf("read: rows = %d, err = %v, want 1 row", rows, err)
	}
}

// checkSettingRefused fails the test unless the read of one namespaces row
// gave no row and an error that wraps ErrCorrupt and outputshare.ErrInvalid.
func checkSettingRefused(t *testing.T, rows int, err error) {
	t.Helper()

	if !errors.Is(err, backup.ErrCorrupt) || !errors.Is(err, outputshare.ErrInvalid) {
		t.Fatalf("read: err = %v, want ErrCorrupt and outputshare.ErrInvalid", err)
	}

	if rows != 0 {
		t.Fatalf("read: %d rows reached the caller, want none", rows)
	}
}

// TestReadPortableSourceFilter proves the check of the restore alone: a
// portable namespaces table with a source filter that is not the empty text or
// a filter document that passes the checks of a filter fails with ErrCorrupt
// and source.ErrInvalid, and does not hold the value. A valid value reaches
// the caller.
func TestReadPortableSourceFilter(t *testing.T) {
	t.Parallel()

	const id = "ns-abcdefghij234567klmn"

	cases := []struct {
		filter any
		name   string
		want   bool
	}{
		{name: "empty", filter: "", want: true},
		{name: "empty document", filter: "{}", want: true},
		{
			name:   "full document",
			filter: `{"submodules":["https://git.example.com/a/*"],"failOnDenied":true,"protectedBranches":["main"]}`,
			want:   true,
		},
		{name: "null value", filter: nil, want: false},
		{name: "not JSON", filter: "canary not json", want: false},
		{name: "unknown key", filter: `{"canary":1}`, want: false},
		{name: "bad pattern", filter: `{"submodules":["http://canary.example.com/a"]}`, want: false},
		{name: "glob branch", filter: `{"protectedBranches":["canary/*"]}`, want: false},
		{name: "second document", filter: `{} {}`, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rows, err := readNamespaceRowFilter(t, id, "nobody", tc.filter)
			if tc.want {
				checkSettingKept(t, rows, err)

				return
			}

			if !errors.Is(err, backup.ErrCorrupt) || !errors.Is(err, source.ErrInvalid) {
				t.Fatalf("read: err = %v, want ErrCorrupt and source.ErrInvalid", err)
			}

			if strings.Contains(err.Error(), "canary") {
				t.Fatalf("the error holds the value of the filter: %v", err)
			}

			if rows != 0 {
				t.Fatalf("read: %d rows reached the caller, want none", rows)
			}
		})
	}
}
