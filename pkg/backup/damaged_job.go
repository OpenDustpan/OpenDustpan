package backup

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// DamagedJobPrefix starts the text of a DamagedJobError. A client reads it to
// choose its help, so the text of the error never changes this start.
const DamagedJobPrefix = "damaged job "

// ReasonJobNoNamespace is the reason of a job row whose namespace the backup
// does not hold.
const ReasonJobNoNamespace = "the namespace of the job is not in the backup"

// maxDamagedJobField bounds each field of the text of a DamagedJobError.
const maxDamagedJobField = 160

// JobRow is one row of the jobs table of a backup, with the name of its
// namespace. The check of a backup and the restore read the same row with the
// same rules (JobRowChecker).
type JobRow struct {
	ID          string
	Namespace   string
	NamespaceID string
	UnitID      string
	State       string
	Wait        string
	Operation   string
	Key         string
	Body        string
	Seq         int64
	Phase       int64
	Version     int64
	CreatedMs   int64
	UpdatedMs   int64
	ChangedMs   int64
	Changes     bool
}

// JobRowChecker applies the rules of a stored job to one job row of a
// backup. The store of jobs implements it, and the restore uses the same
// function on its staged rows, so the check of a backup and a restore never
// disagree. It returns nil for a row that the server could read, and a
// *DamagedJobError for a row that it could not. It fails closed: an error
// that is not a *DamagedJobError is also a refusal.
type JobRowChecker interface {
	CheckJobRow(row JobRow) error
}

// DamagedJobError reports a job row that the server could not read: the
// backup holds it, and a restore refuses it. It wraps ErrCorrupt. Its text
// holds the job ID, the unit and the reason, and nothing else from the row.
type DamagedJobError struct {
	// JobID is the ID of the damaged job row.
	JobID string
	// UnitID is the unit ID of the row, not the unit that its body names.
	UnitID string
	// Reason says why the row is damaged.
	Reason string
}

// NewDamagedJobError returns the error for a damaged job row. A row of a
// backup is untrusted input, so each field is cut to a bound and each
// character that is not printable ASCII becomes "?". The text is safe in a
// log line, in the stored restore text and in the output of the CLI.
func NewDamagedJobError(jobID, unitID, reason string) *DamagedJobError {
	return &DamagedJobError{
		JobID:  cleanJobField(jobID),
		UnitID: cleanJobField(unitID),
		Reason: cleanJobField(reason),
	}
}

// Error returns the text. It ends with the text of ErrCorrupt, as every
// refusal of a corrupt backup does.
func (e *DamagedJobError) Error() string {
	return fmt.Sprintf("%s%s of unit %s: %s: %s", DamagedJobPrefix, e.JobID, e.UnitID, e.Reason, ErrCorrupt)
}

// Unwrap returns ErrCorrupt.
func (e *DamagedJobError) Unwrap() error { return ErrCorrupt }

// cleanJobField cuts s to the bound and replaces each character that is not
// printable ASCII.
func cleanJobField(s string) string {
	var b strings.Builder

	n := 0

	for _, r := range s {
		if n == maxDamagedJobField {
			b.WriteString("...")

			break
		}

		if r < 0x20 || r > 0x7e {
			r = '?'
		}

		b.WriteRune(r)

		n++
	}

	return b.String()
}

// errJobCheckInput reports a table of a backup that the job check cannot use.
var errJobCheckInput = errors.New("the job check cannot read the backup tables")

// checkBackupJobs reads the namespaces and the jobs tables of a backup and
// applies checker to each job row. It returns the first *DamagedJobError, so
// backup check names the same row that a restore stops at. A table file that
// does not match its descriptor returns ErrCorrupt.
func checkBackupJobs(ctx context.Context, checker JobRowChecker, open PortableOpener, manifest Manifest) error {
	if checker == nil {
		// Fail closed: a service without a checker never reports a backup
		// as complete.
		return fmt.Errorf("%w: no job checker", errJobCheckInput)
	}

	names, err := readNamespaceNames(ctx, open, manifest)
	if err != nil {
		return err
	}

	table, file, err := portableTableOf(manifest, "jobs")
	if err != nil {
		return err
	}

	pos := columnIndex(table)

	// The first damaged row stops the read, so the check names the first row
	// in the order of the file, as the restore names the first row it meets.
	err = ReadPortableTable(ctx, open, file, table, func(v []any) error {
		row := jobRowOf(v, pos)

		name, ok := names[row.NamespaceID]
		if !ok {
			return NewDamagedJobError(row.ID, row.UnitID, ReasonJobNoNamespace)
		}

		row.Namespace = name

		if checkErr := checker.CheckJobRow(row); checkErr != nil {
			return asDamagedJob(row, checkErr)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("read the jobs of the backup: %w", err)
	}

	return nil
}

// asDamagedJob returns err as a *DamagedJobError. An error of another type
// still refuses the row, with a reason that names no detail.
func asDamagedJob(row JobRow, err error) error {
	if damaged, ok := errors.AsType[*DamagedJobError](err); ok && damaged != nil {
		return damaged
	}

	return NewDamagedJobError(row.ID, row.UnitID, "the job check failed")
}

// readNamespaceNames returns the name of each namespace of the backup by ID.
func readNamespaceNames(ctx context.Context, open PortableOpener, manifest Manifest) (map[string]string, error) {
	table, file, err := portableTableOf(manifest, portTableNamespaces)
	if err != nil {
		return nil, err
	}

	pos := columnIndex(table)
	idAt, idOK := pos["id"]
	nameAt, nameOK := pos["name"]

	if !idOK || !nameOK {
		return nil, fmt.Errorf("%w: table %q has no id or name column: %w", errJobCheckInput, table.Table, ErrCorrupt)
	}

	names := map[string]string{}

	err = ReadPortableTable(ctx, open, file, table, func(v []any) error {
		names[textAt(v, idAt)] = textAt(v, nameAt)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return names, nil
}

// portableTableOf returns the description and the file of table in manifest.
func portableTableOf(manifest Manifest, name string) (PortableTable, PortableFile, error) {
	for _, table := range PortableTables() {
		if table.Table != name {
			continue
		}

		for _, file := range manifest.SQLFiles {
			if file.Table == name {
				return table, file, nil
			}
		}
	}

	return PortableTable{}, PortableFile{}, fmt.Errorf(
		"%w: no file of table %q: %w",
		errJobCheckInput,
		name,
		ErrCorrupt,
	)
}

// columnIndex maps the column names of table to their positions.
func columnIndex(table PortableTable) map[string]int {
	out := make(map[string]int, len(table.Columns))
	for i, c := range table.Columns {
		out[c.Name] = i
	}

	return out
}

// textAt returns the text at position i of a row, or "" for a value of
// another type or a position outside the row.
func textAt(v []any, i int) string {
	if i < 0 || i >= len(v) {
		return ""
	}

	if s, ok := v[i].(string); ok {
		return s
	}

	return ""
}

// numAt returns the integer at position i of a row, or 0 for a value of
// another type or a position outside the row.
func numAt(v []any, i int) int64 {
	if i < 0 || i >= len(v) {
		return 0
	}

	if n, ok := v[i].(int64); ok {
		return n
	}

	return 0
}

// flagAt returns the flag at position i of a row, or false for a value of
// another type or a position outside the row.
func flagAt(v []any, i int) bool {
	if i < 0 || i >= len(v) {
		return false
	}

	if b, ok := v[i].(bool); ok {
		return b
	}

	return false
}

// jobRowOf reads one job row from the values of a table file. A value of
// another type reads as its zero value, so a hostile file fails the row
// checks of the store and does not panic. A column that the table lacks reads
// as position -1.
func jobRowOf(v []any, pos map[string]int) JobRow {
	at := func(name string) int {
		if i, ok := pos[name]; ok {
			return i
		}

		return -1
	}

	return JobRow{
		ID: textAt(v, at("id")), Namespace: "", NamespaceID: textAt(v, at(portColNamespaceID)),
		UnitID: textAt(v, at(portColUnitID)), State: textAt(v, at(portColState)), Wait: textAt(v, at("wait")),
		Operation: textAt(v, at("current_operation")), Key: textAt(v, at("idempotency_key")),
		Body: textAt(v, at(portColBody)), Seq: numAt(v, at(portColSeq)), Phase: numAt(v, at("current_phase")),
		Version: numAt(v, at("version")), CreatedMs: numAt(v, at(portColCreatedAtMs)),
		UpdatedMs: numAt(v, at(portColUpdatedAtMs)), ChangedMs: numAt(v, at("state_changed_at_ms")),
		Changes: flagAt(v, at("current_changes")),
	}
}
