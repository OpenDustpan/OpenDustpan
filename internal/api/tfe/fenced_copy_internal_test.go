package tfe

import (
	"fmt"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestWriteErrorFencedCopy proves that a fenced write answers the sentence of
// the sentinel and nothing else, also when code wraps the sentinel: the
// context of the wrap stays in the log.
func TestWriteErrorFencedCopy(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
	}{
		{name: "sentinel", err: namespace.ErrFenced},
		{name: "wrapped", err: fmt.Errorf("record resource changes: %w", namespace.ErrFenced)},
		{name: "wrapped twice", err: fmt.Errorf("update: %w", fmt.Errorf("append: %w", namespace.ErrFenced))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := writeErrorOf(tc.err)

			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
			}

			want := fmt.Sprintf(
				"{\"errors\":[{\"status\":\"409\",\"title\":%q,\"detail\":%q}]}\n",
				titleConflict,
				namespace.ErrFenced.Error(),
			)
			if got := rec.Body.String(); got != want {
				t.Fatalf("body = %q, want %q", got, want)
			}
		})
	}
}
