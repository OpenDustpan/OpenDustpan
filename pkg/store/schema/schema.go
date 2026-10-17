// Package schema reports the schema versions of the database and the
// binary. It holds the minimum binary version rule shared by every store
// adapter: a binary refuses a schema only when the minimum binary version
// that the schema records is above its own version.
//
// It lives here, not in store/internal/migrate, because both store adapters
// and cmd/opendustpan must import it: a package under store/internal can only
// be imported from inside store, so the command could not reach it. This
// package imports only the standard library, so no import cycle is possible.
// The version comparison itself stays in store/internal/migrate with the
// migrations it checks.
package schema

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// ErrBinaryBelowMinimum reports a binary whose version is below the minimum
// binary version that the database schema records.
var ErrBinaryBelowMinimum = errors.New("binary version is below the minimum of the database schema")

// ErrBadMinimum reports a minimum binary version that is not of the form
// vMAJOR.MINOR.PATCH.
var ErrBadMinimum = errors.New("minimum binary version must have the form vMAJOR.MINOR.PATCH")

// ErrSchemaDiverged reports a database that records migrations the binary
// does not embed and lacks migrations the binary embeds.
var ErrSchemaDiverged = errors.New("database schema and binary have diverged")

// MinimumError reports the minimum binary version of the database schema and
// the version of the binary that is below it. The adapters return it.
type MinimumError struct {
	// SchemaMinimum is the minimum binary version the database schema records.
	SchemaMinimum string
	// BinaryVersion is the version of the binary.
	BinaryVersion string
}

// Error names both versions and the one action: start a binary at or above
// the minimum, or restore a backup.
func (e *MinimumError) Error() string {
	return fmt.Sprintf(
		"binary version %s is below %s, the minimum binary version of the database schema: "+
			"start a binary of version %s or later, or restore a backup",
		e.BinaryVersion,
		e.SchemaMinimum,
		e.SchemaMinimum,
	)
}

// Unwrap returns ErrBinaryBelowMinimum, so errors.Is finds the sentinel.
func (e *MinimumError) Unwrap() error {
	return ErrBinaryBelowMinimum
}

// ErrBadBinaryVersion reports a binary version that is not a release
// version, "dev", an empty version or a commit hash. A store refuses to open
// with it, so that a stamp that does not parse cannot skip the minimum.
var ErrBadBinaryVersion = errors.New(
	"binary version must be MAJOR.MINOR.PATCH with an optional v, dev or a commit hash",
)

// version is a parsed release version: MAJOR.MINOR.PATCH with the
// pre-release identifiers, if any.
type version struct {
	major, minor, patch int
	pre                 []string
}

// commitHash matches the version of a build from a commit with no tag, as
// `git describe --always --dirty` writes it.
var commitHash = regexp.MustCompile(`^[0-9a-f]{7,40}(-dirty)?$`)

// describeSuffix matches the suffix that `git describe` adds to a tag:
// the commit count and the hash, and -dirty for local changes.
var describeSuffix = regexp.MustCompile(`^(\d+-g[0-9a-f]+)?(-?dirty)?$`)

// preIdentifier matches one pre-release identifier of semver.
var preIdentifier = regexp.MustCompile(`^[0-9A-Za-z-]+$`)

// isDev reports whether raw names a build with no release version: "dev",
// an empty version or a commit hash.
func isDev(raw string) bool {
	s := strings.TrimSpace(raw)

	return s == "" || s == "dev" || commitHash.MatchString(s)
}

// parseVersion reads a release version: MAJOR.MINOR.PATCH with an optional
// "v" or "V", an optional pre-release after "-" and optional build metadata
// after "+", which it ignores. A `git describe` suffix such as -3-gabc1234
// or -dirty is not a pre-release: the build counts as its tag.
func parseVersion(raw string) (version, bool) {
	var none version

	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")

	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}

	core, suffix, hasSuffix := strings.Cut(s, "-")

	parts := strings.Split(core, ".")
	if len(parts) != 3 { //nolint:mnd // MAJOR, MINOR and PATCH.
		return none, false
	}

	numbers := make([]int, 0, len(parts))

	for _, part := range parts {
		n, ok := parseNumber(part)
		if !ok {
			return none, false
		}

		numbers = append(numbers, n)
	}

	v := version{major: numbers[0], minor: numbers[1], patch: numbers[2], pre: nil}

	if !hasSuffix {
		return v, true
	}

	pre, ok := parsePreRelease(suffix)
	if !ok {
		return none, false
	}

	v.pre = pre

	return v, true
}

// parsePreRelease reads the suffix after the first "-" of a version. A
// `git describe` suffix is no pre-release and answers nil. An empty suffix,
// an empty identifier or a numeric identifier with a leading zero fails.
func parsePreRelease(suffix string) ([]string, bool) {
	if suffix == "" {
		return nil, false
	}

	if describeSuffix.MatchString(suffix) {
		return nil, true
	}

	var pre []string

	for id := range strings.SplitSeq(suffix, ".") {
		if !preIdentifier.MatchString(id) || (isNumeric(id) && len(id) > 1 && id[0] == '0') {
			return nil, false
		}

		pre = append(pre, id)
	}

	return pre, true
}

// parseNumber reads one decimal component with no sign and no leading zero.
func parseNumber(part string) (int, bool) {
	if part == "" || !isNumeric(part) || (len(part) > 1 && part[0] == '0') {
		return 0, false
	}

	n, err := strconv.Atoi(part)
	if err != nil {
		return 0, false
	}

	return n, true
}

// isNumeric reports whether s holds only the digits 0 to 9.
func isNumeric(s string) bool {
	for _, c := range []byte(s) {
		if c < '0' || c > '9' {
			return false
		}
	}

	return s != ""
}

// compare returns -1, 0 or 1 when v is below, equal to or above other, by
// the precedence of semver for the cases that occur: a minimum is always a
// release, so a pre-release is below its release, and two pre-releases of
// one release compare as equal.
func (v version) compare(other version) int {
	for _, c := range [][2]int{{v.major, other.major}, {v.minor, other.minor}, {v.patch, other.patch}} {
		if c[0] != c[1] {
			return cmpInt(c[0], c[1])
		}
	}

	switch {
	case len(v.pre) == len(other.pre), len(v.pre) > 0 && len(other.pre) > 0:
		return 0
	case len(v.pre) > 0:
		return -1
	default:
		return 1
	}
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// IsDevBuild reports whether version names a build with no release
// version: "dev", an empty version or a commit hash. A dev build counts as
// newer than every release, so no minimum refuses it.
func IsDevBuild(version string) bool {
	return isDev(version)
}

// CheckBinaryVersion returns ErrBadBinaryVersion when version is neither a
// dev build nor a release version.
func CheckBinaryVersion(version string) error {
	if isDev(version) {
		return nil
	}

	if _, ok := parseVersion(version); !ok {
		return fmt.Errorf("%w: got %q", ErrBadBinaryVersion, version)
	}

	return nil
}

// ValidMinimum returns ErrBadMinimum when v is not exactly vMAJOR.MINOR.PATCH.
// Older binaries read every recorded minimum with this rule, so the format
// of a minimum must not change.
func ValidMinimum(v string) error {
	parsed, ok := parseVersion(v)
	if !ok || len(parsed.pre) > 0 || fmt.Sprintf("v%d.%d.%d", parsed.major, parsed.minor, parsed.patch) != v {
		return fmt.Errorf("%w: %q", ErrBadMinimum, v)
	}

	return nil
}

// HighestMinimum returns the highest of the recorded minimum binary versions,
// or "" when none is set. An empty value sets no minimum.
func HighestMinimum(values []string) (string, error) {
	highest := ""

	var top version

	for _, v := range values {
		if v == "" {
			continue
		}

		if err := ValidMinimum(v); err != nil {
			return "", err
		}

		parsed, _ := parseVersion(v)
		if highest == "" || parsed.compare(top) > 0 {
			highest, top = v, parsed
		}
	}

	return highest, nil
}

// CheckMinimum returns a *MinimumError when binaryVersion is below minimum.
// A dev build passes, and so does an empty minimum. A binary version that is
// neither a dev build nor a release version fails with ErrBadBinaryVersion,
// also with no minimum. A minimum that is not vMAJOR.MINOR.PATCH fails with
// ErrBadMinimum.
func CheckMinimum(minimum, binaryVersion string) error {
	if err := CheckBinaryVersion(binaryVersion); err != nil {
		return err
	}

	if minimum == "" || isDev(binaryVersion) {
		return nil
	}

	if err := ValidMinimum(minimum); err != nil {
		return err
	}

	binary, _ := parseVersion(binaryVersion)
	floor, _ := parseVersion(minimum)

	if binary.compare(floor) < 0 {
		return &MinimumError{SchemaMinimum: minimum, BinaryVersion: binaryVersion}
	}

	return nil
}

// BaselineVersion is the version of the baseline migration. The baseline
// replaced every migration before it, so a database that records a version
// below it is from before the baseline, and no binary can upgrade it.
const BaselineVersion int64 = 100

// BaselineName is the file name of the baseline migration.
const BaselineName = "0100_baseline.sql"

// ErrSchemaBeforeBaseline reports a database that records a migration
// version from before the baseline.
var ErrSchemaBeforeBaseline = errors.New("database schema is older than the baseline schema of the binary")

// BeforeBaselineError reports the schema version of a database from before
// the baseline and the baseline version. The adapters return it.
type BeforeBaselineError struct {
	// DatabaseVersion is the newest version below the baseline that the
	// database records.
	DatabaseVersion int64
	// BaselineVersion is the version of the baseline migration.
	BaselineVersion int64
}

// Error describes the refusal and the one action: start on an empty
// database.
func (e *BeforeBaselineError) Error() string {
	return fmt.Sprintf(
		"database schema version %d is older than baseline schema version %d: "+
			"no binary can upgrade this database. Start the server on an empty database",
		e.DatabaseVersion,
		e.BaselineVersion,
	)
}

// Unwrap returns ErrSchemaBeforeBaseline, so errors.Is finds the sentinel.
func (e *BeforeBaselineError) Unwrap() error {
	return ErrSchemaBeforeBaseline
}

// CheckBaseline returns a *BeforeBaselineError when recorded holds a
// version below BaselineVersion, and nil otherwise. An empty database
// records nothing and passes.
func CheckBaseline(recorded []int64) error {
	var newest int64

	for _, v := range recorded {
		if v < BaselineVersion && v > newest {
			newest = v
		}
	}

	if newest == 0 {
		return nil
	}

	return &BeforeBaselineError{DatabaseVersion: newest, BaselineVersion: BaselineVersion}
}

// DatabaseVersion returns the newest version the database records, or zero
// when it records none.
func DatabaseVersion(recorded []int64) int64 {
	var newest int64

	for _, v := range recorded {
		if v > newest {
			newest = v
		}
	}

	return newest
}

// BinaryVersion returns the newest migration the binary embeds, or zero when
// it embeds none.
func BinaryVersion(embedded []int64) int64 {
	var newest int64

	for _, v := range embedded {
		if v > newest {
			newest = v
		}
	}

	return newest
}

// AppliedMigration is one row of schema_migrations: the version and the
// file name of a migration the database records, ordered by version. The
// installation API answers them, so a client compares them with the
// migrations in its own binary without opening the database.
type AppliedMigration struct {
	// Version is the migration version the database records.
	Version int64
	// Name is the migration file name, for example 0001_init.sql.
	Name string
	// MinBinaryVersion is the minimum binary version that the migration
	// sets, or "" when it sets none.
	MinBinaryVersion string
}

// ErrBadMigrationName reports a migration file not named
// NNNN_description.sql.
var ErrBadMigrationName = errors.New("migration file must be named NNNN_description.sql")

// ParseMigrationVersion returns the version prefix of a migration file name
// of the form NNNN_description.sql.
func ParseMigrationVersion(name string) (int64, error) {
	prefix, _, ok := strings.Cut(name, "_")
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrBadMigrationName, name)
	}

	version, err := strconv.ParseInt(prefix, 10, 64)
	if err != nil || version <= 0 {
		return 0, fmt.Errorf("%w: %s", ErrBadMigrationName, name)
	}

	return version, nil
}

// embeddedMigrationNames is the single ordered list of migration file names
// every dialect embeds. Both store adapters embed the same names with their
// own SQL, and the upgrade check client reads this list without importing a
// store adapter (ADR 39 decision 4). Adapter tests pin that each embedded
// directory holds exactly these names: add a migration to both dialects and
// to this list together.
//
//nolint:gochecknoglobals // The shared frozen migration list: Go has no constant slices, and both dialects use this one source.
var embeddedMigrationNames = []string{
	BaselineName,
}

// EmbeddedMigrationNames returns the ordered migration file names the binary
// embeds with the newest version. Both adapters and the upgrade check client
// share it, so the verdict never depends on which dialect the server runs.
func EmbeddedMigrationNames() ([]string, int64) {
	var newest int64

	for _, name := range embeddedMigrationNames {
		version, err := ParseMigrationVersion(name)
		if err != nil {
			continue
		}

		if version > newest {
			newest = version
		}
	}

	return slices.Clone(embeddedMigrationNames), newest
}

// Inspection is the read-only schema state of one database. Inspect in each
// store adapter returns it, and `opendustpan upgrade check` reads it.
type Inspection struct {
	// DatabaseVersion is the newest migration version the database records.
	// It is zero when the database records none.
	DatabaseVersion int64
	// BinaryVersion is the newest migration version the binary embeds.
	BinaryVersion int64
	// Pending holds the names of the embedded migrations the database does
	// not record, ordered by version.
	Pending []string
	// Unknown holds the recorded versions the binary does not embed,
	// ordered. It is empty when the database is equal or older.
	Unknown []int64
	// Minimum is the minimum binary version the database schema records, or
	// "" when it records none.
	Minimum string
}
