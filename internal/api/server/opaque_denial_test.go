package server_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/identity"
)

// errOpaqueDenialFault is a failure that no class of the API maps.
var errOpaqueDenialFault = errors.New("the store did not answer")

// TestTokenRoutesDenyWithOneBody proves that the two token routes whose object
// comes from the stored token answer the same 403 whatever the stored token is.
func TestTokenRoutesDenyWithOneBody(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	f.seedService(t, "deploy")

	var service, personal tokenDoc

	f.as(t, "admin", apiCall{
		http.MethodPost, "/api/server/principals/prn-deploy/tokens", `{"name":"ci"}`, http.StatusCreated,
	}, &service)
	f.as(t, "alice", apiCall{
		http.MethodPost, "/api/server/caller/tokens", `{"name":"laptop"}`, http.StatusCreated,
	}, &personal)

	type route struct {
		path   func(id string) string
		name   string
		method string
		body   string
	}

	routes := []route{
		{
			name: "delete token", method: http.MethodDelete, body: "",
			path: func(id string) string { return "/api/server/tokens/" + id },
		},
		{
			name: "rotate service token", method: http.MethodPost, body: "{}",
			path: func(id string) string { return "/api/server/service-tokens/" + id + "/rotate" },
		},
	}

	states := []struct {
		name string
		id   string
	}{
		{"existing service token", service.ID},
		{"existing personal token of another person", personal.ID},
		{"unknown well-formed ID", "tok-unknown"},
		{"malformed ID", "not-a-token"},
	}

	want := f.asToken(t, "bob", routes[0].method, routes[0].path(states[0].id), []byte(routes[0].body)).Body.String()

	for _, rt := range routes {
		for _, state := range states {
			t.Run(rt.name+"/"+state.name, func(t *testing.T) {
				t.Parallel()

				rec := f.asToken(t, "bob", rt.method, rt.path(state.id), []byte(rt.body))
				checkTokenDenial(t, rec, want, state.id)
			})
		}
	}
}

// checkTokenDenial fails unless rec is the one 403 body of a token route and
// the body names no action, no object kind and no token ID.
func checkTokenDenial(t *testing.T, rec *httptest.ResponseRecorder, want, id string) {
	t.Helper()

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}

	if got := rec.Body.String(); got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}

	var body denialBody
	decodeInto(t, rec.Body.String(), &body)

	if body.ErrorCode != errcode.AccessDenied {
		t.Errorf("errorCode = %q, want %q", body.ErrorCode, errcode.AccessDenied)
	}

	for _, word := range []string{"principal", "server", "manage-tokens", id} {
		if strings.Contains(rec.Body.String(), word) {
			t.Errorf("body holds %q: %s", word, rec.Body.String())
		}
	}
}

// TestTokenRoutesHideStoredTokenState proves that the two token routes whose
// object comes from the stored token answer the opaque 403 for a token that
// is missing, malformed or of the wrong kind, also to a caller that holds
// server:manage-tokens and to an administrator. Another token route and a
// store failure keep their answer.
func TestTokenRoutesHideStoredTokenState(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	f.seedService(t, "deploy")

	var role roleDoc
	f.as(t, "ann", apiCall{
		http.MethodPost, "/api/server/roles", `{"name":"token-revoker","actions":["server:manage-tokens"]}`,
		http.StatusCreated,
	}, &role)
	f.as(t, "ann", apiCall{
		http.MethodPost, "/api/server/grants",
		`{"subject":"principal:prn-bob","role":"` + role.ID + `"}`, http.StatusCreated,
	}, nil)

	var service, personal tokenDoc
	f.as(t, "admin", apiCall{
		http.MethodPost, "/api/server/principals/prn-deploy/tokens", `{"name":"ci"}`, http.StatusCreated,
	}, &service)
	f.as(t, "alice", apiCall{
		http.MethodPost, "/api/server/caller/tokens", `{"name":"laptop"}`, http.StatusCreated,
	}, &personal)

	const (
		deletePath = "/api/server/tokens/"
		rotatePath = "/api/server/service-tokens/"
	)

	want := f.asToken(t, "olga", http.MethodDelete, deletePath+service.ID, nil).Body.String()

	broken := newAccessFixture(t)
	if err := broken.store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	tests := []tokenStateCase{
		{
			name: "holder delete unknown well-formed ID", subject: "bob", method: http.MethodDelete,
			path: deletePath + "tok-unknown", body: "", id: "tok-unknown",
			wantStatus: http.StatusForbidden, wantDenial: true, broken: false, session: false,
		},
		{
			name: "holder delete malformed ID", subject: "bob", method: http.MethodDelete,
			path: deletePath + "not-a-token", body: "", id: "not-a-token",
			wantStatus: http.StatusForbidden, wantDenial: true, broken: false, session: false,
		},
		{
			name: "holder delete service token of another principal", subject: "bob", method: http.MethodDelete,
			path: deletePath + service.ID, body: "", id: service.ID,
			wantStatus: http.StatusForbidden, wantDenial: true, broken: false, session: false,
		},
		{
			name: "holder rotate unknown well-formed ID", subject: "bob", method: http.MethodPost,
			path: rotatePath + "tok-unknown/rotate", body: "{}", id: "tok-unknown",
			wantStatus: http.StatusForbidden, wantDenial: true, broken: false, session: false,
		},
		{
			name: "holder rotate malformed ID", subject: "bob", method: http.MethodPost,
			path: rotatePath + "not-a-token/rotate", body: "{}", id: "not-a-token",
			wantStatus: http.StatusForbidden, wantDenial: true, broken: false, session: false,
		},
		{
			name: "holder rotate service token of another principal", subject: "bob", method: http.MethodPost,
			path: rotatePath + service.ID + "/rotate", body: "{}", id: service.ID,
			wantStatus: http.StatusForbidden, wantDenial: true, broken: false, session: false,
		},
		{
			name: "administrator delete unknown well-formed ID", subject: "admin", method: http.MethodDelete,
			path: deletePath + "tok-unknown", body: "", id: "tok-unknown",
			wantStatus: http.StatusForbidden, wantDenial: true, broken: false, session: false,
		},
		{
			name: "administrator rotate unknown well-formed ID", subject: "admin", method: http.MethodPost,
			path: rotatePath + "tok-unknown/rotate", body: "{}", id: "tok-unknown",
			wantStatus: http.StatusForbidden, wantDenial: true, broken: false, session: true,
		},
		{
			name: "administrator rotate personal token", subject: "admin", method: http.MethodPost,
			path: rotatePath + personal.ID + "/rotate", body: "{}", id: personal.ID,
			wantStatus: http.StatusForbidden, wantDenial: true, broken: false, session: true,
		},
		{
			name: "administrator rotate unknown ID with an undecodable body", subject: "admin", method: http.MethodPost,
			path: rotatePath + "tok-unknown/rotate", body: `{"x":1}`, id: "tok-unknown",
			wantStatus: http.StatusForbidden, wantDenial: true, broken: false, session: true,
		},
		{
			name: "administrator rotate unknown ID without a session", subject: "admin", method: http.MethodPost,
			path: rotatePath + "tok-unknown/rotate", body: "{}", id: "tok-unknown",
			wantStatus: http.StatusForbidden, wantDenial: false, broken: false, session: false,
		},
		{
			name: "administrator rotate service token without a session", subject: "admin", method: http.MethodPost,
			path: rotatePath + service.ID + "/rotate", body: "{}", id: service.ID,
			wantStatus: http.StatusForbidden, wantDenial: false, broken: false, session: false,
		},
		{
			name: "administrator read unknown token", subject: "admin", method: http.MethodGet,
			path: deletePath + "tok-unknown", body: "", id: "tok-unknown",
			wantStatus: http.StatusNotFound, wantDenial: false, broken: false, session: false,
		},
		{
			name: "store failure delete", subject: "admin", method: http.MethodDelete,
			path: deletePath + "tok-unknown", body: "", id: "tok-unknown",
			wantStatus: http.StatusInternalServerError, wantDenial: false, broken: true, session: false,
		},
		{
			name: "store failure rotate", subject: "admin", method: http.MethodPost,
			path: rotatePath + "tok-unknown/rotate", body: "{}", id: "tok-unknown",
			wantStatus: http.StatusInternalServerError, wantDenial: false, broken: true, session: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkTokenStateCase(t, f, broken, tc, want)
		})
	}
}

// TestOpaqueTokenRoutesKeepRequestErrors proves that the opaque denial of the
// token routes covers only the state of the token in the path: an invalid
// request on a token that the caller may change keeps its own answer.
func TestOpaqueTokenRoutesKeepRequestErrors(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	f.seedService(t, "deploy")

	var service tokenDoc
	f.as(t, "admin", apiCall{
		http.MethodPost, "/api/server/principals/prn-deploy/tokens", `{"name":"ci"}`, http.StatusCreated,
	}, &service)

	tests := []struct {
		name       string
		body       string
		wantCode   string
		wantStatus int
	}{
		{
			name: "an end time of the old token in the past", body: `{"oldTokenEndsAt":"2000-01-01T00:00:00Z"}`,
			wantStatus: http.StatusBadRequest, wantCode: errcode.TokenInvalid,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := f.asSessionRequest(t, "admin", http.MethodPost,
				"/api/server/service-tokens/"+service.ID+"/rotate", tc.body)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.wantStatus, rec.Body.String())
			}

			var body denialBody
			decodeInto(t, rec.Body.String(), &body)

			if body.ErrorCode != tc.wantCode {
				t.Errorf("errorCode = %q, want %q", body.ErrorCode, tc.wantCode)
			}
		})
	}
}

// tokenStateCase is one request of TestTokenRoutesHideStoredTokenState.
type tokenStateCase struct {
	name       string
	subject    string
	method     string
	path       string
	body       string
	id         string
	wantStatus int
	wantDenial bool
	broken     bool
	session    bool
}

// checkTokenStateCase sends one request of TestTokenRoutesHideStoredTokenState
// and checks its answer.
func checkTokenStateCase(t *testing.T, f, broken accessFixture, tc tokenStateCase, want string) {
	t.Helper()

	fixture := f
	if tc.broken {
		fixture = broken
	}

	var rec *httptest.ResponseRecorder
	if tc.session {
		rec = fixture.asSessionRequest(t, tc.subject, tc.method, tc.path, tc.body)
	} else {
		rec = fixture.asToken(t, tc.subject, tc.method, tc.path, []byte(tc.body))
	}

	if tc.wantDenial {
		checkTokenDenial(t, rec, want, tc.id)

		return
	}

	if rec.Code != tc.wantStatus {
		t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.wantStatus, rec.Body.String())
	}

	var body denialBody
	decodeInto(t, rec.Body.String(), &body)

	// A 403 that is not the opaque denial is the refusal of a rotation
	// without a fresh console session. It is the same for each token ID.
	var wantCode string

	switch tc.wantStatus {
	case http.StatusInternalServerError:
		wantCode = errcode.ServerInternal
	case http.StatusForbidden:
		wantCode = errcode.MFAFreshStepRequired
	default:
		wantCode = errcode.TokenNotFound
	}

	if body.ErrorCode != wantCode {
		t.Errorf("errorCode = %q, want %q", body.ErrorCode, wantCode)
	}

	if tc.wantStatus == http.StatusForbidden && strings.Contains(rec.Body.String(), tc.id) {
		t.Errorf("body holds the token ID %q: %s", tc.id, rec.Body.String())
	}
}

// asSessionRequest sends one request in a new console session of subject and
// returns its answer. A rotation needs a fresh sign-in, so its rows use this
// helper.
func (f accessFixture) asSessionRequest(
	t *testing.T, subject, method, target, body string,
) *httptest.ResponseRecorder {
	t.Helper()

	f.seedSessionAdmin(t, subject)

	cookie, csrf := f.sessionOf(t, subject, f.sessions.Now())

	req := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	req.AddCookie(cookie)
	req.Header.Set(apiserver.CSRFHeader, csrf)
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	return rec
}

// TestOpaqueDenialField proves that OpaqueDenial changes only the text of a
// refusal on its own route: a denial answers the plain text, the same denial
// without the field names the action and the kind, and any other error keeps
// its answer.
func TestOpaqueDenialField(t *testing.T) {
	t.Parallel()

	denied := &identity.DeniedError{
		Subject: "bob",
		Action:  identity.ActionPrincipalManageTokens,
		Object:  identity.PrincipalObject("prn-deploy"),
	}

	tests := []struct {
		authErr     error
		name        string
		wantCode    string
		wantMessage string
		wantStatus  int
		opaque      bool
	}{
		{
			name: "opaque denial", authErr: denied, opaque: true,
			wantStatus: http.StatusForbidden, wantCode: errcode.AccessDenied, wantMessage: "forbidden",
		},
		{
			name: "named denial", authErr: denied, opaque: false,
			wantStatus: http.StatusForbidden, wantCode: errcode.AccessDenied,
			wantMessage: "forbidden: you need the action principal:manage-tokens on this principal",
		},
		{
			name: "opaque other error", authErr: errOpaqueDenialFault, opaque: true,
			wantStatus: http.StatusInternalServerError, wantCode: errcode.ServerInternal, wantMessage: "internal error",
		},
		{
			name: "named other error", authErr: errOpaqueDenialFault, opaque: false,
			wantStatus: http.StatusInternalServerError, wantCode: errcode.ServerInternal, wantMessage: "internal error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := forbiddenHandler(t, tc.authErr)

			route := apiserver.Route{
				Method: http.MethodGet, Path: "/test/opaque-denial", Scope: apiserver.ScopeAuthorized,
				Actions: []identity.Action{identity.ActionPrincipalManageTokens},
				Resolve: func(_ http.ResponseWriter, _ *http.Request) (identity.Object, error) {
					return identity.PrincipalObject("prn-deploy"), nil
				},
				ActionsFor:   nil,
				OpaqueDenial: tc.opaque,
				Serve: func(w http.ResponseWriter, _ *http.Request, _ identity.Principal, _ identity.Object) {
					w.WriteHeader(http.StatusNoContent)
				},
			}

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, route.Path, http.NoBody)
			req.Header.Set("Authorization", "Bearer admin-token")

			rec := httptest.NewRecorder()
			handler.ServeRouteForTest(rec, req, route)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.wantStatus, rec.Body.String())
			}

			var body denialBody
			decodeInto(t, rec.Body.String(), &body)

			if body.ErrorCode != tc.wantCode {
				t.Errorf("errorCode = %q, want %q", body.ErrorCode, tc.wantCode)
			}

			if body.Message != tc.wantMessage {
				t.Errorf("message = %q, want %q", body.Message, tc.wantMessage)
			}
		})
	}
}
