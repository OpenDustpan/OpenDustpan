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

// revocableTokens signs in two access tokens and checks them as a running
// update does. The token "test-token" belongs to a principal with the token ID
// tok-1. The token "boot-token" is a bootstrap token with no token ID. A test
// calls end to make tok-1 unusable, as a revoke does.
type revocableTokens struct {
	ended map[string]bool
	mu    sync.Mutex
}

func newRevocableTokens() *revocableTokens {
	return &revocableTokens{ended: map[string]bool{}, mu: sync.Mutex{}}
}

func (a *revocableTokens) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	switch token {
	case "test-token":
		if a.ended["tok-1"] {
			return identity.Principal{}, identity.ErrUnauthenticated
		}

		return identity.Principal{
			TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
			Subject:        "tester",
			DisplayName:    "Tester",
			Admin:          false,
			TokenID:        "tok-1",
			TokenExpiresAt: time.Date(2100, time.January, 1, 0, 0, 0, 0, time.UTC),
			Kind:           "",
			Credential:     "",
			JobID:          "",
		}, nil
	case "boot-token":
		return identity.Principal{
			TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
			Subject:        "admin",
			DisplayName:    "Admin",
			Admin:          true,
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

// CheckActive implements pulumi.AccessTokenChecker.
func (a *revocableTokens) CheckActive(_ context.Context, id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if id != "tok-1" || a.ended[id] {
		return identity.ErrUnauthenticated
	}

	return nil
}

func (a *revocableTokens) end(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.ended[id] = true
}

// startWithToken creates a mutating update, starts it with the access token
// token, and returns its paths and lease token.
func startWithToken(t *testing.T, f updateFixture, token string) (updatePaths, string) {
	t.Helper()

	u, err := f.updates.Create(
		t.Context(),
		f.ref,
		stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
	)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	paths := mintedPaths(u.ID)

	status, body := f.doUpdate(t, updateRequest{
		Method: http.MethodPost, Target: paths.Start, Access: token, Lease: "", Body: "", UserAgent: "",
	})
	if status != http.StatusOK {
		t.Fatalf("start answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var started struct {
		Token string `json:"token"`
	}

	if err := json.Unmarshal([]byte(body), &started); err != nil || started.Token == "" {
		t.Fatalf("decode start: %v (%s)", err, body)
	}

	return paths, started.Token
}

// TestRevokedTokenEndsLeaseRequests reproduces #505. An access token starts an
// update, the token is revoked, and every later lease request must answer 401,
// as a renewal does. Before the fix, the lease routes never checked the access
// token, so the requests answered 200.
func TestRevokedTokenEndsLeaseRequests(t *testing.T) {
	t.Parallel()

	authn := newRevocableTokens()
	f := newUpdateFixtureWithAuthenticator(
		t,
		allowAll(),
		authn,
		fixtureOptions{manual: nil, checker: authn, limit: 0, cutoff: 0, replayer: nil, guard: nil},
	)
	paths, lease := startWithToken(t, f, "test-token")

	status, body := f.doUpdate(t, leaseRequest(http.MethodPost, paths.Renew, lease, `{"duration":60}`))
	if status != http.StatusOK {
		t.Fatalf("renew with a live token answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	authn.end("tok-1")

	requests := []struct {
		name string
		req  updateRequest
	}{
		{name: "renew", req: leaseRequest(http.MethodPost, paths.Renew, lease, `{"duration":60}`)},
		{name: "events", req: leaseRequest(http.MethodPost, paths.Events, lease, `{"events":[]}`)},
		{
			name: "checkpoint",
			req: leaseRequest(
				http.MethodPatch,
				paths.Checkpoint,
				lease,
				`{"version":3,"deployment":{"resources":[]}}`,
			),
		},
		{name: "complete", req: leaseRequest(http.MethodPost, paths.Complete, lease, `{"status":"succeeded"}`)},
	}

	for _, r := range requests {
		status, body := f.doUpdate(t, r.req)
		if status != http.StatusUnauthorized {
			t.Errorf("%s after revoke answers %d, want %d (%s)", r.name, status, http.StatusUnauthorized, body)
		}
	}
}

// TestBootstrapTokenUpdateSkipsTokenCheck proves that an update started with a
// bootstrap token has no token ID, so no token check runs on its lease requests.
func TestBootstrapTokenUpdateSkipsTokenCheck(t *testing.T) {
	t.Parallel()

	authn := newRevocableTokens()
	f := newUpdateFixtureWithAuthenticator(
		t,
		allowAll(),
		authn,
		fixtureOptions{manual: nil, checker: authn, limit: 0, cutoff: 0, replayer: nil, guard: nil},
	)
	paths, lease := startWithToken(t, f, "boot-token")

	authn.end("tok-1")

	status, body := f.doUpdate(t, leaseRequest(http.MethodPost, paths.Renew, lease, `{"duration":60}`))
	if status != http.StatusOK {
		t.Fatalf("renew of a bootstrap-started update answers %d, want %d (%s)", status, http.StatusOK, body)
	}
}
