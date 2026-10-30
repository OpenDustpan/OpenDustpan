package tfe_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/tfe"
)

const (
	moduleVersionsPath = "/v1/modules/acme/vpc/aws/versions"
	moduleDownloadPath = "/v1/modules/acme/vpc/aws/1.0.0/download"
)

// moduleVersionWire is one version of the version list of the module protocol.
type moduleVersionWire struct {
	Version string `json:"version"`
}

// moduleListWire is one module in the version list.
type moduleListWire struct {
	Versions []moduleVersionWire `json:"versions"`
}

// moduleVersionsWire decodes the version list of the module protocol.
type moduleVersionsWire struct {
	Modules []moduleListWire `json:"modules"`
}

func TestModuleVersions(t *testing.T) {
	t.Parallel()

	f := newRegistryFixture(t, registryHost, allowAll())

	rec := f.get(t, moduleVersionsPath)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("versions = %d %s", rec.Code, rec.Body.String())
	}

	var doc moduleVersionsWire
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil ||
		len(doc.Modules) != 1 || len(doc.Modules[0].Versions) != 1 || doc.Modules[0].Versions[0].Version != "1.0.0" {
		t.Fatalf("versions = %s, %v", rec.Body.String(), err)
	}
}

func TestModuleDownload(t *testing.T) {
	t.Parallel()

	f := newRegistryFixture(t, registryHost, allowAll())

	rec := f.get(t, moduleDownloadPath)
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("download = %d %s, want 204 with no body", rec.Code, rec.Body.String())
	}

	u, err := url.Parse(rec.Header().Get("X-Terraform-Get"))
	if err != nil || u.Scheme != "https" || u.Host != registryHost ||
		!strings.HasPrefix(u.Path, "/v1/modules/acme/vpc/aws/1.0.0/files/"+moduleHash) {
		t.Fatalf("X-Terraform-Get = %q, %v", rec.Header().Get("X-Terraform-Get"), err)
	}

	// The archive member tells the client the format of the file.
	if u.Query().Get("archive") != "tar.gz" {
		t.Fatalf("archive member = %q, want tar.gz", u.Query().Get("archive"))
	}

	file := f.serve(t, regReq{host: registryHost, target: u.RequestURI(), token: "", headers: nil})
	if file.Code != http.StatusOK || file.Body.String() != "tgz!" {
		t.Fatalf("archive = %d %q", file.Code, file.Body.String())
	}
}

func TestModuleMissing(t *testing.T) {
	t.Parallel()

	f := newRegistryFixture(t, registryHost, allowAll())

	for _, target := range []string{
		"/v1/modules/acme/vpc/gcp/versions",
		"/v1/modules/acme/other/aws/versions",
		"/v1/modules/beta/vpc/aws/versions",
		"/v1/modules/acme/vpc/aws/9.9.9/download",
		"/v1/modules/acme/vpc/aws/..%2f1.0.0/download",
		"/v1/modules/acme/vpc/aws/1.0.0/other",
	} {
		rec := f.get(t, target)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"code":"`) {
			t.Errorf("GET %s = %d %s, want 404 in the registry error document", target, rec.Code, rec.Body.String())
		}
	}
}

// TestModuleHostGate sends hostile hosts straight to the module routes, as if
// every other layer had failed.
func TestModuleHostGate(t *testing.T) {
	t.Parallel()

	f := newRegistryFixture(t, registryHost, allowAll())

	for _, host := range []string{"evil.example.com", "registry.example.com.evil.com", "", "registry.example.com@evil", "registry.example.com:8443"} {
		for _, target := range []string{moduleVersionsPath, moduleDownloadPath} {
			rec := f.serve(t, regReq{host: host, target: target, token: "test-token", headers: nil})
			if rec.Code != http.StatusNotFound {
				t.Errorf("host %q GET %s = %d, want 404", host, target, rec.Code)
			}
		}
	}

	if len(f.audits.entries) == 0 {
		t.Fatal("a request for another host wrote no audit entry")
	}
}

func TestModuleAuthorization(t *testing.T) {
	t.Parallel()

	denied := newRegistryFixture(t, registryHost, stubAuthorizer{})

	for _, target := range []string{moduleVersionsPath, moduleDownloadPath} {
		if rec := denied.get(t, target); rec.Code != http.StatusForbidden {
			t.Errorf("GET %s without registry:read = %d, want 403", target, rec.Code)
		}

		rec := denied.serve(t, regReq{host: registryHost, target: target, token: "", headers: nil})
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without credentials = %d, want 401", target, rec.Code)
		}
	}
}

func TestModuleFileSignature(t *testing.T) {
	t.Parallel()

	f := newRegistryFixture(t, registryHost, allowAll())

	good, err := url.Parse(f.get(t, moduleDownloadPath).Header().Get("X-Terraform-Get"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	withQuery := func(mutate func(q url.Values)) string {
		q := url.Values{}
		for k, v := range good.Query() {
			q[k] = append([]string(nil), v...)
		}

		mutate(q)

		return good.Path + "?" + q.Encode()
	}

	// A signature that a provider file got for the same names is no signature of
	// a module file: the labels differ.
	provider := f.get(t, downloadPath)

	var dl downloadURLWire
	if err = json.Unmarshal(provider.Body.Bytes(), &dl); err != nil {
		t.Fatalf("download: %v", err)
	}

	providerURL, err := url.Parse(dl.DownloadURL)
	if err != nil {
		t.Fatalf("parse: %v", err)
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
		{"the signature for another file", strings.Replace(good.RequestURI(), moduleHash, zipHash, 1), 0},
		{"the signature for another version", strings.Replace(good.RequestURI(), "/1.0.0/", "/1.0.1/", 1), 0},
		{"the signature for another module", strings.Replace(good.RequestURI(), "/vpc/", "/net/", 1), 0},
		{"the signature for another system", strings.Replace(good.RequestURI(), "/aws/", "/gcp/", 1), 0},
		{"the signature for another namespace", strings.Replace(good.RequestURI(), "/acme/", "/beta/", 1), 0},
		{"a signature of a provider file", good.Path + "?" + providerURL.RawQuery, 0},
		{"a path that leaves the release", "/v1/modules/acme/vpc/aws/1.0.0/files/..%2f..%2fsecret?" + good.RawQuery, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newRegistryFixture(t, registryHost, allowAll())
			*h.clock = h.clock.Add(tc.advance)

			got := h.serve(t, regReq{host: registryHost, target: tc.target, token: "", headers: nil})
			if got.Code != http.StatusNotFound {
				t.Fatalf("%s = %d %s, want 404", tc.name, got.Code, got.Body.String())
			}
		})
	}
}

// TestModuleFileRouteChecksTheHostFirst proves that a correct signature on
// another host opens nothing.
func TestModuleFileRouteChecksTheHostFirst(t *testing.T) {
	t.Parallel()

	f := newRegistryFixture(t, registryHost, allowAll())

	u, err := url.Parse(f.get(t, moduleDownloadPath).Header().Get("X-Terraform-Get"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	got := f.serve(t, regReq{host: "evil.example.com", target: u.RequestURI(), token: "", headers: nil})
	if got.Code != http.StatusNotFound {
		t.Fatalf("file on another host = %d, want 404", got.Code)
	}
}
