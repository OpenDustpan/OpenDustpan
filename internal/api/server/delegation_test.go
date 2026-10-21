package server_test

import (
	"crypto/sha256"
	"net/http"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// tokenDoc decodes the ID of a token.
type tokenDoc struct {
	ID string `json:"id"`
}

// seedService stores the service principal "prn-<subject>".
func (f accessFixture) seedService(t *testing.T, subject string) {
	t.Helper()

	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)

	prn := principal.Principal{
		CreatedAt:   at,
		UpdatedAt:   at,
		ID:          "prn-" + subject,
		Subject:     subject,
		DisplayName: "",
		Kind:        principal.KindService,
		Source:      principal.SourceLocal,
		Namespace:   "",
		GroupCount:  0,
		BreakGlass:  false,
		DisabledAt:  time.Time{},
	}
	if err := f.store.CreatePrincipal(t.Context(), prn, storetest.ServerChange(at, "principal:"+prn.ID)); err != nil {
		t.Fatalf("seed service principal: %v", err)
	}
}

// seedToken stores an active token of the principal prn-<subject> and
// returns its ID. A route cannot make it: an access token never gives the
// fresh authentication that a token change needs.
func (f accessFixture) seedToken(t *testing.T, id, subject string, kind accesstoken.Kind) tokenDoc {
	t.Helper()

	at := time.Now().UTC().Truncate(time.Millisecond)
	tok := accesstoken.Token{
		CreatedAt:   at.Add(-time.Hour),
		ExpiresAt:   at.Add(48 * time.Hour),
		LastUsedAt:  time.Time{},
		RevokedAt:   time.Time{},
		DisabledAt:  time.Time{},
		ID:          id,
		PrincipalID: "prn-" + subject,
		Name:        "seed",
		Kind:        kind,
		ReplacedBy:  "",
		CreatedBy:   "admin",
	}

	digest := accesstoken.Digest(sha256.Sum256([]byte(id)))
	change := storetest.ServerChange(at, accesstoken.Ref(id))

	if err := f.store.CreateToken(t.Context(), tok, digest, change); err != nil {
		t.Fatalf("seed token %s: %v", id, err)
	}

	return tokenDoc{ID: id}
}

// TestDelegationRoutesRefuse proves the refusals of the routes of one
// service principal with the authorizer that decides with grants: a
// principal with no grant, a token manager on one principal, and a token
// manager on the installation, which holds every principal but gets no
// right on a personal token.
func TestDelegationRoutesRefuse(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	f.seedService(t, "deploy")
	f.seed(t, "ops", access.RoleTokenManager, identity.PrincipalObject("prn-deploy"))
	f.seed(t, "tm", access.RoleTokenManager, identity.ServerObject())

	const (
		deploy = "/api/server/principals/prn-deploy"
		alice  = "/api/server/principals/prn-alice"
	)

	service := f.seedToken(t, "tok-delegation-service", "deploy", accesstoken.KindService)
	personal := f.seedToken(t, "tok-delegation-personal", "alice", accesstoken.KindPersonal)

	for _, c := range []struct {
		subject string
		call    apiCall
	}{
		{"bob", apiCall{http.MethodPost, deploy + "/tokens", `{"name":"x"}`, http.StatusForbidden}},
		{"bob", apiCall{http.MethodPost, deploy + "/tokens/" + service.ID + "/rotate", `{}`, http.StatusForbidden}},
		{"bob", apiCall{http.MethodDelete, deploy + "/tokens/" + service.ID, "", http.StatusForbidden}},
		{"bob", apiCall{http.MethodDelete, "/api/server/tokens/" + service.ID, "", http.StatusForbidden}},
		{"bob", apiCall{
			http.MethodPost, "/api/server/service-tokens", `{"principalID":"prn-deploy","name":"x"}`,
			http.StatusForbidden,
		}},
		{"bob", apiCall{
			http.MethodPost, "/api/server/service-tokens", `{"principalID":"prn-none","name":"x"}`,
			http.StatusForbidden,
		}},
		{"bob", apiCall{http.MethodGet, deploy + "/grants", "", http.StatusForbidden}},
		{"bob", apiCall{http.MethodGet, deploy + "/access-review", "", http.StatusForbidden}},
		{"ops", apiCall{
			http.MethodPost, deploy + "/grants", `{"subject":"principal:prn-bob","role":"token-manager"}`,
			http.StatusForbidden,
		}},
		{"ops", apiCall{http.MethodDelete, "/api/server/tokens/" + personal.ID, "", http.StatusForbidden}},
		{"tm", apiCall{http.MethodDelete, alice + "/tokens/" + personal.ID, "", http.StatusBadRequest}},
		// An access token never gives a fresh authentication, so the fresh
		// check refuses the creation before the kind of the principal.
		{"tm", apiCall{http.MethodPost, alice + "/tokens/" + personal.ID + "/rotate", `{}`, http.StatusBadRequest}},
		{"tm", apiCall{http.MethodPost, alice + "/tokens", `{"name":"x"}`, http.StatusForbidden}},
		{"tm", apiCall{http.MethodDelete, "/api/server/tokens/" + personal.ID, "", http.StatusForbidden}},
		{"admin", apiCall{
			http.MethodPost, "/api/server/service-tokens", `{"principalID":"prn-none","name":"x"}`,
			http.StatusForbidden,
		}},
	} {
		f.as(t, c.subject, c.call, nil)
	}

	// The token manager of deploy revokes its tokens. An access token makes no
	// token: that change needs a console session with a recent sign-in.
	made := f.seedToken(t, "tok-delegation-made", "deploy", accesstoken.KindService)
	f.as(t, "ops", apiCall{http.MethodPost, deploy + "/tokens", `{"name":"ops"}`, http.StatusForbidden}, nil)
	f.as(t, "ops", apiCall{http.MethodDelete, deploy + "/tokens/" + made.ID, "", http.StatusNoContent}, nil)
	f.as(t, "ops", apiCall{http.MethodDelete, "/api/server/tokens/" + service.ID, "", http.StatusNoContent}, nil)
}

// limitedTokenListDoc decodes a token list with the maximum lifetime.
type limitedTokenListDoc struct {
	Tokens             []tokenDoc `json:"tokens"`
	Total              int64      `json:"total"`
	MaxLifetimeSeconds int64      `json:"maxLifetimeSeconds"`
}

// TestTokenManagerReads proves the reads of a token manager with the
// authorizer that decides with grants: the token list of the principals it
// manages, the principals themselves, and the maximum lifetime, which every
// signed-in person reads. A principal without the right, and a person path,
// are refused.
func TestTokenManagerReads(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	f.seedService(t, "deploy")
	f.seedService(t, "other")
	f.seed(t, "ops", access.RoleTokenManager, identity.PrincipalObject("prn-deploy"))
	f.seed(t, "tm", access.RoleTokenManager, identity.ServerObject())

	const (
		deploy = "/api/server/principals/prn-deploy/tokens"
		other  = "/api/server/principals/prn-other/tokens"
		year   = int64(365 * 24 * 60 * 60)
	)

	f.seedToken(t, "tok-reads-deploy", "deploy", accesstoken.KindService)
	f.seedToken(t, "tok-reads-other", "other", accesstoken.KindService)

	var list limitedTokenListDoc
	f.as(t, "ops", apiCall{http.MethodGet, deploy, "", http.StatusOK}, &list)

	if list.Total != 1 || len(list.Tokens) != 1 || list.MaxLifetimeSeconds != year {
		t.Fatalf("tokens of deploy = %+v, want one token and the maximum lifetime %d", list, year)
	}

	for _, c := range []struct {
		subject string
		call    apiCall
	}{
		{"ops", apiCall{http.MethodGet, other, "", http.StatusForbidden}},
		{"bob", apiCall{http.MethodGet, deploy, "", http.StatusForbidden}},
		{"tm", apiCall{http.MethodGet, "/api/server/principals/prn-alice/tokens", "", http.StatusBadRequest}},
		{"admin", apiCall{http.MethodGet, "/api/server/principals/prn-none/tokens", "", http.StatusNotFound}},
		{"ops", apiCall{http.MethodGet, "/api/server/caller/access?type=principal&action=unit:read", "", http.StatusBadRequest}},
		{"tm", apiCall{
			http.MethodGet, "/api/server/caller/access?type=principal&action=server:manage-access", "",
			http.StatusBadRequest,
		}},
		{"bob", apiCall{http.MethodGet, "/api/server/principals/prn-none/tokens", "", http.StatusForbidden}},
	} {
		f.as(t, c.subject, c.call, nil)
	}

	for _, c := range []struct {
		subject string
		objects []string
		all     bool
	}{
		{"ops", []string{"principal:prn-deploy"}, false},
		{"tm", nil, true},
		{"bob", nil, false},
	} {
		var got accessObjectsDoc
		f.as(t, c.subject, apiCall{http.MethodGet, "/api/server/caller/access?type=principal", "", http.StatusOK}, &got)

		objects := make([]string, 0, len(got.Objects))
		for _, o := range got.Objects {
			objects = append(objects, o.Object)
		}

		if got.All != c.all || !slices.Equal(objects, c.objects) ||
			got.Action != string(identity.ActionPrincipalManageTokens) {
			t.Errorf("principals of %s = %+v, want %v, all %t", c.subject, got, c.objects, c.all)
		}
	}

	var own limitedTokenListDoc
	f.as(t, "bob", apiCall{http.MethodGet, "/api/server/caller/tokens", "", http.StatusOK}, &own)

	if own.MaxLifetimeSeconds != year {
		t.Fatalf("maximum lifetime of a person = %d, want %d", own.MaxLifetimeSeconds, year)
	}
}

// TestNamespacePrincipalRoutes proves the routes of the principals of a
// namespace with the authorizer that decides with grants: the namespace
// admin manages them, a principal without a grant on the namespace cannot,
// and a principal of a namespace gets no token and no grant outside it.
func TestNamespacePrincipalRoutes(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)

	const principals = "/api/server/namespaces/acme/principals"

	var bot principalDoc
	f.as(t, "alice", apiCall{http.MethodPost, principals, `{"subject":"ci-acme"}`, http.StatusCreated}, &bot)

	if bot.Kind != "service" || bot.Namespace != "acme" {
		t.Fatalf("principal = %+v, want a service principal of acme", bot)
	}

	one := principals + "/" + bot.ID
	tokens := "/api/server/principals/" + bot.ID + "/tokens"

	var ops groupDoc
	f.as(t, "admin", apiCall{http.MethodPost, "/api/server/groups", `{"name":"ops"}`, http.StatusCreated}, &ops)

	for _, c := range []struct {
		subject string
		call    apiCall
	}{
		{"alice", apiCall{http.MethodGet, principals, "", http.StatusOK}},
		{"alice", apiCall{http.MethodGet, one, "", http.StatusOK}},
		{"alice", apiCall{http.MethodPatch, one, `{"displayName":"Acme bot"}`, http.StatusOK}},
		{"alice", apiCall{http.MethodGet, principals + "/prn-alice", "", http.StatusNotFound}},
		{"alice", apiCall{
			http.MethodPost, "/api/server/namespaces/acme/grants",
			`{"subject":"principal:` + bot.ID + `","role":"writer"}`, http.StatusCreated,
		}},
		{"bob", apiCall{http.MethodPost, principals, `{"subject":"ci-bob"}`, http.StatusForbidden}},
		{"bob", apiCall{http.MethodGet, principals, "", http.StatusForbidden}},
		{"bob", apiCall{http.MethodDelete, one, "", http.StatusForbidden}},
		{"olga", apiCall{http.MethodGet, principals, "", http.StatusForbidden}},
		{"alice", apiCall{http.MethodPost, tokens, `{"name":"x"}`, http.StatusForbidden}},
		{"alice", apiCall{http.MethodGet, tokens, "", http.StatusOK}},
		{"bob", apiCall{http.MethodPost, tokens, `{"name":"x"}`, http.StatusForbidden}},
		{"bob", apiCall{http.MethodGet, tokens, "", http.StatusForbidden}},
		{"admin", apiCall{http.MethodPost, tokens, `{"name":"x"}`, http.StatusForbidden}},
		{"admin", apiCall{http.MethodGet, tokens, "", http.StatusOK}},
		{"admin", apiCall{
			http.MethodPost, "/api/server/groups/" + ops.ID + "/members", `{"principalID":"` + bot.ID + `"}`,
			http.StatusBadRequest,
		}},
		{"admin", apiCall{
			http.MethodPost, "/api/server/grants", `{"subject":"principal:` + bot.ID + `","role":"viewer"}`,
			http.StatusBadRequest,
		}},
		{"alice", apiCall{http.MethodDelete, one, "", http.StatusNoContent}},
	} {
		f.as(t, c.subject, c.call, nil)
	}
}

// TestNoRouteMakesAPersonalTokenOfAnother proves that no route makes or
// rotates a personal token of another person, for an administrator too:
// the person makes its own under /api/server/caller/tokens. The list, the
// read and the revoke of every token stay.
func TestNoRouteMakesAPersonalTokenOfAnother(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)

	own := f.seedToken(t, "tok-own-alice", "alice", accesstoken.KindPersonal)

	for _, c := range []apiCall{
		{http.MethodPost, "/api/server/tokens", `{"principalID":"prn-alice","name":"laptop"}`, http.StatusMethodNotAllowed},
		{http.MethodPost, "/api/server/tokens/" + own.ID + "/rotate", `{}`, http.StatusNotFound},
		{http.MethodGet, "/api/server/tokens/" + own.ID, "", http.StatusOK},
		{http.MethodDelete, "/api/server/tokens/" + own.ID, "", http.StatusNoContent},
	} {
		f.as(t, "admin", c, nil)
	}
}
