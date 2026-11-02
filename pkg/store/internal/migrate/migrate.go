// Package migrate loads ordered SQL migrations from an embedded directory.
// Each store adapter applies them with its own driver and locking.
package migrate

import (
	"crypto/sha256"
	"encoding/hex"
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

// ErrEmptySchemaFile reports a schema file that holds no statement.
var ErrEmptySchemaFile = errors.New("the schema file is empty")

// ErrDuplicateVersion reports two migration files with the same version.
var ErrDuplicateVersion = errors.New("two migration files have the same version")

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
// that older binaries still use. SHA256 is the SHA-256 of SQL as lowercase
// hex. The store records it with the migration, and Check refuses a database
// that records another value for the same version.
type Migration struct {
	Name             string
	SQL              string
	MinBinaryVersion string
	SHA256           string
	Version          int64
}

// sum returns the SHA-256 of sql as lowercase hex.
func sum(sql string) string {
	digest := sha256.Sum256([]byte(sql))

	return hex.EncodeToString(digest[:])
}

// SchemaFile returns the schema file of a store dialect as its one
// migration: version schema.BaselineVersion, name schema.BaselineName, and
// the minimum binary version of its header. Until the first tag, each store
// applies only this migration.
func SchemaFile(sql string) (Migration, error) {
	if strings.TrimSpace(sql) == "" {
		return Migration{}, fmt.Errorf("%w: %s", ErrEmptySchemaFile, schema.BaselineName)
	}

	minimum, err := readMinimum(sql)
	if err != nil {
		return Migration{}, fmt.Errorf("schema file %s: %w", schema.BaselineName, err)
	}

	return Migration{
		Name:             schema.BaselineName,
		SQL:              sql,
		MinBinaryVersion: minimum,
		SHA256:           sum(sql),
		Version:          schema.BaselineVersion,
	}, nil
}

// checkRecordedSums refuses a recorded migration whose version the binary
// embeds under another name or another SHA-256 of its SQL. Another build of
// the schema made such a database, and no migration can change it to the
// schema of this binary. A recorded value that is not a SHA-256 in lowercase
// hex fails too, so the check fails closed on a damaged row.
func checkRecordedSums(recorded []schema.AppliedMigration, embedded []Migration) error {
	byVersion := make(map[int64]Migration, len(embedded))
	for _, m := range embedded {
		byVersion[m.Version] = m
	}

	for _, r := range recorded {
		m, ok := byVersion[r.Version]
		if !ok {
			continue
		}

		if !schema.ValidSHA256(r.SHA256) {
			return fmt.Errorf("%w: migration %d records the SHA-256 %q, which is not 64 lowercase hex digits: "+
				"start the server on an empty database", schema.ErrSchemaChanged, r.Version, r.SHA256)
		}

		if r.Name != m.Name || r.SHA256 != m.SHA256 {
			return fmt.Errorf("%w: the database records migration %s with SHA-256 %s, and this binary has %s "+
				"with SHA-256 %s: start the server on an empty database",
				schema.ErrSchemaChanged, r.Name, r.SHA256, m.Name, m.SHA256)
		}
	}

	return nil
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
// schema.ErrSchemaDiverged. A database that records an embedded version
// under another name or another SHA-256 fails with schema.ErrSchemaChanged.
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

	if err := checkRecordedSums(recorded, embedded); err != nil {
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

	for i := 1; i < len(migrations); i++ {
		if migrations[i].Version == migrations[i-1].Version {
			return nil, fmt.Errorf(
				"%w: %s and %s: give one of them a new version",
				ErrDuplicateVersion, migrations[i-1].Name, migrations[i].Name,
			)
		}
	}

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

	return Migration{
		Name:             name,
		SQL:              string(data),
		MinBinaryVersion: minimum,
		SHA256:           sum(string(data)),
		Version:          version,
	}, nil
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

// CheckInstances refuses a pending migration that sets a minimum binary
// version while an instance row is below that minimum. It returns an
// *schema.InstancesBelowMinimumError for the first such migration, in the
// order of pending, and nil when no migration sets a minimum or no row is
// below it. The rows are not a liveness lease: a row of an instance that
// crashed blocks the migration until an administrator deletes it. A row whose
// version does not parse counts as below, so the check fails closed. A dev
// build row passes, as a dev build passes every minimum.
func CheckInstances(pending []Migration, rows []schema.InstanceRow) error {
	for _, m := range pending {
		if m.MinBinaryVersion == "" {
			continue
		}

		var below []schema.InstanceRow

		for _, row := range rows {
			if schema.CheckMinimum(m.MinBinaryVersion, row.BinaryVersion) != nil {
				below = append(below, row)
			}
		}

		if len(below) > 0 {
			return &schema.InstancesBelowMinimumError{Migration: m.Name, Minimum: m.MinBinaryVersion, Instances: below}
		}
	}

	return nil
}
