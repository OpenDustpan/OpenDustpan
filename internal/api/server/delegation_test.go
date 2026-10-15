package server_test

import (
	"net/http"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
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
		CreatedAt: at, UpdatedAt: at, ID: "prn-" + subject, Subject: subject,
		DisplayName: "", Kind: principal.KindService, Source: principal.SourceLocal, GroupCount: 0,
		DisabledAt: time.Time{},
	}
	if err := f.store.CreatePrincipal(t.Context(), prn, storetest.ServerChange(at, "principal:"+prn.ID)); err != nil {
		t.Fatalf("seed service principal: %v", err)
	}
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

	var service, personal tokenDoc
	f.as(t, "admin", apiCall{http.MethodPost, deploy + "/tokens", `{"name":"ci"}`, http.StatusCreated}, &service)
	f.as(t, "admin", apiCall{
		http.MethodPost, "/api/server/tokens", `{"principalID":"prn-alice","name":"laptop"}`, http.StatusCreated,
	}, &personal)

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
		{"tm", apiCall{http.MethodPost, alice + "/tokens/" + personal.ID + "/rotate", `{}`, http.StatusBadRequest}},
		{"tm", apiCall{http.MethodPost, alice + "/tokens", `{"name":"x"}`, http.StatusBadRequest}},
		{"tm", apiCall{http.MethodDelete, "/api/server/tokens/" + personal.ID, "", http.StatusForbidden}},
		{"admin", apiCall{
			http.MethodPost, "/api/server/service-tokens", `{"principalID":"prn-none","name":"x"}`,
			http.StatusNotFound,
		}},
	} {
		f.as(t, c.subject, c.call, nil)
	}

	// The token manager of deploy makes and revokes its tokens.
	var made tokenDoc
	f.as(t, "ops", apiCall{http.MethodPost, deploy + "/tokens", `{"name":"ops"}`, http.StatusCreated}, &made)
	f.as(t, "ops", apiCall{http.MethodDelete, deploy + "/tokens/" + made.ID, "", http.StatusNoContent}, nil)
	f.as(t, "ops", apiCall{http.MethodDelete, "/api/server/tokens/" + service.ID, "", http.StatusNoContent}, nil)
}
