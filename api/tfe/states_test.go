package tfe_test

import (
	"crypto/md5" //nolint:gosec // The test recomputes the MD5 the protocol uses for integrity.
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/api/tfe"
	"opendustpan.org/opendustpan/identity"
	"opendustpan.org/opendustpan/internal/baseurl"
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
		Authenticator: tokenAuthenticator{},
		Authorizer:    allowAll(),
		Logger:        slog.New(slog.DiscardHandler),
		Namespaces:    newTestNamespaces(t),
		Workspaces:    stubWorkspaces{},
		BaseURL:       base,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// TestStateRoutesDeclareActionsAndObjects proves each state route declares
// unit:read and resolves the unit opentofu:acme/web.
func TestStateRoutesDeclareActionsAndObjects(t *testing.T) {
	t.Parallel()

	handler := newStateHandler(t, allowAll())

	cases := []workspaceRouteCase{
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
			actions: []identity.Action{identity.ActionUnitRead},
			object:  "opentofu:acme/web",
			isUnit:  true,
		},
		{
			name:    "current state version outputs",
			pattern: "GET /api/v2/workspaces/{id}/current-state-version-outputs",
			method:  http.MethodGet,
			target:  "/api/v2/workspaces/ws-test123/current-state-version-outputs",
			body:    "",
			actions: []identity.Action{identity.ActionUnitRead},
			object:  "opentofu:acme/web",
			isUnit:  true,
		},
		{
			name:    "state version output read",
			pattern: "GET /api/v2/state-version-outputs/{output_id}",
			method:  http.MethodGet,
			target:  "/api/v2/state-version-outputs/sv-test123.greeting",
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
