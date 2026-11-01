package servertest_test

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/servertest"
	"opendustpan.org/opendustpan/pkg/backup"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
)

// restoreLimit bounds the wait for one backup or restore of these tests.
const restoreLimit = 2 * time.Minute

// tofuState returns a small OpenTofu state with serial and lineage.
func tofuState(lineage string) string {
	return `{"version":4,"terraform_version":"1.10.0","serial":1,"lineage":"` + lineage +
		`","outputs":{"name":{"value":"` + lineage + `","type":"string"}},"resources":[],"check_results":null}`
}

// tofuDo sends one request of the OpenTofu http backend to server 0, with the
// token as the password of basic auth, as the CLI sends it.
func tofuDo(t *testing.T, c *servertest.Cluster, method, path, body string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, c.URL(0)+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.SetBasicAuth("tofu", servertest.Token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}

	data, readErr := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil || readErr != nil {
		t.Fatalf("read the answer of %s %s: %v %v", method, path, readErr, closeErr)
	}

	return resp.StatusCode, string(data)
}

// writeWorkspace writes one state of workspace name with LOCK, POST ?ID= and
// UNLOCK, as the OpenTofu http backend does.
func writeWorkspace(t *testing.T, c *servertest.Cluster, name string) {
	t.Helper()

	path := "/tofu/state/acme/" + name
	lock := `{"ID":"lock-` + name + `","Operation":"OperationTypeApply","Info":"","Who":"qa","Version":"1.10.0",` +
		`"Created":"2026-11-01T00:00:00Z","Path":""}`

	for _, step := range []struct{ method, path, body string }{
		{method: "LOCK", path: path, body: lock},
		{method: http.MethodPost, path: path + "?ID=lock-" + name, body: tofuState(name)},
		{method: "UNLOCK", path: path, body: lock},
	} {
		if status, body := tofuDo(t, c, step.method, step.path, step.body); status != http.StatusOK {
			t.Fatalf("%s %s: status %d, body %s", step.method, step.path, status, body)
		}
	}
}

// stateBlobs returns the keys of the sealed state blobs in the blob
// directory of the cluster, relative to it, in order.
func stateBlobs(t *testing.T, root *os.Root) []string {
	t.Helper()

	var out []string

	err := fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() && strings.Contains(path, "/tofu/") {
			out = append(out, path)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("walk the blob directory: %v", err)
	}

	slices.Sort(out)

	return out
}

// restoreWarningDoc is one warning of a restore answer.
type restoreWarningDoc struct {
	Code string `json:"code"`
}

// restoreDoc is the part of the answer of GET /api/server/restores/{id} that
// these tests read.
type restoreDoc struct {
	ID          string              `json:"id"`
	State       string              `json:"state"`
	PreBackupID string              `json:"preBackupID"`
	Error       string              `json:"error"`
	Warnings    []restoreWarningDoc `json:"warnings"`
}

// startRestore starts a restore of backupID on server 0 and returns its ID.
func startRestore(t *testing.T, c *servertest.Cluster, backupID string) string {
	t.Helper()

	status, body := c.Do(t, 0, http.MethodPost, "/api/server/restores", `{"backupID":"`+backupID+`"}`)
	if status != http.StatusAccepted {
		t.Fatalf("start restore: status %d, body %s", status, body)
	}

	var doc restoreDoc
	if err := json.Unmarshal([]byte(body), &doc); err != nil || doc.ID == "" {
		t.Fatalf("start restore answer %q: %v", body, err)
	}

	return doc.ID
}

// runRestore restores backupID on server 0 and returns the ended restore.
func runRestore(t *testing.T, c *servertest.Cluster, backupID string) restoreDoc {
	t.Helper()

	id := startRestore(t, c, backupID)
	deadline := time.Now().Add(restoreLimit)

	for time.Now().Before(deadline) {
		status, body := c.Do(t, 0, http.MethodGet, "/api/server/restores/"+id, "")
		if status != http.StatusOK {
			t.Fatalf("read restore %s: status %d, body %s", id, status, body)
		}

		var doc restoreDoc
		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatalf("restore answer %q: %v", body, err)
		}

		if doc.State == string(backup.RestoreSucceeded) || doc.State == string(backup.RestoreFailed) {
			return doc
		}

		select {
		case <-t.Context().Done():
			t.Fatalf("wait for restore %s: %v", id, t.Context().Err())
		case <-time.After(50 * time.Millisecond):
		}
	}

	t.Fatalf("restore %s did not end in time", id)

	return restoreDoc{ID: id, State: "", PreBackupID: "", Error: "", Warnings: nil}
}

// readFile reads the file of key under root.
func readFile(t *testing.T, root *os.Root, key string) []byte {
	t.Helper()

	data, err := root.ReadFile(key)
	if err != nil {
		t.Fatalf("read %s: %v", key, err)
	}

	return data
}

// damagedRig is a server with two OpenTofu workspaces and a backup of them.
type damagedRig struct {
	c        *servertest.Cluster
	root     *os.Root
	backupID string
	victim   string
	first    []byte
	other    []byte
}

// newDamagedRig starts a server, writes the workspaces w1 and w2, and takes
// a backup.
func newDamagedRig(t *testing.T) damagedRig {
	t.Helper()

	target, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open backup target: %v", err)
	}

	c := servertest.Start(t, 1, dustpan.WithBackupTarget(target, "test"))

	status, body := c.Do(t, 0, http.MethodPost, "/api/server/namespaces", `{"name":"acme"}`)
	if status != http.StatusCreated {
		t.Fatalf("create namespace: status %d, body %s", status, body)
	}

	writeWorkspace(t, c, "w1")
	writeWorkspace(t, c, "w2")

	backupID := startBackup(t, c, 0)
	if got := waitBackup(t, c, 0, backupID, restoreLimit); got.State != string(backup.StateSucceeded) {
		t.Fatalf("backup %s = %q (%s), want succeeded", backupID, got.State, got.Error)
	}

	root, err := os.OpenRoot(c.BlobDir())
	if err != nil {
		t.Fatalf("open the blob directory: %v", err)
	}

	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Errorf("close the blob directory: %v", err)
		}
	})

	blobs := stateBlobs(t, root)
	if len(blobs) != 2 {
		t.Fatalf("state blobs = %v, want 2", blobs)
	}

	return damagedRig{
		c: c, root: root, backupID: backupID, victim: blobs[0],
		first: readFile(t, root, blobs[0]), other: readFile(t, root, blobs[1]),
	}
}

// mustRepaired restores the backup and checks that the restore succeeded with
// the warning of damaged live blobs, that the live blob holds its first
// bytes, and that both states read back.
func (r damagedRig) mustRepaired(t *testing.T, name string) {
	t.Helper()

	doc := runRestore(t, r.c, r.backupID)
	if doc.State != string(backup.RestoreSucceeded) {
		t.Fatalf("%s: restore = %q (%s), want succeeded", name, doc.State, doc.Error)
	}

	if !slices.Contains(doc.Warnings, restoreWarningDoc{Code: string(backup.WarnDamagedLiveBlobs)}) {
		t.Fatalf("%s: warnings = %+v, want %q", name, doc.Warnings, backup.WarnDamagedLiveBlobs)
	}

	if got := readFile(t, r.root, r.victim); !bytes.Equal(got, r.first) {
		t.Fatalf("%s: live blob after the restore = %d bytes, want its first bytes", name, len(got))
	}

	for _, ws := range []string{"w1", "w2"} {
		status, body := tofuDo(t, r.c, http.MethodGet, "/tofu/state/acme/"+ws, "")
		if status != http.StatusOK || !strings.Contains(body, `"lineage":"`+ws+`"`) {
			t.Fatalf("%s: read state %s: status %d, body %.200s", name, ws, status, body)
		}
	}
}

// TestRestoreRepairsMissingAndDamagedStateBlobs runs the reproduction of a
// restore over a lost or damaged live state blob: two workspaces, a backup,
// then a live blob that is deleted, that holds other bytes, or that holds the
// sealed bytes of the other workspace. Each restore succeeds with the warning
// of damaged live blobs, the live blob holds its first bytes again, and the
// state reads back.
func TestRestoreRepairsMissingAndDamagedStateBlobs(t *testing.T) {
	t.Parallel()

	r := newDamagedRig(t)

	for _, tc := range []struct {
		name   string
		damage func() error
	}{
		{name: "a live blob that is absent", damage: func() error { return r.root.Remove(r.victim) }},
		{name: "a live blob that holds other bytes", damage: func() error {
			return r.root.WriteFile(r.victim, []byte("tampered"), 0o600)
		}},
		{name: "a live blob that holds the sealed bytes of the other workspace", damage: func() error {
			return r.root.WriteFile(r.victim, r.other, 0o600)
		}},
	} {
		if err := tc.damage(); err != nil {
			t.Fatalf("%s: damage the live blob: %v", tc.name, err)
		}

		r.mustRepaired(t, tc.name)
	}
}
