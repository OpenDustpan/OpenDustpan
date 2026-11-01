package pulumi_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/pulumi"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/stack"
)

// TestUpdateErrorsCarryACode proves that the errors of the update cycle and
// of the update journal answer their code, and that a manual lock answers
// lock.locked with the holder, the time and the locked object, but never the
// reason of the lock.
func TestUpdateErrorsCarryACode(t *testing.T) {
	t.Parallel()

	locked := &manuallock.LockedError{
		Lock: manuallock.Lock{
			Since:  time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC),
			Target: manuallock.UnitTarget(collection.UnitRef{Namespace: "acme", Tool: "pulumi", Name: "web/dev"}),
			Holder: "prn-alice",
			Reason: "release freeze",
		},
		Unit: collection.UnitRef{Namespace: "acme", Tool: "pulumi", Name: "web/dev"},
		Noun: "stack",
		Name: "acme/web/dev",
	}

	lockArgs := map[string]any{
		"holder": "prn-alice", "since": "2026-10-23T09:30:00Z", "lockedKind": "stack", "locked": "acme/web/dev",
		"refusedKind": "stack", "refused": "acme/web/dev",
	}

	checkErrorAnswers(t, []errorAnswer{
		{"manual lock", locked, http.StatusConflict, errcode.LockLocked, lockArgs},
		{
			"manual lock in an update conflict", fmt.Errorf("%w: %w", stack.ErrUpdateConflict, locked),
			http.StatusConflict, errcode.LockLocked, lockArgs,
		},
		{"invalid update", stack.ErrInvalidUpdate, http.StatusBadRequest, errcode.UpdateInvalid, nil},
		{"update not found", stack.ErrUpdateNotFound, http.StatusNotFound, errcode.UpdateNotFound, nil},
		{
			"console session on an update", pulumi.ErrSessionUpdate, http.StatusForbidden,
			errcode.UpdateConsoleSession, nil,
		},
		{"update conflict", stack.ErrUpdateConflict, http.StatusConflict, errcode.UpdateConflict, nil},
		{
			"update conflict with the running update", &stack.UpdateConflictError{Active: "upd-1"},
			http.StatusConflict, errcode.UpdateConflict, nil,
		},
		{"wrong state", stack.ErrWrongUpdateState, http.StatusConflict, errcode.UpdateWrongState, nil},
		{
			"concurrent modification", stack.ErrConcurrentModification, http.StatusConflict,
			errcode.UpdateConcurrentModification, nil,
		},
		{"invalid delta", stack.ErrInvalidDelta, http.StatusConflict, errcode.UpdateDeltaInvalid, nil},
		{
			"checkpoint conflict", stack.ErrCheckpointConflict, http.StatusConflict,
			errcode.UpdateCheckpointConflict, nil,
		},
		{"stale checkpoint", stack.ErrStaleCheckpoint, http.StatusConflict, errcode.UpdateCheckpointStale, nil},
		{"journal conflict", stack.ErrJournalConflict, http.StatusConflict, errcode.JournalConflict, nil},
		{"journal incomplete", stack.ErrJournalIncomplete, http.StatusConflict, errcode.JournalIncomplete, nil},
		{"journal batch", stack.ErrJournalBatch, http.StatusBadRequest, errcode.JournalBatchInvalid, nil},
		{"lease invalid", stack.ErrLeaseInvalid, http.StatusUnauthorized, errcode.UpdateLeaseInvalid, nil},
		{"lease expired", stack.ErrLeaseExpired, http.StatusUnauthorized, errcode.UpdateLeaseExpired, nil},
		{"too large", stack.ErrTooLarge, http.StatusRequestEntityTooLarge, errcode.UpdateTooLarge, nil},
		{
			"journal batch over its bound", stack.BatchOverLimit(2048, 1024), http.StatusRequestEntityTooLarge,
			errcode.UpdateTooLarge, nil,
		},
		{
			"write attempt past its deadline", fmt.Errorf("save checkpoint: %w", blob.ErrAttemptExpired),
			http.StatusServiceUnavailable, errcode.UpdateAttemptExpired, nil,
		},
		{
			"journal build timeout", stack.ErrJournalBuildTimeout, http.StatusServiceUnavailable,
			errcode.JournalBuildTimeout, nil,
		},
	})
}
