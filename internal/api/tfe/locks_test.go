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

	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
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

func (conflictWorkspaces) Lock(
	_ context.Context, id workspace.ID, _, _ string,
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

func (rconflictWorkspaces conflictWorkspaces) ForceUnlock(
	ctx context.Context,
	id workspace.ID,
	by string,
	_ namespace.Overrides, _ func() error,
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
		CreatedAt: stubWorkspaceTime(),
		UpdatedAt: stubWorkspaceTime(),
		Lock: workspace.Lock{
			LockedAt: stubWorkspaceTime(),
			ID:       "lock-alice",
			Holder:   "alice",
			Reason:   "apply",
		},
		ID:        id,
		Ref:       workspace.Ref{Namespace: ns, Name: name},
		Revision:  2,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
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

	handler, err := tfe.NewHandler(tfe.Config{
		Authenticator:  tokenAuthenticator{},
		Authorizer:     allowAll(),
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     conflictWorkspaces{stubWorkspaces: stubWorkspaces{}},
		BaseURL:        baseurl.Resolver{},
		Audit:          nil,
		Gate:           nil,
		MaxStateBytes:  0,
		OnAuditFailure: nil,
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
			Subject:        "alice",
			DisplayName:    "Alice",
			Admin:          false,
			TokenID:        "",
			TokenExpiresAt: time.Time{},
		}, nil
	case "test-token-b":
		return identity.Principal{
			Subject:        "bob",
			DisplayName:    "Bob",
			Admin:          false,
			TokenID:        "",
			TokenExpiresAt: time.Time{},
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

func (r *raceWorkspaces) Lock(
	_ context.Context, id workspace.ID, holder, _ string,
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
		CreatedAt: stubWorkspaceTime(),
		UpdatedAt: stubWorkspaceTime(),
		Lock: workspace.Lock{
			LockedAt: stubWorkspaceTime(),
			ID:       "lock-test",
			Holder:   holder,
			Reason:   "",
		},
		ID:        workspace.ID("ws-test123"),
		Ref:       workspace.Ref{Namespace: ns, Name: name},
		Revision:  2,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
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
		Authenticator:  concurrentAuthenticator{},
		Authorizer:     allowAll(),
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     stores,
		BaseURL:        baseurl.Resolver{},
		Audit:          nil,
		Gate:           nil,
		MaxStateBytes:  0,
		OnAuditFailure: nil,
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
