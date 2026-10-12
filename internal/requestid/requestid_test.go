package requestid_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/requestid"
)

func TestNewMintDistinctIDs(t *testing.T) {
	t.Parallel()

	if first, second := requestid.New(), requestid.New(); first == "" || first == second {
		t.Fatalf("IDs = %q, %q, want two distinct non-empty IDs", first, second)
	}
}

func TestFromContextRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := requestid.WithContext(t.Context(), "req-1")
	if got := requestid.FromContext(ctx); got != "req-1" {
		t.Fatalf("ID = %q, want req-1", got)
	}

	if got := requestid.FromContext(t.Context()); got != "" {
		t.Fatalf("ID = %q, want empty", got)
	}
}
