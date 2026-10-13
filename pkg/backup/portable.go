package backup

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
)

// PortableFormat names the portable SQL format in the manifest (ADR 42,
// decision 4). A later format takes a new name.
const PortableFormat = "portable"

// PortableManifestFormat is the manifest format number of the portable format
// (ADR 42, decision 4). The native format keeps its number, 1.
const PortableManifestFormat = 2

// Kind is the portable form of one column value (ADR 42, decision 3). Each
// dialect reads its own storage form and the table writer writes one form.
type Kind uint8

// The portable kinds.
const (
	// KindText is a text value, written as a JSON string.
	KindText Kind = iota + 1
	// KindInteger is an integer value, written as a JSON number.
	KindInteger
	// KindBool is a boolean value, written as true or false. SQLite stores it
	// as an integer, so the reader maps 0 and 1 here.
	KindBool
	// KindTime is a time as an integer of Unix milliseconds. A missing time is
	// null.
	KindTime
	// KindBytes is a byte value, written as a base64 string.
	KindBytes
	// KindJSON is a JSON value, written as the JSON it holds. The import
	// compares it as JSON, not as text (ADR 42, decision 3).
	KindJSON
)

// Column is one column of a portable table: its portable name, which is the
// same in both dialects, and its kind.
type Column struct {
	Name string
	Kind Kind
}

// PortableTable is the portable description of one table: its columns in
// schema order, and the columns that order its rows in the table file.
type PortableTable struct {
	Table   string
	OrderBy []string
	Columns []Column
}

// PortableTables returns the portable description of every table of Tables, in
// the order of Tables. The result is a fresh slice; callers may change it.
func PortableTables() []PortableTable {
	return portableSpec()
}

// PortablePath returns the path of the file of table in a portable backup:
// sql/<table>.jsonl.gz (ADR 42, decision 2).
func PortablePath(table string) string {
	return "sql/" + table + ".jsonl.gz"
}

// PortableFile describes one table file in the manifest (ADR 42, decision 4):
// the columns, the row count, the size and the SHA-256 of the file bytes.
type PortableFile struct {
	Table   string   `json:"table"`
	Path    string   `json:"path"`
	SHA256  string   `json:"sha256"`
	Columns []string `json:"columns"`
	Rows    int64    `json:"rows"`
	Size    int64    `json:"size"`
}

// PortableSink opens the writer of the file at path. The exporter closes the
// writer after the last row of the file. A failed open or write stops the
// export.
type PortableSink func(path string) (io.WriteCloser, error)

// PortableExporter writes the SQL data of a store as one portable file for
// each table of Tables (ADR 42, decisions 2, 6 and 7). It runs the coverage
// check before the first row, reads every table at one point in time, and
// returns the summary and the file descriptors in the order of Tables. The
// caller writes the manifest.
type PortableExporter interface {
	ExportPortable(ctx context.Context, sink PortableSink) (Summary, []PortableFile, error)
}

// TableWriter writes one table file: JSON Lines, one object for each row,
// inside gzip at the best level. The header holds no name and no time, so an
// export of the same rows gives the same bytes (ADR 42, decision 2).
type TableWriter struct {
	table   PortableTable
	gz      *gzip.Writer
	sum     hash.Hash
	counted *countingWriter
	rows    int64
}

// countingWriter counts the bytes that reach the sink.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)

	if err != nil {
		return n, fmt.Errorf("write file bytes: %w", err)
	}

	return n, nil
}

// NewTableWriter returns a writer of the file of table, which writes to w.
func NewTableWriter(w io.Writer, table PortableTable) (*TableWriter, error) {
	sum := sha256.New()
	counted := &countingWriter{w: io.MultiWriter(w, sum), n: 0}

	gz, err := gzip.NewWriterLevel(counted, gzip.BestCompression)
	if err != nil {
		return nil, fmt.Errorf("open gzip of table %q: %w", table.Table, err)
	}

	return &TableWriter{table: table, gz: gz, sum: sum, counted: counted, rows: 0}, nil
}

// WriteRow writes one row. values holds one value for each column of the
// table, in the order of the columns: nil, int64, bool, string or []byte as the
// dialect reads it. A value that does not fit its kind fails with ErrCoverage,
// since the column list then differs from the schema.
func (t *TableWriter) WriteRow(values []any) error {
	if len(values) != len(t.table.Columns) {
		return fmt.Errorf("table %q row has %d values, want %d: %w",
			t.table.Table, len(values), len(t.table.Columns), ErrCoverage)
	}

	obj := make(map[string]any, len(values))

	for i, column := range t.table.Columns {
		value, err := portableValue(column, values[i])
		if err != nil {
			return fmt.Errorf("table %q column %q: %w", t.table.Table, column.Name, err)
		}

		obj[column.Name] = value
	}

	enc := json.NewEncoder(t.gz)
	enc.SetEscapeHTML(false)

	if err := enc.Encode(obj); err != nil {
		return fmt.Errorf("write row of table %q: %w", t.table.Table, err)
	}

	t.rows++

	return nil
}

// Close finishes the gzip stream and returns the descriptor of the file. It
// does not close the sink: the caller does, after Close returns.
func (t *TableWriter) Close() (PortableFile, error) {
	if err := t.gz.Close(); err != nil {
		return PortableFile{}, fmt.Errorf("finish gzip of table %q: %w", t.table.Table, err)
	}

	names := make([]string, 0, len(t.table.Columns))
	for _, column := range t.table.Columns {
		names = append(names, column.Name)
	}

	return PortableFile{
		Table:   t.table.Table,
		Path:    PortablePath(t.table.Table),
		SHA256:  hex.EncodeToString(t.sum.Sum(nil)),
		Columns: names,
		Rows:    t.rows,
		Size:    t.counted.n,
	}, nil
}

// portableValue converts one dialect value to its portable JSON form.
func portableValue(column Column, value any) (any, error) {
	if value == nil {
		return nil, nil //nolint:nilnil // A null value has no JSON form but null.
	}

	switch column.Kind {
	case KindText:
		return textValue(value)
	case KindInteger, KindTime:
		n, ok := value.(int64)
		if !ok {
			return nil, fmt.Errorf("value %T is not an integer: %w", value, ErrCoverage)
		}

		return n, nil
	case KindBool:
		return boolValue(value)
	case KindBytes:
		b, ok := value.([]byte)
		if !ok {
			return nil, fmt.Errorf("value %T is not bytes: %w", value, ErrCoverage)
		}

		// A zero-length value can arrive as a nil slice, which encodes as null.
		// Only a SQL null is null, so an empty value is the empty string.
		if b == nil {
			b = []byte{}
		}

		return b, nil
	case KindJSON:
		return jsonValue(value)
	}

	return nil, fmt.Errorf("column kind %d is unknown: %w", column.Kind, ErrCoverage)
}

func textValue(value any) (string, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	}

	return "", fmt.Errorf("value %T is not text: %w", value, ErrCoverage)
}

func boolValue(value any) (bool, error) {
	switch v := value.(type) {
	case bool:
		return v, nil
	case int64:
		return v != 0, nil
	}

	return false, fmt.Errorf("value %T is not a boolean: %w", value, ErrCoverage)
}

// jsonValue returns the JSON text of a JSON column as raw JSON, so the file
// holds the value and not a string that contains it.
func jsonValue(value any) (json.RawMessage, error) {
	var text []byte

	switch v := value.(type) {
	case string:
		text = []byte(v)
	case []byte:
		text = v
	default:
		return nil, fmt.Errorf("value %T is not JSON text: %w", value, ErrCoverage)
	}

	if !json.Valid(text) {
		return nil, fmt.Errorf("JSON value is not valid JSON: %w", ErrCorrupt)
	}

	return json.RawMessage(text), nil
}

// ErrExportRead reports that the store could not read its rows for a portable
// export. The driver error stays out of the value: an adapter translates it
// into this sentinel (CLAUDE.md, backend rules).
var ErrExportRead = errors.New("backup cannot read the store rows")
