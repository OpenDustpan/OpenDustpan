package tfe_test

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/outputshare"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// errDriverDetail is an error that the adapter does not classify.
var errDriverDetail = errors.New("driver detail")

// errorCodeCase is one error that the error writer answers, with the status,
// the code and the arguments of the answer.
type errorCodeCase struct {
	name   string
	err    error
	status int
	want   errcodetest.Want
}

// checkErrorCodes fails for an error of cases that the error writer answers
// with another status, code or arguments. Each error is wrapped once, as a
// service wraps it.
func checkErrorCodes(t *testing.T, cases []errorCodeCase) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v2/x", http.NoBody)
			tfe.WriteErrorForTest(rec, req, fmt.Errorf("serve: %w", tc.err))

			errcodetest.CheckAnswer(t, errcodetest.ShapeJSONAPI, tc.status, rec.Body.Bytes(), tc.want)
		})
	}
}

// TestCoreErrorCodes proves the code of the 500, of the workspace errors that
// a route of a missing or locked workspace answers, of a client-encrypted
// state, of a full body budget and of a manual lock.
func TestCoreErrorCodes(t *testing.T) {
	t.Parallel()

	since := time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC)
	manual := &manuallock.LockedError{
		Lock: manuallock.Lock{
			Since: since, Holder: "user-1", Reason: "release freeze",
			Target: manuallock.UnitTarget(collection.UnitRef{Namespace: "acme", Tool: "opentofu", Name: "web"}),
		},
		Unit: collection.UnitRef{Namespace: "acme", Tool: "opentofu", Name: "web"},
		Noun: "workspace", Name: "acme/web",
	}

	checkErrorCodes(t, []errorCodeCase{
		{
			"internal", errDriverDetail, http.StatusInternalServerError,
			errcodetest.Want{Code: errcode.ServerInternal, Args: nil, RequestBody: nil},
		},
		{
			"workspace not found", workspace.ErrNotFound, http.StatusNotFound,
			errcodetest.Want{Code: errcode.WorkspaceNotFound, Args: nil, RequestBody: nil},
		},
		{
			"unknown producer", outputshare.ErrUnknownProducer, http.StatusNotFound,
			errcodetest.Want{Code: errcode.WorkspaceNotFound, Args: nil, RequestBody: nil},
		},
		{
			"outputs of a client-encrypted state", outputshare.ErrClientEncrypted, http.StatusConflict,
			errcodetest.Want{Code: errcode.WorkspaceClientEncrypted, Args: nil, RequestBody: nil},
		},
		{
			"client-encrypted state", workspace.ErrClientEncrypted, http.StatusConflict,
			errcodetest.Want{Code: errcode.WorkspaceClientEncrypted, Args: nil, RequestBody: nil},
		},
		{
			"not locked", workspace.ErrNotLocked, http.StatusConflict,
			errcodetest.Want{Code: errcode.WorkspaceNotLocked, Args: nil, RequestBody: nil},
		},
		{
			"lock mismatch", workspace.ErrLockMismatch, http.StatusConflict,
			errcodetest.Want{Code: errcode.WorkspaceLockMismatch, Args: nil, RequestBody: nil},
		},
		{
			"already exists", workspace.ErrAlreadyExists, http.StatusConflict,
			errcodetest.Want{Code: errcode.WorkspaceAlreadyExists, Args: nil, RequestBody: nil},
		},
		{
			"locked", workspace.ErrLocked, http.StatusConflict,
			errcodetest.Want{Code: errcode.WorkspaceLocked, Args: nil, RequestBody: nil},
		},
		{
			"locked by another", workspace.ErrLockedByOther, http.StatusConflict,
			errcodetest.Want{Code: errcode.WorkspaceLocked, Args: nil, RequestBody: nil},
		},
		{
			"busy", workspace.ErrBusy, http.StatusServiceUnavailable,
			errcodetest.Want{Code: errcode.StateVersionBusy, Args: nil, RequestBody: nil},
		},
		{
			"manual lock", fmt.Errorf("%w: %w", workspace.ErrLocked, manual), http.StatusConflict,
			errcodetest.Want{Code: errcode.LockLocked, Args: map[string]any{
				"holder": "user-1", "since": "2026-10-23T09:30:00Z", "lockedKind": "workspace",
				"locked": "acme/web", "refusedKind": "workspace", "refused": "acme/web",
			}, RequestBody: nil},
		},
	})
}

// lockAnswer sends a lock or an unlock request with an empty body to handler
// and returns the answer.
func lockAnswer(t *testing.T, handler http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target, strings.NewReader(`{"data":{}}`))
	req.Header.Set("Authorization", "Bearer test-token")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

// TestLockRoutesAnswerCodes proves that a lock route answers workspace.locked
// for a lock of a tool that another principal holds, and lock.locked with its
// arguments for a manual lock.
func TestLockRoutesAnswerCodes(t *testing.T) {
	t.Parallel()

	held := newConflictHandler(t)

	for _, target := range []string{
		"/api/v2/workspaces/ws-test123/actions/lock", "/api/v2/workspaces/ws-test123/actions/unlock",
	} {
		rec := lockAnswer(t, held, target)
		errcodetest.CheckAnswer(t, errcodetest.ShapeJSONAPI, http.StatusConflict, rec.Body.Bytes(),
			errcodetest.Want{Code: errcode.WorkspaceLocked, Args: nil, RequestBody: []byte(`{"data":{}}`)})
	}

	lock := manuallock.Lock{
		Since:  time.Date(2026, time.October, 19, 10, 0, 0, 0, time.UTC),
		Target: manuallock.CollectionTarget(collection.Ref{Namespace: "acme", Name: "payments"}),
		Holder: "alice", Reason: "change window closed",
	}

	manual, err := tfe.NewHandler(tfe.Config{
		Registry:      nil,
		Authenticator: tokenAuthenticator{},
		Authorizer:    allowAll(),
		AddressGate:   nil,
		Logger:        slog.New(slog.DiscardHandler),
		Namespaces:    newTestNamespaces(t),
		Workspaces: manualLockedWorkspaces{
			conflictWorkspaces: conflictWorkspaces{stubWorkspaces: stubWorkspaces{}}, lock: lock,
		},
		OutputChecker:  nil,
		BaseURL:        baseurl.Resolver{},
		Audit:          newTestAudit(),
		Gate:           nil,
		MaxStateBytes:  0,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		SessionPolicy:  nil,
		StateGuard:     stategate.Off("the test keeps no gates"),
		Placer:         testPlacer(),
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	rec := lockAnswer(t, manual, "/api/v2/workspaces/ws-test123/actions/lock")
	errcodetest.CheckAnswer(t, errcodetest.ShapeJSONAPI, http.StatusConflict, rec.Body.Bytes(), errcodetest.Want{
		Code: errcode.LockLocked, Args: map[string]any{
			"holder": "alice", "since": "2026-10-19T10:00:00Z", "lockedKind": "collection",
			"locked": "acme/payments", "refusedKind": "workspace", "refused": "acme/web",
		},
		RequestBody: []byte(`{"data":{}}`),
	})

	if strings.Contains(rec.Body.String(), `"reason"`) {
		t.Fatalf("the answer holds the reason of the lock as an argument: %s", rec.Body.String())
	}
}
