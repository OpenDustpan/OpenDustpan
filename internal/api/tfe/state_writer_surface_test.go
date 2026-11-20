package tfe_test

import (
	"reflect"
	"testing"

	"opendustpan.org/opendustpan/internal/api/tfe"
)

// TestStateWriterHasOnlyTheCheckedDeclare proves that the handler can reach
// only the declare that refuses a workspace in no collection. The interface has
// the checked method and no unchecked BeginState, so a handler that calls the
// unchecked declare does not compile, and adding the method back fails here.
func TestStateWriterHasOnlyTheCheckedDeclare(t *testing.T) {
	t.Parallel()

	writer := reflect.TypeFor[tfe.StateWriter]()

	if _, ok := writer.MethodByName("BeginStateInCollection"); !ok {
		t.Error("StateWriter has no BeginStateInCollection")
	}

	if _, ok := writer.MethodByName("BeginState"); ok {
		t.Error("StateWriter has the unchecked BeginState; the handler could skip the collection check")
	}

	workspaces := reflect.TypeFor[tfe.WorkspaceService]()
	if _, ok := workspaces.MethodByName("BeginState"); ok {
		t.Error("WorkspaceService has the unchecked BeginState; the handler could skip the collection check")
	}

	locker := reflect.TypeFor[tfe.WorkspaceLocker]()
	if _, ok := locker.MethodByName("LockForJob"); ok {
		t.Error("WorkspaceLocker has the unchecked LockForJob; the handler could skip the collection check")
	}

	if _, ok := workspaces.MethodByName("LockForJob"); ok {
		t.Error("WorkspaceService has the unchecked LockForJob; the handler could skip the collection check")
	}
}
