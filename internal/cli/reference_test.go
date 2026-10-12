package cli_test

import (
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"opendustpan.org/opendustpan/internal/cli"
)

// updateReference rewrites the settings reference from the declarations.
// The drift test compares the file without it; make generate sets it.
//
//nolint:gochecknoglobals // A test flag is read once; passing it as a parameter adds nothing.
var updateReference = flag.Bool("update", false, "rewrite docs/reference/configuration.md from the declarations")

// repoRoot answers the repository root from the location of this test file.
func repoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller holds no file")
	}

	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// TestConfigurationReference proves docs/reference/configuration.md holds
// exactly what the setting declarations generate. Run with -update (also in
// make generate) to rewrite it after a declaration change.
func TestConfigurationReference(t *testing.T) {
	t.Parallel()

	path := filepath.Join(repoRoot(t), "docs", "reference", "configuration.md")
	want := cli.Reference()

	if *updateReference {
		if err := os.WriteFile(path, []byte(want), 0o600); err != nil {
			t.Fatalf("rewrite the settings reference: %v", err)
		}

		return
	}

	//nolint:gosec // The test reads the reference file it generates, at a fixed path under the repo.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the settings reference: %v", err)
	}

	if string(raw) != want {
		t.Fatal("docs/reference/configuration.md differs from the setting declarations: " +
			"rewrite it with go test ./internal/cli/ -run TestConfigurationReference -update")
	}
}
