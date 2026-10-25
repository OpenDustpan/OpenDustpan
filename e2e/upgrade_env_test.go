//go:build e2e && upgrade

package e2e_test

import (
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// TestNewEnvWithBinaryCreatesNamespace starts the current build through
// NewEnvWithBinary. A create flag that the build refuses fails this test now,
// and not at the first release, whose binary the upgrade test starts the same
// way.
func TestNewEnvWithBinaryCreatesNamespace(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	bin := e2e.BuildBinaryWithVersion(ctx, t, t.TempDir(), "dev")
	env := e2e.NewEnvWithBinary(ctx, t, e2e.NoCLI(), e2e.StoreSQLite, bin)

	// A 200 for the grants of the namespace shows that the server runs and
	// that the namespace exists.
	status, body, err := env.ServerAPIStatus(
		ctx, http.MethodGet, "/api/server/namespaces/"+e2e.Namespace+"/grants", "",
	)
	if err != nil {
		t.Fatalf("GET grants of %s: %v", e2e.Namespace, err)
	}

	if status != http.StatusOK {
		t.Fatalf("GET grants of %s: status %d, want %d: %s", e2e.Namespace, status, http.StatusOK, body)
	}
}
