package pulumi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/stack"
)

// revocableAuthenticator signs in the test access token until revoke runs.
// After that, every request with the token answers unauthenticated, as a
// revoked token does.
type revocableAuthenticator struct {
	mu      sync.Mutex
	revoked bool
}

func (a *revocableAuthenticator) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if token != "test-token" || a.revoked {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return identity.Principal{
		Subject:        "tester",
		DisplayName:    "Tester",
		Admin:          false,
		TokenID:        "tok-1",
		TokenExpiresAt: time.Date(2100, time.January, 1, 0, 0, 0, 0, time.UTC),
	}, nil
}

func (a *revocableAuthenticator) revoke() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.revoked = true
}

// TestLeaseSurvivesAccessTokenRevoke reproduces #505. An access token starts
// an update, the token is revoked, and the update's lease renewal must then
// answer 401. The lease routes never resolve the access token, so today the
// renewal still answers 200.
func TestLeaseSurvivesAccessTokenRevoke(t *testing.T) {
	t.Parallel()

	authn := &revocableAuthenticator{mu: sync.Mutex{}, revoked: false}
	f := newUpdateFixtureWithAuthenticator(t, allowAll(), authn, 0, 0)

	u, err := f.updates.Create(t.Context(), f.ref, stack.Plan{
		Kind: stack.KindPreview, DryRun: false, CreatedBy: "", RestoredFrom: 0,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	paths := mintedPaths(u.ID)

	status, body := f.doUpdate(t, accessRequest(http.MethodPost, paths.Start, ""))
	if status != http.StatusOK {
		t.Fatalf("start answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var started struct {
		Token string `json:"token"`
	}

	if err := json.Unmarshal([]byte(body), &started); err != nil || started.Token == "" {
		t.Fatalf("decode start: %v (%s)", err, body)
	}

	authn.revoke()

	status, body = f.doUpdate(t, accessRequest(http.MethodGet, paths.Get, ""))
	if status != http.StatusUnauthorized {
		t.Fatalf("get after revoke answers %d, want %d (%s): the revoke did not apply to new requests",
			status, http.StatusUnauthorized, body)
	}

	status, body = f.doUpdate(t, leaseRequest(http.MethodPost, paths.Renew, started.Token, `{"duration":60}`))
	if status != http.StatusUnauthorized {
		t.Fatalf("renew after revoke answers %d, want %d (%s): the running update keeps its lease",
			status, http.StatusUnauthorized, body)
	}
}
