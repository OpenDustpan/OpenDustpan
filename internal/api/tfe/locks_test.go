package tfe_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// conflictWorkspaces locks ws-test123 for alice: Lock answers ErrLocked and
// Unlock by another answers ErrLockedByOther, while GetByID reports the held
// lock. ForceUnlock answers ErrNotLocked, as for an unlocked workspace.
type conflictWorkspaces struct {
	stubWorkspaces
}

func (conflictWorkspaces) LockForJob(
	_ context.Context, id workspace.ID, _, _, _ string,
) (workspace.Workspace, error) {
	if id.String() != "ws-test123" {
		return workspace.Workspace{}, workspace.ErrNotFound
	}

	return workspace.Workspace{}, workspace.ErrLocked
}

func (conflictWorkspaces) Unlock(
	_ context.Context, id workspace.ID, _ string,
) (workspace.Workspace, error) {
	if id.String() != "ws-test123" {
		return workspace.Workspace{}, workspace.ErrNotFound
	}

	return workspace.Workspace{}, workspace.ErrLockedByOther
}

//nolint:revive // The signature of the workspace port that the handler reads.
func (rconflictWorkspaces conflictWorkspaces) ForceUnlockLock(
	ctx context.Context,
	id workspace.ID,
	_ workspace.Lock,
	by string,
	_ namespace.Overrides, _ func() error, _ audit.Entry,
) (workspace.Workspace, bool, error) {
	err := rconflictWorkspaces.forceUnlockUnfenced(ctx, id, by)

	return workspace.Workspace{}, false, err
}

func (conflictWorkspaces) GetByID(_ context.Context, id workspace.ID) (workspace.Workspace, error) {
	if id.String() != "ws-test123" {
		return workspace.Workspace{}, workspace.ErrNotFound
	}

	ns, err := namespace.ParseName("acme")
	if err != nil {
		return workspace.Workspace{}, err
	}

	name, err := workspace.ParseName("web")
	if err != nil {
		return workspace.Workspace{}, err
	}

	return workspace.Workspace{
		NamespaceID: "",
		CreatedAt:   stubWorkspaceTime(),
		UpdatedAt:   stubWorkspaceTime(),
		Lock: workspace.Lock{
			LockedAt: stubWorkspaceTime(),
			ID:       "lock-alice",
			Holder:   "alice",
			Reason:   "apply",
		},
		LastRelease: workspace.NoRelease(),
		ID:          id,
		Ref:         workspace.Ref{Namespace: ns, Name: name},
		Revision:    2,
		Overrides: namespace.Overrides{
			Forced:   false,
			Unlocked: false,
		},
		Origin: identity.StateOrigin{Job: "", Commit: ""},
	}, nil
}

func (conflictWorkspaces) forceUnlockUnfenced(_ context.Context, id workspace.ID, _ string) error {
	if id.String() != "ws-test123" {
		return workspace.ErrNotFound
	}

	return workspace.ErrNotLocked
}

// newConflictHandler returns a production Handler over the held workspace.
func newConflictHandler(t *testing.T) *tfe.Handler {
	t.Helper()

	return newConflictHandlerOver(t, conflictWorkspaces{stubWorkspaces: stubWorkspaces{}})
}

// newConflictHandlerOver returns a production Handler over workspaces.
func newConflictHandlerOver(t *testing.T, workspaces tfe.WorkspaceService) *tfe.Handler {
	t.Helper()

	handler, err := tfe.NewHandler(tfe.Config{
		Registry:       nil,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     allowAll(),
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     workspaces,
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

// lockErrorDetail posts to target and returns the 409 detail.
func lockErrorDetail(t *testing.T, handler *tfe.Handler, target string) string {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target, strings.NewReader(`{"data":{}}`))
	req.Header.Set("Authorization", "Bearer test-token")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("request answers %d, want %d: %s", rec.Code, http.StatusConflict, rec.Body.String())
	}

	var doc jsonAPIErrorDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode error body: %v", err)
	}

	if len(doc.Errors) != 1 {
		t.Fatalf("error document holds %d errors, want 1", len(doc.Errors))
	}

	return doc.Errors[0].Detail
}

// TestLockConflictDetail proves a lock on a held workspace answers 409 with a
// detail holding "is locked by User" and the holder, the words go-tfe parses.
func TestLockConflictDetail(t *testing.T) {
	t.Parallel()

	handler := newConflictHandler(t)
	detail := lockErrorDetail(t, handler, "/api/v2/workspaces/ws-test123/actions/lock")

	if !strings.Contains(detail, "is locked by User") || !strings.Contains(detail, "alice") {
		t.Fatalf("lock detail = %q, want %q and the holder", detail, "is locked by User")
	}
}

// TestUnlockConflictDetail proves an unlock by another principal answers 409
// with a detail holding "is locked by User" and the holder.
func TestUnlockConflictDetail(t *testing.T) {
	t.Parallel()

	handler := newConflictHandler(t)
	detail := lockErrorDetail(t, handler, "/api/v2/workspaces/ws-test123/actions/unlock")

	if !strings.Contains(detail, "is locked by User") || !strings.Contains(detail, "alice") {
		t.Fatalf("unlock detail = %q, want %q and the holder", detail, "is locked by User")
	}
}

// movedLockWorkspaces refuses an unlock with the lock of dana, while a later
// read of the workspace sees the lock of alice.
type movedLockWorkspaces struct {
	conflictWorkspaces
}

func (movedLockWorkspaces) Unlock(context.Context, workspace.ID, string) (workspace.Workspace, error) {
	return workspace.Workspace{}, &workspace.LockRefusedError{
		Lock: workspace.Lock{LockedAt: stubWorkspaceTime(), ID: "lock-dana", Holder: "dana", Reason: ""},
		Err:  workspace.ErrLockedByOther,
	}
}

// TestUnlockConflictNamesTheRefusingLock proves that a refused unlock names
// the holder of the lock that refused it, not the holder that a later read
// sees: the answer and the refusal come from one revision.
func TestUnlockConflictNamesTheRefusingLock(t *testing.T) {
	t.Parallel()

	handler := newConflictHandlerOver(t, movedLockWorkspaces{conflictWorkspaces{stubWorkspaces: stubWorkspaces{}}})
	detail := lockErrorDetail(t, handler, "/api/v2/workspaces/ws-test123/actions/unlock")

	if !strings.Contains(detail, "is locked by User dana") {
		t.Fatalf("unlock detail = %q, want the holder dana of the refusal", detail)
	}
}

// TestForceUnlockUnlocked proves a force-unlock of an unlocked workspace
// answers 409.
func TestForceUnlockUnlocked(t *testing.T) {
	t.Parallel()

	handler := newConflictHandler(t)

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/api/v2/workspaces/ws-test123/actions/force-unlock", http.NoBody,
	)
	req.Header.Set("Authorization", "Bearer test-token")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	checkJSONAPIError(t, rec, http.StatusConflict)
}

// concurrentAuthenticator signs in two test tokens as two subjects, so two
// concurrent lock requests contend as two principals.
type concurrentAuthenticator struct{}

func (concurrentAuthenticator) AuthenticateToken(
	_ context.Context, raw string,
) (identity.Principal, error) {
	switch raw {
	case "test-token-a":
		return identity.Principal{
			TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
			Subject:        "alice",
			DisplayName:    "Alice",
			Admin:          false,
			TokenID:        "",
			TokenExpiresAt: time.Time{},
			Kind:           "",
			Credential:     "",
			JobID:          "",
		}, nil
	case "test-token-b":
		return identity.Principal{
			TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
			Subject:        "bob",
			DisplayName:    "Bob",
			Admin:          false,
			TokenID:        "",
			TokenExpiresAt: time.Time{},
			Kind:           "",
			Credential:     "",
			JobID:          "",
		}, nil
	default:
		return identity.Principal{}, identity.ErrUnauthenticated
	}
}

// raceWorkspaces holds one workspace, acme/web with ID ws-test123. The first
// Lock wins and records its holder. Every later Lock loses the
// compare-and-swap and answers ErrConcurrentModification, as a racing loser
// does. GetByID reports the current holder, so the conflict detail names it.
type raceWorkspaces struct {
	stubWorkspaces

	mu     sync.Mutex
	holder string
}

func (r *raceWorkspaces) LockForJob(
	_ context.Context, id workspace.ID, holder, _, _ string,
) (workspace.Workspace, error) {
	if id.String() != "ws-test123" {
		return workspace.Workspace{}, workspace.ErrNotFound
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.holder != "" {
		return workspace.Workspace{}, workspace.ErrConcurrentModification
	}

	r.holder = holder

	return r.lockedWorkspace(holder), nil
}

func (r *raceWorkspaces) GetByID(_ context.Context, id workspace.ID) (workspace.Workspace, error) {
	if id.String() != "ws-test123" {
		return workspace.Workspace{}, workspace.ErrNotFound
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.holder == "" {
		return stubWorkspaceByID(id)
	}

	return r.lockedWorkspace(r.holder), nil
}

// lockedWorkspace returns acme/web held by holder.
func (r *raceWorkspaces) lockedWorkspace(holder string) workspace.Workspace {
	ns, _ := namespace.ParseName("acme")  //nolint:errcheck // The test namespace is valid.
	name, _ := workspace.ParseName("web") //nolint:errcheck // The test workspace name is valid.

	return workspace.Workspace{
		NamespaceID: "",
		CreatedAt:   stubWorkspaceTime(),
		UpdatedAt:   stubWorkspaceTime(),
		Lock: workspace.Lock{
			LockedAt: stubWorkspaceTime(),
			ID:       "lock-test",
			Holder:   holder,
			Reason:   "",
		},
		LastRelease: workspace.NoRelease(),
		ID:          workspace.ID("ws-test123"),
		Ref:         workspace.Ref{Namespace: ns, Name: name},
		Revision:    2,
		Overrides: namespace.Overrides{
			Forced:   false,
			Unlocked: false,
		},
		Origin: identity.StateOrigin{Job: "", Commit: ""},
	}
}

// lockOutcome is the status and body of one concurrent lock request.
type lockOutcome struct {
	code int
	body string
}

// postConcurrentLock posts one lock request as token and returns its outcome.
func postConcurrentLock(handler *tfe.Handler, target, token string, start <-chan struct{}) lockOutcome {
	<-start

	req := httptest.NewRequestWithContext(
		context.Background(), http.MethodPost, target, strings.NewReader(`{"data":{}}`),
	)
	req.Header.Set("Authorization", "Bearer "+token)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return lockOutcome{code: rec.Code, body: rec.Body.String()}
}

// splitOutcomes sorts two lock outcomes into the winner (200) and the loser
// (409), failing the test for any other status or shape. The two outcomes
// stay unnamed, as nonamedreturns requires; the first is the winner, the
// second is the loser.
//
//nolint:revive // Unnamed two-outcome result per nonamedreturns; order is documented.
func splitOutcomes(t *testing.T, outs []lockOutcome) (lockOutcome, lockOutcome) {
	t.Helper()

	var won, lost *lockOutcome

	for i := range outs {
		switch outs[i].code {
		case http.StatusOK:
			won = &outs[i]
		case http.StatusConflict:
			lost = &outs[i]
		default:
			t.Fatalf("request %d answers %d, want 200 or 409: %s", i, outs[i].code, outs[i].body)
		}
	}

	if won == nil || lost == nil {
		t.Fatalf("outcomes = %+v, want one 200 and one 409", outs)
	}

	return *won, *lost
}

// checkHeldDetail proves the 409 body holds "is locked by User" and one of
// the contending holders, the words the CLI waits and retries on.
func checkHeldDetail(t *testing.T, body string) {
	t.Helper()

	var doc jsonAPIErrorDoc
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode error body: %v", err)
	}

	if len(doc.Errors) != 1 {
		t.Fatalf("error document holds %d errors, want 1", len(doc.Errors))
	}

	detail := doc.Errors[0].Detail

	if !strings.Contains(detail, "is locked by User") {
		t.Fatalf("lock detail = %q, want %q", detail, "is locked by User")
	}

	if !strings.Contains(detail, "alice") && !strings.Contains(detail, "bob") {
		t.Fatalf("lock detail = %q, want the winning holder", detail)
	}
}

// TestConcurrentLocksReportHeldLock proves two concurrent lock requests leave
// one winner with 200 and one loser with 409: the loser lost the
// compare-and-swap, and its detail holds "is locked by User" and the winner,
// the words the CLI waits and retries on.
func TestConcurrentLocksReportHeldLock(t *testing.T) {
	t.Parallel()

	stores := &raceWorkspaces{stubWorkspaces: stubWorkspaces{}, mu: sync.Mutex{}, holder: ""}

	handler, err := tfe.NewHandler(tfe.Config{
		Registry:       nil,
		Authenticator:  concurrentAuthenticator{},
		Authorizer:     allowAll(),
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     stores,
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

	const target = "/api/v2/workspaces/ws-test123/actions/lock"

	tokens := []string{"test-token-a", "test-token-b"}
	outs := make([]lockOutcome, len(tokens))
	start := make(chan struct{})

	var wg sync.WaitGroup

	for i, token := range tokens {
		wg.Go(func() {
			outs[i] = postConcurrentLock(handler, target, token, start)
		})
	}

	close(start)
	wg.Wait()

	_, loser := splitOutcomes(t, outs)
	checkHeldDetail(t, loser.body)
}
