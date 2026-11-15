//go:build e2e

package e2e_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
)

// registryNamespace is the namespace that holds the module release of this
// step.
const registryNamespace = "registry-backup"

// TestBackupRestoresRegistryFiles proves the registry file part of the backup
// guide with the real binary: the backup command copies the file of a
// published module release into the backup target and prints the count of
// copied files, and a restore of that backup onto an empty database and an
// empty blob store writes the file back, so the registry serves it again.
func TestBackupRestoresRegistryFiles(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skipf("the registry backup step needs Linux, not %s", runtime.GOOS)
	}

	t.Cleanup(http.DefaultClient.CloseIdleConnections)

	bin := e2e.BuildBinaryWithVersion(t.Context(), t, t.TempDir(), "")

	prod := t.TempDir()
	targetDir := filepath.Join(t.TempDir(), "backups")

	server := startProduction(t, bin, prod, targetDir)

	status, body := post(t, server.url, "/api/server/namespaces", `{"name":"`+registryNamespace+`"}`)
	if status != http.StatusCreated {
		t.Fatalf("POST namespace = %d, want %d (%s)", status, http.StatusCreated, body)
	}

	archive := moduleZip(t, "the restored module", nil)

	sum := sha256.Sum256(archive)
	contentKey := hex.EncodeToString(sum[:])

	if code, answer := publishRegistryModule(t, server, archive); code != http.StatusOK {
		t.Fatalf("PUT module version = %d, want %d (%s)", code, http.StatusOK, answer)
	}

	// The backup command of the guide, against this server.
	out, err := runBinary(t, bin, "backup", "--server", server.url)
	if err != nil {
		t.Fatalf("opendustpan backup: %v\n%s", err, out)
	}

	if want := "registry files: 1 files, " + strconv.Itoa(len(archive)) + " bytes"; !strings.Contains(out, want) {
		t.Fatalf("backup output %q lacks the line %q", out, want)
	}

	id := backupIDOf(t, out)

	_, row := get(t, server.url, "/api/server/backups/"+id)
	if !strings.Contains(row, `"registryFiles"`) || !strings.Contains(row, contentKey) {
		t.Fatalf("backup row names no registry file %q: %s", contentKey, row)
	}

	// An installation with an empty database and an empty blob store, with the
	// key and the backup target of the first. Only the backup holds the file.
	server.stop(t)

	for _, name := range []string{"serve.db", "blobs"} {
		if rmErr := os.RemoveAll(filepath.Join(prod, name)); rmErr != nil {
			t.Fatalf("remove %s: %v", name, rmErr)
		}
	}

	second := startProduction(t, bin, prod, targetDir)
	defer second.stop(t)

	restoreID := startRestore(t, second, id)

	waitProductionRestoreSucceeded(t, second, restoreID)

	fetched := downloadRegistryModule(t, second)
	if !bytes.Equal(fetched, archive) {
		t.Fatalf("the registry serves %d bytes, want the %d bytes of the published archive",
			len(fetched), len(archive))
	}
}

// backupIDOf reads the ID of the succeeded backup from the output of the
// backup command.
func backupIDOf(t *testing.T, out string) string {
	t.Helper()

	match := regexp.MustCompile(`backup (bkp-[a-z0-9]+) succeeded`).FindStringSubmatch(out)
	if match == nil {
		t.Fatalf("backup output holds no succeeded backup ID:\n%s", out)
	}

	return match[1]
}

// registryCall is one request to the registry API of the namespace.
type registryCall struct {
	method      string
	path        string
	contentType string
	body        string
}

// callRegistry sends one request to the registry API of the namespace with
// the administrator token.
func callRegistry(t *testing.T, server *productionServer, call registryCall) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), call.method, server.url+call.path, strings.NewReader(call.body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+serveToken)

	if call.contentType != "" {
		req.Header.Set("Content-Type", call.contentType)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", call.method, call.path, err)
	}

	raw, readErr := io.ReadAll(resp.Body)

	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("close body: %v", closeErr)
	}

	if readErr != nil {
		t.Fatalf("read %s %s: %v", call.method, call.path, readErr)
	}

	return resp.StatusCode, string(raw)
}

// publishRegistryModule stages the module archive and publishes it as one
// module release, the way the module guide shows.
func publishRegistryModule(t *testing.T, server *productionServer, archive []byte) (int, string) {
	t.Helper()

	base := "/api/dustpan/v1/namespaces/" + registryNamespace + "/registry"

	status, body := callRegistry(t, server, registryCall{
		method: http.MethodPost, path: base + "/uploads", contentType: "application/octet-stream",
		body: string(archive),
	})
	if status != http.StatusCreated {
		return status, body
	}

	var staged struct {
		Upload string `json:"upload"`
	}

	if err := json.Unmarshal([]byte(body), &staged); err != nil || staged.Upload == "" {
		t.Fatalf("stage answer %q: %v", body, err)
	}

	publish, err := json.Marshal(map[string]string{"upload": staged.Upload})
	if err != nil {
		t.Fatalf("marshal the publish body: %v", err)
	}

	return callRegistry(t, server, registryCall{
		method: http.MethodPut, path: base + "/modules/vpc/aws/versions/1.0.0", contentType: "application/json",
		body: string(publish),
	})
}

// waitProductionRestoreSucceeded polls the restore until it succeeds, and
// proves that it carries no registry file warning.
func waitProductionRestoreSucceeded(t *testing.T, server *productionServer, id string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Minute)

	for time.Now().Before(deadline) {
		_, body := get(t, server.url, "/api/server/restores/"+id)

		var doc restoreDoc
		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatalf("decode restore %q: %v", body, err)
		}

		switch doc.State {
		case "succeeded":
			for _, w := range doc.Warnings {
				if w.Code == "registry-files-absent" {
					t.Fatalf("restore %s warns %q, want the registry files", id, w.Code)
				}
			}

			return
		case "checking", "applying":
			select {
			case <-t.Context().Done():
				t.Fatalf("wait for restore %s: %v", id, t.Context().Err())
			case <-time.After(devPollInterval):
			}
		default:
			t.Fatalf("restore ends %q (%s), want succeeded", doc.State, doc.Error)
		}
	}

	t.Fatalf("restore %s never ended", id)
}

// downloadRegistryModule fetches the archive of the module release through
// the module protocol of OpenTofu, as the CLI does: the download answer names
// the signed file URL, and that URL serves the bytes.
func downloadRegistryModule(t *testing.T, server *productionServer) []byte {
	t.Helper()

	req, err := http.NewRequestWithContext(
		t.Context(), http.MethodGet,
		server.url+"/v1/modules/"+registryNamespace+"/vpc/aws/1.0.0/download", http.NoBody,
	)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+serveToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("module download: %v", err)
	}

	signed := resp.Header.Get("X-Terraform-Get")

	raw, readErr := io.ReadAll(resp.Body)

	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("close body: %v", closeErr)
	}

	if readErr != nil {
		t.Fatalf("read the download answer: %v", readErr)
	}

	if resp.StatusCode != http.StatusNoContent || signed == "" {
		t.Fatalf("module download = %d with file URL %q, want 204 with a URL (%s)",
			resp.StatusCode, signed, raw)
	}

	file, err := http.NewRequestWithContext(t.Context(), http.MethodGet, signed, http.NoBody)
	if err != nil {
		t.Fatalf("request the file: %v", err)
	}

	fileResp, err := http.DefaultClient.Do(file)
	if err != nil {
		t.Fatalf("module file: %v", err)
	}

	content, readErr := io.ReadAll(fileResp.Body)

	if closeErr := fileResp.Body.Close(); closeErr != nil {
		t.Fatalf("close body: %v", closeErr)
	}

	if readErr != nil {
		t.Fatalf("read the module file: %v", readErr)
	}

	if fileResp.StatusCode != http.StatusOK {
		t.Fatalf("module file = %d, want %d (%s)", fileResp.StatusCode, http.StatusOK, content)
	}

	return content
}
