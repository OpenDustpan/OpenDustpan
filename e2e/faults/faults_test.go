//go:build faults

package faults_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

// TestLatencyThroughProxy proves a health request through a proxy with
// 500 ms latency still succeeds and takes at least 500 ms.
func TestLatencyThroughProxy(t *testing.T) {
	t.Parallel()

	bin := toxiproxyBin(t)
	srv := startFaultServer(t)
	api := startToxiproxy(t, bin)
	listen := createProxy(t, api, "latency-proxy", srv.upstream)

	addToxic(t, api, "latency-proxy", "latency-down", "latency", "downstream", `{"latency":500,"jitter":0}`)

	client := &http.Client{Timeout: 10 * time.Second}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, proxyURL(listen)+"/health/live", http.NoBody)
	if err != nil {
		t.Fatalf("build health request: %v", err)
	}

	start := time.Now()

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /health/live through latency proxy: %v", err)
	}

	discardBody(resp)
	elapsed := time.Since(start)

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("GET /health/live status %d, want %d", resp.StatusCode, http.StatusNoContent)
	}

	if elapsed < 500*time.Millisecond {
		t.Fatalf("GET /health/live took %s, want at least 500 ms", elapsed)
	}

	removeToxic(t, api, "latency-proxy", "latency-down")
}

// TestTimeoutThroughProxy proves a toxic that stops data fails the client
// request with a timeout, and the server stays healthy after the toxic is removed.
func TestTimeoutThroughProxy(t *testing.T) {
	t.Parallel()

	bin := toxiproxyBin(t)
	srv := startFaultServer(t)
	api := startToxiproxy(t, bin)
	listen := createProxy(t, api, "timeout-proxy", srv.upstream)

	addToxic(t, api, "timeout-proxy", "stall", "timeout", "upstream", `{"timeout":0}`)

	stalled := &http.Client{Timeout: time.Second}

	stalledReq, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		proxyURL(listen)+"/health/live",
		http.NoBody,
	)
	if err != nil {
		t.Fatalf("build stalled request: %v", err)
	}

	_, err = stalled.Do(stalledReq)
	if err == nil {
		t.Fatal("GET /health/live through stalled proxy succeeded, want a timeout error")
	}

	if !isTimeoutError(err) {
		t.Fatalf("GET /health/live through stalled proxy: err = %v, want a timeout error", err)
	}

	removeToxic(t, api, "timeout-proxy", "stall")

	healthy := &http.Client{Timeout: 5 * time.Second}

	// The server stays healthy after the toxic is removed: the same
	// request through the same proxy succeeds, and a direct request does too.
	for _, target := range []string{proxyURL(listen) + "/health/live", srv.url + "/health/live"} {
		req, reqErr := http.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
		if reqErr != nil {
			t.Fatalf("build health request: %v", reqErr)
		}

		resp, doErr := healthy.Do(req)
		if doErr != nil {
			t.Fatalf("GET %s after toxic removal: %v", target, doErr)
		}

		discardBody(resp)

		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("GET %s after toxic removal: status %d, want %d", target, resp.StatusCode, http.StatusNoContent)
		}
	}
}

// TestLimitDataKeepsOldState proves a connection cut in the middle of a
// large checkpoint upload fails the upload and stores nothing partial: the
// old version is still current.
func TestLimitDataKeepsOldState(t *testing.T) {
	t.Parallel()

	bin := toxiproxyBin(t)
	srv := startFaultServer(t)
	direct := &http.Client{Timeout: 10 * time.Second}
	tokenAuth := "token " + srv.token

	createStack(t, direct, srv.url, tokenAuth)

	firstLease := startUpdate(t, direct, srv.url, tokenAuth)
	saveCheckpoint(t, direct, srv.url, tokenAuth, firstLease, `{"manifest":{},"resources":[]}`)
	completeUpdate(t, direct, srv.url, firstLease, "succeeded")

	before := exportStack(t, direct, srv.url, tokenAuth)

	secondLease := startUpdate(t, direct, srv.url, tokenAuth)

	api := startToxiproxy(t, bin)
	listen := createProxy(t, api, "limit-proxy", srv.upstream)

	addToxic(t, api, "limit-proxy", "cut-body", "limit_data", "upstream", `{"bytes":1024}`)

	large := `{"manifest":{},"resources":[],"padding":"` + strings.Repeat("a", 256*1024) + `"}`

	// The upload through the proxy must fail: the proxy closes the
	// connection after 1024 bytes of the large body.
	uploadStatus, uploadErr := patchCheckpointThroughProxy(t, proxyURL(listen), secondLease, large)
	if uploadErr == nil && uploadStatus == http.StatusOK {
		t.Fatal("large checkpoint upload through limit_data proxy succeeded, want a failure")
	}

	removeToxic(t, api, "limit-proxy", "cut-body")

	after := exportStack(t, direct, srv.url, tokenAuth)
	if after != before {
		t.Fatal("export after cut upload differs from the old version, want the old version still current")
	}

	completeUpdate(t, direct, srv.url, secondLease, "failed")
}

// patchCheckpointThroughProxy sends one large checkpoint save through the
// proxy and returns its status, or the client error when the connection dies.
func patchCheckpointThroughProxy(t *testing.T, proxyBase, lease, deployment string) (int, error) {
	t.Helper()

	body := `{"version":3,"deployment":` + deployment + `}`

	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPatch,
		proxyBase+"/api/stacks/acme/web/prod/update/"+leaseID(lease)+"/checkpoint",
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf("build checkpoint request: %v", err)
	}

	req.Header.Set("Authorization", "update-token "+leaseToken(lease))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The status is the check; the body is drained by readBody callers.
	}()

	_ = readBody(t, resp)

	return resp.StatusCode, nil
}

// leaseID splits the lease "updateID/token" form used below. The start
// helper returns them joined, so the checkpoint path can name the update.
func leaseID(lease string) string {
	if i := strings.Index(lease, "/"); i >= 0 {
		return lease[:i]
	}

	return lease
}

// leaseToken splits the joined lease into its token part.
func leaseToken(lease string) string {
	if i := strings.Index(lease, "/"); i >= 0 {
		return lease[i+1:]
	}

	return lease
}

// createStack creates acme/web/prod through the API.
func createStack(t *testing.T, client *http.Client, base, auth string) {
	t.Helper()

	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		base+"/api/stacks/acme/web",
		strings.NewReader(`{"stackName":"prod"}`),
	)
	if err != nil {
		t.Fatalf("build create stack: %v", err)
	}

	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("create stack: %v", err)
	}

	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create stack: status %d, want %d (body %s)", resp.StatusCode, http.StatusOK, body)
	}
}

// startUpdate creates and starts an update and returns its ID and lease joined as "id/token".
func startUpdate(t *testing.T, client *http.Client, base, auth string) string {
	t.Helper()

	var created struct {
		UpdateID string `json:"updateID"`
	}

	postJSON(t, client, base+"/api/stacks/acme/web/prod/update", auth, `{}`, &created)
	if created.UpdateID == "" {
		t.Fatal("create update: no update id")
	}

	var started struct {
		Token string `json:"token"`
	}

	postJSON(t, client, base+"/api/stacks/acme/web/prod/update/"+created.UpdateID, auth, `{}`, &started)
	if started.Token == "" {
		t.Fatal("start update: no lease token")
	}

	return created.UpdateID + "/" + started.Token
}

// saveCheckpoint saves deployment in the running update named by lease.
func saveCheckpoint(t *testing.T, client *http.Client, base, _, lease, deployment string) {
	t.Helper()

	id := leaseID(lease)
	tok := leaseToken(lease)
	body := `{"version":3,"deployment":` + deployment + `}`

	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPatch,
		base+"/api/stacks/acme/web/prod/update/"+id+"/checkpoint",
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf("build checkpoint request: %v", err)
	}

	req.Header.Set("Authorization", "update-token "+tok)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	respBody := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save checkpoint: status %d, want %d (body %s)", resp.StatusCode, http.StatusOK, respBody)
	}
}

// completeUpdate completes the update named by lease with status.
func completeUpdate(t *testing.T, client *http.Client, base, lease, status string) {
	t.Helper()

	id := leaseID(lease)
	tok := leaseToken(lease)

	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		base+"/api/stacks/acme/web/prod/update/"+id+"/complete",
		strings.NewReader(`{"status":`+fmt.Sprintf("%q", status)+`}`),
	)
	if err != nil {
		t.Fatalf("build complete request: %v", err)
	}

	req.Header.Set("Authorization", "update-token "+tok)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("complete update: %v", err)
	}

	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("complete update: status %d, want %d (body %s)", resp.StatusCode, http.StatusOK, body)
	}
}

// exportStack returns the export body of acme/web/prod.
func exportStack(t *testing.T, client *http.Client, base, auth string) string {
	t.Helper()

	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		base+"/api/stacks/acme/web/prod/export",
		http.NoBody,
	)
	if err != nil {
		t.Fatalf("build export request: %v", err)
	}

	req.Header.Set("Authorization", auth)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("export stack: %v", err)
	}

	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export stack: status %d, want %d (body %s)", resp.StatusCode, http.StatusOK, body)
	}

	return body
}

// postJSON sends one authenticated POST with body and decodes the JSON response into out.
func postJSON(t *testing.T, client *http.Client, target, auth, body string, out any) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, target, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build POST %s: %v", target, err)
	}

	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", target, err)
	}

	data := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s: status %d, want %d (body %s)", target, resp.StatusCode, http.StatusOK, data)
	}

	decodeJSON(t, "POST "+target, []byte(data), out)
}

// TestPostgresCutThroughProxy proves a proxy in front of PostgreSQL that is
// cut during a write fails the write, and the next write after the toxic is
// removed succeeds. The PostgreSQL case joins the table only when
// DUSTPAN_TEST_POSTGRES_URL is set; without it the suite runs the SQLite
// cases only.
func TestPostgresCutThroughProxy(t *testing.T) {
	t.Parallel()

	base := os.Getenv("DUSTPAN_TEST_POSTGRES_URL")

	type pgCase struct {
		name string
		base string
	}

	var cases []pgCase

	if base != "" {
		cases = append(cases, pgCase{name: "postgres cut during write", base: base})
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkPostgresCut(t, tc.base)
		})
	}
}

// checkPostgresCut runs the PostgreSQL cut case against base. A base that is
// set but unreachable fails the test.
func checkPostgresCut(t *testing.T, base string) {
	t.Helper()

	bin := toxiproxyBin(t)
	ctx := t.Context()

	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse postgres URL: %v", err)
	}

	host := parsed.Hostname()
	port := parsed.Port()
	if port == "" {
		port = "5432"
	}

	api := startToxiproxy(t, bin)
	listen := createProxy(t, api, "pg-proxy", net.JoinHostPort(host, port))

	schema := randomSchema(t)

	createPGSchema(t, base, schema)
	t.Cleanup(func() { dropPGSchema(schema, base) })

	proxied := schemaURL(t, parsed, listen, schema)

	// A write through the proxy works before the cut.
	openStore := func() *postgres.Store {
		t.Helper()

		st, err := postgres.Open(ctx, proxied)
		if err != nil {
			t.Fatalf("open postgres store through proxy: %v", err)
		}

		return st
	}

	first := openStore()
	if err := first.CreateNamespace(ctx, pgNamespace("pgfault-ok")); err != nil {
		first.CloseContext(ctx)
		t.Fatalf("write before cut: %v", err)
	}

	first.CloseContext(ctx)

	addToxic(t, api, "pg-proxy", "cut-pg", "timeout", "upstream", `{"timeout":0}`)

	// A write while cut fails. A short timeout bounds the stall.
	cutCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	second := openStore()

	cutErr := second.CreateNamespace(cutCtx, pgNamespace("pgfault-cut"))
	second.CloseContext(ctx)

	if cutErr == nil {
		t.Fatal("write during cut succeeded, want a failure")
	}

	removeToxic(t, api, "pg-proxy", "cut-pg")

	// The next write after the toxic is removed succeeds.
	third := openStore()

	if err := third.CreateNamespace(ctx, pgNamespace("pgfault-after")); err != nil {
		third.CloseContext(ctx)
		t.Fatalf("write after cut: %v", err)
	}

	got, err := third.GetNamespace(ctx, namespace.Name("pgfault-after"))
	third.CloseContext(ctx)

	if err != nil {
		t.Fatalf("read after cut: %v", err)
	}

	if got.Name.String() != "pgfault-after" {
		t.Fatalf("namespace after cut = %q, want %q", got.Name.String(), "pgfault-after")
	}
}

// pgNamespace returns a namespace row for a PostgreSQL cut write.
func pgNamespace(name string) namespace.Namespace {
	return namespace.Namespace{
		CreatedAt:       time.Now(),
		Name:            namespace.Name(name),
		HomeRegion:      namespace.Region(faultRegion),
		FailoverVersion: 1,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	}
}

// randomSchema returns a fresh schema name for one PostgreSQL run.
func randomSchema(t *testing.T) string {
	t.Helper()

	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generate schema name: %v", err)
	}

	return "fault_" + hex.EncodeToString(raw)
}

// createPGSchema creates schema on base. A failure fails the test: the
// variable is set but unreachable.
func createPGSchema(t *testing.T, base, schema string) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), base)
	if err != nil {
		t.Fatalf("connect to postgres: %v", err)
	}

	defer func() {
		_ = conn.Close(t.Context()) //nolint:errcheck // The schema check below reports the outcome.
	}()

	if _, err := conn.Exec(t.Context(), `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
}

// dropPGSchema drops schema on base. It runs at cleanup with a fresh context.
func dropPGSchema(schema, base string) {
	ctx := context.Background()

	conn, err := pgx.Connect(ctx, base)
	if err != nil {
		return
	}

	defer func() {
		_ = conn.Close(ctx) //nolint:errcheck // Best-effort cleanup at test end.
	}()

	_, _ = conn.Exec(ctx, `DROP SCHEMA "`+schema+`" CASCADE`) //nolint:errcheck // Best-effort cleanup.
}

// schemaURL returns base with its host replaced by proxyListen and its
// search path set to schema.
func schemaURL(t *testing.T, base *url.URL, proxyListen, schema string) string {
	t.Helper()

	next := *base
	next.Host = proxyListen

	q := next.Query()
	q.Set("search_path", schema)
	next.RawQuery = q.Encode()

	return next.String()
}
