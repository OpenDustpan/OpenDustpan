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

// release is the MAJOR.MINOR.PATCH part of a binary version.
type release struct {
	major, minor, patch int
}

// compare returns -1, 0 or 1 when r is below, equal to or above other.
func (r release) compare(other release) int {
	switch {
	case r.major != other.major:
		return cmpInt(r.major, other.major)
	case r.minor != other.minor:
		return cmpInt(r.minor, other.minor)
	default:
		return cmpInt(r.patch, other.patch)
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

// parseRelease reads vMAJOR.MINOR.PATCH at the start of v. A suffix after
// "-" or "+" is ignored, so a `git describe` version such as
// v0.1.0-3-gabc1234-dirty counts as its tag v0.1.0.
func parseRelease(v string) (release, bool) {
	var none release

	rest, ok := strings.CutPrefix(v, "v")
	if !ok {
		return none, false
	}

	if i := strings.IndexAny(rest, "-+"); i >= 0 {
		rest = rest[:i]
	}

	parts := strings.Split(rest, ".")
	if len(parts) != 3 { //nolint:mnd // MAJOR, MINOR and PATCH.
		return none, false
	}

	numbers := make([]int, 0, len(parts))

	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 || part == "" || (len(part) > 1 && part[0] == '0') {
			return none, false
		}

		numbers = append(numbers, n)
	}

	return release{major: numbers[0], minor: numbers[1], patch: numbers[2]}, true
}

// IsDevBuild reports whether version is not a release version: "dev", an
// empty version, a commit hash or any other value that does not start with
// vMAJOR.MINOR.PATCH. A dev build counts as newer than every release, so no
// minimum refuses it.
func IsDevBuild(version string) bool {
	_, ok := parseRelease(version)

	return !ok
}

// ValidMinimum returns ErrBadMinimum when v is not exactly vMAJOR.MINOR.PATCH.
func ValidMinimum(v string) error {
	r, ok := parseRelease(v)
	if !ok || fmt.Sprintf("v%d.%d.%d", r.major, r.minor, r.patch) != v {
		return fmt.Errorf("%w: %q", ErrBadMinimum, v)
	}

	return nil
}

// HighestMinimum returns the highest of the recorded minimum binary versions,
// or "" when none is set. An empty value sets no minimum.
func HighestMinimum(values []string) (string, error) {
	highest := ""

	var top release

	for _, v := range values {
		if v == "" {
			continue
		}

		if err := ValidMinimum(v); err != nil {
			return "", err
		}

		r, _ := parseRelease(v)
		if highest == "" || r.compare(top) > 0 {
			highest, top = v, r
		}
	}

	return highest, nil
}

// CheckMinimum returns a *MinimumError when binaryVersion is below minimum.
// An empty minimum and a dev build pass. A minimum that is not of the form
// vMAJOR.MINOR.PATCH fails with ErrBadMinimum.
func CheckMinimum(minimum, binaryVersion string) error {
	if minimum == "" {
		return nil
	}

	if err := ValidMinimum(minimum); err != nil {
		return err
	}

	binary, ok := parseRelease(binaryVersion)
	if !ok {
		return nil
	}

	floor, _ := parseRelease(minimum)
	if binary.compare(floor) < 0 {
		return &MinimumError{SchemaMinimum: minimum, BinaryVersion: binaryVersion}
	}

	return nil
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
	"0001_init.sql",
	"0002_updates.sql",
	"0003_stack_id.sql",
	"0004_updates_stack_id.sql",
	"0005_updates_drop_names.sql",
	"0006_checkpoints_per_update.sql",
	"0007_stack_tags.sql",
	"0008_secret_keys.sql",
	"0009_inventory.sql",
	"0010_workspaces.sql",
	"0011_restored_from.sql",
	"0012_update_history_fields.sql",
	"0013_backups.sql",
	"0014_checkpoint_resource_count.sql",
	"0015_maintenance.sql",
	"0016_checkpoint_blobs.sql",
	"0017_inventory_lock.sql",
	"0018_restores.sql",
	"0019_audit_entries.sql",
	"0020_sealed_state.sql",
	"0021_sensitive_marks.sql",
	"0022_audit_value_count.sql",
	"0023_update_journal_version.sql",
	"0024_state_fence.sql",
	"0025_update_journal.sql",
	"0026_override_switches.sql",
	"0027_update_journal_md5.sql",
	"0028_principals_and_groups.sql",
	"0029_audit_server_scope.sql",
	"0031_soft_delete_stacks.sql",
	"0032_soft_delete_workspaces.sql",
	"0035_access_tokens.sql",
	"0042_roles_and_grants.sql",
	"0048_audit_changes.sql",
	"0049_audit_sink_cursors.sql",
	"0050_update_token_id.sql",
	"0054_token_policy.sql",
	"0056_audit_object_id_and_blob_purges.sql",
	"0057_namespace_ip_allowlist.sql",
	"0058_inventory_deleted_purge_after.sql",
	"0059_collections.sql",
	"0062_sessions.sql",
	"0064_blob_purges_age_byte_order.sql",
	"0065_session_policy.sql",
	"0066_namespace_principals.sql",
	"0067_audit_chain_state.sql",
	"0068_update_journal_records_changed.sql",
	"0069_audit_sink_leases.sql",
	"0070_membership_source_state.sql",
	"0071_break_glass_accounts.sql",
	"0074_update_journal_refusals.sql",
	"0075_sign_in_freshness.sql",
	"0076_sign_in_providers.sql",
	"0079_schema_minimum.sql",
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
