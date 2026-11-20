package runnerpools_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/runnerpools"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/fakecred"
	"opendustpan.org/opendustpan/internal/proxysessionsvc"
	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/secret"
)

const (
	// hiddenValue is the password of the upstream credential that no answer
	// may hold.
	hiddenValue = "pool-upstream-value-0123"
	settingsURL = "https://upstream.example.com:3128"
)

// settingsEnv is a management handler that serves the proxy settings routes
// over a fresh store with the pool prod in acme.
type settingsEnv struct {
	t       *testing.T
	server  *httptest.Server
	handler *runnerpools.Handler
	pool    string
}

// newSettingsEnv serves the management handler with the proxy session service
// and the authorizer authz.
func newSettingsEnv(t *testing.T, authz identity.Authorizer) *settingsEnv {
	t.Helper()

	s, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.UnixMilli(1_700_000_000_000).UTC(), Name: "acme",
		PrimaryInstallation: installationName, FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err = s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	namespaces, err := namespace.NewService(namespace.Config{Store: s, Now: nil, Installation: installationName})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	pool, err := s.Runners().CreatePool(t.Context(), runner.Pool{
		ID: "", Namespace: "acme", Name: "prod", Purpose: runner.PurposeJobs, Lease: runner.DefaultLease,
		Concurrency: 1, Isolation: []runner.Isolation{runner.IsolationGVisor},
		GitCredentials: runner.GitCredentials{Mode: runner.GitCredentialsNone, Helper: ""},
		CreatedAt:      time.UnixMilli(1_700_000_000_000).UTC(), UpdatedAt: time.UnixMilli(1_700_000_000_000).UTC(),
	}, ns.Fence(), []audit.Entry{poolEntry()})
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	logAudit := newTestAudit()

	h, err := runnerpools.NewHandler(runnerpools.Config{
		Namespaces: namespaces, Pools: s.Runners(), Members: s.Runners(), Authenticator: adminAuthenticator{},
		Authorizer: authz, Audit: logAudit, OnAuditFailure: nil, DenialLimiter: nil, Sessions: nil,
		Logger: slog.New(slog.DiscardHandler), ServerURL: serverURL, Now: nil,
		Proxies: settingsService(t, s, namespaces, logAudit),
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	return &settingsEnv{t: t, server: srv, handler: h, pool: pool.ID}
}

// settingsService builds the proxy session service over s.
func settingsService(
	t *testing.T, s *sqlite.Store, namespaces *namespace.Service, log proxysessionsvc.Appender,
) *proxysessionsvc.Service {
	t.Helper()

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("wrapper: %v", err)
	}

	secrets, err := secret.NewService(secret.Config{
		Keys: s, Wrapper: wrapper, Namespaces: namespaces, Now: nil, MaxBatchBytes: 0,
	})
	if err != nil {
		t.Fatalf("secret service: %v", err)
	}

	svc, err := proxysessionsvc.New(proxysessionsvc.Config{
		Settings: s.Runners(), Rules: s.ProxyRules(), Secrets: secrets, Namespaces: namespaces,
		Operations: s.DurableOperations(), Claims: s.Runners(), Jobs: s.Jobs(), Units: s, Audit: log,
		Logger: slog.New(slog.DiscardHandler), Now: nil,
	})
	if err != nil {
		t.Fatalf("proxy session service: %v", err)
	}

	return svc
}

// poolEntry returns the audit entry of the create of the pool.
func poolEntry() audit.Entry {
	return audit.Entry{
		At: time.UnixMilli(1_700_000_000_000).UTC(), Namespace: "acme", Subject: "ada", IDPSubject: "",
		TokenID: "", Action: string(identity.ActionNamespaceManageRunners),
		Object: identity.NamespaceObject("acme").String(), Version: "", ValuePath: "", ValueCount: nil,
		Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "", Origin: nil,
	}
}

// settingsRequest is one request of a case.
type settingsRequest struct {
	method      string
	body        string
	contentType string
	ifMatch     string
}

// do sends req to the proxy settings of the pool and returns the status, the
// headers and the body.
func (e *settingsEnv) do(req settingsRequest) (int, http.Header, []byte) {
	e.t.Helper()

	url := e.server.URL + "/api/dustpan/v1/namespaces/acme/runner-pools/" + e.pool + "/proxy-settings"

	r, err := http.NewRequestWithContext(e.t.Context(), req.method, url, strings.NewReader(req.body))
	if err != nil {
		e.t.Fatalf("request: %v", err)
	}

	r.Header.Set("Authorization", "token "+adminToken)

	if req.contentType != "" {
		r.Header.Set("Content-Type", req.contentType)
	}

	if req.ifMatch != "" {
		r.Header.Set("If-Match", req.ifMatch)
	}

	resp, err := e.server.Client().Do(r)
	if err != nil {
		e.t.Fatalf("send: %v", err)
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			e.t.Errorf("close body: %v", closeErr)
		}
	}()

	var buf bytes.Buffer
	if _, err = buf.ReadFrom(resp.Body); err != nil {
		e.t.Fatalf("read body: %v", err)
	}

	return resp.StatusCode, resp.Header, buf.Bytes()
}

// put returns a PUT request of body as JSON.
func put(body string) settingsRequest {
	return settingsRequest{method: http.MethodPut, body: body, contentType: "application/json", ifMatch: ""}
}

// get returns a GET request.
func get() settingsRequest {
	return settingsRequest{method: http.MethodGet, body: "", contentType: "", ifMatch: ""}
}

// withType returns req with the content type.
func withType(req settingsRequest, contentType string) settingsRequest {
	req.contentType = contentType

	return req
}

// withIfMatch returns req with the If-Match header.
func withIfMatch(req settingsRequest, ifMatch string) settingsRequest {
	req.ifMatch = ifMatch

	return req
}

// saveBody is the body of a save with one range, the upstream URL and the
// credential.
func saveBody() string {
	return `{"allowedRanges":["10.20.0.0/16"],"upstreamProxy":"` + settingsURL +
		`","upstreamCredential":{"username":"proxy","password":"` + hiddenValue + `"}}`
}

// TestPoolProxySettingsRoutes checks the read and the save as the
// administrator: the default, the save, the ETag, the write-only credential
// and the route table.
func TestPoolProxySettingsRoutes(t *testing.T) {
	t.Parallel()

	e := newSettingsEnv(t, identity.AdminOnly{})

	status, header, body := e.do(settingsRequest{method: http.MethodGet, body: "", contentType: "", ifMatch: ""})
	if status != http.StatusOK || header.Get("ETag") != `"0"` ||
		!strings.Contains(string(body), `"source":"default"`) || !strings.Contains(string(body), `"allowedRanges":[]`) {
		t.Fatalf("GET of a pool with no settings = %d %s %s, want 200, ETag \"0\" and the default",
			status, header.Get("ETag"), body)
	}

	saved := put(saveBody())
	saved.ifMatch = `"0"`

	status, header, body = e.do(saved)
	if status != http.StatusOK || header.Get("ETag") != `"1"` ||
		!strings.Contains(string(body), `"credentialSet":true`) {
		t.Fatalf("PUT = %d %s %s, want 200, ETag \"1\" and a credential", status, header.Get("ETag"), body)
	}

	// No credential field keeps the credential; null removes it.
	status, _, body = e.do(put(`{"allowedRanges":["10.20.0.0/16"],"upstreamProxy":"` + settingsURL + `"}`))
	if status != http.StatusOK || !strings.Contains(string(body), `"credentialSet":true`) {
		t.Errorf("PUT with no credential field = %d %s, want the credential kept", status, body)
	}

	status, _, body = e.do(put(`{"allowedRanges":[],"upstreamProxy":"","upstreamCredential":null}`))
	if status != http.StatusOK || !strings.Contains(string(body), `"credentialSet":false`) {
		t.Errorf("PUT with a null credential = %d %s, want no credential", status, body)
	}
}

// TestPoolProxySettingsRoutesHostile is the table of hostile requests of the
// handler layer. Each answer must hold no credential.
func TestPoolProxySettingsRoutesHostile(t *testing.T) {
	t.Parallel()

	readOnly := holdsOnly{actions: []identity.Action{
		identity.ActionNamespaceRead, identity.ActionNamespaceReadRunners,
	}}
	none := holdsOnly{actions: nil}
	admin := identity.AdminOnly{}

	long := `{"allowedRanges":["10.20.0.0/16"],"upstreamProxy":"` + settingsURL + `","x":"` +
		strings.Repeat("a", 20<<10) + `"}`

	cases := []struct {
		name   string
		authz  identity.Authorizer
		req    settingsRequest
		status int
		code   string
	}{
		{
			name: "PUT with the read grant only", authz: readOnly, req: put(saveBody()),
			status: http.StatusForbidden, code: "",
		},
		{name: "GET with no grant", authz: none, req: get(), status: http.StatusNotFound, code: ""},
		{name: "PUT with no grant", authz: none, req: put(saveBody()), status: http.StatusNotFound, code: ""},
		{
			name: "PUT of a loopback range", authz: admin,
			req:    put(`{"allowedRanges":["127.0.0.0/8"],"upstreamProxy":""}`),
			status: http.StatusBadRequest, code: errcode.RunnerInvalid,
		},
		{
			name: "PUT of an ftp URL", authz: admin,
			req:    put(`{"allowedRanges":[],"upstreamProxy":"ftp://proxy.example.com"}`),
			status: http.StatusBadRequest, code: errcode.RunnerInvalid,
		},
		{
			name: "PUT past 16 KB", authz: admin, req: put(long),
			status: http.StatusRequestEntityTooLarge, code: errcode.RequestBodyTooLarge,
		},
		{
			name: "PUT as text", authz: admin, req: withType(put(saveBody()), "text/plain"),
			status: http.StatusUnsupportedMediaType, code: errcode.RequestContentTypeUnsupported,
		},
		{
			name: "PUT with a stale If-Match", authz: admin, req: withIfMatch(put(saveBody()), `"7"`),
			status: http.StatusPreconditionFailed, code: errcode.RequestPreconditionFailed,
		},
		{
			name: "PUT with a bad If-Match", authz: admin, req: withIfMatch(put(saveBody()), "*"),
			status: http.StatusBadRequest, code: errcode.RequestIfMatchInvalid,
		},
		{
			name: "PUT with an unknown field", authz: admin,
			req:    put(`{"allowedRanges":[],"upstreamProxy":"","source":"set"}`),
			status: http.StatusBadRequest, code: errcode.RequestBodyInvalid,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newSettingsEnv(t, tc.authz)

			status, _, body := e.do(tc.req)
			if status != tc.status {
				t.Fatalf("answer = %d %s, want %d", status, body, tc.status)
			}

			if tc.code != "" && !strings.Contains(string(body), `"errorCode":"`+tc.code+`"`) {
				t.Errorf("answer = %s, want the code %s", body, tc.code)
			}

			if strings.Contains(string(body), hiddenValue) {
				t.Errorf("answer %s holds the password", body)
			}
		})
	}
}

// TestPoolProxySettingsRoutesHoldNoCredential reads the settings after a save
// and scans each answer for the credential.
func TestPoolProxySettingsRoutesHoldNoCredential(t *testing.T) {
	t.Parallel()

	e := newSettingsEnv(t, identity.AdminOnly{})

	_, _, saved := e.do(put(saveBody()))
	_, _, read := e.do(settingsRequest{method: http.MethodGet, body: "", contentType: "", ifMatch: ""})

	for _, body := range [][]byte{saved, read} {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(body, &fields); err != nil {
			t.Fatalf("decode %s: %v", body, err)
		}

		if _, ok := fields["upstreamCredential"]; ok || bytes.Contains(body, []byte(hiddenValue)) {
			t.Errorf("answer %s holds the credential, want credentialSet only", body)
		}
	}
}

// TestPoolProxySettingsRoutesActions checks the actions of the two routes in
// the route table.
func TestPoolProxySettingsRoutesActions(t *testing.T) {
	t.Parallel()

	h := newSettingsEnv(t, identity.AdminOnly{}).handler

	want := map[string]identity.Action{
		http.MethodGet + " /api/dustpan/v1/namespaces/{namespace}/runner-pools/{pool}/proxy-settings": identity.
			ActionNamespaceReadRunners,
		http.MethodPut + " /api/dustpan/v1/namespaces/{namespace}/runner-pools/{pool}/proxy-settings": identity.
			ActionNamespaceManageRunners,
	}

	found := 0

	for _, rt := range h.Routes() {
		action, ok := want[rt.Method+" "+rt.Path]
		if !ok {
			continue
		}

		found++

		if !slices.Equal(rt.Actions, []identity.Action{action}) || rt.Scope != runnerpools.ScopeAuthorized {
			t.Errorf("route %s %s declares %v, want %s", rt.Method, rt.Path, rt.Actions, action)
		}
	}

	if found != len(want) {
		t.Errorf("proxy settings routes = %d, want %d", found, len(want))
	}
}

// TestPoolProxySettingsRoutesEndpointChange is the table of the handler layer
// for a save that moves the upstream proxy and names no credential. An
// accepted move clears the credential, and the answer and the next read say
// so. A refused URL changes nothing, so the credential stays. This test fails
// when the handler or a layer below it keeps the credential for another
// endpoint.
func TestPoolProxySettingsRoutesEndpointChange(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		url    string
		status int
		kept   bool
	}{
		{"other host", "https://other.example.com:3128", http.StatusOK, false},
		{"other scheme", "http://upstream.example.com:3128", http.StatusOK, false},
		{"other port", "https://upstream.example.com:3129", http.StatusOK, false},
		{"default port instead of 3128", "https://upstream.example.com", http.StatusOK, false},
		{"Unicode look-alike", "https://upstrеam.example.com:3128", http.StatusOK, false},
		{"two trailing dots", "https://upstream.example.com..:3128", http.StatusOK, false},
		{"old host in the path", "https://evil.example.com/https://upstream.example.com:3128", http.StatusOK, false},
		{"same host, other case", "HTTPS://UPSTREAM.EXAMPLE.COM:3128", http.StatusOK, true},
		{"same host, trailing dot", "https://upstream.example.com.:3128", http.StatusOK, true},
		{"same host, redirect-like path", settingsURL + "/redirect?to=https://evil.example.com", http.StatusOK, true},
		{
			"user information trick",
			fakecred.URL("https", "upstream.example.com", "3128", "evil.example.com", ""),
			http.StatusBadRequest,
			true,
		},
		{"fragment trick", "https://evil.example.com#@upstream.example.com", http.StatusBadRequest, true},
		{"backslash trick", `https://evil.example.com\@upstream.example.com:3128`, http.StatusBadRequest, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkEndpointMove(t, tc.url, tc.status, tc.kept)
		})
	}
}

// checkEndpointMove saves the first settings, then a save to url with no
// credential field, and checks the status, the answer and the next read.
func checkEndpointMove(t *testing.T, url string, wantStatus int, kept bool) {
	t.Helper()

	e := newSettingsEnv(t, identity.AdminOnly{})

	if status, _, body := e.do(put(saveBody())); status != http.StatusOK {
		t.Fatalf("first PUT = %d %s, want 200", status, body)
	}

	quoted, err := json.Marshal(url)
	if err != nil {
		t.Fatalf("marshal URL: %v", err)
	}

	status, _, body := e.do(put(`{"allowedRanges":["10.20.0.0/16"],"upstreamProxy":` + string(quoted) + `}`))
	if status != wantStatus {
		t.Fatalf("PUT of %q = %d %s, want %d", url, status, body, wantStatus)
	}

	want := `"credentialSet":` + strconv.FormatBool(kept)
	if status == http.StatusOK && !strings.Contains(string(body), want) {
		t.Errorf("PUT of %q answer = %s, want %s", url, body, want)
	}

	status, _, body = e.do(get())
	if status != http.StatusOK || !strings.Contains(string(body), want) {
		t.Errorf("GET after a PUT of %q = %d %s, want %s", url, status, body, want)
	}

	if strings.Contains(string(body), hiddenValue) {
		t.Errorf("GET answer holds the credential: %s", body)
	}
}
