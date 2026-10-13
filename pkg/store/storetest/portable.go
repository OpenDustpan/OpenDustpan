package storetest

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

// PortableStore is a store that exports the portable format: the backup store
// of RunBackup, with the portable exporter.
type PortableStore interface {
	BackupStore
	backup.PortableExporter
	backup.PortableStager
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
		{name: "a portable export stages with the same rows", test: testPortableStages},
		{name: "a table file that differs from its descriptor is refused", test: testPortableStageRefusesCorrupt},
		{name: "a portable stage refuses another schema version", test: testPortableStageRefusesVersion},
		{name: "an empty byte value is the empty string, not null", test: testPortableEmptyBytes},
		{name: "a files list with a duplicate table is refused", test: testPortableStageRefusesDuplicate},
		{name: "a files list with an unknown table is refused", test: testPortableStageRefusesUnknown},
		{name: "a files list with a missing table is refused", test: testPortableStageRefusesMissing},
		{name: "a summary with a wrong row count is refused", test: testPortableStageRefusesRowCount},
		{name: "peek returns one key of the wrapper without loading", test: testPortablePeekKey},
		{name: "peek refuses an unknown wrapper", test: testPortablePeekUnknownWrapper},
		{name: "peek refuses a corrupt key file", test: testPortablePeekCorrupt},
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

// read returns the bytes of one file of the sink, as a reader.
func (s *portableSink) read(path string) (io.ReadCloser, error) {
	data := s.files[path]
	if data == nil {
		return nil, fmt.Errorf("no file %q: %w", path, backup.ErrCorrupt)
	}

	return io.NopCloser(bytes.NewReader(data.Bytes())), nil
}

// testPortableStages checks that a portable export stages back into the store
// with the row counts of its summary, and that the staging area can be
// discarded (ADR 42, decision 8).
func testPortableStages(t *testing.T, newStore PortableFactory) {
	t.Helper()

	s := newStore(t)
	seedBackupStore(t, s)

	sink, sum, files := exportPortable(t, s)

	staged, err := s.StagePortable(t.Context(), sink.read, sum, files)
	if err != nil {
		t.Fatalf("stage portable backup: %v", err)
	}

	for _, table := range backup.Tables {
		if staged.RowCounts[table] != sum.RowCounts[table] {
			t.Fatalf("staged rows of %q = %d, want %d", table, staged.RowCounts[table], sum.RowCounts[table])
		}
	}

	if err := s.DiscardStaging(t.Context()); err != nil {
		t.Fatalf("discard staging: %v", err)
	}
}

// testPortableStageRefusesCorrupt checks that one table file that differs from
// its descriptor fails the stage with ErrCorrupt (ADR 42, decision 4).
func testPortableStageRefusesCorrupt(t *testing.T, newStore PortableFactory) {
	t.Helper()

	s := newStore(t)
	seedBackupStore(t, s)

	sink, sum, files := exportPortable(t, s)

	data := sink.files[backup.PortablePath("namespaces")]
	raw := data.Bytes()
	raw[len(raw)/2] ^= 0xff

	_, err := s.StagePortable(t.Context(), sink.read, sum, files)
	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("stage a corrupt table file: err = %v, want ErrCorrupt", err)
	}
}

// testPortableStageRefusesVersion checks that a portable backup of another
// schema version is refused with ErrSchemaMismatch.
func testPortableStageRefusesVersion(t *testing.T, newStore PortableFactory) {
	t.Helper()

	s := newStore(t)
	seedBackupStore(t, s)

	sink, sum, files := exportPortable(t, s)
	sum.SchemaVersion++

	_, err := s.StagePortable(t.Context(), sink.read, sum, files)
	if !errors.Is(err, backup.ErrSchemaMismatch) {
		t.Fatalf("stage another schema version: err = %v, want ErrSchemaMismatch", err)
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

// testPortablePeekKey peeks one wrapped data key per seeded wrapper from the
// secret_keys file of the export through an empty store: the key must equal the
// stored row, and the peeking store must stay empty (ADR 42, decision 4).
func testPortablePeekKey(t *testing.T, newStore PortableFactory) {
	t.Helper()

	src := newStore(t)
	seed := seedBackupStore(t, src)
	sink, _, files := exportPortable(t, src)
	keyFile := portableFileNamed(t, files, "secret_keys")

	dst := newStore(t)

	for i, wrapper := range []string{"kek-1", "kek-2"} {
		got, err := dst.PeekPortableKey(t.Context(), sink.read, keyFile, wrapper)
		if err != nil {
			t.Fatalf("peek key of wrapper %q: %v", wrapper, err)
		}

		want := mustGetKey(t, src, seed.scopes[i])

		if got.Scope != want.Scope || got.WrapperID != want.WrapperID ||
			!got.CreatedAt.Equal(want.CreatedAt) || !bytes.Equal(got.Ciphertext, want.Ciphertext) {
			t.Fatalf("peeked key of wrapper %q = %+v, want %+v", wrapper, got, want)
		}
	}

	assertBackupEmpty(t, dst)
}

// testPortablePeekUnknownWrapper peeks a wrapper the export holds no key of and
// expects ErrCorrupt, with the peeking store still empty.
func testPortablePeekUnknownWrapper(t *testing.T, newStore PortableFactory) {
	t.Helper()

	src := newStore(t)
	seedBackupStore(t, src)
	sink, _, files := exportPortable(t, src)

	dst := newStore(t)

	_, err := dst.PeekPortableKey(t.Context(), sink.read, portableFileNamed(t, files, "secret_keys"), "kek-unknown")
	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("peek key of an unknown wrapper: err = %v, want ErrCorrupt", err)
	}

	assertBackupEmpty(t, dst)
}

// testPortablePeekCorrupt peeks a key file that differs from its descriptor and
// expects ErrCorrupt, with the peeking store still empty.
func testPortablePeekCorrupt(t *testing.T, newStore PortableFactory) {
	t.Helper()

	src := newStore(t)
	seedBackupStore(t, src)
	sink, _, files := exportPortable(t, src)
	keyFile := portableFileNamed(t, files, "secret_keys")

	raw := sink.files[keyFile.Path].Bytes()
	raw[len(raw)/2] ^= 0xff

	dst := newStore(t)

	if _, err := dst.PeekPortableKey(t.Context(), sink.read, keyFile, "kek-1"); !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("peek key of a corrupt file: err = %v, want ErrCorrupt", err)
	}

	assertBackupEmpty(t, dst)
}

// portableFileNamed returns the descriptor of table in files, failing the test
// when the export holds none.
func portableFileNamed(t *testing.T, files []backup.PortableFile, table string) backup.PortableFile {
	t.Helper()

	var found backup.PortableFile

	for _, file := range files {
		if file.Table == table {
			found = file

			break
		}
	}

	if found.Table != table {
		t.Fatalf("export holds no file of table %q", table)
	}

	return found
}

// testPortableStageRefusesDuplicate checks that a files list that holds two
// descriptors of one table fails the stage with ErrCorrupt before any load, and
// names the table (ADR 42, decision 4).
func testPortableEmptyBytes(t *testing.T, newStore PortableFactory) {
	t.Helper()

	s := newStore(t)
	seedBackupStore(t, s)

	sink, _, _ := exportPortable(t, s)
	path := backup.PortablePath("updates")

	found := false

	for _, row := range readPortableLines(t, path, sink.files[path].Bytes()) {
		value, present := row["lease_digest"]
		if !present {
			t.Fatalf("row of %q misses lease_digest", path)
		}

		if value == nil {
			t.Fatal("lease_digest = null, want the empty string for an empty value")
		}

		if value == "" {
			found = true
		}
	}

	if !found {
		t.Fatal("no updates row holds an empty lease_digest, so the case proves nothing")
	}
}

// testPortableStageRefusesDuplicate checks that a files list that holds two
// descriptors of one table fails the stage with ErrCorrupt before any load, and
// names the table (ADR 42, decision 4).
func testPortableStageRefusesDuplicate(t *testing.T, newStore PortableFactory) {
	t.Helper()

	s := newStore(t)
	seedBackupStore(t, s)

	sink, sum, files := exportPortable(t, s)
	files = append(files, files[0])

	assertStageRefused(t, s, sink, portableExport{sum: sum, files: files}, "namespaces")
}

// testPortableStageRefusesUnknown checks that a files list that names a table the
// spec does not hold fails the stage with ErrCorrupt, and names the table.
func testPortableStageRefusesUnknown(t *testing.T, newStore PortableFactory) {
	t.Helper()

	s := newStore(t)
	seedBackupStore(t, s)

	sink, sum, files := exportPortable(t, s)
	files = append(files, backup.PortableFile{
		Table:   "no_such_table",
		Path:    "sql/no_such_table.jsonl.gz",
		SHA256:  "00",
		Columns: nil,
		Rows:    0,
		Size:    0,
	})

	assertStageRefused(t, s, sink, portableExport{sum: sum, files: files}, "no_such_table")
}

// testPortableStageRefusesMissing checks that a files list that omits a table of
// the spec fails the stage with ErrCorrupt, and names the table.
func testPortableStageRefusesMissing(t *testing.T, newStore PortableFactory) {
	t.Helper()

	s := newStore(t)
	seedBackupStore(t, s)

	sink, sum, files := exportPortable(t, s)
	var kept []backup.PortableFile

	for _, file := range files {
		if file.Table != "installation" {
			kept = append(kept, file)
		}
	}

	assertStageRefused(t, s, sink, portableExport{sum: sum, files: kept}, "installation")
}

// portableExport holds a summary and its files, as an export returns them.
type portableExport struct {
	sum   backup.Summary
	files []backup.PortableFile
}

// assertStageRefused stages files and checks that the stage fails with
// ErrCorrupt, naming table.
func assertStageRefused(
	t *testing.T, s PortableStore, sink *portableSink, export portableExport, table string,
) {
	t.Helper()

	_, err := s.StagePortable(t.Context(), sink.read, export.sum, export.files)
	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("stage a bad files list: err = %v, want ErrCorrupt", err)
	}

	if !strings.Contains(err.Error(), `"`+table+`"`) {
		t.Fatalf("error = %v, want it to name the table %q", err, table)
	}
}

// testPortableStageRefusesRowCount checks that the files are correct but the
// summary names another row count for one table: the stage fails with ErrCorrupt
// and names the table, on both dialects (ADR 42, decision 4).
func testPortableStageRefusesRowCount(t *testing.T, newStore PortableFactory) {
	t.Helper()

	s := newStore(t)
	seedBackupStore(t, s)

	sink, sum, files := exportPortable(t, s)

	counts := maps.Clone(sum.RowCounts)

	counts["updates"]++
	sum.RowCounts = counts

	assertStageRefused(t, s, sink, portableExport{sum: sum, files: files}, "updates")
}
