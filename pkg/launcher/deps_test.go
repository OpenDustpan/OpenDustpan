package launcher_test

import (
	"os/exec"
	"slices"
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

// linkedInternalPackages returns the sorted set of internal packages that
// pkg/launcher links. The binary cmd/dustpan-launcher imports pkg/launcher
// and links more packages, which this list does not hold.
func linkedInternalPackages() []string {
	return []string{
		"opendustpan.org/opendustpan/internal/ident",
		"opendustpan.org/opendustpan/internal/scopeaudit",
		"opendustpan.org/opendustpan/internal/token",
		"opendustpan.org/opendustpan/pkg/audit",
		"opendustpan.org/opendustpan/pkg/backup/blobguard",
		"opendustpan.org/opendustpan/pkg/blob",
		"opendustpan.org/opendustpan/pkg/collection",
		"opendustpan.org/opendustpan/pkg/durable",
		"opendustpan.org/opendustpan/pkg/egress",
		"opendustpan.org/opendustpan/pkg/gate",
		"opendustpan.org/opendustpan/pkg/history",
		"opendustpan.org/opendustpan/pkg/identity",
		"opendustpan.org/opendustpan/pkg/inventory",
		"opendustpan.org/opendustpan/pkg/job",
		"opendustpan.org/opendustpan/pkg/launcher",
		"opendustpan.org/opendustpan/pkg/logsink",
		"opendustpan.org/opendustpan/pkg/maintenance",
		"opendustpan.org/opendustpan/pkg/manuallock",
		"opendustpan.org/opendustpan/pkg/namespace",
		"opendustpan.org/opendustpan/pkg/outputshare",
		"opendustpan.org/opendustpan/pkg/runner",
		"opendustpan.org/opendustpan/pkg/secret",
		"opendustpan.org/opendustpan/pkg/source",
		"opendustpan.org/opendustpan/pkg/stack",
		"opendustpan.org/opendustpan/pkg/workspace",
	}
}

// TestLinkedInternalPackages checks the internal packages that pkg/launcher
// links against linkedInternalPackages. The check does not cover
// cmd/dustpan-launcher. A new import of pkg/launcher must add its path to the
// list in the same change.
func TestLinkedInternalPackages(t *testing.T) {
	t.Parallel()

	cmd := exec.CommandContext(
		t.Context(), "go", "list", "-deps", "opendustpan.org/opendustpan/pkg/launcher",
	)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}

	const ownPrefix = "opendustpan.org/opendustpan/"

	var deps []string

	for dep := range strings.FieldsSeq(string(out)) {
		if strings.HasPrefix(dep, ownPrefix) {
			deps = append(deps, dep)
		}
	}

	for _, dep := range unlistedDeps(deps, linkedInternalPackages()) {
		t.Errorf("pkg/launcher links %q, which is not in the allowlist", dep)
	}
}

// unlistedDeps returns the deps that allowed does not hold, in order.
func unlistedDeps(deps, allowed []string) []string {
	var unlisted []string

	for _, dep := range deps {
		if !slices.Contains(allowed, dep) {
			unlisted = append(unlisted, dep)
		}
	}

	return unlisted
}

func TestUnlistedDeps(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		deps    []string
		allowed []string
		want    []string
	}{
		{"all listed", []string{"a", "b"}, []string{"a", "b"}, nil},
		{"one not listed", []string{"a", "b"}, []string{"a"}, []string{"b"}},
		{"empty input", nil, nil, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := unlistedDeps(tc.deps, tc.allowed)
			if !slices.Equal(got, tc.want) {
				t.Errorf("unlistedDeps(%v, %v) = %v, want %v", tc.deps, tc.allowed, got, tc.want)
			}
		})
	}
}
