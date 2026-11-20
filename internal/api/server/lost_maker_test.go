package server_test

import (
	"crypto/sha256"
	"net/http"
	"strings"
	"testing"
	"time"

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

// seedLostMaker stores the service principal prn-ci, the person tina with the
// role token-manager on it through grant (direct or through the group
// grp-deployers), and the service token lostMakerToken that tina made.
func seedLostMaker(t *testing.T, f accessFixture, path makerPath) {
	t.Helper()

	ctx := t.Context()
	at := time.Now().UTC().Truncate(time.Millisecond)

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

	at := time.Now().UTC().Truncate(time.Millisecond)
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
