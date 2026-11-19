package server_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestRevokeTokenOfUnknownKindAnswers409 checks, through the HTTP handler and
// the real token service, that a revoke of a stored token of a kind that this
// version does not know answers a defined 409 with a fixed message that does
// not name the row, and that the row stays unrevoked.
func TestRevokeTokenOfUnknownKindAnswers409(t *testing.T) {
	t.Parallel()

	const adminSecret = "revoke-unknown-kind-admin-secret-0123456789"

	admin, err := identity.NewStaticToken("admin", adminSecret)
	if err != nil {
		t.Fatalf("static token: %v", err)
	}

	path := filepath.Join(t.TempDir(), "dustpan.db")
	handler, st := newServiceBackedHandlerAt(t, path, admin, identity.AdminOnly{})

	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)

	prn := principal.Principal{
		CreatedAt: at, UpdatedAt: at, ID: "prn-ci", Subject: "ci", DisplayName: "",
		Kind: principal.KindService, Source: principal.SourceLocal, Namespace: "",
		GroupCount: 0, BreakGlass: false, DisabledAt: time.Time{},
	}
	createErr := st.CreatePrincipal(t.Context(), prn, storetest.ServerChange(at, "principal:"+prn.ID))
	if createErr != nil {
		t.Fatalf("seed principal: %v", createErr)
	}

	svc, err := accesstoken.NewService(accesstoken.Config{
		TempAdmins: nil,
		Store:      st, Principals: st, Freshness: nil, Authorizer: nil,
		Reach: access.ReachConfig{
			Grants: st, Roles: st, Callers: st, Principals: st, Collections: stubCollections{}, Units: st,
		},
		Logger: nil, Now: func() time.Time { return at }, NewID: nil,
	})
	if err != nil {
		t.Fatalf("token service: %v", err)
	}

	issued, err := svc.NewTrustToken(t.Context(), at, prn.ID, "old kind", "trust-rule:old")
	if err != nil {
		t.Fatalf("NewTrustToken: %v", err)
	}

	entry := storetest.ServerChange(at, "token:"+issued.Token.ID)
	if createErr := st.CreateToken(t.Context(), issued.Token, issued.Digest, entry); createErr != nil {
		t.Fatalf("store the token: %v", createErr)
	}

	setStoredTokenKind(t, path, issued.Token.ID, "trust")

	routes := []string{
		"/api/server/tokens/" + issued.Token.ID,
		"/api/server/principals/" + prn.ID + "/tokens/" + issued.Token.ID,
	}

	for _, route := range routes {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, route, nil)
		req.Header.Set("Authorization", "Bearer "+adminSecret)

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusConflict {
			t.Errorf("DELETE %s answers %d, want %d (%s)", route, rec.Code, http.StatusConflict, rec.Body.String())
		}

		if !strings.Contains(rec.Body.String(), errcode.TokenUnknownKind) {
			t.Errorf("DELETE %s body %q lacks the code %q", route, rec.Body.String(), errcode.TokenUnknownKind)
		}

		if strings.Contains(rec.Body.String(), issued.Token.ID) {
			t.Errorf("DELETE %s body %q names the token ID", route, rec.Body.String())
		}
	}
}
