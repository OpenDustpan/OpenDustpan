package audit

import (
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestEveryClassHasACode checks that each error that this API classifies
// answers with a valid stable code.
func TestEveryClassHasACode(t *testing.T) {
	t.Parallel()

	errs := []error{
		errMissingOrganization, errInvalidPage, errInvalidSequence, identity.ErrInvalidObject,
		namespace.ErrNotFound, audit.ErrEntryNotFound, errNoEntryReader,
	}

	for _, err := range errs {
		p, ok := classify(fmt.Errorf("wrapped: %w", err))
		if !ok || !problem.ValidCode(p.Code) {
			t.Errorf("%v: classified %t with code %q, want a valid code", err, ok, p.Code)
		}
	}
}
