package server_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// lostMakerToken is the ID of the service token that tina made for ci.
const lostMakerToken = "tok-ci-tina"

// makerPath is how tina holds token-manager on ci: through a direct grant or
// through the group grp-deployers.
type makerPath int

const (
	pathDirect makerPath = iota
	pathGroup
)

// lostMakerAt returns the fixed time of the rows of these tests. It is far in
// the future, so the service token that expires 24 hours after it never
// expires while the tests run, and no test reads the wall clock.
func lostMakerAt() time.Time {
	return time.Date(2030, time.January, 2, 12, 0, 0, 0, time.UTC)
}

// seedLostMaker stores the service principal prn-ci, the person tina with the
// role token-manager on it through grant (direct or through the group
// grp-deployers), and the service token lostMakerToken that tina made.
func seedLostMaker(t *testing.T, f accessFixture, path makerPath) {
	t.Helper()

	ctx := t.Context()
	at := lostMakerAt()

	ci := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: "prn-ci", Subject: "ci-deploy",
		DisplayName: "ci-deploy", Kind: principal.KindService, Source: principal.SourceLocal,
		Namespace: "", GroupCount: 0, BreakGlass: false,
	}
	if err := f.store.CreatePrincipal(ctx, ci, storetest.ServerChange(at, "principal:prn-ci")); err != nil {
		t.Fatalf("create ci: %v", err)
	}

	obj, err := identity.ParsePrincipalObject("prn-ci")
	if err != nil {
		t.Fatalf("parse the principal object: %v", err)
	}

	switch path {
	case pathGroup:
		f.seed(t, "tina", "", identity.Object{})
		seedDeployers(t, f, obj, at)
	case pathDirect:
		f.seed(t, "tina", access.RoleTokenManager, obj)
	default:
		t.Fatalf("unknown maker path %d", path)
	}

	tok := accesstoken.Token{
		CreatedAt: at, ExpiresAt: at.Add(24 * time.Hour), LastUsedAt: time.Time{}, RevokedAt: time.Time{},
		DisabledAt: time.Time{}, ID: lostMakerToken, PrincipalID: "prn-ci", Name: "deploy",
		Kind: accesstoken.KindService, ReplacedBy: "", CreatedBy: "tina",
	}
	created := f.store.CreateToken(ctx, tok, sha256.Sum256([]byte(lostMakerToken)),
		storetest.ServerChange(at, "token:"+lostMakerToken))
	if created != nil {
		t.Fatalf("create the token: %v", created)
	}
}

// seedDeployers stores the group grp-deployers with tina as its member, and
// the grant grt-deployers of token-manager on obj to the group.
func seedDeployers(t *testing.T, f accessFixture, obj identity.Object, at time.Time) {
	t.Helper()

	ctx := t.Context()

	grp := principal.Group{
		CreatedAt: at, UpdatedAt: at, ID: "grp-deployers", Name: "deployers", Description: "",
		Source: principal.SourceLocal, MemberCount: 0, ClosedToIdentityProvider: false,
	}
	if err := f.store.CreateGroup(ctx, grp, storetest.ServerChange(at, "group:grp-deployers")); err != nil {
		t.Fatalf("create the group: %v", err)
	}

	mem := principal.Membership{
		AddedAt: at, SuspendedAt: time.Time{}, GroupID: "grp-deployers", PrincipalID: "prn-tina",
		Source: principal.MembershipSourceLocal, SourceID: "", State: principal.MembershipStateActive, Pinned: false,
	}
	if err := f.store.AddMember(ctx, mem, storetest.ServerChange(at, "group:grp-deployers"), nil); err != nil {
		t.Fatalf("add tina: %v", err)
	}

	grant := access.Grant{
		CreatedAt: at, Condition: nil, Object: obj, ID: "grt-deployers", Role: access.RoleTokenManager,
		CreatedBy: "admin", Subject: access.Subject{Kind: access.SubjectGroup, ID: "grp-deployers"},
	}

	fence, e := storetest.GrantWrite(t, f.store, obj, at, "grant:grt-deployers")
	if err := f.store.CreateGrant(ctx, grant, fence, e, nil); err != nil {
		t.Fatalf("grant the group: %v", err)
	}
}

// lostMakerRevocation returns the audit entry of the revocation of
// lostMakerToken, or nil when the token is active and no entry exists.
func lostMakerRevocation(t *testing.T, f accessFixture) *audit.Entry {
	t.Helper()

	tok, err := f.store.GetToken(t.Context(), lostMakerToken)
	if err != nil {
		t.Fatalf("get the token: %v", err)
	}

	listing, err := f.store.List(t.Context(), audit.ServerScope, audit.Page{Size: audit.MaxPageSize, Before: 0})
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}

	var found []audit.Entry

	for i := range listing.Entries {
		e := &listing.Entries[i]
		if e.Subject == accesstoken.MakerSubject && strings.HasSuffix(e.Object, "#token:"+lostMakerToken) {
			found = append(found, *e)
		}
	}

	switch {
	case tok.RevokedAt.IsZero() && len(found) == 0:
		return nil
	case !tok.RevokedAt.IsZero() && len(found) == 1:
		return &found[0]
	default:
		t.Fatalf("token revoked at %v with the revocation entries %+v, want both or neither", tok.RevokedAt, found)

		return nil
	}
}

// causeOf returns the change "cause" of e.
func causeOf(e *audit.Entry) string {
	for _, c := range e.Changes {
		if c.Key == "cause" {
			return c.New
		}
	}

	return ""
}

// TestTokenEndsWhenItsMakerLosesTheActAsPath sends each change that can end
// the last act-as path of the maker of a service token. Each revokes the
// token in a recorded automatic action whose entry names the change.
func TestTokenEndsWhenItsMakerLosesTheActAsPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		path   makerPath
		change apiCall
		cause  string
	}{
		{
			"revoke of the grant of the maker", pathDirect,
			apiCall{http.MethodDelete, "/api/server/grants/grt-seed-tina", "", http.StatusNoContent},
			"server:manage-access grant:grt-seed-tina",
		},
		{
			"revoke of the grant of the group of the maker", pathGroup,
			apiCall{http.MethodDelete, "/api/server/grants/grt-deployers", "", http.StatusNoContent},
			"server:manage-access grant:grt-deployers",
		},
		{
			"removal of the maker from the group", pathGroup,
			apiCall{http.MethodDelete, "/api/server/groups/grp-deployers/members/prn-tina", "", http.StatusNoContent},
			"server:manage-groups group:grp-deployers",
		},
		{
			"delete of the group", pathGroup,
			apiCall{http.MethodDelete, "/api/server/groups/grp-deployers", "", http.StatusNoContent},
			"server:manage-groups group:grp-deployers",
		},
		{
			"delete of the maker", pathDirect,
			apiCall{http.MethodDelete, "/api/server/principals/prn-tina", "", http.StatusNoContent},
			"server:manage-principals principal:prn-tina",
		},
		{
			"disable of the maker", pathDirect,
			apiCall{http.MethodPost, "/api/server/principals/prn-tina/disable", "", http.StatusOK},
			"server:manage-principals principal:prn-tina",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newAccessFixture(t)
			seedLostMaker(t, f, tc.path)

			f.as(t, "admin", tc.change, nil)

			e := lostMakerRevocation(t, f)
			if e == nil {
				t.Fatal("the token of a maker with no act-as path is still active")
			}

			if !strings.HasPrefix(causeOf(e), tc.cause) || e.Reason != accesstoken.ReasonMakerLostPath {
				t.Fatalf("revocation entry = %+v, want the cause %q and the reason", *e, tc.cause)
			}
		})
	}
}

// TestTokenEndsWhenTheRoleOfItsMakerLosesTheAction proves that a change of a
// role that drops principal:manage-tokens revokes the token of a maker whose
// last path was that role.
func TestTokenEndsWhenTheRoleOfItsMakerLosesTheAction(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	seedLostMaker(t, f, pathDirect)

	var role struct {
		ID string `json:"id"`
	}
	f.as(t, "admin", apiCall{
		http.MethodPost, "/api/server/roles",
		`{"name":"minter","description":"","actions":["principal:manage-tokens"]}`, http.StatusCreated,
	}, &role)

	f.as(t, "admin", apiCall{
		http.MethodPost, "/api/server/grants",
		`{"subject":"principal:prn-tina","role":"` + role.ID + `"}`, http.StatusCreated,
	}, nil)

	// The built-in role still gives a path: the revoke of its grant keeps
	// the token.
	f.as(t, "admin", apiCall{http.MethodDelete, "/api/server/grants/grt-seed-tina", "", http.StatusNoContent}, nil)

	if lostMakerRevocation(t, f) != nil {
		t.Fatal("the token of a maker who keeps a path through another role was revoked")
	}

	f.as(t, "admin", apiCall{
		http.MethodPatch, "/api/server/roles/" + role.ID, `{"actions":["server:read-principals"]}`, http.StatusOK,
	}, nil)

	e := lostMakerRevocation(t, f)
	if e == nil {
		t.Fatal("the token of a maker whose role lost the action is still active")
	}

	if !strings.HasPrefix(causeOf(e), "server:manage-roles role:") {
		t.Fatalf("cause = %q, want the change of the role", causeOf(e))
	}
}

// TestTokenStaysWhileItsMakerKeepsAPath proves that a change that leaves the
// maker an act-as path keeps the token and writes no revocation.
func TestTokenStaysWhileItsMakerKeepsAPath(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	seedLostMaker(t, f, pathDirect)

	at := lostMakerAt()
	fence := namespace.Fence{Namespace: "", FailoverVersion: 0}

	other := access.Grant{
		CreatedAt: at, Condition: nil, Object: identity.ServerObject(), ID: "grt-other",
		Role: access.RoleViewer, CreatedBy: "admin",
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: "prn-bob"},
	}
	granted := f.store.CreateGrant(t.Context(), other, fence, storetest.ServerChange(at, "grant:grt-other"), nil)
	if granted != nil {
		t.Fatalf("create the other grant: %v", granted)
	}

	f.as(t, "admin", apiCall{http.MethodDelete, "/api/server/grants/grt-other", "", http.StatusNoContent}, nil)
	f.as(t, "admin", apiCall{http.MethodPost, "/api/server/principals/prn-bob/disable", "", http.StatusOK}, nil)

	if lostMakerRevocation(t, f) != nil {
		t.Fatal("a change that leaves the maker a path revoked the token")
	}
}

// TestRevokeOfLostMakersSurvivesTheCancelOfTheRequest proves that a client
// that leaves after the commit of the change does not stop the revoke. The
// request context ends right after the commit of the revoke of the grant. The
// token still ends, and its audit entry names the change as the cause, not the
// use of the token.
func TestRevokeOfLostMakersSurvivesTheCancelOfTheRequest(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	f := newAccessFixtureAll(t, accessFixtureParts{
		entries: func(st *sqlite.Store) auditStore { return st }, passkeys: nil, tweak: nil,
		links: noGitHostLinks, ports: accessPorts{collections: nil, settings: nil, authzSettings: nil},
		now: nil, logger: nil, noSignIn: false, getGrantErr: nil, afterRevoke: cancel, tokenList: nil,
	})
	seedLostMaker(t, f, pathDirect)

	req := httptest.NewRequestWithContext(ctx, http.MethodDelete, "/api/server/grants/grt-seed-tina", nil)
	req.Header.Set("Authorization", "Bearer admin")

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	if ctx.Err() == nil {
		t.Fatal("the request context was not canceled after the commit: the test does not test the cancel")
	}

	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke of the grant answers %d, want %d (%s)", rec.Code, http.StatusNoContent, rec.Body.String())
	}

	e := lostMakerRevocation(t, f)
	if e == nil {
		t.Fatal("the cancel of the request stopped the revoke: the token of the maker with no path is still active")
	}

	if !strings.HasPrefix(causeOf(e), "server:manage-access grant:grt-seed-tina") ||
		e.Reason != accesstoken.ReasonMakerLostPath {
		t.Fatalf("revocation entry = %+v, want the change as the cause and the reason of the sweep", *e)
	}
}

// TestRevokeOfLostMakersEndsAtItsTimeout proves that a store that never
// answers does not hold the request. The list of the tokens blocks until its
// context ends. The revoke ends at its timeout, the request answers, the fault
// is in the log, and the check at the use of the token refuses and revokes it.
func TestRevokeOfLostMakersEndsAtItsTimeout(t *testing.T) {
	t.Parallel()

	var (
		armed atomic.Bool
		logs  bytes.Buffer
	)

	block := func(ctx context.Context) error {
		if !armed.Load() {
			return nil
		}

		<-ctx.Done()

		return fmt.Errorf("the list of the tokens ended with its context: %w", ctx.Err())
	}

	f := newAccessFixtureAll(t, accessFixtureParts{
		entries: func(st *sqlite.Store) auditStore { return st }, passkeys: nil, tweak: nil,
		links: noGitHostLinks, ports: accessPorts{collections: nil, settings: nil, authzSettings: nil},
		now: nil, logger: slog.New(slog.NewTextHandler(&logs, nil)), noSignIn: false, getGrantErr: nil,
		afterRevoke: nil, tokenList: block,
	})
	seedLostMaker(t, f, pathDirect)
	apiserver.SetLostMakerRevokeTimeout(f.handler.Handler, 100*time.Millisecond)

	armed.Store(true)

	answered := make(chan int, 1)

	// The deadline only ends a broken build: with the timeout in place the
	// request answers after 100 ms.
	deadline, stop := context.WithTimeout(t.Context(), 10*time.Second)
	defer stop()

	go func() {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/api/server/grants/grt-seed-tina", nil)
		req.Header.Set("Authorization", "Bearer admin")

		rec := httptest.NewRecorder()
		f.handler.ServeHTTP(rec, req)
		answered <- rec.Code
	}()

	select {
	case code := <-answered:
		if code != http.StatusNoContent {
			t.Fatalf("revoke of the grant answers %d, want %d", code, http.StatusNoContent)
		}
	case <-deadline.Done():
		t.Fatal("the request did not answer: the revoke of the lost makers has no timeout")
	}

	armed.Store(false)

	if !strings.Contains(logs.String(), "revoke of the service tokens whose maker lost the act-as path failed") {
		t.Fatalf("log = %q, want the fault of the revoke", logs.String())
	}

	if lostMakerRevocation(t, f) != nil {
		t.Fatal("the token was revoked by the sweep that the timeout stopped")
	}

	err := f.tokens.CheckActive(t.Context(), lostMakerToken)
	if !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("check at use = %v, want ErrUnauthenticated", err)
	}

	e := lostMakerRevocation(t, f)
	if e == nil || e.Reason != accesstoken.ReasonMakerLostPathAtUse {
		t.Fatalf("revocation entry = %+v, want the reason of a use", e)
	}
}
