//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// TestServerAuditScope proves the installation audit scope @server is read
// through the audit API (ADR 60, decision 7). A namespace create writes an
// entry to @server, so the list of @server must hold it, and its chain must
// check valid. It runs against the real Pulumi CLI version and each store.
func TestServerAuditScope(t *testing.T) {
	t.Parallel()

	fetcher := sharedFetcher(t)

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	version := e2e.Pulumi().Single()
	if version == "" {
		t.Fatal("the Pulumi tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	cli, err := fetcher.Install(installCtx, e2e.Pulumi(), version)
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.Pulumi().Name, version, err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runServerAuditOnStore(t, cli, db)
		})
	}
}

// runServerAuditOnStore runs the @server audit steps on one store.
func runServerAuditOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnv(ctx, t, cli, store)

	checkReady(ctx, t, env)

	status, body, err := env.ServerAPIStatus(
		ctx, http.MethodPost, "/api/server/namespaces", `{"name":"serverscope"}`,
	)
	if err != nil || status != http.StatusCreated {
		t.Fatalf("create namespace answers %d, want %d (%v, %s)", status, http.StatusCreated, err, body)
	}

	created := checkServerAuditList(ctx, t, env)
	checkAuditEntryRead(ctx, t, env, "@server", created)
	checkServerAuditEntryMissing(ctx, t, env)
	checkServerAuditChain(ctx, t, env)
}

// checkServerAuditEntryMissing proves that a sequence that @server does not
// hold answers 404.
func checkServerAuditEntryMissing(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	const path = "/api/audit/entries/999999999?organization=@server"

	status, body, err := env.APIStatus(ctx, http.MethodGet, path, "")
	if err != nil || status != http.StatusNotFound {
		t.Fatalf("GET %s answers %d, want %d (%v, %s)", path, status, http.StatusNotFound, err, body)
	}
}

// checkServerAuditList proves the list of @server holds the namespace
// create, and returns that entry.
func checkServerAuditList(ctx context.Context, t *testing.T, env *e2e.Env) *auditListItem {
	t.Helper()

	status, body, err := env.APIStatus(ctx, http.MethodGet, "/api/audit/entries?organization=@server", "")
	if err != nil {
		t.Fatalf("list @server audit entries: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("list @server audit entries answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var got struct {
		Entries []auditListItem `json:"entries"`
		Total   int64           `json:"total"`
	}

	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode @server audit entries: %v (%s)", err, body)
	}

	for i := range got.Entries {
		if got.Entries[i].Action == "server:create-namespace" && got.Entries[i].Object == "namespace:serverscope" {
			return &got.Entries[i]
		}
	}

	t.Fatalf("@server list holds no server:create-namespace entry for namespace:serverscope: %+v", got.Entries)

	return nil
}

// checkServerAuditChain proves the chain of @server checks valid.
func checkServerAuditChain(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	status, body, err := env.APIStatus(ctx, http.MethodGet, "/api/audit/check?organization=@server", "")
	if err != nil {
		t.Fatalf("check @server audit chain: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("check @server audit chain answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var got struct {
		Valid bool `json:"valid"`
	}

	if err := json.Unmarshal([]byte(body), &got); err != nil || !got.Valid {
		t.Fatalf("@server chain check = %s, want valid", body)
	}
}
