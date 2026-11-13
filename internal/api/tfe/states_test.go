package tfe_test

import (
	"context"
	"crypto/md5" //nolint:gosec // The test recomputes the MD5 the protocol uses for integrity.
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// stateTestAttributes decodes the attributes of a state version document in
// tests.
type stateTestAttributes struct {
	CreatedAt          time.Time `json:"created-at"`                //nolint:tagliatelle // The TFE wire name; it must not change.
	DownloadURL        string    `json:"hosted-state-download-url"` //nolint:tagliatelle // The TFE wire name; it must not change.
	Status             string    `json:"status"`
	Serial             int64     `json:"serial"`
	Lineage            string    `json:"lineage"`
	MD5                string    `json:"md5"`
	Size               int64     `json:"size"`
	ResourcesProcessed bool      `json:"resources-processed"` //nolint:tagliatelle // The TFE wire name; it must not change.
	TerraformVersion   string    `json:"terraform-version"`   //nolint:tagliatelle // The TFE wire name; it must not change.
}

// stateTestRelationTarget decodes one relationship target in tests.
type stateTestRelationTarget struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// stateTestRelationList decodes one relationship with several targets in
// tests: the outputs of a state version.
type stateTestRelationList struct {
	Data []stateTestRelationTarget `json:"data"`
}

// stateTestResource decodes one state version resource in tests.
type stateTestResource struct {
	ID            string                           `json:"id"`
	Type          string                           `json:"type"`
	Attributes    stateTestAttributes              `json:"attributes"`
	Relationships map[string]stateTestRelationList `json:"relationships"`
	Links         map[string]string                `json:"links"`
}

// stateTestDoc decodes a state version document in tests.
type stateTestDoc struct {
	Data stateTestResource `json:"data"`
}

// stateOutputTestAttributes decodes the attributes of an output in tests.
type stateOutputTestAttributes struct {
	Name         string `json:"name"`
	Sensitive    bool   `json:"sensitive"`
	Type         string `json:"type"`
	Value        any    `json:"value"`
	DetailedType any    `json:"detailed-type"` //nolint:tagliatelle // The TFE wire name; it must not change.
}

// stateOutputTestResource decodes one output resource in tests.
type stateOutputTestResource struct {
	ID         string                    `json:"id"`
	Type       string                    `json:"type"`
	Attributes stateOutputTestAttributes `json:"attributes"`
	Links      map[string]string         `json:"links"`
}

// stateOutputTestListDoc decodes an outputs list document in tests.
type stateOutputTestListDoc struct {
	Data []stateOutputTestResource `json:"data"`
	Meta workspaceTestListMeta     `json:"meta"`
}

// stateOutputTestDoc decodes a single output document in tests.
type stateOutputTestDoc struct {
	Data stateOutputTestResource `json:"data"`
}

// decodeStateDoc decodes a state version document and fails the test when
// the status or shape is wrong.
func decodeStateDoc(t *testing.T, rec *httptest.ResponseRecorder, want int) stateTestDoc {
	t.Helper()

	if rec.Code != want {
		t.Fatalf("request answers %d, want %d: %s", rec.Code, want, rec.Body.String())
	}

	if got := rec.Header().Get("Content-Type"); got != "application/vnd.api+json" {
		t.Fatalf("media type = %q, want %q", got, "application/vnd.api+json")
	}

	var doc stateTestDoc

	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode state body: %v", err)
	}

	return doc
}

// decodeOutputList decodes an outputs list document and fails the test when
// the status is wrong.
func decodeOutputList(t *testing.T, rec *httptest.ResponseRecorder, want int) stateOutputTestListDoc {
	t.Helper()

	if rec.Code != want {
		t.Fatalf("request answers %d, want %d: %s", rec.Code, want, rec.Body.String())
	}

	var doc stateOutputTestListDoc

	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode outputs body: %v", err)
	}

	return doc
}

// decodeOutputDoc decodes a single output document and fails the test when
// the status is wrong.
func decodeOutputDoc(t *testing.T, rec *httptest.ResponseRecorder, want int) stateOutputTestDoc {
	t.Helper()

	if rec.Code != want {
		t.Fatalf("request answers %d, want %d: %s", rec.Code, want, rec.Body.String())
	}

	var doc stateOutputTestDoc

	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode output body: %v", err)
	}

	return doc
}

// stateRequest builds an authenticated GET request for a state route.
func stateRequest(t *testing.T, target string) *http.Request {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	req.Header.Set("Authorization", "Bearer test-token")

	return req
}

// newStateHandler returns a production Handler over the stub workspaces with
// their seeded state, with authz as the authorizer.
func newStateHandler(t *testing.T, authz identity.Authorizer) *tfe.Handler {
	t.Helper()

	return newWorkspaceHandler(t, authz)
}

// newStateHandlerWithBaseURL returns a production Handler over the stub
// workspaces with the base URL resolver for absolute URLs.
func newStateHandlerWithBaseURL(t *testing.T, base baseurl.Resolver) *tfe.Handler {
	t.Helper()

	handler, err := tfe.NewHandler(tfe.Config{
		Registry:       nil,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     allowAll(),
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     stubWorkspaces{},
		OutputChecker:  nil,
		BaseURL:        base,
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

// TestStateRoutesDeclareActionsAndObjects proves each state route declares
// its actions and resolves the unit opentofu:acme/web. Reads need unit:read,
// except the two outputs routes, which need unit:read-outputs. The download
// also needs unit:read-secrets, as it answers plain secret values.
func TestStateRoutesDeclareActionsAndObjects(t *testing.T) {
	t.Parallel()

	handler := newStateHandler(t, allowAll())

	cases := []workspaceRouteCase{
		{
			name:    "state version list",
			pattern: "GET /api/v2/state-versions",
			method:  http.MethodGet,
			target:  "/api/v2/state-versions?filter[organization][name]=acme&filter[workspace][name]=web",
			body:    "",
			actions: []identity.Action{identity.ActionUnitRead},
			object:  "opentofu:acme/web",
			isUnit:  true,
		},
		{
			name:    "current state version",
			pattern: "GET /api/v2/workspaces/{id}/current-state-version",
			method:  http.MethodGet,
			target:  "/api/v2/workspaces/ws-test123/current-state-version",
			body:    "",
			actions: []identity.Action{identity.ActionUnitRead},
			object:  "opentofu:acme/web",
			isUnit:  true,
		},
		{
			name:    "state version read",
			pattern: "GET /api/v2/state-versions/{state_version_id}",
			method:  http.MethodGet,
			target:  "/api/v2/state-versions/sv-test123",
			body:    "",
			actions: []identity.Action{identity.ActionUnitRead},
			object:  "opentofu:acme/web",
			isUnit:  true,
		},
		{
			name:    "state download",
			pattern: "GET /api/v2/state-versions/{state_version_id}/download",
			method:  http.MethodGet,
			target:  "/api/v2/state-versions/sv-test123/download",
			body:    "",
			actions: []identity.Action{identity.ActionUnitRead, identity.ActionUnitReadSecrets},
			object:  "opentofu:acme/web",
			isUnit:  true,
		},
		{
			name:    "current state version outputs",
			pattern: "GET /api/v2/workspaces/{id}/current-state-version-outputs",
			method:  http.MethodGet,
			target:  "/api/v2/workspaces/ws-test123/current-state-version-outputs",
			body:    "",
			actions: []identity.Action{identity.ActionUnitReadOutputs},
			object:  "opentofu:acme/web",
			isUnit:  true,
		},
		{
			name:    "state version output read",
			pattern: "GET /api/v2/state-version-outputs/{output_id}",
			method:  http.MethodGet,
			target:  "/api/v2/state-version-outputs/sv-test123.greeting",
			body:    "",
			actions: []identity.Action{identity.ActionUnitReadOutputs},
			object:  "opentofu:acme/web",
			isUnit:  true,
		},
		{
			name:    "masked resource list",
			pattern: "GET /api/v2/state-versions/{state_version_id}/resources",
			method:  http.MethodGet,
			target:  "/api/v2/state-versions/sv-test123/resources",
			body:    "",
			actions: []identity.Action{identity.ActionUnitRead},
			object:  "opentofu:acme/web",
			isUnit:  true,
		},
		{
			name:    "masked state at a time",
			pattern: "GET /api/v2/workspaces/{workspace_id}/state-at/{at}",
			method:  http.MethodGet,
			target:  "/api/v2/workspaces/ws-test123/state-at/1500",
			body:    "",
			actions: []identity.Action{identity.ActionUnitRead},
			object:  "opentofu:acme/web",
			isUnit:  true,
		},
		{
			name:    "one masked resource",
			pattern: "GET /api/v2/state-versions/{state_version_id}/resources/{address}",
			method:  http.MethodGet,
			target:  "/api/v2/state-versions/sv-test123/resources/terraform_data.smoke",
			body:    "",
			actions: []identity.Action{identity.ActionUnitRead},
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

// checkStateDoc proves one state version document holds the stub version:
// id sv-test123, the serial, lineage, MD5 and size of the stub content, the
// finalized status, an absolute download URL under the request host and the
// outputs relationship with the greeting output.
func checkStateDoc(t *testing.T, doc stateTestDoc, host string) {
	t.Helper()

	got := doc.Data

	if got.ID != "sv-test123" || got.Type != "state-versions" {
		t.Fatalf("state version = %q %q, want id sv-test123 type state-versions", got.ID, got.Type)
	}

	checkStateAttributes(t, got.Attributes)
	checkStateDownload(t, got.Attributes, host)
	checkStateOutputsRelation(t, got.Relationships)

	if got.Links["self"] != "/api/v2/state-versions/sv-test123" {
		t.Fatalf("links = %+v, want self /api/v2/state-versions/sv-test123", got.Links)
	}
}

// checkStateAttributes proves the attributes hold the serial, lineage, MD5
// and size of the stub content with the finalized status.
func checkStateAttributes(t *testing.T, attrs stateTestAttributes) {
	t.Helper()

	content := stubStateContent()
	sum := md5.Sum([]byte(content)) //nolint:gosec // The test recomputes the MD5 the protocol uses.

	if attrs.Serial != 3 || attrs.Lineage != "seed-lineage" || attrs.MD5 != hex.EncodeToString(sum[:]) ||
		attrs.Size != int64(len(content)) {
		t.Fatalf("attributes = %+v, want serial 3 of the seed lineage with the MD5 and size of the content", attrs)
	}

	if attrs.Status != "finalized" || !attrs.ResourcesProcessed {
		t.Fatalf("attributes = %+v, want status finalized and resources processed", attrs)
	}
}

// checkStateDownload proves the download URL is absolute under host.
func checkStateDownload(t *testing.T, attrs stateTestAttributes, host string) {
	t.Helper()

	want := "http://" + host + "/api/v2/state-versions/sv-test123/download"
	if attrs.DownloadURL != want {
		t.Fatalf("download URL = %q, want %q", attrs.DownloadURL, want)
	}
}

// checkStateOutputsRelation proves the outputs relationship holds the
// greeting output.
func checkStateOutputsRelation(t *testing.T, relations map[string]stateTestRelationList) {
	t.Helper()

	outputs, ok := relations["outputs"]
	if !ok {
		t.Fatal("document holds no outputs relationship")
	}

	if len(outputs.Data) != 1 || outputs.Data[0].ID != "sv-test123.greeting" ||
		outputs.Data[0].Type != "state-version-outputs" {
		t.Fatalf("outputs relationship = %+v, want the greeting output", outputs.Data)
	}
}

// TestReadCurrentStateVersion proves the current state route answers the
// stub version document.
func TestReadCurrentStateVersion(t *testing.T) {
	t.Parallel()

	handler := newStateHandler(t, allowAll())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, stateRequest(t, "/api/v2/workspaces/ws-test123/current-state-version"))

	checkStateDoc(t, decodeStateDoc(t, rec, http.StatusOK), "example.com")
}

// TestReadCurrentStateVersionUnknown proves a workspace without state and an
// unknown workspace answer 404 as a JSON:API error.
func TestReadCurrentStateVersionUnknown(t *testing.T) {
	t.Parallel()

	handler := newStateHandler(t, allowAll())

	for _, target := range []string{
		"/api/v2/workspaces/ws-empty123/current-state-version",
		"/api/v2/workspaces/ws-missing/current-state-version",
	} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, stateRequest(t, target))

			checkJSONAPIError(t, rec, http.StatusNotFound)
		})
	}
}

// TestReadStateVersion proves the state version route answers the stub
// version document.
func TestReadStateVersion(t *testing.T) {
	t.Parallel()

	handler := newStateHandler(t, allowAll())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, stateRequest(t, "/api/v2/state-versions/sv-test123"))

	checkStateDoc(t, decodeStateDoc(t, rec, http.StatusOK), "example.com")
}

// TestReadStateVersionUnknown proves an unknown state version answers 404.
func TestReadStateVersionUnknown(t *testing.T) {
	t.Parallel()

	handler := newStateHandler(t, allowAll())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, stateRequest(t, "/api/v2/state-versions/sv-missing"))

	checkJSONAPIError(t, rec, http.StatusNotFound)
}

// TestPendingStateVersionNeverReadable proves a version that never finalized
// answers 404 on the read, the download and the outputs routes: it is never
// current or readable.
func TestPendingStateVersionNeverReadable(t *testing.T) {
	t.Parallel()

	handler := newStateHandler(t, allowAll())

	for _, target := range []string{
		"/api/v2/state-versions/sv-pending123",
		"/api/v2/state-versions/sv-pending123/download",
		"/api/v2/state-version-outputs/sv-pending123.greeting",
	} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, stateRequest(t, target))

			checkJSONAPIError(t, rec, http.StatusNotFound)
		})
	}
}

// TestDownloadState proves the download route streams the stub content.
func TestDownloadState(t *testing.T) {
	t.Parallel()

	handler := newStateHandler(t, allowAll())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, stateRequest(t, "/api/v2/state-versions/sv-test123/download"))

	if rec.Code != http.StatusOK {
		t.Fatalf("download answers %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("media type = %q, want %q", got, "application/json")
	}

	if rec.Body.String() != stubStateContent() {
		t.Fatalf("download = %q, want the stub state", rec.Body.String())
	}
}

// TestDownloadStateUnknown proves an unknown state version answers 404 on
// the download route.
func TestDownloadStateUnknown(t *testing.T) {
	t.Parallel()

	handler := newStateHandler(t, allowAll())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, stateRequest(t, "/api/v2/state-versions/sv-missing/download"))

	checkJSONAPIError(t, rec, http.StatusNotFound)
}

// checkGreetingOutput proves one output resource is the greeting output of
// the stub state.
func checkGreetingOutput(t *testing.T, got stateOutputTestResource) {
	t.Helper()

	if got.ID != "sv-test123.greeting" || got.Type != "state-version-outputs" {
		t.Fatalf("output = %q %q, want sv-test123.greeting of type state-version-outputs", got.ID, got.Type)
	}

	attrs := got.Attributes
	if attrs.Name != "greeting" || attrs.Sensitive || attrs.Type != "string" {
		t.Fatalf("attributes = %+v, want greeting, not sensitive, type string", attrs)
	}

	if value, ok := attrs.Value.(string); !ok || value != "hello" {
		t.Fatalf("value = %v, want hello", attrs.Value)
	}

	if detailed, ok := attrs.DetailedType.(string); !ok || detailed != "string" {
		t.Fatalf("detailed type = %v, want string", attrs.DetailedType)
	}

	if got.Links["self"] != "/api/v2/state-version-outputs/sv-test123.greeting" {
		t.Fatalf("links = %+v, want the output self link", got.Links)
	}
}

// TestReadCurrentStateVersionOutputs proves the current outputs route answers
// the greeting output with the pagination object.
func TestReadCurrentStateVersionOutputs(t *testing.T) {
	t.Parallel()

	handler := newStateHandler(t, allowAll())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(
		rec,
		stateRequest(t, "/api/v2/workspaces/ws-test123/current-state-version-outputs"),
	)

	doc := decodeOutputList(t, rec, http.StatusOK)

	if len(doc.Data) != 1 {
		t.Fatalf("outputs hold %d entries, want the greeting output", len(doc.Data))
	}

	checkGreetingOutput(t, doc.Data[0])

	page := doc.Meta.Pagination
	if page.CurrentPage != 1 || page.TotalCount != 1 || page.TotalPages != 1 {
		t.Fatalf("pagination = %+v, want page 1 of one page with one entry", page)
	}

	if page.PrevPage != nil || page.NextPage != nil {
		t.Fatalf("pagination = %+v, want null prev and next pages", page)
	}
}

// TestReadCurrentStateVersionOutputsUnknown proves a workspace without state
// and an unknown workspace answer 404 on the outputs route.
func TestReadCurrentStateVersionOutputsUnknown(t *testing.T) {
	t.Parallel()

	handler := newStateHandler(t, allowAll())

	for _, target := range []string{
		"/api/v2/workspaces/ws-empty123/current-state-version-outputs",
		"/api/v2/workspaces/ws-missing/current-state-version-outputs",
	} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, stateRequest(t, target))

			checkJSONAPIError(t, rec, http.StatusNotFound)
		})
	}
}

// TestReadStateVersionOutput proves the single output route answers the
// greeting output.
func TestReadStateVersionOutput(t *testing.T) {
	t.Parallel()

	handler := newStateHandler(t, allowAll())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, stateRequest(t, "/api/v2/state-version-outputs/sv-test123.greeting"))

	checkGreetingOutput(t, decodeOutputDoc(t, rec, http.StatusOK).Data)
}

// TestReadStateVersionOutputUnknown proves an unknown output, an unknown
// state version and a malformed output ID answer 404.
func TestReadStateVersionOutputUnknown(t *testing.T) {
	t.Parallel()

	handler := newStateHandler(t, allowAll())

	for _, target := range []string{
		"/api/v2/state-version-outputs/sv-test123.unknown",
		"/api/v2/state-version-outputs/sv-missing.greeting",
		"/api/v2/state-version-outputs/not-an-output-id",
	} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, stateRequest(t, target))

			checkJSONAPIError(t, rec, http.StatusNotFound)
		})
	}
}

// TestStateDownloadUsesPublicURL proves the state document reports the
// download URL under the public URL when one is set.
func TestStateDownloadUsesPublicURL(t *testing.T) {
	t.Parallel()

	public, err := baseurl.Parse("https://dustpan.example")
	if err != nil {
		t.Fatalf("parse public URL: %v", err)
	}

	handler := newStateHandlerWithBaseURL(t, public)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, stateRequest(t, "/api/v2/workspaces/ws-test123/current-state-version"))

	doc := decodeStateDoc(t, rec, http.StatusOK)

	want := "https://dustpan.example/api/v2/state-versions/sv-test123/download"
	if doc.Data.Attributes.DownloadURL != want {
		t.Fatalf("download URL = %q, want %q", doc.Data.Attributes.DownloadURL, want)
	}

	if !strings.HasPrefix(doc.Data.Attributes.DownloadURL, "https://dustpan.example/") {
		t.Fatalf("download URL = %q, want the public host", doc.Data.Attributes.DownloadURL)
	}
}

// TestStateVersionListDeclaresActionAndObject proves the list route declares
// unit:read and resolves the filtered workspace to opentofu:acme/web.
func TestStateVersionListDeclaresActionAndObject(t *testing.T) {
	t.Parallel()

	handler := newStateHandler(t, allowAll())

	const pattern = "GET /api/v2/state-versions"

	var found *tfe.Route

	for _, route := range handler.Routes() {
		if route.Method+" "+route.Path == pattern {
			found = &route

			break
		}
	}

	if found == nil {
		t.Fatalf("route %q is not declared", pattern)
	}

	if len(found.Actions) != 1 || found.Actions[0] != identity.ActionUnitRead {
		t.Fatalf("route %q actions = %v, want [unit:read]", pattern, found.Actions)
	}

	if found.Resolve == nil {
		t.Fatalf("route %q declares no resolver", pattern)
	}

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/api/v2/state-versions?filter[organization][name]=acme&filter[workspace][name]=web",
		http.NoBody,
	)

	obj, err := found.Resolve(httptest.NewRecorder(), req)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if obj.String() != "opentofu:acme/web" || !obj.IsUnit() {
		t.Fatalf("object = %q (unit %v), want unit %q", obj.String(), obj.IsUnit(), "opentofu:acme/web")
	}
}

// stateListTestAttributes decodes the added attributes of a listed state
// version in tests.
type stateListTestAttributes struct {
	Holder       string `json:"holder"`
	Force        bool   `json:"force"`
	RestoredFrom string `json:"restored-from"`  //nolint:tagliatelle // The TFE wire name; it must not change.
	FenceBy      string `json:"fence-by"`       //nolint:tagliatelle // The TFE wire name; it must not change.
	Unlocked     bool   `json:"unlocked-write"` //nolint:tagliatelle // The TFE wire name; it must not change.
}

// stateListTestResource decodes one listed state version in tests.
type stateListTestResource struct {
	ID         string                  `json:"id"`
	Type       string                  `json:"type"`
	Attributes stateListTestAttributes `json:"attributes"`
}

// stateListTestDoc decodes a state version list document in tests.
type stateListTestDoc struct {
	Data []stateListTestResource `json:"data"`
	Meta workspaceTestListMeta   `json:"meta"`
}

// decodeStateList decodes a state version list document and fails the test
// when the status is not 200.
func decodeStateList(t *testing.T, rec *httptest.ResponseRecorder) stateListTestDoc {
	t.Helper()

	if rec.Code != http.StatusOK {
		t.Fatalf("list answers %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	if got := rec.Header().Get("Content-Type"); got != "application/vnd.api+json" {
		t.Fatalf("media type = %q, want %q", got, "application/vnd.api+json")
	}

	var doc stateListTestDoc

	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode list body: %v", err)
	}

	return doc
}

// listStateWorkspaces serves fixed finalized versions of ws-test123, newest
// first, with paging like the store: a non-positive limit reads as the
// default size, a large limit is capped and a negative offset reads as zero.
type listStateWorkspaces struct {
	stubWorkspaces

	versions []workspace.StateVersion
}

func (l listStateWorkspaces) History(
	_ context.Context, id workspace.ID, page workspace.Page,
) ([]workspace.StateVersion, int, error) {
	if id.String() != "ws-test123" {
		return nil, 0, nil
	}

	limit := page.Limit
	if limit <= 0 {
		limit = workspace.DefaultStatePageSize
	}

	limit = min(limit, workspace.MaxStatePageSize)
	offset := max(page.Offset, 0)

	total := len(l.versions)

	if offset >= total {
		return nil, total, nil
	}

	end := min(offset+limit, total)

	return append([]workspace.StateVersion(nil), l.versions[offset:end]...), total, nil
}

// newListStateHandler returns a production Handler over three finalized state
// versions of ws-test123, newest first: sv-3, sv-2 and sv-1. The second is
// forced and the third restores the first, so the tests prove the added
// attributes.
func newListStateHandler(t *testing.T) *tfe.Handler {
	t.Helper()

	at := stubWorkspaceTime()
	versions := make([]workspace.StateVersion, 0, 3)

	for i, tc := range []struct {
		id, holder, restored, fenceBy string
		force, unlockedWrite          bool
		serial                        int64
	}{
		{id: "sv-3", holder: "", restored: "", fenceBy: "", force: false, unlockedWrite: true, serial: 5},
		{id: "sv-2", holder: "", restored: "", fenceBy: "alice", force: false, unlockedWrite: false, serial: 4},
		{id: "sv-1", holder: "tester", restored: "sv-0", fenceBy: "", force: true, unlockedWrite: false, serial: 3},
	} {
		created := at.Add(time.Duration(i) * time.Minute)
		versions = append(versions, workspace.StateVersion{
			CreatedAt:       created,
			FinalizedAt:     created,
			UploadExpiresAt: created,
			Upload:          token.Digest{},
			ID:              workspace.StateVersionID(tc.id),
			Workspace:       workspace.ID("ws-test123"),
			Holder:          tc.holder,
			LockID:          "lock-test",
			Lineage:         "seed-lineage",
			Status:          workspace.StatusFinalized,
			Content:         blob.Key("namespaces/ns-aaaaaaaaaaaaaaaaaaaa/tofu/ws-test123/" + tc.id + "/seed"),
			Outputs:         []byte(stubStateOutputs()),
			MD5:             [md5.Size]byte{},
			Serial:          tc.serial,
			Size:            10,
			Force:           tc.force,
			Sealed:          false,
			RestoredFrom:    workspace.StateVersionID(tc.restored),
			SensitiveMarks:  nil,
			FenceBy:         tc.fenceBy,
			UnlockedWrite:   tc.unlockedWrite, Origin: identity.StateOrigin{Job: "", Commit: ""},
		})
	}

	handler, err := tfe.NewHandler(tfe.Config{
		Registry:       nil,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     allowAll(),
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     listStateWorkspaces{stubWorkspaces: stubWorkspaces{}, versions: versions},
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

// listTarget builds a state version list target with the go-tfe query shape:
// filter[organization][name], filter[workspace][name], page[number] and
// page[size].
func listTarget(query string) string {
	target := "/api/v2/state-versions?filter[organization][name]=acme&filter[workspace][name]=web"
	if query != "" {
		target += "&" + query
	}

	return target
}

// wantListPage holds the IDs and pagination one list page must hold.
type wantListPage struct {
	ids    []string
	number int
	size   int
	total  int
	pages  int
}

// checkListIDs proves a list document holds the want IDs in order with the
// want pagination.
func checkListIDs(t *testing.T, doc stateListTestDoc, want wantListPage) {
	t.Helper()

	if len(doc.Data) != len(want.ids) {
		t.Fatalf("list holds %d entries, want %d (%v)", len(doc.Data), len(want.ids), want.ids)
	}

	for i := range want.ids {
		if doc.Data[i].ID != want.ids[i] || doc.Data[i].Type != "state-versions" {
			t.Fatalf(
				"entry %d = %q %q, want %q of type state-versions",
				i,
				doc.Data[i].ID,
				doc.Data[i].Type,
				want.ids[i],
			)
		}
	}

	page := doc.Meta.Pagination
	if page.CurrentPage != want.number || page.PageSize != want.size ||
		page.TotalCount != want.total || page.TotalPages != want.pages {
		t.Fatalf("pagination = %+v, want page %d of size %d with %d entries in %d pages",
			page, want.number, want.size, want.total, want.pages)
	}
}

// TestListStateVersions proves the list answers the stub version with the
// holder, force and restored-from attributes and the pagination object.
func TestListStateVersions(t *testing.T) {
	t.Parallel()

	handler := newStateHandler(t, allowAll())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, stateRequest(t, listTarget("")))

	doc := decodeStateList(t, rec)

	if len(doc.Data) != 1 || doc.Data[0].ID != "sv-test123" {
		t.Fatalf("list holds %+v, want one entry sv-test123", doc.Data)
	}

	got := doc.Data[0].Attributes

	if got.Holder != "tester" || got.Force || got.RestoredFrom != "" {
		t.Fatalf("attributes = %+v, want holder tester, force false, no restored-from", got)
	}

	checkListIDs(t, doc, wantListPage{ids: []string{"sv-test123"}, number: 1, size: 20, total: 1, pages: 1})
}

// TestListStateVersionsPaging proves the list is newest first and respects
// page[size] and page[number].
func TestListStateVersionsPaging(t *testing.T) {
	t.Parallel()

	handler := newListStateHandler(t)

	t.Run("first page", func(t *testing.T) {
		t.Parallel()

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, stateRequest(t, listTarget("page[number]=1&page[size]=2")))

		doc := decodeStateList(t, rec)
		checkListIDs(t, doc, wantListPage{ids: []string{"sv-3", "sv-2"}, number: 1, size: 2, total: 3, pages: 2})

		if page := doc.Meta.Pagination; page.PrevPage != nil || page.NextPage == nil || *page.NextPage != 2 {
			t.Fatalf("pagination = %+v, want null prev and next 2", page)
		}
	})

	t.Run("second page", func(t *testing.T) {
		t.Parallel()

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, stateRequest(t, listTarget("page[number]=2&page[size]=2")))

		doc := decodeStateList(t, rec)
		checkListIDs(t, doc, wantListPage{ids: []string{"sv-1"}, number: 2, size: 2, total: 3, pages: 2})

		if page := doc.Meta.Pagination; page.PrevPage == nil || *page.PrevPage != 1 || page.NextPage != nil {
			t.Fatalf("pagination = %+v, want prev 1 and null next", page)
		}
	})
}

// TestListStateVersionsAddedAttributes proves the list carries holder, force,
// restored-from, fence-by and unlocked-write on every entry.
func TestListStateVersionsAddedAttributes(t *testing.T) {
	t.Parallel()

	handler := newListStateHandler(t)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, stateRequest(t, listTarget("")))

	doc := decodeStateList(t, rec)

	want := map[string]stateListTestAttributes{
		"sv-3": {Holder: "", Force: false, RestoredFrom: "", FenceBy: "", Unlocked: true},
		"sv-2": {Holder: "", Force: false, RestoredFrom: "", FenceBy: "alice", Unlocked: false},
		"sv-1": {Holder: "tester", Force: true, RestoredFrom: "sv-0", FenceBy: "", Unlocked: false},
	}

	if len(doc.Data) != len(want) {
		t.Fatalf("list holds %d entries, want %d", len(doc.Data), len(want))
	}

	for _, item := range doc.Data {
		if item.Attributes != want[item.ID] {
			t.Fatalf("attributes of %q = %+v, want %+v", item.ID, item.Attributes, want[item.ID])
		}
	}
}

// TestListStateVersionsDeniedHidesExistence proves a caller without
// unit:read and without unit:read-outputs answers the same 404 for an existing
// and an unknown workspace, so the route never tells whether the filtered
// workspace exists. A caller with unit:read-outputs only knows that the
// workspace exists and gets 403, which TestStateVersionListOutputsOnlyIsForbidden proves.
func TestListStateVersionsDeniedHidesExistence(t *testing.T) {
	t.Parallel()

	handler := newStateHandler(
		t,
		selectiveAuthorizer{deny: map[identity.Action]bool{
			identity.ActionUnitRead: true, identity.ActionUnitReadOutputs: true,
		}},
	)

	for _, tc := range []struct {
		name   string
		target string
	}{
		{
			name:   "existing workspace",
			target: "/api/v2/state-versions?filter[organization][name]=acme&filter[workspace][name]=web",
		},
		{
			name:   "unknown workspace",
			target: "/api/v2/state-versions?filter[organization][name]=acme&filter[workspace][name]=ghost",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, stateRequest(t, tc.target))

			// The existing and the unknown workspace answer the same 404.
			checkJSONAPIError(t, rec, http.StatusNotFound)
		})
	}
}

// TestListStateVersionsFilters proves a missing filter answers 400, an
// unknown workspace answers 404 and a bad page answers 422, each as a
// JSON:API error.
func TestListStateVersionsFilters(t *testing.T) {
	t.Parallel()

	handler := newListStateHandler(t)

	for _, tc := range []struct {
		name   string
		target string
		want   int
	}{
		{name: "missing workspace", target: "/api/v2/state-versions?filter[organization][name]=acme", want: http.StatusBadRequest},
		{name: "missing organization", target: "/api/v2/state-versions?filter[workspace][name]=web", want: http.StatusBadRequest},
		{name: "no filters", target: "/api/v2/state-versions", want: http.StatusBadRequest},
		{
			name:   "unknown workspace",
			target: "/api/v2/state-versions?filter[organization][name]=acme&filter[workspace][name]=ghost",
			want:   http.StatusNotFound,
		},
		{
			name:   "unknown organization",
			target: "/api/v2/state-versions?filter[organization][name]=ghost&filter[workspace][name]=web",
			want:   http.StatusNotFound,
		},
		{
			name:   "bad page size",
			target: listTarget("page[size]=many"),
			want:   http.StatusUnprocessableEntity,
		},
		{
			name:   "zero page number",
			target: listTarget("page[number]=0"),
			want:   http.StatusUnprocessableEntity,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, stateRequest(t, tc.target))

			checkJSONAPIError(t, rec, tc.want)
		})
	}
}

// sensitiveOutputsWorkspaces serves the stub workspace with a state version
// that holds one plain output and one sensitive output.
type sensitiveOutputsWorkspaces struct {
	stubWorkspaces
}

// sensitiveStateVersion returns the stub state with the greeting output and a
// sensitive secret output.
func sensitiveStateVersion() workspace.StateVersion {
	sv := stubStateVersion()
	sv.Outputs = []byte(
		`{"greeting":{"sensitive":false,"value":"hello","type":"string"},` +
			`"secret":{"sensitive":true,"value":"s3cr3t","type":"string"}}`,
	)

	return sv
}

func (sensitiveOutputsWorkspaces) Current(_ context.Context, id workspace.ID) (workspace.StateVersion, error) {
	if id.String() != "ws-test123" {
		return workspace.StateVersion{}, workspace.ErrNoState
	}

	return sensitiveStateVersion(), nil
}

func (sensitiveOutputsWorkspaces) ReadState(
	_ context.Context, id workspace.StateVersionID,
) (workspace.StateVersion, error) {
	if id.String() != "sv-test123" {
		return workspace.StateVersion{}, workspace.ErrStateVersionNotFound
	}

	return sensitiveStateVersion(), nil
}

func (sensitiveOutputsWorkspaces) Open(_ context.Context, sv workspace.StateVersion) (io.ReadCloser, error) {
	if sv.ID.String() != "sv-test123" || sv.Status != workspace.StatusFinalized {
		return nil, workspace.ErrStateVersionNotFound
	}

	return io.NopCloser(strings.NewReader(stubStateContent())), nil
}

// newSensitiveStateHandler returns a production Handler over the state with a
// sensitive output.
func newSensitiveStateHandler(t *testing.T) *tfe.Handler {
	t.Helper()

	handler, err := tfe.NewHandler(tfe.Config{
		Registry:      nil,
		Authenticator: tokenAuthenticator{},
		Authorizer:    allowAll(),
		AddressGate:   nil,
		Logger:        slog.New(slog.DiscardHandler),
		Namespaces:    newTestNamespaces(t),
		Workspaces:    sensitiveOutputsWorkspaces{stubWorkspaces: stubWorkspaces{}},
		OutputChecker: newTestOutputChecker(
			t, allowAll(), sensitiveOutputsWorkspaces{stubWorkspaces: stubWorkspaces{}},
		),
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

// checkListGreeting proves the list still serves the plain greeting value.
func checkListGreeting(t *testing.T, byName map[string]stateOutputTestResource) {
	t.Helper()

	got, found := byName["greeting"]
	if !found {
		t.Fatal("outputs hold no greeting output")
	}

	value, isString := got.Attributes.Value.(string)
	if !isString || value != "hello" {
		t.Fatalf("greeting value = %v, want hello", got.Attributes.Value)
	}
}

// checkListSecretHidden proves the list reports null as the value of the
// sensitive output and the raw body never holds the plaintext.
func checkListSecretHidden(t *testing.T, raw string, byName map[string]stateOutputTestResource) {
	t.Helper()

	got, found := byName["secret"]
	if !found {
		t.Fatal("outputs hold no secret output")
	}

	if !got.Attributes.Sensitive {
		t.Fatal("secret output is not sensitive")
	}

	if got.Attributes.Value != nil {
		t.Fatalf("secret value = %v, want null", got.Attributes.Value)
	}

	if strings.Contains(raw, "s3cr3t") {
		t.Fatalf("outputs list holds the sensitive value: %s", raw)
	}
}

// checkSingleSecretShown proves the single output route still serves the
// sensitive value.
func checkSingleSecretShown(t *testing.T, got stateOutputTestResource) {
	t.Helper()

	if !got.Attributes.Sensitive {
		t.Fatal("single secret output is not sensitive")
	}

	value, isString := got.Attributes.Value.(string)
	if !isString || value != "s3cr3t" {
		t.Fatalf("single secret value = %v, want s3cr3t", got.Attributes.Value)
	}
}

// TestOutputsListHidesSensitiveValues proves the outputs list reports null as
// the value of a sensitive output, while the single output route still serves
// the value.
func TestOutputsListHidesSensitiveValues(t *testing.T) {
	t.Parallel()

	handler := newSensitiveStateHandler(t)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(
		rec,
		stateRequest(t, "/api/v2/workspaces/ws-test123/current-state-version-outputs"),
	)

	doc := decodeOutputList(t, rec, http.StatusOK)

	if len(doc.Data) != 2 {
		t.Fatalf("outputs hold %d entries, want the greeting and the secret", len(doc.Data))
	}

	byName := map[string]stateOutputTestResource{}

	for _, output := range doc.Data {
		byName[output.Attributes.Name] = output
	}

	checkListGreeting(t, byName)
	checkListSecretHidden(t, rec.Body.String(), byName)

	single := httptest.NewRecorder()
	handler.ServeHTTP(single, stateRequest(t, "/api/v2/state-version-outputs/sv-test123.secret"))

	checkSingleSecretShown(t, decodeOutputDoc(t, single, http.StatusOK).Data)
}

// TestStateSecretsNeedReadSecrets proves a caller with unit:read but without
// unit:read-secrets answers 403 on the download and the single output route,
// while the outputs list still answers 200 with sensitive values as null.
func TestStateSecretsNeedReadSecrets(t *testing.T) {
	t.Parallel()

	noSecrets := selectiveAuthorizer{deny: map[identity.Action]bool{identity.ActionUnitReadSecrets: true}}

	handler, err := tfe.NewHandler(tfe.Config{
		Registry:      nil,
		Authenticator: tokenAuthenticator{},
		Authorizer:    noSecrets,
		AddressGate:   nil,
		Logger:        slog.New(slog.DiscardHandler),
		Namespaces:    newTestNamespaces(t),
		Workspaces:    sensitiveOutputsWorkspaces{stubWorkspaces: stubWorkspaces{}},
		OutputChecker: newTestOutputChecker(
			t, noSecrets, sensitiveOutputsWorkspaces{stubWorkspaces: stubWorkspaces{}},
		),
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

	for _, target := range []struct {
		name string
		path string
	}{
		{name: "download", path: "/api/v2/state-versions/sv-test123/download"},
		{name: "single output", path: "/api/v2/state-version-outputs/sv-test123.secret"},
	} {
		t.Run(target.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, stateRequest(t, target.path))

			checkJSONAPIError(t, rec, http.StatusForbidden)
		})
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(
		rec,
		stateRequest(t, "/api/v2/workspaces/ws-test123/current-state-version-outputs"),
	)

	doc := decodeOutputList(t, rec, http.StatusOK)

	if len(doc.Data) != 2 {
		t.Fatalf("outputs hold %d entries, want the greeting and the secret", len(doc.Data))
	}

	byName := map[string]stateOutputTestResource{}

	for _, output := range doc.Data {
		byName[output.Attributes.Name] = output
	}

	checkListGreeting(t, byName)
	checkListSecretHidden(t, rec.Body.String(), byName)
}
