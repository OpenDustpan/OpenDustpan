package dustpan_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"opendustpan.org/opendustpan/pkg/dustpan"
)

// TestRecordedAddressIgnoresUntrustedHeader proves that the installation
// list checks the address that trusted-proxy handling records, not a header
// from an untrusted peer. A header from a trusted peer still changes the
// recorded address.
func TestRecordedAddressIgnoresUntrustedHeader(t *testing.T) {
	t.Parallel()

	p := newPorts(t)

	srv, err := dustpan.NewServer(append(
		fullServerOptions(t, p.st, p.blobs, p.authn),
		dustpan.WithSettings(p.st.SettingStore(), nil, nil),
		dustpan.WithIdempotencyStore(p.st.Idempotency(testWrapper(t))),
		dustpan.WithTrustedProxies(netip.MustParsePrefix("10.0.0.0/8")),
	)...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	handler := startedHandler(t, srv)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	createAuditNamespace(t, p.st)

	_, etag := readSettings(t, ts.URL)

	status, body := patchSettings(t, ts.URL, etag,
		`{"ipAllowlist.enabled":true,"ipAllowlist.installation":"198.51.100.0/24"}`)
	if status != http.StatusOK {
		t.Fatalf("PATCH the installation list = %d %s, want 200", status, body)
	}

	send := func(remote, forwarded string) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodGet, "/api/dustpan/v1/namespaces/acme/units", http.NoBody,
		)
		req.RemoteAddr = remote
		req.Header.Set("X-Forwarded-For", forwarded)
		req.Header.Set("Authorization", "Bearer "+adminToken)

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		return rec
	}

	untrusted := send("203.0.113.10:1234", "198.51.100.7")
	if untrusted.Code != http.StatusForbidden {
		t.Fatalf("untrusted peer with a spoofed header = %d %s, want 403", untrusted.Code, untrusted.Body.String())
	}

	var answer struct {
		ErrorCode string         `json:"errorCode"`
		ErrorArgs map[string]any `json:"errorArgs"`
	}

	if err := json.Unmarshal(untrusted.Body.Bytes(), &answer); err != nil {
		t.Fatalf("decode the refusal: %v: %s", err, untrusted.Body.String())
	}

	if answer.ErrorCode != "ip-allowlist.denied" || answer.ErrorArgs["scope"] != "installation" {
		t.Fatalf("refusal = %s, want ip-allowlist.denied with scope installation", untrusted.Body.String())
	}

	trusted := send("10.1.2.3:1234", "198.51.100.7")
	if trusted.Code != http.StatusOK {
		t.Fatalf("trusted peer with a forwarded address = %d %s, want 200", trusted.Code, trusted.Body.String())
	}
}
