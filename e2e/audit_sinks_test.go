//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

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

// TestAuditSinks starts a server with a file sink, writes one audited
// change and reads the sink list: one sink is listed and its lag is a
// number.
func TestAuditSinks(t *testing.T) {
	t.Parallel()

	srv := newAuditSinkTestServer(t)
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
		dustpan.WithRegion("e2e"),
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
