package tfe_test

import (
	"net/http"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// TestStateErrorCodes proves the code of the namespace errors of a write, of
// the limits of a body, of the conflicts of a state version and of the 404 of
// a state, a state version, a resource and a namespace.
func TestStateErrorCodes(t *testing.T) {
	t.Parallel()

	first := time.Date(2026, 10, 9, 10, 0, 0, 123000000, time.UTC)

	none := func(code string) errcodetest.Want {
		return errcodetest.Want{Code: code, Args: nil, RequestBody: nil}
	}

	checkErrorCodes(t, []errorCodeCase{
		{
			"maintenance", maintenance.ErrInMaintenance, http.StatusServiceUnavailable,
			none(errcode.MaintenanceInMaintenance),
		},
		{"fenced", namespace.ErrFenced, http.StatusConflict, none(errcode.NamespaceFenced)},
		{"not home", namespace.ErrNotHome, http.StatusMisdirectedRequest, none(errcode.NamespaceNotHome)},
		{
			"database unavailable", namespace.ErrUnavailable, http.StatusServiceUnavailable,
			none(errcode.NamespaceUnavailable),
		},
		{"too large", workspace.ErrTooLarge, http.StatusRequestEntityTooLarge, none(errcode.StateVersionTooLarge)},
		{
			"budget too small", workspace.ErrBudgetTooSmall, http.StatusRequestEntityTooLarge,
			none(errcode.StateVersionBudgetTooSmall),
		},
		{"rate limited", identity.ErrRateLimited, http.StatusTooManyRequests, none(errcode.AuthRateLimited)},
		{
			"pending state version", workspace.ErrPendingState, http.StatusConflict,
			none(errcode.WorkspacePendingStateVersion),
		},
		{
			"changed concurrently", workspace.ErrConcurrentModification, http.StatusConflict,
			none(errcode.WorkspaceChangedConcurrently),
		},
		{"too many pending", workspace.ErrTooManyPending, http.StatusConflict, none(errcode.WorkspaceTooManyPending)},
		{"namespace not found", namespace.ErrNotFound, http.StatusNotFound, none(errcode.NamespaceNotFound)},
		{"no state", workspace.ErrNoState, http.StatusNotFound, none(errcode.WorkspaceNoState)},
		{
			"no state version yet", &workspace.NoStateAtError{First: time.Time{}}, http.StatusNotFound,
			none(errcode.WorkspaceNoState),
		},
		{
			"before the first state version", &workspace.NoStateAtError{First: first}, http.StatusNotFound,
			errcodetest.Want{
				Code: errcode.WorkspaceNoStateAt, Args: map[string]any{"firstAt": "2026-10-09T10:00:00.123Z"},
				RequestBody: nil,
			},
		},
		{
			"state version not found", workspace.ErrStateVersionNotFound, http.StatusNotFound,
			none(errcode.StateVersionNotFound),
		},
		{
			"resource not found", workspace.ErrResourceNotFound, http.StatusNotFound,
			none(errcode.StateVersionResourceNotFound),
		},
	})
}
