package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
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
