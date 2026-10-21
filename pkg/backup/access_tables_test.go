package backup_test

import (
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

// TestGitHostInstallationsIsAnAccessTable proves that only a restore with
// server:restore-access can change the map from an installation to a
// namespace: a restore without it keeps the live rows of every access table.
func TestGitHostInstallationsIsAnAccessTable(t *testing.T) {
	t.Parallel()

	for _, table := range []string{backup.TableGitHostInstallations, backup.TableGitHostAppRegistrations} {
		if !backup.IsAccessTable(table) {
			t.Errorf("IsAccessTable(%q) = false, want true", table)
		}

		if !slices.Contains(backup.Tables, table) {
			t.Errorf("Tables does not hold %q", table)
		}
	}

	if backup.IsAccessTable("git_host_connections") {
		t.Error("git_host_connections is an access table, want a table of the namespace data")
	}
}
