package main

import (
	"context"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/egress/egresshttp"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/logsink"
	"opendustpan.org/opendustpan/pkg/logsink/webhook"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// noEnv is a lookup that finds no variable.
func noEnv(string) (string, bool) { return "", false }

// envOf returns a lookup that finds only the given variables.
func envOf(vars map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, ok := vars[name]

		return v, ok
	}
}

// openTestSinks opens items against a fresh blob directory and closes the
// sinks when the test ends.
func openTestSinks(t *testing.T, items []string, lookup func(string) (string, bool)) (openedSinks, error) {
	t.Helper()

	blobs, err := fs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	sinks, err := openAuditSinks(items, blobs, lookup, nil)
	if err == nil {
		t.Cleanup(func() {
			if closeErr := sinks.close(); closeErr != nil {
				t.Errorf("close sinks: %v", closeErr)
			}
		})
	}

	return sinks, err
}

func TestAuditSinksEmptyValueOpensNothing(t *testing.T) {
	t.Parallel()

	sinks, err := openTestSinks(t, []string{" ", ""}, noEnv)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	if len(sinks.ship) != 0 {
		t.Fatalf("sinks: got %d, want 0", len(sinks.ship))
	}
}

func TestAuditSinksNamesKindsAndIntervals(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "audit.jsonl")
	items := []string{
		"file:" + path,
		" siem=syslog+tcp://127.0.0.1:6514?facility=16&max-size=2048 ",
		"archive=blob:audit",
	}

	sinks, err := openTestSinks(t, items, noEnv)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	got := make([]string, 0, len(sinks.ship))
	for _, s := range sinks.ship {
		got = append(got, s.Name+"@"+s.Interval.String())
	}

	want := "file@0s siem@0s archive@" + blobSinkInterval.String()
	if strings.Join(got, " ") != want {
		t.Fatalf("sinks: got %v, want %s", got, want)
	}

	if len(sinks.files) != 1 {
		t.Fatalf("file sinks: got %d, want 1", len(sinks.files))
	}

	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("the file sink did not create its file: %v", statErr)
	}
}

func TestAuditSinkNameBeforeAnEqualsSignInAPathIsNoName(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "a=b.jsonl")

	sinks, err := openTestSinks(t, []string{"file:" + path}, noEnv)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	if sinks.ship[0].Name != "file" {
		t.Fatalf("name: got %q, want file", sinks.ship[0].Name)
	}
}

func TestAuditSinksRefuseBadItems(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
	}{
		{name: "unknown kind", raw: "kafka://broker:9092"},
		{name: "syslog over unix", raw: "syslog+unix:///dev/log"},
		{name: "syslog facility not a number", raw: "syslog+udp://127.0.0.1:514?facility=audit"},
		{name: "syslog facility out of range", raw: "syslog+udp://127.0.0.1:514?facility=24"},
		{name: "syslog max-size not a number", raw: "syslog+udp://127.0.0.1:514?max-size=big"},
		{name: "syslog max-size too low", raw: "syslog+udp://127.0.0.1:514?max-size=100"},
		{name: "syslog tls with a missing CA file", raw: "syslog+tls://127.0.0.1:6514?ca-file=/nonexistent/ca.pem"},
		{name: "webhook without a secret", raw: "webhook:https://hooks.example/audit"},
		{name: "webhook over plain http", raw: "webhook:http://hooks.example/audit"},
		{name: "blob with an invalid prefix", raw: "blob:Audit Logs"},
		{name: "file with an empty path", raw: "file:"},
		{name: "blob under the namespaces prefix", raw: "blob:namespaces/audit"},
		{name: "blob under the directory of a namespace", raw: "blob:namespaces/ns-acmeacmeacmeacmeacme/tofu"},
		{name: "blob under the Pulumi state prefix", raw: "blob:pulumi"},
		{name: "blob under the audit checkpoint prefix", raw: "blob:audit-checkpoints/audit"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			lookup := noEnv
			if strings.HasPrefix(tc.raw, "webhook:http:") {
				lookup = envOf(map[string]string{webhookSecretEnvVar: "s3cret"})
			}

			_, err := openTestSinks(t, []string{tc.raw}, lookup)
			if err == nil {
				t.Fatalf("open %q: got nil error", tc.raw)
			}

			if !errors.Is(err, errAuditSink) && !errors.Is(err, errNoWebhookSecret) {
				t.Fatalf("open %q: got %v, want an --audit-sink error", tc.raw, err)
			}
		})
	}
}

// TestAuditSinkRefusesTheNamespacesPrefix proves that an operator sink under
// namespaces/ is refused with the reserved prefix error. The state blobs and
// the item key blobs live there, and the sweepers delete unknown objects.
func TestAuditSinkRefusesTheNamespacesPrefix(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"blob:namespaces", "blob:namespaces/audit", "blob:namespaces/ns-acmeacmeacmeacmeacme/x"} {
		_, err := openTestSinks(t, []string{raw}, noEnv)
		if !errors.Is(err, errReservedPrefix) {
			t.Errorf("open %q: err = %v, want errReservedPrefix", raw, err)
		}
	}
}

func TestWebhookSecretComesFromTheVariableOrAPrivateFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	private := filepath.Join(dir, "private")
	shared := filepath.Join(dir, "shared")

	if err := os.WriteFile(private, []byte("from-file\n"), 0o600); err != nil {
		t.Fatalf("write private secret: %v", err)
	}

	if err := os.WriteFile(shared, []byte("from-file\n"), 0o600); err != nil {
		t.Fatalf("write shared secret: %v", err)
	}

	// The test makes a file that others can read on purpose.
	chmodErr := os.Chmod(shared, 0o644) //nolint:gosec // See the comment above.
	if chmodErr != nil {
		t.Fatalf("chmod shared secret: %v", chmodErr)
	}

	cases := []struct {
		vars    map[string]string
		name    string
		want    string
		wantErr error
	}{
		{
			name:    "variable wins over the file",
			vars:    map[string]string{webhookSecretEnvVar: "from-env", webhookSecretFileEnvVar: private},
			want:    "from-env",
			wantErr: nil,
		},
		{
			name:    "private file",
			vars:    map[string]string{webhookSecretFileEnvVar: private},
			want:    "from-file",
			wantErr: nil,
		},
		{
			name:    "file that others can read",
			vars:    map[string]string{webhookSecretFileEnvVar: shared},
			want:    "",
			wantErr: errSecretFilePermissions,
		},
		{name: "nothing set", vars: map[string]string{}, want: "", wantErr: errNoWebhookSecret},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := webhookSecret(envOf(tc.vars))
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("secret: got %v, want %v", err, tc.wantErr)
				}

				return
			}

			if err != nil || string(got) != tc.want {
				t.Fatalf("secret: got %q, %v, want %q", got, err, tc.want)
			}
		})
	}
}

// TestSecretFromRefusesControlCharacters proves that a secret with a
// control character is refused, and that the error names the variable,
// never the value.
func TestSecretFromRefusesControlCharacters(t *testing.T) {
	t.Parallel()

	const value = "secret-value"

	dir := t.TempDir()
	nulFile := filepath.Join(dir, "nul")

	if err := os.WriteFile(nulFile, []byte("before\x00after"), 0o600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}

	for _, tc := range []struct {
		name   string
		vars   map[string]string
		source string
	}{
		{
			name:   "fingerprint token",
			vars:   map[string]string{oidcSecretEnvVar: "\x00fingerprint:00112233445566778899aabbccddeeff"},
			source: oidcSecretEnvVar,
		},
		{
			name:   "newline",
			vars:   map[string]string{oidcSecretEnvVar: value + "\n" + value},
			source: oidcSecretEnvVar,
		},
		{
			name:   "DEL",
			vars:   map[string]string{oidcSecretEnvVar: value + "\x7f"},
			source: oidcSecretEnvVar,
		},
		{
			name:   "NUL in the file",
			vars:   map[string]string{oidcSecretFileEnvVar: nulFile},
			source: oidcSecretFileEnvVar,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkSecretControlRefused(t, tc.vars, tc.source, value)
		})
	}

	got, err := secretFrom(
		envOf(map[string]string{oidcSecretEnvVar: value}), oidcSecretEnvVar, oidcSecretFileEnvVar, errNoOIDCSecret,
	)
	if err != nil || string(got) != value {
		t.Fatalf("secretFrom of a normal secret = %q, %v; want %q", got, err, value)
	}
}

// checkSecretControlRefused proves that secretFrom refuses vars with the
// sentinel, names source in the error, and never repeats value or the text
// of the file.
func checkSecretControlRefused(t *testing.T, vars map[string]string, source, value string) {
	t.Helper()

	got, err := secretFrom(envOf(vars), oidcSecretEnvVar, oidcSecretFileEnvVar, errNoOIDCSecret)
	if err == nil || !errors.Is(err, errSecretControlChar) {
		t.Fatalf("secretFrom = %q, %v; want %v", got, err, errSecretControlChar)
	}

	if !strings.Contains(err.Error(), source) {
		t.Errorf("error %q does not name %s", err, source)
	}

	for _, text := range []string{value, "before", "after", "fingerprint"} {
		if strings.Contains(err.Error(), text) {
			t.Errorf("error %q holds %q", err, text)
		}
	}
}

func TestAWebhookSinkOpensWithASecret(t *testing.T) {
	t.Parallel()

	sinks, err := openTestSinks(t, []string{"webhook:https://hooks.example/audit?a=1,2"}, envOf(map[string]string{
		webhookSecretEnvVar: "s3cret",
	}))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	if sinks.ship[0].Name != "webhook" || sinks.ship[0].Interval != 0 {
		t.Fatalf("webhook sink: got %+v", sinks.ship[0])
	}
}

// TestAuditSinksRefuseBlockedURLAtStart proves that a blocked URL stops the
// start and that the error names the sink item, never the URL.
func TestAuditSinksRefuseBlockedURLAtStart(t *testing.T) {
	t.Parallel()

	const canary = "canary-token"

	cases := []struct {
		name string
		item string
		kind string
	}{
		{
			name: "webhook metadata address",
			item: "webhook:https://169.254.169.254/hook?token=" + canary,
			kind: sinkKindWebhook,
		},
		{
			name: "otlp loopback address",
			item: "otlp:https://127.0.0.1:4318?token=" + canary,
			kind: sinkKindOTLP,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			lookup := noEnv
			if tc.kind == sinkKindWebhook {
				lookup = envOf(map[string]string{webhookSecretEnvVar: "s3cret"})
			}

			_, err := openTestSinks(t, []string{tc.item}, lookup)
			if !errors.Is(err, errAuditSink) || !errors.Is(err, egress.ErrRefused) {
				t.Fatalf("open %s: err = %v, want an --audit-sink refusal", tc.kind, err)
			}

			if !strings.Contains(err.Error(), tc.kind) {
				t.Fatalf("error %q does not name the sink %s", err, tc.kind)
			}

			if strings.Contains(err.Error(), canary) {
				t.Fatalf("the error repeats the URL: %v", err)
			}
		})
	}
}

func TestWebhookSinkRefusesBlockedAddress(t *testing.T) {
	t.Parallel()

	sink, requests := blockedWebhookSink(t)

	err := sink.Write(t.Context(), []logsink.Record{testRecord()})
	if !errors.Is(err, egresshttp.ErrDialRefused) {
		t.Fatalf("write = %v, want %v", err, egresshttp.ErrDialRefused)
	}

	if got := requests.Load(); got != 0 {
		t.Fatalf("the receiver read %d requests, want 0", got)
	}
}

func TestOTLPSinkRefusesBlockedAddress(t *testing.T) {
	t.Parallel()

	requests := startCountingReceiver(t)

	cfg := auditSinkConfig(nil, noEnv, nil)
	cfg.rules = egress.Rules{Allow: nil, LoopbackForTests: false}
	cfg.options = egresshttp.Options{RootCAs: serverPool(t, requests.srv), Resolver: loopbackResolver(t)}

	sink, err := openOTLPSink("https://example.com:"+serverPort(t, requests.srv), cfg)
	if err != nil {
		t.Fatalf("open OTLP sink: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := sink.Close(); closeErr != nil {
			t.Errorf("close OTLP sink: %v", closeErr)
		}
	})

	err = sink.Write(t.Context(), []logsink.Record{testRecord()})
	if !errors.Is(err, egresshttp.ErrDialRefused) {
		t.Fatalf("write = %v, want %v", err, egresshttp.ErrDialRefused)
	}

	if got := requests.count.Load(); got != 0 {
		t.Fatalf("the receiver read %d requests, want 0", got)
	}
}

func TestWebhookSinkRefusedAddressGetsNothing(t *testing.T) {
	t.Parallel()

	sink, requests := blockedWebhookSink(t)
	shipper := newOneEntryShipper(t, sink)

	err := shipper.ShipOnce(t.Context())
	if !errors.Is(err, egresshttp.ErrDialRefused) {
		t.Fatalf("ship once = %v, want %v", err, egresshttp.ErrDialRefused)
	}

	if got := requests.Load(); got != 0 {
		t.Fatalf("the receiver read %d requests, want 0", got)
	}

	statuses, err := shipper.Status(t.Context())
	if err != nil {
		t.Fatalf("status: %v", err)
	}

	if len(statuses) != 1 || statuses[0].LastError == "" {
		t.Fatalf("sink status = %+v, want the failure", statuses)
	}
}

func TestWebhookSinkStopsASlowReceiver(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	cfg := auditSinkConfig(nil, envOf(map[string]string{webhookSecretEnvVar: "s3cret"}), nil)
	cfg.rules = egress.Rules{Allow: nil, LoopbackForTests: true}
	cfg.options = egresshttp.Options{RootCAs: serverPool(t, srv), Resolver: nil}

	client := sinkClient(cfg)
	if client.Timeout != egress.Timeout {
		t.Fatalf("client timeout = %v, want %v", client.Timeout, egress.Timeout)
	}

	client.Timeout = 20 * time.Millisecond

	sink, err := webhook.New(webhook.Config{
		Client: client, URL: srv.URL + "/hook", Secret: []byte("s3cret"), Timeout: 0,
	})
	if err != nil {
		t.Fatalf("new webhook sink: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := sink.Close(); closeErr != nil {
			t.Errorf("close webhook sink: %v", closeErr)
		}
	})

	err = sink.Write(t.Context(), []logsink.Record{testRecord()})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("write = %v, want a timeout", err)
	}
}

// countingReceiver is a TLS test server that counts its requests.
type countingReceiver struct {
	srv   *httptest.Server
	count *atomic.Int32
}

// startCountingReceiver starts a TLS receiver that answers 204 and counts
// each request.
func startCountingReceiver(t *testing.T) *countingReceiver {
	t.Helper()

	var count atomic.Int32

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		count.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	return &countingReceiver{srv: srv, count: &count}
}

// blockedWebhookSink answers a webhook sink whose URL names a host that the
// test resolver answers with a loopback address, and the receiver that counts
// its requests.
func blockedWebhookSink(t *testing.T) (*webhook.Sink, *atomic.Int32) {
	t.Helper()

	requests := startCountingReceiver(t)

	cfg := auditSinkConfig(nil, envOf(map[string]string{webhookSecretEnvVar: "s3cret"}), nil)
	cfg.rules = egress.Rules{Allow: nil, LoopbackForTests: false}
	cfg.options = egresshttp.Options{RootCAs: serverPool(t, requests.srv), Resolver: loopbackResolver(t)}

	sink, err := openWebhookSink("https://example.com:"+serverPort(t, requests.srv)+"/hook", cfg)
	if err != nil {
		t.Fatalf("open webhook sink: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := sink.Close(); closeErr != nil {
			t.Errorf("close webhook sink: %v", closeErr)
		}
	})

	return sink, requests.count
}

// serverPort answers the port of the test server.
func serverPort(t *testing.T, srv *httptest.Server) string {
	t.Helper()

	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("server address: %v", err)
	}

	return port
}

// serverPool answers a certificate pool that trusts the test server.
func serverPool(t *testing.T, srv *httptest.Server) *x509.CertPool {
	t.Helper()

	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())

	return pool
}

// testRecord answers one record for a sink write.
func testRecord() logsink.Record {
	return logsink.Record{Time: time.Unix(0, 0), Level: logsink.LevelInfo, Message: "m", Attrs: nil}
}

// oneEntryFeed is an audit.Feed and audit.NamespaceLister with one entry in
// one namespace.
type oneEntryFeed struct {
	ns    namespace.Namespace
	entry audit.Entry
}

func (f *oneEntryFeed) ListNamespaces(context.Context) ([]namespace.Namespace, error) {
	return []namespace.Namespace{f.ns}, nil
}

func (f *oneEntryFeed) After(_ context.Context, ns namespace.Name, after int64, _ int) ([]audit.Entry, error) {
	if ns != f.ns.Name || after >= f.entry.Sequence {
		return nil, nil
	}

	return []audit.Entry{f.entry}, nil
}

func (f *oneEntryFeed) SinkCursor(context.Context, string, namespace.Name) (int64, error) {
	return 0, nil
}

func (f *oneEntryFeed) AdvanceSinkCursor(context.Context, namespace.Fence, durable.Claim, string, int64) error {
	return nil
}

// newOneEntryShipper answers a shipper with one entry in one namespace.
func newOneEntryShipper(t *testing.T, sink logsink.Sink) *audit.Shipper {
	t.Helper()

	ns := namespace.Namespace{
		CreatedAt:           time.Unix(0, 0),
		ID:                  namespace.NewID(),
		Name:                "acme",
		PrimaryInstallation: "eu-west",
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
		FailoverVersion:     1,
	}

	feed := &oneEntryFeed{
		ns: ns,
		entry: audit.Entry{
			At:            time.Unix(0, 0),
			Namespace:     ns.Name,
			Subject:       "alice",
			IDPSubject:    "",
			TokenID:       "",
			Action:        "unit:read-secrets",
			Object:        "opentofu:acme/prod",
			Version:       "sv-1",
			ValuePath:     "",
			ValueCount:    nil,
			Changes:       nil,
			SourceIP:      "192.0.2.1",
			UserAgent:     "OpenTofu/1.11",
			Channel:       audit.ChannelAPI,
			Kind:          audit.KindToolOperation,
			Outcome:       audit.OutcomeSuccess,
			RequestID:     "req-1",
			ObjectID:      "",
			PrevHash:      "",
			Hash:          "",
			Sequence:      1,
			FenceVersion:  1,
			PrincipalKind: "",
			JobID:         "",
			Reason:        "",
		},
	}

	shipper, err := audit.NewShipper(audit.ShipperConfig{
		Feed:          feed,
		Namespaces:    feed,
		Logger:        nil,
		OnShipped:     nil,
		OnFailure:     nil,
		OnSkipped:     nil,
		OnLag:         nil,
		OnLagEntries:  nil,
		Now:           func() time.Time { return time.Unix(0, 0) },
		Sinks:         []audit.ShipSink{{Sink: sink, Name: "webhook", Kind: sinkKindWebhook, Interval: 0}},
		InstanceID:    "",
		Leases:        nil,
		LeaseTTL:      0,
		RenewInterval: 0,
	})
	if err != nil {
		t.Fatalf("new shipper: %v", err)
	}

	return shipper
}

// dnsHeader is the length of the header of a DNS message.
const dnsHeader = 12

// dnsTypeA is the DNS record type of an IPv4 address.
const dnsTypeA = 1

// loopbackResolver starts a DNS server on loopback that answers every IPv4
// name lookup with 127.0.0.1 and returns a resolver that asks only that
// server.
func loopbackResolver(t *testing.T) *net.Resolver {
	t.Helper()

	var lc net.ListenConfig

	pc, err := lc.ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for DNS: %v", err)
	}

	done := make(chan struct{})

	go func() {
		defer close(done)

		buf := make([]byte, 512)

		for {
			n, from, readErr := pc.ReadFrom(buf)
			if readErr != nil {
				return
			}

			if resp := loopbackAnswer(buf[:n]); resp != nil {
				if _, writeErr := pc.WriteTo(resp, from); writeErr != nil {
					return
				}
			}
		}
	}()

	t.Cleanup(func() {
		if closeErr := pc.Close(); closeErr != nil {
			t.Error(closeErr)
		}

		<-done
	})

	server := pc.LocalAddr().String()

	return &net.Resolver{
		PreferGo:     true,
		StrictErrors: false,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer

			return d.DialContext(ctx, "udp", server)
		},
	}
}

// loopbackAnswer answers an IPv4 query with 127.0.0.1 and every other query
// with no record.
func loopbackAnswer(query []byte) []byte {
	if len(query) < dnsHeader+5 {
		return nil
	}

	end := dnsHeader
	for end < len(query) && query[end] != 0 {
		end += int(query[end]) + 1
	}

	end += 5
	if end > len(query) {
		return nil
	}

	qtype := binary.BigEndian.Uint16(query[end-4:])

	resp := append([]byte{}, query[:end]...)
	resp[2], resp[3] = 0x81, 0x80

	count := uint16(0)
	if qtype == dnsTypeA {
		count = 1
	}

	binary.BigEndian.PutUint16(resp[6:], count)
	resp[8], resp[9], resp[10], resp[11] = 0, 0, 0, 0

	if count == 1 {
		resp = append(resp, 0xC0, dnsHeader)
		resp = binary.BigEndian.AppendUint16(resp, dnsTypeA)
		resp = binary.BigEndian.AppendUint16(resp, 1)
		resp = binary.BigEndian.AppendUint32(resp, 0)
		resp = binary.BigEndian.AppendUint16(resp, net.IPv4len)
		resp = append(resp, 127, 0, 0, 1)
	}

	return resp
}

//nolint:paralleltest // serve reads process environment, so this test sets it.
func TestServeOpensTheAuditSinkFile(t *testing.T) {
	setServeEnv(t, testKeyBase64())

	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	args := []string{
		"--database", "sqlite:" + filepath.Join(dir, "dustpan.db"),
		"--listen", "127.0.0.1:0",
		"--audit-sink", "file:" + path,
	}

	if err := startServer(t, args...); err != nil {
		t.Fatalf("serve with a file audit sink: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("serve did not open the audit sink file: %v", err)
	}

	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("audit sink file mode %04o, want 0600", perm)
	}
}

//nolint:paralleltest // serve reads process environment, so this test sets it.
func TestServeRefusesAnUnknownAuditSink(t *testing.T) {
	setServeEnv(t, testKeyBase64())

	dir := t.TempDir()

	var logs lockedBuffer

	err := serve(t.Context(), []string{
		"--database", "sqlite:" + filepath.Join(dir, "dustpan.db"),
		"--listen", "127.0.0.1:0",
		"--audit-sink", "kafka://broker:9092",
	}, &logs)
	if !errors.Is(err, errAuditSink) {
		t.Fatalf("serve with an unknown sink: got %v, want an --audit-sink error", err)
	}
}
