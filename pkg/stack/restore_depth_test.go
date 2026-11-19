package stack_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/stack"
)

// TestRestoreAcceptsAStateDeeperThanTheMaskedReadBound proves that the depth
// bound belongs to the masked readers only. A restore of a stored version that
// nests deeper than MaxStateDepth succeeds and gives back the same bytes, while
// a masked read of the same bytes is refused with ErrStateBound. A restore
// copies the bytes and builds no path, so the bound does not apply to it.
func TestRestoreAcceptsAStateDeeperThanTheMaskedReadBound(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)

	levels := stack.MaxStateDepth + 50
	deep := `{"resources":[{"urn":"u","type":"t","outputs":` +
		strings.Repeat(`{"a":`, levels) + `"v"` + strings.Repeat(`}`, levels) + `}]}`

	finishVersion(t, f, deep)
	finishVersion(t, f, `{"v":2}`)

	if _, err := f.svc.Restore(t.Context(), f.ref, 1, "bob@example.com"); err != nil {
		t.Fatalf("restore of a state deeper than %d: %v", stack.MaxStateDepth, err)
	}

	checkRestoreExport(t, f, deep)

	if _, err := stack.ReadStateResource(t.Context(), []byte(deep), "u"); !errors.Is(err, stack.ErrStateBound) {
		t.Fatalf("masked read of the same state: err = %v, want ErrStateBound", err)
	}
}
