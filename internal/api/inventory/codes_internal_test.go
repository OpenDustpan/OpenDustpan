package inventory

import (
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/savedview"
)

// TestEveryClassHasACode checks that each error that this API classifies
// answers with a valid stable code.
func TestEveryClassHasACode(t *testing.T) {
	t.Parallel()

	errs := []error{
		errMissingOrganization, identity.ErrInvalidObject, namespace.ErrNotFound, collection.ErrInvalidName,
		coreinventory.ErrUnitNotFound, collection.ErrNotFound, collection.ErrUnitNotFound,
		collection.ErrAlreadyExists, collection.ErrNotEmpty, collection.ErrCollectionChanged,
		collection.ErrNoCollection, collection.ErrMoveOutOfLockedCollection, errMissingUnit, errCollectionBody,
		errMoveBody, errInvalidQuery, errInvalidBody, errNoHistory, coreinventory.ErrInvalidName,
		coreinventory.ErrNotFound, coreinventory.ErrNameTaken, savedview.ErrNotPerson, savedview.ErrNotFound,
		savedview.ErrInvalid, errViewBody, errViewIdempotencyKey, savedview.ErrExists, savedview.ErrLimit,
		manuallock.ErrTargetNotFound, manuallock.ErrNotLocked, manuallock.ErrAlreadyLocked,
		manuallock.ErrNotHolder, manuallock.ErrLockChanged, manuallock.ErrLocked, manuallock.ErrReasonRequired,
		manuallock.ErrInvalidTarget, audit.ErrInvalidReason, errLockBody,
	}

	for _, err := range errs {
		p, ok := classify(fmt.Errorf("wrapped: %w", err))
		if !ok || !problem.ValidCode(p.Code) {
			t.Errorf("%v: classified %t with code %q, want a valid code", err, ok, p.Code)
		}
	}
}
