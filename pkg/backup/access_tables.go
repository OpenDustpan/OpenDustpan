package backup

import "errors"

// AccessTables lists the tables that a restore replaces only when the
// caller holds server:restore-access. A restore by any other caller keeps
// the current rows of these tables and replaces the other tables.
var AccessTables = []string{ //nolint:gochecknoglobals // Shared frozen list, like Tables.
	"principals",
	"principal_groups",
	"group_members",
	"access_tokens",
	"roles",
	"grants",
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
	for _, name := range AccessTables {
		if name == table {
			return true
		}
	}

	return false
}
