package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/blob"
)

// checkBackupDoc decodes a backup check answer in tests.
type checkBackupDoc struct {
	ID       string `json:"id"`
	Complete bool   `json:"complete"`
	Error    string `json:"error"`
}

// readBackupCheck reads the check of id and returns its status and document,
// failing the test when the body is not the check shape.
func readBackupCheck(t *testing.T, fix backupFixture, id string) (int, checkBackupDoc) {
	t.Helper()

	status, body := serveBackup(t, fix.handler, http.MethodGet, "/api/dustpan/v1/backups/"+id+"/check")

	var doc checkBackupDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode backup check %s %q: %v", id, body, err)
	}

	return status, doc
}

// checkBackupStores is a Handler with the stores its backup service runs
// over, so a test can delete target objects after a backup succeeds.
type checkBackupStores struct {
	fixture backupFixture
	stores  backupStores
}

// openCheckFixture builds a Handler with a backup service over fresh stores
// and returns both, so the caller can corrupt the target after a backup.
func openCheckFixture(t *testing.T) checkBackupStores {
	t.Helper()

	stores := openBackupDeps(t)
	fix := newBackupFixture(t, stores, stores.registry, nil)

	return checkBackupStores{fixture: fix, stores: stores}
}

// TestBackupCheckComplete proves the check of a succeeded backup answers
// complete true with its ID.
func TestBackupCheckComplete(t *testing.T) {
	t.Parallel()

	fix := openCheckFixture(t).fixture
	id := startBackup(t, fix.handler)
	pollBackupSucceeded(t, fix.handler, id)

	status, doc := readBackupCheck(t, fix, id)
	if status != http.StatusOK {
		t.Fatalf("GET backup %s check answers %d, want %d", id, status, http.StatusOK)
	}

	if !doc.Complete || doc.ID != id || doc.Error != "" {
		t.Fatalf("backup check = %+v, want complete true for %q with no error", doc, id)
	}
}

// TestBackupCheckCorrupt proves the check of a backup whose manifest is gone
// answers complete false with an error naming the manifest, while the row
// still reads: corrupt and unknown stay apart.
func TestBackupCheckCorrupt(t *testing.T) {
	t.Parallel()

	held := openCheckFixture(t)
	id := startBackup(t, held.fixture.handler)
	pollBackupSucceeded(t, held.fixture.handler, id)

	key, err := blob.ParseKey("backups/" + id + "/manifest.json")
	if err != nil {
		t.Fatalf("parse manifest key: %v", err)
	}

	if err := held.stores.target.Delete(t.Context(), key); err != nil {
		t.Fatalf("delete manifest: %v", err)
	}

	status, doc := readBackupCheck(t, held.fixture, id)
	if status != http.StatusOK {
		t.Fatalf("GET backup %s check answers %d, want %d", id, status, http.StatusOK)
	}

	if doc.Complete || doc.ID != id {
		t.Fatalf("backup check = %+v, want complete false for %q", doc, id)
	}

	if !strings.Contains(doc.Error, "manifest") {
		t.Fatalf("backup check error = %q, want it to name the manifest", doc.Error)
	}
}

// TestBackupCheckUnknown proves the check of a well-formed ID with no backup
// row answers 404 like GET /api/dustpan/v1/backups/{id}: callers can tell
// unknown from corrupt.
func TestBackupCheckUnknown(t *testing.T) {
	t.Parallel()

	fix := openCheckFixture(t).fixture

	const id = "bkp-missing000000000001"

	status, body := serveBackup(t, fix.handler, http.MethodGet, "/api/dustpan/v1/backups/"+id+"/check")
	if status != http.StatusNotFound {
		t.Fatalf("GET backup %s check answers %d, want %d (%s)", id, status, http.StatusNotFound, body)
	}

	doc := decodeError(t, body)
	if doc.Code != http.StatusNotFound || doc.Message != "backup not found" {
		t.Fatalf("backup check error = %+v, want code 404 with backup not found", doc)
	}
}

// TestBackupCheckInvalidID proves a malformed ID answers 400 with a fixed
// message, never driver text: the ID never reaches the target.
func TestBackupCheckInvalidID(t *testing.T) {
	t.Parallel()

	fix := openCheckFixture(t).fixture

	// Dots never reach the handler: the mux cleans them first. Every ID
	// below reaches the handler as one segment and must answer 400.
	ids := []string{
		"no-prefix",
		"bkp-bad!id",
		"bkp-" + strings.Repeat("a", 125),
		"-bkp-leading-dash",
	}

	for _, id := range ids {
		status, body := serveBackup(t, fix.handler, http.MethodGet, "/api/dustpan/v1/backups/"+id+"/check")
		if status != http.StatusBadRequest {
			t.Fatalf("GET backup %q check answers %d, want %d (%s)", id, status, http.StatusBadRequest, body)
		}

		const want = "invalid backup ID: use an ID that opendustpan backup printed"

		doc := decodeError(t, body)
		if doc.Code != http.StatusBadRequest || doc.Message != want {
			t.Fatalf("backup check error = %+v, want code 400 with %q", doc, want)
		}
	}
}

// TestBackupCheckOtherFormat proves the check of a backup whose manifest has
// another format number answers complete false, with the text that names the
// format, and not a server error.
func TestBackupCheckOtherFormat(t *testing.T) {
	t.Parallel()

	held := openCheckFixture(t)
	id := startBackup(t, held.fixture.handler)
	pollBackupSucceeded(t, held.fixture.handler, id)

	key, err := blob.ParseKey("backups/" + id + "/manifest.json")
	if err != nil {
		t.Fatalf("parse manifest key: %v", err)
	}

	if err := held.stores.target.Delete(t.Context(), key); err != nil {
		t.Fatalf("delete manifest: %v", err)
	}

	other := []byte(`{"id":"` + id + `","format":1}`)

	if err := held.stores.target.Put(t.Context(), key, bytes.NewReader(other)); err != nil {
		t.Fatalf("put manifest of another format: %v", err)
	}

	status, doc := readBackupCheck(t, held.fixture, id)
	if status != http.StatusOK {
		t.Fatalf("GET backup %s check answers %d, want %d", id, status, http.StatusOK)
	}

	if doc.Complete || doc.ID != id {
		t.Fatalf("backup check = %+v, want complete false for %q", doc, id)
	}

	if !strings.Contains(doc.Error, "manifest format 1") {
		t.Fatalf("backup check error = %q, want it to name manifest format 1", doc.Error)
	}
}
