package blocklist_test

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/localuser/blocklist"
	"opendustpan.org/opendustpan/pkg/localuser/localusertest"
)

func TestBlocklistConformance(t *testing.T) {
	t.Parallel()

	localusertest.RunBlocklist(t, func(t *testing.T) localuser.Blocklist {
		t.Helper()

		return blocklist.Embedded()
	})
}

// TestListFile checks the file of the list on disk: the line count and the
// license next to it.
func TestListFile(t *testing.T) {
	t.Parallel()

	root, err := os.OpenRoot(filepath.Join("third_party", "github.com", "danielmiessler", "SecLists"))
	if err != nil {
		t.Fatalf("open the directory of the list: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Errorf("close the directory of the list: %v", closeErr)
		}
	})

	raw, err := root.ReadFile("xato-net-10-million-passwords-100000.txt")
	if err != nil {
		t.Fatalf("read the list: %v", err)
	}

	if n := strings.Count(string(raw), "\n"); n != blocklist.ListLines {
		t.Fatalf("the list has %d lines, want %d", n, blocklist.ListLines)
	}

	license, err := root.ReadFile("LICENSE")
	if err != nil {
		t.Fatalf("read the license of the list: %v", err)
	}

	if !strings.HasPrefix(string(license), "MIT License") {
		t.Fatal("the license of the list is not the MIT license")
	}
}

// TestNoNetworkImport checks that the package imports no network package, so
// that a check cannot call a service outside the installation.
func TestNoNetworkImport(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list the files: %v", err)
	}

	fset := token.NewFileSet()

	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}

		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}

		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("import of %s: %v", name, err)
			}

			if path == "net" || strings.HasPrefix(path, "net/") || path == "os/exec" {
				t.Errorf("%s imports %s", name, path)
			}
		}
	}
}

// TestEndedContext checks that a check with an ended context fails and does
// not answer "not blocked".
func TestEndedContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	got, err := blocklist.Embedded().Blocked(ctx, "password")
	if !errors.Is(err, context.Canceled) || got {
		t.Fatalf("blocked with an ended context = %t, %v; want false, context.Canceled", got, err)
	}
}
