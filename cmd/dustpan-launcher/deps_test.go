package main

import (
	"slices"
	"strings"
	"testing"
)

// goList returns the packages that pkg links, built with no cgo.
func goList(t *testing.T, pkg string) []string {
	t.Helper()

	cmd := goCommand(t, "list", "-deps", pkg)
	cmd.Env = append(cmd.Env, "CGO_ENABLED=0", "GOOS=linux")

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps %s: %v", pkg, err)
	}

	return strings.Fields(string(out))
}

// TestLinkedDependencies checks what the launcher binary links: no SQL
// driver, no forge, no cgo, and no internal package that the launcher
// contract does not already link. The launcher is the only code of the
// project in the place of a step, so its dependencies are part of the
// attack surface of each job.
func TestLinkedDependencies(t *testing.T) {
	t.Parallel()

	const module = "opendustpan.org/opendustpan/"

	forbidden := []string{
		"database/sql", "github.com/jackc/pgx", "modernc.org/sqlite", "github.com/mattn/go-sqlite3",
		"github.com/lib/pq", module + "pkg/forge", module + "pkg/store", module + "internal/api", "runtime/cgo",
		module + "pkg/dustpan",
	}

	contract := goList(t, module+"pkg/launcher")

	for _, dep := range goList(t, module+"cmd/dustpan-launcher") {
		for _, prefix := range forbidden {
			if dep == prefix || strings.HasPrefix(dep, prefix+"/") {
				t.Errorf("the launcher links %q, which it must not link", dep)
			}
		}

		if strings.HasPrefix(dep, module+"internal/") && !slices.Contains(contract, dep) {
			t.Errorf("the launcher links %q, an internal package that the launcher contract does not link", dep)
		}
	}
}
