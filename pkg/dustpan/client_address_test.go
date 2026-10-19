package dustpan_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// baseOptionCount is the count of base options in newClientAddrServer, so
// the slice fits one append of the extra options.
const baseOptionCount = 15

// newClientAddrServer returns a server with extra options and a JSON request
// log to buf at debug level, so the liveness probe leaves a line.
func newClientAddrServer(t *testing.T, buf *bytes.Buffer, extra ...dustpan.Option) *dustpan.Server {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	return newClientAddrServerOn(t, buf, st, extra...)
}

// newClientAddrServerOn is newClientAddrServer on the store st, so the test
// can read what the server wrote.
func newClientAddrServerOn(
	t *testing.T, buf *bytes.Buffer, st *sqlite.Store, extra ...dustpan.Option,
) *dustpan.Server {
	t.Helper()

	blobs, err := fs.Open(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	authn, err := identity.NewStaticToken("admin", adminToken)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	opts := make([]dustpan.Option, 0, baseOptionCount+len(extra))
	opts = append(opts,
		dustpan.WithRegion(region),
		dustpan.WithNamespaceStore(st),
		dustpan.WithStackStore(st),
		dustpan.WithUpdateStore(st),
		dustpan.WithCheckpointStore(st),
		dustpan.WithEventStore(st),
		dustpan.WithSecretKeyStore(st),
		dustpan.WithSecretWrapper(wrapper),
		dustpan.WithWorkspaceStore(st),
		dustpan.WithStateStore(st),
		dustpan.WithBlobStore(blobs),
		dustpan.WithInventoryReader(st),
		dustpan.WithAuthenticator(authn),
		dustpan.WithLogger(logger),
		dustpan.WithClock(func() time.Time { return time.Date(2026, time.October, 8, 21, 0, 0, 0, time.UTC) }),
	)
	opts = append(opts, extra...)

	srv, err := dustpan.NewServer(opts...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	return srv
}

// clientIPCase is one request through the client address server: the
// options, the peer, the forwarding headers, the logged address and a header
// value that no log field may hold. wantMissing means the request line holds
// no client_ip field at all.
type clientIPCase struct {
	name        string
	extra       []dustpan.Option
	remote      string
	xff         string
	fwd         string
	want        string
	wantMissing bool
	absent      string
}

// checkClientIPLog serves one request and proves the client_ip field of its
// request line.
func checkClientIPLog(t *testing.T, tc clientIPCase) {
	t.Helper()

	var buf bytes.Buffer

	srv := newClientAddrServer(t, &buf, tc.extra...)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health/live", nil)
	req.RemoteAddr = tc.remote

	if tc.xff != "" {
		req.Header.Set("X-Forwarded-For", tc.xff)
	}

	if tc.fwd != "" {
		req.Header.Set("Forwarded", tc.fwd)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("liveness status %d, want %d", rec.Code, http.StatusNoContent)
	}

	lines := requestLines(t, buf.String())
	if len(lines) != 1 {
		t.Fatalf("log lines = %d, want 1", len(lines))
	}

	entry := lines[0]

	got, ok := entry["client_ip"]
	if tc.wantMissing {
		if ok {
			t.Fatalf("client_ip = %v, want none", got)
		}
	} else if !ok || got != tc.want {
		t.Fatalf("client_ip = %v, want %q", entry["client_ip"], tc.want)
	}

	checkNoHeaderLeak(t, entry, tc.absent)
}

// checkNoHeaderLeak proves no field of the parsed log line holds leaked.
func checkNoHeaderLeak(t *testing.T, entry map[string]any, leaked string) {
	t.Helper()

	if leaked == "" {
		return
	}

	for key, value := range entry {
		text, ok := value.(string)
		if !ok {
			continue
		}

		if strings.Contains(text, leaked) {
			t.Fatalf("log field %q holds the header value %q", key, leaked)
		}
	}
}

// TestRequestLogClientIP proves the client_ip field of the request line:
// an untrusted peer keeps its own address despite a spoofed header, a
// trusted peer resolves to the forwarded client, a garbage header logs no
// address at all, and a server without the option ignores headers.
func TestRequestLogClientIP(t *testing.T) {
	t.Parallel()

	trusted := netip.MustParsePrefix("10.0.0.0/8")

	cases := []clientIPCase{
		{
			name:        "untrusted peer spoofed header logs peer",
			extra:       []dustpan.Option{dustpan.WithTrustedProxies(trusted)},
			remote:      "198.51.100.7:1234",
			xff:         "203.0.113.9",
			fwd:         "",
			want:        "198.51.100.7",
			wantMissing: false,
			absent:      "203.0.113.9",
		},
		{
			name:        "trusted peer forwarded client logs client",
			extra:       []dustpan.Option{dustpan.WithTrustedProxies(trusted)},
			remote:      "10.1.2.3:1234",
			xff:         "203.0.113.9, 10.9.9.9",
			fwd:         "",
			want:        "203.0.113.9",
			wantMissing: false,
			absent:      "",
		},
		{
			name:        "trusted peer garbage header logs no address",
			extra:       []dustpan.Option{dustpan.WithTrustedProxies(trusted)},
			remote:      "10.1.2.3:1234",
			xff:         "not-an-ip",
			fwd:         "",
			want:        "",
			wantMissing: true,
			absent:      "10.1.2.3",
		},
		{
			name: "forwarded setting reads forwarded only",
			extra: []dustpan.Option{
				dustpan.WithTrustedProxies(trusted),
				dustpan.WithTrustedProxyHeader("forwarded"),
			},
			remote:      "10.1.2.3:1234",
			xff:         "6.6.6.6",
			fwd:         "for=203.0.113.9",
			want:        "203.0.113.9",
			wantMissing: false,
			absent:      "6.6.6.6",
		},
		{
			name:        "no option ignores header",
			extra:       nil,
			remote:      "10.1.2.3:1234",
			xff:         "203.0.113.9",
			fwd:         "",
			want:        "10.1.2.3",
			wantMissing: false,
			absent:      "203.0.113.9",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkClientIPLog(t, tc)
		})
	}
}

// missingHeaderText is the warning the server writes when a trusted peer
// sends no forwarding header.
const missingHeaderText = "a trusted proxy sent no forwarding header: " +
	"check --trusted-proxies and --trusted-proxy-header"

// countMissingHeaderWarns counts the missing-header warnings in raw and
// proves each one names the peer and the header, never a header value.
func countMissingHeaderWarns(t *testing.T, raw string) int {
	t.Helper()

	count := 0

	for line := range strings.Lines(strings.TrimSpace(raw)) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}

		if entry["msg"] != missingHeaderText {
			continue
		}

		count++

		if entry["peer"] != "10.1.2.3" {
			t.Fatalf("warn peer = %v, want 10.1.2.3", entry["peer"])
		}

		if entry["header"] != "x-forwarded-for" {
			t.Fatalf("warn header = %v, want x-forwarded-for", entry["header"])
		}
	}

	return count
}

// serveLiveness serves one liveness probe from remote through srv.
func serveLiveness(t *testing.T, srv *dustpan.Server, remote string) {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health/live", nil)
	req.RemoteAddr = remote

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("liveness status %d, want %d", rec.Code, http.StatusNoContent)
	}
}

// TestRequestLogMissingHeaderWarnsOnce proves a trusted peer with no
// forwarding header logs no client_ip and one warning, and a second request
// inside the minute logs no second warning.
func TestRequestLogMissingHeaderWarnsOnce(t *testing.T) {
	t.Parallel()

	trusted := netip.MustParsePrefix("10.0.0.0/8")

	var buf bytes.Buffer

	srv := newClientAddrServer(t, &buf, dustpan.WithTrustedProxies(trusted))

	serveLiveness(t, srv, "10.1.2.3:1234")

	lines := requestLines(t, buf.String())
	if len(lines) != 1 {
		t.Fatalf("log lines = %d, want 1", len(lines))
	}

	if got, ok := lines[0]["client_ip"]; ok {
		t.Fatalf("client_ip = %v, want none", got)
	}

	if got := countMissingHeaderWarns(t, buf.String()); got != 1 {
		t.Fatalf("warnings = %d, want 1", got)
	}

	serveLiveness(t, srv, "10.1.2.3:1234")

	lines = requestLines(t, buf.String())
	if len(lines) != 2 {
		t.Fatalf("log lines = %d, want 2", len(lines))
	}

	if got := countMissingHeaderWarns(t, buf.String()); got != 1 {
		t.Fatalf("warnings = %d, want still 1", got)
	}
}

// TestNewServerRefusesBadProxySettings proves NewServer fails with a proxy
// list that covers every address, with host bits, or with an unknown header.
func TestNewServerRefusesBadProxySettings(t *testing.T) {
	t.Parallel()

	st, blobs, authn := requireServerPorts(t)
	full := fullServerOptions(t, st, blobs, authn)

	cases := []struct {
		name  string
		extra dustpan.Option
		want  string
	}{
		{
			name:  "zero prefix",
			extra: dustpan.WithTrustedProxies(netip.MustParsePrefix("0.0.0.0/0")),
			want:  "covers every address",
		},
		{
			name:  "host bits",
			extra: dustpan.WithTrustedProxies(netip.MustParsePrefix("10.0.0.1/8")),
			want:  "has host bits set",
		},
		{
			name:  "unknown header",
			extra: dustpan.WithTrustedProxyHeader("x-real-ip"),
			want:  "is not x-forwarded-for or forwarded",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			opts := append(append([]dustpan.Option(nil), full...), tc.extra)

			if _, err := dustpan.NewServer(opts...); err == nil {
				t.Fatalf("NewServer with %s succeeded", tc.name)
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("NewServer with %s: err = %v, want %q", tc.name, err, tc.want)
			}
		})
	}
}

// TestCollectionAuditSourceIPBehindProxy proves a collection change records
// the client address that a trusted proxy forwards, not the proxy address.
func TestCollectionAuditSourceIPBehindProxy(t *testing.T) {
	t.Parallel()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	ns := createChainNamespace(t, st)

	var buf bytes.Buffer

	srv := newClientAddrServerOn(t, &buf, st,
		dustpan.WithTrustedProxies(netip.MustParsePrefix("10.0.0.0/8")))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/dustpan/v1/namespaces/"+ns.Name.String()+"/collections", strings.NewReader(`{"name":"payments"}`))
	req.RemoteAddr = "10.1.2.3:4444"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	req.Header.Set("Authorization", "token "+adminToken)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create collection status %d, want %d: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	entries := listScope(t, st, ns.Name)
	if len(entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(entries))
	}

	if got := entries[0].SourceIP; got != "203.0.113.9" {
		t.Fatalf("audit source IP = %q, want the forwarded client 203.0.113.9", got)
	}
}
