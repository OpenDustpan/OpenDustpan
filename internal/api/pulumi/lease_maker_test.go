package pulumi_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// makerToken is the ID of the service token that the maker made.
const makerToken = "tok-ci-maker"

// makerAt is the fixed time of the rows of these tests, and the clock of the
// token service. It is far in the future, so the token never expires while the
// tests run, and no test reads the wall clock.
func makerAt() time.Time {
	return time.Date(2030, time.January, 2, 12, 0, 0, 0, time.UTC)
}

// errMakerStore is the error of a maker check that cannot decide.
var errMakerStore = errors.New("the grant store is not available")

// switchMaker answers the act-as path of the maker. A test turns it off as a
// direct change in the store does: no run of RevokeLostMakers follows it.
type switchMaker struct {
	can  atomic.Bool
	fail atomic.Bool
}

func (m *switchMaker) CanActAs(context.Context, string, string) (bool, error) {
	if m.fail.Load() {
		return false, errMakerStore
	}

	return m.can.Load(), nil
}

// makerAuthn signs in the service token "service-token" with the ID makerToken.
type makerAuthn struct{}

func (makerAuthn) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	if token != "service-token" {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject: "ci-deploy", DisplayName: "ci-deploy", Admin: false,
		TokenID: makerToken, TokenExpiresAt: time.Date(2100, time.January, 1, 0, 0, 0, 0, time.UTC),
		Kind: "", Credential: "", JobID: "",
	}, nil
}

// seedMakerToken stores the service principal prn-ci and the service token
// that the maker made for it.
func seedMakerToken(t *testing.T, st *sqlite.Store) {
	t.Helper()

	ctx := t.Context()
	at := makerAt()

	ci := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: "prn-ci", Subject: "ci-deploy",
		DisplayName: "ci-deploy", Kind: principal.KindService, Source: principal.SourceLocal,
		Namespace: "", GroupCount: 0, BreakGlass: false,
	}
	if err := st.CreatePrincipal(ctx, ci, storetest.ServerChange(at, "principal:prn-ci")); err != nil {
		t.Fatalf("create principal: %v", err)
	}

	tok := accesstoken.Token{
		CreatedAt: at, ExpiresAt: at.Add(24 * time.Hour), LastUsedAt: time.Time{}, RevokedAt: time.Time{},
		DisabledAt: time.Time{}, ID: makerToken, PrincipalID: "prn-ci", Name: "deploy",
		Kind: accesstoken.KindService, ReplacedBy: "", CreatedBy: "maker",
	}
	digest := sha256.Sum256([]byte(makerToken))

	if err := st.CreateToken(ctx, tok, digest, storetest.ServerChange(at, "token:"+makerToken)); err != nil {
		t.Fatalf("create token: %v", err)
	}
}

// wantCliRefusal fails the test unless status and body are the refusal that
// the CLI handles for a revoked token: 401 with the code and the message, and
// no word about the check. It returns body.
func wantCliRefusal(t *testing.T, name string, status int, body string) string {
	t.Helper()

	if status != http.StatusUnauthorized {
		t.Errorf("%s with no path answers %d, want %d (%s)", name, status, http.StatusUnauthorized, body)

		return body
	}

	var shape struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}

	if err := json.Unmarshal([]byte(body), &shape); err != nil || shape.Code != http.StatusUnauthorized ||
		shape.Message == "" {
		t.Errorf("%s body = %s, want the CLI error shape with code 401", name, body)
	}

	if strings.Contains(body, "maker") || strings.Contains(body, "act-as") {
		t.Errorf("%s body names the check: %s", name, body)
	}

	return body
}

// wantOneMakerRevocation fails the test unless the token is revoked and has
// one entry of a revocation at its use.
func wantOneMakerRevocation(t *testing.T, st *sqlite.Store) {
	t.Helper()

	got, err := st.GetToken(t.Context(), makerToken)
	if err != nil || got.RevokedAt.IsZero() {
		t.Fatalf("token after the refusal = %+v, %v; want revoked", got, err)
	}

	listing, err := st.List(t.Context(), audit.ServerScope, audit.Page{Size: audit.MaxPageSize, Before: 0})
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}

	n := 0

	for i := range listing.Entries {
		e := &listing.Entries[i]
		if e.Subject != accesstoken.MakerSubject || !strings.HasSuffix(e.Object, "#token:"+makerToken) {
			continue
		}

		n++

		if e.Reason != accesstoken.ReasonMakerLostPathAtUse {
			t.Errorf("entry reason = %q, want %q", e.Reason, accesstoken.ReasonMakerLostPathAtUse)
		}
	}

	if n != 1 {
		t.Fatalf("revocation entries = %d, want 1", n)
	}
}

// TestLeaseRequestsRefuseATokenWhoseMakerLostThePath runs the real token
// service on a real store under the update routes. An update starts with a
// service token. The maker then loses the path with no check after the change.
// Each next lease request answers 401 with the same body, and the token is
// revoked with one audit entry.
func TestLeaseRequestsRefuseATokenWhoseMakerLostThePath(t *testing.T) {
	t.Parallel()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	seedMakerToken(t, st)

	makers := &switchMaker{can: atomic.Bool{}, fail: atomic.Bool{}}
	makers.can.Store(true)

	svc, err := accesstoken.NewService(accesstoken.Config{
		Makers:     makers,
		TempAdmins: nil,
		Store:      st,
		Principals: st,
		Freshness:  nil,
		Authorizer: nil,
		Reach: access.ReachConfig{
			Grants:      nil,
			Roles:       nil,
			Callers:     nil,
			Principals:  nil,
			Collections: nil,
			Units:       nil,
		},
		Logger: nil,
		Now:    makerAt,
		NewID:  nil,
	})
	if err != nil {
		t.Fatalf("token service: %v", err)
	}

	f := newUpdateFixtureWithAuthenticator(t, allowAll(), makerAuthn{}, fixtureOptions{
		manual: nil, checker: svc, limit: 0, cutoff: 0, replayer: nil, guard: nil, placer: nil, placement: nil,
	})
	paths, lease := startWithToken(t, f, "service-token")

	status, body := f.doUpdate(t, leaseRequest(http.MethodPost, paths.Renew, lease, `{"duration":60}`))
	if status != http.StatusOK {
		t.Fatalf("renew with a path answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	makers.can.Store(false)

	requests := map[string]updateRequest{
		"renew":  leaseRequest(http.MethodPost, paths.Renew, lease, `{"duration":60}`),
		"events": leaseRequest(http.MethodPost, paths.Events, lease, `{"events":[]}`),
		"checkpoint": leaseRequest(http.MethodPatch, paths.Checkpoint, lease,
			`{"version":3,"deployment":{"resources":[]}}`),
		"complete": leaseRequest(http.MethodPost, paths.Complete, lease, `{"status":"succeeded"}`),
	}

	bodies := map[string]bool{}

	for name, req := range requests {
		status, body := f.doUpdate(t, req)
		bodies[wantCliRefusal(t, name, status, body)] = true
	}

	if len(bodies) != 1 {
		t.Errorf("the refusals have %d different bodies, want 1: %v", len(bodies), bodies)
	}

	wantOneMakerRevocation(t, st)
}
