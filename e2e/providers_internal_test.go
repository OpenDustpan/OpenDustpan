//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A harness setup writes the CLI configuration of OpenTofu without a network:
// the mirror stays empty until a test runs init.
func TestTofuRCDoesNotFillMirror(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("E2E_CACHE_DIR", cache)

	rc := tofuRC(t, "tf.example.test", "token-value")

	mirror := filepath.Join(cache, providerMirrorDirName)
	if !strings.Contains(rc, "filesystem_mirror") || !strings.Contains(rc, mirror) {
		t.Fatalf("CLI configuration does not name the mirror %s:\n%s", mirror, rc)
	}

	if _, err := os.Stat(mirror); !os.IsNotExist(err) {
		t.Fatalf("tofuRC touched the mirror %s: %v", mirror, err)
	}
}
