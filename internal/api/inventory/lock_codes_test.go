package inventory_test

import (
	"net/http"
	"testing"
	"time"

	apiinventory "opendustpan.org/opendustpan/internal/api/inventory"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestChangeAndLockErrorsHaveCodes fails for an error of the deleted-unit
// routes, of a fenced write or of the manual lock routes that answers without
// its code.
func TestChangeAndLockErrorsHaveCodes(t *testing.T) {
	t.Parallel()

	checkCodes(t, []codeCase{
		{"invalid query", apiinventory.ErrInvalidQuery, http.StatusBadRequest, code(errcode.RequestInvalidQuery)},
		{"invalid body", apiinventory.ErrInvalidBody, http.StatusBadRequest, code(errcode.RequestBodyInvalid)},
		{
			"no history", apiinventory.ErrNoHistory, http.StatusNotImplemented,
			code(errcode.UnitHistoryUnavailable),
		},
		{"invalid unit name", coreinventory.ErrInvalidName, http.StatusBadRequest, code(errcode.UnitInvalidName)},
		{
			"deleted unit not found", coreinventory.ErrNotFound, http.StatusNotFound,
			code(errcode.UnitDeletedNotFound),
		},
		{"name taken", coreinventory.ErrNameTaken, http.StatusConflict, code(errcode.UnitNameTaken)},
		{"fenced", namespace.ErrFenced, http.StatusConflict, code(errcode.NamespaceFenced)},
		{"not home", namespace.ErrNotHome, http.StatusMisdirectedRequest, code(errcode.NamespaceNotHome)},
		{
			"in maintenance", maintenance.ErrInMaintenance, http.StatusServiceUnavailable,
			code(errcode.MaintenanceInMaintenance),
		},
		{
			"lock target not found", manuallock.ErrTargetNotFound, http.StatusNotFound,
			code(errcode.LockTargetNotFound),
		},
		{"not locked", manuallock.ErrNotLocked, http.StatusNotFound, code(errcode.LockNotLocked)},
		{"already locked", manuallock.ErrAlreadyLocked, http.StatusConflict, code(errcode.LockAlreadyLocked)},
		{"not holder", manuallock.ErrNotHolder, http.StatusConflict, code(errcode.LockNotHolder)},
		{"lock changed", manuallock.ErrLockChanged, http.StatusConflict, code(errcode.LockChanged)},
		{
			"reason required", manuallock.ErrReasonRequired, http.StatusBadRequest,
			code(errcode.LockReasonRequired),
		},
		{"invalid target", manuallock.ErrInvalidTarget, http.StatusBadRequest, code(errcode.LockInvalidTarget)},
		{"invalid reason", audit.ErrInvalidReason, http.StatusBadRequest, code(errcode.AuditReasonInvalid)},
		{"lock body", apiinventory.ErrLockBody, http.StatusBadRequest, code(errcode.RequestBodyInvalid)},
		{"lock text", apiinventory.ErrLockText, http.StatusBadRequest, code(errcode.RequestBodyInvalid)},
		{
			"locked without the lock", manuallock.ErrLocked, http.StatusInternalServerError,
			code(errcode.ServerInternal),
		},
	})
}

// TestManualLockRefusalsHaveTheirArguments fails when the refusal of a manual
// lock answers without lock.locked, or without the holder, the time, the
// locked object or the refused unit. The reason of the lock is never an
// argument.
func TestManualLockRefusalsHaveTheirArguments(t *testing.T) {
	t.Parallel()

	since := time.Date(2026, time.October, 19, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		target     manuallock.Target
		wantTarget string
		wantKind   string
	}{
		{"namespace", manuallock.NamespaceTarget("acme"), "acme", "namespace"},
		{
			"collection", manuallock.CollectionTarget(collection.Ref{Namespace: "acme", Name: "prod"}),
			"acme/prod", "collection",
		},
		{
			"unit", manuallock.UnitTarget(collection.UnitRef{Namespace: "acme", Tool: "pulumi", Name: "web/prod"}),
			"pulumi:acme/web/prod", "unit",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkCodes(t, []codeCase{{
				name: "locked",
				err: &manuallock.LockedError{
					Lock: manuallock.Lock{Since: since, Target: tc.target, Holder: "alice", Reason: "change window"},
					Noun: "unit", Name: "acme/web/prod",
				},
				status: http.StatusConflict,
				want: errcodetest.Want{Code: errcode.LockLocked, Args: map[string]any{
					"holder": "alice", "since": "2026-10-19T10:00:00Z", "target": tc.wantTarget,
					"targetKind": tc.wantKind, "unit": "acme/web/prod",
				}, RequestBody: nil},
			}})
		})
	}
}

// TestMoveOutOfALockedCollectionAnswersLockLocked fails when the refusal of a
// move out of a locked collection answers without lock.locked and the values
// of the lock of the collection.
func TestMoveOutOfALockedCollectionAnswersLockLocked(t *testing.T) {
	t.Parallel()

	old := manuallock.CollectionTarget(collection.Ref{Namespace: "acme", Name: "old"})
	store := coveringLocks{memLocks: newMemLocks(), lock: lockOf(old), err: nil}
	authz := objectDenier{action: identity.ActionUnitManage, object: "pulumi:acme/web/prod"}

	f := newMoveFixtureFull(t, authz, allowMoves{}, nil, lockService(t, store, authz))
	rec := f.move(t, "new")

	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusConflict, rec.Body.Bytes(), errcodetest.Want{
		Code: errcode.LockLocked, Args: map[string]any{
			"holder": "alice", "since": "2026-10-19T10:00:00Z", "target": "acme/old",
			"targetKind": "collection", "unit": "acme/web/prod",
		}, RequestBody: nil,
	})
}
