package tfe_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
)

// rateLimitedAuthenticator refuses every token because the address has failed
// too many sign-ins.
type rateLimitedAuthenticator struct{}

func (rateLimitedAuthenticator) AuthenticateToken(context.Context, string) (identity.Principal, error) {
	return identity.Principal{}, fmt.Errorf("sign-in: %w", identity.ErrRateLimited)
}

// failingAuthorizer fails with an error that is not a denial.
type failingAuthorizer struct{}

// errPolicyStore and errRegistryStore are the failures of the test doubles.
var (
	errPolicyStore   = errors.New("policy store unavailable")
	errRegistryStore = errors.New("registry database unavailable")
)

func (failingAuthorizer) Authorize(context.Context, identity.Principal, identity.Action, identity.Object) error {
	return errPolicyStore
}

func (failingAuthorizer) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{}, errPolicyStore
}

// failingRegistry fails every release listing with an error that is not a
// not-found.
type failingRegistry struct {
	*fakeRegistry
}

func (failingRegistry) ServableReleases(context.Context, namespace.Name, string) ([]registry.Release, error) {
	return nil, errRegistryStore
}

var _ tfe.RegistryService = failingRegistry{fakeRegistry: nil}

// The texts of the errors member that the registry answers carry.
const (
	notFoundText    = "not found"
	rateLimitedText = "too many failed sign-ins from this address: try again later"
)

// registryErrorWire is the error document of the registry protocol: the
// errors of the protocol, and the code of the catalog next to them.
type registryErrorWire struct {
	Errors []string               `json:"errors"`
	Code   string                 `json:"code"`
	Meta   *registryErrorMetaWire `json:"meta"`
}

// registryErrorMetaWire is the meta member of the registry error document.
type registryErrorMetaWire struct {
	Args map[string]any `json:"args"`
}

// TestRegistryErrorCodes proves that each error answer of the registry
// protocol keeps its errors member and holds the code of the catalog next to
// it, with no arguments.
func TestRegistryErrorCodes(t *testing.T) {
	t.Parallel()

	open := newRegistryFixture(t, registryHost, allowAll())
	denied := newRegistryFixture(t, registryHost, stubAuthorizer{})

	limited := buildRegistryFixture(t, registryHost, rateLimitedAuthenticator{}, allowAll(), nil)
	broken := buildRegistryFixture(t, registryHost, tokenAuthenticator{}, failingAuthorizer{}, nil)
	unavailable := buildRegistryFixture(t, registryHost, tokenAuthenticator{}, allowAll(),
		failingRegistry{fakeRegistry: newFakeRegistry()})

	cases := []struct {
		name   string
		fix    registryFixture
		q      regReq
		status int
		text   string
		code   string
	}{
		{
			"unknown provider", open,
			regReq{host: registryHost, target: "/v1/providers/acme/other/versions", token: "test-token", headers: nil},
			http.StatusNotFound, notFoundText, errcode.RegistryNotFound,
		},
		{
			"another host", open,
			regReq{host: "other.example.com", target: versionsPath, token: "test-token", headers: nil},
			http.StatusNotFound, notFoundText, errcode.RegistryNotFound,
		},
		{
			"no token", open,
			regReq{host: registryHost, target: versionsPath, token: "", headers: nil},
			http.StatusUnauthorized, "unauthenticated", errcode.AuthUnauthenticated,
		},
		{
			"rate limited", limited,
			regReq{host: registryHost, target: versionsPath, token: "test-token", headers: nil},
			http.StatusTooManyRequests, rateLimitedText, errcode.AuthRateLimited,
		},
		{
			"denied", denied,
			regReq{host: registryHost, target: versionsPath, token: "test-token", headers: nil},
			http.StatusForbidden, "forbidden", errcode.AccessDenied,
		},
		{
			"authorizer failure", broken,
			regReq{host: registryHost, target: versionsPath, token: "test-token", headers: nil},
			http.StatusInternalServerError, "internal error", errcode.ServerInternal,
		},
		{
			"registry service failure", unavailable,
			regReq{host: registryHost, target: versionsPath, token: "test-token", headers: nil},
			http.StatusInternalServerError, "internal error", errcode.ServerInternal,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := tc.fix.serve(t, tc.q)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}

			checkRegistryAnswer(t, rec, tc.text, tc.code)
		})
	}
}

// checkRegistryAnswer proves that rec holds the error document of the registry
// protocol with the one error text, the code want and no arguments.
func checkRegistryAnswer(t *testing.T, rec *httptest.ResponseRecorder, text, want string) {
	t.Helper()

	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q, want application/json", got)
	}

	var doc registryErrorWire
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v: %s", err, rec.Body.String())
	}

	if _, ok := errcode.Lookup(doc.Code); !ok || doc.Code != want || len(doc.Errors) != 1 || doc.Meta != nil {
		t.Fatalf("answer = %s, want one error, the code %s and no arguments", rec.Body.String(), want)
	}

	if doc.Errors[0] != text {
		t.Fatalf("errors = %q, want %q", doc.Errors[0], text)
	}

	if strings.Contains(rec.Body.String(), "errorCode") || strings.Contains(rec.Body.String(), "errorArgs") {
		t.Fatalf("answer = %s, want no errorCode and no errorArgs member", rec.Body.String())
	}
}

// TestRouteNotFoundCode proves that a path that the route table does not hold
// answers 404 with server.route-not-found.
func TestRouteNotFoundCode(t *testing.T) {
	t.Parallel()

	rec := resourceResponse(t, "/api/v2/no-such-route", http.StatusNotFound)
	errcodetest.CheckAnswer(t, errcodetest.ShapeJSONAPI, http.StatusNotFound, rec.Body.Bytes(), errcodetest.Want{
		Code: errcode.ServerRouteNotFound, Args: nil, RequestBody: nil,
	})
}
