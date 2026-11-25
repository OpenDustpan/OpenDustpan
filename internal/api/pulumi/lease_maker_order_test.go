package pulumi_test

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/freshstep"
)

// makerHarness is an update that a service token started, on the real token
// service and a real store. The maker has the path until a test turns it off.
type makerHarness struct {
	f      updateFixture
	st     *sqlite.Store
	makers *switchMaker
	paths  updatePaths
	lease  string
}

func newMakerHarness(t *testing.T) makerHarness {
	t.Helper()

	st := openMakerStore(t)
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
			Settings:    nil,
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
		Guard:  freshstep.Guard{},
	})
	if err != nil {
		t.Fatalf("token service: %v", err)
	}

	f := newUpdateFixtureWithAuthenticator(t, allowAll(), makerAuthn{}, fixtureOptions{
		manual: nil, checker: svc, limit: 0, cutoff: 0, replayer: nil, guard: nil, placer: nil, placement: nil,
	})
	paths, lease := startWithToken(t, f, "service-token")

	return makerHarness{f: f, st: st, makers: makers, paths: paths, lease: lease}
}

// requests returns the four lease requests that carry lease.
func (h *makerHarness) requests(lease string) map[string]updateRequest {
	return map[string]updateRequest{
		"renew":  leaseRequest(http.MethodPost, h.paths.Renew, lease, `{"duration":60}`),
		"events": leaseRequest(http.MethodPost, h.paths.Events, lease, `{"events":[]}`),
		"checkpoint": leaseRequest(http.MethodPatch, h.paths.Checkpoint, lease,
			`{"version":3,"deployment":{"resources":[]}}`),
		"journal entries": leaseRequest(http.MethodPatch, h.paths.Journal, lease, `{"entries":[]}`),
		"checkpoint delta": leaseRequest(http.MethodPatch, h.paths.Delta, lease,
			`{"version":3,"sequenceNumber":2,"checkpointHash":"`+strings.Repeat("0", 64)+`","deploymentDelta":[]}`),
		"complete": leaseRequest(http.MethodPost, h.paths.Complete, lease, `{"status":"succeeded"}`),
	}
}

// makerEntries counts the audit entries of a revocation of the service token.
func makerEntries(t *testing.T, st *sqlite.Store) int {
	t.Helper()

	listing, err := st.List(t.Context(), audit.ServerScope, audit.Page{Size: audit.MaxPageSize, Before: 0})
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}

	n := 0

	for i := range listing.Entries {
		e := &listing.Entries[i]
		if e.Subject == accesstoken.MakerSubject && strings.HasSuffix(e.Object, "#token:"+makerToken) {
			n++
		}
	}

	return n
}

// wantTokenKept fails the test when the service token is revoked or an entry
// of a revocation exists.
func wantTokenKept(t *testing.T, st *sqlite.Store, why string) {
	t.Helper()

	got, err := st.GetToken(t.Context(), makerToken)
	if err != nil {
		t.Fatalf("get token: %v", err)
	}

	if !got.RevokedAt.IsZero() {
		t.Errorf("the token is revoked after %s, want it kept", why)
	}

	if n := makerEntries(t, st); n != 0 {
		t.Errorf("%d revocation entries after %s, want none", n, why)
	}
}

// TestWrongLeaseNeverStartsTheMakerCheck sends each lease request with a
// well-formed lease token that is not the one of the update, while the maker
// has no path. The answer must not depend on the maker: it equals the answer
// while the maker has the path. The token stays active and no entry is
// written. Then the right lease starts the check, so the setup is real.
func TestWrongLeaseNeverStartsTheMakerCheck(t *testing.T) {
	t.Parallel()

	h := newMakerHarness(t)

	foreign, err := token.New()
	if err != nil {
		t.Fatalf("new token: %v", err)
	}

	type answer struct {
		status int
		body   string
	}

	withPath := map[string]answer{}

	for name, req := range h.requests(foreign.Reveal()) {
		status, body := h.f.doUpdate(t, req)
		withPath[name] = answer{status, body}
	}

	h.makers.can.Store(false)

	for name, req := range h.requests(foreign.Reveal()) {
		status, body := h.f.doUpdate(t, req)
		if want := withPath[name]; status != want.status || body != want.body {
			t.Errorf("%s with a wrong lease answers %d %s while the maker has no path, want %d %s",
				name, status, body, want.status, want.body)
		}
	}

	wantTokenKept(t, h.st, "requests with a wrong lease")

	status, body := h.f.doUpdate(t, h.requests(h.lease)["renew"])
	wantCliRefusal(t, "renew with the right lease", status, body)
	wantOneMakerRevocation(t, h.st)
}

// TestMakerCheckErrorEndsNoUpdate sends each lease request while the check of
// the maker cannot decide. The answer is a server error, not the 401 of a
// revoked token, so the client retries and does not end the update. The token
// is kept and no entry is written. When the check decides again, the update
// goes on.
func TestMakerCheckErrorEndsNoUpdate(t *testing.T) {
	t.Parallel()

	h := newMakerHarness(t)
	h.makers.fail.Store(true)

	for name, req := range h.requests(h.lease) {
		status, body := h.f.doUpdate(t, req)
		if status < http.StatusInternalServerError {
			t.Errorf("%s while the check cannot decide answers %d, want a server error (%s)", name, status, body)
		}

		if strings.Contains(body, errMakerStore.Error()) {
			t.Errorf("%s body names the store error: %s", name, body)
		}
	}

	wantTokenKept(t, h.st, "a check that cannot decide")

	h.makers.fail.Store(false)

	status, body := h.f.doUpdate(t, h.requests(h.lease)["renew"])
	if status != http.StatusOK {
		t.Fatalf("renew after the check decides answers %d, want %d (%s)", status, http.StatusOK, body)
	}
}
