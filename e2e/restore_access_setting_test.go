//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
)

// TestRestoreAccessSetting proves the restore access setting against the real
// binary on each store. The setting is off, so an operator restore keeps the
// access tables. An access token cannot turn the setting on. A console session
// with a fresh step turns it on. Then the same operator restore replaces the
// access tables, which brings back a token that the live data revoked.
func TestRestoreAccessSetting(t *testing.T) {
	t.Parallel()

	stores, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, store := range stores {
		t.Run(string(store), func(t *testing.T) {
			t.Parallel()

			if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			restoreAccessScenario(t, store)
		})
	}
}

// restoreAccessScenario runs the steps of TestRestoreAccessSetting on store.
func restoreAccessScenario(t *testing.T, store e2e.Store) {
	t.Helper()

	ctx := t.Context()
	env := e2e.NewEnvWithSignIn(ctx, t, e2e.NoCLI(), store)

	if env.InProcess() {
		t.Skip("needs E2E_SERVER=binary: the restore worker runs in the server binary")
	}

	// The refusal of an access token below needs freshAuthEnabled on. It goes
	// on before the backup, so that a restore keeps it on.
	env.EnableFreshAuth(ctx, t)

	operator := makeOperatorToken(ctx, t, env, "operator")
	victim := makeVictimToken(ctx, t, env)
	backupID := haStartBackup(ctx, t, env)
	waitBackupSucceeded(ctx, t, env, backupID)

	revokeToken(ctx, t, env, victim.ID)

	// The setting is off: the restore keeps the access tables.
	waitRestoreSucceeded(ctx, t, env, startRestoreAs(ctx, t, env, operator, backupID))
	checkTokenRevoked(ctx, t, env, victim.ID)

	read, doc := settingsRead(ctx, t, env, settingsRoute)
	item := settingFor(t, doc, "restore.replace-access-tables")

	if on, isBool := item.Value.(bool); !isBool || on {
		t.Fatalf("restore.replace-access-tables = %+v, want false by default", item)
	}

	tag := read.Header.Get("ETag")
	on := `{"restore.replace-access-tables":true}`

	// An access token never gives the fresh step.
	resp, refused := settingsPatch(ctx, t, env, settingsReq{path: settingsRoute, ifMatch: tag, body: on})
	if resp.Status != http.StatusForbidden || refused.ErrorCode != "mfa.fresh-step-required" {
		t.Fatalf("PATCH with an access token answers %d %q (%s), want 403 mfa.fresh-step-required",
			resp.Status, refused.ErrorCode, resp.Body)
	}

	status, body := env.FreshAdminMergePatch(ctx, t, settingsRoute, tag, on)
	if status != http.StatusOK || !strings.Contains(body, `"restore.replace-access-tables"`) {
		t.Fatalf("PATCH with a fresh console session answers %d %s, want 200", status, body)
	}

	// The setting is on: the operator restore replaces the access
	// tables, so the token is active again, as the backup holds it.
	waitRestoreSucceeded(ctx, t, env, startRestoreAs(ctx, t, env, operator, backupID))
	checkTokenActive(ctx, t, env, victim.ID)
}

// checkTokenActive proves the token id is not revoked.
func checkTokenActive(ctx context.Context, t *testing.T, env *e2e.Env, id string) {
	t.Helper()

	status, body, err := env.ServerAPIStatus(ctx, http.MethodGet, "/api/server/tokens/"+id, "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("get token %s answers %d %v (%s), want 200", id, status, err, body)
	}

	var token e2eToken
	if err := json.Unmarshal([]byte(body), &token); err != nil {
		t.Fatalf("decode token %s %q: %v", id, body, err)
	}

	if token.RevokedAt != nil && *token.RevokedAt != "" {
		t.Fatalf("token %s is revoked after a restore with the setting on, want active", id)
	}
}

// waitBackupSucceeded polls the backup id until it succeeds, and fails the test
// when it fails or when two minutes pass.
func waitBackupSucceeded(ctx context.Context, t *testing.T, env *e2e.Env, id string) {
	t.Helper()

	deadline, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()

	for {
		status, body, err := env.ServerAPIStatus(ctx, http.MethodGet, "/api/dustpan/v1/backups/"+id, "")
		if err != nil || status != http.StatusOK {
			t.Fatalf("get backup %s answers %d %v (%s), want 200", id, status, err, body)
		}

		var doc backupDoc
		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatalf("decode backup %s %q: %v", id, body, err)
		}

		switch doc.State {
		case "succeeded":
			return
		case "running":
		default:
			t.Fatalf("backup %s reads %q with the error %q, want succeeded", id, doc.State, doc.Error)
		}

		select {
		case <-deadline.Done():
			t.Fatalf("backup %s stays in %q: %v", id, doc.State, deadline.Err())
		case <-timer.C:
			timer.Reset(100 * time.Millisecond)
		}
	}
}
