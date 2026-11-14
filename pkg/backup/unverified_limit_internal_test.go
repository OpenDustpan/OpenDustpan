package backup

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestRefuseUnverifiedIsTheSentinel proves the identity of the refusal: a
// count over the bound matches ErrTooManyUnverified and holds the count and
// the bound, also when the caller wraps it, and the count at the bound is
// accepted. The failed row of a backup holds the text alone, so the check is
// here.
func TestRefuseUnverifiedIsTheSentinel(t *testing.T) {
	t.Parallel()

	if err := refuseUnverified([]string{"a", "b", "c"}, 3); err != nil {
		t.Fatalf("refuse at the bound = %v, want nil", err)
	}

	err := refuseUnverified([]string{"a", "b", "c", "d"}, 3)
	if err == nil {
		t.Fatal("refuse above the bound = nil, want ErrTooManyUnverified")
	}

	if !errors.Is(err, ErrTooManyUnverified) {
		t.Fatalf("refuse above the bound = %v, want ErrTooManyUnverified", err)
	}

	wrapped := fmt.Errorf("run backup bkp-a: %w", err)

	var refused *TooManyUnverifiedError
	if !errors.As(wrapped, &refused) {
		t.Fatalf("wrapped refusal = %v, want *TooManyUnverifiedError", wrapped)
	}

	if refused.Count != 4 || refused.Limit != 3 {
		t.Fatalf("refusal holds count %d and limit %d, want 4 and 3", refused.Count, refused.Limit)
	}

	for _, want := range []string{"4 blobs without a size and an MD5", "more than the limit of 3"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal text = %q, want %q", err.Error(), want)
		}
	}
}
