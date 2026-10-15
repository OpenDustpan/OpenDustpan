package tofuhttp_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/tofuhttp"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

const (
	adminToken = "admin-token-0123456789"
	userToken  = "user-token-0123456789"
	unitPath   = "/tofu/state/acme/net"
	lockOne    = "lock-one"
)

// unlockedLock returns the lock of a unit that no principal holds.
func unlockedLock() workspace.Lock {
	return workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""}
}

// fakeUnits is an in-memory workspace service for the handler tests. It keeps
// the lock of one unit and enforces the lock rules that the handler relies on.
type fakeUnits struct {
	mu        sync.Mutex
	ws        workspace.Workspace
	exists    bool
	hideOnce  bool
	createErr error
	// writeErr, when set, is what WriteState answers.
	writeErr error
}

// newFakeUnits returns a service whose unit exists, unlocked, with both switches on.
func newFakeUnits(exists bool) *fakeUnits {
	ref := workspace.Ref{Namespace: "acme", Name: "net"}
	on := namespace.Overrides{Forced: true, Unlocked: true}

	return &fakeUnits{
		mu: sync.Mutex{},
		ws: workspace.Workspace{
			Ref:       ref,
			ID:        "ws-1",
			CreatedAt: time.Time{},
			UpdatedAt: time.Time{},
			Lock:      unlockedLock(),
			Revision:  0,
			Overrides: on,
		},
		exists:    exists,
		hideOnce:  false,
		createErr: nil,
		writeErr:  nil,
	}
}

// Get returns the unit, or ErrNotFound when it does not exist yet.
func (f *fakeUnits) Get(_ context.Context, _ workspace.Ref) (workspace.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if !f.exists || f.hideOnce {
		f.hideOnce = false

		return workspace.Workspace{}, workspace.ErrNotFound
	}

	return f.ws, nil
}

// GetByID returns the unit with id.
func (f *fakeUnits) GetByID(ctx context.Context, _ workspace.ID) (workspace.Workspace, error) {
	return f.Get(ctx, f.ws.Ref)
}

// Create makes the unit. A create on a unit that exists answers ErrAlreadyExists.
func (f *fakeUnits) Create(_ context.Context, _ workspace.Ref) (workspace.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.createErr != nil {
		return workspace.Workspace{}, f.createErr
	}

	if f.exists {
		return workspace.Workspace{}, workspace.ErrAlreadyExists
	}

	f.exists = true

	return f.ws, nil
}

// LockWithID takes the lock under lockID, or answers ErrLocked.
func (f *fakeUnits) LockWithID(
	_ context.Context,
	_ workspace.ID,
	holder, reason, lockID string,
) (workspace.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.ws.Lock.Locked() {
		return workspace.Workspace{}, workspace.ErrLocked
	}

	f.ws.Lock = workspace.Lock{LockedAt: time.Now().UTC(), ID: lockID, Holder: holder, Reason: reason}

	return f.ws, nil
}

// UnlockLock releases the lock when holder holds lockID.
func (f *fakeUnits) UnlockLock(_ context.Context, _ workspace.ID, lockID, holder string) (workspace.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	switch {
	case !f.ws.Lock.Locked():
		return workspace.Workspace{}, workspace.ErrNotLocked
	case lockID != "" && f.ws.Lock.ID != lockID:
		return workspace.Workspace{}, workspace.ErrLockMismatch
	case f.ws.Lock.Holder != holder:
		return workspace.Workspace{}, workspace.ErrLockedByOther
	}

	f.ws.Lock = unlockedLock()

	return f.ws, nil
}

// ForceUnlockLock releases the lock whoever holds it, when lockID is the lock.
//
//nolint:revive // The port takes the lock ID beside the caller, the allowance and the audit record.
func (f *fakeUnits) ForceUnlockLock(
	_ context.Context, _ workspace.ID, expect workspace.Lock, _ string, _ namespace.Overrides, _ func() error,
) (workspace.Workspace, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	switch {
	case !f.ws.Lock.Locked():
		return workspace.Workspace{}, false, workspace.ErrNotLocked
	case expect.ID != "" && f.ws.Lock.ID != expect.ID:
		return workspace.Workspace{}, false, workspace.ErrLockMismatch
	}

	f.ws.Lock = unlockedLock()

	return f.ws, false, nil
}

// Current answers that the unit has no state.
func (f *fakeUnits) Current(context.Context, workspace.ID) (workspace.StateVersion, error) {
	var none workspace.StateVersion

	return none, workspace.ErrNoState
}

// Open is never reached: the unit has no state.
func (f *fakeUnits) Open(context.Context, workspace.StateVersion) (io.ReadCloser, error) {
	return nil, workspace.ErrNoState
}

// WriteState applies the lock rules of a streamed write, then drains the body.
func (f *fakeUnits) WriteState(
	_ context.Context, _ workspace.ID, in workspace.StreamInput, _ workspace.StreamAuthority, body io.Reader,
) (workspace.StateVersion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var none workspace.StateVersion

	if f.writeErr != nil {
		return none, f.writeErr
	}

	switch {
	case in.LockID == "" && f.ws.Lock.Locked():
		return none, workspace.ErrLocked
	case in.LockID != "" && f.ws.Lock.ID != in.LockID:
		return none, workspace.ErrLockMismatch
	}

	if _, err := io.Copy(io.Discard, body); err != nil {
		return none, fmt.Errorf("drain body: %w", err)
	}

	return none, nil
}

// fakeNamespaces answers the switches of the namespace of the unit.
type fakeNamespaces struct{}

// Get returns the namespace with both switches on.
func (fakeNamespaces) Get(context.Context, namespace.Name) (namespace.Namespace, error) {
	return namespace.Namespace{
		CreatedAt:       time.Time{},
		Name:            "acme",
		HomeRegion:      "",
		FailoverVersion: 0,
		Overrides:       namespace.Overrides{Forced: true, Unlocked: true},
	}, nil
}

// tokenAuth accepts the admin and the user token, and no other.
type tokenAuth struct{}

// AuthenticateToken returns the principal of token.
func (tokenAuth) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	switch token {
	case adminToken:
		return identity.Principal{
			TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
			Subject:        "alice",
			DisplayName:    "",
			Admin:          true,
			TokenID:        "",
			TokenExpiresAt: time.Time{},
		}, nil
	case userToken:
		return identity.Principal{
			TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
			Subject:        "bob",
			DisplayName:    "",
			Admin:          false,
			TokenID:        "",
			TokenExpiresAt: time.Time{},
		}, nil
	default:
		return identity.Principal{
			TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
			Subject:        "",
			DisplayName:    "",
			Admin:          false,
			TokenID:        "",
			TokenExpiresAt: time.Time{},
		}, identity.ErrUnauthenticated
	}
}

// userAuthz lets bob read and write a unit, and refuses him the manage and
// forced-write actions. The admin may do anything.
type userAuthz struct{}

// Authorize applies the rules above.
func (userAuthz) Authorize(_ context.Context, p identity.Principal, action identity.Action, obj identity.Object) error {
	if p.Admin {
		return nil
	}

	if slices.Contains(
		[]identity.Action{identity.ActionUnitRead, identity.ActionUnitWrite, identity.ActionUnitWriteUnlocked},
		action,
	) {
		return nil
	}

	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

// newTestHandler returns a handler over units.
func newTestHandler(t *testing.T, units *fakeUnits) http.Handler {
	t.Helper()

	h, err := tofuhttp.NewHandler(tofuhttp.Config{
		Authenticator: tokenAuth{},
		Authorizer:    userAuthz{},
		Logger:        slog.New(slog.DiscardHandler),
		Workspaces:    units,
		Namespaces:    fakeNamespaces{},
		Audit:         nil,
		Gate:          nil,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	return h
}

// do sends one request to the handler and returns the response.
func do(
	h http.Handler,
	method, target, body string,
	headers map[string]string,
) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, target, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

// bearer returns the header of a Bearer token.
func bearer(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

// lockBody returns the lock info of a LOCK or UNLOCK request.
func lockBody(id, who string) string {
	return fmt.Sprintf(
		`{"ID":%q,"Operation":"OperationTypeApply","Info":"","Who":%q,"Version":"1.13.1","Created":"2026-10-13T00:00:00Z","Path":""}`,
		id,
		who,
	)
}

// TestBasicAndBearerAuth proves both credential forms reach the handler, and
// that a missing or wrong token answers 401 with a challenge.
func TestBasicAndBearerAuth(t *testing.T) {
	t.Parallel()

	h := newTestHandler(t, newFakeUnits(true))

	basic := map[string]string{
		"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte("ignored:"+adminToken)),
	}

	rec := do(h, http.MethodGet, unitPath, "", basic)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("basic auth with a unit that has no state: status %d, want 404", rec.Code)
	}

	rec = do(h, http.MethodGet, unitPath, "", bearer(adminToken))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bearer auth with a unit that has no state: status %d, want 404", rec.Code)
	}

	for name, headers := range map[string]map[string]string{
		"missing token": nil,
		"wrong token":   bearer("not-a-token-0123456789"),
	} {
		rec := do(h, http.MethodGet, unitPath, "", headers)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, rec.Code)
		}

		if rec.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("%s: no WWW-Authenticate challenge", name)
		}
	}
}

// TestLockedBodies proves a refused LOCK answers 423 with the lock info of the
// holder, and a refused UNLOCK with another lock ID answers 409 with that info.
func TestLockedBodies(t *testing.T) {
	t.Parallel()

	h := newTestHandler(t, newFakeUnits(true))

	rec := do(h, "LOCK", unitPath, lockBody(lockOne, "alice"), bearer(adminToken))
	if rec.Code != http.StatusOK {
		t.Fatalf("first LOCK: status %d, want 200", rec.Code)
	}

	rec = do(h, "LOCK", unitPath, lockBody("lock-two", "bob"), bearer(userToken))
	if rec.Code != http.StatusLocked {
		t.Fatalf("LOCK of a held unit: status %d, want 423", rec.Code)
	}

	assertLockBody(t, rec.Body.Bytes(), lockOne, "alice")

	rec = do(h, "UNLOCK", unitPath, lockBody("lock-two", "bob"), bearer(userToken))
	if rec.Code != http.StatusConflict {
		t.Fatalf("UNLOCK with another lock ID: status %d, want 409", rec.Code)
	}

	assertLockBody(t, rec.Body.Bytes(), lockOne, "alice")
}

// assertLockBody checks the ID and the holder of a lock info body.
func assertLockBody(t *testing.T, body []byte, wantID, wantWho string) {
	t.Helper()

	var info struct {
		ID  string `json:"ID"`  //nolint:tagliatelle // The OpenTofu http backend reads PascalCase keys.
		Who string `json:"Who"` //nolint:tagliatelle // The OpenTofu http backend reads PascalCase keys.
	}

	if err := json.Unmarshal(body, &info); err != nil {
		t.Fatalf("lock body %q: %v", body, err)
	}

	if info.ID != wantID || info.Who != wantWho {
		t.Fatalf("lock body = ID %q Who %q, want ID %q Who %q", info.ID, info.Who, wantID, wantWho)
	}
}

// TestCreateOnFirstWriteNeedsManage proves a LOCK that creates the unit needs
// unit:manage. A principal without it is refused with 403, and the unit is not
// created.
func TestCreateOnFirstWriteNeedsManage(t *testing.T) {
	t.Parallel()

	units := newFakeUnits(false)
	h := newTestHandler(t, units)

	rec := do(
		h,
		"LOCK",
		unitPath,
		lockBody(lockOne, "bob"),
		bearer(userToken),
	)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("LOCK that creates the unit without unit:manage: status %d, want 403", rec.Code)
	}

	if units.exists {
		t.Fatal("the unit was created without unit:manage")
	}

	rec = do(h, "LOCK", unitPath, lockBody(lockOne, "alice"), bearer(adminToken))
	if rec.Code != http.StatusOK {
		t.Fatalf("LOCK that creates the unit as an admin: status %d, want 200", rec.Code)
	}
}

// TestCreateRaceReadsAgain proves two first requests do not answer 500: the
// create that loses answers ErrAlreadyExists, and the unit is read again.
func TestCreateRaceReadsAgain(t *testing.T) {
	t.Parallel()

	// The first read answers not found, and the unit exists by the time the create
	// runs: the create loses with ErrAlreadyExists, and the second read finds it.
	units := newFakeUnits(true)
	units.hideOnce = true

	h := newTestHandler(t, units)

	rec := do(h, "LOCK", unitPath, lockBody(lockOne, "alice"), bearer(adminToken))
	if rec.Code != http.StatusOK {
		t.Fatalf("LOCK after another request created the unit: status %d, want 200", rec.Code)
	}
}

// TestForceUnlockNeedsItsAction proves an UNLOCK by a principal that does not
// hold the lock needs unit:write-forced. A principal without it is refused with
// 403 and the lock stays. An admin with it releases the lock of another holder.
func TestForceUnlockNeedsItsAction(t *testing.T) {
	t.Parallel()

	h := newTestHandler(t, newFakeUnits(true))

	rec := do(h, "LOCK", unitPath, lockBody(lockOne, "alice"), bearer(adminToken))
	if rec.Code != http.StatusOK {
		t.Fatalf("LOCK by alice: status %d, want 200", rec.Code)
	}

	rec = do(
		h,
		"UNLOCK",
		unitPath,
		lockBody(lockOne, "alice"),
		bearer(userToken),
	)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("force-unlock by bob without unit:write-forced: status %d, want 403", rec.Code)
	}

	rec = do(h, "UNLOCK", unitPath, lockBody(lockOne, "alice"), bearer(adminToken))
	if rec.Code != http.StatusOK {
		t.Fatalf("UNLOCK by the holder: status %d, want 200", rec.Code)
	}

	rec = do(h, "LOCK", unitPath, lockBody("lock-two", "bob"), bearer(userToken))
	if rec.Code != http.StatusOK {
		t.Fatalf("LOCK by bob: status %d, want 200", rec.Code)
	}

	rec = do(h, "UNLOCK", unitPath, lockBody("lock-two", "bob"), bearer(adminToken))
	if rec.Code != http.StatusOK {
		t.Fatalf("force-unlock of bob's lock by an admin: status %d, want 200", rec.Code)
	}
}

// TestStatusMapping proves the status codes of the write refusals: a write
// without the lock ID of a locked unit answers 423, and a write that names another
// lock answers 409.
func TestStatusMapping(t *testing.T) {
	t.Parallel()

	h := newTestHandler(t, newFakeUnits(true))

	rec := do(h, "LOCK", unitPath, lockBody(lockOne, "alice"), bearer(adminToken))
	if rec.Code != http.StatusOK {
		t.Fatalf("LOCK: status %d, want 200", rec.Code)
	}

	state := `{"serial":1,"lineage":"l","resources":[]}`

	rec = do(h, http.MethodPost, unitPath, state, bearer(adminToken))
	if rec.Code != http.StatusLocked {
		t.Fatalf("POST without the lock ID of a locked unit: status %d, want 423", rec.Code)
	}

	rec = do(
		h,
		http.MethodPost,
		unitPath+"?ID=other",
		state,
		bearer(adminToken),
	)
	if rec.Code != http.StatusConflict {
		t.Fatalf("POST with another lock ID: status %d, want 409", rec.Code)
	}

	rec = do(h, http.MethodPost, unitPath+"?ID="+lockOne, state, bearer(adminToken))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST with the current lock ID: status %d, want 200", rec.Code)
	}

	rec = do(h, http.MethodPost, unitPath+"?ID="+lockOne, state, map[string]string{
		"Content-MD5": "!!not-base64!!",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST without a credential: status %d, want 401", rec.Code)
	}

	rec = do(h, http.MethodPost, unitPath+"?ID="+lockOne, state, map[string]string{
		"Authorization": "Bearer " + adminToken,
		"Content-MD5":   "!!not-base64!!",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST with a malformed Content-MD5: status %d, want 400", rec.Code)
	}
}

// TestPostCreatesTheUnitOnFirstWrite proves a POST to a unit that does not exist
// creates it for a caller with unit:manage, and writes the state (ADR 49,
// decision 5).
func TestPostCreatesTheUnitOnFirstWrite(t *testing.T) {
	t.Parallel()

	units := newFakeUnits(false)
	h := newTestHandler(t, units)

	state := `{"serial":1,"lineage":"l","resources":[]}`

	if rec := do(h, http.MethodPost, unitPath, state, bearer(adminToken)); rec.Code != http.StatusOK {
		t.Fatalf("POST that creates the unit as an admin: status %d, want 200", rec.Code)
	}

	if !units.exists {
		t.Fatal("the unit was not created by the POST")
	}
}

// TestPostConflictBodyNamesTheLock proves a POST that names another lock answers
// 409 with the lock info of the current lock.
func TestPostConflictBodyNamesTheLock(t *testing.T) {
	t.Parallel()

	h := newTestHandler(t, newFakeUnits(true))

	if rec := do(h, "LOCK", unitPath, lockBody(lockOne, "alice"), bearer(adminToken)); rec.Code != http.StatusOK {
		t.Fatalf("LOCK: status %d, want 200", rec.Code)
	}

	rec := do(h, http.MethodPost, unitPath+"?ID=other", `{"serial":1,"lineage":"l"}`, bearer(adminToken))
	if rec.Code != http.StatusConflict {
		t.Fatalf("POST with another lock ID: status %d, want 409", rec.Code)
	}

	assertLockBody(t, rec.Body.Bytes(), lockOne, "alice")
}

// TestWriteRefusalStatuses proves the status of each refusal of a POST: the
// oversized state, the full pending cap, the override that is off, and a unit
// that already exists.
func TestWriteRefusalStatuses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		err  error
		name string
		want int
	}{
		{
			name: "too large",
			err:  fmt.Errorf("%w: outputs of more than 16777216 bytes", workspace.ErrTooLarge),
			want: http.StatusRequestEntityTooLarge,
		},
		{name: "too many pending", err: workspace.ErrTooManyPending, want: http.StatusTooManyRequests},
		{name: "override refused", err: workspace.ErrOverrideRefused, want: http.StatusForbidden},
		{name: "already exists", err: workspace.ErrAlreadyExists, want: http.StatusConflict},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			units := newFakeUnits(true)
			units.writeErr = tc.err
			h := newTestHandler(t, units)

			rec := do(h, http.MethodPost, unitPath, `{"serial":1,"lineage":"l"}`, bearer(adminToken))
			if rec.Code != tc.want {
				t.Fatalf("status %d, want %d; body %q", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}
