package objectcontext

import (
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/objectcontext"
)

// TestEveryClassHasACode checks that each error that this API classifies
// answers with a code of the catalog.
func TestEveryClassHasACode(t *testing.T) {
	t.Parallel()

	errs := []error{
		errNotFound, namespace.ErrNotFound, objectcontext.ErrInvalidObject, objectcontext.ErrNotFound, errBody,
		errInclude, objectcontext.ErrInvalidContext, objectcontext.ErrUnknownSection,
		objectcontext.ErrFieldFromCode, objectcontext.ErrUnitDeleted,
	}

	for _, err := range errs {
		p, ok := contextErrors(fmt.Errorf("wrapped: %w", err))
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
