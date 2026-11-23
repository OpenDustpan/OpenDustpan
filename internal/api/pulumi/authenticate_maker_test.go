package pulumi_test

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/stack"
)

// authenticateHarness is the update routes with the real token service as the
// authenticator, on a real store. The service token has a real secret, so each
// request runs the check of the maker at the authentication of the token.
type authenticateHarness struct {
	f      updateFixture
	st     *sqlite.Store
	makers *switchMaker
	path   string
	secret string
	id     string
}

func newAuthenticateHarness(t *testing.T) authenticateHarness {
	t.Helper()

	st := openMakerStore(t)
	seedMakerToken(t, st)

	makers := &switchMaker{can: atomic.Bool{}, fail: atomic.Bool{}}
	makers.can.Store(true)

	svc, err := accesstoken.NewService(accesstoken.Config{
		Makers: makers, TempAdmins: nil, Store: st, Principals: st, Freshness: noFreshStepGuard(t), Authorizer: nil,
		Reach: access.ReachConfig{
			Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil,
		},
		Logger: nil, Now: makerAt, NewID: nil,
	})
	if err != nil {
		t.Fatalf("token service: %v", err)
	}

	admin := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: makerAt(), Subject: "maker", DisplayName: "maker",
		Admin: true, TokenID: "", TokenExpiresAt: makerAt(), Kind: "", Credential: "", JobID: "",
	}
	entry := audit.Entry{
		At: makerAt(), Namespace: "", Subject: "maker", IDPSubject: "", TokenID: "",
		Action: string(identity.ActionServerManageTokens), Object: "", Version: "", ValuePath: "",
		ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1", UserAgent: "test", Channel: audit.ChannelAPI, Kind: "",
		Outcome: "", RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0,
		Reason: "", PrincipalKind: "", JobID: "", Origin: nil,
	}

	tok, secret, err := svc.Create(t.Context(), admin, entry, accesstoken.ScopeService, accesstoken.Input{
		ExpiresAt: makerAt().Add(accesstoken.DefaultLifetime), PrincipalID: "prn-ci", Name: "signin",
	})
	if err != nil {
		t.Fatalf("create the service token: %v", err)
	}

	f := newUpdateFixtureWithAuthenticator(t, allowAll(), svc, fixtureOptions{
		manual: nil, checker: svc, limit: 0, cutoff: 0, replayer: nil, guard: nil, placer: nil, placement: nil,
	})

	plan := stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""}

	u, err := f.updates.Create(t.Context(), f.ref, plan)
	if err != nil {
		t.Fatalf("create update: %v", err)
	}

	return authenticateHarness{
		f: f, st: st, makers: makers, path: mintedPaths(u.ID).Get, secret: secret.Reveal(), id: tok.ID,
	}
}

// get sends one authenticated read with the secret of the service token.
func (h *authenticateHarness) get(t *testing.T) (int, string) {
	t.Helper()

	return h.f.doUpdate(t, updateRequest{
		Method: http.MethodGet, Target: h.path, Access: h.secret, Lease: "", Body: "", UserAgent: "",
	})
}

// state returns whether the service token is revoked and the number of audit
// entries of its revocation.
func (h *authenticateHarness) state(t *testing.T) (bool, int) {
	t.Helper()

	got, err := h.st.GetToken(t.Context(), h.id)
	if err != nil {
		t.Fatalf("get token: %v", err)
	}

	listing, err := h.st.List(t.Context(), audit.ServerScope, audit.Page{Size: audit.MaxPageSize, Before: 0})
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}

	n := 0

	for i := range listing.Entries {
		e := &listing.Entries[i]
		if e.Subject == accesstoken.MakerSubject && strings.HasSuffix(e.Object, "#token:"+h.id) {
			n++
		}
	}

	return !got.RevokedAt.IsZero(), n
}

// TestAuthenticateMakerCheckErrorAnswersServerError sends a request with a
// real service token while the check of the maker cannot decide. The answer is
// a server error with no word of the store error, not the 401 of a revoked
// token. The token is kept and no entry is written. When the check decides
// again, the same token signs in.
func TestAuthenticateMakerCheckErrorAnswersServerError(t *testing.T) {
	t.Parallel()

	h := newAuthenticateHarness(t)

	h.makers.fail.Store(true)

	for range 2 {
		status, body := h.get(t)
		if status < http.StatusInternalServerError {
			t.Fatalf("a request while the check cannot decide answers %d, want a server error (%s)", status, body)
		}

		if strings.Contains(body, errMakerStore.Error()) || strings.Contains(body, "maker") {
			t.Errorf("the body names the check: %s", body)
		}
	}

	if revoked, entries := h.state(t); revoked || entries != 0 {
		t.Errorf("after a check that cannot decide: revoked = %v, entries = %d, want kept with none", revoked, entries)
	}

	h.makers.fail.Store(false)

	if status, body := h.get(t); status != http.StatusOK {
		t.Fatalf("a request after the check decides answers %d, want %d (%s)", status, http.StatusOK, body)
	}
}

// TestAuthenticateMakerWithNoPathAnswers401 sends a request with a real
// service token whose maker lost the path. The answer is the 401 of the CLI,
// and the token is revoked with one audit entry.
func TestAuthenticateMakerWithNoPathAnswers401(t *testing.T) {
	t.Parallel()

	h := newAuthenticateHarness(t)

	h.makers.can.Store(false)

	status, body := h.get(t)
	wantCliRefusal(t, "a request with no path", status, body)

	if revoked, entries := h.state(t); !revoked || entries != 1 {
		t.Errorf("after the refusal: revoked = %v, entries = %d, want revoked with one", revoked, entries)
	}
}
