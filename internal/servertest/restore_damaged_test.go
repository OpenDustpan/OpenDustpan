package servertest_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"slices"
	"strconv"
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

// findingsDoc is what the backup before a restore found at the live blobs.
type findingsDoc struct {
	Named   int      `json:"named"`
	Missing int      `json:"missing"`
	Damaged int      `json:"damaged"`
	Keys    []string `json:"keys"`
}

// limitDoc is the bound on missing live blobs in a restore answer.
type limitDoc struct {
	Count        int `json:"count"`
	Percent      int `json:"percent"`
	PercentAbove int `json:"percentAbove"`
}

// restoreDoc is the part of the answer of GET /api/server/restores/{id} that
// these tests read.
type restoreDoc struct {
	ID               string              `json:"id"`
	State            string              `json:"state"`
	PreBackupID      string              `json:"preBackupID"`
	Error            string              `json:"error"`
	Warnings         []restoreWarningDoc `json:"warnings"`
	PreBackupFound   *findingsDoc        `json:"preBackupFound"`
	MissingBlobLimit limitDoc            `json:"missingBlobLimit"`
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

	return restoreDoc{
		ID: id, State: "", PreBackupID: "", Error: "", Warnings: nil, PreBackupFound: nil,
		MissingBlobLimit: limitDoc{Count: 0, Percent: 0, PercentAbove: 0},
	}
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

// damagedRig is a server with OpenTofu workspaces and a backup of them.
type damagedRig struct {
	c        *servertest.Cluster
	root     *os.Root
	backupID string
	victim   string
	first    []byte
	other    []byte
	names    []string
}

// newDamagedRig starts a server, writes the workspaces w1 to wn, and takes a
// backup. Each workspace has one state blob.
func newDamagedRig(t *testing.T, n int) damagedRig {
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

	seedPlacement(t, c, 0, `{"name":"acme"}`)

	names := make([]string, 0, n)

	for i := range n {
		name := "w" + strconv.Itoa(i+1)
		writeWorkspace(t, c, name)
		names = append(names, name)
	}

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
	if len(blobs) != n {
		t.Fatalf("state blobs = %v, want %d", blobs, n)
	}

	return damagedRig{
		c: c, root: root, backupID: backupID, victim: blobs[0],
		first: readFile(t, root, blobs[0]), other: readFile(t, root, blobs[1]), names: names,
	}
}

// mustStatesRead checks that the state of each workspace of the rig reads
// back.
func (r damagedRig) mustStatesRead(t *testing.T, name string) {
	t.Helper()

	for _, ws := range r.names {
		status, body := tofuDo(t, r.c, http.MethodGet, "/tofu/state/acme/"+ws, "")
		if status != http.StatusOK || !strings.Contains(body, `"lineage":"`+ws+`"`) {
			t.Fatalf("%s: read state %s: status %d, body %.200s", name, ws, status, body)
		}
	}
}

// TestRestoreWritesAMissingStateBlob runs the reproduction of a restore over
// a lost live state blob: two workspaces, a backup, then one live blob that
// is deleted. One missing blob is within the bound at every size, so the
// restore writes the blob of the backup at the absent key: that adds a key
// and changes none. The answer names the missing blob and the bound, and
// every state reads back.
func TestRestoreWritesAMissingStateBlob(t *testing.T) {
	t.Parallel()

	r := newDamagedRig(t, 2)

	if err := r.root.Remove(r.victim); err != nil {
		t.Fatalf("delete the live blob: %v", err)
	}

	doc := runRestore(t, r.c, r.backupID)
	if doc.State != string(backup.RestoreSucceeded) {
		t.Fatalf("restore = %q (%s), want succeeded", doc.State, doc.Error)
	}

	if !slices.Contains(doc.Warnings, restoreWarningDoc{Code: string(backup.WarnMissingLiveBlobs)}) {
		t.Fatalf("warnings = %+v, want %q", doc.Warnings, backup.WarnMissingLiveBlobs)
	}

	if doc.PreBackupFound == nil || doc.PreBackupFound.Missing != 1 || doc.PreBackupFound.Named != 2 ||
		doc.PreBackupFound.Damaged != 0 || !slices.Contains(doc.PreBackupFound.Keys, r.victim) {
		t.Fatalf("preBackupFound = %+v, want 1 missing of 2 with the key %s", doc.PreBackupFound, r.victim)
	}

	if doc.MissingBlobLimit != (limitDoc{Count: 10, Percent: 20, PercentAbove: 1}) {
		t.Fatalf("missingBlobLimit = %+v, want 10 blobs, or 20 percent when more than 1", doc.MissingBlobLimit)
	}

	if got := readFile(t, r.root, r.victim); !bytes.Equal(got, r.first) {
		t.Fatalf("live blob after the restore = %d bytes, want its first bytes", len(got))
	}

	r.mustStatesRead(t, "a live blob that is absent")
}

// TestRestoreStopsWhenTooManyStateBlobsAreMissing deletes both live state
// blobs, as a blob store setting that names another store does: 2 of 2 is
// above the bound. The restore stops before any change of the live data with
// the text that names the count and the next step, and the keys stay absent.
func TestRestoreStopsWhenTooManyStateBlobsAreMissing(t *testing.T) {
	t.Parallel()

	r := newDamagedRig(t, 2)
	blobs := stateBlobs(t, r.root)

	for _, key := range blobs {
		if err := r.root.Remove(key); err != nil {
			t.Fatalf("delete the live blob: %v", err)
		}
	}

	doc := runRestore(t, r.c, r.backupID)
	if doc.State != string(backup.RestoreFailed) || !strings.Contains(doc.Error, "found 2 of 2 live blobs missing") ||
		!strings.Contains(doc.Error, "store check --target blobs") {
		t.Fatalf("restore = %q (%s), want failed at the bound", doc.State, doc.Error)
	}

	if doc.PreBackupFound == nil || doc.PreBackupFound.Missing != 2 {
		t.Fatalf("preBackupFound = %+v, want 2 missing", doc.PreBackupFound)
	}

	for _, key := range blobs {
		if _, err := r.root.Stat(key); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("stat the live blob %s = %v, want absent", key, err)
		}
	}
}

// TestRestoreStopsAtSealedStateThatDoesNotOpen runs the reproduction of a
// restore over a live state blob whose sealed bytes do not open or do not
// match: bytes that are not sealed, a flipped byte, a truncated blob, and the
// sealed bytes of the other workspace. The restore stops before any change of
// the live data, the error of the restore answer names the key and the next
// step, and the live blob keeps its bytes.
func TestRestoreStopsAtSealedStateThatDoesNotOpen(t *testing.T) {
	t.Parallel()

	r := newDamagedRig(t, 2)

	flipped := bytes.Clone(r.first)
	flipped[len(flipped)/2] ^= 0xff

	for _, tc := range []struct {
		name  string
		bytes []byte
	}{
		{name: "a live blob that holds other bytes", bytes: []byte("tampered")},
		{name: "a live blob with one flipped byte", bytes: flipped},
		{name: "a live blob that is truncated", bytes: r.first[:len(r.first)/2]},
		{name: "a live blob that holds the sealed bytes of the other workspace", bytes: r.other},
	} {
		if err := r.root.WriteFile(r.victim, tc.bytes, 0o600); err != nil {
			t.Fatalf("%s: damage the live blob: %v", tc.name, err)
		}

		doc := runRestore(t, r.c, r.backupID)
		if doc.State != string(backup.RestoreFailed) || !strings.Contains(doc.Error, strconv.Quote(r.victim)) ||
			!strings.Contains(
				doc.Error,
				"changed no live data",
			) || !strings.Contains(doc.Error, "opendustpan restore") {
			t.Fatalf("%s: restore = %q (%s), want failed with the key and the next step", tc.name, doc.State, doc.Error)
		}

		if doc.PreBackupFound == nil || doc.PreBackupFound.Damaged != 1 {
			t.Fatalf("%s: preBackupFound = %+v, want 1 damaged", tc.name, doc.PreBackupFound)
		}

		if got := readFile(t, r.root, r.victim); !bytes.Equal(got, tc.bytes) {
			t.Fatalf("%s: live blob after the restore = %d bytes, want the bytes that it held", tc.name, len(got))
		}
	}
}
