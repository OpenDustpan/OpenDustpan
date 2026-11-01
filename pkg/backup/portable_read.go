package backup

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
	"opendustpan.org/opendustpan/pkg/source"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// PortableOpener opens the file at path of a portable backup for reading. The
// caller closes the reader.
type PortableOpener func(path string) (io.ReadCloser, error)

// PortableStager stages a portable backup into the staging area of the store,
// across dialects (ADR 42, decisions 8 and 9). It reads the table files in
// foreign-key order, in batches, and checks the staged data as Stager does.
// A failed stage leaves the live data unchanged and no staging area.
type PortableStager interface {
	// StagePortable loads the table files named by files into the staging
	// area, reading each file with open. It checks each file against its
	// descriptor, then the summary against want. It returns ErrCorrupt for a
	// file that does not match its descriptor, ErrSchemaMismatch for another
	// schema version, and the check errors of Stager.
	StagePortable(
		ctx context.Context,
		open PortableOpener,
		want Summary,
		files []PortableFile,
		opts StageOptions,
	) (Summary, error)
}

// ReadPortableTable reads the rows of one table file and calls emit for each
// row, with one value for each column of table in order: nil for null, int64,
// bool, string (text and JSON text) or []byte. It checks the descriptor: the
// columns, the row count, the size and the SHA-256 of the file bytes. Any
// mismatch returns ErrCorrupt. The rows reach emit before the digest check
// runs, so the caller discards what it loaded when this returns an error.
func ReadPortableTable(
	ctx context.Context, open PortableOpener, file PortableFile, table PortableTable, emit func(values []any) error,
) error {
	if file.Table != table.Table || !slices.Equal(file.Columns, portableNames(table)) {
		return fmt.Errorf("table file %q does not match table %q: %w", file.Path, table.Table, ErrCorrupt)
	}

	src, err := open(file.Path)
	if err != nil {
		return fmt.Errorf("open table file %q: %w", file.Path, err)
	}

	defer func() {
		if closeErr := src.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	digest := sha256.New()
	counted := &countingReader{r: io.TeeReader(src, digest), n: 0}

	zr, err := gzip.NewReader(counted)
	if err != nil {
		return fmt.Errorf("open gzip of table file %q: %w", file.Path, ErrCorrupt)
	}

	rows, err := decodePortableRows(ctx, zr, table, emit)
	if err != nil {
		return err
	}

	if _, err := io.Copy(io.Discard, counted); err != nil {
		return fmt.Errorf("read table file %q: %w", file.Path, ErrCorrupt)
	}

	if rows != file.Rows {
		return fmt.Errorf("table file %q holds %d rows, descriptor says %d: %w", file.Path, rows, file.Rows, ErrCorrupt)
	}

	if counted.n != file.Size || hex.EncodeToString(digest.Sum(nil)) != file.SHA256 {
		return fmt.Errorf("table file %q does not match its descriptor: %w", file.Path, ErrCorrupt)
	}

	return nil
}

// decodePortableRows decodes the JSON objects of one table file and emits each
// one as values in column order. It returns the number of rows.
func decodePortableRows(
	ctx context.Context, r io.Reader, table PortableTable, emit func(values []any) error,
) (int64, error) {
	dec := json.NewDecoder(r)

	var rows int64

	for {
		if err := ctx.Err(); err != nil {
			return rows, fmt.Errorf("read table %q: %w", table.Table, err)
		}

		var obj map[string]json.RawMessage
		if err := dec.Decode(&obj); err != nil {
			if errors.Is(err, io.EOF) {
				return rows, nil
			}

			return rows, fmt.Errorf("decode a row of table %q: %w", table.Table, ErrCorrupt)
		}

		values, err := portableRowValues(table, obj)
		if err != nil {
			return rows, err
		}

		if emitErr := emit(values); emitErr != nil {
			return rows, emitErr
		}

		rows++
	}
}

// portableRowValues returns the values of one decoded row in column order. A
// row with a missing or an extra member fails with ErrCorrupt.
func portableRowValues(table PortableTable, obj map[string]json.RawMessage) ([]any, error) {
	if len(obj) != len(table.Columns) {
		return nil, fmt.Errorf("row of table %q has %d members, want %d: %w",
			table.Table, len(obj), len(table.Columns), ErrCorrupt)
	}

	values := make([]any, len(table.Columns))

	for i, column := range table.Columns {
		raw, ok := obj[column.Name]
		if !ok {
			return nil, fmt.Errorf("row of table %q misses column %q: %w", table.Table, column.Name, ErrCorrupt)
		}

		value, err := portableValueOf(column, raw)
		if err != nil {
			return nil, fmt.Errorf("table %q column %q: %w", table.Table, column.Name, err)
		}

		if checkErr := checkPortableValue(table.Table, column.Name, value); checkErr != nil {
			return nil, checkErr
		}

		values[i] = value
	}

	return values, nil
}

// checkManualLockReason refuses a manual lock row whose reason a person could
// not have set: empty, longer than the limit, or with a control or format
// character. A lock tool shows the reason in a terminal, so a restore must not
// bring such a value in. It returns ErrCorrupt, wrapped with
// audit.ErrInvalidReason.
//
// Defense in depth: the lock service validates a new reason too, and the lock
// text escapes the reason when it prints it. This check stays so that a
// restored backup cannot bring in a reason that skips both.
func checkManualLockReason(value any) error {
	reason, isText := value.(string)
	if !isText || strings.TrimSpace(reason) == "" {
		return fmt.Errorf("manual lock row has no reason: %w", ErrCorrupt)
	}

	if err := audit.ValidateReason(reason); err != nil {
		return fmt.Errorf("manual lock row has an invalid reason: %w: %w", err, ErrCorrupt)
	}

	return nil
}

// checkPortableValue runs the check of the column that has one: the ID, the
// output sharing setting and the source filter of a namespace, the reason of a manual lock,
// the document of the preferences of a person, the user name and the password
// hash of a local user, the kind of the last release of a workspace and the
// unit settings.
func checkPortableValue(table, column string, value any) error {
	switch {
	case table == portTableNamespaces && column == "id":
		return checkNamespaceID(value)
	case table == portTableNamespaces && column == portColOutputSharingDefault:
		return checkOutputSharingDefault(value)
	case table == portTableNamespaces && column == portColSourceFilter:
		return checkSourceFilter(value)
	case table == portTableManualLocks && column == portColReason:
		return checkManualLockReason(value)
	case table == TablePersonPreferences && column == portColDocument:
		return checkPreferencesDocument(value)
	default:
		return checkOtherValue(table, column, value)
	}
}

// checkOtherValue runs the checks of the local users, of the kind of the last
// release of a workspace and of the unit settings, so that checkPortableValue
// stays short.
func checkOtherValue(table, column string, value any) error {
	if table == TableLocalUsers {
		return checkLocalUserValue(column, value)
	}

	if table == portTableWorkspaces && column == portColLastReleaseKind {
		return checkLastReleaseKind(value)
	}

	return checkUnitSettingsValue(table, column, value)
}

// checkNamespaceID refuses a namespace row whose id value is not a valid
// namespace ID. It returns ErrCorrupt, wrapped with namespace.ErrInvalidID.
func checkNamespaceID(value any) error {
	id, isText := value.(string)
	if !isText {
		id = ""
	}

	if _, err := namespace.ParseID(id); err != nil {
		return fmt.Errorf("namespace row has an invalid ID: %w: %w", err, ErrCorrupt)
	}

	return nil
}

// portTableWorkspaces is the table of the workspaces.
const portTableWorkspaces = "workspaces"

// portColLastReleaseKind is the column of the kind of the last released lock
// of a workspace.
const portColLastReleaseKind = "last_release_kind"

// checkLastReleaseKind refuses a workspace row whose kind of the last release
// is not empty, normal or forced. It returns ErrCorrupt.
//
// Defense in depth: the CHECK of the column refuses such a value at the copy,
// and the store reads such a value as no record. This check stays so that a
// restored backup fails before it reaches the database.
func checkLastReleaseKind(value any) error {
	kind, isText := value.(string)
	if !isText {
		return fmt.Errorf("workspace row has a last release kind that is not text: %w", ErrCorrupt)
	}

	if kind != "" && !workspace.ReleaseKind(kind).Valid() {
		return fmt.Errorf("workspace row has an invalid last release kind: %w", ErrCorrupt)
	}

	return nil
}

// portColOutputSharingDefault is the column of the output sharing setting of
// a namespace.
const portColOutputSharingDefault = "output_sharing_default"

// checkOutputSharingDefault refuses a namespace row whose output sharing
// setting is not a value of the setting. It returns ErrCorrupt, wrapped with
// outputshare.ErrInvalid.
//
// Defense in depth: the CHECK of the column refuses such a value at the copy,
// and the store refuses it at each read. This check stays so that a restored
// backup fails before it reaches the database.
func checkOutputSharingDefault(value any) error {
	d, isText := value.(string)
	if !isText {
		d = ""
	}

	if err := outputshare.Default(d).Validate(); err != nil {
		return fmt.Errorf("namespace row has an invalid output sharing setting: %w: %w",
			outputshare.ErrInvalid, ErrCorrupt)
	}

	return nil
}

// portColSourceFilter is the column of the source filter of a namespace.
const portColSourceFilter = "source_filter"

// checkSourceFilter refuses a namespace row whose source filter is not the
// empty text or a filter document that passes the checks of a filter. It
// returns ErrCorrupt, wrapped with source.ErrInvalid. The error does not hold
// the value.
//
// Defense in depth: the CHECK of the column refuses a text that is not JSON
// at the copy, and the store refuses a document that is not a filter at each
// read. This check stays so that a restored backup fails before it reaches the
// database.
func checkSourceFilter(value any) error {
	text, isText := value.(string)
	if !isText {
		return fmt.Errorf("namespace row has a source filter that is not text: %w: %w", source.ErrInvalid, ErrCorrupt)
	}

	if _, err := source.DecodeFilter(text); err != nil {
		return fmt.Errorf("namespace row has an invalid source filter: %w: %w", source.ErrInvalid, ErrCorrupt)
	}

	return nil
}

// portableValueOf decodes one JSON value of a column of kind. A null is nil for
// every kind.
func portableValueOf(column Column, raw json.RawMessage) (any, error) {
	if string(bytes.TrimSpace(raw)) == "null" {
		return nil, nil //nolint:nilnil // A null value has no Go value but nil.
	}

	switch column.Kind {
	case KindText:
		return decodeText(raw)
	case KindInteger, KindTime:
		return decodeInteger(raw)
	case KindBool:
		return decodeBool(raw)
	case KindBytes:
		return decodeBytes(raw)
	case KindJSON:
		return decodeJSON(raw)
	}

	return nil, fmt.Errorf("column kind %d is unknown: %w", column.Kind, ErrCoverage)
}

func decodeText(raw json.RawMessage) (string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("text value: %w", ErrCorrupt)
	}

	return s, nil
}

func decodeInteger(raw json.RawMessage) (int64, error) {
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, fmt.Errorf("integer value: %w", ErrCorrupt)
	}

	v, err := strconv.ParseInt(n.String(), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("integer value: %w", ErrCorrupt)
	}

	return v, nil
}

func decodeBool(raw json.RawMessage) (bool, error) {
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		return false, fmt.Errorf("boolean value: %w", ErrCorrupt)
	}

	return b, nil
}

func decodeBytes(raw json.RawMessage) ([]byte, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("bytes value: %w", ErrCorrupt)
	}

	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("bytes value: %w", ErrCorrupt)
	}

	return b, nil
}

func decodeJSON(raw json.RawMessage) (string, error) {
	if !json.Valid(raw) {
		return "", fmt.Errorf("JSON value: %w", ErrCorrupt)
	}

	return string(raw), nil
}

// portableNames returns the names of the columns of table in order.
func portableNames(table PortableTable) []string {
	names := make([]string, 0, len(table.Columns))
	for _, column := range table.Columns {
		names = append(names, column.Name)
	}

	return names
}

// countingReader counts the bytes that a reader returns.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)

	if err != nil && !errors.Is(err, io.EOF) {
		return n, fmt.Errorf("read table file bytes: %w", err)
	}

	return n, err //nolint:wrapcheck // io.EOF is the end of the file, not a failure.
}

// IndexPortableFiles checks the descriptors of a portable backup as a whole and
// returns them by table. Each table of PortableTables must have exactly one
// descriptor, and the descriptor's row count must equal the row count of the
// summary, rows, for that table. A table the spec does not name, a table that
// holds two descriptors, a table with none, and a table that rows does not hold
// return ErrCorrupt, and the error names the table (ADR 42, decision 4). Both
// dialects check the list here, before they load a row.
func IndexPortableFiles(files []PortableFile, rows map[string]int64) (map[string]PortableFile, error) {
	index, err := indexPortableDescriptors(files)
	if err != nil {
		return nil, err
	}

	for _, table := range PortableTables() {
		want, counted := rows[table.Table]
		if !counted {
			return nil, fmt.Errorf(
				"portable backup summary holds no row count of table %q: %w",
				table.Table,
				ErrCorrupt,
			)
		}

		file := index[table.Table]
		if file.Rows != want {
			return nil, fmt.Errorf("table %q file holds %d rows, the summary says %d: %w",
				table.Table, file.Rows, want, ErrCorrupt)
		}
	}

	return index, nil
}

// indexPortableDescriptors checks the names of the descriptors of a portable
// backup: each table of PortableTables has exactly one, at the path of its
// table, and no other table is named. readManifest runs this check before any
// object is opened, so a descriptor cannot point at another object of the
// backup. It returns ErrCorrupt and names the table on a failure.
func indexPortableDescriptors(files []PortableFile) (map[string]PortableFile, error) {
	known := make(map[string]bool)

	for _, table := range PortableTables() {
		known[table.Table] = true
	}

	index := make(map[string]PortableFile, len(files))

	for _, file := range files {
		if !known[file.Table] {
			return nil, fmt.Errorf("portable backup names unknown table %q: %w", file.Table, ErrCorrupt)
		}

		if file.Path != PortablePath(file.Table) {
			return nil, fmt.Errorf("portable backup names path %q for table %q, want %q: %w",
				file.Path, file.Table, PortablePath(file.Table), ErrCorrupt)
		}

		if _, dup := index[file.Table]; dup {
			return nil, fmt.Errorf("portable backup holds two files of table %q: %w", file.Table, ErrCorrupt)
		}

		index[file.Table] = file
	}

	for _, table := range PortableTables() {
		if _, ok := index[table.Table]; !ok {
			return nil, fmt.Errorf("portable backup misses the file of table %q: %w", table.Table, ErrCorrupt)
		}
	}

	return index, nil
}

// portableFileOf returns the descriptor of table in files.
func portableFileOf(files []PortableFile, table string) (PortableFile, bool) {
	for _, file := range files {
		if file.Table == table {
			return file, true
		}
	}

	var none PortableFile

	return none, false
}
