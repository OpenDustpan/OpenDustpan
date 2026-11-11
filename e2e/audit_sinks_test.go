//go:build e2e

package e2e_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/logsink/file"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// auditSinkEntry decodes one row of the sink list.
type auditSinkEntry struct {
	Name       string           `json:"name"`
	Kind       string           `json:"kind"`
	Cursor     map[string]int64 `json:"cursor"`
	LagSeconds float64          `json:"lagSeconds"`
	LastError  string           `json:"lastError"`
}

// auditSinkList decodes the sink list.
type auditSinkList struct {
	Sinks []auditSinkEntry `json:"sinks"`
}

// TestAuditSinks proves the audit sinks. The file subtest starts an
// in-process server with a file sink, writes one audited change and reads
// the sink list: one sink is listed and its lag is a number. The receivers
// subtest runs on the in-process server, on each store, with a syslog sink
// over TLS, a signed webhook sink and an OTLP sink at once. A receiver in the
// test process reads each sink on the wire, and the entries it holds match the
// audit export by scope and sequence. The binary subtest proves that a
// loopback sink URL fails the start.
func TestAuditSinks(t *testing.T) {
	t.Parallel()

	t.Run("file", testFileSink)
	t.Run("receivers", testSinkReceivers)
	t.Run("loopback refused", testSinkLoopbackRefused)
}

// testFileSink checks the sink list of an in-process server with one file
// sink.
func testFileSink(t *testing.T) {
	t.Parallel()

	srv := newAuditSinkTestServer(t)

	if err := srv.Start(t.Context()); err != nil {
		t.Fatalf("start the server: %v", err)
	}

	ts := httptest.NewServer(srv.Handler())

	t.Cleanup(ts.Close)

	ctx := t.Context()
	client := ts.Client()

	call := func(method, path, body string) (int, string) {
		return sinkRequest(ctx, t, client, ts.URL, method, path, body)
	}

	status, body := call(http.MethodPost, "/api/server/namespaces", `{"name":"acme"}`)
	if status != http.StatusCreated {
		t.Fatalf("create namespace answers %d, want %d (%s)", status, http.StatusCreated, body)
	}

	status, body = call(http.MethodGet, "/api/server/audit/sinks", "")
	if status != http.StatusOK {
		t.Fatalf("list sinks answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	checkOneSink(t, body)
	checkSinkLagMetrics(t, srv)
}

// newAuditSinkTestServer starts a server with one file sink.
func newAuditSinkTestServer(t *testing.T) *dustpan.Server {
	t.Helper()

	ctx := t.Context()

	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	blobs, err := fs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	sink, err := file.Open(filepath.Join(t.TempDir(), "audit.jsonl"))
	if err != nil {
		t.Fatalf("open file sink: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := sink.Close(); closeErr != nil {
			t.Errorf("close file sink: %v", closeErr)
		}
	})

	authn, err := identity.NewStaticToken("admin", "test-token-0123456789abcdef0123456789")
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	srv, err := dustpan.NewServer(
		dustpan.WithInstallation("e2e"),
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
		dustpan.WithAuditSinks(audit.ShipSink{Sink: sink, Name: "file", Kind: "file", Interval: 0}),
	)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	return srv
}

// sinkRequest sends one authenticated request and answers its status and
// body.
//
//nolint:revive // A test helper names every request field explicitly, so the call reads as one line.
func sinkRequest(
	ctx context.Context, t *testing.T, client *http.Client, base, method, path, body string,
) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, method, base+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build %s %s: %v", method, path, err)
	}

	req.Header.Set("Authorization", "Bearer test-token-0123456789abcdef0123456789")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The body was read to completion above.
	}()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s %s: %v", method, path, err)
	}

	return resp.StatusCode, string(data)
}

// checkOneSink proves the list holds one file sink with a numeric lag and a
// cursor for acme.
func checkOneSink(t *testing.T, body string) {
	t.Helper()

	var got auditSinkList
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode sinks: %v (%s)", err, body)
	}

	if len(got.Sinks) != 1 {
		t.Fatalf("sinks hold %d entries, want 1 (%s)", len(got.Sinks), body)
	}

	sink := got.Sinks[0]
	if sink.Name != "file" || sink.Kind != "file" {
		t.Fatalf("sink = %+v, want name file and kind file", sink)
	}

	if sink.LagSeconds < 0 {
		t.Fatalf("sink lag = %v, want a number at or above 0", sink.LagSeconds)
	}

	if _, ok := sink.Cursor["acme"]; !ok {
		t.Fatalf("sink cursor = %+v, want a cursor for acme", sink.Cursor)
	}
}

// checkSinkLagMetrics proves the metrics output holds both delivery lags of
// the file sink: the lag in seconds and the lag in entries.
func checkSinkLagMetrics(t *testing.T, srv *dustpan.Server) {
	t.Helper()

	var out strings.Builder
	if err := srv.Metrics().Write(&out); err != nil {
		t.Fatalf("write metrics: %v", err)
	}

	for _, want := range []string{
		`dustpan_audit_sink_lag_seconds{sink="file"}`,
		`dustpan_audit_sink_lag_entries{sink="file"}`,
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("metrics lack %s:\n%s", want, out.String())
		}
	}
}

// sinkWait bounds the wait for the sinks to ship the entries. A sink ships
// every 5 seconds, and a round that fails waits longer.
const sinkWait = 90 * time.Second

// sinkScopes returns the namespaces whose entries the receivers must hold: the
// two namespaces that the test creates.
func sinkScopes() []string {
	return []string{"orbit", "delta"}
}

// testSinkReceivers starts an in-process server with the three sinks set,
// writes audited changes, and compares what each receiver read with the audit
// export.
func testSinkReceivers(t *testing.T) {
	t.Parallel()

	if e2e.UseBinaryServer() {
		t.Skip("needs the in-process server: a loopback receiver needs the test rules of the sink client")
	}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()

			if db == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			runSinkScenario(t, db)
		})
	}
}

// testSinkLoopbackRefused proves that a webhook sink URL on the loopback
// fails the start of the server binary and that the error names the item.
func testSinkLoopbackRefused(t *testing.T) {
	t.Parallel()

	if !e2e.UseBinaryServer() {
		t.Skip("needs E2E_SERVER=binary: the audit sink flag belongs to the server binary")
	}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()

			runSinkLoopbackRefused(t, db)
		})
	}
}

// runSinkLoopbackRefused proves the refusal on one store.
func runSinkLoopbackRefused(t *testing.T, db e2e.Store) {
	t.Helper()

	if db == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()

	env := e2e.NewEnv(ctx, t, e2e.NoCLI(), db)
	if env.HAEnabled() {
		t.Skip("needs one server: the refusal proves the start of one process")
	}

	env.StopServer(ctx, t)
	env.SetAuditSinks(t, e2e.AuditSinkSettings{
		Items: []string{"webhook=webhook:https://127.0.0.1:4318/hook"},
		Env:   []string{"DUSTPAN_AUDIT_WEBHOOK_SECRET=" + strings.Repeat("0", 64)},
	})

	code, out := env.StartServerExpectingRefusal(ctx, t, env.DatabaseURL(t))
	if code == 0 {
		t.Fatalf("serve with a loopback webhook sink exits with status 0, want a refusal\n%s", out)
	}

	if !strings.Contains(out, "--audit-sink") || !strings.Contains(out, "webhook") {
		t.Fatalf("serve error does not name the sink item:\n%s", out)
	}
}

// runSinkScenario runs the scenario on one store and then the subtests, one
// for each receiver.
func runSinkScenario(t *testing.T, db e2e.Store) {
	t.Helper()

	ctx := t.Context()

	receivers := e2e.NewAuditReceivers(t)
	env := e2e.NewEnvWithAuditSinks(ctx, t, db, receivers.Sinks(t)...)

	for _, name := range sinkScopes() {
		env.CreateNamespace(ctx, t, name)

		// A collection is an audited change in the scope of its namespace.
		path := "/api/dustpan/v1/namespaces/" + name + "/collections"

		status, body, callErr := env.APIStatus(ctx, http.MethodPost, path, collectionBody(t, "sinks-"+name))
		if callErr != nil || status != http.StatusCreated {
			t.Fatalf("make a collection in %s answers %d %v (%s)", name, status, callErr, body)
		}
	}

	exports := make(map[string][]exportLine, len(sinkScopes()))

	for _, scope := range sinkScopes() {
		exports[scope] = readAuditExport(ctx, t, env, "/api/audit/export?organization="+url.QueryEscape(scope))
		if len(exports[scope]) == 0 {
			t.Fatalf("export of %s holds no entry", scope)
		}
	}

	secrets := append(receivers.Secrets(), env.TokenA())

	t.Run("syslog", func(t *testing.T) {
		waitForEntries(ctx, t, receivers.Syslog.Entries, exports)
		checkReceived(t, receivers.Syslog.Entries(), exports)
		checkReceiverClean(t, receivers.Syslog.Raw(), receivers.Syslog.Malformed(), secrets)
	})

	t.Run("webhook", func(t *testing.T) {
		waitForEntries(ctx, t, receivers.Webhook.Entries, exports)
		checkReceived(t, receivers.Webhook.Entries(), exports)
		checkReceiverClean(t, receivers.Webhook.Raw(), receivers.Webhook.Malformed(), secrets)
		checkWebhookRefusals(ctx, t, receivers)
	})

	t.Run("otlp", func(t *testing.T) {
		waitForEntries(ctx, t, receivers.OTLP.Entries, exports)
		checkReceived(t, receivers.OTLP.Entries(), exports)
		checkReceiverClean(t, receivers.OTLP.Raw(), receivers.OTLP.Malformed(), secrets)
		checkOTLPRefusal(ctx, t, receivers)
	})
}

// sinkKey names one entry by its scope and sequence.
type sinkKey struct {
	scope    string
	sequence int64
}

// waitForEntries waits until read holds every entry of exports, or fails the
// test after sinkWait.
func waitForEntries(
	ctx context.Context, t *testing.T, read func() []e2e.ReceivedEntry, exports map[string][]exportLine,
) {
	t.Helper()

	deadline := time.Now().Add(sinkWait)

	for {
		missing := missingEntries(read(), exports)
		if len(missing) == 0 {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("after %s the receiver lacks %d entries, for example %+v", sinkWait, len(missing), missing[0])
		}

		timer := time.NewTimer(time.Second)

		select {
		case <-ctx.Done():
			timer.Stop()
			t.Fatalf("context done while waiting for the sinks: %v", ctx.Err())
		case <-timer.C:
		}
	}
}

// missingEntries lists the entries of exports that got holds no copy of.
func missingEntries(got []e2e.ReceivedEntry, exports map[string][]exportLine) []sinkKey {
	have := make(map[sinkKey]bool, len(got))
	for i := range got {
		have[sinkKey{scope: got[i].Scope, sequence: got[i].Sequence}] = true
	}

	var missing []sinkKey

	for scope, lines := range exports {
		for i := range lines {
			if key := (sinkKey{scope: scope, sequence: lines[i].Sequence}); !have[key] {
				missing = append(missing, key)
			}
		}
	}

	return missing
}

// checkReceived proves that, for each scope, the receiver holds every entry
// of the export with the same sequence, action, object, kind and hash. A
// sink delivers at least once, so a receiver may hold an entry twice, but
// both copies must be equal.
func checkReceived(t *testing.T, got []e2e.ReceivedEntry, exports map[string][]exportLine) {
	t.Helper()

	bySequence := make(map[sinkKey]e2e.ReceivedEntry, len(got))

	for i := range got {
		key := sinkKey{scope: got[i].Scope, sequence: got[i].Sequence}
		if prev, dup := bySequence[key]; dup && prev != got[i] {
			t.Fatalf("two copies of %+v differ: %+v and %+v", key, prev, got[i])
		}

		bySequence[key] = got[i]
	}

	for scope, lines := range exports {
		for i := range lines {
			line := &lines[i]

			entry, ok := bySequence[sinkKey{scope: scope, sequence: line.Sequence}]
			if !ok {
				t.Fatalf("receiver lacks sequence %d of %s", line.Sequence, scope)
			}

			want := e2e.ReceivedEntry{
				Scope: scope, Action: line.Action, Object: line.Object, Kind: line.Kind, Hash: line.Hash,
				Sequence: line.Sequence,
			}
			if entry != want {
				t.Fatalf("sequence %d of %s: receiver holds %+v, export holds %+v", line.Sequence, scope, entry, want)
			}
		}
	}
}

// checkReceiverClean proves that the receiver found no break of the wire
// format and that no secret is in the text it accepted.
func checkReceiverClean(t *testing.T, raw string, malformed, secrets []string) {
	t.Helper()

	if len(malformed) > 0 {
		t.Fatalf("the receiver read %d messages that break the wire format, for example: %s",
			len(malformed), malformed[0])
	}

	if raw == "" {
		t.Fatal("the receiver accepted no text")
	}

	for i, value := range secrets {
		if strings.Contains(raw, value) {
			t.Fatalf("secret %d of %d is in the entries that the receiver read", i+1, len(secrets))
		}
	}
}

// receiverClient returns an HTTPS client that trusts the certificate of the
// receivers.
func receiverClient(t *testing.T, receivers *e2e.AuditReceivers) *http.Client {
	t.Helper()

	pemBytes, err := os.ReadFile(receivers.CAFile)
	if err != nil {
		t.Fatalf("read receiver CA file: %v", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		t.Fatal("receiver CA file holds no certificate")
	}

	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}

	t.Cleanup(transport.CloseIdleConnections)

	return &http.Client{Transport: transport, Timeout: time.Minute}
}

// receiverPost is one request that a test sends to a receiver itself.
type receiverPost struct {
	header map[string]string
	target string
	body   string
}

// postToReceiver sends one POST and answers the status.
func postToReceiver(ctx context.Context, t *testing.T, client *http.Client, in receiverPost) int {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, in.target, strings.NewReader(in.body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	for name, value := range in.header {
		req.Header.Set(name, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("post to the receiver: %v", err)
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("close the answer: %v", closeErr)
		}
	}()

	if _, err = io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatalf("read the answer: %v", err)
	}

	return resp.StatusCode
}

// checkWebhookRefusals proves that the webhook receiver refuses a request
// with a wrong signature, a request with a signature of another body, and a
// signed request with an old timestamp, and keeps none of their entries. A
// correct signature is accepted.
func checkWebhookRefusals(ctx context.Context, t *testing.T, receivers *e2e.AuditReceivers) {
	t.Helper()

	client := receiverClient(t, receivers)
	target := receivers.Webhook.URL()
	before := len(receivers.Webhook.Entries())
	refusedBefore := receivers.Webhook.Refused()
	now := strconv.FormatInt(time.Now().Unix(), 10)
	old := strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)
	body := `[{"scope":"forged","sequence":1,"action":"forged","object":"forged","kind":"forged","hash":""}]`

	for name, header := range map[string]map[string]string{
		"wrong signature": {
			"Dustpan-Timestamp": now,
			"Dustpan-Signature": "v1=" + strings.Repeat("0", 64),
		},
		"no signature": {"Dustpan-Timestamp": now},
		"signature of another body": {
			"Dustpan-Timestamp": now,
			"Dustpan-Signature": receivers.Webhook.Sign(now, []byte("[]")),
		},
		"old timestamp": {
			"Dustpan-Timestamp": old,
			"Dustpan-Signature": receivers.Webhook.Sign(old, []byte(body)),
		},
	} {
		request := receiverPost{target: target, header: header, body: body}

		if got := postToReceiver(ctx, t, client, request); got != http.StatusUnauthorized {
			t.Fatalf("webhook with %s answers %d, want %d", name, got, http.StatusUnauthorized)
		}
	}

	if got := receivers.Webhook.Refused() - refusedBefore; got != 4 {
		t.Fatalf("webhook receiver counts %d refusals, want 4", got)
	}

	if got := len(receivers.Webhook.Entries()); got != before {
		t.Fatalf("webhook receiver holds %d entries after refusals, want %d", got, before)
	}

	good := map[string]string{"Dustpan-Timestamp": now, "Dustpan-Signature": receivers.Webhook.Sign(now, []byte("[]"))}
	request := receiverPost{target: target, header: good, body: "[]"}

	if got := postToReceiver(ctx, t, client, request); got != http.StatusNoContent {
		t.Fatalf("webhook with a correct signature answers %d, want %d", got, http.StatusNoContent)
	}
}

// checkOTLPRefusal proves that the OTLP collector refuses an export without
// the header key that the sink sends.
func checkOTLPRefusal(ctx context.Context, t *testing.T, receivers *e2e.AuditReceivers) {
	t.Helper()

	client := receiverClient(t, receivers)
	target := receivers.OTLP.URL() + "/v1/logs"
	before := len(receivers.OTLP.Entries())

	header := map[string]string{"Content-Type": "application/json"}
	request := receiverPost{target: target, header: header, body: `{"resourceLogs":[]}`}

	if got := postToReceiver(ctx, t, client, request); got != http.StatusUnauthorized {
		t.Fatalf("OTLP export without a key answers %d, want %d", got, http.StatusUnauthorized)
	}

	if got := len(receivers.OTLP.Entries()); got != before {
		t.Fatalf("OTLP collector holds %d entries after a refusal, want %d", got, before)
	}
}
