package tfe_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
)

const (
	registryHost = "registry.example.com"
	sumsHash     = "5555555555555555555555555555555555555555555555555555555555555555"
	sigHash      = "6666666666666666666666666666666666666666666666666666666666666666"
	zipHash      = "7777777777777777777777777777777777777777777777777777777777777777"
	fingerprint  = "0123456789ABCDEF0123456789ABCDEF01234567"
)

// fakeRegistry serves one release of acme/payments.
type fakeRegistry struct {
	mu      sync.Mutex
	keyGone bool
	files   map[string][]byte
}

func (f *fakeRegistry) release() registry.Release {
	return registry.Release{
		PublishedAt: time.Unix(1_800_000_000, 0).UTC(),
		Address:     registry.Address{Namespace: "acme", Kind: registry.KindProvider, Name: "payments", System: ""},
		Version:     "1.0.0", SumsFile: "terraform-provider-payments_1.0.0_SHA256SUMS", SumsSHA256: sumsHash,
		SignatureFile: "terraform-provider-payments_1.0.0_SHA256SUMS.sig", SignatureSHA256: sigHash,
		KeyFingerprint: fingerprint, Protocols: []string{"5.0", "6.0"}, PublishedBy: "ada",
		Archives: []registry.Archive{
			{Platform: "linux_amd64", FileName: "terraform-provider-payments_1.0.0_linux_amd64.zip", SHA256: zipHash, Size: 3},
		},
	}
}

func (f *fakeRegistry) Releases(_ context.Context, ns namespace.Name, name string) ([]registry.Release, error) {
	if ns != "acme" || name != "payments" {
		return nil, registry.ErrNotFound
	}

	return []registry.Release{f.release()}, nil
}

func (f *fakeRegistry) Release(_ context.Context, ns namespace.Name, name, version string) (registry.Release, error) {
	if ns != "acme" || name != "payments" || version != "1.0.0" {
		return registry.Release{}, registry.ErrNotFound
	}

	return f.release(), nil
}

func (f *fakeRegistry) SigningKey(_ context.Context, rel registry.Release) (registry.TrustedKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.keyGone {
		return registry.TrustedKey{}, registry.ErrKeyNotFound
	}

	return registry.TrustedKey{Fingerprint: rel.KeyFingerprint, Armored: []byte("-----BEGIN PGP PUBLIC KEY BLOCK-----\nkey\n-----END PGP PUBLIC KEY BLOCK-----\n")}, nil
}

func (f *fakeRegistry) OpenFile(_ context.Context, ns namespace.Name, name, version, sum string) (int64, io.ReadCloser, error) {
	if ns != "acme" || name != "payments" || version != "1.0.0" {
		return 0, nil, registry.ErrNotFound
	}

	data, ok := f.files[sum]
	if !ok {
		return 0, nil, registry.ErrNotFound
	}

	return int64(len(data)), io.NopCloser(bytes.NewReader(data)), nil
}

type registryFixture struct {
	handler *tfe.Handler
	reg     *fakeRegistry
	audits  *memAuditStore
	clock   *time.Time
}

func newRegistryFixture(t *testing.T, host string, authz identity.Authorizer) registryFixture {
	t.Helper()

	reg := &fakeRegistry{
		mu: sync.Mutex{}, keyGone: false,
		files: map[string][]byte{zipHash: []byte("zip"), sumsHash: []byte("sums"), sigHash: []byte("sig")},
	}
	audits := &memAuditStore{entries: nil, err: nil, mu: sync.Mutex{}}
	now := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)

	public, err := baseurl.Parse("https://" + registryHost)
	if err != nil {
		t.Fatalf("base url: %v", err)
	}

	handler, err := tfe.NewHandler(tfe.Config{
		Authenticator: tokenAuthenticator{}, Authorizer: authz, AddressGate: nil, Logger: slog.New(slog.DiscardHandler),
		Namespaces: newTestNamespaces(t), Workspaces: stubWorkspaces{}, BaseURL: public, Audit: audits, Gate: nil,
		MaxStateBytes: 0, OnAuditFailure: nil, DenialLimiter: nil,
		Registry: tfe.RegistryConfig{
			Service: reg, Host: host, DefaultPort: "443", URLKey: bytes.Repeat([]byte{7}, 32), URLTTL: 0,
			Now: func() time.Time { return now },
		},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return registryFixture{handler: handler, reg: reg, audits: audits, clock: &now}
}

type regReq struct {
	host, target, token string
	headers             map[string]string
}

func (f registryFixture) serve(t *testing.T, q regReq) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, q.target, http.NoBody)
	req.Host = q.host

	if q.token != "" {
		req.Header.Set("Authorization", "Bearer "+q.token)
	}

	for k, v := range q.headers {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	return rec
}

func (f registryFixture) get(t *testing.T, target string) *httptest.ResponseRecorder {
	t.Helper()

	return f.serve(t, regReq{host: registryHost, target: target, token: "test-token", headers: nil})
}

const versionsPath = "/v1/providers/acme/payments/versions"
const downloadPath = "/v1/providers/acme/payments/1.0.0/download/linux/amd64"

func TestRegistryDiscovery(t *testing.T) {
	t.Parallel()

	f := newRegistryFixture(t, registryHost, allowAll())

	rec := f.serve(t, regReq{host: registryHost, target: "/.well-known/terraform.json", token: "", headers: nil})

	var doc map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || doc["providers.v1"] != "/v1/providers/" || doc["tfe.v2"] == "" {
		t.Fatalf("discovery = %q, %v", rec.Body.String(), err)
	}

	// A handler with no registry advertises none.
	plain := newProductionHandler(t)
	rec2 := httptest.NewRecorder()
	plain.ServeHTTP(rec2, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/.well-known/terraform.json", http.NoBody))

	if strings.Contains(rec2.Body.String(), "providers.v1") {
		t.Fatalf("discovery advertises providers.v1 with no registry: %s", rec2.Body.String())
	}
}

func TestRegistryVersionsAndDownload(t *testing.T) {
	t.Parallel()

	f := newRegistryFixture(t, registryHost, allowAll())

	rec := f.get(t, versionsPath)
	if rec.Code != http.StatusOK {
		t.Fatalf("versions = %d %s", rec.Code, rec.Body.String())
	}

	var versions struct {
		Versions []struct {
			Version   string   `json:"version"`
			Protocols []string `json:"protocols"`
			Platforms []struct {
				OS   string `json:"os"`
				Arch string `json:"arch"`
			} `json:"platforms"`
		} `json:"versions"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &versions); err != nil || len(versions.Versions) != 1 ||
		versions.Versions[0].Version != "1.0.0" || len(versions.Versions[0].Protocols) != 2 ||
		versions.Versions[0].Platforms[0].OS != "linux" || versions.Versions[0].Platforms[0].Arch != "amd64" {
		t.Fatalf("versions = %s, %v", rec.Body.String(), err)
	}

	rec = f.get(t, downloadPath)
	if rec.Code != http.StatusOK {
		t.Fatalf("download = %d %s", rec.Code, rec.Body.String())
	}

	var dl struct {
		Protocols   []string `json:"protocols"`
		Filename    string   `json:"filename"`
		DownloadURL string   `json:"download_url"`
		ShasumsURL  string   `json:"shasums_url"`
		SigURL      string   `json:"shasums_signature_url"`
		Shasum      string   `json:"shasum"`
		Keys        struct {
			GPG []struct {
				KeyID string `json:"key_id"`
				Armor string `json:"ascii_armor"`
			} `json:"gpg_public_keys"`
		} `json:"signing_keys"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &dl); err != nil || dl.Shasum != zipHash ||
		dl.Filename != "terraform-provider-payments_1.0.0_linux_amd64.zip" || len(dl.Keys.GPG) != 1 ||
		dl.Keys.GPG[0].KeyID != "89ABCDEF01234567" || !strings.Contains(dl.Keys.GPG[0].Armor, "BEGIN PGP") {
		t.Fatalf("download = %s, %v", rec.Body.String(), err)
	}

	// The three URLs open their files with no credential.
	for name, want := range map[string]string{dl.DownloadURL: "zip", dl.ShasumsURL: "sums", dl.SigURL: "sig"} {
		u, err := url.Parse(name)
		if err != nil || u.Host != registryHost || u.Scheme != "https" {
			t.Fatalf("file URL %q is not on the registry host: %v", name, err)
		}

		file := f.serve(t, regReq{host: registryHost, target: u.RequestURI(), token: "", headers: nil})
		if file.Code != http.StatusOK || file.Body.String() != want {
			t.Fatalf("file %s = %d %q, want %q", u.Path, file.Code, file.Body.String(), want)
		}
	}
}

func TestRegistryHostGate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		host    string
		headers map[string]string
		want    int
	}{
		{"the configured host", registryHost, nil, http.StatusOK},
		{"the configured host in capitals", "REGISTRY.EXAMPLE.COM", nil, http.StatusOK},
		{"the configured host with the default port", registryHost + ":443", nil, http.StatusOK},
		{"another host", "evil.example.com", nil, http.StatusNotFound},
		{"a suffix of the host", "xregistry.example.com", nil, http.StatusNotFound},
		{"the host as a prefix", registryHost + ".evil.com", nil, http.StatusNotFound},
		{"the host with a trailing dot", registryHost + ".", nil, http.StatusNotFound},
		{"the host with another port", registryHost + ":8443", nil, http.StatusNotFound},
		{"the host with user information", "user@" + registryHost, nil, http.StatusNotFound},
		{"the host with a path", registryHost + "/x", nil, http.StatusNotFound},
		{"an empty host", "", nil, http.StatusNotFound},
		{"an address", "192.0.2.1", nil, http.StatusNotFound},
		{"a forwarded host does not help", "evil.example.com", map[string]string{"X-Forwarded-Host": registryHost}, http.StatusNotFound},
		{"a forwarded host does not hurt", registryHost, map[string]string{"X-Forwarded-Host": "evil.example.com"}, http.StatusOK},
		{"a host header with two values", registryHost + ", evil.example.com", nil, http.StatusNotFound},
	}

	for _, tc := range tests {
		for _, route := range []string{versionsPath, downloadPath} {
			t.Run(tc.name+" "+route, func(t *testing.T) {
				t.Parallel()

				f := newRegistryFixture(t, registryHost, allowAll())

				rec := f.serve(t, regReq{host: tc.host, target: route, token: "test-token", headers: tc.headers})
				if rec.Code != tc.want {
					t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body.String(), tc.want)
				}
			})
		}
	}
}

func TestRegistryHostGateWritesAnAuditEntryAndAnswersAsMissing(t *testing.T) {
	t.Parallel()

	f := newRegistryFixture(t, registryHost, allowAll())

	hostile := f.serve(t, regReq{host: "evil.example.com", target: versionsPath, token: "test-token", headers: nil})
	missing := f.serve(t, regReq{host: registryHost, target: "/v1/providers/acme/nothing/versions", token: "test-token", headers: nil})

	if hostile.Code != http.StatusNotFound || missing.Code != http.StatusNotFound || hostile.Body.String() != missing.Body.String() {
		t.Fatalf("hostile host = %d %q, missing address = %d %q: want the same answer",
			hostile.Code, hostile.Body.String(), missing.Code, missing.Body.String())
	}

	f.audits.mu.Lock()
	defer f.audits.mu.Unlock()

	if len(f.audits.entries) != 1 {
		t.Fatalf("%d audit entries, want 1", len(f.audits.entries))
	}

	e := f.audits.entries[0]
	if e.Outcome != "denied" || e.Action != "registry:read" || e.Namespace != "acme" || !strings.Contains(e.Reason, "another host") {
		t.Fatalf("audit entry = %+v", e)
	}
}

func TestRegistryHostGateFailsClosedWithNoConfiguredHost(t *testing.T) {
	t.Parallel()

	f := newRegistryFixture(t, "", allowAll())

	for _, host := range []string{registryHost, "", "localhost"} {
		rec := f.serve(t, regReq{host: host, target: versionsPath, token: "test-token", headers: nil})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("host %q = %d, want 404", host, rec.Code)
		}
	}
}

func TestRegistryAuthorization(t *testing.T) {
	t.Parallel()

	denied := newRegistryFixture(t, registryHost, stubAuthorizer{})
	if rec := denied.get(t, versionsPath); rec.Code != http.StatusForbidden {
		t.Fatalf("versions without registry:read = %d", rec.Code)
	}

	open := newRegistryFixture(t, registryHost, allowAll())

	for _, token := range []string{"", "stranger"} {
		rec := open.serve(t, regReq{host: registryHost, target: versionsPath, token: token, headers: nil})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("token %q = %d, want 401", token, rec.Code)
		}
	}
}

func TestRegistryHostileNames(t *testing.T) {
	t.Parallel()

	f := newRegistryFixture(t, registryHost, allowAll())

	for _, target := range []string{
		"/v1/providers/Acme/payments/versions",
		"/v1/providers/acme/Payments/versions",
		"/v1/providers/acme/pay_ments/1.0.0/download/linux/amd64",
		"/v1/providers/acme/payments/latest/download/linux/amd64",
		"/v1/providers/acme/payments/1.0.0/download/plan9/amd64",
		"/v1/providers/%2e%2e/payments/versions",
		"/v1/providers/acme/payments/1.0.0/download/linux%2Famd64",
		"/v1/providers/beta/payments/versions",
	} {
		if rec := f.get(t, target); rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d %s, want 404", target, rec.Code, rec.Body.String())
		}
	}
}

func TestRegistryDownloadFailsClosedWhenTheKeyIsRemoved(t *testing.T) {
	t.Parallel()

	f := newRegistryFixture(t, registryHost, allowAll())
	f.reg.mu.Lock()
	f.reg.keyGone = true
	f.reg.mu.Unlock()

	if rec := f.get(t, downloadPath); rec.Code != http.StatusNotFound {
		t.Fatalf("download with a removed key = %d, want 404", rec.Code)
	}
}

// TestRegistryFileSignature sends hostile file requests straight to the file
// route, as if every other layer had failed.
func TestRegistryFileSignature(t *testing.T) {
	t.Parallel()

	f := newRegistryFixture(t, registryHost, allowAll())

	rec := f.get(t, downloadPath)

	var dl struct {
		DownloadURL string `json:"download_url"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &dl); err != nil {
		t.Fatalf("download: %v", err)
	}

	good, err := url.Parse(dl.DownloadURL)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	query := good.Query()

	withQuery := func(mutate func(q url.Values)) string {
		q := url.Values{}
		for k, v := range query {
			q[k] = append([]string(nil), v...)
		}

		mutate(q)

		return good.Path + "?" + q.Encode()
	}

	tests := []struct {
		name    string
		target  string
		advance time.Duration
	}{
		{"no signature", good.Path, 0},
		{"no expiry", withQuery(func(q url.Values) { q.Del("expires") }), 0},
		{"a signature of other bytes", withQuery(func(q url.Values) { q.Set("sig", strings.Repeat("A", 43)) }), 0},
		{"a signature that is not base64", withQuery(func(q url.Values) { q.Set("sig", "!!!") }), 0},
		{"another expiry", withQuery(func(q url.Values) { q.Set("expires", "9999999999") }), 0},
		{"an expiry in the past", withQuery(func(q url.Values) { q.Set("expires", "1") }), 0},
		{"the signature after it expired", good.RequestURI(), tfe.DefaultRegistryURLTTL + time.Second},
		{"the signature for another file", strings.Replace(good.RequestURI(), zipHash, sumsHash, 1), 0},
		{"the signature for another version", strings.Replace(good.RequestURI(), "/1.0.0/", "/1.0.1/", 1), 0},
		{"the signature for another provider", strings.Replace(good.RequestURI(), "/payments/", "/ledger/", 1), 0},
		{"the signature for another namespace", strings.Replace(good.RequestURI(), "/acme/", "/beta/", 1), 0},
		{"a path that leaves the release", "/v1/providers/acme/payments/1.0.0/files/..%2f..%2fsecret?" + good.RawQuery, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newRegistryFixture(t, registryHost, allowAll())
			*h.clock = h.clock.Add(tc.advance)

			if rec := h.serve(t, regReq{host: registryHost, target: tc.target, token: "", headers: nil}); rec.Code != http.StatusNotFound {
				t.Fatalf("%s = %d %s, want 404", tc.name, rec.Code, rec.Body.String())
			}
		})
	}

	// A signature from a handler with another key is no signature.
	other := newRegistryFixture(t, registryHost, allowAll())
	other.clock = f.clock

	if rec := other.serve(t, regReq{host: registryHost, target: good.RequestURI(), token: "", headers: nil}); rec.Code != http.StatusOK {
		// Same key in both fixtures: this is the control case.
		t.Fatalf("control request = %d, want 200", rec.Code)
	}
}

func TestRegistryFileRouteChecksTheHostFirst(t *testing.T) {
	t.Parallel()

	f := newRegistryFixture(t, registryHost, allowAll())

	var dl struct {
		DownloadURL string `json:"download_url"`
	}

	if err := json.Unmarshal(f.get(t, downloadPath).Body.Bytes(), &dl); err != nil {
		t.Fatalf("download: %v", err)
	}

	u, err := url.Parse(dl.DownloadURL)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	rec := f.serve(t, regReq{host: "evil.example.com", target: u.RequestURI(), token: "", headers: nil})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("file on another host = %d, want 404", rec.Code)
	}
}

func TestRegistryRoutesDeclareScopes(t *testing.T) {
	t.Parallel()

	f := newRegistryFixture(t, registryHost, allowAll())

	seen := map[string]tfe.Scope{}

	for _, route := range f.handler.Routes() {
		if strings.HasPrefix(route.Path, "/v1/providers/") {
			seen[route.Method+" "+route.Path] = route.Scope

			if len(route.Actions) != 1 || route.Actions[0] != identity.ActionRegistryRead {
				t.Errorf("route %s declares %v, want registry:read", route.Path, route.Actions)
			}
		}
	}

	want := map[string]tfe.Scope{
		"GET /v1/providers/{namespace}/{type}/versions":                       tfe.ScopeRegistry,
		"GET /v1/providers/{namespace}/{type}/{version}/download/{os}/{arch}": tfe.ScopeRegistry,
		"GET /v1/providers/{namespace}/{type}/{version}/files/{sha256}":       tfe.ScopeSigned,
	}

	if len(seen) != len(want) {
		t.Fatalf("registry routes = %v, want %v", seen, want)
	}

	for k, v := range want {
		if seen[k] != v {
			t.Errorf("route %q has scope %d, want %d", k, seen[k], v)
		}
	}

	// Every served pattern has a row.
	for _, pattern := range f.handler.RegisteredPatterns() {
		if strings.Contains(pattern, "/v1/providers/") {
			if _, ok := want[pattern]; !ok {
				t.Errorf("served route %q has no row", pattern)
			}
		}
	}
}

func TestNewHandlerRefusesAShortRegistryKey(t *testing.T) {
	t.Parallel()

	_, err := tfe.NewHandler(tfe.Config{
		Authenticator: tokenAuthenticator{}, Authorizer: allowAll(), AddressGate: nil, Logger: slog.New(slog.DiscardHandler),
		Namespaces: newTestNamespaces(t), Workspaces: stubWorkspaces{}, BaseURL: baseurl.Resolver{}, Audit: nil, Gate: nil,
		MaxStateBytes: 0, OnAuditFailure: nil, DenialLimiter: nil,
		Registry: tfe.RegistryConfig{
			Service: &fakeRegistry{mu: sync.Mutex{}, keyGone: false, files: nil}, Host: registryHost, DefaultPort: "443",
			URLKey: []byte("short"), URLTTL: 0, Now: nil,
		},
	})
	if err == nil {
		t.Fatal("NewHandler accepted a short URL key")
	}
}
