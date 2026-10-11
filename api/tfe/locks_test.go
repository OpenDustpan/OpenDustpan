package tfe_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/api/tfe"
	"opendustpan.org/opendustpan/namespace"
	"opendustpan.org/opendustpan/workspace"
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

func (conflictWorkspaces) ForceUnlock(_ context.Context, id workspace.ID) (workspace.Workspace, error) {
	if id.String() != "ws-test123" {
		return workspace.Workspace{}, workspace.ErrNotFound
	}

	return workspace.Workspace{}, workspace.ErrNotLocked
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
		ID:       id,
		Ref:      workspace.Ref{Namespace: ns, Name: name},
		Revision: 2,
	}, nil
}

// newConflictHandler returns a production Handler over the held workspace.
func newConflictHandler(t *testing.T) *tfe.Handler {
	t.Helper()

	handler, err := tfe.NewHandler(tfe.Config{
		Authenticator: tokenAuthenticator{},
		Authorizer:    allowAll(),
		Logger:        slog.New(slog.DiscardHandler),
		Namespaces:    newTestNamespaces(t),
		Workspaces:    conflictWorkspaces{stubWorkspaces: stubWorkspaces{}},
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
