package objectcontext

import (
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/objectcontext"
)

// TestEveryClassHasACode checks that each error that this API classifies
// answers with a valid stable code.
func TestEveryClassHasACode(t *testing.T) {
	t.Parallel()

	errs := []error{
		errNotFound, namespace.ErrNotFound, objectcontext.ErrInvalidObject, objectcontext.ErrNotFound, errBody,
		errInclude, objectcontext.ErrInvalidContext, objectcontext.ErrUnknownSection,
		objectcontext.ErrFieldFromCode, objectcontext.ErrUnitDeleted,
	}

	for _, err := range errs {
		p, ok := classify(fmt.Errorf("wrapped: %w", err))
		if !ok || !problem.ValidCode(p.Code) {
			t.Errorf("%v: classified %t with code %q, want a valid code", err, ok, p.Code)
		}
	}
}
