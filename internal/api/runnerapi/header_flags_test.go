package runnerapi_test

import (
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/runnerapi"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// headerBehavior is what a route of the runner adapter does with the three
// conditional-request headers. The runner protocol and the runner management
// routes are outside the description of the tool-neutral REST API, so no
// generated client reads a flag for them. This table is the record of their
// behavior, and the test below compares it with the handler.
type headerBehavior struct {
	// body is the JSON body of the probe request, empty for none.
	body string
	// etag is true when a success answer holds the ETag header.
	etag bool
	// ifMatch is true when the route honors the If-Match header.
	ifMatch bool
	// idempotencyKey is true when the route replays the answer for a repeated
	// Idempotency-Key header.
	idempotencyKey bool
}

// noHeaders is the row of a route that sets no ETag, ignores If-Match and
// ignores Idempotency-Key.
func noHeaders(body string) headerBehavior {
	return headerBehavior{body: body, etag: false, ifMatch: false, idempotencyKey: false}
}

// TestHeaderFlagsMatchTheHandler proves, for each route of the runner
// adapter, what the handler does with the ETag, If-Match and Idempotency-Key
// headers. A route without a row fails the test, so a route cannot enter
// without a decision about its headers. A row that claims a header behavior
// fails too, until the test has a scenario for it.
//
// A management route is probed as the administrator. A protocol route is
// probed with no runner proof, so the probe covers the checks before the
// handler; the flow tests cover the handler of each protocol route.
func TestHeaderFlagsMatchTheHandler(t *testing.T) {
	t.Parallel()

	want := map[string]headerBehavior{
		"GET /api/runners/{namespace}/pools": noHeaders(""),
		"POST /api/runners/{namespace}/pools": noHeaders(
			`{"name":"prod","purpose":"jobs"}`,
		),
		"GET /api/runners/{namespace}/pools/{pool}":                          noHeaders(""),
		"PATCH /api/runners/{namespace}/pools/{pool}":                        noHeaders("{}"),
		"DELETE /api/runners/{namespace}/pools/{pool}":                       noHeaders(""),
		"GET /api/runners/{namespace}/pools/{pool}/join-tokens":              noHeaders(""),
		"POST /api/runners/{namespace}/pools/{pool}/join-tokens":             noHeaders(""),
		"POST /api/runners/{namespace}/pools/{pool}/join-tokens/{id}/revoke": noHeaders(""),
		"GET /api/runners/{namespace}/pools/{pool}/runners":                  noHeaders(""),
		"POST /api/runners/{namespace}/runners/{runner}/drain":               noHeaders(""),
		"POST /api/runners/{namespace}/runners/{runner}/retire":              noHeaders(""),
		"POST " + protocol.PathJoin:                                          noHeaders("{}"),
		"POST " + protocol.PathRotate:                                        noHeaders("{}"),
		"POST " + protocol.PathHeartbeat:                                     noHeaders("{}"),
		"POST " + protocol.PathPoll:                                          noHeaders("{}"),
		"POST " + protocol.PathClaimHandoff:                                  noHeaders("{}"),
		"POST " + protocol.PathClaimRenew:                                    noHeaders("{}"),
		"POST " + protocol.PathClaimRelease:                                  noHeaders("{}"),
		"POST " + protocol.PathSession:                                       noHeaders("{}"),
		"POST " + protocol.PathSessionRenew:                                  noHeaders("{}"),
		"POST " + protocol.PathSessionStepBegin:                              noHeaders("{}"),
		"POST " + protocol.PathSessionStepEnd:                                noHeaders("{}"),
		"PUT " + protocol.PathSessionLog:                                     noHeaders("{}"),
		"POST " + protocol.PathSessionFinish:                                 noHeaders("{}"),
		"PUT " + protocol.PathSessionPlanUpload:                              noHeaders("{}"),
		"POST " + protocol.PathSessionPlanCommit:                             noHeaders("{}"),
		"GET " + protocol.PathSessionPlanPart:                                noHeaders(""),
	}

	routes := newEnv(t).handler.Routes()
	if len(routes) != len(want) {
		t.Fatalf("the handler serves %d routes, the test has %d rows", len(routes), len(want))
	}

	for i := range routes {
		rt := routes[i]
		pattern := rt.Method + " " + rt.Path

		row, ok := want[pattern]
		if !ok {
			t.Fatalf("route %s has no header row", pattern)
		}

		t.Run(pattern, func(t *testing.T) {
			t.Parallel()

			if row.ifMatch || row.idempotencyKey {
				t.Fatalf("route %s: add a scenario for If-Match or Idempotency-Key, "+
					"as internal/api/jobs/header_flags_test.go does", pattern)
			}

			checkRouteHeaders(t, rt, row)
		})
	}
}

// answer is what the handler answers to one probe.
type answer struct {
	status int
	header http.Header
}

// checkRouteHeaders sends the route three requests, each to a new server: a
// plain one, one with a stale If-Match, and one with an Idempotency-Key. It
// compares the answers with the row.
func checkRouteHeaders(t *testing.T, rt runnerapi.Route, row headerBehavior) {
	t.Helper()

	plain := probeRoute(t, rt, row, nil)
	if plain.status == http.StatusPreconditionFailed || plain.status == http.StatusPreconditionRequired {
		t.Fatalf("%s answers %d to a request with no precondition", rt.Path, plain.status)
	}

	if has := plain.header.Get("ETag") != ""; has != row.etag {
		t.Errorf("%s: ETag header present = %t, row says %t", rt.Path, has, row.etag)
	}

	stale := probeRoute(t, rt, row, map[string]string{"If-Match": `"stale-1000"`})
	if stale.status != plain.status {
		t.Errorf("%s with a stale If-Match = %d, want %d: the handler reads a header that the row does not show",
			rt.Path, stale.status, plain.status)
	}

	keyed := probeRoute(t, rt, row, map[string]string{"Idempotency-Key": "header-test-key-1"})
	if keyed.status != plain.status {
		t.Errorf("%s with an Idempotency-Key = %d, want %d: the handler reads a header that the row does not show",
			rt.Path, keyed.status, plain.status)
	}

	if keyed.header.Get("Idempotent-Replayed") != "" {
		t.Errorf("%s answers an Idempotency-Key as a replay, but the row shows no key", rt.Path)
	}
}

// probeRoute sends one request to the route on a new server, with the extra
// headers.
func probeRoute(t *testing.T, rt runnerapi.Route, row headerBehavior, headers map[string]string) answer {
	t.Helper()

	e := newEnv(t)
	path := strings.NewReplacer(
		"{namespace}", "acme", "{pool}", "rp-1", "{id}", "rjt-1", "{runner}", "rn-1", "{operation}", "op-1",
	).Replace(rt.Path)

	req, err := http.NewRequestWithContext(t.Context(), rt.Method, e.server.URL+path, strings.NewReader(row.body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	if row.body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	if rt.Scope == runnerapi.ScopeAuthorized {
		req.Header.Set("Authorization", "token "+adminToken)
	} else {
		req.Header.Set(protocol.HeaderRelease, "0.4.0")
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := e.server.Client().Do(req)
	if err != nil {
		t.Fatalf("send %s %s: %v", req.Method, req.URL.Path, err)
	}

	if err := resp.Body.Close(); err != nil {
		t.Errorf("close body: %v", err)
	}

	return answer{status: resp.StatusCode, header: resp.Header}
}
