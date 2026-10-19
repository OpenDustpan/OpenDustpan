//go:build e2e

package e2e_test

import (
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// TestProfilesAPI drives the profile routes on a server of each store. The
// routes belong to no tool, so this test needs no CLI binary; the scenarios
// of the two tools run the same steps beside their own.
func TestProfilesAPI(t *testing.T) {
	t.Parallel()

	stores, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, store := range stores {
		t.Run(string(store), func(t *testing.T) {
			t.Parallel()

			env := e2e.NewEnv(t.Context(), t, e2e.NoCLI(), store)

			profileScenario(t, env, "api")
		})
	}
}
