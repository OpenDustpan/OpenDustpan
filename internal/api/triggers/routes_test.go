package triggers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/triggers"
	"opendustpan.org/opendustpan/internal/cron"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/trigger"
)

// routeCase is a route with the request path and the checks that the route
// makes, in order.
type routeCase struct {
	route  triggers.Route
	path   string
	body   string
	checks []string
	reads  int
}

// casesOf returns a routeCase for each route of the table. The namespace read is
// the first check of each route.
func casesOf(t *testing.T) []routeCase {
	t.Helper()

	routes := newRig(t, newAuthorizer()).h.Routes()
	out := make([]routeCase, 0, len(routes))

	for _, route := range routes {
		path := strings.NewReplacer("{namespace}", "acme", "{unit_id}", netID, "{trigger_id}", "trg-seed").
			Replace(route.Path)
		object := "opentofu:acme/net"
		checks := make([]string, 0, 1+len(route.Reads)+len(route.Actions))
		checks = append(checks, string(identity.ActionNamespaceRead)+" acme")

		for _, a := range route.Reads {
			checks = append(checks, string(a)+" "+object)
		}

		reads := len(checks)

		for _, a := range route.Actions {
			checks = append(checks, string(a)+" "+object)
		}

		body := ""
		if (route.Method == http.MethodPost && !strings.HasSuffix(route.Path, "/fire")) ||
			route.Method == http.MethodPut {
			body = scheduleDoc
		}

		out = append(out, routeCase{route: route, path: path, body: body, checks: checks, reads: reads})
	}

	return out
}

// seed stores the trigger trg-seed on unit net.
func seed(r *rig) {
	r.store.plant(trigger.Trigger{
		ID: "trg-seed", Unit: unitNet(), Kind: trigger.KindSchedule,
		Frequency: trigger.Frequency{Preset: cron.PresetDaily, Cron: ""}, Source: noUnit(), Enabled: true,
		LastSaver: ada, LastSaverID: "prn-" + ada,
	})
}

func (rc *routeCase) wantStatus(held int) int {
	switch {
	case held < rc.reads:
		return http.StatusNotFound
	case held < len(rc.checks):
		return http.StatusForbidden
	case rc.route.Method == http.MethodDelete:
		return http.StatusNoContent
	case rc.route.Method == http.MethodPost:
		return http.StatusCreated
	default:
		return http.StatusOK
	}
}

// checkNoTokenRefused checks that each route fails closed for a request with no
// token: 401, never 404.
func checkNoTokenRefused(t *testing.T, handler http.Handler) {
	t.Helper()

	cases := casesOf(t)
	for i := range cases {
		rc := &cases[i]

		rec := serve(t, handler, request{method: rc.route.Method, path: rc.path, token: "", body: "", header: nil})
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("route %q with no token = %d, want 401", rc.route.Method+" "+rc.route.Path, rec.Code)
		}
	}
}

// checkRouteShape fails for a route with no scope, no read action or no
// resolver, and for a write route with no action.
func checkRouteShape(t *testing.T, pattern string, route triggers.Route) {
	t.Helper()

	switch {
	case route.Scope != triggers.ScopeAuthorized:
		t.Errorf("route %q is not authorized: this API serves no public route", pattern)
	case len(route.Reads) == 0:
		t.Errorf("route %q declares no read action", pattern)
	case route.Resolve == nil || route.Serve == nil:
		t.Errorf("route %q declares no resolver or no handler", pattern)
	case (route.Method == http.MethodGet) != (len(route.Actions) == 0):
		t.Errorf("route %q: only a read route may declare no action", pattern)
	default:
	}
}

// TestRoutesDeclareActions walks every route and fails for a route with no
// scope, no read action or no resolver, and for a write route with no action.
// It checks both directions: every served pattern has a table row, and every
// table row is served.
func TestRoutesDeclareActions(t *testing.T) {
	t.Parallel()

	handler := newRig(t, newAuthorizer()).h
	byPattern := map[string]triggers.Route{}

	for _, route := range handler.Routes() {
		pattern := route.Method + " " + route.Path
		if _, dup := byPattern[pattern]; dup {
			t.Fatalf("duplicate route %q", pattern)
		}

		byPattern[pattern] = route

		checkRouteShape(t, pattern, route)
	}

	for _, pattern := range handler.RegisteredPatterns() {
		if _, ok := byPattern[pattern]; !ok {
			t.Errorf("served route %q has no table row", pattern)
		}
	}

	if len(byPattern) != len(handler.RegisteredPatterns()) {
		t.Errorf("the table has %d rows and the mux %d patterns", len(byPattern), len(handler.RegisteredPatterns()))
	}

	checkNoTokenRefused(t, handler)
}

// TestNoMuxBypass proves every served route goes through the recorder: it fails
// if any production file in this package builds a ServeMux or touches the inner
// mux of patternMux outside routes.go.
func TestNoMuxBypass(t *testing.T) {
	t.Parallel()

	entries, err := packageSources.ReadDir(".")
	if err != nil {
		t.Fatalf("read package sources: %v", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if name == "routes.go" || strings.HasSuffix(name, "_test.go") {
			continue
		}

		content, err := packageSources.ReadFile(name)
		if err != nil {
			t.Fatalf("read file %s: %v", name, err)
		}

		for _, needle := range []string{"NewServeMux", "servemux"} {
			if strings.Contains(string(content), needle) {
				t.Fatalf("file %s reaches the mux without recording (contains %q)", name, needle)
			}
		}
	}
}

// TestRoutesEnforceTheChecks proves, for every route, the order and the answer
// of the checks: a refused namespace read or unit read answers the one 404, a
// refused action answers 403, and a caller who holds every check passes. A
// refused write never reaches the store or the dispatcher.
func TestRoutesEnforceTheChecks(t *testing.T) {
	t.Parallel()

	for _, rc := range casesOf(t) {
		for held := range len(rc.checks) + 1 {
			t.Run(fmt.Sprintf("%s %s holding %d of %d checks", rc.route.Method, rc.route.Path, held, len(rc.checks)),
				func(t *testing.T) {
					t.Parallel()

					checkRouteHolding(t, &rc, held)
				})
		}
	}
}

// checkRouteHolding calls the route of rc as a caller that holds the first held
// checks, and checks the status, the order of the checks and the writes.
func checkRouteHolding(t *testing.T, rc *routeCase, held int) {
	t.Helper()

	authz := newAuthorizer(rc.checks[:held]...)
	r := newRig(t, authz)
	seed(r)

	before := r.store.written()
	rec := serve(t, r.h, request{
		method: rc.route.Method, path: rc.path, token: testToken, body: rc.body, header: nil,
	})

	want := rc.wantStatus(held)
	if rec.Code != want {
		t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body.String(), want)
	}

	got := authz.seen()
	// The namespace read of the path runs once before the lookup, for the IP
	// allowlist, and once more with the other checks.
	wantChecks := append([]string{rc.checks[0]}, rc.checks[:min(held+1, len(rc.checks))]...)

	if len(got) < len(wantChecks) || !slices.Equal(got[:len(wantChecks)], wantChecks) {
		t.Errorf("checks = %q, want %q first, in order", got, wantChecks)
	}

	if rc.route.Method == http.MethodGet {
		return
	}

	wrote := r.store.written() != before || r.disp.fires() > 0
	if wrote != (held == len(rc.checks)) {
		t.Errorf("writes = %v for status %d", wrote, want)
	}
}

// TestUnknownObjectsAnswerAlike proves an unknown namespace, an unknown unit, a
// unit of another namespace, a deleted unit, an unknown trigger and a trigger of
// another unit answer 404 before any check of the caller runs for the object.
func TestUnknownObjectsAnswerAlike(t *testing.T) {
	t.Parallel()

	authz := newAuthorizer(holdsAll()...)
	r := newRig(t, authz)
	seed(r)
	r.store.plant(trigger.Trigger{
		ID: "trg-app", Unit: graphUnit("pulumi", appID), Kind: trigger.KindSchedule,
		Frequency: trigger.Frequency{Preset: cron.PresetDaily, Cron: ""}, Source: noUnit(), Enabled: true,
		LastSaver: ada, LastSaverID: "prn-" + ada,
	})

	r.inv.gone["ws-gone"] = true

	for _, path := range []string{
		"/api/dustpan/v1/namespaces/absent/units/" + netID + "/triggers",
		"/api/dustpan/v1/namespaces/acme/units/ws-absent/triggers",
		"/api/dustpan/v1/namespaces/acme/units/ws-gone/triggers",
		"/api/dustpan/v1/namespaces/other/units/" + netID + "/triggers",
		"/api/dustpan/v1/namespaces/acme/units/" + otherID + "/triggers",
		"/api/dustpan/v1/namespaces/BAD%20NAME/units/" + netID + "/triggers",
		"/api/dustpan/v1/namespaces/acme/units/" + strings.Repeat("x", 101) + "/triggers",
		basePath + "/trg-nope",
		basePath + "/TRG-SEED",
		basePath + "/trg-app",
		"/api/dustpan/v1/namespaces/acme/units/" + appID + "/triggers/trg-seed",
	} {
		rec := serve(t, r.h, request{method: http.MethodGet, path: path, token: testToken, body: "", header: nil})

		var body struct {
			Message string `json:"message"`
		}

		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusNotFound ||
			body.Message != "not found" {
			t.Errorf("GET %s = %d %s, want the 404 of an unknown object", path, rec.Code, rec.Body.String())
		}
	}

	if got := authz.seen(); slices.ContainsFunc(got, func(c string) bool { return strings.Contains(c, "unit:") }) {
		t.Errorf("checks = %q, want no unit check for an object that does not exist", got)
	}
}

// TestKeyedWritesAreRefused proves a write with an Idempotency-Key header
// answers 400, because no route of this API takes the key except the fire
// route.
func TestKeyedWritesAreRefused(t *testing.T) {
	t.Parallel()

	for _, rc := range casesOf(t) {
		if rc.route.Method == http.MethodGet || rc.route.IdempotencyKey {
			continue
		}

		r := newRig(t, newAuthorizer(holdsAll()...))
		seed(r)

		rec := serve(t, r.h, request{
			method: rc.route.Method, path: rc.path, token: testToken, body: rc.body,
			header: map[string]string{"Idempotency-Key": "k-1"},
		})

		if rec.Code != http.StatusBadRequest ||
			!strings.Contains(rec.Body.String(), "request.idempotency-key-unsupported") {
			t.Errorf("%s %s with a key = %d %s, want 400", rc.route.Method, rc.route.Path, rec.Code, rec.Body.String())
		}

		if r.store.written() != 0 || r.disp.fires() != 0 {
			t.Errorf("%s %s with a key wrote", rc.route.Method, rc.route.Path)
		}
	}
}
