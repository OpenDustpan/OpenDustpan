package pulumi

import (
	"fmt"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/pkg/stack"
)

// TestClassifyUpdateAnswers503OnJournalBuildTimeout checks that the journal
// build timeout maps to 503, so the caller does not get an internal error.
func TestClassifyUpdateAnswers503OnJournalBuildTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{name: "direct", err: stack.ErrJournalBuildTimeout},
		{name: "wrapped", err: fmt.Errorf("build: %w", stack.ErrJournalBuildTimeout)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if status, _ := classifyUpdate(tc.err); status != http.StatusServiceUnavailable {
				t.Fatalf("classifyUpdate answers %d, want %d", status, http.StatusServiceUnavailable)
			}

			if status, _ := classify(tc.err); status != http.StatusServiceUnavailable {
				t.Fatalf("classify answers %d, want %d", status, http.StatusServiceUnavailable)
			}
		})
	}
}
