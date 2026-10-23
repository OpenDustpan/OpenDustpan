package jobstore_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/internal/jobstore"
)

// TestCheckPlanBodyAcceptsAReserve proves that the plan row of a reserve, which
// has no part yet, passes the restore check, and that a row of another plan
// does not.
func TestCheckPlanBodyAcceptsAReserve(t *testing.T) {
	t.Parallel()

	const reserve = `{"id":"pln-1","itemKey":"ik-1","stateVersion":"sv-1","parts":[],` +
		`"createdAt":"2026-10-21T09:00:00Z","expiresAt":"2027-10-21T09:00:00Z","redactedAt":"0001-01-01T00:00:00Z"}`

	if err := jobstore.CheckPlanBody("pln-1", reserve); err != nil {
		t.Errorf("check of a reserve = %v, want nil", err)
	}

	if err := jobstore.CheckPlanBody("pln-2", reserve); !errors.Is(err, jobstore.ErrDamaged) {
		t.Errorf("check of a reserve under another ID = %v, want ErrDamaged", err)
	}
}
