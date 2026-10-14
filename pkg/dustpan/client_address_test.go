package dustpan_test

import (
	"bytes"
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
// options, the peer, the spoofed header, the logged address and a header
// value that no log field may hold.
type clientIPCase struct {
	name   string
	extra  []dustpan.Option
	remote string
	xff    string
	want   string
	absent string
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

	got, ok := entry["client_ip"].(string)
	if !ok || got != tc.want {
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
// trusted peer resolves to the forwarded client, and a server without the
// option ignores headers.
func TestRequestLogClientIP(t *testing.T) {
	t.Parallel()

	trusted := netip.MustParsePrefix("10.0.0.0/8")

	cases := []clientIPCase{
		{
			name:   "untrusted peer spoofed header logs peer",
			extra:  []dustpan.Option{dustpan.WithTrustedProxies(trusted)},
			remote: "198.51.100.7:1234",
			xff:    "203.0.113.9",
			want:   "198.51.100.7",
			absent: "203.0.113.9",
		},
		{
			name:   "trusted peer forwarded client logs client",
			extra:  []dustpan.Option{dustpan.WithTrustedProxies(trusted)},
			remote: "10.1.2.3:1234",
			xff:    "203.0.113.9, 10.9.9.9",
			want:   "203.0.113.9",
			absent: "",
		},
		{
			name:   "no option ignores header",
			extra:  nil,
			remote: "10.1.2.3:1234",
			xff:    "203.0.113.9",
			want:   "10.1.2.3",
			absent: "203.0.113.9",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkClientIPLog(t, tc)
		})
	}
}
