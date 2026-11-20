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

// restoreStartDoc decodes POST /api/dustpan/v1/restores: the started restore ID.
type restoreStartDoc struct {
	ID string `json:"id"`
}

// restoreStateDoc decodes GET /api/dustpan/v1/restores/{id}: the restore state.
type restoreStateDoc struct {
	ID    string `json:"id"`
	State string `json:"state"`
	Error string `json:"error"`
}

// checkOperatorRestoreKeepsRevokedToken proves an installation-operator
// restore cannot bring back a revoked token: it takes a backup with an
// active token, revokes the token, restores the backup as the operator, and
// proves the token stays revoked.
func checkOperatorRestoreKeepsRevokedToken(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	operatorSecret := makeOperatorToken(ctx, t, env, "operator")
	victim := makeVictimToken(ctx, t, env)

	backupID := runBackupCommand(ctx, t, env)

	revokeToken(ctx, t, env, victim.ID)

	restoreID := startRestoreAs(ctx, t, env, operatorSecret, backupID)
	waitRestoreSucceeded(ctx, t, env, restoreID)
	checkTokenRevoked(ctx, t, env, victim.ID)
}

// checkOperatorRestoreKeepsTempAdmin proves a restore without
// server:restore-access keeps the live temporary administrators: it takes a
// backup, makes a temporary administrator on the host, restores the backup as
// the installation operator, and proves the principal keeps its
// site-administrator grant and its expiry. A restore that took temp_admins
// from the backup would drop the record of the live administrator, and a row
// of the backup could end it. It reports false when the store cannot run the
// host command in this mode: a SQLite database needs the binary server, which
// the command opens while the server is stopped.
func checkOperatorRestoreKeepsTempAdmin(
	ctx context.Context, t *testing.T, env *e2e.Env, store e2e.Store,
) bool {
	t.Helper()

	if store == e2e.StoreSQLite && !e2e.UseBinaryServer() {
		return false
	}

	const person = "restore-temp-admin"

	operatorSecret := makeOperatorToken(ctx, t, env, "operator-temp")
	backupID := runBackupCommand(ctx, t, env)

	if store == e2e.StoreSQLite {
		env.StopServer(ctx, t)
	}

	token, expires := runHostBinary(ctx, t, env, person)

	if store == e2e.StoreSQLite {
		env.StartServer(ctx, t)
	}

	session := hostSession{token: token, person: person, expires: expires}
	checkHostPrincipal(ctx, t, env, session)

	restoreID := startRestoreAs(ctx, t, env, operatorSecret, backupID)
	waitRestoreSucceeded(ctx, t, env, restoreID)

	// The principal list needs the site-administrator grant of the person, and
	// shows the live record with its fixed expiry.
	checkHostPrincipal(ctx, t, env, session)

	return true
}

// makeOperatorToken creates a principal named name with the
// installation-operator role and returns the secret of its token.
func makeOperatorToken(ctx context.Context, t *testing.T, env *e2e.Env, name string) string {
	t.Helper()

	var operator e2ePrincipal
	principalCall(ctx, t, env, principalStep{
		http.MethodPost,
		"/api/server/principals",
		`{"subject":"` + e2e.SignInSubject(name) + `","kind":"person"}`,
		http.StatusCreated,
	}, &operator)

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/grants",
		`{"subject":"principal:` + operator.ID + `","role":"installation-operator"}`,
		http.StatusCreated,
	}, nil)

	return env.PersonalToken(ctx, t, operator.ID, name).Secret
}

// makeVictimToken creates a principal with a token and returns the token.
func makeVictimToken(ctx context.Context, t *testing.T, env *e2e.Env) e2e.PersonalToken {
	t.Helper()

	var victim e2ePrincipal
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals", `{"subject":"` + e2e.SignInSubject("victim") + `","kind":"person"}`,
		http.StatusCreated,
	}, &victim)

	return env.PersonalToken(ctx, t, victim.ID, "victim")
}

// revokeToken revokes the token id through the installation API.
func revokeToken(ctx context.Context, t *testing.T, env *e2e.Env, id string) {
	t.Helper()

	status, body, err := env.ServerAPIStatus(ctx, http.MethodDelete, "/api/server/tokens/"+id, "")
	if err != nil {
		t.Fatalf("revoke token %s: %v", id, err)
	}

	if status != http.StatusNoContent {
		t.Fatalf("revoke token %s answers %d, want %d (%s)", id, status, http.StatusNoContent, body)
	}
}

// startRestoreAs starts a restore of backupID with the token secret and
// returns the restore ID.
func startRestoreAs(ctx context.Context, t *testing.T, env *e2e.Env, secret, backupID string) string {
	t.Helper()

	status, body, err := env.ServerAPIStatusAs(
		ctx, secret, http.MethodPost, "/api/dustpan/v1/restores", `{"backupID":"`+backupID+`"}`,
	)
	if err != nil {
		t.Fatalf("start restore of %s as operator: %v", backupID, err)
	}

	if status != http.StatusAccepted {
		t.Fatalf("start restore of %s as operator answers %d, want %d (%s)",
			backupID, status, http.StatusAccepted, body)
	}

	var doc restoreStartDoc
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode restore start %q: %v", body, err)
	}

	if doc.ID == "" {
		t.Fatalf("restore start %q holds no ID", body)
	}

	return doc.ID
}

// waitRestoreSucceeded polls the restore until it succeeds.
func waitRestoreSucceeded(ctx context.Context, t *testing.T, env *e2e.Env, id string) {
	t.Helper()

	deadline, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()

	for {
		status, body, err := env.ServerAPIStatus(ctx, http.MethodGet, "/api/dustpan/v1/restores/"+id, "")
		if err != nil {
			t.Fatalf("get restore %s: %v", id, err)
		}

		if status != http.StatusOK {
			t.Fatalf("get restore %s answers %d, want %d (%s)", id, status, http.StatusOK, body)
		}

		var doc restoreStateDoc
		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatalf("decode restore %s %q: %v", id, body, err)
		}

		switch doc.State {
		case "succeeded":
			return
		case "checking", "applying", "running":
		default:
			t.Fatalf("restore %s reads %q with error %q, want succeeded", id, doc.State, doc.Error)
		}

		select {
		case <-deadline.Done():
			t.Fatalf("restore %s stays in %q, want succeeded: %v", id, doc.State, deadline.Err())
		case <-timer.C:
			timer.Reset(100 * time.Millisecond)
		}
	}
}

// checkTokenRevoked proves the token id stays revoked after the restore.
func checkTokenRevoked(ctx context.Context, t *testing.T, env *e2e.Env, id string) {
	t.Helper()

	status, body, err := env.ServerAPIStatus(ctx, http.MethodGet, "/api/server/tokens/"+id, "")
	if err != nil {
		t.Fatalf("get token %s: %v", id, err)
	}

	if status != http.StatusOK {
		t.Fatalf("get token %s answers %d, want %d (%s)", id, status, http.StatusOK, body)
	}

	var token e2eToken
	if err := json.Unmarshal([]byte(body), &token); err != nil {
		t.Fatalf("decode token %s %q: %v", id, body, err)
	}

	if token.RevokedAt == nil || *token.RevokedAt == "" {
		t.Fatalf("token %s is active after an operator restore, want revoked", id)
	}
}
