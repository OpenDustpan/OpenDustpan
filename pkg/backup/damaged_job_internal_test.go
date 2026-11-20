package backup

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// errFixtureChecker is the error of a job checker that fails in another way
// than a damaged job.
var errFixtureChecker = errors.New("the checker failed")

// errFixtureDriver is the error of a driver that holds a path.
var errFixtureDriver = errors.New("driver boom at /secret/path")

// fakeJobChecker refuses each row whose body is not "ok", as the job store
// refuses a body that it does not read. A non-nil other fails every row.
type fakeJobChecker struct {
	other error
}

func (f fakeJobChecker) CheckJobRow(row JobRow) error {
	if f.other != nil {
		return f.other
	}

	if row.Body != "ok" {
		return NewDamagedJobError(row.ID, row.UnitID, "the body is not JSON")
	}

	return nil
}

// jobsFixture is the set of the table files of a backup in memory.
type jobsFixture struct {
	files    map[string][]byte
	manifest Manifest
	nsID     string
}

// zeroValue returns the value of a column that a row of these tests leaves
// empty.
func zeroValue(c Column) any {
	switch c.Kind {
	case KindInteger, KindTime:
		return int64(0)
	case KindBool:
		return false
	case KindBytes:
		return []byte{}
	case KindJSON:
		return "{}"
	case KindText:
		return ""
	}

	return ""
}

// rowValues returns the values of one row of table, in column order. A column
// that row does not name holds its zero value.
func rowValues(table PortableTable, row map[string]any) []any {
	values := make([]any, len(table.Columns))

	for i, c := range table.Columns {
		if v, ok := row[c.Name]; ok {
			values[i] = v
		} else {
			values[i] = zeroValue(c)
		}
	}

	return values
}

// writeFixtureTable writes the table name with one row per entry of rows. Each
// entry maps a column name to its value.
func (f *jobsFixture) writeFixtureTable(t *testing.T, name string, rows []map[string]any) {
	t.Helper()

	for _, table := range PortableTables() {
		if table.Table != name {
			continue
		}

		var buf bytes.Buffer

		w, newErr := NewTableWriter(&buf, table)
		if newErr != nil {
			t.Fatalf("writer of %s: %v", name, newErr)
		}

		for _, row := range rows {
			if writeErr := w.WriteRow(rowValues(table, row)); writeErr != nil {
				t.Fatalf("write a row of %s: %v", name, writeErr)
			}
		}

		file, closeErr := w.Close()
		if closeErr != nil {
			t.Fatalf("close %s: %v", name, closeErr)
		}

		f.files[file.Path] = buf.Bytes()
		f.manifest.SQLFiles = append(f.manifest.SQLFiles, file)

		return
	}

	t.Fatalf("no table %s", name)
}

// newJobsFixture returns a backup with one namespace named acme and the job
// rows jobs. A job row that has no namespace_id key belongs to that namespace.
func newJobsFixture(t *testing.T, jobs []map[string]any) *jobsFixture {
	t.Helper()

	f := &jobsFixture{files: map[string][]byte{}, manifest: zeroManifest(), nsID: string(namespace.NewID())}
	f.writeFixtureTable(t, "namespaces", []map[string]any{
		{"id": f.nsID, "name": "acme", "output_sharing_default": "nobody"},
	})

	for _, row := range jobs {
		if _, ok := row["namespace_id"]; !ok {
			row["namespace_id"] = f.nsID
		}
	}

	f.writeFixtureTable(t, "jobs", jobs)

	return f
}

func (f *jobsFixture) open(path string) (io.ReadCloser, error) {
	data, ok := f.files[path]
	if !ok {
		return nil, &MissingObjectError{Key: ""}
	}

	return io.NopCloser(bytes.NewReader(data)), nil
}

func jobEntry(id, unit, body string) map[string]any {
	return map[string]any{"id": id, "unit_id": unit, "body": body}
}

// TestCheckBackupJobsRefusesDamagedRows is the table of hostile job rows for
// the layer of the backup service. Each row is one that a restore refuses. The
// check must name the first of them with the job, the unit and the reason, and
// the text holds no control character and has a bound.
func TestCheckBackupJobsRefusesDamagedRows(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("u", 5000)

	cases := []struct {
		name    string
		jobs    []map[string]any
		checker JobRowChecker
		want    string
	}{
		{
			name: "a damaged row", jobs: []map[string]any{jobEntry("job-1", "ws-1", "not json")},
			checker: fakeJobChecker{other: nil}, want: "damaged job job-1 of unit ws-1: the body is not JSON",
		},
		{
			name: "the first of two damaged rows",
			jobs: []map[string]any{
				jobEntry("job-1", "ws-1", "ok"), jobEntry("job-2", "ws-1", "x"), jobEntry("job-3", "ws-1", "y"),
			},
			checker: fakeJobChecker{other: nil}, want: "damaged job job-2 of unit ws-1",
		},
		{
			name: "a namespace that the backup lacks",
			jobs: []map[string]any{
				{"id": "job-1", "namespace_id": "ns-gone", "unit_id": "ws-1", "body": "ok"},
			},
			checker: fakeJobChecker{other: nil},
			want:    "damaged job job-1 of unit ws-1: " + ReasonJobNoNamespace,
		},
		{
			name:    "control characters in the ID and the unit",
			jobs:    []map[string]any{jobEntry("job-1\nlevel=ERROR forged", "ws\x1b[31m-1", "x")},
			checker: fakeJobChecker{other: nil},
			want:    "damaged job job-1?level=ERROR forged of unit ws?[31m-1",
		},
		{
			name: "a unit of 5000 bytes", jobs: []map[string]any{jobEntry("job-1", long, "x")},
			checker: fakeJobChecker{other: nil}, want: "of unit " + strings.Repeat("u", maxDamagedJobField) + "...: ",
		},
		{
			name: "a checker that fails with another error", jobs: []map[string]any{jobEntry("job-1", "ws-1", "ok")},
			checker: fakeJobChecker{other: errFixtureChecker},
			want:    "damaged job job-1 of unit ws-1: the job check failed",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newJobsFixture(t, tc.jobs)
			err := checkBackupJobs(t.Context(), tc.checker, f.open, f.manifest)

			damaged, ok := errors.AsType[*DamagedJobError](err)
			if !ok || damaged == nil || !errors.Is(err, ErrCorrupt) {
				t.Fatalf("check = %v, want a damaged job error", err)
			}

			if !strings.Contains(err.Error(), tc.want) || !strings.HasSuffix(err.Error(), ErrCorrupt.Error()) {
				t.Fatalf("check = %q, want %q", err, tc.want)
			}

			if strings.ContainsAny(err.Error(), "\r\n\x1b") || len(err.Error()) > 2000 {
				t.Fatalf("check = %q, want a bounded text with no control character", err)
			}
		})
	}
}

// TestCheckBackupJobsAcceptsRowsThatRead proves the other side: a row that the
// checker reads, and a backup with no job row, pass.
func TestCheckBackupJobsAcceptsRowsThatRead(t *testing.T) {
	t.Parallel()

	for name, jobs := range map[string][]map[string]any{
		"a row that reads": {jobEntry("job-1", "ws-1", "ok")},
		"no job rows":      nil,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newJobsFixture(t, jobs)
			if err := checkBackupJobs(t.Context(), fakeJobChecker{other: nil}, f.open, f.manifest); err != nil {
				t.Fatalf("check = %v, want nil", err)
			}
		})
	}
}

// TestCheckBackupJobsFailsClosed proves the refusals that no row decides: no
// checker, a table file with other bytes, and a manifest with no jobs file.
func TestCheckBackupJobsFailsClosed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		checker JobRowChecker
		mutate  func(f *jobsFixture)
		want    error
	}{
		{name: "no checker", checker: nil, mutate: func(*jobsFixture) {}, want: errJobCheckInput},
		{
			name: "a table file with other bytes", checker: fakeJobChecker{other: nil},
			mutate: func(f *jobsFixture) { f.files["sql/jobs.jsonl.gz"] = append(f.files["sql/jobs.jsonl.gz"], 0) },
			want:   ErrCorrupt,
		},
		{
			name: "a manifest with no jobs file", checker: fakeJobChecker{other: nil},
			mutate: func(f *jobsFixture) { f.manifest.SQLFiles = f.manifest.SQLFiles[:1] }, want: ErrCorrupt,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newJobsFixture(t, []map[string]any{jobEntry("job-1", "ws-1", "ok")})
			tc.mutate(f)

			if err := checkBackupJobs(t.Context(), tc.checker, f.open, f.manifest); !errors.Is(err, tc.want) {
				t.Fatalf("check = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestDamagedJobErrorText proves the text of the error is bounded and has no
// control character, whatever the row holds.
func TestDamagedJobErrorText(t *testing.T) {
	t.Parallel()

	err := NewDamagedJobError("a\r\nb\x00c\x7f", strings.Repeat("é", 1000), "r\tz")

	text := err.Error()
	for _, r := range text {
		if r < 0x20 || r > 0x7e {
			t.Fatalf("text %q holds the character %q", text, r)
		}
	}

	if len(text) > 4*maxDamagedJobField+200 {
		t.Fatalf("text holds %d bytes, want a bound", len(text))
	}

	if !strings.HasPrefix(text, DamagedJobPrefix) || !errors.Is(err, ErrCorrupt) {
		t.Fatalf("text = %q, want the prefix and ErrCorrupt", text)
	}
}

// TestRestoreErrorTextOfADamagedJob proves that a restore stores the text of a
// damaged job and nothing else from the error chain.
func TestRestoreErrorTextOfADamagedJob(t *testing.T) {
	t.Parallel()

	damaged := NewDamagedJobError("job-1", "ws-1", "r")

	got := sanitizeRestoreError(errors.Join(errFixtureDriver, damaged))
	if got != damaged.Error() {
		t.Fatalf("stored text = %q, want only the damaged job text", got)
	}
}

// zeroManifest returns a manifest with no field set.
func zeroManifest() Manifest {
	var m Manifest

	return m
}

// TestDamagedJobErrorTextEncodesAtThePointOfUse proves that Error cleans each
// field again. The value is built without NewDamagedJobError, as a new code
// path of this package could build it. The test fails when the cleaning in
// Error is removed.
func TestDamagedJobErrorTextEncodesAtThePointOfUse(t *testing.T) {
	t.Parallel()

	err := &DamagedJobError{
		jobID:  "job-1\r\nlevel=ERROR forged\x1b[31m",
		unitID: strings.Repeat("u", 5000),
		reason: "reason\r\n",
	}

	text := err.Error()
	if strings.ContainsAny(text, "\r\n\x1b") || len(text) > 4*maxDamagedJobField+200 {
		t.Fatalf("text %q holds a control character or has no bound (%d bytes)", text, len(text))
	}

	if !strings.HasPrefix(text, DamagedJobPrefix+"job-1??level=ERROR forged?[31m of unit u") {
		t.Fatalf("text = %q, want the cleaned job ID and the unit", text)
	}
}

// TestCheckBackupJobsDuplicateNamespace proves that a namespaces table with
// the same ID twice is corrupt, so a hostile table cannot choose the name that
// a job row gets.
func TestCheckBackupJobsDuplicateNamespace(t *testing.T) {
	t.Parallel()

	f := &jobsFixture{files: map[string][]byte{}, manifest: zeroManifest(), nsID: string(namespace.NewID())}
	twice := map[string]any{"id": f.nsID, "name": "acme", "output_sharing_default": "nobody"}
	other := map[string]any{"id": f.nsID, "name": "other", "output_sharing_default": "nobody"}

	f.writeFixtureTable(t, "namespaces", []map[string]any{twice, other})
	f.writeFixtureTable(t, "jobs", nil)

	err := checkBackupJobs(t.Context(), fakeJobChecker{other: nil}, f.open, f.manifest)
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("check = %v, want ErrCorrupt", err)
	}
}
