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

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tofuhttp"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

const (
	adminToken = "admin-token-0123456789"
	// jobToken authenticates the job job-1 with a job token.
	jobToken = "job-token-0123456789"
	// jobIDOnAccessToken authenticates an access token whose principal names a job.
	jobIDOnAccessToken = "access-with-job-0123456789"
	userToken          = "user-token-0123456789"
	unitPath           = "/tofu/state/acme/net"
	lockOne            = "lock-one"
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
	// lockErr, when set, is what LockWithIDForJob answers.
	lockErr error
	// admitErr, when set, is what AdmitFirstWrite and AdmitFirstLock answer.
	admitErr error
	// created counts the calls of Create that made the unit.
	created int
	// lockJobs holds the job ID of each lock request.
	lockJobs []string
	// forced holds the audit entry template of each force-unlock.
	forced []audit.Entry
}

// newFakeUnits returns a service whose unit exists, unlocked, with both switches on.
func newFakeUnits(exists bool) *fakeUnits {
	ref := workspace.Ref{Namespace: "acme", Name: "net"}
	on := namespace.Overrides{Forced: true, Unlocked: true}

	return &fakeUnits{
		mu: sync.Mutex{},
		ws: workspace.Workspace{
			NamespaceID: "",
			Ref:         ref,
			ID:          "ws-1",
			CreatedAt:   time.Time{},
			UpdatedAt:   time.Time{},
			Lock:        unlockedLock(),
			LastRelease: workspace.NoRelease(),
			Revision:    0,
			Overrides:   on, Origin: identity.StateOrigin{Job: "", Commit: ""},
		},
		exists:    exists,
		hideOnce:  false,
		createErr: nil,
		writeErr:  nil,
		lockErr:   nil,
		admitErr:  nil,
		created:   0,
		lockJobs:  nil,
		forced:    nil,
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
func (f *fakeUnits) Create(_ context.Context, _ workspace.Ref, _ workspace.Placement) (workspace.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.createErr != nil {
		return workspace.Workspace{}, f.createErr
	}

	if f.exists {
		return workspace.Workspace{}, workspace.ErrAlreadyExists
	}

	f.exists = true
	f.created++

	return f.ws, nil
}

// AdmitFirstWrite answers admitErr, or nil.
func (f *fakeUnits) AdmitFirstWrite(_ context.Context, _ workspace.Ref, _ workspace.StreamInput) error {
	return f.admitErr
}

// AdmitFirstLock answers admitErr, or nil.
func (f *fakeUnits) AdmitFirstLock(_ context.Context, _ workspace.Ref, _ workspace.LockRequest) error {
	return f.admitErr
}

// LockWithIDForJob takes the lock under lockID, or answers ErrLocked. It keeps
// the job ID of the request.
func (f *fakeUnits) LockWithIDForJob(
	_ context.Context,
	_ workspace.ID,
	req workspace.LockRequest,
) (workspace.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	holder, reason, lockID := req.Holder, req.Reason, req.LockID
	f.lockJobs = append(f.lockJobs, req.JobID)

	if f.lockErr != nil {
		return workspace.Workspace{}, f.lockErr
	}

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

// RepeatsRelease applies the rule of the domain with the wall clock.
func (f *fakeUnits) RepeatsRelease(w workspace.Workspace, q workspace.ReleaseRepeat) bool {
	return w.RepeatsRelease(time.Now().UTC(), q)
}

// ForceUnlockLock releases the lock whoever holds it, when lockID is the lock.
//
//nolint:revive // The port takes the lock ID beside the caller, the allowance and the audit record.
func (f *fakeUnits) ForceUnlockLock(
	_ context.Context, _ workspace.ID, expect workspace.Lock, _ string, _ namespace.Overrides, _ func() error,
	entry audit.Entry,
) (workspace.Workspace, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.forced = append(f.forced, entry)

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

// WritableHere returns the namespace as Get does.
func (n fakeNamespaces) WritableHere(ctx context.Context, name namespace.Name) (namespace.Namespace, error) {
	return n.Get(ctx, name)
}

// Get returns the namespace with both switches on.
func (fakeNamespaces) Get(context.Context, namespace.Name) (namespace.Namespace, error) {
	return namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.Time{},
		Name:                "acme",
		PrimaryInstallation: "",
		FailoverVersion:     0,
		Overrides:           namespace.Overrides{Forced: true, Unlocked: true},
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
			Kind:           "",
			Credential:     "",
			JobID:          "",
		}, nil
	case jobToken, jobIDOnAccessToken:
		cred := identity.CredentialJobToken
		if token == jobIDOnAccessToken {
			// A principal that carries a job ID with another credential.
			cred = identity.CredentialAccessToken
		}

		return identity.Principal{
			TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
			Subject:        "alice",
			DisplayName:    "",
			Admin:          true,
			TokenID:        "",
			TokenExpiresAt: time.Time{},
			Kind:           "",
			Credential:     cred,
			JobID:          "job-1",
		}, nil
	case userToken:
		return identity.Principal{
			TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
			Subject:        "bob",
			DisplayName:    "",
			Admin:          false,
			TokenID:        "",
			TokenExpiresAt: time.Time{},
			Kind:           "",
			Credential:     "",
			JobID:          "",
		}, nil
	default:
		return identity.Principal{
			TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
			Subject:        "",
			DisplayName:    "",
			Admin:          false,
			TokenID:        "",
			TokenExpiresAt: time.Time{},
			Kind:           "",
			Credential:     "",
			JobID:          "",
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

// ListObjects gives an administrator every object and any other principal none.
func (userAuthz) ListObjects(_ context.Context, p identity.Principal, _ identity.Action) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

// newTestHandler returns a handler over units.
func newTestHandler(t *testing.T, units *fakeUnits) http.Handler {
	t.Helper()

	h, err := tofuhttp.NewHandler(tofuhttp.Config{
		Authenticator:  tokenAuth{},
		Authorizer:     userAuthz{},
		Logger:         slog.New(slog.DiscardHandler),
		Workspaces:     units,
		Namespaces:     fakeNamespaces{},
		Audit:          newTestAudit(),
		Gate:           nil,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		StateGuard:     stategate.Off("the test keeps no gates"),
		Placer:         testPlacer(),
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

	units := newFakeUnits(true)
	h := newTestHandler(t, units)

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

	// The backend passes the fields of the audit entry of the force-unlock. The
	// lock info has no field for a reason, so the reason is empty.
	units.mu.Lock()
	defer units.mu.Unlock()

	if len(units.forced) != 1 {
		t.Fatalf("force-unlock entries = %+v, want one", units.forced)
	}

	got := units.forced[0]
	if got.Action != "unit:write-forced" || got.Object != "opentofu:acme/net" || got.Kind != audit.KindChange ||
		got.Reason != "" {
		t.Fatalf("force-unlock entry = %+v, want the action, the unit, the kind change and no reason", got)
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

// TestWriteRefusalTexts proves that each cause of a refused write has its own
// text: a write with no lock says to send LOCK and names the switches, a
// forced write names the forced-write switches, and a client-encrypted state
// says why the server cannot fence it. No text of the store reaches the
// client.
func TestWriteRefusalTexts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want []string
	}{
		{
			name: "no lock and the switch off",
			err: workspace.NewOverrideRefusedError(
				workspace.OverrideUnlocked, "store text: secret detail", workspace.ErrNotLocked,
			),
			want: []string{
				"workspace is not locked: send LOCK first",
				"PUT /api/server/namespaces/{namespace}/override-switches", "unit:write-unlocked",
			},
		},
		{
			name: "no lock",
			err:  fmt.Errorf("write acme/web: %w", workspace.ErrNotLocked),
			want: []string{"workspace is not locked: send LOCK first"},
		},
		{
			name: "forced write",
			err:  workspace.NewOverrideRefusedError(workspace.OverrideForced, "store text: secret detail"),
			want: []string{
				"write refused: a forced write needs the forced-write switch",
				"PUT /api/v2/workspaces/{id}/override-switches", "unit:write-forced",
			},
		},
		{
			name: "client-encrypted state",
			err:  fmt.Errorf("store text: secret detail: %w", workspace.ErrClientEncrypted),
			want: []string{"the state is encrypted by the client, so the server cannot fence it"},
		},
		{
			name: "another override",
			err:  workspace.ErrOverrideRefused,
			want: []string{"write refused: an override is off or not granted"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			units := newFakeUnits(true)
			units.writeErr = tc.err
			h := newTestHandler(t, units)

			rec := do(h, http.MethodPost, unitPath, `{"serial":1,"lineage":"l"}`, bearer(adminToken))
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status %d, want 403; body %q", rec.Code, rec.Body.String())
			}

			body := rec.Body.String()
			for _, want := range tc.want {
				if !strings.Contains(body, want) {
					t.Errorf("body %q, want it to hold %q", body, want)
				}
			}

			if strings.Contains(body, "secret detail") {
				t.Errorf("body %q holds the text of the store", body)
			}
		})
	}
}

// TestFencedWriteStatuses proves a fenced LOCK answers 503 with a Retry-After
// time, and a fenced state write answers 409. OpenTofu reads a 409 on LOCK as
// a lock that another client holds, and it retries a 503, which reads the
// namespace again.
func TestFencedWriteStatuses(t *testing.T) {
	t.Parallel()

	fenced := fmt.Errorf("lock workspace ws-1: %w", namespace.ErrFenced)

	locking := newFakeUnits(true)
	locking.lockErr = fenced

	rec := do(newTestHandler(t, locking), "LOCK", unitPath, lockBody(lockOne, "alice"), bearer(adminToken))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("fenced LOCK: status %d, want %d; body %q", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}

	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("fenced LOCK answers no Retry-After time")
	}

	if !strings.Contains(rec.Body.String(), namespace.ErrFenced.Error()) {
		t.Fatalf("fenced LOCK body %q, want it to hold %q", rec.Body.String(), namespace.ErrFenced.Error())
	}

	writing := newFakeUnits(true)
	writing.writeErr = fenced

	rec = do(newTestHandler(t, writing), http.MethodPost, unitPath, `{"serial":1,"lineage":"l"}`, bearer(adminToken))
	if rec.Code != http.StatusConflict {
		t.Fatalf("fenced POST: status %d, want %d; body %q", rec.Code, http.StatusConflict, rec.Body.String())
	}
}

// TestLockRefusalsAreAudited proves the domain layer of the denial audit: an
// UNLOCK that the middleware allows but the workspace refuses writes a denied
// entry with the reason, and the answer stays 409.
func TestLockRefusalsAreAudited(t *testing.T) {
	t.Parallel()

	store := &recordingAudit{Store: nil, err: nil, entries: nil, mu: sync.Mutex{}}

	h, err := tofuhttp.NewHandler(tofuhttp.Config{
		Authenticator: tokenAuth{}, Authorizer: userAuthz{}, Logger: slog.New(slog.DiscardHandler),
		Workspaces: newFakeUnits(true), Namespaces: fakeNamespaces{}, Audit: store,
		Gate: nil, OnAuditFailure: nil,
		DenialLimiter: nil,
		StateGuard:    stategate.Off("the test keeps no gates"),
		Placer:        testPlacer(),
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	if rec := do(h, "LOCK", unitPath, lockBody(lockOne, "alice"), bearer(adminToken)); rec.Code != http.StatusOK {
		t.Fatalf("LOCK: status %d, want 200", rec.Code)
	}

	rec := do(h, "UNLOCK", unitPath, lockBody("lock-two", "alice"), bearer(adminToken))
	if rec.Code != http.StatusConflict {
		t.Fatalf("UNLOCK with another lock ID: status %d, want 409", rec.Code)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	if len(store.entries) != 1 {
		t.Fatalf("entries = %+v, want one denied entry", store.entries)
	}

	e := store.entries[0]
	if e.Outcome != audit.OutcomeDenied || !strings.HasPrefix(e.Reason, "unlock names a lock") ||
		e.Object != "opentofu:acme/net" || e.Action != string(identity.ActionUnitLock) {
		t.Fatalf("entry = %+v, want denied, the unit, the lock action and the reason of the lock refusal", e)
	}
}

// TestOverrideRefusalsAreAudited proves the domain layer of the denial audit:
// a state write that the workspace refuses because the forced-write switch is
// off answers 403 and writes a denied entry with the forced-write action.
func TestOverrideRefusalsAreAudited(t *testing.T) {
	t.Parallel()

	store := &recordingAudit{Store: nil, err: nil, entries: nil, mu: sync.Mutex{}}
	units := newFakeUnits(true)
	units.writeErr = fmt.Errorf("%w: the forced-write switch is off", workspace.ErrOverrideRefused)

	h, err := tofuhttp.NewHandler(tofuhttp.Config{
		Authenticator: tokenAuth{}, Authorizer: userAuthz{}, Logger: slog.New(slog.DiscardHandler),
		Workspaces: units, Namespaces: fakeNamespaces{}, Audit: store,
		Gate: nil, OnAuditFailure: nil,
		DenialLimiter: nil,
		StateGuard:    stategate.Off("the test keeps no gates"),
		Placer:        testPlacer(),
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	rec := do(h, http.MethodPost, unitPath, `{"version":4}`, bearer(userToken))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("refused override: status %d, want 403; body %q", rec.Code, rec.Body.String())
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	if len(store.entries) != 1 {
		t.Fatalf("entries = %+v, want one denied entry", store.entries)
	}

	e := store.entries[0]
	if e.Outcome != audit.OutcomeDenied || e.Action != string(identity.ActionUnitWriteForced) ||
		e.Kind != audit.KindOverride || e.Object != "opentofu:acme/net" || e.Subject != "bob" {
		t.Fatalf("entry = %+v, want denied, kind override, the forced-write action and the unit", e)
	}
}

// TestRefusedUnlockedWriteIsAudited proves a write without the lock that the
// workspace refuses writes a denied entry with the unlocked-write action when
// the switch is off and when the caller lacks the permission, and that a forced
// refusal keeps the forced-write action.
func TestRefusedUnlockedWriteIsAudited(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantAction string
	}{
		{
			name: "permission missing",
			err: workspace.NewOverrideRefusedError(
				workspace.OverrideUnlocked,
				"workspace is not locked: needs `unit:write-unlocked`",
				workspace.ErrNotLocked,
			),
			wantStatus: http.StatusForbidden,
			wantAction: string(identity.ActionUnitWriteUnlocked),
		},
		{
			name: "switch off",
			err: workspace.NewOverrideRefusedError(
				workspace.OverrideUnlocked, "write refused: the unlocked-write switch is off",
			),
			wantStatus: http.StatusForbidden,
			wantAction: string(identity.ActionUnitWriteUnlocked),
		},
		{
			name: "forced",
			err: workspace.NewOverrideRefusedError(
				workspace.OverrideForced, "write refused: the forced-write switch is off",
			),
			wantStatus: http.StatusForbidden,
			wantAction: string(identity.ActionUnitWriteForced),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := &recordingAudit{Store: nil, err: nil, entries: nil, mu: sync.Mutex{}}
			units := newFakeUnits(true)
			units.writeErr = tc.err

			h, err := tofuhttp.NewHandler(tofuhttp.Config{
				Authenticator: tokenAuth{}, Authorizer: userAuthz{}, Logger: slog.New(slog.DiscardHandler),
				Workspaces: units, Namespaces: fakeNamespaces{}, Audit: store,
				Gate: nil, OnAuditFailure: nil,
				DenialLimiter: nil,
				StateGuard:    stategate.Off("the test keeps no gates"),
				Placer:        testPlacer(),
			})
			if err != nil {
				t.Fatalf("new handler: %v", err)
			}

			rec := do(h, http.MethodPost, unitPath, `{"version":4}`, bearer(userToken))
			if rec.Code != tc.wantStatus {
				t.Fatalf("status %d, want %d; body %q", rec.Code, tc.wantStatus, rec.Body.String())
			}

			store.mu.Lock()
			defer store.mu.Unlock()

			if len(store.entries) != 1 {
				t.Fatalf("entries = %+v, want one denied entry", store.entries)
			}

			if e := store.entries[0]; e.Outcome != audit.OutcomeDenied || e.Action != tc.wantAction ||
				e.Kind != audit.KindOverride {
				t.Fatalf("entry = %+v, want denied override entry with action %s", e, tc.wantAction)
			}
		})
	}
}

// TestRefusedFirstRequestCreatesNoUnit proves that the handler creates the unit
// for a first LOCK or POST only when the admission checks of the service pass:
// a refusal answers its status and the unit does not exist.
func TestRefusedFirstRequestCreatesNoUnit(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		method string
		path   string
		body   string
		refuse error
		want   int
	}{
		{
			"POST with no lock",
			http.MethodPost,
			unitPath,
			`{"serial":1,"lineage":"l"}`,
			workspace.ErrOverrideRefused,
			http.StatusForbidden,
		},
		{
			"POST that names a lock",
			http.MethodPost,
			unitPath + "?ID=x",
			`{"serial":1,"lineage":"l"}`,
			workspace.ErrLockMismatch,
			http.StatusConflict,
		},
		{"LOCK under a manual lock", "LOCK", unitPath, lockBody(lockOne, "ci"), workspace.ErrLocked, http.StatusLocked},
		{
			"LOCK with a bad lock ID",
			"LOCK",
			unitPath,
			lockBody("a b", "ci"),
			workspace.ErrInvalidLockID,
			http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			units := newFakeUnits(false)
			units.admitErr = tc.refuse
			h := newTestHandler(t, units)

			rec := do(h, tc.method, tc.path, tc.body, bearer(adminToken))
			if rec.Code != tc.want {
				t.Fatalf("%s: status %d, want %d: %s", tc.method, rec.Code, tc.want, rec.Body.String())
			}

			if units.exists || units.created != 0 {
				t.Fatalf("a refused %s created the unit (created %d)", tc.method, units.created)
			}
		})
	}
}

// TestLockedBodyNamesTheRefusingLock proves that a refused LOCK answers with
// the lock that the refusal names, also when a later read of the unit would
// see another lock: the answer and the refusal come from one revision.
func TestLockedBodyNamesTheRefusingLock(t *testing.T) {
	t.Parallel()

	units := newFakeUnits(true)
	units.ws.Lock = workspace.Lock{LockedAt: time.Now().UTC(), ID: "lock-now", Holder: "carol", Reason: ""}
	units.lockErr = &workspace.LockRefusedError{
		Lock: workspace.Lock{LockedAt: time.Now().UTC(), ID: "lock-dana", Holder: "dana", Reason: ""},
		Err:  workspace.ErrLocked,
	}

	rec := do(newTestHandler(t, units), "LOCK", unitPath, lockBody("lock-two", "bob"), bearer(userToken))
	if rec.Code != http.StatusLocked {
		t.Fatalf("LOCK of a held unit: status %d, want 423", rec.Code)
	}

	assertLockBody(t, rec.Body.Bytes(), "lock-dana", "dana")
}
