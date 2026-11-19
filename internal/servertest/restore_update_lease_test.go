package servertest_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/servertest"
	"opendustpan.org/opendustpan/pkg/backup"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
)

// leaseCall is one request with an update token, as the Pulumi CLI sends it.
type leaseCall struct {
	path  string
	token string
	body  string
}

// sendLeaseCall sends call to server 0 with the update token as its credential
// and returns the status and the body.
func sendLeaseCall(t *testing.T, c *servertest.Cluster, call leaseCall) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, c.URL(0)+call.path,
		strings.NewReader(call.body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Authorization", "update-token "+call.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", call.path, err)
	}

	data, readErr := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil || readErr != nil {
		t.Fatalf("read answer of POST %s: %v %v", call.path, readErr, closeErr)
	}

	return resp.StatusCode, string(data)
}

// runningUpdate is an update that runs, with its path and its lease token.
type runningUpdate struct {
	base string
	path string
	// token is the lease token that the start of the update gave.
	token string
}

// startRunningUpdate creates the stack web/dev in the namespace acme and
// starts an update of it.
func startRunningUpdate(t *testing.T, c *servertest.Cluster) runningUpdate {
	t.Helper()

	status, body := c.Do(t, 0, http.MethodPost, "/api/server/namespaces", `{"name":"acme"}`)
	if status != http.StatusCreated {
		t.Fatalf("create namespace: status %d, body %s", status, body)
	}

	seedPlacement(t, c, 0, `{"name":"acme"}`)

	status, body = c.Do(t, 0, http.MethodPost, "/api/stacks/acme/web", `{"stackName":"dev"}`)
	if status != http.StatusOK {
		t.Fatalf("create stack: status %d, body %s", status, body)
	}

	base := "/api/stacks/acme/web/dev/update"
	id := createUpdate(t, c, base)

	var started struct {
		Token string `json:"token"`
	}

	status, body = c.Do(t, 0, http.MethodPost, base+"/"+id, "{}")
	if err := json.Unmarshal([]byte(body), &started); status != http.StatusOK || err != nil || started.Token == "" {
		t.Fatalf("start update: status %d, body %s, %v", status, body, err)
	}

	return runningUpdate{base: base, path: base + "/" + id, token: started.Token}
}

// createUpdate creates an update of the stack under base and returns its ID.
func createUpdate(t *testing.T, c *servertest.Cluster, base string) string {
	t.Helper()

	var created struct {
		UpdateID string `json:"updateID"`
	}

	status, body := c.Do(t, 0, http.MethodPost, base, "{}")
	if err := json.Unmarshal([]byte(body), &created); status != http.StatusOK || err != nil || created.UpdateID == "" {
		t.Fatalf("create update: status %d, body %s, %v", status, body, err)
	}

	return created.UpdateID
}

// secondUpdateStatus creates and starts another update of the stack under base
// and returns the status of the first request that does not answer 200, or 200.
func secondUpdateStatus(t *testing.T, c *servertest.Cluster, base string) int {
	t.Helper()

	status, body := c.Do(t, 0, http.MethodPost, base, "{}")
	if status != http.StatusOK {
		return status
	}

	var created struct {
		UpdateID string `json:"updateID"`
	}

	if err := json.Unmarshal([]byte(body), &created); err != nil || created.UpdateID == "" {
		t.Fatalf("create update answer %s: %v", body, err)
	}

	status, _ = c.Do(t, 0, http.MethodPost, base+"/"+created.UpdateID, "{}")

	return status
}

// TestRestoreExpiresLeaseOfRunningUpdate checks the whole path of a restore of
// a backup that holds a running update: the old lease token stops with 401 and
// the code update.lease-expired, the update keeps the stack locked, and a
// cancel unlocks the stack.
func TestRestoreExpiresLeaseOfRunningUpdate(t *testing.T) {
	t.Parallel()

	target, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open backup target: %v", err)
	}

	c := servertest.Start(t, 1, dustpan.WithBackupTarget(target, "test"))
	update := startRunningUpdate(t, c)
	renew := leaseCall{path: update.path + "/renew_lease", token: update.token, body: `{"duration":300}`}

	// The lease works before the restore.
	status, body := sendLeaseCall(t, c, renew)
	if status != http.StatusOK {
		t.Fatalf("renew the lease before the restore: status %d, body %s", status, body)
	}

	// Back up while the update runs.
	backupID := startBackup(t, c, 0)
	if got := waitBackup(t, c, 0, backupID, restoreLimit); got.State != string(backup.StateSucceeded) {
		t.Fatalf("backup %s = %q (%s), want succeeded", backupID, got.State, got.Error)
	}

	// The restore does not start while an update runs, so the live update
	// ends. The backup still holds it as running.
	done := leaseCall{path: update.path + "/complete", token: update.token, body: `{"status":"succeeded"}`}

	status, body = sendLeaseCall(t, c, done)
	if status != http.StatusOK {
		t.Fatalf("complete the live update: status %d, body %s", status, body)
	}

	if got := runRestore(t, c, backupID); got.State != string(backup.RestoreSucceeded) {
		t.Fatalf("restore = %q (%s), want succeeded", got.State, got.Error)
	}

	// The old lease token gets 401 with the code update.lease-expired.
	status, body = sendLeaseCall(t, c, renew)
	if status != http.StatusUnauthorized || !strings.Contains(body, `"update.lease-expired"`) {
		t.Fatalf("renew with the old token after the restore: status %d, body %s, want 401 update.lease-expired",
			status, body)
	}

	checkCancelUnlocksStack(t, c, update)
}

// checkCancelUnlocksStack checks that the restored update keeps the stack
// locked until a cancel ends it.
func checkCancelUnlocksStack(t *testing.T, c *servertest.Cluster, update runningUpdate) {
	t.Helper()

	if got := secondUpdateStatus(t, c, update.base); got != http.StatusConflict {
		t.Errorf("start of a second update while the first runs: status %d, want %d", got, http.StatusConflict)
	}

	if status, body := c.Do(t, 0, http.MethodPost, update.path+"/cancel", "{}"); status != http.StatusOK {
		t.Fatalf("cancel the restored update: status %d, body %s", status, body)
	}

	if got := secondUpdateStatus(t, c, update.base); got != http.StatusOK {
		t.Errorf("start of an update after the cancel: status %d, want %d", got, http.StatusOK)
	}
}
