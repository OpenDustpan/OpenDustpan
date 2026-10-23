package tfe_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
)

// registryErrorWire is the error document of the registry protocol: the
// errors of the protocol, and the code of the catalog next to them.
type registryErrorWire struct {
	Errors    []string       `json:"errors"`
	ErrorCode string         `json:"errorCode"`
	ErrorArgs map[string]any `json:"errorArgs"`
}

// TestRegistryErrorCodes proves that each error answer of the registry
// protocol keeps its errors member and holds the code of the catalog next to
// it, with no arguments.
func TestRegistryErrorCodes(t *testing.T) {
	t.Parallel()

	open := newRegistryFixture(t, registryHost, allowAll())
	denied := newRegistryFixture(t, registryHost, stubAuthorizer{})

	cases := []struct {
		name   string
		fix    registryFixture
		q      regReq
		status int
		code   string
	}{
		{
			"unknown provider", open,
			regReq{host: registryHost, target: "/v1/providers/acme/other/versions", token: "test-token", headers: nil},
			http.StatusNotFound, errcode.RegistryNotFound,
		},
		{
			"another host", open,
			regReq{host: "other.example.com", target: versionsPath, token: "test-token", headers: nil},
			http.StatusNotFound, errcode.RegistryNotFound,
		},
		{
			"no token", open,
			regReq{host: registryHost, target: versionsPath, token: "", headers: nil},
			http.StatusUnauthorized, errcode.AuthUnauthenticated,
		},
		{
			"denied", denied,
			regReq{host: registryHost, target: versionsPath, token: "test-token", headers: nil},
			http.StatusForbidden, errcode.AccessDenied,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := tc.fix.serve(t, tc.q)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}

			var doc registryErrorWire
			if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
				t.Fatalf("decode: %v: %s", err, rec.Body.String())
			}

			if _, ok := errcode.Lookup(doc.ErrorCode); !ok || doc.ErrorCode != tc.code || len(doc.Errors) != 1 ||
				doc.ErrorArgs != nil {
				t.Fatalf("answer = %s, want one error, the code %s and no arguments", rec.Body.String(), tc.code)
			}
		})
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
