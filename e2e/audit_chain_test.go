//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
)

// auditChainWait bounds the wait for a migration: it waits four cache
// limits of maintenance mode, 8 seconds.
const auditChainWait = 60 * time.Second

// auditChainState is the answer of GET /api/server/audit/chain.
type auditChainState struct {
	State     string `json:"state"`
	Setting   string `json:"setting"`
	Pending   bool   `json:"pending"`
	Migrating bool   `json:"migrating"`
}

// TestAuditChain proves the change of the audit chain on the real server
// binary and each store: a restart with the setting off waits in
// maintenance mode, the approval migrates the log and leaves maintenance
// mode, and a restart with the setting on does the same back. The chain of
// @server checks valid after each change.
func TestAuditChain(t *testing.T) {
	t.Parallel()

	if !e2e.UseBinaryServer() {
		t.Skip("needs E2E_SERVER=binary: the test restarts the server")
	}

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	versions := e2e.Pulumi().Versions()
	if len(versions) == 0 {
		t.Fatal("the Pulumi tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	cli, err := fetcher.Install(installCtx, e2e.Pulumi(), versions[0])
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.Pulumi().Name, versions[0], err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()

			if db == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			ctx := t.Context()
			env := e2e.NewEnv(ctx, t, cli, db)

			if got := readAuditChain(ctx, t, env); got.State != "on" || got.Setting != "on" || got.Pending {
				t.Fatalf("audit chain of a new installation = %+v, want on with no waiting change", got)
			}

			changeAuditChain(ctx, t, env, "off")
			changeAuditChain(ctx, t, env, "on")
		})
	}
}

// changeAuditChain restarts the server of env with setting, proves that the
// change waits in maintenance mode, approves it, and waits until the
// migration ends.
func changeAuditChain(ctx context.Context, t *testing.T, env *e2e.Env, setting string) {
	t.Helper()

	env.StopServer(ctx, t)
	env.SetAuditChain(setting)
	env.StartServer(ctx, t)

	if got := readAuditChain(ctx, t, env); got.Setting != setting || !got.Pending {
		t.Fatalf("audit chain after a restart with %s = %+v, want a waiting change", setting, got)
	}

	if got := getMaintenanceState(ctx, t, env); got != "maintenance" {
		t.Fatalf("installation state while a change waits = %q, want maintenance", got)
	}

	status, body, err := env.ServerAPIStatus(ctx, http.MethodPost, "/api/server/audit/chain", "")
	if err != nil {
		t.Fatalf("approve the audit chain change: %v", err)
	}

	if status != http.StatusAccepted {
		t.Fatalf("POST /api/server/audit/chain answers %d, want %d (%s)", status, http.StatusAccepted, body)
	}

	deadline := time.Now().Add(auditChainWait)

	for {
		got := readAuditChain(ctx, t, env)
		if got.State == setting && !got.Pending && !got.Migrating &&
			getMaintenanceState(ctx, t, env) == "normal" {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("audit chain after %s = %+v, want %s with the migration complete", auditChainWait, got, setting)
		}

		pauseAuditChain(ctx, t)
	}

	checkServerAuditChain(ctx, t, env)
}

// readAuditChain answers the state of the audit chain of env.
func readAuditChain(ctx context.Context, t *testing.T, env *e2e.Env) auditChainState {
	t.Helper()

	status, body, err := env.ServerAPIStatus(ctx, http.MethodGet, "/api/server/audit/chain", "")
	if err != nil {
		t.Fatalf("read the audit chain: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("GET /api/server/audit/chain answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var got auditChainState
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode the audit chain %s: %v", body, err)
	}

	return got
}

// pauseAuditChain waits a short time, or fails the test when ctx ends first.
func pauseAuditChain(ctx context.Context, t *testing.T) {
	t.Helper()

	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		t.Fatal("context done while the audit chain migrates")
	case <-timer.C:
	}
}
