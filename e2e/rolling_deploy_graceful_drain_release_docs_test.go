//go:build e2e

package e2e_test

import (
	"os"
	"strings"
	"testing"
)

// TestSeveralInstancesPageStatesTheDrain reads the several-instances guide as
// text and checks the statements that this change adds: the drain time in the
// header of the draining probes, the refusal of a new durable start with its
// code, and the rule for the timeout of a proxy. It names no route and no
// credential.
func TestSeveralInstancesPageStatesTheDrain(t *testing.T) {
	t.Parallel()

	root, err := os.OpenRoot("..")
	if err != nil {
		t.Fatalf("open the repository: %v", err)
	}

	raw, readErr := root.ReadFile("docs/guides/several-instances.md")

	if closeErr := root.Close(); readErr != nil || closeErr != nil {
		t.Fatalf("read the several-instances guide: %v %v", readErr, closeErr)
	}

	page := string(raw)

	for _, want := range []string{
		"The answer carries the header `Retry-After` with the time left of the drain in whole seconds",
		"during the drain answers 503 with `Retry-After` and the error code `instance.draining`",
		"Send the request again to another instance after the time in `Retry-After`.",
		"The drain and the wait together are at most 40 seconds.",
		"above the drain of 10 seconds",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the several-instances guide lacks %q", want)
		}
	}
}
