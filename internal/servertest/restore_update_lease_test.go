package servertest_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/servertest"
	"opendustpan.org/opendustpan/pkg/backup"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
)

// updateTokenCall sends a request to server 0 with the update token as its
// credential, as the Pulumi CLI does, and returns the status and the body.
func updateTokenCall(t *testing.T, c *servertest.Cluster, method, path, leaseToken, body string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, c.URL(0)+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Authorization", "update-token "+leaseToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("close body: %v", closeErr)
		}
	}()

	var sb strings.Builder

	buf := make([]byte, 4096)

	for {
		n, readErr := resp.Body.Read(buf)
		sb.Write(buf[:n])

		if readErr != nil {
			break
		}
	}

	return resp.StatusCode, sb.String()
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

	if status, body := c.Do(t, 0, http.MethodPost, "/api/server/namespaces", `{"name":"acme"}`); status != http.StatusCreated {
		t.Fatalf("create namespace: status %d, body %s", status, body)
	}

	seedPlacement(t, c, 0, `{"name":"acme"}`)

	if status, body := c.Do(t, 0, http.MethodPost, "/api/stacks/acme/web", `{"stackName":"dev"}`); status != http.StatusOK {
		t.Fatalf("create stack: status %d, body %s", status, body)
	}

	// Start an update and keep it running.
	base := "/api/stacks/acme/web/dev/update"

	var created struct {
		UpdateID string `json:"updateID"`
	}

	status, body := c.Do(t, 0, http.MethodPost, base, "{}")
	if err = json.Unmarshal([]byte(body), &created); status != http.StatusOK || err != nil || created.UpdateID == "" {
		t.Fatalf("create update: status %d, body %s, %v", status, body, err)
	}

	updatePath := base + "/" + created.UpdateID

	var started struct {
		Token string `json:"token"`
	}

	status, body = c.Do(t, 0, http.MethodPost, updatePath, "{}")
	if err = json.Unmarshal([]byte(body), &started); status != http.StatusOK || err != nil || started.Token == "" {
		t.Fatalf("start update: status %d, body %s, %v", status, body, err)
	}

	// The lease works before the restore.
	if status, body = updateTokenCall(t, c, http.MethodPost, updatePath+"/renew_lease", started.Token,
		`{"duration":300}`); status != http.StatusOK {
		t.Fatalf("renew the lease before the restore: status %d, body %s", status, body)
	}

	// Back up while the update runs, then restore the backup.
	backupID := startBackup(t, c, 0)
	if got := waitBackup(t, c, 0, backupID, restoreLimit); got.State != string(backup.StateSucceeded) {
		t.Fatalf("backup %s = %q (%s), want succeeded", backupID, got.State, got.Error)
	}

	// The restore does not start while an update runs, so the live update
	// ends. The backup still holds it as running.
	if status, body = updateTokenCall(t, c, http.MethodPost, updatePath+"/complete", started.Token,
		`{"status":"succeeded"}`); status != http.StatusOK {
		t.Fatalf("complete the live update: status %d, body %s", status, body)
	}

	if got := runRestore(t, c, backupID); got.State != string(backup.RestoreSucceeded) {
		t.Fatalf("restore = %q (%s), want succeeded", got.State, got.Error)
	}

	// The old lease token gets 401 with the code update.lease-expired.
	status, body = updateTokenCall(t, c, http.MethodPost, updatePath+"/renew_lease", started.Token, `{"duration":300}`)

	if status != http.StatusUnauthorized || !strings.Contains(body, `"update.lease-expired"`) {
		t.Fatalf("renew with the old token after the restore: status %d, body %s, want 401 update.lease-expired", status, body)
	}

	// The update still runs and keeps the stack locked: a second update does
	// not start.
	second := startSecondUpdate(t, c, base)
	if second != http.StatusConflict {
		t.Errorf("start of a second update while the first runs: status %d, want %d", second, http.StatusConflict)
	}

	// The cancel ends the update, and the stack is unlocked.
	if status, body = c.Do(t, 0, http.MethodPost, updatePath+"/cancel", "{}"); status != http.StatusOK {
		t.Fatalf("cancel the restored update: status %d, body %s", status, body)
	}

	if second = startSecondUpdate(t, c, base); second != http.StatusOK {
		t.Errorf("start of an update after the cancel: status %d, want %d", second, http.StatusOK)
	}
}

// startSecondUpdate creates an update of the stack under base and starts it,
// and returns the status of the first request that does not answer 200, or 200.
func startSecondUpdate(t *testing.T, c *servertest.Cluster, base string) int {
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
