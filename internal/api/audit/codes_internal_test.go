package audit

import (
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestEveryClassHasACode checks that each error that this API classifies
// answers with a code of the catalog.
func TestEveryClassHasACode(t *testing.T) {
	t.Parallel()

	errs := []error{
		errMissingOrganization, errInvalidPage, errInvalidSequence, identity.ErrInvalidObject,
		namespace.ErrNotFound, audit.ErrEntryNotFound, errNoEntryReader,
	}

	for _, err := range errs {
		p, ok := requestErrors(fmt.Errorf("wrapped: %w", err))
		if !ok || !codeInCatalog(p.Code) {
			t.Errorf("%v: classified %t with code %q, want a code of the catalog", err, ok, p.Code)
		}
	}
}

// codeInCatalog reports whether code is in the catalog.
func codeInCatalog(code string) bool {
	_, ok := errcode.Lookup(code)

	return ok
}
