package tfe_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// workspaceTestPermissions decodes the permission flags of a workspace
// document in tests.
type workspaceTestPermissions struct {
	CanLock        bool `json:"can-lock"`         //nolint:tagliatelle // The TFE wire name; it must not change.
	CanUnlock      bool `json:"can-unlock"`       //nolint:tagliatelle // The TFE wire name; it must not change.
	CanForceUnlock bool `json:"can-force-unlock"` //nolint:tagliatelle // The TFE wire name; it must not change.
	CanQueueRun    bool `json:"can-queue-run"`    //nolint:tagliatelle // The TFE wire name; it must not change.
}

// workspaceTestAttributes decodes the attributes of a workspace document in
// tests.
type workspaceTestAttributes struct {
	Name          string                   `json:"name"`
	ExecutionMode string                   `json:"execution-mode"` //nolint:tagliatelle // The TFE wire name; it must not change.
	Locked        bool                     `json:"locked"`
	LockHolder    string                   `json:"lock-holder"` //nolint:tagliatelle // The TFE wire name; it must not change.
	LockedAt      string                   `json:"locked-at"`   //nolint:tagliatelle // The TFE wire name; it must not change.
	LockReason    string                   `json:"lock-reason"` //nolint:tagliatelle // The TFE wire name; it must not change.
	AutoApply     bool                     `json:"auto-apply"`  //nolint:tagliatelle // The TFE wire name; it must not change.
	Permissions   workspaceTestPermissions `json:"permissions"`
}

// workspaceTestRelationTarget decodes one relationship target in tests.
type workspaceTestRelationTarget struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// workspaceTestRelation decodes one relationship in tests: data is null when
// the workspace has no current state.
type workspaceTestRelation struct {
	Data *workspaceTestRelationTarget `json:"data"`
}

// workspaceTestResource decodes one workspace resource in tests.
type workspaceTestResource struct {
	ID            string                           `json:"id"`
	Type          string                           `json:"type"`
	Attributes    workspaceTestAttributes          `json:"attributes"`
	Relationships map[string]workspaceTestRelation `json:"relationships"`
}

// workspaceTestDoc decodes a single workspace document in tests.
type workspaceTestDoc struct {
	Data workspaceTestResource `json:"data"`
}

// workspaceTestPagination decodes the meta.pagination object in tests.
type workspaceTestPagination struct {
	CurrentPage int  `json:"current-page"` //nolint:tagliatelle // The TFE wire name; it must not change.
	PageSize    int  `json:"page-size"`    //nolint:tagliatelle // The TFE wire name; it must not change.
	PrevPage    *int `json:"prev-page"`    //nolint:tagliatelle // The TFE wire name; it must not change.
	NextPage    *int `json:"next-page"`    //nolint:tagliatelle // The TFE wire name; it must not change.
	TotalPages  int  `json:"total-pages"`  //nolint:tagliatelle // The TFE wire name; it must not change.
	TotalCount  int  `json:"total-count"`  //nolint:tagliatelle // The TFE wire name; it must not change.
}

// workspaceTestListMeta decodes the meta object of a list document in tests.
type workspaceTestListMeta struct {
	Pagination workspaceTestPagination `json:"pagination"`
}

// workspaceTestListDoc decodes a workspace list document in tests.
type workspaceTestListDoc struct {
	Data []workspaceTestResource `json:"data"`
	Meta workspaceTestListMeta   `json:"meta"`
}

// newWorkspaceHandler returns a production Handler over stubWorkspaces: only
// the production routes, with authz as the authorizer.
func newWorkspaceHandler(t *testing.T, authz identity.Authorizer) *tfe.Handler {
	t.Helper()

	handler, err := tfe.NewHandler(tfe.Config{
		Registry:       nil,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     authz,
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     stubWorkspaces{},
		OutputChecker:  newTestOutputChecker(t, authz, stubWorkspaces{}),
		BaseURL:        baseurl.Resolver{},
		Audit:          newTestAudit(),
		Gate:           nil,
		MaxStateBytes:  0,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		SessionPolicy:  nil,
		StateGuard:     stategate.Off("the test keeps no gates"),
		Placer:         testPlacer(),
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// allowAll authorizes every action.
func allowAll() selectiveAuthorizer {
	return selectiveAuthorizer{deny: map[identity.Action]bool{}}
}

// workspaceRequest builds an authenticated request for a workspace route.
func workspaceRequest(t *testing.T, method, target, body string) *http.Request {
	t.Helper()

	var req *http.Request

	if body == "" {
		req = httptest.NewRequestWithContext(t.Context(), method, target, http.NoBody)
	} else {
		req = httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	}

	req.Header.Set("Authorization", "Bearer test-token")

	return req
}

// createBody returns a workspace create body for name.
func createBody(name string) string {
	return `{"data":{"type":"workspaces","attributes":{"name":` + strconv.Quote(name) + `}}}`
}

// decodeWorkspaceDoc decodes a single workspace document and fails the test
// when the status or shape is wrong.
func decodeWorkspaceDoc(t *testing.T, rec *httptest.ResponseRecorder, want int) workspaceTestDoc {
	t.Helper()

	if rec.Code != want {
		t.Fatalf("request answers %d, want %d: %s", rec.Code, want, rec.Body.String())
	}

	if got := rec.Header().Get("Content-Type"); got != "application/vnd.api+json" {
		t.Fatalf("media type = %q, want %q", got, "application/vnd.api+json")
	}

	var doc workspaceTestDoc

	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode workspace body: %v", err)
	}

	return doc
}

// TestWorkspaceRoutesDeclareActionsAndObjects proves each workspace route
// declares its action and resolves its object: reads use unit:read, creation
// uses unit:manage, force-unlock uses unit:write-forced, lock and unlock use unit:write, the list
// uses namespace:read, and every unit route resolves to opentofu:acme/web.
func TestWorkspaceRoutesDeclareActionsAndObjects(t *testing.T) {
	t.Parallel()

	handler := newWorkspaceHandler(t, allowAll())

	cases := []workspaceRouteCase{
		{
			name:    "read by name",
			pattern: "GET /api/v2/organizations/{organization}/workspaces/{workspace}",
			method:  http.MethodGet,
			target:  "/api/v2/organizations/acme/workspaces/web",
			body:    "",
			actions: []identity.Action{identity.ActionUnitRead},
			object:  "opentofu:acme/web",
			isUnit:  true,
		},
		{
			name:    "create",
			pattern: "POST /api/v2/organizations/{organization}/workspaces",
			method:  http.MethodPost,
			target:  "/api/v2/organizations/acme/workspaces",
			body:    createBody("web"),
			actions: []identity.Action{identity.ActionUnitManage},
			object:  "opentofu:acme/web",
			isUnit:  true,
		},
		{
			name:    "read by ID",
			pattern: "GET /api/v2/workspaces/{id}",
			method:  http.MethodGet,
			target:  "/api/v2/workspaces/ws-test123",
			body:    "",
			actions: []identity.Action{identity.ActionUnitRead},
			object:  "opentofu:acme/web",
			isUnit:  true,
		},
		{
			name:    "list",
			pattern: "GET /api/v2/organizations/{organization}/workspaces",
			method:  http.MethodGet,
			target:  "/api/v2/organizations/acme/workspaces",
			body:    "",
			actions: []identity.Action{identity.ActionNamespaceRead},
			object:  "acme",
			isUnit:  false,
		},
		{
			name:    "lock",
			pattern: "POST /api/v2/workspaces/{id}/actions/lock",
			method:  http.MethodPost,
			target:  "/api/v2/workspaces/ws-test123/actions/lock",
			body:    `{"data":{}}`,
			actions: []identity.Action{identity.ActionUnitWrite},
			object:  "opentofu:acme/web",
			isUnit:  true,
		},
		{
			name:    "unlock",
			pattern: "POST /api/v2/workspaces/{id}/actions/unlock",
			method:  http.MethodPost,
			target:  "/api/v2/workspaces/ws-test123/actions/unlock",
			body:    `{"data":{}}`,
			actions: []identity.Action{identity.ActionUnitWrite},
			object:  "opentofu:acme/web",
			isUnit:  true,
		},
		{
			name:    "force-unlock",
			pattern: "POST /api/v2/workspaces/{id}/actions/force-unlock",
			method:  http.MethodPost,
			target:  "/api/v2/workspaces/ws-test123/actions/force-unlock",
			body:    `{"data":{}}`,
			actions: []identity.Action{identity.ActionUnitWriteForced},
			object:  "opentofu:acme/web",
			isUnit:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkWorkspaceRoute(t, handler, tc)
		})
	}
}

// workspaceRouteCase pins one workspace route: its pattern, its actions and
// the object its resolver returns.
type workspaceRouteCase struct {
	name    string
	pattern string
	method  string
	target  string
	body    string
	actions []identity.Action
	object  string
	isUnit  bool
}

// checkWorkspaceRoute proves one workspace route declares its actions and
// resolves its object.
func checkWorkspaceRoute(t *testing.T, handler *tfe.Handler, tc workspaceRouteCase) {
	t.Helper()

	var found *tfe.Route

	for _, route := range handler.Routes() {
		if route.Method+" "+route.Path == tc.pattern {
			found = &route

			break
		}
	}

	if found == nil {
		t.Fatalf("route %q is not declared", tc.pattern)
	}

	if len(found.Actions) != len(tc.actions) {
		t.Fatalf("route %q actions = %v, want %v", tc.pattern, found.Actions, tc.actions)
	}

	for i := range tc.actions {
		if found.Actions[i] != tc.actions[i] {
			t.Fatalf("route %q actions = %v, want %v", tc.pattern, found.Actions, tc.actions)
		}
	}

	if found.Resolve == nil {
		t.Fatalf("route %q declares no resolver", tc.pattern)
	}

	var req *http.Request

	if tc.body == "" {
		req = httptest.NewRequestWithContext(t.Context(), tc.method, tc.target, http.NoBody)
	} else {
		req = httptest.NewRequestWithContext(t.Context(), tc.method, tc.target, strings.NewReader(tc.body))
	}

	req.SetPathValue("organization", "acme")
	req.SetPathValue("workspace", "web")
	req.SetPathValue("id", "ws-test123")
	req.SetPathValue("workspace_id", "ws-test123")
	req.SetPathValue("state_version_id", "sv-test123")
	req.SetPathValue("output_id", "sv-test123.greeting")

	obj, err := found.Resolve(httptest.NewRecorder(), req)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if obj.String() != tc.object || obj.IsUnit() != tc.isUnit {
		t.Fatalf("object = %q (unit %v), want %q (unit %v)", obj.String(), obj.IsUnit(), tc.object, tc.isUnit)
	}
}

// checkWorkspaceDoc proves one workspace document holds the want ID and
// name, type workspaces, execution-mode local, auto-apply false, every
// permission flag set, no lock view and the current-state-version
// relationship of want state: the sv-test123 version, or null when the
// workspace has no state.
func checkWorkspaceDoc(
	t *testing.T, doc workspaceTestDoc, wantID, wantName string, wantState *string,
) {
	t.Helper()

	got := doc.Data

	if got.ID != wantID || got.Type != "workspaces" {
		t.Fatalf("workspace = %q %q, want id %q type %q", got.ID, got.Type, wantID, "workspaces")
	}

	attrs := got.Attributes
	if attrs.Name != wantName || attrs.ExecutionMode != "local" || attrs.Locked || attrs.AutoApply {
		t.Fatalf("attributes = %+v, want name %s, execution-mode local, unlocked, auto-apply false", attrs, wantName)
	}

	if attrs.LockHolder != "" || attrs.LockedAt != "" || attrs.LockReason != "" {
		t.Fatalf("lock view = %q %q %q, want empty", attrs.LockHolder, attrs.LockedAt, attrs.LockReason)
	}

	checkAllPermissions(t, attrs.Permissions)
	checkStateRelation(t, got.Relationships, wantState)
}

// checkAllPermissions proves every permission flag is set.
func checkAllPermissions(t *testing.T, perms workspaceTestPermissions) {
	t.Helper()

	if !perms.CanLock || !perms.CanUnlock || !perms.CanForceUnlock || !perms.CanQueueRun {
		t.Fatalf("permissions = %+v, want every flag set", perms)
	}
}

// checkStateRelation proves the document holds the current-state-version
// relationship: the want state version, or null data when want is nil.
func checkStateRelation(t *testing.T, relations map[string]workspaceTestRelation, want *string) {
	t.Helper()

	relation, ok := relations["current-state-version"]
	if !ok {
		t.Fatal("document holds no current-state-version relationship")
	}

	if want == nil {
		if relation.Data != nil {
			t.Fatalf("current-state-version = %+v, want null data", relation.Data)
		}

		return
	}

	if relation.Data == nil || relation.Data.ID != *want || relation.Data.Type != "state-versions" {
		t.Fatalf("current-state-version = %+v, want %q of type state-versions", relation.Data, *want)
	}
}

// TestReadWorkspace proves the read by name answers the workspace document
// with its current state version, and a workspace without state answers a
// null current-state-version relationship.
func TestReadWorkspace(t *testing.T) {
	t.Parallel()

	handler := newWorkspaceHandler(t, allowAll())

	t.Run("workspace with state", func(t *testing.T) {
		t.Parallel()

		req := workspaceRequest(t, http.MethodGet, "/api/v2/organizations/acme/workspaces/web", "")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		checkWorkspaceDoc(t, decodeWorkspaceDoc(t, rec, http.StatusOK), "ws-test123", "web", new("sv-test123"))
	})

	t.Run("workspace without state", func(t *testing.T) {
		t.Parallel()

		req := workspaceRequest(t, http.MethodGet, "/api/v2/organizations/acme/workspaces/empty", "")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		checkWorkspaceDoc(t, decodeWorkspaceDoc(t, rec, http.StatusOK), "ws-empty123", "empty", nil)
	})
}

// TestReadWorkspaceUnknown proves a missing workspace and an unknown
// organization answer 404 as a JSON:API error.
func TestReadWorkspaceUnknown(t *testing.T) {
	t.Parallel()

	handler := newWorkspaceHandler(t, allowAll())

	for _, target := range []string{
		"/api/v2/organizations/acme/workspaces/ghost",
		"/api/v2/organizations/ghost/workspaces/web",
	} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, workspaceRequest(t, http.MethodGet, target, ""))

			checkJSONAPIError(t, rec, http.StatusNotFound)
		})
	}
}

// TestReadWorkspaceInvalidName proves a bad workspace name answers 422.
func TestReadWorkspaceInvalidName(t *testing.T) {
	t.Parallel()

	handler := newWorkspaceHandler(t, allowAll())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, workspaceRequest(t, http.MethodGet, "/api/v2/organizations/acme/workspaces/bad!", ""))

	checkJSONAPIError(t, rec, http.StatusUnprocessableEntity)
}

// TestReadWorkspaceByID proves the read by ID answers the workspace resolved
// through the ID, and an unknown ID answers 404.
func TestReadWorkspaceByID(t *testing.T) {
	t.Parallel()

	handler := newWorkspaceHandler(t, allowAll())

	t.Run("known ID", func(t *testing.T) {
		t.Parallel()

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, workspaceRequest(t, http.MethodGet, "/api/v2/workspaces/ws-test123", ""))

		doc := decodeWorkspaceDoc(t, rec, http.StatusOK)

		if doc.Data.ID != "ws-test123" || doc.Data.Attributes.Name != "web" {
			t.Fatalf("workspace = %+v, want ws-test123 web", doc.Data)
		}
	})

	t.Run("unknown ID", func(t *testing.T) {
		t.Parallel()

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, workspaceRequest(t, http.MethodGet, "/api/v2/workspaces/ws-missing", ""))

		checkJSONAPIError(t, rec, http.StatusNotFound)
	})
}

// TestCreateWorkspace proves a creation answers 201 with the workspace
// document and a ws- ID.
func TestCreateWorkspace(t *testing.T) {
	t.Parallel()

	handler := newWorkspaceHandler(t, allowAll())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(
		rec,
		workspaceRequest(t, http.MethodPost, "/api/v2/organizations/acme/workspaces", createBody("fresh")),
	)

	doc := decodeWorkspaceDoc(t, rec, http.StatusCreated)

	if !strings.HasPrefix(doc.Data.ID, "ws-") {
		t.Fatalf("workspace id = %q, want the ws- prefix", doc.Data.ID)
	}

	if doc.Data.Type != "workspaces" || doc.Data.Attributes.Name != "fresh" {
		t.Fatalf("workspace = %+v, want workspaces fresh", doc.Data)
	}

	if doc.Data.Attributes.ExecutionMode != "local" || doc.Data.Attributes.AutoApply {
		t.Fatalf("attributes = %+v, want execution-mode local and auto-apply false", doc.Data.Attributes)
	}
}

// TestCreateWorkspaceBadName proves a bad name answers 422: an invalid name,
// a missing name and a malformed body.
func TestCreateWorkspaceBadName(t *testing.T) {
	t.Parallel()

	handler := newWorkspaceHandler(t, allowAll())

	for _, tc := range []struct{ name, body string }{
		{name: "invalid name", body: createBody("bad!")},
		{name: "missing name", body: `{"data":{"type":"workspaces","attributes":{}}}`},
		{name: "malformed body", body: `{`},
		{name: "empty body", body: ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			handler.ServeHTTP(
				rec,
				workspaceRequest(t, http.MethodPost, "/api/v2/organizations/acme/workspaces", tc.body),
			)

			checkJSONAPIError(t, rec, http.StatusUnprocessableEntity)
		})
	}
}

// TestCreateWorkspaceNameStatesTheRule proves that an invalid name and a
// missing name answer 422 with the same detail: the rule of a workspace name.
func TestCreateWorkspaceNameStatesTheRule(t *testing.T) {
	t.Parallel()

	handler := newWorkspaceHandler(t, allowAll())

	const rule = ": use 1 to 90 ASCII letters, digits, - and _"

	for _, tc := range []struct{ body, want string }{
		{body: createBody("a/b"), want: `invalid workspace name: "a/b"` + rule},
		{body: `{"data":{"type":"workspaces","attributes":{}}}`, want: `invalid workspace name: ""` + rule},
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, workspaceRequest(t, http.MethodPost, "/api/v2/organizations/acme/workspaces", tc.body))

		var doc jsonAPIErrorDoc
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || rec.Code != http.StatusUnprocessableEntity ||
			len(doc.Errors) != 1 || doc.Errors[0].Detail != tc.want {
			t.Errorf("create %s answers %d %s, want 422 with the detail %s",
				tc.body, rec.Code, rec.Body.String(), tc.want)
		}
	}
}

// TestCreateWorkspaceDuplicate proves a duplicate name answers 422 with the
// detail go-tfe reads.
func TestCreateWorkspaceDuplicate(t *testing.T) {
	t.Parallel()

	handler := newWorkspaceHandler(t, allowAll())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(
		rec,
		workspaceRequest(t, http.MethodPost, "/api/v2/organizations/acme/workspaces", createBody("web")),
	)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate answers %d, want %d: %s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}

	var doc jsonAPIErrorDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode error body: %v", err)
	}

	if len(doc.Errors) != 1 {
		t.Fatalf("error document holds %d errors, want 1", len(doc.Errors))
	}

	got := doc.Errors[0]

	if got.Status != "422" || got.Title != "invalid attribute" || got.Detail != "Name has already been taken" {
		t.Fatalf("error = %+v, want 422 invalid attribute Name has already been taken", got)
	}

	errcodetest.CheckAnswer(t, errcodetest.ShapeJSONAPI, http.StatusUnprocessableEntity, rec.Body.Bytes(),
		errcodetest.Want{Code: errcode.WorkspaceAlreadyExists, Args: nil, RequestBody: nil})
}

// TestCreateWorkspaceUnknownOrganization proves a creation in an unknown
// organization answers 404.
func TestCreateWorkspaceUnknownOrganization(t *testing.T) {
	t.Parallel()

	handler := newWorkspaceHandler(t, allowAll())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, workspaceRequest(
		t,
		http.MethodPost,
		"/api/v2/organizations/ghost/workspaces",
		createBody("fresh"),
	))

	checkJSONAPIError(t, rec, http.StatusNotFound)
}

// TestListWorkspaces proves the list answers the workspace ordered by name
// with the meta.pagination object go-tfe reads.
func TestListWorkspaces(t *testing.T) {
	t.Parallel()

	handler := newWorkspaceHandler(t, allowAll())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, workspaceRequest(t, http.MethodGet, "/api/v2/organizations/acme/workspaces", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("list answers %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var doc workspaceTestListDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode list body: %v", err)
	}

	if len(doc.Data) != 1 || doc.Data[0].Attributes.Name != "web" {
		t.Fatalf("list holds %+v, want one workspace web", doc.Data)
	}

	page := doc.Meta.Pagination
	if page.CurrentPage != 1 || page.PageSize != 20 || page.TotalPages != 1 || page.TotalCount != 1 {
		t.Fatalf("pagination = %+v, want page 1 of size 20, one page, one entry", page)
	}

	if page.PrevPage != nil || page.NextPage != nil {
		t.Fatalf("pagination = %+v, want null prev and next pages", page)
	}
}

// TestListWorkspacesUnknownOrganization proves a list of an unknown
// organization answers 404.
func TestListWorkspacesUnknownOrganization(t *testing.T) {
	t.Parallel()

	handler := newWorkspaceHandler(t, allowAll())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, workspaceRequest(t, http.MethodGet, "/api/v2/organizations/ghost/workspaces", ""))

	checkJSONAPIError(t, rec, http.StatusNotFound)
}

// pagingWorkspaces serves three workspaces in acme for the list tests: beta
// holds alice's lock, acme and zeta are unlocked, and none has state.
type pagingWorkspaces struct {
	items []workspace.Workspace
}

func (pagingWorkspaces) Delete(_ context.Context, _ workspace.Ref, _ audit.Entry) error {
	return workspace.ErrNotFound
}

func newPagingWorkspaces(t *testing.T) pagingWorkspaces {
	t.Helper()

	ns, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("parse namespace: %v", err)
	}

	at := stubWorkspaceTime()

	locked := workspace.Lock{LockedAt: at, ID: "lock-alice", Holder: "alice", Reason: "apply"}
	unlocked := workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""}

	items := make([]workspace.Workspace, 0, 3)

	for _, tc := range []struct {
		id, name string
		lock     workspace.Lock
	}{
		{id: "ws-acme", name: "acme", lock: unlocked},
		{id: "ws-beta", name: "beta", lock: locked},
		{id: "ws-zeta", name: "zeta", lock: unlocked},
	} {
		name, err := workspace.ParseName(tc.name)
		if err != nil {
			t.Fatalf("parse workspace name %q: %v", tc.name, err)
		}

		items = append(items, workspace.Workspace{
			NamespaceID: "",
			CreatedAt:   at,
			UpdatedAt:   at,
			Lock:        tc.lock,
			LastRelease: workspace.NoRelease(),
			ID:          workspace.ID(tc.id),
			Ref:         workspace.Ref{Namespace: ns, Name: name},
			Revision:    1,
			Overrides: namespace.Overrides{
				Forced:   false,
				Unlocked: false,
			},
			Origin: identity.StateOrigin{Job: "", Commit: ""},
		})
	}

	return pagingWorkspaces{items: items}
}

func (p pagingWorkspaces) Create(
	_ context.Context,
	_ workspace.Ref,
	_ workspace.Placement,
) (workspace.Workspace, error) {
	return workspace.Workspace{}, workspace.ErrAlreadyExists
}

func (p pagingWorkspaces) Get(_ context.Context, _ workspace.Ref) (workspace.Workspace, error) {
	return workspace.Workspace{}, workspace.ErrNotFound
}

func (p pagingWorkspaces) GetByID(_ context.Context, _ workspace.ID) (workspace.Workspace, error) {
	return workspace.Workspace{}, workspace.ErrNotFound
}

func (p pagingWorkspaces) List(_ context.Context, ns namespace.Name) ([]workspace.Workspace, error) {
	if ns.String() != "acme" {
		return nil, nil
	}

	return append([]workspace.Workspace(nil), p.items...), nil
}

func (p pagingWorkspaces) Query(ctx context.Context, q workspace.ListQuery) (workspace.ListPage, error) {
	all, err := p.List(ctx, q.Namespace)
	if err != nil {
		return workspace.ListPage{}, err
	}

	return workspace.PageWorkspaces(all, q), nil
}

func (p pagingWorkspaces) LockForJob(
	_ context.Context, _ workspace.ID, _, _, _ string,
) (workspace.Workspace, error) {
	return workspace.Workspace{}, workspace.ErrNotFound
}

func (p pagingWorkspaces) Unlock(
	_ context.Context, _ workspace.ID, _ string,
) (workspace.Workspace, error) {
	return workspace.Workspace{}, workspace.ErrNotFound
}

//nolint:revive // The signature of the workspace port that the handler reads.
func (p pagingWorkspaces) ForceUnlockLock(
	_ context.Context, _ workspace.ID, _ workspace.Lock, _ string, _ namespace.Overrides, _ func() error,
	_ audit.Entry,
) (workspace.Workspace, bool, error) {
	return workspace.Workspace{}, false, workspace.ErrNotFound
}

func (p pagingWorkspaces) Current(_ context.Context, _ workspace.ID) (workspace.StateVersion, error) {
	return workspace.StateVersion{}, workspace.ErrNoState
}

func (p pagingWorkspaces) BeginState(
	_ context.Context, _ workspace.ID, _ string, _ workspace.NewState, _ workspace.Authority,
) (workspace.PendingState, error) {
	return workspace.PendingState{}, workspace.ErrNotLocked
}

func (p pagingWorkspaces) Upload(
	_ context.Context, _ workspace.StateVersionID, _ token.Token, _ io.Reader,
) (workspace.StateVersion, error) {
	return workspace.StateVersion{}, workspace.ErrUploadDenied
}

func (p pagingWorkspaces) CheckUpload(
	_ context.Context, _ workspace.StateVersionID, _ token.Token,
) (namespace.Name, error) {
	return "acme", nil
}

func (p pagingWorkspaces) CheckJSONRenderingUpload(
	_ context.Context, _ workspace.StateVersionID, _ token.Token,
) (namespace.Name, error) {
	return "acme", nil
}

func (p pagingWorkspaces) DiscardJSONRendering(
	_ context.Context, _ workspace.StateVersionID, _ token.Token, _ io.Reader,
) error {
	return workspace.ErrUploadDenied
}

func (p pagingWorkspaces) ReadState(
	_ context.Context, _ workspace.StateVersionID,
) (workspace.StateVersion, error) {
	return workspace.StateVersion{}, workspace.ErrStateVersionNotFound
}

func (p pagingWorkspaces) ReadResources(
	_ context.Context, _ workspace.StateVersionID, _ string, _ int, _ string,
) (workspace.StatePage, error) {
	return workspace.StatePage{}, workspace.ErrStateVersionNotFound
}

func (p pagingWorkspaces) ReadResource(
	_ context.Context, _ workspace.StateVersionID, _ string,
) (workspace.StateDetail, error) {
	return workspace.StateDetail{}, workspace.ErrStateVersionNotFound
}

func (p pagingWorkspaces) ReadStateAt(
	_ context.Context, _ workspace.ID, _ workspace.StateAtRequest,
) (workspace.StateAtRead, error) {
	return workspace.StateAtRead{}, workspace.ErrNoStateAt
}

func (p pagingWorkspaces) DiffStates(
	_ context.Context, _, _ workspace.StateVersionID, _ string, _ int,
) (workspace.StateDiff, error) {
	return workspace.StateDiff{}, workspace.ErrStateVersionNotFound
}

func (p pagingWorkspaces) Open(_ context.Context, _ workspace.StateVersion) (io.ReadCloser, error) {
	return nil, workspace.ErrStateVersionNotFound
}

func (p pagingWorkspaces) Restore(
	_ context.Context, _ workspace.ID, _ workspace.StateVersionID, _ string,
) (workspace.StateVersion, error) {
	return workspace.StateVersion{}, workspace.ErrStateVersionNotFound
}

func (p pagingWorkspaces) ReadRollbackChange(
	_ context.Context, _ workspace.ID, _ workspace.StateVersionID,
) (workspace.RollbackChange, error) {
	return workspace.RollbackChange{}, workspace.ErrStateVersionNotFound
}

func (p pagingWorkspaces) RestoreExpecting(
	_ context.Context, _ workspace.ID, _ workspace.StateVersionID, _ string, _ workspace.StateVersionID,
) (workspace.StateVersion, error) {
	return workspace.StateVersion{}, workspace.ErrStateVersionNotFound
}

func (p pagingWorkspaces) History(
	_ context.Context, _ workspace.ID, _ workspace.Page,
) ([]workspace.StateVersion, int, error) {
	return nil, 0, nil
}

// newPagingHandler returns a production Handler over three workspaces.
func newPagingHandler(t *testing.T, authz identity.Authorizer) *tfe.Handler {
	t.Helper()

	return newPagingHandlerWith(t, authz, newPagingWorkspaces(t))
}

// newPagingHandlerWith is newPagingHandler over the workspaces ws.
func newPagingHandlerWith(t *testing.T, authz identity.Authorizer, ws tfe.WorkspaceService) *tfe.Handler {
	t.Helper()

	handler, err := tfe.NewHandler(tfe.Config{
		Registry:       nil,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     authz,
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     ws,
		OutputChecker:  nil,
		BaseURL:        baseurl.Resolver{},
		Audit:          newTestAudit(),
		Gate:           nil,
		MaxStateBytes:  0,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		SessionPolicy:  nil,
		StateGuard:     stategate.Off("the test keeps no gates"),
		Placer:         testPlacer(),
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// listNames returns the workspace names of a list document.
func listNames(doc workspaceTestListDoc) []string {
	names := make([]string, 0, len(doc.Data))

	for i := range doc.Data {
		names = append(names, doc.Data[i].Attributes.Name)
	}

	return names
}

// decodeWorkspaceList decodes a workspace list document and fails the test
// when the status is wrong.
func decodeWorkspaceList(t *testing.T, rec *httptest.ResponseRecorder, want int) workspaceTestListDoc {
	t.Helper()

	if rec.Code != want {
		t.Fatalf("list answers %d, want %d: %s", rec.Code, want, rec.Body.String())
	}

	var doc workspaceTestListDoc

	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode list body: %v", err)
	}

	return doc
}

// getListPage requests one list page and decodes it.
func getListPage(t *testing.T, handler *tfe.Handler, target string) workspaceTestListDoc {
	t.Helper()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, workspaceRequest(t, http.MethodGet, target, ""))

	return decodeWorkspaceList(t, rec, http.StatusOK)
}

// checkListPage proves one list page holds the want names.
func checkListPage(t *testing.T, handler *tfe.Handler, target string, want ...string) workspaceTestListDoc {
	t.Helper()

	doc := getListPage(t, handler, target)
	names := listNames(doc)

	if len(names) != len(want) {
		t.Fatalf("page holds %v, want %v", names, want)
	}

	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("page holds %v, want %v", names, want)
		}
	}

	return doc
}

// TestListWorkspacesPagination proves the list pages ordered by name with the
// prev and next pages of the meta.pagination object.
func TestListWorkspacesPagination(t *testing.T) {
	t.Parallel()

	handler := newPagingHandler(t, allowAll())

	t.Run("first page", func(t *testing.T) {
		t.Parallel()

		doc := checkListPage(
			t,
			handler,
			"/api/v2/organizations/acme/workspaces?page[number]=1&page[size]=2",
			"acme",
			"beta",
		)

		page := doc.Meta.Pagination
		if page.CurrentPage != 1 || page.TotalPages != 2 || page.TotalCount != 3 {
			t.Fatalf("pagination = %+v, want page 1 of 2 with 3 entries", page)
		}

		if page.PrevPage != nil || page.NextPage == nil || *page.NextPage != 2 {
			t.Fatalf("pagination = %+v, want null prev and next 2", page)
		}
	})

	t.Run("second page", func(t *testing.T) {
		t.Parallel()

		doc := checkListPage(t, handler, "/api/v2/organizations/acme/workspaces?page[number]=2&page[size]=2", "zeta")

		page := doc.Meta.Pagination
		if page.PrevPage == nil || *page.PrevPage != 1 || page.NextPage != nil {
			t.Fatalf("pagination = %+v, want prev 1 and null next", page)
		}
	})

	t.Run("locked flag follows the lock", func(t *testing.T) {
		t.Parallel()
		checkLockedFlags(t, handler)
	})

	for _, tc := range []struct{ name, target string }{
		{name: "zero size", target: "/api/v2/organizations/acme/workspaces?page[size]=0"},
		{name: "zero number", target: "/api/v2/organizations/acme/workspaces?page[number]=0"},
		{name: "bad size", target: "/api/v2/organizations/acme/workspaces?page[size]=many"},
		{name: "bad number", target: "/api/v2/organizations/acme/workspaces?page[number]=first"},
	} {
		t.Run("invalid page "+tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, workspaceRequest(t, http.MethodGet, tc.target, ""))

			checkJSONAPIError(t, rec, http.StatusUnprocessableEntity)
		})
	}
}

// TestListWorkspacesHugePageValues proves huge page numbers and sizes answer
// 200 with an empty page instead of panicking on overflowed arithmetic.
func TestListWorkspacesHugePageValues(t *testing.T) {
	t.Parallel()

	handler := newPagingHandler(t, allowAll())

	huge := strconv.Itoa(int(^uint(0) >> 1))

	for _, tc := range []struct{ name, target string }{
		{name: "huge number and size", target: "/api/v2/organizations/acme/workspaces?page[number]=3&page[size]=" + huge},
		{name: "huge number", target: "/api/v2/organizations/acme/workspaces?page[number]=" + huge + "&page[size]=2"},
		{name: "huge size", target: "/api/v2/organizations/acme/workspaces?page[number]=2&page[size]=" + huge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			doc := getListPage(t, handler, tc.target)

			if len(doc.Data) != 0 {
				t.Fatalf("page holds %v, want no workspaces", listNames(doc))
			}
		})
	}

	t.Run("huge size first page holds every workspace", func(t *testing.T) {
		t.Parallel()

		checkListPage(
			t,
			handler,
			"/api/v2/organizations/acme/workspaces?page[number]=1&page[size]="+huge,
			"acme",
			"beta",
			"zeta",
		)
	})
}

// checkLockedFlags proves the locked flag follows the lock: only beta is
// locked.
func checkLockedFlags(t *testing.T, handler *tfe.Handler) {
	t.Helper()

	doc := getListPage(t, handler, "/api/v2/organizations/acme/workspaces")

	locked := map[string]bool{}
	for i := range doc.Data {
		locked[doc.Data[i].Attributes.Name] = doc.Data[i].Attributes.Locked
	}

	if !locked["beta"] || locked["acme"] || locked["zeta"] {
		t.Fatalf("locked flags = %v, want only beta locked", locked)
	}
}

// checkLockedWorkspace proves one locked workspace shows the lock view: the
// holder, the time as RFC 3339 and the reason. Every other attribute stays.
func checkLockedWorkspace(t *testing.T, got workspaceTestResource) {
	t.Helper()

	attrs := got.Attributes

	if !attrs.Locked {
		t.Fatal("beta locked = false, want true")
	}

	if attrs.LockHolder != "alice" {
		t.Fatalf("beta lock-holder = %q, want %q", attrs.LockHolder, "alice")
	}

	if attrs.LockReason != "apply" {
		t.Fatalf("beta lock-reason = %q, want %q", attrs.LockReason, "apply")
	}

	wantAt := stubWorkspaceTime().UTC().Format(time.RFC3339)
	if attrs.LockedAt != wantAt {
		t.Fatalf("beta locked-at = %q, want %q", attrs.LockedAt, wantAt)
	}

	if _, err := time.Parse(time.RFC3339, attrs.LockedAt); err != nil {
		t.Fatalf("beta locked-at %q is not RFC 3339: %v", attrs.LockedAt, err)
	}

	if attrs.Name != "beta" || attrs.ExecutionMode != "local" || attrs.AutoApply {
		t.Fatalf(
			"beta attributes = %+v, want name beta, execution-mode local, auto-apply false",
			attrs,
		)
	}
}

// checkUnlockedWorkspace proves one unlocked workspace shows an empty lock
// view.
func checkUnlockedWorkspace(t *testing.T, name string, got workspaceTestResource) {
	t.Helper()

	attrs := got.Attributes

	if attrs.Locked {
		t.Fatalf("%s locked = true, want false", name)
	}

	if attrs.LockHolder != "" || attrs.LockedAt != "" || attrs.LockReason != "" {
		t.Fatalf(
			"%s lock view = %q %q %q, want empty",
			name,
			attrs.LockHolder,
			attrs.LockedAt,
			attrs.LockReason,
		)
	}
}

// TestWorkspaceLockAttributes proves the resource writer shows the lock view:
// a locked workspace shows the holder, the time as RFC 3339 and the reason,
// while an unlocked workspace shows empty values. Every other attribute stays.
func TestWorkspaceLockAttributes(t *testing.T) {
	t.Parallel()

	handler := newPagingHandler(t, allowAll())

	doc := getListPage(t, handler, "/api/v2/organizations/acme/workspaces")

	byName := map[string]workspaceTestResource{}
	for i := range doc.Data {
		byName[doc.Data[i].Attributes.Name] = doc.Data[i]
	}

	beta, ok := byName["beta"]
	if !ok {
		t.Fatal("list holds no workspace beta")
	}

	checkLockedWorkspace(t, beta)

	for _, name := range []string{"acme", "zeta"} {
		item, ok := byName[name]
		if !ok {
			t.Fatalf("list holds no workspace %s", name)
		}

		checkUnlockedWorkspace(t, name, item)
	}
}

// TestListWorkspacesSearchName proves search[name] keeps the workspaces
// whose name holds the value: a match lists them, no match lists none, and an
// empty value lists every workspace.
func TestListWorkspacesSearchName(t *testing.T) {
	t.Parallel()

	handler := newPagingHandler(t, allowAll())

	t.Run("match", func(t *testing.T) {
		t.Parallel()

		doc := checkListPage(t, handler, "/api/v2/organizations/acme/workspaces?search[name]=et", "beta", "zeta")

		if doc.Meta.Pagination.TotalCount != 2 {
			t.Fatalf("total count = %d, want 2", doc.Meta.Pagination.TotalCount)
		}
	})

	t.Run("no match", func(t *testing.T) {
		t.Parallel()

		doc := getListPage(t, handler, "/api/v2/organizations/acme/workspaces?search[name]=zzz")

		if len(doc.Data) != 0 {
			t.Fatalf("page holds %v, want no workspaces", listNames(doc))
		}

		if doc.Meta.Pagination.TotalCount != 0 {
			t.Fatalf("total count = %d, want 0", doc.Meta.Pagination.TotalCount)
		}
	})

	t.Run("empty value", func(t *testing.T) {
		t.Parallel()

		checkListPage(
			t,
			handler,
			"/api/v2/organizations/acme/workspaces?search[name]=",
			"acme",
			"beta",
			"zeta",
		)
	})
}

// TestWorkspacePermissionFlags proves each flag follows the Authorizer result
// for the caller: without unit:write the lock and queue-run flags are false
// while force-unlock stays true, and without unit:write-forced only
// force-unlock is false. Without unit:manage, force-unlock stays true.
func TestWorkspacePermissionFlags(t *testing.T) {
	t.Parallel()

	t.Run("without write", func(t *testing.T) {
		t.Parallel()

		handler := newWorkspaceHandler(
			t,
			selectiveAuthorizer{deny: map[identity.Action]bool{identity.ActionUnitWrite: true}},
		)

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, workspaceRequest(t, http.MethodGet, "/api/v2/organizations/acme/workspaces/web", ""))

		doc := decodeWorkspaceDoc(t, rec, http.StatusOK)
		perms := doc.Data.Attributes.Permissions

		if perms.CanLock || perms.CanUnlock || perms.CanQueueRun {
			t.Fatalf("permissions = %+v, want lock, unlock and queue-run false", perms)
		}

		if !perms.CanForceUnlock {
			t.Fatalf("permissions = %+v, want force-unlock true", perms)
		}
	})

	t.Run("without manage", func(t *testing.T) {
		t.Parallel()

		handler := newWorkspaceHandler(
			t,
			selectiveAuthorizer{deny: map[identity.Action]bool{identity.ActionUnitManage: true}},
		)

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, workspaceRequest(t, http.MethodGet, "/api/v2/organizations/acme/workspaces/web", ""))

		doc := decodeWorkspaceDoc(t, rec, http.StatusOK)
		if perms := doc.Data.Attributes.Permissions; !perms.CanForceUnlock {
			t.Fatalf("permissions = %+v, want force-unlock true: it follows unit:write-forced", perms)
		}
	})

	t.Run("without write-forced", func(t *testing.T) {
		t.Parallel()

		handler := newWorkspaceHandler(
			t,
			selectiveAuthorizer{deny: map[identity.Action]bool{identity.ActionUnitWriteForced: true}},
		)

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, workspaceRequest(t, http.MethodGet, "/api/v2/organizations/acme/workspaces/web", ""))

		doc := decodeWorkspaceDoc(t, rec, http.StatusOK)
		perms := doc.Data.Attributes.Permissions

		if !perms.CanLock || !perms.CanUnlock || !perms.CanQueueRun {
			t.Fatalf("permissions = %+v, want lock, unlock and queue-run true", perms)
		}

		if perms.CanForceUnlock {
			t.Fatalf("permissions = %+v, want force-unlock false", perms)
		}
	})
}

// TestListPageErrorsNameTheParameter proves that a page value that is not a
// whole number from 1 to the largest int answers 422 with a detail that names
// the parameter and its rule, and does not repeat the value.
func TestListPageErrorsNameTheParameter(t *testing.T) {
	t.Parallel()

	handler := newPagingHandler(t, allowAll())

	rule := " must be a whole number from 1 to " + strconv.Itoa(math.MaxInt)
	size := "invalid query parameter: page[size]" + rule
	number := "invalid query parameter: page[number]" + rule

	for _, tc := range []struct{ query, want string }{
		{query: "page[size]=0", want: size},
		{query: "page[size]=abc", want: size},
		{query: "page[number]=0", want: number},
		{query: "page[number]=-3", want: number},
		{query: "page[number]=99999999999999999999", want: number},
	} {
		rec := httptest.NewRecorder()
		target := "/api/v2/organizations/acme/workspaces?" + tc.query
		handler.ServeHTTP(rec, workspaceRequest(t, http.MethodGet, target, ""))

		var doc jsonAPIErrorDoc
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || rec.Code != http.StatusUnprocessableEntity ||
			len(doc.Errors) != 1 || doc.Errors[0].Detail != tc.want {
			t.Errorf("%s: answer %d %s, want 422 with the detail %q", tc.query, rec.Code, rec.Body.String(), tc.want)
		}
	}
}
