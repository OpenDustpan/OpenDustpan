package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/freshstep"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// lateAuthenticator forwards to an authenticator that the test sets after the
// handler exists.
type lateAuthenticator struct {
	mu   sync.Mutex
	next identity.Authenticator
}

func (a *lateAuthenticator) AuthenticateToken(ctx context.Context, token string) (identity.Principal, error) {
	a.mu.Lock()
	next := a.next
	a.mu.Unlock()

	return next.AuthenticateToken(ctx, token)
}

func (a *lateAuthenticator) set(next identity.Authenticator) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.next = next
}

// TestBearerOfOldKindTokenAnswers401 checks, through the HTTP handler and the
// real token service, that a stored token of a kind that this version does not
// know does not sign in: the answer is 401 on a route that any caller of a
// valid token can reach.
func TestBearerOfOldKindTokenAnswers401(t *testing.T) {
	t.Parallel()

	late := &lateAuthenticator{mu: sync.Mutex{}, next: nil}
	path := filepath.Join(t.TempDir(), "dustpan.db")
	handler, st := newServiceBackedHandlerAt(t, path, late, identity.AdminOnly{}, nil...)

	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)

	prn := principal.Principal{
		CreatedAt: at, UpdatedAt: at, ID: "prn-ci", Subject: "ci", DisplayName: "",
		Kind: principal.KindService, Source: principal.SourceLocal, Namespace: "",
		GroupCount: 0, BreakGlass: false, DisabledAt: time.Time{},
	}
	if err := st.CreatePrincipal(t.Context(), prn, storetest.ServerChange(at, "principal:"+prn.ID)); err != nil {
		t.Fatalf("seed principal: %v", err)
	}

	svc, err := accesstoken.NewService(accesstoken.Config{
		Makers:     nil,
		TempAdmins: nil,
		Store:      st, Principals: st, Freshness: nil, Authorizer: nil,
		Reach: access.ReachConfig{
			Settings: access.FixedSettings(true),
			Grants:   st, Roles: st, Callers: st, Principals: st, Collections: stubCollections{}, Units: st,
		},
		Logger: nil, Now: func() time.Time { return at }, NewID: nil,
		Guard: freshstep.Guard{},
	})
	if err != nil {
		t.Fatalf("token service: %v", err)
	}

	late.set(svc)

	issued, err := svc.NewTrustToken(t.Context(), at, prn.ID, "old kind", "trust-rule:old")
	if err != nil {
		t.Fatalf("NewTrustToken: %v", err)
	}

	entry := storetest.ServerChange(at, "token:"+issued.Token.ID)

	if err := st.CreateToken(t.Context(), issued.Token, issued.Digest, entry); err != nil {
		t.Fatalf("store the token: %v", err)
	}

	setStoredTokenKind(t, path, issued.Token.ID, "trust")

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/server/roles", nil)
	req.Header.Set("Authorization", "Bearer "+issued.Secret.Reveal())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf(
			"GET with an old-kind token answers %d, want %d (%s)",
			rec.Code,
			http.StatusUnauthorized,
			rec.Body.String(),
		)
	}
}
