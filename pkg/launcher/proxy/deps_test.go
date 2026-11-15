package proxy_test

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// TestLinkedDependencies checks the packages that pkg/launcher/proxy links.
// The runner module links this package, so it must never pull in a SQL
// driver, the forge or internal code. Of this module it links only itself,
// the job proxy rules and the outbound guard. Defense in depth: the depguard
// rule for this package also denies these imports; this test stays so that a
// transitive import is caught even when the rule is changed.
func TestLinkedDependencies(t *testing.T) {
	t.Parallel()

	forbidden := []string{
		"database/sql",
		"github.com/jackc/pgx",
		"modernc.org/sqlite",
		"github.com/mattn/go-sqlite3",
		"github.com/lib/pq",
		"opendustpan.org/opendustpan/internal",
		"opendustpan.org/opendustpan/pkg/forge",
	}

	allowedOwn := []string{
		"opendustpan.org/opendustpan/pkg/launcher/proxy",
		"opendustpan.org/opendustpan/pkg/jobproxy",
		"opendustpan.org/opendustpan/pkg/egress",
	}

	cmd := exec.CommandContext(
		t.Context(), "go", "list", "-deps", "opendustpan.org/opendustpan/pkg/launcher/proxy",
	)

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}

	for dep := range strings.FieldsSeq(string(out)) {
		for _, prefix := range forbidden {
			if dep == prefix || strings.HasPrefix(dep, prefix+"/") {
				t.Errorf("pkg/launcher/proxy links %q, which it must not link", dep)
			}
		}

		if strings.HasPrefix(dep, "opendustpan.org/opendustpan/") && !slices.Contains(allowedOwn, dep) {
			t.Errorf("pkg/launcher/proxy links %q; of this module it links only %v", dep, allowedOwn)
		}
	}
}
