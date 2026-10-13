package storetest

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

// PortableStore is a store that exports the portable format: the backup store
// of RunBackup, with the portable exporter.
type PortableStore interface {
	BackupStore
	backup.PortableExporter
}

// PortableFactory returns a fresh portable store for one test.
type PortableFactory func(t *testing.T) PortableStore

// RunPortable runs the conformance cases of the portable export (ADR 42,
// decisions 2 to 4 and 6) against the store that newStore returns.
func RunPortable(t *testing.T, newStore PortableFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, newStore PortableFactory)
		name string
	}{
		{name: "every table has one file that matches its descriptor", test: testPortableFiles},
		{name: "an export of the same rows gives the same bytes", test: testPortableByteIdentical},
		{name: "values take their portable forms", test: testPortableValueForms},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore)
		})
	}
}

// portableSink holds the bytes of each file in memory, by path.
type portableSink struct {
	files map[string]*bytes.Buffer
}

// portableWriter is the writer of one file of portableSink.
type portableWriter struct {
	*bytes.Buffer
}

// Close does nothing: the buffer stays in the sink.
func (portableWriter) Close() error {
	return nil
}

// open implements backup.PortableSink.
func (s *portableSink) open(path string) (io.WriteCloser, error) {
	buf := &bytes.Buffer{}
	s.files[path] = buf

	return portableWriter{Buffer: buf}, nil
}

// exportPortable runs one export into a fresh sink and returns the sink with the
// summary and the descriptors.
func exportPortable(t *testing.T, s PortableStore) (*portableSink, backup.Summary, []backup.PortableFile) {
	t.Helper()

	sink := &portableSink{files: make(map[string]*bytes.Buffer)}

	sum, files, err := s.ExportPortable(t.Context(), sink.open)
	if err != nil {
		t.Fatalf("export portable: %v", err)
	}

	return sink, sum, files
}

// readPortableLines returns the JSON objects of one table file, decoded after
// gzip. Each line is one row.
func readPortableLines(t *testing.T, path string, data []byte) []map[string]any {
	t.Helper()

	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("open gzip of %q: %v", path, err)
	}

	defer func() {
		if closeErr := zr.Close(); closeErr != nil {
			t.Errorf("close gzip of %q: %v", path, closeErr)
		}
	}()

	var rows []map[string]any

	scanner := bufio.NewScanner(zr)
	scanner.Buffer(make([]byte, 0, 1<<16), 1<<26)

	for scanner.Scan() {
		row := map[string]any{}
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatalf("decode a row of %q: %v", path, err)
		}

		rows = append(rows, row)
	}

	if err := scanner.Err(); err != nil {
		t.Fatalf("read %q: %v", path, err)
	}

	return rows
}

// testPortableFiles checks that the export writes one file for each table of
// backup.Tables, and that each file matches its descriptor: the path, the
// columns, the bytes, the row count, and the row count of the summary (ADR 42,
// decision 4).
func testPortableFiles(t *testing.T, newStore PortableFactory) {
	t.Helper()

	s := newStore(t)
	seedBackupStore(t, s)

	sink, sum, files := exportPortable(t, s)

	if len(files) != len(backup.Tables) {
		t.Fatalf("portable files = %d, want one for each of %d tables", len(files), len(backup.Tables))
	}

	for i, table := range backup.Tables {
		file := files[i]

		if file.Table != table || file.Path != backup.PortablePath(table) {
			t.Fatalf("file %d = table %q at %q, want table %q at %q",
				i, file.Table, file.Path, table, backup.PortablePath(table))
		}

		checkPortableFile(t, sink, file)

		if sum.RowCounts[table] != file.Rows {
			t.Fatalf("summary rows of %q = %d, file rows = %d", table, sum.RowCounts[table], file.Rows)
		}
	}
}

// checkPortableFile checks the bytes of one file against its descriptor: the
// size, the SHA-256, the row count and the columns.
func checkPortableFile(t *testing.T, sink *portableSink, file backup.PortableFile) {
	t.Helper()

	data := sink.files[file.Path]
	if data == nil {
		t.Fatalf("no bytes written for %q", file.Path)
	}

	digest := sha256.Sum256(data.Bytes())
	if hex.EncodeToString(digest[:]) != file.SHA256 || int64(data.Len()) != file.Size {
		t.Fatalf("file %q does not match its descriptor: size %d, sha256 %x", file.Path, data.Len(), digest)
	}

	if rows := int64(len(readPortableLines(t, file.Path, data.Bytes()))); rows != file.Rows {
		t.Fatalf("file %q holds %d rows, descriptor says %d", file.Path, rows, file.Rows)
	}

	if !slices.Equal(file.Columns, portableColumnNames(file.Table)) {
		t.Fatalf("file %q columns = %v, want %v", file.Path, file.Columns, portableColumnNames(file.Table))
	}
}

// portableColumnNames returns the portable column names of table, in order.
func portableColumnNames(table string) []string {
	for _, spec := range backup.PortableTables() {
		if spec.Table == table {
			names := make([]string, 0, len(spec.Columns))
			for _, column := range spec.Columns {
				names = append(names, column.Name)
			}

			return names
		}
	}

	return nil
}

// testPortableByteIdentical checks that two exports of the same rows write the
// same bytes for every file (ADR 42, decision 2).
func testPortableByteIdentical(t *testing.T, newStore PortableFactory) {
	t.Helper()

	s := newStore(t)
	seedBackupStore(t, s)

	first, _, _ := exportPortable(t, s)
	second, _, _ := exportPortable(t, s)

	if len(first.files) != len(backup.Tables) || len(second.files) != len(first.files) {
		t.Fatalf(
			"files of the two exports = %d and %d, want %d",
			len(first.files),
			len(second.files),
			len(backup.Tables),
		)
	}

	for path, data := range first.files {
		other := second.files[path]
		if other == nil || !bytes.Equal(data.Bytes(), other.Bytes()) {
			t.Fatalf("file %q differs between two exports of the same rows", path)
		}
	}
}

// testPortableValueForms checks the portable forms of the values (ADR 42,
// decision 3): a boolean is true or false, a time is a number, a byte value is a
// base64 string, and a JSON value is JSON and not a string that holds it.
func testPortableValueForms(t *testing.T, newStore PortableFactory) {
	t.Helper()

	s := newStore(t)
	seed := seedBackupStore(t, s)

	sink, _, _ := exportPortable(t, s)

	namespaces := readPortableLines(
		t,
		backup.PortablePath("namespaces"),
		sink.files[backup.PortablePath("namespaces")].Bytes(),
	)
	acme := findPortableRow(t, namespaces, "name", seed.acme.Name.String())

	if forced, isBool := acme["allow_forced_writes"].(bool); !isBool || !forced {
		t.Fatalf(
			"allow_forced_writes = %v, want true for the namespace with its switch on",
			acme["allow_forced_writes"],
		)
	}

	if _, isNumber := acme["created_at_ms"].(float64); !isNumber {
		t.Fatalf(
			"created_at_ms = %v (%T), want a number of Unix milliseconds",
			acme["created_at_ms"],
			acme["created_at_ms"],
		)
	}

	states := readPortableLines(
		t,
		backup.PortablePath("state_versions"),
		sink.files[backup.PortablePath("state_versions")].Bytes(),
	)
	if len(states) == 0 {
		t.Fatal("state_versions holds no row, want the seeded versions")
	}

	md5, isString := states[0]["md5"].(string)
	if !isString {
		t.Fatalf("md5 = %v (%T), want a base64 string", states[0]["md5"], states[0]["md5"])
	}

	if raw, err := base64.StdEncoding.DecodeString(md5); err != nil || len(raw) != 16 {
		t.Fatalf("md5 %q is not base64 of 16 bytes: %v", md5, err)
	}

	checkpoints := readPortableLines(
		t,
		backup.PortablePath("checkpoints"),
		sink.files[backup.PortablePath("checkpoints")].Bytes(),
	)
	if len(checkpoints) == 0 {
		t.Fatal("checkpoints holds no row, want the seeded checkpoint")
	}

	if _, isText := checkpoints[0]["features"].(string); isText || checkpoints[0]["features"] == nil {
		t.Fatalf(
			"features = %v (%T), want JSON and not a string",
			checkpoints[0]["features"],
			checkpoints[0]["features"],
		)
	}
}

// findPortableRow returns the first row whose member name equals value.
func findPortableRow(t *testing.T, rows []map[string]any, name, value string) map[string]any {
	t.Helper()

	for _, row := range rows {
		if row[name] == value {
			return row
		}
	}

	t.Fatalf("no row with %s = %q", name, value)

	return nil
}
