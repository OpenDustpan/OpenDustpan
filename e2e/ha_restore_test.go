//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/e2e"
)

// haRestoreOperation is the newest restore operation that the database holds.
type haRestoreOperation struct {
	id       string
	state    string
	owner    string
	expiries int64
}

// haReadRestoreOperation reads the newest restore operation of the database.
func haReadRestoreOperation(ctx context.Context, t *testing.T, conn *pgx.Conn) (haRestoreOperation, bool) {
	t.Helper()

	var op haRestoreOperation

	rows, err := conn.Query(ctx,
		`SELECT id, state, COALESCE(claim_owner, ''), expiries FROM durable_operations `+
			`WHERE kind = 'restore' ORDER BY created_at_ms DESC LIMIT 1`)
	if err != nil {
		t.Fatalf("read the restore operation: %v", err)
	}

	defer rows.Close()

	if !rows.Next() {
		return op, false
	}

	if err := rows.Scan(&op.id, &op.state, &op.owner, &op.expiries); err != nil {
		t.Fatalf("scan the restore operation: %v", err)
	}

	return op, true
}

// haWaitRestoreClaim waits until a worker holds the claim of the newest
// restore operation, and returns the operation.
func haWaitRestoreClaim(ctx context.Context, t *testing.T, conn *pgx.Conn) haRestoreOperation {
	t.Helper()

	ctx, cancel := context.WithTimeout(ctx, haLease+haBackupGrace)
	defer cancel()

	for {
		if op, ok := haReadRestoreOperation(ctx, t, conn); ok && op.state == "running" && op.owner != "" {
			return op
		}

		select {
		case <-ctx.Done():
			t.Fatalf("no worker claimed the restore: %v", ctx.Err())
		case <-time.After(haPollEvery):
		}
	}
}

// haStartRestore starts a restore of the backup id through the installation
// API and returns the restore ID.
func haStartRestore(ctx context.Context, t *testing.T, env *e2e.Env, id string) string {
	t.Helper()

	status, body, err := env.ServerAPIStatus(ctx, http.MethodPost, "/api/dustpan/v1/restores", `{"backupID":"`+id+`"}`)
	if err != nil || status != http.StatusAccepted {
		t.Fatalf("POST restores answers %d: %v (%s)", status, err, body)
	}

	var started restoreStartDoc

	if err := json.Unmarshal([]byte(body), &started); err != nil || started.ID == "" {
		t.Fatalf("POST restores answers %q, want an id: %v", body, err)
	}

	return started.ID
}

// haWaitRestoreSucceeded polls the restore id until it is succeeded, and
// fails the test when it fails or when the deadline passes. Any instance
// answers the state.
func haWaitRestoreSucceeded(ctx context.Context, t *testing.T, env *e2e.Env, id string, deadline time.Time) {
	t.Helper()

	for time.Now().Before(deadline) {
		status, body, err := env.ServerAPIStatus(ctx, http.MethodGet, "/api/dustpan/v1/restores/"+id, "")
		if err == nil && status == http.StatusOK {
			var doc restoreStateDoc

			if err := json.Unmarshal([]byte(body), &doc); err != nil {
				t.Fatalf("decode restore %s %q: %v", id, body, err)
			}

			switch doc.State {
			case "succeeded":
				return
			case "checking", "applying":
			default:
				t.Fatalf("restore %s reads %q with the error %q, want succeeded", id, doc.State, doc.Error)
			}
		}

		select {
		case <-ctx.Done():
			t.Fatalf("wait for restore %s: %v", id, ctx.Err())
		case <-time.After(haPollEvery):
		}
	}

	t.Fatalf("restore %s did not end succeeded within the lease period (%s) and %s of the kill",
		id, haLease, haBackupGrace)
}

// haWaitRestoreOperationEnd waits until the durable operation id has ended.
func haWaitRestoreOperationEnd(ctx context.Context, t *testing.T, conn *pgx.Conn, id string) haRestoreOperation {
	t.Helper()

	ctx, cancel := context.WithTimeout(ctx, haOperationEnd)
	defer cancel()

	for {
		op, ok := haReadRestoreOperation(ctx, t, conn)
		if !ok || op.id != id {
			t.Fatalf("the newest restore operation is %+v, want operation %s", op, id)
		}

		if op.state != "running" && op.state != "pending" {
			return op
		}

		select {
		case <-ctx.Done():
			t.Fatalf("the restore operation %s stays %s: %v", id, op.state, ctx.Err())
		case <-time.After(haPollEvery):
		}
	}
}

// haRestartDuringRestore takes a backup of the data of the earlier steps,
// starts a restore of it, finds the instance that holds the claim of the
// restore operation and sends it SIGKILL. The restore must end succeeded on
// another instance within the lease period plus 60 s, the operation must show
// one expiry, and the instance must start again.
func haRestartDuringRestore(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	cluster := env.HA(t)

	env.Recorder.SetStep("4 restart during restore")

	backupID := haStartBackup(ctx, t, env)
	haWaitBackupSucceeded(ctx, t, env, backupID, time.Now().Add(haLease+haBackupGrace))

	conn := haConnect(ctx, t, env)
	id := haStartRestore(ctx, t, env, backupID)
	op := haWaitRestoreClaim(ctx, t, conn)

	victim := cluster.IndexOfID(op.owner)
	if victim < 0 {
		t.Fatalf("the claim owner %q is no instance of the cluster", op.owner)
	}

	t.Logf("restore %s, operation %s: instance %d holds the claim", id, op.id, victim)
	haRestartOnCleanup(ctx, t, cluster, victim)

	killed := time.Now()
	if err := cluster.Kill(victim); err != nil {
		t.Fatalf("kill instance %d: %v", victim, err)
	}

	haWaitRestoreSucceeded(ctx, t, env, id, killed.Add(haLease+haBackupGrace))
	t.Logf("restore %s ended succeeded %s after the kill", id, time.Since(killed).Round(time.Millisecond))

	after := haWaitRestoreOperationEnd(ctx, t, conn, op.id)
	if after.state != "succeeded" || after.expiries != 1 {
		t.Fatalf("the restore operation reads state %q with %d expiries, want succeeded with 1; "+
			"if the restore ended before the kill, raise %s", after.state, after.expiries, haBackupMBEnv)
	}

	if err := cluster.Start(ctx, victim); err != nil {
		t.Fatalf("start instance %d again: %v", victim, err)
	}

	if err := cluster.WaitHealthy(ctx); err != nil {
		t.Fatalf("%v", err)
	}
}
