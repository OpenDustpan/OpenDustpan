package launcher_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestLinkedDependencies checks the packages that pkg/launcher links. The
// runner module links this package, so it must never pull in a SQL driver
// or the forge. Defense in depth: the depguard rule for this package also
// denies these imports; this test stays so that a transitive import is
// caught even when the rule is changed.
func TestLinkedDependencies(t *testing.T) {
	t.Parallel()

	forbidden := []string{
		"database/sql",
		"github.com/jackc/pgx",
		"modernc.org/sqlite",
		"github.com/mattn/go-sqlite3",
		"github.com/lib/pq",
		"opendustpan.org/opendustpan/pkg/forge",
	}

	cmd := exec.CommandContext(
		t.Context(), "go", "list", "-deps", "opendustpan.org/opendustpan/pkg/launcher",
	)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}

	for dep := range strings.FieldsSeq(string(out)) {
		for _, prefix := range forbidden {
			if dep == prefix || strings.HasPrefix(dep, prefix+"/") {
				t.Errorf("pkg/launcher links %q, which it must not link", dep)
			}
		}
	}
}
