//go:build e2e

package e2e_test

import (
	"os"
	"strings"
	"testing"
)

// TestDocsPageDescribesTheDrain reads the several-instances guide as text and
// checks that it states the drain, its bounds, the refusal of a new durable
// start with Retry-After, the claim takeover, and the proxy timeouts that
// exceed the drain timeout.
func TestDocsPageDescribesTheDrain(t *testing.T) {
	t.Parallel()

	root, err := os.OpenRoot("..")
	if err != nil {
		t.Fatalf("open the repository: %v", err)
	}

	raw, readErr := root.ReadFile("docs/guides/several-instances.md")
	closeErr := root.Close()

	if readErr != nil || closeErr != nil {
		t.Fatalf("read the guide: %v %v", readErr, closeErr)
	}

	page := flatten(string(raw))

	for _, want := range []string{
		"failed check `draining`",
		"`Retry-After` header with the drain period in whole seconds, from 1 to 15",
		"a backup, a restore, a job, a drift check or a trigger answers 503",
		"error code `instance.draining`",
		"gives back the claim of each durable operation",
		"another `worker` process then claims the operation at its next poll",
		"drain and the wait together are at most 40 seconds",
		"Set the proxy timeouts to more than the drain timeout and the shutdown wait: at least 50 seconds",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the several-instances guide lacks %q", want)
		}
	}
}
