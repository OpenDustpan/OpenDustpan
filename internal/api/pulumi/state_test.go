package pulumi_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/pulumi"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/stack"
)

// statePatterns lists every masked state read route as "METHOD path". The
// generic route tests skip them: they need live checkpoint state, so the
// dedicated tests below prove them instead.
func statePatterns() []string {
	return []string{
		"GET /api/stacks/{namespace}/{project}/{stack}/state/{version}/resources",
		"GET /api/stacks/{namespace}/{project}/{stack}/state/{version}/resources/{urn}",
		"GET /api/stacks/{namespace}/{project}/{stack}/state/{version}/outputs",
		"GET /api/stacks/{namespace}/{project}/{stack}/state/diff",
		"GET /api/stacks/{namespace}/{project}/{stack}/state-at/{at}/resources",
	}
}

// stateSigKey is the Pulumi secret signature key: no masked body may hold
// it, a ciphertext field, or secret plain text.
const stateSigKey = "4dabf18193072939515e22adb298388d"

// stateSecret returns a secret object with ciphertext for fixtures.
func stateSecret(ciphertext string) string {
	raw, err := json.Marshal(ciphertext)
	if err != nil {
		return `{"` + stateSigKey + `":"secret","ciphertext":"?"}`
	}

	return `{"` + stateSigKey + `":"1b47061264138c4ac30d75fd1eb44270","ciphertext":` + string(raw) + `}`
}

const (
	// stateURNStack is the stack resource of the state fixtures.
	stateURNStack = "urn:pulumi:dev::web::pulumi:pulumi:Stack::web-dev"
	// stateURNRef is the reference resource of the state fixtures.
	stateURNRef = "urn:pulumi:dev::web::pulumi:pulumi:StackReference::web-dev-ref"
)

// stateDeploymentV1 is the first checkpoint of the state fixtures: a stack
// resource with a secret output and a reference resource.
func stateDeploymentV1() string {
	return `{"resources":[{` +
		`"urn":"` + stateURNStack + `","type":"pulumi:pulumi:Stack",` +
		`"inputs":{"password":` + stateSecret("v1:input-cipher") + `},` +
		`"outputs":{"password":` + stateSecret("v1:output-cipher") + `,"greeting":"hi"}},` +
		`{"urn":"` + stateURNRef + `","type":"pulumi:pulumi:StackReference",` +
		`"inputs":{"name":"acme/web/other"},"outputs":{}}]}`
}

// stateDeploymentV2 changes only the greeting of the stack resource: the
// reference resource stays the same, and the secret ciphertext differs.
func stateDeploymentV2() string {
	return `{"resources":[{` +
		`"urn":"` + stateURNStack + `","type":"pulumi:pulumi:Stack",` +
		`"inputs":{"password":` + stateSecret("v1:input-cipher") + `},` +
		`"outputs":{"password":` + stateSecret("v1:rotated-cipher") + `,"greeting":"changed"}},` +
		`{"urn":"` + stateURNRef + `","type":"pulumi:pulumi:StackReference",` +
		`"inputs":{"name":"acme/web/other"},"outputs":{}}]}`
}

// stateVersions are the versions of the two fixture checkpoints.
type stateVersions struct {
	first  int
	second int
}

// stateActor is the subject that asks for each seeded update.
const stateActor = "alice"

// seedStateVersion saves deployment as the next finished version of the
// fixture stack and returns its version.
func seedStateVersion(t *testing.T, f updateFixture, deployment string) int {
	t.Helper()

	u, err := f.updates.Create(t.Context(), f.ref, stack.Plan{
		Kind: stack.KindUpdate, DryRun: false, CreatedBy: stateActor, RestoredFrom: 0, GitHead: "",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	started, err := f.updates.Start(t.Context(), f.ref, u.ID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	saveErr := f.updates.SaveCheckpoint(t.Context(), f.ref, u.ID, started.Token, stack.CheckpointInput{
		Features:      nil,
		Deployment:    []byte(deployment),
		SchemaVersion: 3,
		Sequence:      0,
		Invalid:       false,
	})
	if saveErr != nil {
		t.Fatalf("save checkpoint: %v", saveErr)
	}

	done, err := f.updates.Complete(t.Context(), f.ref, u.ID, started.Token, stack.StatusSucceeded)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	return int(done.Version)
}

// seedStateVersions saves the two fixture deployments.
func seedStateVersions(t *testing.T, f updateFixture) stateVersions {
	t.Helper()

	return stateVersions{
		first:  seedStateVersion(t, f, stateDeploymentV1()),
		second: seedStateVersion(t, f, stateDeploymentV2()),
	}
}

// checkStateRoute proves one route declares exactly unit:read on an
// authorized route with a resolver.
func checkStateRoute(t *testing.T, route pulumi.Route) {
	t.Helper()

	pattern := route.Method + " " + route.Path

	if route.Scope != pulumi.ScopeAuthorized {
		t.Errorf("route %q scope = %v, want authorized", pattern, route.Scope)
	}

	if len(route.Actions) != 1 || route.Actions[0] != identity.ActionUnitRead {
		t.Errorf("route %q actions = %v, want exactly [unit:read]", pattern, route.Actions)
	}

	if route.Resolve == nil {
		t.Errorf("route %q declares actions but no resolver", pattern)
	}
}

// TestStateRoutesDeclareUnitRead proves each masked state route declares
// exactly unit:read with a resolver on an authorized route.
func TestStateRoutesDeclareUnitRead(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	seen := map[string]bool{}

	for _, route := range f.handler.Routes() {
		pattern := route.Method + " " + route.Path
		if !slices.Contains(statePatterns(), pattern) {
			continue
		}

		seen[pattern] = true
		checkStateRoute(t, route)
	}

	for _, pattern := range statePatterns() {
		if !seen[pattern] {
			t.Errorf("route %q is not declared", pattern)
		}
	}
}

// stateTargets returns one valid target per state route for enforcement:
// the list, one resource with a seeded URN, the outputs, and the diff of
// the two seeded versions.
func stateTargets() []string {
	return []string{
		"/api/stacks/acme/web/prod/state/latest/resources",
		"/api/stacks/acme/web/prod/state/latest/resources/" + stateURNRef,
		"/api/stacks/acme/web/prod/state/latest/outputs",
		"/api/stacks/acme/web/prod/state/diff?from=1&to=2",
		"/api/stacks/acme/web/prod/state-at/" + stateAtFuture() + "/resources",
	}
}

// TestStateRoutesEnforce proves the state routes enforce unit:read: an
// allowed request answers 200, a denied one 403, and an anonymous one 401.
func TestStateRoutesEnforce(t *testing.T) {
	t.Parallel()

	for _, target := range stateTargets() {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			checkStateEnforcement(t, target)
		})
	}
}

// checkStateEnforcement proves one state target enforces unit:read.
func checkStateEnforcement(t *testing.T, target string) {
	t.Helper()

	allowed := newUpdateFixture(t, allowAll())
	seedStateVersions(t, allowed)

	if status, body := allowed.doUpdate(t, accessRequest(http.MethodGet, target, "")); status != http.StatusOK {
		t.Fatalf("allowed request answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	denied := newUpdateFixture(t, selectiveAuthorizer{deny: map[identity.Action]bool{identity.ActionUnitRead: true}})

	if status, body := denied.doUpdate(t, accessRequest(http.MethodGet, target, "")); status != http.StatusForbidden {
		t.Fatalf("denied request answers %d, want %d (%s)", status, http.StatusForbidden, body)
	}

	guest := newUpdateFixture(t, allowAll())

	if status, _ := guest.doUpdate(t, anonymousRequest(http.MethodGet, target, "")); status != http.StatusUnauthorized {
		t.Fatalf("anonymous request answers %d, want %d", status, http.StatusUnauthorized)
	}
}

// TestStateRoutesAuthorizeUnit proves each state route authorizes unit:read
// on the unit in the path.
func TestStateRoutesAuthorizeUnit(t *testing.T) {
	t.Parallel()

	for _, target := range stateTargets() {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			checkStateObject(t, target)
		})
	}
}

// checkStateObject proves one state target authorizes on
// pulumi:acme/web/prod.
func checkStateObject(t *testing.T, target string) {
	t.Helper()

	const want = "pulumi:acme/web/prod"

	recorder := &recordingAuthorizer{calls: nil}
	f := newUpdateFixture(t, recorder)
	seedStateVersions(t, f)

	if status, body := f.doUpdate(t, accessRequest(http.MethodGet, target, "")); status != http.StatusOK {
		t.Fatalf("allowed request answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	if !slices.Contains(recorder.calls, recordedCall{action: identity.ActionUnitRead, object: want}) {
		t.Fatalf("route never asks unit:read on %q (calls %v)", want, recorder.calls)
	}
}

// stateListItem is one entry of the resource list in tests.
type stateListItem struct {
	URN     string `json:"urn"`
	Type    string `json:"type"`
	Changed bool   `json:"changed"`
}

// stateListDoc is the wire shape of the resource list in tests.
type stateListDoc struct {
	Resources  []stateListItem `json:"resources"`
	NextCursor string          `json:"nextCursor"`
	Incomplete bool            `json:"incomplete"`
}

// decodeStateList decodes a resource list body.
func decodeStateList(t *testing.T, body string) stateListDoc {
	t.Helper()

	var doc stateListDoc
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode resource list: %v (%s)", err, body)
	}

	return doc
}

// checkChangedMarkers proves the changed markers of the latest list: only
// the stack resource changed.
func checkChangedMarkers(t *testing.T, latest stateListDoc) {
	t.Helper()

	if len(latest.Resources) != 2 {
		t.Fatalf("latest list holds %d resources, want 2", len(latest.Resources))
	}

	for _, res := range latest.Resources {
		wantChanged := res.URN == stateURNStack
		if res.Changed != wantChanged {
			t.Errorf("latest resource %q changed = %t, want %t", res.URN, res.Changed, wantChanged)
		}
	}
}

// checkFirstVersionMarkers proves every resource of the first version is
// new.
func checkFirstVersionMarkers(t *testing.T, pinned stateListDoc) {
	t.Helper()

	if len(pinned.Resources) != 2 {
		t.Fatalf("version 1 list holds %d resources, want 2", len(pinned.Resources))
	}

	for _, res := range pinned.Resources {
		if !res.Changed {
			t.Errorf("version 1 resource %q changed = false, want true for a new resource", res.URN)
		}
	}
}

// TestStateListChanged proves changed is true only on the changed resource
// of the latest version, and true on every resource of the first version.
func TestStateListChanged(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	versions := seedStateVersions(t, f)

	if versions.first != 1 || versions.second != 2 {
		t.Fatalf("versions = %+v, want 1 and 2", versions)
	}

	latest := decodeStateList(t, getStateOK(t, f, "/api/stacks/acme/web/prod/state/latest/resources"))
	checkChangedMarkers(t, latest)

	pinned := decodeStateList(t, getStateOK(t, f, "/api/stacks/acme/web/prod/state/1/resources"))
	checkFirstVersionMarkers(t, pinned)
}

// getStateOK fetches target and requires status 200.
func getStateOK(t *testing.T, f updateFixture, target string) string {
	t.Helper()

	status, body := f.doUpdate(t, accessRequest(http.MethodGet, target, ""))

	if status != http.StatusOK {
		t.Fatalf("GET %s answers %d, want %d (%s)", target, status, http.StatusOK, body)
	}

	return body
}

// TestStateListPaging proves the cursor pages through three resources with
// a limit of two.
func TestStateListPaging(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	third := `{"urn":"urn:pulumi:dev::web::custom:resource:Type::res3",` +
		`"type":"custom:resource:Type","inputs":{},"outputs":{}}`
	deployment := `{"resources":[{` +
		`"urn":"` + stateURNStack + `","type":"pulumi:pulumi:Stack","inputs":{},"outputs":{}},` +
		`{"urn":"` + stateURNRef + `","type":"pulumi:pulumi:StackReference","inputs":{},"outputs":{}},` +
		third + `]}`

	seedStateVersion(t, f, deployment)

	first := decodeStateList(t, getStateOK(t, f, "/api/stacks/acme/web/prod/state/latest/resources?limit=2"))

	if len(first.Resources) != 2 || first.NextCursor == "" || first.Incomplete {
		t.Fatalf("first page = %+v, want 2 resources with a cursor", first)
	}

	second := decodeStateList(
		t,
		getStateOK(t, f, "/api/stacks/acme/web/prod/state/latest/resources?limit=2&cursor="+first.NextCursor),
	)

	if len(second.Resources) != 1 || second.NextCursor != "" {
		t.Fatalf("second page = %+v, want 1 resource with no cursor", second)
	}

	if first.Resources[0].URN == second.Resources[0].URN {
		t.Fatalf("pages overlap on %q", second.Resources[0].URN)
	}
}

// TestStateListValidation proves bad paging and versions answer 400, and
// missing state answers 404.
func TestStateListValidation(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	seedStateVersions(t, f)

	for _, tc := range []struct {
		name   string
		target string
		want   int
	}{
		{name: "limit over 500", target: "/api/stacks/acme/web/prod/state/latest/resources?limit=501", want: 400},
		{name: "limit zero", target: "/api/stacks/acme/web/prod/state/latest/resources?limit=0", want: 400},
		{name: "limit text", target: "/api/stacks/acme/web/prod/state/latest/resources?limit=many", want: 400},
		{name: "bad cursor", target: "/api/stacks/acme/web/prod/state/latest/resources?cursor=nope", want: 400},
		{name: "bad version", target: "/api/stacks/acme/web/prod/state/nope/resources", want: 400},
		{name: "missing version", target: "/api/stacks/acme/web/prod/state/999999/resources", want: 404},
		{name: "missing outputs", target: "/api/stacks/acme/web/prod/state/999999/outputs", want: 404},
		{
			name:   "missing resource",
			target: "/api/stacks/acme/web/prod/state/999999/resources/" + stateURNRef,
			want:   404,
		},
		{name: "no state", target: "/api/stacks/acme/web/empty/state/latest/resources", want: 404},
		{name: "unknown urn", target: "/api/stacks/acme/web/prod/state/latest/resources/urn:unknown", want: 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, _ := f.doUpdate(t, accessRequest(http.MethodGet, tc.target, ""))

			if status != tc.want {
				t.Fatalf("GET %s answers %d, want %d", tc.target, status, tc.want)
			}
		})
	}
}

// TestStateResourceMasksSecrets proves one resource masks its secrets: each
// marker path is in masked, and no body holds the signature key, a
// ciphertext field, or secret plain text.
func TestStateResourceMasksSecrets(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	seedStateVersions(t, f)

	body := getStateOK(t, f, "/api/stacks/acme/web/prod/state/latest/resources/"+stateURNStack)

	var doc struct {
		URN     string   `json:"urn"`
		Inputs  any      `json:"inputs"`
		Outputs any      `json:"outputs"`
		Masked  []string `json:"masked"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode resource: %v (%s)", err, body)
	}

	if doc.URN != stateURNStack {
		t.Fatalf("urn = %q, want %q verbatim", doc.URN, stateURNStack)
	}

	if len(doc.Masked) != 2 {
		t.Fatalf("masked = %q, want the input and output password", doc.Masked)
	}

	checkStateBodyHasNoSecrets(t, body, doc.Masked)
}

// TestLatestStateReadsNeedNoUpdate proves that the latest reads that need no
// version answer from the checkpoint alone: the resource, the outputs and a
// diff of two latest sides answer 200 when the update that saved the latest
// checkpoint cannot be read. The resource list names the previous version,
// so it reads that update and fails.
func TestLatestStateReadsNeedNoUpdate(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	seedStateVersions(t, f)

	f.mem.mu.Lock()
	clear(f.mem.updates)
	f.mem.mu.Unlock()

	for _, target := range []string{
		"/api/stacks/acme/web/prod/state/latest/resources/" + stateURNStack,
		"/api/stacks/acme/web/prod/state/latest/outputs",
		"/api/stacks/acme/web/prod/state/diff?from=latest&to=latest",
	} {
		getStateOK(t, f, target)
	}

	status, body := f.doUpdate(t, accessRequest(http.MethodGet, "/api/stacks/acme/web/prod/state/latest/resources", ""))
	if status == http.StatusOK {
		t.Fatalf("resource list without the update answers 200, want an error: %s", body)
	}
}

// TestStateOutputsMasksSecrets proves the outputs route masks the stack
// outputs the same way.
func TestStateOutputsMasksSecrets(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	seedStateVersions(t, f)

	body := getStateOK(t, f, "/api/stacks/acme/web/prod/state/latest/outputs")

	var doc struct {
		Outputs any      `json:"outputs"`
		Masked  []string `json:"masked"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode outputs: %v (%s)", err, body)
	}

	if len(doc.Masked) != 1 {
		t.Fatalf("masked = %q, want the output password", doc.Masked)
	}

	checkStateBodyHasNoSecrets(t, body, doc.Masked)

	raw, err := json.Marshal(doc.Outputs)
	if err != nil {
		t.Fatalf("marshal outputs: %v", err)
	}

	if !strings.Contains(string(raw), `"hi"`) && !strings.Contains(string(raw), `"changed"`) {
		t.Fatalf("outputs %s lost the plain greeting", raw)
	}
}

// checkStateBodyHasNoSecrets proves body holds no signature key, no
// ciphertext field and no secret plain text, and that each marker path is
// in masked.
func checkStateBodyHasNoSecrets(t *testing.T, body string, masked []string) {
	t.Helper()

	for _, leaked := range []string{stateSigKey, "ciphertext", "v1:input-cipher", "v1:output-cipher", "v1:rotated-cipher"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("state body holds %q: %s", leaked, body)
		}
	}

	var doc any

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode state body: %v", err)
	}

	walkStateMarkers(t, doc, masked)
}

// stateMarkerPath reports the value path when v is a secret marker.
func stateMarkerPath(v any) (string, bool) {
	obj, ok := v.(map[string]any)
	if !ok {
		return "", false
	}

	secret, ok := obj["secret"].(bool)
	if !ok || !secret {
		return "", false
	}

	path, ok := obj["path"].(string)
	if !ok || path == "" {
		return "", false
	}

	return path, true
}

// walkStateMarkers proves each secret marker in v names a path in masked.
func walkStateMarkers(t *testing.T, v any, masked []string) {
	t.Helper()

	switch typed := v.(type) {
	case map[string]any:
		walkStateObject(t, typed, masked)
	case []any:
		walkStateArray(t, typed, masked)
	default:
	}
}

// walkStateObject proves each secret marker in obj names a path in masked.
func walkStateObject(t *testing.T, obj map[string]any, masked []string) {
	t.Helper()

	if path, ok := stateMarkerPath(obj); ok {
		if !slices.Contains(masked, path) {
			t.Fatalf("marker for %q is not in masked %q", path, masked)
		}

		return
	}

	for _, elem := range obj {
		walkStateMarkers(t, elem, masked)
	}
}

// walkStateArray proves each secret marker in list names a path in masked.
func walkStateArray(t *testing.T, list []any, masked []string) {
	t.Helper()

	for _, elem := range list {
		walkStateMarkers(t, elem, masked)
	}
}

// TestStateResourceWithSlashInURN proves the one-resource route serves a
// URN whose type token holds "/": clients path-escape the URN, so that "/"
// becomes "%2F" and the URN stays one segment.
func TestStateResourceWithSlashInURN(t *testing.T) {
	t.Parallel()

	const urn = "urn:pulumi:prod::web::aws:s3/bucket:Bucket::logs"

	f := newUpdateFixture(t, allowAll())
	seedStateVersion(t, f, `{"resources":[{"urn":"`+urn+`",`+
		`"type":"aws:s3/bucket:Bucket","inputs":{},"outputs":{}}]}`)

	body := getStateOK(
		t,
		f,
		"/api/stacks/acme/web/prod/state/latest/resources/"+url.PathEscape(urn),
	)

	var doc struct {
		URN  string `json:"urn"`
		Type string `json:"type"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode resource: %v (%s)", err, body)
	}

	if doc.URN != urn {
		t.Fatalf("urn = %q, want %q verbatim", doc.URN, urn)
	}

	if doc.Type != "aws:s3/bucket:Bucket" {
		t.Fatalf("type = %q, want the type token with a slash verbatim", doc.Type)
	}
}

// stateDiffChangedEntry is one changed resource of the diff in tests.
type stateDiffChangedEntry struct {
	URN            string   `json:"urn"`
	ChangedPaths   []string `json:"changedPaths"`
	ChangedSecrets []string `json:"changedSecrets"`
}

// stateDiffDoc is the wire shape of the diff in tests.
type stateDiffDoc struct {
	Added      []string                `json:"added"`
	Removed    []string                `json:"removed"`
	Changed    []stateDiffChangedEntry `json:"changed"`
	NextCursor string                  `json:"nextCursor"`
	Incomplete bool                    `json:"incomplete"`
}

// decodeStateDiff decodes a diff body.
func decodeStateDiff(t *testing.T, body string) stateDiffDoc {
	t.Helper()

	var doc stateDiffDoc
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode diff: %v (%s)", err, body)
	}

	return doc
}

// diffURNs are the URNs of the diff fixtures.
const (
	diffURNStack = stateURNStack
	diffURNKeep  = "urn:pulumi:dev::web::pulumi:pulumi:StackReference::web-dev-keep"
	diffURNGone  = "urn:pulumi:dev::web::pulumi:pulumi:StackReference::web-dev-gone"
	diffURNAdded = "urn:pulumi:dev::web::pulumi:pulumi:StackReference::web-dev-added"
)

// diffDeploymentV1 holds the stack, the kept reference and the removed
// reference.
func diffDeploymentV1() string {
	return `{"resources":[{` +
		`"urn":"` + diffURNStack + `","type":"pulumi:pulumi:Stack",` +
		`"inputs":{},"outputs":{"greeting":"hi","password":` + stateSecret("v1:old-cipher") + `}},` +
		`{"urn":"` + diffURNKeep + `","type":"pulumi:pulumi:StackReference",` +
		`"inputs":{"name":"acme/web/other"},"outputs":{}},` +
		`{"urn":"` + diffURNGone + `","type":"pulumi:pulumi:StackReference",` +
		`"inputs":{"name":"acme/web/gone"},"outputs":{}}]}`
}

// diffDeploymentV2 keeps the stack with a changed greeting and secret, keeps
// the kept reference, drops the gone one and adds a new reference.
func diffDeploymentV2() string {
	return `{"resources":[{` +
		`"urn":"` + diffURNStack + `","type":"pulumi:pulumi:Stack",` +
		`"inputs":{},"outputs":{"greeting":"changed","password":` + stateSecret("v1:new-cipher") + `}},` +
		`{"urn":"` + diffURNKeep + `","type":"pulumi:pulumi:StackReference",` +
		`"inputs":{"name":"acme/web/other"},"outputs":{}},` +
		`{"urn":"` + diffURNAdded + `","type":"pulumi:pulumi:StackReference",` +
		`"inputs":{"name":"acme/web/added"},"outputs":{}}]}`
}

// seedDiffVersions saves the diff fixtures as versions 1 and 2.
func seedDiffVersions(t *testing.T, f updateFixture) {
	t.Helper()

	seedStateVersion(t, f, diffDeploymentV1())
	seedStateVersion(t, f, diffDeploymentV2())
}

// checkDiffAddedRemoved proves the diff answers the added and removed URNs.
func checkDiffAddedRemoved(t *testing.T, doc stateDiffDoc) {
	t.Helper()

	if len(doc.Added) != 1 || doc.Added[0] != diffURNAdded {
		t.Fatalf("added = %q, want only %q", doc.Added, diffURNAdded)
	}

	if len(doc.Removed) != 1 || doc.Removed[0] != diffURNGone {
		t.Fatalf("removed = %q, want only %q", doc.Removed, diffURNGone)
	}
}

// checkDiffChangedStack proves the diff answers the changed stack with its
// plain path and its secret path.
func checkDiffChangedStack(t *testing.T, doc stateDiffDoc) {
	t.Helper()

	if len(doc.Changed) != 1 || doc.Changed[0].URN != diffURNStack {
		t.Fatalf("changed = %+v, want only the stack resource", doc.Changed)
	}

	if !slices.Contains(doc.Changed[0].ChangedPaths, "/outputs/greeting") {
		t.Fatalf("changed paths = %q, want /outputs/greeting", doc.Changed[0].ChangedPaths)
	}

	if !slices.Contains(doc.Changed[0].ChangedSecrets, "/outputs/password") {
		t.Fatalf("changed secrets = %q, want /outputs/password", doc.Changed[0].ChangedSecrets)
	}
}

// checkDiffBodyHasNoSecrets proves the diff body holds no ciphertext,
// signature key or plain secret.
func checkDiffBodyHasNoSecrets(t *testing.T, body string, doc stateDiffDoc) {
	t.Helper()

	for _, leaked := range []string{stateSigKey, "ciphertext", "v1:old-cipher", "v1:new-cipher"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("diff body holds %q: %s", leaked, body)
		}
	}

	if doc.NextCursor != "" || doc.Incomplete {
		t.Fatalf("diff = %+v, want a single complete page", doc)
	}
}

// TestStateDiffAnswersAddedRemovedChanged proves the diff of the two fixture
// versions answers the added and removed URNs and the changed stack with its
// plain path and its secret path, without any ciphertext, signature key or
// plain secret.
func TestStateDiffAnswersAddedRemovedChanged(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	seedDiffVersions(t, f)

	body := getStateOK(t, f, "/api/stacks/acme/web/prod/state/diff?from=1&to=2")
	doc := decodeStateDiff(t, body)

	checkDiffAddedRemoved(t, doc)
	checkDiffChangedStack(t, doc)
	checkDiffBodyHasNoSecrets(t, body, doc)
}

// TestStateDiffSameVersionIsEmpty proves the diff of a version with itself
// holds no change.
func TestStateDiffSameVersionIsEmpty(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	seedDiffVersions(t, f)

	doc := decodeStateDiff(t, getStateOK(t, f, "/api/stacks/acme/web/prod/state/diff?from=2&to=2"))

	if len(doc.Added) != 0 || len(doc.Removed) != 0 || len(doc.Changed) != 0 {
		t.Fatalf("same-version diff = %+v, want no change", doc)
	}
}

// TestStateDiffLatest proves "latest" names the latest checkpoint as a diff
// side.
func TestStateDiffLatest(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	seedDiffVersions(t, f)

	pinned := getStateOK(t, f, "/api/stacks/acme/web/prod/state/diff?from=1&to=2")
	latest := getStateOK(t, f, "/api/stacks/acme/web/prod/state/diff?from=1&to=latest")

	if latest != pinned {
		t.Fatalf("diff to latest differs from diff to 2:\nlatest=%s\npinned=%s", latest, pinned)
	}
}

// collectDiffPage records the single entry of one diff page in seen and
// returns the next cursor. It fails when the page holds anything but one
// entry.
func collectDiffPage(t *testing.T, f updateFixture, cursor string, seen map[string]bool) string {
	t.Helper()

	target := "/api/stacks/acme/web/prod/state/diff?from=1&to=2&limit=1&cursor=" + cursor
	doc := decodeStateDiff(t, getStateOK(t, f, target))

	if got := len(doc.Added) + len(doc.Removed) + len(doc.Changed); got != 1 {
		t.Fatalf("page at %q holds %d entries, want 1 (%+v)", cursor, got, doc)
	}

	for _, urn := range slices.Concat(doc.Added, doc.Removed, diffChangedURNs(doc)) {
		if seen[urn] {
			t.Fatalf("resource %q listed twice", urn)
		}

		seen[urn] = true
	}

	return doc.NextCursor
}

// diffChangedURNs returns the URNs of the changed entries of doc.
func diffChangedURNs(doc stateDiffDoc) []string {
	urns := make([]string, 0, len(doc.Changed))
	for _, entry := range doc.Changed {
		urns = append(urns, entry.URN)
	}

	return urns
}

// TestStateDiffPaging proves the cursor pages through the three differing
// URNs with a limit of one.
func TestStateDiffPaging(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	seedDiffVersions(t, f)

	seen := map[string]bool{}
	cursor := ""

	for {
		cursor = collectDiffPage(t, f, cursor, seen)

		if cursor == "" {
			break
		}
	}

	for _, want := range []string{diffURNAdded, diffURNGone, diffURNStack} {
		if !seen[want] {
			t.Fatalf("paged diff never lists %q (seen %v)", want, seen)
		}
	}
}

// TestStateDiffValidation proves bad sides and paging answer 400, and
// missing state answers 404.
func TestStateDiffValidation(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	seedDiffVersions(t, f)

	for _, tc := range []struct {
		name   string
		target string
		want   int
	}{
		{name: "missing from", target: "/api/stacks/acme/web/prod/state/diff?to=2", want: 400},
		{name: "missing to", target: "/api/stacks/acme/web/prod/state/diff?from=1", want: 400},
		{name: "bad from", target: "/api/stacks/acme/web/prod/state/diff?from=nope&to=2", want: 400},
		{name: "bad to", target: "/api/stacks/acme/web/prod/state/diff?from=1&to=nope", want: 400},
		{name: "missing version", target: "/api/stacks/acme/web/prod/state/diff?from=1&to=999999", want: 404},
		{name: "limit over 500", target: "/api/stacks/acme/web/prod/state/diff?from=1&to=2&limit=501", want: 400},
		{name: "bad cursor", target: "/api/stacks/acme/web/prod/state/diff?from=1&to=2&cursor=nope", want: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, _ := f.doUpdate(t, accessRequest(http.MethodGet, tc.target, ""))

			if status != tc.want {
				t.Fatalf("GET %s answers %d, want %d", tc.target, status, tc.want)
			}
		})
	}
}

// TestStateDiffDoesNotClash proves the literal diff path does not clash with
// the version routes: the diff answers with its query, and the version list
// still answers.
func TestStateDiffDoesNotClash(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	seedDiffVersions(t, f)

	getStateOK(t, f, "/api/stacks/acme/web/prod/state/diff?from=1&to=2")
	getStateOK(t, f, "/api/stacks/acme/web/prod/state/1/resources")
	getStateOK(t, f, "/api/stacks/acme/web/prod/state/latest/resources")
}

// TestStateResourceUnescapedSlashMatchesNothing proves the unescaped form
// of a URN with "/" does not match another resource: the one-segment "{urn}"
// pattern only sees the escaped form, so the raw slashes miss the route.
func TestStateResourceUnescapedSlashMatchesNothing(t *testing.T) {
	t.Parallel()

	const (
		slashURN = "urn:pulumi:prod::web::aws:s3/bucket:Bucket::logs"
		plainURN = "urn:pulumi:prod::web::custom:resource:Type::other"
	)

	f := newUpdateFixture(t, allowAll())
	seedStateVersion(t, f, `{"resources":[`+
		`{"urn":"`+slashURN+`","type":"aws:s3/bucket:Bucket","inputs":{},"outputs":{}},`+
		`{"urn":"`+plainURN+`","type":"custom:resource:Type","inputs":{},"outputs":{}}]}`)

	status, body := f.doUpdate(
		t,
		accessRequest(http.MethodGet, "/api/stacks/acme/web/prod/state/latest/resources/"+slashURN, ""),
	)

	if status == http.StatusOK {
		t.Fatalf("unescaped URN answers 200, want no match (%s)", body)
	}

	if strings.Contains(body, plainURN) {
		t.Fatalf("unescaped URN answers with another resource %q (%s)", plainURN, body)
	}
}

// stateFilterList fetches the resource list with filter q and requires
// status 200.
func stateFilterList(t *testing.T, f updateFixture, q string) stateListDoc {
	t.Helper()

	target := "/api/stacks/acme/web/prod/state/latest/resources?filter[q]=" + url.QueryEscape(q)

	return decodeStateList(t, getStateOK(t, f, target))
}

// TestStateListFilterMatchesType proves filter[q] with a type substring
// answers only resources of that type.
func TestStateListFilterMatchesType(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	seedStateVersions(t, f)

	list := stateFilterList(t, f, "StackReference")

	if len(list.Resources) != 1 {
		t.Fatalf("filtered list holds %d resources, want only the reference", len(list.Resources))
	}

	if list.Resources[0].URN != stateURNRef {
		t.Fatalf("urn = %q, want %q", list.Resources[0].URN, stateURNRef)
	}

	if list.NextCursor != "" || list.Incomplete {
		t.Fatalf("filtered list = %+v, want one page with no cursor", list)
	}
}

// TestStateListFilterMatchesURN proves filter[q] with a URN substring
// answers the named resource.
func TestStateListFilterMatchesURN(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	seedStateVersions(t, f)

	list := stateFilterList(t, f, "pulumi:pulumi:Stack::web-dev")

	if len(list.Resources) != 1 || list.Resources[0].URN != stateURNStack {
		t.Fatalf("filtered list = %+v, want only the stack resource", list)
	}
}

// TestStateListFilterIgnoresValues proves filter[q] never matches attribute
// values or secret values: a greeting, a plain input and a ciphertext all
// answer no resources.
func TestStateListFilterIgnoresValues(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	seedStateVersions(t, f)

	for _, q := range []string{"hi", "changed", "acme/web/other", "v1:input-cipher", "v1:rotated-cipher"} {
		list := stateFilterList(t, f, q)

		if len(list.Resources) != 0 {
			t.Fatalf("filter %q matched %d resources, want none", q, len(list.Resources))
		}

		if list.NextCursor != "" {
			t.Fatalf("filter %q next cursor = %q, want empty", q, list.NextCursor)
		}
	}
}

// TestStateListFilterPaging proves paging applies after the filter: a page
// holds only matching resources and the cursor walks them.
func TestStateListFilterPaging(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	third := `{"urn":"urn:pulumi:dev::web::custom:resource:Type::res3",` +
		`"type":"custom:resource:Type","inputs":{},"outputs":{}}`
	deployment := `{"resources":[{` +
		`"urn":"` + stateURNStack + `","type":"pulumi:pulumi:Stack","inputs":{},"outputs":{}},` +
		`{"urn":"` + stateURNRef + `","type":"pulumi:pulumi:StackReference","inputs":{},"outputs":{}},` +
		third + `]}`

	seedStateVersion(t, f, deployment)

	first := decodeStateList(t, getStateOK(
		t,
		f,
		"/api/stacks/acme/web/prod/state/latest/resources?filter[q]="+url.QueryEscape("pulumi:dev")+`&limit=2`,
	))

	if len(first.Resources) != 2 || first.NextCursor == "" {
		t.Fatalf("first filtered page = %+v, want 2 matches with a cursor", first)
	}

	second := decodeStateList(t, getStateOK(
		t,
		f,
		"/api/stacks/acme/web/prod/state/latest/resources?filter[q]="+
			url.QueryEscape("pulumi:dev")+`&limit=2&cursor=`+first.NextCursor,
	))

	if len(second.Resources) != 1 || second.NextCursor != "" {
		t.Fatalf("second filtered page = %+v, want 1 match with no cursor", second)
	}

	if first.Resources[0].URN == second.Resources[0].URN {
		t.Fatalf("filtered pages overlap on %q", second.Resources[0].URN)
	}
}

// TestStateListFilterKeepsIncomplete proves the filter keeps the incomplete
// marker: skipped entries with an empty URN still mark the page, which holds
// only matching resources.
func TestStateListFilterKeepsIncomplete(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	seedStateVersion(t, f, `{"resources":[{`+
		`"urn":"`+stateURNStack+`","type":"pulumi:pulumi:Stack","inputs":{},"outputs":{}},`+
		`{"urn":"","type":"custom:resource:Type","inputs":{}},`+
		`{"urn":"`+stateURNRef+`","type":"pulumi:pulumi:StackReference","inputs":{},"outputs":{}}]}`)

	body := getStateOK(
		t,
		f,
		"/api/stacks/acme/web/prod/state/latest/resources?filter[q]="+url.QueryEscape("pulumi:pulumi:Stack::"),
	)

	var doc struct {
		Resources  []stateListItem `json:"resources"`
		NextCursor string          `json:"nextCursor"`
		Incomplete bool            `json:"incomplete"`
		Reason     string          `json:"reason"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode filtered list: %v (%s)", err, body)
	}

	if !doc.Incomplete || doc.Reason == "" {
		t.Fatalf("filtered list = %+v, want incomplete with a reason", doc)
	}

	if len(doc.Resources) != 1 || doc.Resources[0].URN != stateURNStack {
		t.Fatalf("filtered list = %+v, want only the stack resource", doc)
	}
}

// stateAtFuture is a time after every update of a fixture, in Unix
// milliseconds.
func stateAtFuture() string {
	return strconv.FormatInt(time.Now().Add(time.Hour).UnixMilli(), 10)
}

// stateAtDoc is the wire shape of the resource list at a time in tests.
type stateAtDoc struct {
	stateListDoc

	RequestedBy string `json:"requestedBy"`
	Version     int    `json:"version"`
	EndedAt     int64  `json:"endedAt"`
	Running     int    `json:"running"`
}

// startRunningUpdate starts an update of the fixture stack and leaves it
// running.
func startRunningUpdate(t *testing.T, f updateFixture) {
	t.Helper()

	u, err := f.updates.Create(t.Context(), f.ref, stack.Plan{
		Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: "",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := f.updates.Start(t.Context(), f.ref, u.ID); err != nil {
		t.Fatalf("start: %v", err)
	}
}

// TestStateAtResources proves the resource list at a time answers the update
// that held the state, and names an update that is still running.
func TestStateAtResources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		running     bool
		wantRunning int
	}{
		{name: "after the last update", running: false, wantRunning: 0},
		{name: "while an update runs", running: true, wantRunning: 3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newUpdateFixture(t, allowAll())
			seedStateVersions(t, f)

			if tc.running {
				startRunningUpdate(t, f)
			}

			target := "/api/stacks/acme/web/prod/state-at/" + stateAtFuture() + "/resources"

			var doc stateAtDoc
			if err := json.Unmarshal([]byte(getStateOK(t, f, target)), &doc); err != nil {
				t.Fatalf("decode list at a time: %v", err)
			}

			if doc.Version != 2 || doc.Running != tc.wantRunning || len(doc.Resources) != 2 || doc.EndedAt == 0 ||
				doc.RequestedBy != stateActor {
				t.Errorf("list = %+v, want version 2, running %d, 2 resources, requestedBy %q",
					doc, tc.wantRunning, stateActor)
			}
		})
	}
}

// TestStateAtRefusals proves the refusals of the resource list at a time: a
// time before the first update names the time of that update, a stack with no
// checkpoint and an unknown stack answer 404, and a bad time answers a clear
// error. Each answer has the error shape {"code": <status>, "message": "..."},
// with the errorCode of the refusal when it has one.
func TestStateAtRefusals(t *testing.T) {
	t.Parallel()

	tests := []stateAtRefusal{
		{
			name:   "before the first update",
			target: "/api/stacks/acme/web/prod/state-at/1/resources",
			seed:   true,
			want:   http.StatusNotFound,
			message: regexp.MustCompile(
				`^stack has no state at that time: the first state of acme/web/prod is at \S+ \(\d{13} ms\)$`,
			),
			errorCode: errcode.StackNoStateAtTime,
		},
		{
			name:      "a stack with no checkpoint",
			target:    "/api/stacks/acme/web/prod/state-at/1/resources",
			seed:      false,
			want:      http.StatusNotFound,
			message:   regexp.MustCompile(`^stack has no state at that time: acme/web/prod has no checkpoint yet$`),
			errorCode: errcode.StackNoState,
		},
		{
			name:   "an unknown stack",
			target: "/api/stacks/acme/web/absent/state-at/1/resources",
			seed:   true,
			want:   http.StatusNotFound,
			message: regexp.MustCompile(
				`^state of acme/web/absent at \S+: get stack acme/web/absent: stack not found$`,
			),
			errorCode: errcode.StackNotFound,
		},
		{
			name:      "a time that is not Unix milliseconds",
			target:    "/api/stacks/acme/web/prod/state-at/yesterday/resources",
			seed:      true,
			want:      http.StatusBadRequest,
			message:   regexp.MustCompile(`is not Unix milliseconds`),
			errorCode: errcode.UpdateInvalid,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newUpdateFixture(t, allowAll())
			if tc.seed {
				seedStateVersions(t, f)
			}

			status, body := f.doUpdate(t, accessRequest(http.MethodGet, tc.target, ""))

			checkStateAtRefusal(t, tc, status, body)
		})
	}
}

// stateAtRefusal is one refusal of the resource list at a time.
type stateAtRefusal struct {
	name    string
	target  string
	seed    bool
	want    int
	message *regexp.Regexp
	// errorCode is the code of the catalog that the answer holds, or "" for
	// an answer without a code.
	errorCode string
}

// checkStateAtRefusal fails the test unless the answer status and body of tc
// have the error shape {"code": <status>, "message": "..."} with the status
// tc.want, a message that matches tc.message, and the errorCode tc.errorCode.
// An answer of a code can hold errorArgs too. An answer without a code has no
// errorCode.
func checkStateAtRefusal(t *testing.T, tc stateAtRefusal, status int, body string) {
	t.Helper()

	var shape map[string]any
	if err := json.Unmarshal([]byte(body), &shape); err != nil {
		t.Fatalf("decode error %q: %v", body, err)
	}

	gotCode := shape["errorCode"]
	delete(shape, "errorCode")
	delete(shape, "errorArgs")

	text, textOK := shape["message"].(string)
	number, numberOK := shape["code"].(float64)

	if !textOK || !numberOK || status != tc.want || int(number) != tc.want || len(shape) != 2 ||
		!tc.message.MatchString(text) || (tc.errorCode == "" && gotCode != nil) ||
		(tc.errorCode != "" && gotCode != tc.errorCode) {
		t.Errorf("answer = %d %s, want {\"code\":%d,\"message\":...} matching %q, errorCode %q",
			status, body, tc.want, tc.message, tc.errorCode)
	}
}
