package backup

import (
	"errors"
	"slices"
)

// Access table names. A restore replaces these tables only when the caller
// holds server:restore-access. The constants give every list one source:
// AccessTables, Tables and the portable spec share them.
const (
	TablePrincipals      = "principals"
	TablePrincipalGroups = "principal_groups"
	TableGroupMembers    = "group_members"
	TableAccessTokens    = "access_tokens"
	TableRoles           = "roles"
	TableGrants          = "grants"
)

// AccessTables lists the tables that a restore replaces only when the
// caller holds server:restore-access. A restore by any other caller keeps
// the current rows of these tables and replaces the other tables.
var AccessTables = []string{ //nolint:gochecknoglobals // Shared frozen list, like Tables.
	TablePrincipals,
	TablePrincipalGroups,
	TableGroupMembers,
	TableAccessTokens,
	TableRoles,
	TableGrants,
}

// ErrRestoreAccessMismatch reports that the backup names a principal,
// group, unit or namespace that the kept access tables and the restored
// data do not hold. A site administrator can restore the access tables.
var ErrRestoreAccessMismatch = errors.New(
	"restore backup names access data that the current access tables do not hold: " +
		"a site administrator can restore the access tables",
)

// IsAccessTable reports whether table is one of AccessTables.
func IsAccessTable(table string) bool {
	return slices.Contains(AccessTables, table)
}
