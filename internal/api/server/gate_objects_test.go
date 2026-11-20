package server_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
)

const (
	freezeGateDoc = `{"decisionPoint":"state.write","condition":"context.operation.kind == \"write\"",` +
		`"outcome":"deny","message":"Writes are frozen","mode":"enforce"}`
	noWritesDoc = `{"policy":"forbid (principal, action == OpenDustpan::Action::\"unit:write\", resource);"}`
)

// gateScopeURLs are the three scope levels of the gate routes under acme.
func gateScopeURLs() map[string]string {
	return map[string]string{
		"namespace":  gateBase + "acme",
		"collection": gateBase + "acme/collections/payments",
		"unit":       gateBase + "acme/units/by-name/pulumi/web%2Fprod",
	}
}

type gateObjectDoc struct {
	ID            string          `json:"id"`
	DecisionPoint string          `json:"decisionPoint"`
	Outcome       string          `json:"outcome"`
	Mode          string          `json:"mode"`
	OnError       string          `json:"onError"`
	Final         bool            `json:"final"`
	CheckedWith   *checkedWithDoc `json:"checkedWith"`
	Policy        string          `json:"policy"`
	Version       int64           `json:"version"`
}

type checkedWithDoc struct {
	Engine string `json:"engine"`
	Schema string `json:"schema"`
}

type gateObjectList struct {
	Gates        []gateObjectDoc `json:"gates"`
	AccessLimits []gateObjectDoc `json:"accessLimits"`
}

// TestGateRoutesLifecycle follows one gate and one access limit through
// create, read, list, replace, a stale replace, delete and a second read at
// each of the three scope levels.
func TestGateRoutesLifecycle(t *testing.T) {
	t.Parallel()

	for level, base := range gateScopeURLs() {
		for _, res := range []struct{ segment, doc string }{{"gates", freezeGateDoc}, {"access-limits", noWritesDoc}} {
			t.Run(level+"/"+res.segment, func(t *testing.T) {
				t.Parallel()

				runGateLifecycle(t, level, base, res.segment, res.doc)
			})
		}
	}
}

// runGateLifecycle is the body of one case of TestGateRoutesLifecycle.
func runGateLifecycle(t *testing.T, level, base, segment, doc string) {
	t.Helper()

	_, h := newGateHandler(t, adminGates())
	item := base + "/" + segment + "/freeze"

	wantStatus(t, serveGate(t, h, gateCall{http.MethodGet, item, "", ""}), http.StatusNotFound)

	rec := serveGate(t, h, gateCall{http.MethodPut, item, doc, ""})
	wantStatus(t, rec, http.StatusOK)

	var created gateObjectDoc

	decodeGateJSON(t, rec, &created)

	if created.ID != "freeze" || created.Version != 1 || rec.Header().Get("ETag") != `"1"` {
		t.Fatalf("created = %+v, ETag %q; want id freeze, version 1, ETag \"1\"", created, rec.Header().Get("ETag"))
	}

	// A list shows the objects of its own scope only.
	for other, otherBase := range gateScopeURLs() {
		var list gateObjectList

		decodeGateJSON(t, serveGate(t, h, gateCall{http.MethodGet, otherBase + "/" + segment, "", ""}), &list)

		want := 0
		if other == level {
			want = 1
		}

		if got := len(list.Gates) + len(list.AccessLimits); got != want {
			t.Errorf("%s list holds %d objects of %s, want %d", other, got, level, want)
		}
	}

	wantStatus(t, serveGate(t, h, gateCall{http.MethodPut, item, doc, `"7"`}), http.StatusConflict)
	wantStatus(t, serveGate(t, h, gateCall{http.MethodPut, item, doc, `"1"`}), http.StatusOK)
	wantStatus(t, serveGate(t, h, gateCall{http.MethodDelete, item, "", `"1"`}), http.StatusConflict)
	wantStatus(t, serveGate(t, h, gateCall{http.MethodDelete, item, "", `"2"`}), http.StatusNoContent)
	wantStatus(t, serveGate(t, h, gateCall{http.MethodGet, item, "", ""}), http.StatusNotFound)
	wantStatus(t, serveGate(t, h, gateCall{http.MethodDelete, item, "", ""}), http.StatusNotFound)
}

// TestGatePutShowsTheEffectiveValues proves that a gate with no mode and no
// onError answers the defaults, and the engine and the schema that it passed.
func TestGatePutShowsTheEffectiveValues(t *testing.T) {
	t.Parallel()

	_, h := newGateHandler(t, adminGates())
	doc := `{"decisionPoint":"state.write","condition":"context.operation.kind == \"write\"",` +
		`"outcome":"warn","message":"Careful"}`

	rec := serveGate(t, h, gateCall{http.MethodPut, gateBase + "acme/gates/careful", doc, ""})
	wantStatus(t, rec, http.StatusOK)

	var got gateObjectDoc

	decodeGateJSON(t, rec, &got)

	if got.Mode != "evaluate" || got.OnError != "fail" || got.Final || got.CheckedWith == nil ||
		got.CheckedWith.Engine == "" || got.CheckedWith.Schema != "state.write/1.0" {
		t.Fatalf(
			"gate = %+v, want mode evaluate, onError fail, not final, checked with the engine and state.write/1.0",
			got,
		)
	}
}

// TestGateRoutesRefusals proves that a request that is not valid answers its
// status and stores nothing.
func TestGateRoutesRefusals(t *testing.T) {
	t.Parallel()

	const goodCondition = `context.operation.kind == \"write\"`

	gateDoc := func(extra string) string {
		return `{"decisionPoint":"state.write","condition":"` + goodCondition + `","outcome":"deny","message":"m"` + extra + `}`
	}

	tests := []struct {
		name   string
		method string
		target string
		body   string
		want   int
	}{
		{"unknown field", http.MethodPut, "acme/gates/x", gateDoc(`,"enabled":true`), http.StatusBadRequest},
		{"id in the body", http.MethodPut, "acme/gates/x", gateDoc(`,"id":"x"`), http.StatusBadRequest},
		{"body is not JSON", http.MethodPut, "acme/gates/x", `{`, http.StatusBadRequest},
		{"trailing data", http.MethodPut, "acme/gates/x", gateDoc("") + `{}`, http.StatusBadRequest},
		{"upper-case id", http.MethodPut, "acme/gates/X", gateDoc(""), http.StatusUnprocessableEntity},
		{"built-in id", http.MethodPut, "acme/gates/builtin%2Fx", gateDoc(""), http.StatusUnprocessableEntity},
		{
			"unknown decision point",
			http.MethodPut,
			"acme/gates/x",
			strings.Replace(gateDoc(""), "state.write", "state.read", 1),
			http.StatusUnprocessableEntity,
		},
		{"unknown mode", http.MethodPut, "acme/gates/x", gateDoc(`,"mode":"observe"`), http.StatusUnprocessableEntity},
		{
			"unknown outcome",
			http.MethodPut,
			"acme/gates/x",
			strings.Replace(gateDoc(""), "deny", "allow", 1),
			http.StatusUnprocessableEntity,
		},
		{
			"condition the engine refuses",
			http.MethodPut,
			"acme/gates/x",
			strings.Replace(gateDoc(""), goodCondition, "context.nothing == 1", 1),
			http.StatusUnprocessableEntity,
		},
		{
			"condition that does not parse",
			http.MethodPut,
			"acme/gates/x",
			strings.Replace(gateDoc(""), goodCondition, "((", 1),
			http.StatusUnprocessableEntity,
		},
		{
			"no message",
			http.MethodPut,
			"acme/gates/x",
			strings.Replace(gateDoc(""), `"message":"m"`, `"message":""`, 1),
			http.StatusUnprocessableEntity,
		},
		{
			"reason with a line break",
			http.MethodPut,
			"acme/gates/x",
			gateDoc(`,"reason":"a\nb"`),
			http.StatusUnprocessableEntity,
		},
		{
			"permit policy",
			http.MethodPut,
			"acme/access-limits/x",
			`{"policy":"permit (principal, action, resource);"}`,
			http.StatusUnprocessableEntity,
		},
		{
			"limit policy that does not parse",
			http.MethodPut,
			"acme/access-limits/x",
			`{"policy":"forbid ("}`,
			http.StatusUnprocessableEntity,
		},
		{
			"limit names an unknown action",
			http.MethodPut,
			"acme/access-limits/x",
			`{"policy":"forbid (principal, action == OpenDustpan::Action::\"nope:nope\", resource);"}`,
			http.StatusUnprocessableEntity,
		},
		{"empty limit", http.MethodPut, "acme/access-limits/x", `{"policy":""}`, http.StatusUnprocessableEntity},
		{
			"collection name with a slash",
			http.MethodPut,
			"acme/collections/bad%2Fname/gates/x",
			gateDoc(""),
			http.StatusBadRequest,
		},
		{
			"unit with an empty tool name",
			http.MethodGet,
			"acme/units/by-name/Bad%20Tool/web/gates",
			"",
			http.StatusBadRequest,
		},
		{"bad namespace", http.MethodGet, "a%20b/gates", "", http.StatusBadRequest},
		{"If-Match that is not a version", http.MethodDelete, "acme/gates/x", "", http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			st, h := newGateHandler(t, adminGates())

			rec := serveGate(t, h, gateCall{tt.method, gateBase + tt.target, tt.body, ""})
			if rec.Code != tt.want {
				t.Fatalf("%s %s = %d, want %d: %s", tt.method, tt.target, rec.Code, tt.want, rec.Body.String())
			}

			for _, e := range listNamespaceEntries(t, st, "acme") {
				if strings.HasPrefix(e.Action, "namespace:set-") && tt.want >= http.StatusBadRequest {
					t.Errorf("a refused request wrote the audit entry %+v", e)
				}
			}
		})
	}
}

// gateAuthCase is one request of TestGateRoutesAuthorization.
type gateAuthCase struct {
	name   string
	allow  map[identity.Action]namespace.Name
	method string
	suffix string
	body   string
	want   int
}

// gateAuthCases returns the requests that TestGateRoutesAuthorization sends at
// each scope level. The suffix follows the base path of the level.
func gateAuthCases() []gateAuthCase {
	read, manage := identity.ActionNamespaceReadGates, identity.ActionNamespaceManageGates
	readOnly := map[identity.Action]namespace.Name{read: "acme"}
	manageOnly := map[identity.Action]namespace.Name{manage: "acme"}

	return []gateAuthCase{
		{"list needs read", manageOnly, http.MethodGet, "/gates", "", http.StatusForbidden},
		{"limit list needs read", manageOnly, http.MethodGet, "/access-limits", "", http.StatusForbidden},
		{"get with read", readOnly, http.MethodGet, "/gates/x", "", http.StatusNotFound},
		{"put needs manage", readOnly, http.MethodPut, "/gates/x", freezeGateDoc, http.StatusForbidden},
		{"limit put needs manage", readOnly, http.MethodPut, "/access-limits/x", noWritesDoc, http.StatusForbidden},
		{"delete needs manage", readOnly, http.MethodDelete, "/gates/x", "", http.StatusForbidden},
		{"limit delete needs manage", readOnly, http.MethodDelete, "/access-limits/x", "", http.StatusForbidden},
		{
			"manage on another namespace",
			map[identity.Action]namespace.Name{manage: "beta", read: "beta"},
			http.MethodPut, "/gates/x", freezeGateDoc, http.StatusForbidden,
		},
		{"no action at all", nil, http.MethodGet, "/gates", "", http.StatusForbidden},
	}
}

// runGateAuthCase sends the request of tt at the base path and checks the
// status. A refusal must leave no audit entry of a change.
func runGateAuthCase(t *testing.T, base string, tt gateAuthCase) {
	t.Helper()

	st, h := newGateHandler(t, gateAuthorizer{allow: tt.allow})

	rec := serveGate(t, h, gateCall{tt.method, base + tt.suffix, tt.body, ""})
	wantStatus(t, rec, tt.want)

	if tt.want != http.StatusForbidden {
		return
	}

	entries := listNamespaceEntries(t, st, "acme")
	for i := range entries {
		if a := entries[i].Action; strings.HasPrefix(a, "namespace:set-") || strings.HasPrefix(a, "namespace:delete-") {
			t.Errorf("a denied request wrote the audit entry %+v", entries[i])
		}
	}
}

// TestGateRoutesAuthorization proves that each route needs its action on the
// namespace of the path, also for a collection and a unit of it.
func TestGateRoutesAuthorization(t *testing.T) {
	t.Parallel()

	for level, base := range gateScopeURLs() {
		for _, tt := range gateAuthCases() {
			t.Run(level+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				runGateAuthCase(t, base, tt)
			})
		}
	}
}

// TestGateRoutesRefuseAccessTokenWrites proves that an access token never
// gives the fresh authentication that a write needs: each write answers 403
// and stores nothing. A read with the token works.
func TestGateRoutesRefuseAccessTokenWrites(t *testing.T) {
	t.Parallel()

	g := newGateEnv(t, adminGates())
	base := gateBase + "acme"

	wantStatus(t, serveGate(t, g, gateCall{http.MethodPut, base + "/gates/seed", freezeGateDoc, ""}), http.StatusOK)
	wantStatus(
		t,
		serveGate(t, g, gateCall{http.MethodPut, base + "/access-limits/seed", noWritesDoc, ""}),
		http.StatusOK,
	)

	for _, c := range []gateCall{
		{http.MethodPut, base + "/gates/seed", freezeGateDoc, ""},
		{http.MethodPut, base + "/gates/new", freezeGateDoc, ""},
		{http.MethodDelete, base + "/gates/seed", "", ""},
		{http.MethodPut, base + "/access-limits/seed", noWritesDoc, ""},
		{http.MethodDelete, base + "/access-limits/seed", "", ""},
	} {
		rec := serveGate(t, g.env.handler, c)
		wantStatus(t, rec, http.StatusForbidden)

		if !strings.Contains(rec.Body.String(), `"errorCode":"mfa.fresh-step-required"`) {
			t.Errorf("%s %s body = %s, want the code mfa.fresh-step-required", c.method, c.target, rec.Body.String())
		}
	}

	wantStatus(t, serveGate(t, g.env.handler, gateCall{http.MethodGet, base + "/gates/seed", "", ""}), http.StatusOK)
	wantStatus(
		t,
		serveGate(t, g.env.handler, gateCall{http.MethodGet, base + "/gates/new", "", ""}),
		http.StatusNotFound,
	)
}

// TestGateRoutesWriteAuditEntries proves that each write adds one entry of
// the kind change with the action, the object and the version.
func TestGateRoutesWriteAuditEntries(t *testing.T) {
	t.Parallel()

	st, h := newGateHandler(t, adminGates())
	unit := gateScopeURLs()["unit"]

	for _, c := range []gateCall{
		{http.MethodPut, unit + "/gates/freeze", freezeGateDoc, ""},
		{http.MethodGet, unit + "/gates/freeze", "", ""},
		// A switch to evaluate adds the old and the new mode to the entry.
		{http.MethodPut, unit + "/gates/freeze", strings.Replace(freezeGateDoc, "enforce", "evaluate", 1), ""},
		{http.MethodPut, unit + "/access-limits/no-writes", noWritesDoc, ""},
		{http.MethodDelete, unit + "/access-limits/no-writes", "", ""},
		{http.MethodDelete, unit + "/gates/freeze", "", ""},
	} {
		if rec := serveGate(t, h, c); rec.Code >= http.StatusBadRequest {
			t.Fatalf("%s %s = %d: %s", c.method, c.target, rec.Code, rec.Body.String())
		}
	}

	checkUnitAuditEntries(t, st)
}

// checkUnitAuditEntries checks the five entries that the writes of
// TestGateRoutesWriteAuditEntries leave in acme.
func checkUnitAuditEntries(t *testing.T, st *sqlite.Store) {
	t.Helper()

	var got []audit.Entry

	entries := listNamespaceEntries(t, st, "acme")
	for i := range entries {
		if a := entries[i].Action; strings.Contains(a, "-gate") || strings.Contains(a, "-access-limit") {
			got = append(got, entries[i])
		}
	}

	if len(got) != 5 {
		t.Fatalf("entries = %d, want 5 (set gate twice, set and delete access limit, delete gate): %+v", len(got), got)
	}

	modeSwitches := 0

	for i := range got {
		checkUnitEntry(t, &got[i])

		modeSwitches += countModeSwitches(got[i].Changes)
	}

	if modeSwitches != 1 {
		t.Errorf("mode switches in the entries = %d, want 1", modeSwitches)
	}
}

// checkUnitEntry fails unless e is a successful change by a caller, on the
// unit web/prod of the Pulumi tool.
func checkUnitEntry(t *testing.T, e *audit.Entry) {
	t.Helper()

	if e.Kind != audit.KindChange || e.Outcome != audit.OutcomeSuccess || e.Subject == "" ||
		!strings.HasPrefix(e.Object, "unit/acme/pulumi/web%2Fprod/") {
		t.Errorf("entry = %+v, want kind change, success, a subject and an object of the unit", e)
	}
}

// countModeSwitches counts the changes from enforce to evaluate of the gate freeze.
func countModeSwitches(changes []audit.Change) int {
	n := 0

	for _, c := range changes {
		if c.Key == "gate/freeze.mode" && c.New == "evaluate" && c.Old != nil && *c.Old == "enforce" {
			n++
		}
	}

	return n
}

// TestFinalGateNeedsAReasonToDelete proves that a final gate cannot be deleted
// without a reason, and that the reason reaches the audit entry.
func TestFinalGateNeedsAReasonToDelete(t *testing.T) {
	t.Parallel()

	st, h := newGateHandler(t, adminGates())
	item := gateBase + "acme/gates/hold"
	doc := strings.Replace(freezeGateDoc, `"mode":"enforce"`, `"mode":"enforce","final":true`, 1)

	wantStatus(t, serveGate(t, h, gateCall{http.MethodPut, item, doc, ""}), http.StatusOK)

	// A weaker replace needs a reason too.
	weaker := strings.Replace(doc, "enforce", "evaluate", 1)
	wantStatus(t, serveGate(t, h, gateCall{http.MethodPut, item, weaker, ""}), http.StatusConflict)

	withReason := strings.Replace(weaker, `"final":true`, `"final":true,"reason":"freeze ends"`, 1)
	wantStatus(t, serveGate(t, h, gateCall{http.MethodPut, item, withReason, ""}), http.StatusOK)

	wantStatus(t, serveGate(t, h, gateCall{http.MethodDelete, item, "", ""}), http.StatusConflict)
	wantStatus(
		t,
		serveGate(t, h, gateCall{http.MethodDelete, item + "?reason=freeze%20ended", "", ""}),
		http.StatusNoContent,
	)

	reasons := 0

	for _, e := range listNamespaceEntries(t, st, "acme") {
		if gate.Reason(e) != "" {
			reasons++
		}
	}

	if reasons != 2 {
		t.Errorf("entries with a reason = %d, want 2 (the weaker replace and the delete)", reasons)
	}
}

// TestGateRoutesNotMountedWithoutStore proves that a server whose store keeps
// no gate configuration serves none of the routes.
func TestGateRoutesNotMountedWithoutStore(t *testing.T) {
	t.Parallel()

	h := newGateHandlerWithoutStore(t, adminGates())

	for _, base := range gateScopeURLs() {
		for _, seg := range []string{"/gates", "/access-limits"} {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, base+seg, http.NoBody)
			req.Header.Set("Authorization", "Bearer test-token")
			req.RemoteAddr = ipTestRemote

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Errorf("GET %s = %d, want 404", base+seg, rec.Code)
			}
		}
	}
}

// gateCodeCase is one request of TestGateRoutesAnswerCodes. setup lists the
// objects to make first, as "path" or "path:final".
type gateCodeCase struct {
	name   string
	setup  []string
	method string
	suffix string
	body   string
	ifm    string
	status int
	code   string
}

// TestGateRoutesAnswerCodes proves that each answer of the gate and access
// limit routes names a code of the catalog, at each scope level.
func TestGateRoutesAnswerCodes(t *testing.T) {
	t.Parallel()

	tooLong := strings.Replace(freezeGateDoc, `context.operation.kind == \"write\"`, strings.Repeat("a", 5000), 1)
	weaker := strings.Replace(freezeGateDoc, "enforce", "evaluate", 1)
	tests := []gateCodeCase{
		{"gate not found", nil, http.MethodGet, "/gates/x", "", "", http.StatusNotFound, "gate.not-found"},
		{"limit not found", nil, http.MethodGet, "/access-limits/x", "", "", http.StatusNotFound, "gate.not-found"},
		{
			"delete of a gate that is not there",
			nil,
			http.MethodDelete,
			"/gates/x",
			"",
			"",
			http.StatusNotFound,
			"gate.not-found",
		},
		{
			"invalid gate",
			nil,
			http.MethodPut,
			"/gates/x",
			strings.Replace(freezeGateDoc, "deny", "allow", 1),
			"",
			http.StatusUnprocessableEntity,
			"gate.invalid",
		},
		{
			"condition without the context prefix",
			nil,
			http.MethodPut,
			"/gates/x",
			strings.Replace(freezeGateDoc, "context.operation.kind", "operation.kind", 1),
			"",
			http.StatusUnprocessableEntity,
			"gate.invalid",
		},
		{
			"gate the engine refuses",
			nil,
			http.MethodPut,
			"/gates/x",
			strings.Replace(freezeGateDoc, "kind", "nothing", 1),
			"",
			http.StatusUnprocessableEntity,
			"gate.invalid",
		},
		{
			"permit limit",
			nil,
			http.MethodPut,
			"/access-limits/x",
			`{"policy":"permit (principal, action, resource);"}`,
			"",
			http.StatusUnprocessableEntity,
			"gate.invalid",
		},
		{
			"limit that does not parse",
			nil,
			http.MethodPut,
			"/access-limits/x",
			`{"policy":"forbid ("}`,
			"",
			http.StatusUnprocessableEntity,
			"gate.invalid",
		},
		{
			"membership test of a value that is not a group",
			nil,
			http.MethodPut,
			"/access-limits/x",
			`{"policy":"forbid (principal, action, resource) unless { principal in resource.owners };"}`,
			"",
			http.StatusBadRequest,
			"gate.limit-membership",
		},
		{
			"membership test of a set that holds a principal",
			nil,
			http.MethodPut,
			"/access-limits/x",
			`{"policy":"forbid (principal, action, resource) unless { principal in [` +
				`OpenDustpan::Group::\"sre\", OpenDustpan::Principal::\"olga\"] };"}`,
			"",
			http.StatusBadRequest,
			"gate.limit-membership",
		},
		{
			"limit names a group that the caller cannot manage",
			nil,
			http.MethodPut,
			"/access-limits/x",
			`{"policy":"forbid (principal in OpenDustpan::Group::\"ops\", action, resource);"}`,
			"",
			http.StatusForbidden,
			"gate.limit-group-reach",
		},
		{
			"bad id",
			nil,
			http.MethodPut,
			"/gates/BAD",
			freezeGateDoc,
			"",
			http.StatusUnprocessableEntity,
			"gate.invalid",
		},
		{
			"condition over the limit",
			nil,
			http.MethodPut,
			"/gates/x",
			tooLong,
			"",
			http.StatusUnprocessableEntity,
			"gate.limit",
		},
		{
			"stale gate",
			[]string{"/gates/x"},
			http.MethodPut,
			"/gates/x",
			freezeGateDoc,
			`"9"`,
			http.StatusConflict,
			"gate.version-conflict",
		},
		{
			"stale limit",
			[]string{"/access-limits/x"},
			http.MethodDelete,
			"/access-limits/x",
			"",
			`"9"`,
			http.StatusConflict,
			"gate.version-conflict",
		},
		{
			"weaker final gate",
			[]string{"/gates/x:final"},
			http.MethodPut,
			"/gates/x",
			weaker,
			"",
			http.StatusConflict,
			"gate.final",
		},
		{
			"delete of a final gate",
			[]string{"/gates/x:final"},
			http.MethodDelete,
			"/gates/x",
			"",
			"",
			http.StatusConflict,
			"gate.final",
		},
		{
			"body that is not valid",
			nil,
			http.MethodPut,
			"/gates/x",
			"{",
			"",
			http.StatusBadRequest,
			"request.body-invalid",
		},
		{
			"If-Match that is not a version",
			nil,
			http.MethodPut,
			"/gates/x",
			freezeGateDoc,
			"abc",
			http.StatusBadRequest,
			"request.if-match-invalid",
		},
		{
			"collection name with a slash",
			nil,
			http.MethodGet,
			"/gates/x",
			"",
			"",
			http.StatusBadRequest,
			"request.invalid-object",
		},
	}

	for level, base := range gateScopeURLs() {
		for _, tt := range tests {
			t.Run(level+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				runGateCodeCase(t, base, tt)
			})
		}
	}
}

// runGateCodeCase makes the objects of tt.setup, sends the request and checks
// the status and the code of the answer.
func runGateCodeCase(t *testing.T, base string, tt gateCodeCase) {
	t.Helper()

	final := strings.Replace(freezeGateDoc, `"mode":"enforce"`, `"mode":"enforce","final":true`, 1)
	target := base

	if tt.name == "collection name with a slash" {
		target = gateBase + "acme/collections/bad%2Fname"
	}

	_, h := newGateHandler(t, adminGates())

	for _, s := range tt.setup {
		path, doc, _ := strings.Cut(s, ":")
		body := freezeGateDoc

		if strings.Contains(path, "access-limits") {
			body = noWritesDoc
		}

		if doc == "final" {
			body = final
		}

		wantStatus(t, serveGate(t, h, gateCall{http.MethodPut, base + path, body, ""}), http.StatusOK)
	}

	rec := serveGate(t, h, gateCall{tt.method, target + tt.suffix, tt.body, tt.ifm})
	wantStatus(t, rec, tt.status)

	if !strings.Contains(rec.Body.String(), `"errorCode":"`+tt.code+`"`) {
		t.Errorf("body = %s, want the code %s", rec.Body.String(), tt.code)
	}
}

// failingGroupReachGates allows the gate actions on acme and fails the check
// of the manage-groups action with an error that is not a denial.
type failingGroupReachGates struct {
	gateAuthorizer

	err error
}

func (a failingGroupReachGates) Authorize(
	ctx context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	if action == identity.ActionServerManageGroups {
		return a.err
	}

	return a.gateAuthorizer.Authorize(ctx, p, action, obj)
}

// groupManagerGates allows both gate actions on acme and the management of
// the groups of the installation.
func groupManagerGates() gateAuthorizer {
	a := adminGates()
	a.allow[identity.ActionServerManageGroups] = ""

	return a
}

// accessLimitGroupCase is one request of TestAccessLimitNamesOnlyManagedGroups.
type accessLimitGroupCase struct {
	name   string
	authz  identity.Authorizer
	policy string
	status int
	stored bool
	denied bool
}

// bootstrapAdminGates allows the gate actions on acme and denies the
// management of the groups of the installation. The save of its case runs as
// the bootstrap administrator, so only the administrator flag allows it.
type bootstrapAdminGates struct {
	gateAuthorizer
}

// caller returns the principal of the save of its case.
func (bootstrapAdminGates) caller() identity.Principal { return bootstrapAdmin }

// caseCaller is a case whose save runs as the caller that caller returns, not
// as the console session of its handler.
type caseCaller interface {
	caller() identity.Principal
}

// TestAccessLimitNamesOnlyManagedGroups proves that a save refuses an access
// limit that names a group when its author may not manage the groups of the
// installation, in each position of the group, and that it stores the limit
// for an author who may manage groups or is the bootstrap administrator.
func TestAccessLimitNamesOnlyManagedGroups(t *testing.T) {
	t.Parallel()

	const reason = "access limit names a group that the author cannot manage"

	groupInPrincipal := `{"policy":"forbid (principal in OpenDustpan::Group::\"ops\", action, resource);"}`
	groupInWhen := `{"policy":"forbid (principal, action, resource) when { principal in ` +
		`OpenDustpan::Group::\"ops\" };"}`
	groupInUnless := `{"policy":"forbid (principal, action, resource) unless { principal in ` +
		`OpenDustpan::Group::\"ops\" };"}`

	tests := []accessLimitGroupCase{
		{"group in the principal scope", adminGates(), groupInPrincipal, http.StatusForbidden, false, true},
		{"group in a when clause", adminGates(), groupInWhen, http.StatusForbidden, false, true},
		{"group in an unless clause", adminGates(), groupInUnless, http.StatusForbidden, false, true},
		{"no group", adminGates(), noWritesDoc, http.StatusOK, true, false},
		{"the caller manages groups", groupManagerGates(), groupInPrincipal, http.StatusOK, true, false},
		{
			"the bootstrap administrator",
			bootstrapAdminGates{gateAuthorizer: adminGates()},
			groupInPrincipal, http.StatusOK, true, false,
		},
		{
			"the group decision fails",
			failingGroupReachGates{gateAuthorizer: adminGates(), err: errPrincipalsDown},
			groupInPrincipal, http.StatusForbidden, false, true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			runAccessLimitGroupCase(t, tt, reason)
		})
	}
}

// runAccessLimitGroupCase sends one save of an access limit and checks its
// answer, its audit entry and the stored limit.
func runAccessLimitGroupCase(t *testing.T, tt accessLimitGroupCase, reason string) {
	t.Helper()

	st, h := newGateHandler(t, tt.authz)
	item := gateBase + "acme/access-limits/limit"

	p := noBootstrapAdmin
	if c, ok := tt.authz.(caseCaller); ok {
		p = c.caller()
	}

	rec := saveAccessLimit(t, h, item, tt.policy, p)
	wantStatus(t, rec, tt.status)

	if tt.status == http.StatusForbidden {
		checkLimitGroupReachRefusal(t, rec)
	}

	if tt.denied {
		checkLimitGroupReachDenial(t, st, reason)
	}

	read := serveGate(t, h, gateCall{http.MethodGet, item, "", ""})
	if tt.stored {
		wantStatus(t, read, http.StatusOK)
	} else {
		wantStatus(t, read, http.StatusNotFound)
	}
}

// saveAccessLimit sends the save of an access limit as p. A principal that is
// not an administrator sends it as the console session of g.
func saveAccessLimit(
	t *testing.T, g gateEnv, item, policy string, p identity.Principal,
) *httptest.ResponseRecorder {
	t.Helper()

	if !p.Admin {
		return serveGate(t, g, gateCall{http.MethodPut, item, policy, ""})
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, item, strings.NewReader(policy))
	req.SetPathValue("namespace", "acme")
	req.SetPathValue("id", "limit")
	req.RemoteAddr = ipTestRemote

	rec := httptest.NewRecorder()
	apiserver.ServeAccessLimitSaveForTest(
		g.env.handler.Handler, rec, req, p, identity.NamespaceObject("acme"),
	)

	return rec
}

// checkLimitGroupReachRefusal fails unless rec answers the code of a group
// that the author cannot manage, and names no group.
func checkLimitGroupReachRefusal(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()

	body := rec.Body.String()

	if !strings.Contains(body, `"errorCode":"`+errcode.GateLimitGroupReach+`"`) {
		t.Errorf("body = %s, want the code %s", body, errcode.GateLimitGroupReach)
	}

	if strings.Contains(body, "ops") {
		t.Errorf("refusal body names the group: %s", body)
	}
}

// checkLimitGroupReachDenial fails unless acme holds one denial entry of the
// gate management action with the reason of a group that its author cannot
// manage.
func checkLimitGroupReachDenial(t *testing.T, st *sqlite.Store, reason string) {
	t.Helper()

	var denied []audit.Entry

	entries := listNamespaceEntries(t, st, "acme")
	for i := range entries {
		if entries[i].Outcome == audit.OutcomeDenied &&
			entries[i].Action == string(identity.ActionNamespaceManageGates) {
			denied = append(denied, entries[i])
		}
	}

	if len(denied) != 1 {
		t.Fatalf("denied entries = %+v, want one", denied)
	}

	if !strings.HasPrefix(denied[0].Reason, reason) {
		t.Errorf("reason = %q, want it to start with %q", denied[0].Reason, reason)
	}
}

// TestGateRoutesUnavailableWithoutEngine proves that a server without a policy
// engine stores no gate and no access limit, and answers 503 with the code
// gate.unavailable.
func TestGateRoutesUnavailableWithoutEngine(t *testing.T) {
	t.Parallel()

	for level, base := range gateScopeURLs() {
		for _, res := range []struct{ segment, doc string }{{"gates", freezeGateDoc}, {"access-limits", noWritesDoc}} {
			t.Run(level+"/"+res.segment, func(t *testing.T) {
				t.Parallel()

				g := newGateEnvWith(t, adminGates(), true)
				item := base + "/" + res.segment + "/x"

				rec := serveGate(t, g, gateCall{http.MethodPut, item, res.doc, ""})
				wantStatus(t, rec, http.StatusServiceUnavailable)

				if !strings.Contains(rec.Body.String(), `"errorCode":"gate.unavailable"`) {
					t.Errorf("body = %s, want the code gate.unavailable", rec.Body.String())
				}

				wantStatus(t, serveGate(t, g, gateCall{http.MethodGet, item, "", ""}), http.StatusNotFound)
			})
		}
	}
}

// TestGateRoutesFollowTheUnit proves that the gates of a unit belong to the
// unit, not to its name: a write for a name that no live unit has answers 404
// with unit.not-found, and a new unit with the name of a deleted unit starts
// with no gate.
func TestGateRoutesFollowTheUnit(t *testing.T) {
	t.Parallel()

	st, h := newGateHandler(t, adminGates())
	net := gateBase + "acme/units/by-name/opentofu/net"

	wantStatus(t, serveGate(t, h, gateCall{http.MethodPut, net + "/gates/freeze", freezeGateDoc, ""}), http.StatusOK)

	ns, err := st.GetNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	del := inventory.Deletion{Entry: gateTestEntry("workspace:delete"), PurgeAfter: time.Now().Add(time.Hour)}
	ref := workspace.Ref{Namespace: "acme", Name: "net"}

	if err = st.DeleteWorkspace(t.Context(), ref, ns.Fence(), del); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}

	tests := []struct {
		name   string
		call   gateCall
		status int
		code   string
	}{
		{
			"a put for a unit that never existed",
			gateCall{
				http.MethodPut,
				gateBase + "acme/units/by-name/opentofu/ghost/gates/freeze", freezeGateDoc, "",
			},
			http.StatusNotFound, errcode.UnitNotFound,
		},
		{
			"a put for a deleted unit",
			gateCall{http.MethodPut, net + "/gates/freeze", freezeGateDoc, ""},
			http.StatusNotFound, errcode.UnitNotFound,
		},
		{
			"a put of an access limit for a deleted unit",
			gateCall{http.MethodPut, net + "/access-limits/no-writes", noWritesDoc, ""},
			http.StatusNotFound, errcode.UnitNotFound,
		},
		{
			"a read of the gate of a deleted unit",
			gateCall{http.MethodGet, net + "/gates/freeze", "", ""},
			http.StatusNotFound, errcode.GateNotFound,
		},
	}

	for _, tt := range tests {
		rec := serveGate(t, h, tt.call)
		wantStatus(t, rec, tt.status)

		if !strings.Contains(rec.Body.String(), `"errorCode":"`+tt.code+`"`) {
			t.Fatalf("%s: body = %s, want the code %s", tt.name, rec.Body.String(), tt.code)
		}
	}

	createGateWorkspace(t, st, "ws-gate-net-again", "net")

	rec := serveGate(t, h, gateCall{http.MethodGet, net + "/gates", "", ""})
	wantStatus(t, rec, http.StatusOK)

	var list gateObjectList

	decodeGateJSON(t, rec, &list)

	if len(list.Gates) != 0 {
		t.Fatalf(
			"gates of the new workspace net = %+v, want none: a new unit with a used name starts clean",
			list.Gates,
		)
	}

	rec = serveGate(t, h, gateCall{http.MethodPut, net + "/gates/freeze", freezeGateDoc, ""})
	wantStatus(t, rec, http.StatusOK)

	var created gateObjectDoc

	decodeGateJSON(t, rec, &created)

	if created.Version != 1 {
		t.Fatalf("gate of the new workspace net = version %d, want 1", created.Version)
	}
}

// installationBase is the path of the gate routes of the installation.
const installationBase = "/api/dustpan/v1"

// swapGates is an authorizer whose rules a test changes while it runs. A test
// seeds the rows with a permissive rule, and then asks as a caller with less.
type swapGates struct {
	cur *atomic.Pointer[identity.Authorizer]
}

func newSwapGates(a identity.Authorizer) swapGates {
	p := new(atomic.Pointer[identity.Authorizer])
	p.Store(&a)

	return swapGates{cur: p}
}

func (s swapGates) Authorize(ctx context.Context, p identity.Principal, a identity.Action, o identity.Object) error {
	return (*s.cur.Load()).Authorize(ctx, p, a, o)
}

func (s swapGates) ListObjects(ctx context.Context, p identity.Principal, a identity.Action) (identity.Scope, error) {
	return (*s.cur.Load()).ListObjects(ctx, p, a)
}

func (s swapGates) set(a identity.Authorizer) { s.cur.Store(&a) }

// failingGates is an authorizer whose grant lookup fails.
type failingGates struct{}

func (failingGates) Authorize(context.Context, identity.Principal, identity.Action, identity.Object) error {
	return fmt.Errorf("the grant store is down: %w", io.ErrClosedPipe)
}

func (failingGates) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{}, fmt.Errorf("the grant store is down: %w", io.ErrClosedPipe)
}

// installationOnly allows the actions on the installation object only.
func installationOnly(actions ...identity.Action) gateAuthorizer {
	allow := map[identity.Action]namespace.Name{}
	for _, a := range actions {
		allow[a] = ""
	}

	return gateAuthorizer{allow: allow}
}

// installationAdmin allows both installation actions on the installation.
func installationAdmin() gateAuthorizer {
	return installationOnly(identity.ActionInstallationManageGates, identity.ActionInstallationReadGates)
}

// roleOnInstallation allows the actions of the built-in role on the
// installation object, as a grant of the role on the installation does.
func roleOnInstallation(t *testing.T, role string) gateAuthorizer {
	t.Helper()

	r, ok := access.BuiltInRole(role)
	if !ok {
		t.Fatalf("no built-in role %q", role)
	}

	return installationOnly(r.Actions...)
}

// installationKind is one kind of object of the installation, with one valid
// body.
type installationKind struct{ segment, doc string }

// installationKinds returns the three kinds that the installation holds.
func installationKinds() []installationKind {
	return []installationKind{
		{"gates", freezeGateDoc},
		{"access-limits", noWritesDoc},
		{"approval-rules", `{"count":1,"approvers":{"groups":["ops"]}}`},
	}
}

// itemPath returns the path of the object id of the kind at the installation.
func (k installationKind) itemPath(id string) string {
	return installationBase + "/" + k.segment + "/" + id
}

// listPath returns the path of the list of the kind at the installation.
func (k installationKind) listPath() string { return installationBase + "/" + k.segment }

// TestGateRoutesInstallationReads proves the list and the get of the gates,
// the access limits and the approval rules of the installation: the list
// shows the objects of the installation only, and only a holder of
// installation:read-gates reads.
func TestGateRoutesInstallationReads(t *testing.T) {
	t.Parallel()

	swap := newSwapGates(installationAdmin())
	g := newGateEnv(t, swap)

	// Seed one object of each kind at the installation and at acme.
	for _, k := range installationKinds() {
		wantStatus(t, serveGate(t, g, gateCall{http.MethodPut, k.itemPath("shared"), k.doc, ""}), http.StatusOK)
	}

	swap.set(adminGates())

	for _, k := range installationKinds() {
		wantStatus(
			t,
			serveGate(t, g, gateCall{http.MethodPut, gateBase + "acme/" + k.segment + "/local", k.doc, ""}),
			http.StatusOK,
		)
	}

	swap.set(installationOnly(identity.ActionInstallationReadGates))
	checkInstallationReadsAllowed(t, g)

	denied := map[string]gateAuthorizer{
		"namespace:read-gates only": {
			allow: map[identity.Action]namespace.Name{identity.ActionNamespaceReadGates: "acme"},
		},
		"namespace administrator":        adminGates(),
		"installation:manage-gates only": installationOnly(identity.ActionInstallationManageGates),
		"no action":                      {allow: nil},
	}

	for name, authz := range denied {
		swap.set(authz)

		for _, k := range installationKinds() {
			for _, target := range []string{k.listPath(), k.itemPath("shared")} {
				rec := serveGate(t, g, gateCall{http.MethodGet, target, "", ""})
				if rec.Code != http.StatusForbidden ||
					!strings.Contains(rec.Body.String(), `"errorCode":"access.denied"`) {
					t.Fatalf("%s: GET %s = %d %s, want 403 access.denied", name, target, rec.Code, rec.Body.String())
				}
			}
		}
	}
}

// checkInstallationReadsAllowed reads each kind as a holder of the installation
// action: the list shows the installation object and no namespace object, an
// object of a namespace is not found, and an unknown ID is not found.
func checkInstallationReadsAllowed(t *testing.T, h http.Handler) {
	t.Helper()

	for _, k := range installationKinds() {
		rec := serveGate(t, h, gateCall{http.MethodGet, k.listPath(), "", ""})
		wantStatus(t, rec, http.StatusOK)

		body := rec.Body.String()
		if !strings.Contains(body, `"id":"shared"`) || strings.Contains(body, `"local"`) {
			t.Errorf("list of %s = %s, want the installation object shared and no namespace object", k.segment, body)
		}

		rec = serveGate(t, h, gateCall{http.MethodGet, k.itemPath("shared"), "", ""})
		wantStatus(t, rec, http.StatusOK)

		if rec.Header().Get("ETag") != `"1"` {
			t.Errorf("ETag of %s = %q, want \"1\"", k.segment, rec.Header().Get("ETag"))
		}

		for _, id := range []string{"local", "unknown"} {
			rec = serveGate(t, h, gateCall{http.MethodGet, k.itemPath(id), "", ""})
			wantStatus(t, rec, http.StatusNotFound)
			wantCode(t, rec, errcode.GateNotFound)
		}
	}
}

// TestApprovalRulesFollowTheUnit proves, through the routes, that an approval
// rule belongs to the unit and not to its name: a rename keeps the rule, a new
// unit with the name of a deleted unit has none, and an unknown unit answers
// 404 with the code unit.not-found.
func TestApprovalRulesFollowTheUnit(t *testing.T) {
	t.Parallel()

	st, h := newGateHandler(t, adminGates())

	renameKeepsTheRule(t, st, h)
	reusedNameStartsClean(t, st, h)
	unknownUnitIsNotFound(t, h)
}

// unitRuleBody is a rule that the group ops can meet.
const unitRuleBody = `{"count":1,"approvers":{"groups":["ops"]}}`

// renameKeepsTheRule saves a rule for the stack web/prod, renames the stack,
// and reads the rule through the new name.
func renameKeepsTheRule(t *testing.T, st *sqlite.Store, h http.Handler) {
	t.Helper()

	old := gateBase + "acme/units/by-name/pulumi/web%2Fprod"
	renamed := gateBase + "acme/units/by-name/pulumi/web%2Fstage"

	wantStatus(
		t,
		serveGate(t, h, gateCall{http.MethodPut, old + "/approval-rules/review", unitRuleBody, ""}),
		http.StatusOK,
	)

	ns, err := st.GetNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse stack reference: %v", err)
	}

	if err = st.RenameStack(t.Context(), ref, "stage", ns.Fence(), gateTestEntry("stack:rename")); err != nil {
		t.Fatalf("rename stack: %v", err)
	}

	wantStatus(t, serveGate(t, h, gateCall{http.MethodGet, renamed + "/approval-rules/review", "", ""}), http.StatusOK)
	wantStatus(
		t,
		serveGate(t, h, gateCall{http.MethodGet, old + "/approval-rules/review", "", ""}),
		http.StatusNotFound,
	)
}

// reusedNameStartsClean saves a rule for the workspace net, deletes the
// workspace, and makes a new workspace with the same name. The new workspace
// has no rule.
func reusedNameStartsClean(t *testing.T, st *sqlite.Store, h http.Handler) {
	t.Helper()

	net := gateBase + "acme/units/by-name/opentofu/net"

	wantStatus(
		t,
		serveGate(t, h, gateCall{http.MethodPut, net + "/approval-rules/review", unitRuleBody, ""}),
		http.StatusOK,
	)

	ns, err := st.GetNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	del := inventory.Deletion{Entry: gateTestEntry("workspace:delete"), PurgeAfter: time.Now().Add(time.Hour)}

	err = st.DeleteWorkspace(t.Context(), workspace.Ref{Namespace: "acme", Name: "net"}, ns.Fence(), del)
	if err != nil {
		t.Fatalf("delete workspace: %v", err)
	}

	rec := serveGate(t, h, gateCall{http.MethodPut, net + "/approval-rules/review", unitRuleBody, ""})
	wantStatus(t, rec, http.StatusNotFound)
	wantCode(t, rec, errcode.UnitNotFound)

	createGateWorkspace(t, st, "ws-gate-net-again", "net")

	rec = serveGate(t, h, gateCall{http.MethodGet, net + "/approval-rules", "", ""})
	wantStatus(t, rec, http.StatusOK)

	if strings.TrimSpace(rec.Body.String()) != `{"rules":[]}` {
		t.Fatalf("rules of the new workspace net = %s, want none", rec.Body.String())
	}

	rec = serveGate(t, h, gateCall{http.MethodPut, net + "/approval-rules/review", unitRuleBody, ""})
	wantStatus(t, rec, http.StatusOK)

	var created ruleDoc

	decodeGateJSON(t, rec, &created)

	if created.Version != 1 {
		t.Fatalf("rule of the new workspace net = version %d, want 1", created.Version)
	}
}

// unknownUnitIsNotFound saves a rule and a default for a unit that does not
// exist.
func unknownUnitIsNotFound(t *testing.T, h http.Handler) {
	t.Helper()

	ghost := gateBase + "acme/units/by-name/opentofu/ghost"

	for suffix, payload := range map[string]string{
		"/approval-rules/review": unitRuleBody, "/approval-default": platformDefault,
	} {
		rec := serveGate(t, h, gateCall{http.MethodPut, ghost + suffix, payload, ""})
		wantStatus(t, rec, http.StatusNotFound)
		wantCode(t, rec, errcode.UnitNotFound)
	}
}

// installationEntries returns the audit entries of a change that succeeded at
// the installation, whose action starts with prefix. A refused request leaves
// an entry of the outcome denied or failure, which is not a change.
func installationEntries(t *testing.T, st *sqlite.Store, prefix string) []audit.Entry {
	t.Helper()

	return entriesOf(t, st, audit.ServerScope, func(e *audit.Entry) bool {
		return strings.HasPrefix(e.Action, prefix) && e.Outcome == audit.OutcomeSuccess
	})
}

// TestGateRoutesInstallationWrites proves one guard at a time of the PUT and
// the DELETE of a gate, an access limit and an approval rule of the
// installation. Each hostile request leaves the stored row and the audit log
// as they were.
func TestGateRoutesInstallationWrites(t *testing.T) {
	t.Parallel()

	guards := map[string]func(t *testing.T){
		"authorization":         installationWriteAuthorization,
		"access token":          installationWriteAccessToken,
		"stale sign-in":         installationWriteStaleSession,
		"version":               installationWriteVersion,
		"body":                  installationWriteBody,
		"id":                    installationWriteID,
		"failing grant lookup":  installationWriteFailingLookup,
		"audit entry":           installationWriteAudit,
		"limit":                 installationWriteLimit,
		"no approval default":   installationNoApprovalDefault,
		"ids reach no store":    installationIDsReachNoStore,
		"scope of the path":     installationScopeOfThePath,
		"entries of the denial": installationDenialIsAudited,
	}

	for name, guard := range guards {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			guard(t)
		})
	}
}

// installationDenialIsAudited checks that a refused write leaves an entry of
// the outcome denied and no entry of a change.
func installationDenialIsAudited(t *testing.T) {
	t.Helper()

	swap := newSwapGates(installationAdmin())
	g := newGateEnv(t, swap)

	swap.set(installationOnly(identity.ActionInstallationReadGates))
	wantStatus(
		t,
		serveGate(t, g, gateCall{http.MethodPut, installationKinds()[0].itemPath("probe"), freezeGateDoc, ""}),
		http.StatusForbidden,
	)

	denials := entriesOf(t, g.env.store, audit.ServerScope, func(e *audit.Entry) bool {
		return e.Action == string(identity.ActionInstallationManageGates) && e.Outcome == audit.OutcomeDenied
	})
	if len(denials) != 1 {
		t.Fatalf("denial entries = %d, want 1", len(denials))
	}

	if got := installationEntries(t, g.env.store, "installation:"); len(got) != 0 {
		t.Fatalf("a refused request wrote the entries %+v", got)
	}
}

// installationWriteAuthorization sends each write as a caller that lacks
// installation:manage-gates, and as the two roles that hold it.
func installationWriteAuthorization(t *testing.T) {
	t.Helper()

	manage, read := identity.ActionInstallationManageGates, identity.ActionInstallationReadGates
	swap := newSwapGates(installationOnly(manage, read))
	g := newGateEnv(t, swap)

	denied := map[string]gateAuthorizer{
		"namespace:manage-gates only": {allow: map[identity.Action]namespace.Name{
			identity.ActionNamespaceManageGates: "acme", identity.ActionNamespaceReadGates: "acme",
		}},
		"installation:read-gates only": installationOnly(read),
		"account administrator":        roleOnInstallation(t, access.RoleAccountAdministrator),
		"namespace administrator":      roleOnInstallation(t, access.RoleAdmin),
		"viewer":                       roleOnInstallation(t, access.RoleViewer),
		"no action":                    {allow: nil},
		"manage-gates on a namespace":  {allow: map[identity.Action]namespace.Name{manage: "acme", read: "acme"}},
	}

	for name, authz := range denied {
		swap.set(authz)

		for _, k := range installationKinds() {
			for _, c := range []gateCall{
				{http.MethodPut, k.itemPath("probe"), k.doc, ""}, {http.MethodDelete, k.itemPath("probe"), "", ""},
			} {
				rec := serveGate(t, g, c)
				if rec.Code != http.StatusForbidden ||
					!strings.Contains(rec.Body.String(), `"errorCode":"access.denied"`) {
					t.Fatalf(
						"%s: %s %s = %d %s, want 403 access.denied",
						name,
						c.method,
						c.target,
						rec.Code,
						rec.Body.String(),
					)
				}
			}
		}
	}

	swap.set(installationOnly(manage, read))

	for _, k := range installationKinds() {
		wantStatus(t, serveGate(t, g, gateCall{http.MethodGet, k.itemPath("probe"), "", ""}), http.StatusNotFound)
	}

	if got := installationEntries(t, g.env.store, "installation:"); len(got) != 0 {
		t.Fatalf("refused requests wrote the entries %+v", got)
	}

	checkOperatorRolesWrite(t, swap, g)
}

// checkOperatorRolesWrite sends a write as each role that holds the action.
func checkOperatorRolesWrite(t *testing.T, swap swapGates, h http.Handler) {
	t.Helper()

	for _, role := range []string{access.RoleInstallationOperator, access.RoleSiteAdministrator} {
		swap.set(roleOnInstallation(t, role))

		for _, k := range installationKinds() {
			id := strings.ReplaceAll(role, "-", "")
			wantStatus(t, serveGate(t, h, gateCall{http.MethodPut, k.itemPath(id), k.doc, ""}), http.StatusOK)
		}
	}
}

// installationWriteAccessToken sends each write with an access token of a
// caller that holds the action. A token never gives a fresh authentication.
func installationWriteAccessToken(t *testing.T) {
	t.Helper()

	g := newGateEnv(t, installationAdmin())

	for _, k := range installationKinds() {
		seed := k.itemPath("seed")
		wantStatus(t, serveGate(t, g, gateCall{http.MethodPut, seed, k.doc, ""}), http.StatusOK)

		for _, c := range []gateCall{
			{http.MethodPut, seed, k.doc, ""},
			{http.MethodPut, k.itemPath("new"), k.doc, ""},
			{http.MethodDelete, seed, "", ""},
		} {
			rec := serveGate(t, g.env.handler, c)
			wantStatus(t, rec, http.StatusForbidden)
			wantCode(t, rec, errcode.MFAFreshStepRequired)
		}

		rec := serveGate(t, g.env.handler, gateCall{http.MethodGet, seed, "", ""})
		wantStatus(t, rec, http.StatusOK)

		if rec.Header().Get("ETag") != `"1"` {
			t.Fatalf("%s after the refused writes has the ETag %q, want \"1\"", k.segment, rec.Header().Get("ETag"))
		}

		wantStatus(
			t,
			serveGate(t, g.env.handler, gateCall{http.MethodGet, k.itemPath("new"), "", ""}),
			http.StatusNotFound,
		)
	}

	if got := installationEntries(t, g.env.store, "installation:set-"); len(got) != len(installationKinds()) {
		t.Fatalf("set entries = %d, want %d (the seeds only)", len(got), len(installationKinds()))
	}
}

// installationWriteStaleSession sends each write from a session whose sign-in
// is older than the freshness window.
func installationWriteStaleSession(t *testing.T) {
	t.Helper()

	e := newSignInEnvWith(t, signInEnvOptions{
		autoCreate: true, noPolicyRoutes: false, noGateEngine: false, stored: nil, authz: nil,
		namespaces: []namespace.Name{"acme"}, hostile: nil, passwords: nil, failPolicyRead: nil,
		idempotency: false, failTempAdminRead: false,
	})
	stale := e.staleSession(t)

	for _, k := range installationKinds() {
		item := k.itemPath("probe")

		for _, c := range []gateCall{{http.MethodPut, item, k.doc, ""}, {http.MethodDelete, item, "", ""}} {
			rec := e.sendWithSession(t, c.method, stale, c.target, c.body)
			if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "reauthenticate") {
				t.Fatalf("%s %s from a stale session = %d %s, want 403 with reauthenticate",
					c.method, c.target, rec.Code, rec.Body.String())
			}
		}

		if rec := e.sendWithSession(t, http.MethodGet, stale, item, ""); rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s after the refused writes = %d, want 404", item, rec.Code)
		}
	}

	fresh := e.freshSignIn(t)

	rec := e.sendWithSession(t, http.MethodPut, fresh, installationKinds()[0].itemPath("probe"), freezeGateDoc)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT from a fresh session = %d %s, want 200", rec.Code, rec.Body.String())
	}
}

// installationWriteVersion sends a stale If-Match for each kind.
func installationWriteVersion(t *testing.T) {
	t.Helper()

	g := newGateEnv(t, installationAdmin())

	for _, k := range installationKinds() {
		item := k.itemPath("seed")
		wantStatus(t, serveGate(t, g, gateCall{http.MethodPut, item, k.doc, ""}), http.StatusOK)

		for _, c := range []gateCall{{http.MethodPut, item, k.doc, `"7"`}, {http.MethodDelete, item, "", `"7"`}} {
			rec := serveGate(t, g, c)
			wantStatus(t, rec, http.StatusConflict)
			wantCode(t, rec, errcode.GateVersionConflict)
		}

		rec := serveGate(t, g, gateCall{http.MethodGet, item, "", ""})
		wantStatus(t, rec, http.StatusOK)

		if rec.Header().Get("ETag") != `"1"` {
			t.Fatalf("ETag of %s after the refused writes = %q, want \"1\"", k.segment, rec.Header().Get("ETag"))
		}

		wantStatus(t, serveGate(t, g, gateCall{http.MethodPut, item, k.doc, `"1"`}), http.StatusOK)
		wantStatus(t, serveGate(t, g, gateCall{http.MethodDelete, item, "", `"2"`}), http.StatusNoContent)
	}
}

// installationWriteBody sends a body with the field scope and a body one byte
// above the cap.
func installationWriteBody(t *testing.T) {
	t.Helper()

	g := newGateEnv(t, installationAdmin())

	const capBytes = 64 * 1024

	pad := func(prefix, suffix string) string {
		return prefix + strings.Repeat("a", capBytes+1-len(prefix)-len(suffix)) + suffix
	}

	hostile := map[string]string{
		"gates":          pad(`{"decisionPoint":"state.write","outcome":"deny","message":"`, `"}`),
		"access-limits":  pad(`{"policy":"`, `"}`),
		"approval-rules": pad(`{"count":1,"approvers":{"groups":["`, `"]}}`),
	}

	for _, k := range installationKinds() {
		item := k.itemPath("probe")
		withScope := strings.Replace(k.doc, "{", `{"scope":"installation",`, 1)

		wantStatus(t, serveGate(t, g, gateCall{http.MethodPut, item, withScope, ""}), http.StatusBadRequest)

		if len(hostile[k.segment]) != capBytes+1 {
			t.Fatalf("hostile body of %s has %d bytes, want %d", k.segment, len(hostile[k.segment]), capBytes+1)
		}

		wantStatus(t, serveGate(t, g, gateCall{http.MethodPut, item, hostile[k.segment], ""}), http.StatusBadRequest)
		wantStatus(t, serveGate(t, g, gateCall{http.MethodGet, item, "", ""}), http.StatusNotFound)
	}
}

// installationWriteID sends an ID with a capital letter, a slash and a
// control character. Each answers 400 or 422, and nothing is stored.
func installationWriteID(t *testing.T) {
	t.Helper()

	g := newGateEnv(t, installationAdmin())

	for _, k := range installationKinds() {
		for _, id := range []string{"Freeze", "a%2Fb", "a%07b", "a%0Ab", "a%27%3B%20DROP%20TABLE%20x%3B--"} {
			for _, c := range []gateCall{
				{http.MethodPut, k.itemPath(id), k.doc, ""}, {http.MethodDelete, k.itemPath(id), "", ""},
			} {
				rec := serveGate(t, g, c)
				if rec.Code != http.StatusBadRequest && rec.Code != http.StatusUnprocessableEntity {
					t.Fatalf("%s %s = %d %s, want 400 or 422", c.method, c.target, rec.Code, rec.Body.String())
				}
			}
		}

		rec := serveGate(t, g, gateCall{http.MethodGet, k.listPath(), "", ""})
		wantStatus(t, rec, http.StatusOK)

		if strings.Contains(rec.Body.String(), `"id"`) {
			t.Fatalf("list of %s after the refused writes = %s, want none", k.segment, rec.Body.String())
		}
	}

	if got := installationEntries(t, g.env.store, "installation:"); len(got) != 0 {
		t.Fatalf("refused requests wrote the entries %+v", got)
	}
}

// installationWriteFailingLookup makes the grant lookup fail. Each request
// answers an error status, and nothing is stored.
func installationWriteFailingLookup(t *testing.T) {
	t.Helper()

	swap := newSwapGates(installationAdmin())
	g := newGateEnv(t, swap)

	swap.set(failingGates{})

	for _, k := range installationKinds() {
		for _, c := range []gateCall{
			{http.MethodPut, k.itemPath("probe"), k.doc, ""}, {http.MethodDelete, k.itemPath("probe"), "", ""},
		} {
			rec := serveGate(t, g, c)
			if rec.Code < http.StatusInternalServerError {
				t.Fatalf("%s %s with a failing grant lookup = %d %s, want a 5xx status",
					c.method, c.target, rec.Code, rec.Body.String())
			}
		}
	}

	swap.set(installationOnly(identity.ActionInstallationReadGates))

	for _, k := range installationKinds() {
		wantStatus(t, serveGate(t, g, gateCall{http.MethodGet, k.itemPath("probe"), "", ""}), http.StatusNotFound)
	}

	if got := installationEntries(t, g.env.store, "installation:"); len(got) != 0 {
		t.Fatalf("requests with a failing grant lookup wrote the entries %+v", got)
	}
}

// installationWriteAudit writes and deletes one object of each kind and checks
// each entry: the caller, the action, the scope @server and the object.
func installationWriteAudit(t *testing.T) {
	t.Helper()

	g := newGateEnv(t, installationAdmin())

	for _, k := range installationKinds() {
		wantStatus(t, serveGate(t, g, gateCall{http.MethodPut, k.itemPath("audited"), k.doc, ""}), http.StatusOK)
		wantStatus(t, serveGate(t, g, gateCall{http.MethodDelete, k.itemPath("audited"), "", ""}), http.StatusNoContent)
	}

	entries := installationEntries(t, g.env.store, "installation:")
	if len(entries) != 2*len(installationKinds()) {
		t.Fatalf("entries = %d, want %d: %+v", len(entries), 2*len(installationKinds()), entries)
	}

	seen := map[string]bool{}

	for i := range entries {
		e := &entries[i]
		seen[e.Action] = true

		if e.Namespace != audit.ServerScope || e.Subject == "" || e.Kind != audit.KindChange ||
			!strings.HasPrefix(e.Object, "installation/") || !strings.HasSuffix(e.Object, "/audited") {
			t.Errorf("entry = %+v, want the scope @server, a subject, kind change, an installation object", e)
		}
	}

	for _, action := range []string{
		gate.AuditInstallationSetGate, gate.AuditInstallationDeleteGate, gate.AuditInstallationSetAccessLimit,
		gate.AuditInstallationDeleteAccessLimit, gate.AuditInstallationSetApprovalRule,
		gate.AuditInstallationDeleteApprovalRule,
	} {
		if !seen[action] {
			t.Errorf("no entry with the action %s", action)
		}
	}

	// No namespace holds an entry of the installation write.
	leaked := entriesOf(
		t,
		g.env.store,
		"acme",
		func(e *audit.Entry) bool { return strings.HasPrefix(e.Action, "installation:") },
	)
	if len(leaked) != 0 {
		t.Errorf("acme holds the installation entries %+v", leaked)
	}
}

// installationWriteLimit saves the 65th approval rule of the installation.
func installationWriteLimit(t *testing.T) {
	t.Helper()

	g := newGateEnv(t, installationAdmin())
	rules := installationKinds()[2]

	for i := range gate.MaxApprovalRulesPerScope {
		wantStatus(
			t,
			serveGate(t, g, gateCall{http.MethodPut, rules.itemPath(fmt.Sprintf("r%02d", i)), rules.doc, ""}),
			http.StatusOK,
		)
	}

	rec := serveGate(t, g, gateCall{http.MethodPut, rules.itemPath("over"), rules.doc, ""})
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantCode(t, rec, errcode.GateLimit)
}

// installationNoApprovalDefault checks that the installation has no approval
// default route: the installation holds none.
func installationNoApprovalDefault(t *testing.T) {
	t.Helper()

	g := newGateEnv(t, installationAdmin())

	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		rec := serveGate(t, g, gateCall{method, installationBase + "/approval-default", platformDefault, ""})
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s /approval-default = %d, want 404 or 405", method, rec.Code)
		}
	}
}

// recordingGates is a gate configuration port that records the scope of each
// call and finds no object. A test proves with it that a request that a guard
// refuses reaches no store call.
type recordingGates struct {
	apiserver.GateConfig

	mu     sync.Mutex
	scopes []gate.Scope
}

func (r *recordingGates) Gate(_ context.Context, scope gate.Scope, _ gate.ID) (gate.Gate, error) {
	r.record(scope)

	return gate.Gate{}, gate.ErrNotFound
}

func (r *recordingGates) AccessLimit(_ context.Context, scope gate.Scope, _ gate.ID) (gate.AccessLimit, error) {
	r.record(scope)

	return gate.AccessLimit{}, gate.ErrNotFound
}

func (r *recordingGates) ApprovalRule(_ context.Context, scope gate.Scope, _ gate.ID) (gate.ApprovalRule, error) {
	r.record(scope)

	return gate.ApprovalRule{}, gate.ErrNotFound
}

func (r *recordingGates) ApprovalDefault(_ context.Context, scope gate.Scope) (gate.ApprovalDefault, error) {
	r.record(scope)

	return gate.ApprovalDefault{}, gate.ErrNotFound
}

func (r *recordingGates) ApprovalRules(_ context.Context, scope gate.Scope) ([]gate.ApprovalRule, error) {
	r.record(scope)

	return nil, nil
}

func (r *recordingGates) record(scope gate.Scope) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.scopes = append(r.scopes, scope)
}

func (r *recordingGates) calls() []gate.Scope {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]gate.Scope(nil), r.scopes...)
}

// installationIDsReachNoStore sends an ID with a capital letter, a slash and a
// control character to each kind, and a path that names no valid collection or
// unit. The handler answers 400 or 422, and the port gets no call. A valid ID
// reaches the port with the scope of the path.
func installationIDsReachNoStore(t *testing.T) {
	t.Helper()

	st, nss := openIPAllowFixture(t)
	port := &recordingGates{GateConfig: nil, mu: sync.Mutex{}, scopes: nil}
	authz := gateAuthorizer{allow: map[identity.Action]namespace.Name{
		identity.ActionInstallationReadGates: "", identity.ActionNamespaceReadGates: "acme",
	}}
	h := gateHandler(t, st, nss, authz, port)

	targets := make([]string, 0, 16)

	for _, k := range installationKinds() {
		for _, id := range []string{"Bad", "a%2Fb", "a%07b", "a%0Ab"} {
			targets = append(targets, k.itemPath(id))
		}
	}

	targets = append(targets,
		gateBase+"acme/collections/ab/approval-rules/x",
		gateBase+"acme/collections/pay%2Fments/approval-default",
		gateBase+"acme/units/by-name/pulumi/web%07/approval-rules/x",
		gateBase+"acme/approval-rules/Bad",
	)

	for _, target := range targets {
		rec := serveGate(t, h, gateCall{http.MethodGet, target, "", ""})
		if rec.Code != http.StatusBadRequest && rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("GET %s = %d %s, want 400 or 422", target, rec.Code, rec.Body.String())
		}
	}

	if got := port.calls(); len(got) != 0 {
		t.Fatalf("refused requests made the store calls %v, want none", got)
	}

	checkValidRequestsReachThePort(t, h, port)
}

// checkValidRequestsReachThePort sends a valid read of each kind at the
// installation and a valid read of an approval default at a collection, and
// checks the scope that the port got for each.
func checkValidRequestsReachThePort(t *testing.T, h http.Handler, port *recordingGates) {
	t.Helper()

	for _, k := range installationKinds() {
		wantStatus(t, serveGate(t, h, gateCall{http.MethodGet, k.itemPath("ok"), "", ""}), http.StatusNotFound)
	}

	wantStatus(
		t, serveGate(t, h, gateCall{http.MethodGet, gateBase + "acme/collections/payments/approval-default", "", ""}),
		http.StatusNotFound,
	)

	want := []gate.Scope{
		gate.InstallationScope(), gate.InstallationScope(), gate.InstallationScope(),
		gate.CollectionScope("acme", "payments"),
	}

	got := port.calls()
	if len(got) != len(want) {
		t.Fatalf("valid requests made %d store calls, want %d", len(got), len(want))
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("store call %d had the scope %s, want %s", i, got[i], want[i])
		}
	}
}

// installationScopeOfThePath checks gateScopeOf alone, as if no route table
// stood in front of it: the installation object gives the installation scope,
// a path that names a collection or a unit with the installation object is
// refused, and a namespace object never gives the installation scope.
func installationScopeOfThePath(t *testing.T) {
	t.Helper()

	req := func(collection, tool, name string) *http.Request {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", http.NoBody)
		r.SetPathValue("collection", collection)
		r.SetPathValue("tool", tool)
		r.SetPathValue("name", name)

		return r
	}

	got, err := apiserver.GateScopeOfForTest(req("", "", ""), identity.ServerObject())
	if err != nil || got != gate.InstallationScope() {
		t.Fatalf("scope of the installation object = %v, %v; want the installation scope", got, err)
	}

	for _, r := range []*http.Request{req("payments", "", ""), req("", "pulumi", "web%2Fprod")} {
		if got, err = apiserver.GateScopeOfForTest(r, identity.ServerObject()); err == nil {
			t.Fatalf(
				"scope of the installation object with a collection or a unit in the path = %v, want an error",
				got,
			)
		}
	}

	got, err = apiserver.GateScopeOfForTest(req("", "", ""), identity.NamespaceObject("acme"))
	if err != nil || got != gate.NamespaceScope("acme") {
		t.Fatalf("scope of a namespace object = %v, %v; want the scope of acme", got, err)
	}
}
