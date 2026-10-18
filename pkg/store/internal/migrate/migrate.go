// Package migrate loads ordered SQL migrations from an embedded directory.
// Each store adapter applies them with its own driver and locking.
package migrate

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"

	"opendustpan.org/opendustpan/pkg/store/schema"
)

// ErrBadName reports a migration file not named NNNN_description.sql.
var ErrBadName = errors.New("migration file must be named NNNN_description.sql")

// Inspect returns the schema state for recorded versions against the
// embedded migrations: the newest recorded version, the newest embedded
// version, the names of the embedded migrations the database does not
// record, and the recorded versions the binary does not embed.
func Inspect(recorded []int64, embedded []Migration) schema.Inspection {
	done := make(map[int64]bool, len(recorded))
	for _, v := range recorded {
		done[v] = true
	}

	var pending []string

	for _, m := range embedded {
		if !done[m.Version] {
			pending = append(pending, m.Name)
		}
	}

	versions := make([]int64, 0, len(embedded))
	for _, m := range embedded {
		versions = append(versions, m.Version)
	}

	return schema.Inspection{
		DatabaseVersion: schema.DatabaseVersion(recorded),
		BinaryVersion:   schema.BinaryVersion(versions),
		Pending:         pending,
		Unknown:         UnknownRecorded(recorded, embedded),
		Minimum:         "",
	}
}

// UnknownRecorded returns the recorded versions the binary does not embed,
// ordered. It returns nothing when the database is equal or older, even when
// a recorded version is missing in the middle.
func UnknownRecorded(recorded []int64, embedded []Migration) []int64 {
	known := make(map[int64]bool, len(embedded))
	for _, m := range embedded {
		known[m.Version] = true
	}

	var unknown []int64

	for _, v := range recorded {
		if !known[v] {
			unknown = append(unknown, v)
		}
	}

	slices.Sort(unknown)

	return unknown
}

// Migration is one versioned schema change. MinBinaryVersion is the minimum
// binary version that the migration sets, from its header line
// "-- minimum-binary-version: vMAJOR.MINOR.PATCH", or "" when it sets none.
// A contract migration sets it: a step that deletes or narrows something
// that older binaries still use.
type Migration struct {
	Name             string
	SQL              string
	MinBinaryVersion string
	Version          int64
}

// minimumToken names the header that declares the minimum binary version a
// migration sets: one line "-- minimum-binary-version: vMAJOR.MINOR.PATCH"
// in the leading comment block of the file.
const minimumToken = "minimum-binary-version"

// Plan is what a store applies at open: the pending migrations in order and
// the minimum binary version the database schema records.
type Plan struct {
	Pending []Migration
	Minimum string
}

// Check compares the recorded migrations with the embedded migrations for a
// binary of version binaryVersion. It returns a *schema.MinimumError when the
// binary is below the minimum the schema records. A database that records
// migrations the binary does not embed gets no pending migrations: the
// binary starts on the newer schema and applies nothing. A database that
// records unknown migrations and lacks embedded ones fails with
// schema.ErrSchemaDiverged.
func Check(recorded []schema.AppliedMigration, embedded []Migration, binaryVersion string) (Plan, error) {
	minimums := make([]string, 0, len(recorded))
	versions := make([]int64, 0, len(recorded))

	for _, r := range recorded {
		minimums = append(minimums, r.MinBinaryVersion)
		versions = append(versions, r.Version)
	}

	minimum, err := schema.HighestMinimum(minimums)
	if err != nil {
		return Plan{}, fmt.Errorf("read schema minimum: %w", err)
	}

	if err := schema.CheckMinimum(minimum, binaryVersion); err != nil {
		return Plan{Pending: nil, Minimum: minimum}, err
	}

	done := make(map[int64]bool, len(versions))
	for _, v := range versions {
		done[v] = true
	}

	var pending []Migration

	for _, m := range embedded {
		if !done[m.Version] {
			pending = append(pending, m)
		}
	}

	if err := checkPendingMinimums(pending, binaryVersion); err != nil {
		return Plan{Pending: nil, Minimum: minimum}, err
	}

	unknown := UnknownRecorded(versions, embedded)

	switch {
	case len(unknown) > 0 && len(pending) > 0:
		return Plan{Pending: nil, Minimum: minimum}, fmt.Errorf(
			"%w: the database records migrations %v that this binary does not know, and lacks %d of its migrations: "+
				"restore a backup",
			schema.ErrSchemaDiverged, unknown, len(pending),
		)
	case len(unknown) > 0:
		return Plan{Pending: nil, Minimum: minimum}, nil
	default:
		return Plan{Pending: pending, Minimum: minimum}, nil
	}
}

// Load reads every *.sql file in dir of fsys, ordered by version.
func Load(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}

	migrations := make([]Migration, 0, len(entries))

	for _, entry := range entries {
		if entry.IsDir() || path.Ext(entry.Name()) != ".sql" {
			continue
		}

		m, err := loadFile(fsys, dir, entry.Name())
		if err != nil {
			return nil, err
		}

		migrations = append(migrations, m)
	}

	slices.SortFunc(migrations, func(a, b Migration) int {
		return int(a.Version - b.Version)
	})

	return migrations, nil
}

func loadFile(fsys fs.FS, dir, name string) (Migration, error) {
	prefix, _, ok := strings.Cut(name, "_")
	if !ok {
		return Migration{}, fmt.Errorf("%w: %s", ErrBadName, name)
	}

	version, err := strconv.ParseInt(prefix, 10, 64)
	if err != nil || version <= 0 {
		return Migration{}, fmt.Errorf("%w: %s", ErrBadName, name)
	}

	data, err := fs.ReadFile(fsys, path.Join(dir, name))
	if err != nil {
		return Migration{}, fmt.Errorf("read migration %s: %w", name, err)
	}

	minimum, err := readMinimum(string(data))
	if err != nil {
		return Migration{}, fmt.Errorf("migration %s: %w", name, err)
	}

	return Migration{Name: name, SQL: string(data), MinBinaryVersion: minimum, Version: version}, nil
}

// checkPendingMinimums refuses to apply a migration whose declared minimum
// is above binaryVersion: the binary would then refuse its own database at
// the next start. A dev build may apply it.
func checkPendingMinimums(pending []Migration, binaryVersion string) error {
	for _, m := range pending {
		if m.MinBinaryVersion == "" {
			continue
		}

		if err := schema.CheckMinimum(m.MinBinaryVersion, binaryVersion); err != nil {
			return fmt.Errorf("migration %s sets a minimum above this binary, so it applies nothing: %w", m.Name, err)
		}
	}

	return nil
}

// readMinimum returns the minimum binary version that the header of sql
// declares, or "" when sql has none. The whole file is searched for the
// header name in any case: it must appear at most once, on a comment line
// "-- minimum-binary-version: vMAJOR.MINOR.PATCH" in the leading comment
// block, which can hold blank lines. Any other place, a second header or a
// malformed line fails with schema.ErrBadMinimum, so a header cannot be
// skipped by mistake.
func readMinimum(sql string) (string, error) {
	switch n := strings.Count(strings.ToLower(sql), minimumToken); {
	case n == 0:
		return "", nil
	case n > 1:
		return "", fmt.Errorf("%w: the header %s appears %d times, want once", schema.ErrBadMinimum, minimumToken, n)
	}

	for line := range strings.Lines(sql) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		comment, isComment := strings.CutPrefix(trimmed, "--")
		if !isComment {
			break
		}

		comment = strings.TrimSpace(comment)
		if !strings.Contains(strings.ToLower(comment), minimumToken) {
			continue
		}

		return parseMinimumLine(comment)
	}

	return "", fmt.Errorf("%w: the header %s is not in the leading comment block", schema.ErrBadMinimum, minimumToken)
}

// parseMinimumLine reads the value of one header comment, the text after
// "--": the header name in any case, a colon and the version.
func parseMinimumLine(comment string) (string, error) {
	if len(comment) < len(minimumToken) || !strings.EqualFold(comment[:len(minimumToken)], minimumToken) {
		return "", fmt.Errorf(
			"%w: the line %q must start with the header %s",
			schema.ErrBadMinimum,
			comment,
			minimumToken,
		)
	}

	value, ok := strings.CutPrefix(strings.TrimSpace(comment[len(minimumToken):]), ":")
	if !ok {
		return "", fmt.Errorf("%w: the line %q has no colon after %s", schema.ErrBadMinimum, comment, minimumToken)
	}

	value = strings.TrimSpace(value)
	if err := schema.ValidMinimum(value); err != nil {
		return "", err
	}

	return value, nil
}

// CollapseVersion returns the version of the baseline that replaces the
// migrations with the given versions, in every dialect. It is above each of
// them and at least schema.BaselineVersion. So a database from an untagged
// build records versions that the new binary does not embed, and the
// migration check refuses it instead of finding no pending migration.
func CollapseVersion(versions []int64) int64 {
	next := schema.BaselineVersion

	for _, v := range versions {
		if v >= next {
			next = v + 1
		}
	}

	return next
}
