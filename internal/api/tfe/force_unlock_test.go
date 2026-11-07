package tfe_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// forceUnlockCall is what a force-unlock route passed to the workspace
// service.
type forceUnlockCall struct {
	expect workspace.Lock
	by     string
	entry  audit.Entry
}

// recordingForceUnlock records each call of ForceUnlockLock and answers err.
type recordingForceUnlock struct {
	stubWorkspaces

	err   error
	mu    *sync.Mutex
	calls *[]forceUnlockCall
}

func newRecordingForceUnlock(err error) recordingForceUnlock {
	return recordingForceUnlock{
		stubWorkspaces: stubWorkspaces{},
		err:            err,
		mu:             &sync.Mutex{},
		calls:          &[]forceUnlockCall{},
	}
}

//nolint:revive // The signature of the tfe.WorkspaceLocker port.
func (r recordingForceUnlock) ForceUnlockLock(
	_ context.Context, id workspace.ID, expect workspace.Lock, by string, _ namespace.Overrides, _ func() error,
	entry audit.Entry,
) (workspace.Workspace, bool, error) {
	r.mu.Lock()
	*r.calls = append(*r.calls, forceUnlockCall{expect: expect, by: by, entry: entry})
	r.mu.Unlock()

	if r.err != nil {
		return workspace.Workspace{}, false, r.err
	}

	ws, err := stubWorkspaceByID(id)

	return ws, false, err
}

func (r recordingForceUnlock) recorded() []forceUnlockCall {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]forceUnlockCall(nil), *r.calls...)
}

// newWriteHandlerWithAuthz returns a production Handler over ws that signs in
// the token "test-token" and authorizes with authz.
func newWriteHandlerWithAuthz(t *testing.T, ws tfe.WorkspaceService, authz identity.Authorizer) *tfe.Handler {
	t.Helper()

	handler, err := tfe.NewHandler(tfe.Config{
		Registry:       nil,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     authz,
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     ws,
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

	return handler
}

// forceUnlockHandler returns a production Handler over the recording stub.
func forceUnlockHandler(t *testing.T, ws recordingForceUnlock, authz identity.Authorizer) *tfe.Handler {
	t.Helper()

	return newWriteHandlerWithAuthz(t, ws, authz)
}

// errorObject is one error of a JSON:API error document.
type errorObject struct {
	Status string `json:"status"`
	Code   string `json:"code"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// errorDocument is the JSON:API error document of a response.
type errorDocument struct {
	Errors []errorObject `json:"errors"`
}

func decodeErrors(t *testing.T, rec *httptest.ResponseRecorder) errorDocument {
	t.Helper()

	var doc errorDocument
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || len(doc.Errors) != 1 {
		t.Fatalf("error document = %q (%v), want one error", rec.Body.String(), err)
	}

	return doc
}

// attributesDocument reads the attributes of a resource document as raw JSON.
type attributesDocument struct {
	Data attributesData `json:"data"`
}

// attributesData is the data object of an attributesDocument.
type attributesData struct {
	Attributes map[string]json.RawMessage `json:"attributes"`
}

const forceUnlockTarget = "/api/v2/workspaces/ws-test123/actions/force-unlock"

// forceUnlockBodyCase is one body of a force-unlock request and what the route
// passes to the workspace service.
type forceUnlockBodyCase struct {
	name       string
	body       string
	status     int
	wantReason string
	wantID     string
	wantHolder string
}

// TestForceUnlockBody proves the optional body of a force-unlock: no body and
// an empty object release as before, and each attribute reaches the workspace
// service. A body that cannot be read, a reason that is too long or holds a
// control character, and a lock holder without a lock ID answer 422 and call
// the service not at all.
func TestForceUnlockBody(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("é", audit.MaxReasonLen)
	ok, bad := http.StatusOK, http.StatusUnprocessableEntity

	cases := []forceUnlockBodyCase{
		{"no body", "", ok, "", "", ""},
		{"empty object", `{}`, ok, "", "", ""},
		{"empty data", `{"data":{}}`, ok, "", "", ""},
		{"unknown attribute", `{"data":{"attributes":{"other":1}}}`, ok, "", "", ""},
		{"reason", `{"data":{"attributes":{"reason":"the run crashed"}}}`, ok, "the run crashed", "", ""},
		{"reason of 500 code points", `{"data":{"attributes":{"reason":"` + long + `"}}}`, ok, long, "", ""},
		{"lock ID alone", `{"data":{"attributes":{"lock-id":"lock-1"}}}`, ok, "", "lock-1", ""},
		{
			"lock ID and holder", `{"data":{"attributes":{"lock-id":"lock-1","lock-holder":"alice"}}}`,
			ok, "", "lock-1", "alice",
		},
		{"reason of 501 code points", `{"data":{"attributes":{"reason":"` + long + `a"}}}`, bad, "", "", ""},
		{"line break in the reason", `{"data":{"attributes":{"reason":"a\nb"}}}`, bad, "", "", ""},
		{"null byte in the reason", `{"data":{"attributes":{"reason":"a\u0000b"}}}`, bad, "", "", ""},
		{"lock holder without lock ID", `{"data":{"attributes":{"lock-holder":"alice"}}}`, bad, "", "", ""},
		{"not JSON", `{`, bad, "", "", ""},
		{"reason is not a string", `{"data":{"attributes":{"reason":3}}}`, bad, "", "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ws := newRecordingForceUnlock(nil)
			handler := forceUnlockHandler(t, ws, allowAll())

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, workspaceRequest(t, http.MethodPost, forceUnlockTarget, tc.body))

			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}

			checkForceUnlockCalls(t, ws.recorded(), tc)
		})
	}
}

// checkForceUnlockCalls checks what the route passed to the workspace service:
// nothing for a refused body, else one call with the attributes of the body and
// the fields of the audit entry.
func checkForceUnlockCalls(t *testing.T, calls []forceUnlockCall, tc forceUnlockBodyCase) {
	t.Helper()

	if tc.status != http.StatusOK {
		if len(calls) != 0 {
			t.Fatalf("service calls = %+v, want none for a refused body", calls)
		}

		return
	}

	if len(calls) != 1 {
		t.Fatalf("service calls = %+v, want one", calls)
	}

	got := calls[0]
	if got.entry.Reason != tc.wantReason || got.expect.ID != tc.wantID || got.expect.Holder != tc.wantHolder ||
		got.by != "tester" {
		t.Fatalf("call = %+v, want reason %q, lock ID %q, holder %q, by tester",
			got, tc.wantReason, tc.wantID, tc.wantHolder)
	}

	if got.entry.Action != "unit:write-forced" || got.entry.Object != "opentofu:acme/web" ||
		got.entry.Kind != audit.KindChange || got.entry.Channel != audit.ChannelAPI {
		t.Fatalf("entry = %+v, want the action, the unit, the kind change and the channel api", got.entry)
	}
}

// TestForceUnlockErrorCodes proves the status and the machine code of each
// refusal of a force-unlock, and that the code is a member of the error
// object.
func TestForceUnlockErrorCodes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"not locked", workspace.ErrNotLocked, http.StatusConflict, "workspace.not-locked"},
		{"lock mismatch", workspace.ErrLockMismatch, http.StatusConflict, "workspace.lock-mismatch"},
		{"client encrypted", workspace.ErrClientEncrypted, http.StatusConflict, "workspace.client-encrypted"},
		{"locked by another", workspace.ErrLockedByOther, http.StatusConflict, "workspace.locked"},
		{
			"concurrent change",
			workspace.ErrConcurrentModification,
			http.StatusConflict,
			"workspace.changed-concurrently",
		},
		{"invalid reason", audit.ErrInvalidReason, http.StatusUnprocessableEntity, "audit.reason-invalid"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := forceUnlockHandler(t, newRecordingForceUnlock(tc.err), allowAll())

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, workspaceRequest(t, http.MethodPost, forceUnlockTarget, ""))

			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}

			doc := decodeErrors(t, rec)
			if doc.Errors[0].Code != tc.code || doc.Errors[0].Detail == "" {
				t.Fatalf("error = %+v, want code %q and a detail", doc.Errors[0], tc.code)
			}

			var raw map[string][]map[string]json.RawMessage
			if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
				t.Fatalf("decode: %v", err)
			}

			if _, has := raw["errors"][0]["code"]; has != (tc.code != "") {
				t.Fatalf("code member present = %t, want %t", has, tc.code != "")
			}
		})
	}
}

// refusingAuthorizer refuses unit:write-forced with the given cause, so a test
// proves that the 403 text holds for a cause that is not a missing role.
type refusingAuthorizer struct {
	cause string
}

func (r refusingAuthorizer) Authorize(
	_ context.Context,
	_ identity.Principal,
	action identity.Action,
	_ identity.Object,
) error {
	if action == identity.ActionUnitWriteForced {
		return fmt.Errorf("%w: %s", identity.ErrDenied, r.cause)
	}

	return nil
}

// ListObjects gives an administrator every object and any other principal none.
func (refusingAuthorizer) ListObjects(
	_ context.Context,
	p identity.Principal,
	_ identity.Action,
) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

// TestForceUnlockDeniedNamesTheAction proves that a refused force-unlock gets
// 403 whose detail names the action and the object and is true for every
// cause of the refusal, and that the service is not called. A principal that
// has unit:manage and not unit:write-forced is refused too. The causes are a
// missing role and an address that the IP allowlist refuses.
func TestForceUnlockDeniedNamesTheAction(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		authz identity.Authorizer
	}{
		{
			"missing role",
			selectiveAuthorizer{deny: map[identity.Action]bool{identity.ActionUnitWriteForced: true}},
		},
		{"IP allowlist", refusingAuthorizer{cause: "source address 203.0.113.9 is not in the allowlist"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ws := newRecordingForceUnlock(nil)
			handler := forceUnlockHandler(t, ws, tc.authz)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, workspaceRequest(t, http.MethodPost, forceUnlockTarget, ""))

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
			}

			detail := decodeErrors(t, rec).Errors[0].Detail
			if !strings.Contains(detail, "the action unit:write-forced on ") ||
				!strings.HasSuffix(detail, " was refused") {
				t.Fatalf("detail = %q, want the action, the object and \"was refused\"", detail)
			}

			if strings.Contains(detail, "needs") {
				t.Fatalf("detail = %q, must not claim a cause", detail)
			}

			if calls := ws.recorded(); len(calls) != 0 {
				t.Fatalf("service calls = %+v, want none", calls)
			}
		})
	}
}

// TestWorkspaceDocumentHoldsTheLockID proves that the workspace document
// holds attributes.lock-id: the ID of the current lock, and "" when the
// workspace is unlocked.
func TestWorkspaceDocumentHoldsTheLockID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		ws     tfe.WorkspaceService
		target string
		want   string
	}{
		{
			"locked",
			lockedActionWorkspaces{stubWorkspaces: stubWorkspaces{}},
			"/api/v2/workspaces/ws-test123",
			"lock-alice",
		},
		{"unlocked", stubWorkspaces{}, "/api/v2/workspaces/ws-test123", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := newWriteHandlerWithAuthz(t, tc.ws, allowAll())

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, workspaceRequest(t, http.MethodGet, tc.target, ""))

			var doc attributesDocument
			if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || rec.Code != http.StatusOK {
				t.Fatalf("status %d, body %q, err %v", rec.Code, rec.Body.String(), err)
			}

			raw, ok := doc.Data.Attributes["lock-id"]
			if !ok {
				t.Fatalf("attributes = %v, want lock-id", doc.Data.Attributes)
			}

			var got string
			if err := json.Unmarshal(raw, &got); err != nil || got != tc.want {
				t.Fatalf("lock-id = %s (%v), want %q", raw, err, tc.want)
			}
		})
	}
}

// countingForceUnlock counts the reads of a workspace by ID.
type countingForceUnlock struct {
	recordingForceUnlock

	reads *atomic.Int64
}

func (c countingForceUnlock) GetByID(ctx context.Context, id workspace.ID) (workspace.Workspace, error) {
	c.reads.Add(1)

	return c.recordingForceUnlock.GetByID(ctx, id)
}

// TestForceUnlockReadsTheWorkspaceTwice proves that a force-unlock reads the
// workspace once to resolve its unit and once for the holder and the
// override switches. The domain reads it again in its own transaction.
func TestForceUnlockReadsTheWorkspaceTwice(t *testing.T) {
	t.Parallel()

	ws := countingForceUnlock{recordingForceUnlock: newRecordingForceUnlock(nil), reads: new(atomic.Int64)}
	handler := newWriteHandlerWithAuthz(t, ws, allowAll())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, workspaceRequest(t, http.MethodPost, forceUnlockTarget, ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	if got := ws.reads.Load(); got != 2 {
		t.Fatalf("reads of the workspace = %d, want 2", got)
	}
}
