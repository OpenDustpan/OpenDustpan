package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

const (
	proxyBase    = "/api/dustpan/v1/namespaces/acme"
	proxyNSPath  = proxyBase + "/proxy-rules"
	proxyUnitID  = "stk-gate-web-prod"
	proxyUnitURL = proxyBase + "/units/" + proxyUnitID + "/proxy-rules"
	proxyRuleDoc = `{"rules":[{"id":"status","operations":["plan"],"host":"status.example.com","mode":"inspect",` +
		`"methods":["GET"],"paths":["/v1/status"]}]}`
	proxyOtherDoc = `{"rules":[{"id":"other","operations":["plan","apply"],"host":"other.example.com",` +
		`"mode":"tunnel"}]}`
	proxyJSON = "application/json"
)

// proxyAdmin allows the proxy rule actions and the reads on acme.
func proxyAdmin() gateAuthorizer {
	return gateAuthorizer{allow: map[identity.Action]namespace.Name{
		identity.ActionNamespaceRead:             "acme",
		identity.ActionNamespaceManageProxyRules: "acme",
		identity.ActionUnitRead:                  "acme",
		identity.ActionUnitManageProxyRules:      "acme",
		identity.ActionNamespaceReadGates:        "acme",
		identity.ActionNamespaceManageGates:      "acme",
	}}
}

// proxyCall is one request of a proxy rule test.
type proxyCall struct {
	method      string
	target      string
	body        string
	contentType string
	ifMatch     string
}

// proxyGet returns a read of target.
func proxyGet(target string) proxyCall {
	return proxyCall{method: http.MethodGet, target: target, body: "", contentType: "", ifMatch: ""}
}

// proxyPut returns a save of body at target with a content type and no If-Match.
func proxyPut(target, body, contentType string) proxyCall {
	return proxyCall{method: http.MethodPut, target: target, body: body, contentType: contentType, ifMatch: ""}
}

// serveProxy sends c to h. A PUT goes through gateEnv, which sends it as the
// console session with a fresh sign-in.
func serveProxy(t *testing.T, h http.Handler, c proxyCall) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), c.method, c.target, strings.NewReader(c.body))
	req.RemoteAddr = ipTestRemote
	req.Header.Set("Authorization", "Bearer test-token")

	if c.contentType != "" {
		req.Header.Set("Content-Type", c.contentType)
	}

	if c.ifMatch != "" {
		req.Header.Set("If-Match", c.ifMatch)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

// putProxy saves doc at target as JSON and returns the answer.
func putProxy(t *testing.T, h http.Handler, target, doc, ifMatch string) *httptest.ResponseRecorder {
	t.Helper()

	call := proxyPut(target, doc, proxyJSON)
	call.ifMatch = ifMatch

	return serveProxy(t, h, call)
}

// proxyRuleAnswer is one proxy rule of an answer.
type proxyRuleAnswer struct {
	ID   string `json:"id"`
	Port int    `json:"port"`
}

// proxyJoinedAnswer is one rule of a joined set with its scope.
type proxyJoinedAnswer struct {
	Scope string          `json:"scope"`
	Rule  proxyRuleAnswer `json:"rule"`
}

// proxySetAnswer is the answer of a read or a save.
type proxySetAnswer struct {
	Digest  string              `json:"digest"`
	Version int64               `json:"version"`
	Rules   []proxyRuleAnswer   `json:"rules"`
	Joined  []proxyJoinedAnswer `json:"joined"`
}

// proxyRenderedAnswer is the answer of the rendered view.
type proxyRenderedAnswer struct {
	Operation string              `json:"operation"`
	Rules     []proxyJoinedAnswer `json:"rules"`
}

func decodeProxySet(t *testing.T, rec *httptest.ResponseRecorder) proxySetAnswer {
	t.Helper()

	var out proxySetAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}

	return out
}

// TestProxyRulesSaveReadJoinRender saves a namespace set and a unit set, then
// reads the sets, the joined set and the rendered view.
func TestProxyRulesSaveReadJoinRender(t *testing.T) {
	t.Parallel()

	g := newGateEnv(t, proxyAdmin())

	empty := serveProxy(t, g, proxyGet(proxyNSPath))
	wantStatus(t, empty, http.StatusOK)

	if got := decodeProxySet(t, empty); got.Digest != "" || got.Version != 0 || len(got.Rules) != 0 {
		t.Fatalf("empty scope answer = %+v, want no set", got)
	}

	saved := putProxy(t, g, proxyNSPath, proxyRuleDoc, "")
	wantStatus(t, saved, http.StatusOK)

	ns := decodeProxySet(t, saved)
	if ns.Version != 1 || len(ns.Digest) != 64 || len(ns.Rules) != 1 || ns.Rules[0].Port != 443 {
		t.Fatalf("saved namespace set = %+v, want version 1, a digest and the effective port 443", ns)
	}

	if etag := saved.Header().Get("ETag"); etag != `"`+ns.Digest+`"` {
		t.Fatalf("ETag = %q, want the quoted digest", etag)
	}

	wantStatus(t, putProxy(t, g, proxyUnitURL, proxyOtherDoc, ""), http.StatusOK)

	checkProxyJoined(t, g)
	checkProxyRendered(t, g)
}

// checkProxyJoined reads the unit and checks the order of the joined set.
func checkProxyJoined(t *testing.T, g http.Handler) {
	t.Helper()

	unit := serveProxy(t, g, proxyGet(proxyUnitURL))
	wantStatus(t, unit, http.StatusOK)

	joined := decodeProxySet(t, unit).Joined
	if len(joined) != 2 || joined[0].Scope != "namespace" || joined[0].Rule.ID != "status" ||
		joined[1].Scope != "unit" || joined[1].Rule.ID != "other" {
		t.Fatalf("joined = %+v, want the namespace rule then the unit rule", joined)
	}
}

// checkProxyRendered reads the rendered view of the operation apply.
func checkProxyRendered(t *testing.T, g http.Handler) {
	t.Helper()

	rendered := serveProxy(t, g, proxyGet(proxyUnitURL+"/rendered?operation=apply"))
	wantStatus(t, rendered, http.StatusOK)

	var view proxyRenderedAnswer
	if err := json.Unmarshal(rendered.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode rendered view: %v", err)
	}

	if view.Operation != "apply" || len(view.Rules) != 1 || view.Rules[0].Scope != "unit" {
		t.Fatalf("rendered view = %+v, want the one unit rule that allows apply", view)
	}
}

// TestProxyRuleSetStaleDigestRefused sends a stale digest in If-Match.
func TestProxyRuleSetStaleDigestRefused(t *testing.T) {
	t.Parallel()

	g := newGateEnv(t, proxyAdmin())

	first := decodeProxySet(t, putProxy(t, g, proxyNSPath, proxyRuleDoc, ""))
	wantStatus(t, putProxy(t, g, proxyNSPath, proxyOtherDoc, `"`+first.Digest+`"`), http.StatusOK)

	stale := putProxy(t, g, proxyNSPath, proxyRuleDoc, `"`+first.Digest+`"`)
	wantStatus(t, stale, http.StatusPreconditionFailed)

	read := decodeProxySet(t, serveProxy(t, g, proxyGet(proxyNSPath)))
	if read.Version != 2 || read.Rules[0].ID != "other" {
		t.Fatalf("set after the refused save = %+v, want version 2 with the rule other", read)
	}
}

// proxyRefusal is one hostile request and the status that must answer it.
type proxyRefusal struct {
	name string
	env  http.Handler
	call proxyCall
	want int
}

// proxyRefusalsOfGrants returns the cases of a caller with too few grants.
func proxyRefusalsOfGrants(readOnly, noAccess http.Handler) []proxyRefusal {
	return []proxyRefusal{
		{
			"namespace save without the grant", readOnly,
			proxyPut(proxyNSPath, proxyRuleDoc, proxyJSON), http.StatusForbidden,
		},
		{
			"unit save without the grant", readOnly,
			proxyPut(proxyUnitURL, proxyRuleDoc, proxyJSON), http.StatusForbidden,
		},
		{"namespace read without the grant", noAccess, proxyGet(proxyNSPath), http.StatusForbidden},
		{"unit read without the grant", noAccess, proxyGet(proxyUnitURL), http.StatusForbidden},
		{
			"rendered view without read access", noAccess,
			proxyGet(proxyUnitURL + "/rendered?operation=plan"), http.StatusForbidden,
		},
	}
}

// proxyRefusalsOfPaths returns the cases of a path or a query that is not valid.
func proxyRefusalsOfPaths(g http.Handler) []proxyRefusal {
	return []proxyRefusal{
		{
			"rendered view of an unknown unit", g,
			proxyGet(proxyBase + "/units/nope/proxy-rules/rendered?operation=plan"), http.StatusNotFound,
		},
		{"unit ID with a bad shape", g, proxyGet(proxyBase + "/units/a.b/proxy-rules"), http.StatusBadRequest},
		{
			"rendered view with an unknown operation", g,
			proxyGet(proxyUnitURL + "/rendered?operation=deploy"), http.StatusBadRequest,
		},
		{"rendered view with no operation", g, proxyGet(proxyUnitURL + "/rendered"), http.StatusBadRequest},
	}
}

// proxyRefusalsOfBodies returns the cases of a save with a body that is not valid.
func proxyRefusalsOfBodies(g http.Handler) []proxyRefusal {
	save := func(body string) proxyCall { return proxyPut(proxyNSPath, body, proxyJSON) }

	return []proxyRefusal{
		{
			"save with another content type", g,
			proxyPut(proxyNSPath, proxyRuleDoc, "text/plain"), http.StatusUnsupportedMediaType,
		},
		{"save with no content type", g, proxyPut(proxyNSPath, proxyRuleDoc, ""), http.StatusUnsupportedMediaType},
		{
			"save above the size limit", g,
			save(`{"rules":[],"pad":"` + strings.Repeat("a", 70*1024) + `"}`), http.StatusRequestEntityTooLarge,
		},
		{"save with an unknown field", g, save(`{"rules":[],"extra":1}`), http.StatusUnprocessableEntity},
		{
			"save with an upper case host", g,
			save(strings.Replace(proxyRuleDoc, "status.example.com", "Status.example.com", 1)),
			http.StatusUnprocessableEntity,
		},
		{
			"save with an IP host", g,
			save(strings.Replace(proxyRuleDoc, "status.example.com", "10.0.0.1", 1)),
			http.StatusUnprocessableEntity,
		},
		{
			"save with a path that is not canonical", g,
			save(strings.Replace(proxyRuleDoc, "/v1/status", "/v1/../status", 1)),
			http.StatusUnprocessableEntity,
		},
	}
}

// proxyRefusalsOfHeaders returns the cases of an If-Match header that is not
// valid or not current, and of a namespace that does not exist.
func proxyRefusalsOfHeaders(g http.Handler) []proxyRefusal {
	noDigest := proxyPut(proxyNSPath, proxyRuleDoc, proxyJSON)
	noDigest.ifMatch = `"1"`

	stale := proxyPut(proxyNSPath, proxyRuleDoc, proxyJSON)
	stale.ifMatch = `"` + strings.Repeat("a", 64) + `"`

	return []proxyRefusal{
		{"save with an If-Match that is no digest", g, noDigest, http.StatusBadRequest},
		{"save with a stale digest for a scope with no set", g, stale, http.StatusPreconditionFailed},
		{
			"save to an unknown namespace", g,
			proxyPut("/api/dustpan/v1/namespaces/nope/proxy-rules", proxyRuleDoc, proxyJSON), http.StatusForbidden,
		},
	}
}

// TestProxyRulesHandlerRefusals sends hostile requests to the handler: each
// one must fail closed and change nothing.
func TestProxyRulesHandlerRefusals(t *testing.T) {
	t.Parallel()

	g := newGateEnv(t, proxyAdmin())
	readOnly := newGateEnv(t, gateAuthorizer{allow: map[identity.Action]namespace.Name{
		identity.ActionNamespaceRead: "acme",
		identity.ActionUnitRead:      "acme",
	}})
	noAccess := newGateEnv(t, gateAuthorizer{allow: nil})

	tests := slices.Concat(
		proxyRefusalsOfGrants(readOnly, noAccess), proxyRefusalsOfPaths(g),
		proxyRefusalsOfBodies(g), proxyRefusalsOfHeaders(g),
	)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := serveProxy(t, tt.env, tt.call)
			wantStatus(t, rec, tt.want)

			if strings.Contains(rec.Body.String(), "test-token") {
				t.Fatalf("answer holds the token: %s", rec.Body.String())
			}
		})
	}

	// Each refused save changed nothing.
	read := decodeProxySet(t, serveProxy(t, g, proxyGet(proxyNSPath)))
	if read.Version != 0 || len(read.Rules) != 0 {
		t.Fatalf("namespace set after the refusals = %+v, want no set", read)
	}
}

// TestProxyRulesTokenSaveNeedsFreshSignIn sends a save with an access token:
// it needs the fresh sign-in of a console session.
func TestProxyRulesTokenSaveNeedsFreshSignIn(t *testing.T) {
	t.Parallel()

	g := newGateEnv(t, proxyAdmin())

	rec := putProxy(t, g.env.handler, proxyNSPath, proxyRuleDoc, "")
	wantStatus(t, rec, http.StatusForbidden)

	if !strings.Contains(rec.Body.String(), "mfa.fresh-step-required") {
		t.Fatalf("answer = %s, want the code mfa.fresh-step-required", rec.Body.String())
	}

	rec = putProxy(t, g.env.handler, proxyUnitURL, proxyRuleDoc, "")
	wantStatus(t, rec, http.StatusForbidden)
}
