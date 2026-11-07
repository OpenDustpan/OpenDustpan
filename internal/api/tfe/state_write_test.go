package tfe_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// writeWorkspaces is a fake WorkspaceService for the state write routes: it
// answers the stub workspace everywhere except BeginState, Upload and
// DiscardJSONRendering, which run the injected funcs.
type writeWorkspaces struct {
	stubWorkspaces

	begin   func(ctx context.Context, id workspace.ID, holder string, in workspace.NewState) (workspace.PendingState, error)
	upload  func(ctx context.Context, id workspace.StateVersionID, tok token.Token, content io.Reader) (workspace.StateVersion, error)
	discard func(ctx context.Context, id workspace.StateVersionID, tok token.Token, content io.Reader) error
}

func (w writeWorkspaces) BeginState(
	ctx context.Context, id workspace.ID, holder string, in workspace.NewState, _ workspace.Authority,
) (workspace.PendingState, error) {
	return w.begin(ctx, id, holder, in)
}

func (w writeWorkspaces) Upload(
	ctx context.Context, id workspace.StateVersionID, tok token.Token, content io.Reader,
) (workspace.StateVersion, error) {
	return w.upload(ctx, id, tok, content)
}

func (w writeWorkspaces) DiscardJSONRendering(
	ctx context.Context, id workspace.StateVersionID, tok token.Token, content io.Reader,
) error {
	return w.discard(ctx, id, tok, content)
}

// newWriteHandler returns a production Handler over ws with base as the base
// URL resolver.
func newWriteHandler(t *testing.T, ws tfe.WorkspaceService, base baseurl.Resolver) *tfe.Handler {
	t.Helper()

	handler, err := tfe.NewHandler(tfe.Config{
		Registry:       nil,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     allowAll(),
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     ws,
		OutputChecker:  newTestOutputChecker(t, allowAll(), ws),
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

// stateCreateOutputs is the json-state-outputs document the create tests
// send: the flat map of outputs the CLI sends, with the greeting output and
// its cty value and type.
func stateCreateOutputs() string {
	return `{"greeting":{"sensitive":false,"value":"hello","type":"string"}}`
}

// stateCreateWrappedOutputs is the wrapped json-state-outputs document of
// versions stored before the flat shape was served: the server keeps
// accepting it.
func stateCreateWrappedOutputs() string {
	return `{"outputs":{"greeting":{"sensitive":false,"value":"hello","type":"string"}}}`
}

// stateCreateBody returns a state version create body for serial and the
// base64 outputs document, with the seed lineage and the empty MD5 the
// create tests share.
func stateCreateBody(serial int64, outputs string) string {
	const lineage = "seed-lineage"

	const md5 = "d41d8cd98f00b204e9800998ecf8427e"

	return fmt.Sprintf(
		`{"data":{"type":"state-versions","attributes":{`+
			`"serial":%d,"lineage":%q,"md5":%q,"json-state-outputs":%q}}}`,
		serial,
		lineage,
		md5,
		base64.StdEncoding.EncodeToString([]byte(outputs)),
	)
}

// createStateRequest builds an authenticated create request.
func createStateRequest(t *testing.T, target, body string) *http.Request {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")

	return req
}

// createTestAttributes decodes the attributes of a created state version in
// tests.
type createTestAttributes struct {
	UploadURL     string `json:"hosted-state-upload-url"`      //nolint:tagliatelle // The TFE wire name; it must not change.
	JSONUploadURL string `json:"hosted-json-state-upload-url"` //nolint:tagliatelle // The TFE wire name; it must not change.
	Status        string `json:"status"`
	Serial        int64  `json:"serial"`
	Lineage       string `json:"lineage"`
	MD5           string `json:"md5"`
}

// createTestResource decodes a created state version resource in tests.
type createTestResource struct {
	ID         string               `json:"id"`
	Type       string               `json:"type"`
	Attributes createTestAttributes `json:"attributes"`
}

// createTestDoc decodes a state version create document in tests.
type createTestDoc struct {
	Data createTestResource `json:"data"`
}

// decodeCreateDoc decodes a create document and fails the test when the
// status is not 201.
func decodeCreateDoc(t *testing.T, rec *httptest.ResponseRecorder) createTestDoc {
	t.Helper()

	if rec.Code != http.StatusCreated {
		t.Fatalf("request answers %d, want %d: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	if got := rec.Header().Get("Content-Type"); got != "application/vnd.api+json" {
		t.Fatalf("media type = %q, want %q", got, "application/vnd.api+json")
	}

	var doc createTestDoc

	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode create body: %v", err)
	}

	return doc
}

// TestCreateStateVersionDeclaresUnitWrite proves the create route declares
// unit:write and resolves the unit opentofu:acme/web.
func TestCreateStateVersionDeclaresUnitWrite(t *testing.T) {
	t.Parallel()

	handler := newWriteHandler(t, stubWorkspaces{}, baseurl.Resolver{})

	checkWorkspaceRoute(t, handler, workspaceRouteCase{
		name:    "state version create",
		pattern: "POST /api/v2/workspaces/{id}/state-versions",
		method:  http.MethodPost,
		target:  "/api/v2/workspaces/ws-test123/state-versions",
		body:    stateCreateBody(4, stateCreateOutputs()),
		actions: []identity.Action{identity.ActionUnitWrite},
		object:  "opentofu:acme/web",
		isUnit:  true,
	})
}

// TestCreateStateVersion proves a create answers 201 with the pending state
// version document holding both absolute upload URLs under the request host.
func TestCreateStateVersion(t *testing.T) {
	t.Parallel()

	handler := newWriteHandler(t, stubWorkspaces{}, baseurl.Resolver{})

	const md5 = "d41d8cd98f00b204e9800998ecf8427e"

	rec := httptest.NewRecorder()
	handler.ServeHTTP(
		rec,
		createStateRequest(
			t,
			"/api/v2/workspaces/ws-test123/state-versions",
			stateCreateBody(4, stateCreateOutputs()),
		),
	)

	doc := decodeCreateDoc(t, rec)

	got := doc.Data

	if got.ID != "sv-new123" || got.Type != "state-versions" {
		t.Fatalf("state version = %q %q, want id sv-new123 type state-versions", got.ID, got.Type)
	}

	attrs := got.Attributes
	if attrs.Status != "pending" || attrs.Serial != 4 || attrs.Lineage != "seed-lineage" || attrs.MD5 != md5 {
		t.Fatalf("attributes = %+v, want pending serial 4 of the seed lineage", attrs)
	}

	checkUploadURL(t, attrs.UploadURL, "http://example.com", "state-uploads", "sv-new123")
	checkUploadURL(t, attrs.JSONUploadURL, "http://example.com", "json-state-uploads", "sv-new123")

	if !strings.HasSuffix(attrs.UploadURL, uploadToken(attrs.UploadURL)) ||
		uploadToken(attrs.UploadURL) != uploadToken(attrs.JSONUploadURL) {
		t.Fatalf("upload URLs %q and %q share no capability", attrs.UploadURL, attrs.JSONUploadURL)
	}
}

// TestCreateStateVersionAcceptsWrappedOutputs proves a create with the
// wrapped outputs document of versions stored before the flat shape was
// served still answers 201.
func TestCreateStateVersionAcceptsWrappedOutputs(t *testing.T) {
	t.Parallel()

	handler := newWriteHandler(t, stubWorkspaces{}, baseurl.Resolver{})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(
		rec,
		createStateRequest(
			t,
			"/api/v2/workspaces/ws-test123/state-versions",
			stateCreateBody(4, stateCreateWrappedOutputs()),
		),
	)

	decodeCreateDoc(t, rec)
}

// uploadToken returns the token in an upload URL path.
func uploadToken(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}

	return u.Path[strings.LastIndex(u.Path, "/")+1:]
}

// checkUploadURL proves raw is an absolute upload URL of kind for the state
// version under host with a well-formed capability in the path.
func checkUploadURL(t *testing.T, raw, host, kind, version string) {
	t.Helper()

	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() {
		t.Fatalf("upload URL = %q, want an absolute URL", raw)
	}

	want := host + "/api/v2/" + kind + "/" + version + "/"
	if !strings.HasPrefix(raw, want) {
		t.Fatalf("upload URL = %q, want the prefix %q", raw, want)
	}

	if _, err := token.Parse(uploadToken(raw)); err != nil {
		t.Fatalf("upload URL %q holds no capability: %v", raw, err)
	}
}

// TestCreateStateVersionValidation proves malformed creates answer 422: a
// malformed body, missing attributes, the inline state attribute, outputs
// that are not base64 and outputs that are not the outputs shape.
func TestCreateStateVersionValidation(t *testing.T) {
	t.Parallel()

	handler := newWriteHandler(t, stubWorkspaces{}, baseurl.Resolver{})

	const target = "/api/v2/workspaces/ws-test123/state-versions"

	cases := []struct {
		name string
		body string
	}{
		{name: "malformed body", body: `{`},
		{name: "missing attributes", body: `{"data":{"type":"state-versions"}}`},
		{
			name: "missing serial",
			body: `{"data":{"type":"state-versions","attributes":{"lineage":"l","md5":"d41d8cd98f00b204e9800998ecf8427e"}}}`,
		},
		{
			name: "inline state",
			body: `{"data":{"type":"state-versions","attributes":{` +
				`"serial":4,"lineage":"l","md5":"d41d8cd98f00b204e9800998ecf8427e","state":"e30="}}}`,
		},
		{
			name: "outputs not base64",
			body: `{"data":{"type":"state-versions","attributes":{` +
				`"serial":4,"lineage":"l","md5":"d41d8cd98f00b204e9800998ecf8427e","json-state-outputs":"!!!"}}}`,
		},
		{
			name: "outputs not the outputs shape",
			body: `{"data":{"type":"state-versions","attributes":{` +
				`"serial":4,"lineage":"l","md5":"d41d8cd98f00b204e9800998ecf8427e","json-state-outputs":"W10="}}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, createStateRequest(t, target, tc.body))

			checkJSONAPIError(t, rec, http.StatusUnprocessableEntity)
		})
	}
}

// TestCreateStateVersionErrors proves the create maps service errors: a
// caller without the lock gets 409, a stale serial 422 and an unknown
// workspace 404.
func TestCreateStateVersionErrors(t *testing.T) {
	t.Parallel()

	const target = "/api/v2/workspaces/ws-test123/state-versions"

	body := stateCreateBody(1, stateCreateOutputs())

	cases := []struct {
		name  string
		begin error
		want  int
	}{
		{name: "without the lock", begin: workspace.ErrNotLocked, want: http.StatusConflict},
		{name: "held by another", begin: workspace.ErrLockedByOther, want: http.StatusConflict},
		{name: "too many pending", begin: workspace.ErrTooManyPending, want: http.StatusConflict},
		{name: "stale serial", begin: workspace.ErrStaleSerial, want: http.StatusUnprocessableEntity},
		{name: "lineage mismatch", begin: workspace.ErrLineageMismatch, want: http.StatusUnprocessableEntity},
		{name: "invalid state", begin: workspace.ErrInvalidState, want: http.StatusUnprocessableEntity},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := newWriteHandler(t, writeWorkspaces{
				stubWorkspaces: stubWorkspaces{},
				begin: func(
					_ context.Context, _ workspace.ID, _ string, _ workspace.NewState,
				) (workspace.PendingState, error) {
					return workspace.PendingState{}, tc.begin
				},
				upload: func(
					_ context.Context, _ workspace.StateVersionID, _ token.Token, _ io.Reader,
				) (workspace.StateVersion, error) {
					return workspace.StateVersion{}, workspace.ErrUploadDenied
				},
				discard: func(
					_ context.Context, _ workspace.StateVersionID, _ token.Token, _ io.Reader,
				) error {
					return workspace.ErrUploadDenied
				},
			}, baseurl.Resolver{})

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, createStateRequest(t, target, body))

			checkJSONAPIError(t, rec, tc.want)
		})
	}

	t.Run("unknown workspace", func(t *testing.T) {
		t.Parallel()

		handler := newWriteHandler(t, stubWorkspaces{}, baseurl.Resolver{})

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, createStateRequest(t, "/api/v2/workspaces/ws-missing/state-versions", body))

		checkJSONAPIError(t, rec, http.StatusNotFound)
	})
}

// lineageLockWorkspaces answers a lineage mismatch on BeginState and a locked
// or unlocked stub workspace on GetByID, so the lineage detail tests prove
// when the 422 names the force-unlock command.
type lineageLockWorkspaces struct {
	stubWorkspaces

	holder string
}

func (l lineageLockWorkspaces) BeginState(
	_ context.Context, _ workspace.ID, _ string, _ workspace.NewState, _ workspace.Authority,
) (workspace.PendingState, error) {
	return workspace.PendingState{}, fmt.Errorf(
		`new state for acme/web: %w: "new-lineage", current "seed-lineage"`,
		workspace.ErrLineageMismatch,
	)
}

func (l lineageLockWorkspaces) GetByID(_ context.Context, id workspace.ID) (workspace.Workspace, error) {
	ws, err := stubWorkspaceByID(id)
	if err != nil {
		return workspace.Workspace{}, err
	}

	if l.holder != "" {
		ws.Lock.Holder = l.holder
		ws.Lock.ID = "lock-test"
	}

	return ws, nil
}

// TestCreateStateVersionLineageNamesForceUnlock proves a refused state upload
// of another lineage answers 422 with the force-unlock command while the
// caller holds the lock, and without it while another principal holds the
// lock or the workspace is unlocked.
func TestCreateStateVersionLineageNamesForceUnlock(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		holder     string
		wantUnlock bool
	}{
		{name: "lock of the caller names force-unlock", holder: "tester", wantUnlock: true},
		{name: "lock of another job keeps the detail", holder: "job-b", wantUnlock: false},
		{name: "unlocked keeps the detail", holder: "", wantUnlock: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := newWriteHandler(
				t,
				lineageLockWorkspaces{stubWorkspaces: stubWorkspaces{}, holder: tc.holder},
				baseurl.Resolver{},
			)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(
				rec,
				createStateRequest(
					t,
					"/api/v2/workspaces/ws-test123/state-versions",
					stateCreateBody(4, stateCreateOutputs()),
				),
			)

			checkJSONAPIError(t, rec, http.StatusUnprocessableEntity)

			body := rec.Body.String()
			if !strings.Contains(body, "state lineage differs from the current state") {
				t.Fatalf("422 detail lacks the lineage mismatch: %s", body)
			}

			if tc.wantUnlock {
				if !strings.Contains(body, "tofu force-unlock acme/web") {
					t.Fatalf("422 detail lacks the force-unlock command: %s", body)
				}
			} else if strings.Contains(body, "force-unlock") {
				t.Fatalf("422 detail names force-unlock for a lock the caller does not hold: %s", body)
			}
		})
	}
}

// TestCreateStateVersionTooLarge proves a create body over the create limit
// answers 413 with a JSON:API error stating the limit.
func TestCreateStateVersionTooLarge(t *testing.T) {
	t.Parallel()

	handler := newWriteHandler(t, stubWorkspaces{}, baseurl.Resolver{})

	const limit = 32 << 20

	rec := httptest.NewRecorder()
	handler.ServeHTTP(
		rec,
		createStateRequest(
			t,
			"/api/v2/workspaces/ws-test123/state-versions",
			strings.Repeat("x", limit+1),
		),
	)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("request answers %d, want %d: %s", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
	}

	checkJSONAPIError(t, rec, http.StatusRequestEntityTooLarge)

	if !strings.Contains(rec.Body.String(), strconv.Itoa(limit)) {
		t.Fatalf("error detail lacks the limit %d: %s", limit, rec.Body.String())
	}
}

// uploadTestSetup holds one upload test's handler and capability.
type uploadTestSetup struct {
	handler *tfe.Handler
	service writeWorkspaces
	reveal  string
}

// newUploadSetup returns a handler whose Upload runs upload and the revealed
// capability of a fresh token.
func newUploadSetup(
	t *testing.T,
	upload func(ctx context.Context, id workspace.StateVersionID, tok token.Token, content io.Reader) (workspace.StateVersion, error),
) uploadTestSetup {
	t.Helper()

	uploadToken, err := token.New()
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	service := writeWorkspaces{
		stubWorkspaces: stubWorkspaces{},
		begin: func(
			_ context.Context, _ workspace.ID, _ string, _ workspace.NewState,
		) (workspace.PendingState, error) {
			return workspace.PendingState{}, workspace.ErrNotLocked
		},
		upload: upload,
		discard: func(
			_ context.Context, _ workspace.StateVersionID, _ token.Token, _ io.Reader,
		) error {
			return nil
		},
	}

	return uploadTestSetup{
		handler: newWriteHandler(t, service, baseurl.Resolver{}),
		service: service,
		reveal:  uploadToken.Reveal(),
	}
}

// uploadRequest builds a PUT to the state upload path without an
// Authorization header, as the CLI sends it: the token in the path is the
// credential.
func uploadRequest(t *testing.T, target, content string) *http.Request {
	t.Helper()

	return httptest.NewRequestWithContext(t.Context(), http.MethodPut, target, strings.NewReader(content))
}

// TestUploadState proves a PUT with the capability answers 200 and reads the
// content through the service.
func TestUploadState(t *testing.T) {
	t.Parallel()

	var gotID workspace.StateVersionID

	var gotContent string

	setup := newUploadSetup(t, func(
		_ context.Context, id workspace.StateVersionID, _ token.Token, content io.Reader,
	) (workspace.StateVersion, error) {
		raw, err := io.ReadAll(content)
		if err != nil {
			return workspace.StateVersion{}, fmt.Errorf("read upload content: %w", err)
		}

		gotID, gotContent = id, string(raw)

		return stubStateVersion(), nil
	})

	rec := httptest.NewRecorder()
	setup.handler.ServeHTTP(
		rec,
		uploadRequest(t, "/api/v2/state-uploads/sv-new123/"+setup.reveal, `{"version":4}`),
	)

	if rec.Code != http.StatusOK {
		t.Fatalf("upload answers %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	if gotID.String() != "sv-new123" || gotContent != `{"version":4}` {
		t.Fatalf("upload reached %q with %q, want sv-new123 with the state", gotID, gotContent)
	}
}

// TestUploadStateErrors proves the PUT maps service errors: a wrong
// capability answers 403, a lost lock 409, a checksum mismatch 422, an
// unknown version 404 and an oversized upload 413.
func TestUploadStateErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		upload error
		want   int
	}{
		{name: "denied capability", upload: workspace.ErrUploadDenied, want: http.StatusForbidden},
		{name: "not locked", upload: workspace.ErrNotLocked, want: http.StatusConflict},
		{name: "locked by another", upload: workspace.ErrLockedByOther, want: http.StatusConflict},
		{name: "checksum mismatch", upload: workspace.ErrChecksumMismatch, want: http.StatusUnprocessableEntity},
		{name: "unknown version", upload: workspace.ErrStateVersionNotFound, want: http.StatusNotFound},
		{name: "too large", upload: workspace.ErrTooLarge, want: http.StatusRequestEntityTooLarge},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			setup := newUploadSetup(t, func(
				_ context.Context, _ workspace.StateVersionID, _ token.Token, _ io.Reader,
			) (workspace.StateVersion, error) {
				return workspace.StateVersion{}, tc.upload
			})

			rec := httptest.NewRecorder()
			setup.handler.ServeHTTP(
				rec,
				uploadRequest(t, "/api/v2/state-uploads/sv-new123/"+setup.reveal, "state"),
			)

			checkJSONAPIError(t, rec, tc.want)
		})
	}

	t.Run("malformed token", func(t *testing.T) {
		t.Parallel()

		called := false

		setup := newUploadSetup(t, func(
			_ context.Context, _ workspace.StateVersionID, _ token.Token, _ io.Reader,
		) (workspace.StateVersion, error) {
			called = true

			return stubStateVersion(), nil
		})

		rec := httptest.NewRecorder()
		setup.handler.ServeHTTP(rec, uploadRequest(t, "/api/v2/state-uploads/sv-new123/not-a-token", "state"))

		checkJSONAPIError(t, rec, http.StatusForbidden)

		if called {
			t.Fatal("malformed token reached the service")
		}
	})
}

// TestUploadStateSkipsSignIn proves the PUT serves without sign-in: with no
// Authorization header and with a wrong Bearer token it still reaches the
// service, so only the capability decides.
func TestUploadStateSkipsSignIn(t *testing.T) {
	t.Parallel()

	// A request without a token must fail closed, never with 404: the route
	// is served.
	without := newUploadSetup(t, func(
		_ context.Context, _ workspace.StateVersionID, _ token.Token, _ io.Reader,
	) (workspace.StateVersion, error) {
		return stubStateVersion(), nil
	})

	rec := httptest.NewRecorder()
	without.handler.ServeHTTP(rec, uploadRequest(t, "/api/v2/state-uploads/sv-new123/not-a-token", "state"))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("unsigned upload answers %d, want %d", rec.Code, http.StatusForbidden)
	}

	// A wrong Bearer token changes nothing: the path capability decides.
	ignored := newUploadSetup(t, func(
		_ context.Context, _ workspace.StateVersionID, _ token.Token, _ io.Reader,
	) (workspace.StateVersion, error) {
		return stubStateVersion(), nil
	})

	req := uploadRequest(t, "/api/v2/state-uploads/sv-new123/"+ignored.reveal, "state")
	req.Header.Set("Authorization", "Bearer wrong-token")

	rec = httptest.NewRecorder()
	ignored.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("upload with a wrong bearer token answers %d, want %d", rec.Code, http.StatusOK)
	}
}

// TestOnlyUploadRoutesSkipSignIn proves exactly the two upload routes carry
// the capability scope: every other route is public or needs sign-in.
func TestOnlyUploadRoutesSkipSignIn(t *testing.T) {
	t.Parallel()

	handler := newProductionHandler(t)

	var upload []string

	for _, route := range handler.Routes() {
		if route.Scope == tfe.ScopeUpload {
			upload = append(upload, route.Method+" "+route.Path)
		}
	}

	want := []string{
		"PUT /api/v2/state-uploads/{id}/{token}",
		"PUT /api/v2/json-state-uploads/{id}/{token}",
	}

	if len(upload) != len(want) {
		t.Fatalf("upload routes = %v, want %v", upload, want)
	}

	for i := range want {
		if upload[i] != want[i] {
			t.Fatalf("upload routes = %v, want %v", upload, want)
		}
	}
}

// TestUploadJSONState proves the JSON rendering PUT answers 200 and discards
// the rendering, while a wrong capability answers 403.
func TestUploadJSONState(t *testing.T) {
	t.Parallel()

	t.Run("discarded", func(t *testing.T) {
		t.Parallel()

		var gotContent string

		reveal, err := token.New()
		if err != nil {
			t.Fatalf("token: %v", err)
		}

		handler := newWriteHandler(t, writeWorkspaces{
			stubWorkspaces: stubWorkspaces{},
			begin: func(
				_ context.Context, _ workspace.ID, _ string, _ workspace.NewState,
			) (workspace.PendingState, error) {
				return workspace.PendingState{}, workspace.ErrNotLocked
			},
			upload: func(
				_ context.Context, _ workspace.StateVersionID, _ token.Token, _ io.Reader,
			) (workspace.StateVersion, error) {
				return workspace.StateVersion{}, workspace.ErrUploadDenied
			},
			discard: func(
				_ context.Context, _ workspace.StateVersionID, _ token.Token, content io.Reader,
			) error {
				raw, err := io.ReadAll(content)
				if err != nil {
					return fmt.Errorf("read rendering content: %w", err)
				}

				gotContent = string(raw)

				return nil
			},
		}, baseurl.Resolver{})

		rec := httptest.NewRecorder()
		handler.ServeHTTP(
			rec,
			uploadRequest(t, "/api/v2/json-state-uploads/sv-new123/"+reveal.Reveal(), `{"rendering":1}`),
		)

		if rec.Code != http.StatusOK {
			t.Fatalf("rendering upload answers %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
		}

		if gotContent != `{"rendering":1}` {
			t.Fatalf("rendering reached the service as %q", gotContent)
		}
	})

	t.Run("wrong capability", func(t *testing.T) {
		t.Parallel()

		handler := newWriteHandler(t, writeWorkspaces{
			stubWorkspaces: stubWorkspaces{},
			begin: func(
				_ context.Context, _ workspace.ID, _ string, _ workspace.NewState,
			) (workspace.PendingState, error) {
				return workspace.PendingState{}, workspace.ErrNotLocked
			},
			upload: func(
				_ context.Context, _ workspace.StateVersionID, _ token.Token, _ io.Reader,
			) (workspace.StateVersion, error) {
				return workspace.StateVersion{}, workspace.ErrUploadDenied
			},
			discard: func(
				_ context.Context, _ workspace.StateVersionID, _ token.Token, _ io.Reader,
			) error {
				return workspace.ErrUploadDenied
			},
		}, baseurl.Resolver{})

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, uploadRequest(t, "/api/v2/json-state-uploads/sv-new123/not-a-token", "{}"))

		checkJSONAPIError(t, rec, http.StatusForbidden)
	})
}

// TestStateUploadUsesPublicURL proves the create reports the upload URLs
// under the public URL when one is set.
func TestStateUploadUsesPublicURL(t *testing.T) {
	t.Parallel()

	public, err := baseurl.Parse("https://dustpan.example.test")
	if err != nil {
		t.Fatalf("parse public URL: %v", err)
	}

	handler := newWriteHandler(t, stubWorkspaces{}, public)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, createStateRequest(
		t,
		"/api/v2/workspaces/ws-test123/state-versions",
		stateCreateBody(4, stateCreateOutputs()),
	))

	doc := decodeCreateDoc(t, rec)

	for _, raw := range []string{doc.Data.Attributes.UploadURL, doc.Data.Attributes.JSONUploadURL} {
		if !strings.HasPrefix(raw, "https://dustpan.example.test/") {
			t.Fatalf("upload URL = %q, want the public host", raw)
		}
	}
}

// TestStateUploadProxyIgnoresForwardedHeaders proves a create through a
// reverse proxy that sets X-Forwarded-* still reports the upload URLs under
// the public URL: the server never trusts proxy headers.
func TestStateUploadProxyIgnoresForwardedHeaders(t *testing.T) {
	t.Parallel()

	public, err := baseurl.Parse("https://dustpan.example.test")
	if err != nil {
		t.Fatalf("parse public URL: %v", err)
	}

	backend := httptest.NewServer(newWriteHandler(t, stubWorkspaces{}, public))
	defer backend.Close()

	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatalf("parse backend URL: %v", err)
	}

	//nolint:exhaustruct_v5 // The test proxy needs only Rewrite; the rest stays zero.
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			r.Out.Host = "proxy.example.test"
			r.Out.Header.Set("X-Forwarded-Host", "proxy.example.test")
			r.Out.Header.Set("X-Forwarded-Proto", "http")
			r.Out.Header.Set("X-Forwarded-Port", "80")
		},
	}
	front := httptest.NewServer(proxy)
	defer front.Close()

	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		front.URL+"/api/v2/workspaces/ws-test123/state-versions",
		strings.NewReader(stateCreateBody(4, stateCreateOutputs())),
	)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/vnd.api+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post through the proxy: %v", err)
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The body was read to completion below where it matters.
	}()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read proxy response: %v", err)
	}

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create answers %d, want %d: %s", resp.StatusCode, http.StatusCreated, raw)
	}

	var doc createTestDoc

	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode create body: %v", err)
	}

	for _, upload := range []string{doc.Data.Attributes.UploadURL, doc.Data.Attributes.JSONUploadURL} {
		if !strings.HasPrefix(upload, "https://dustpan.example.test/") {
			t.Fatalf("upload URL = %q, want the public host, not the proxy headers", upload)
		}
	}
}

// readStateAttributesDoc decodes the attributes object of a state version
// document in tests.
type readStateAttributesDoc struct {
	Data readStateAttributesData `json:"data"`
}

// readStateAttributesData decodes the data object of a state version
// document in tests.
type readStateAttributesData struct {
	Attributes map[string]any `json:"attributes"`
}

// TestReadStateOmitsUploadURLs proves state reads carry no upload URLs: only
// the create response mints a capability.
func TestReadStateOmitsUploadURLs(t *testing.T) {
	t.Parallel()

	handler := newWriteHandler(t, stubWorkspaces{}, baseurl.Resolver{})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, stateRequest(t, "/api/v2/workspaces/ws-test123/current-state-version"))

	if rec.Code != http.StatusOK {
		t.Fatalf("request answers %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var decoded readStateAttributesDoc

	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode state body: %v", err)
	}

	for _, key := range []string{"hosted-state-upload-url", "hosted-json-state-upload-url"} {
		if _, ok := decoded.Data.Attributes[key]; ok {
			t.Fatalf("state read holds %q, want no upload URL", key)
		}
	}
}
