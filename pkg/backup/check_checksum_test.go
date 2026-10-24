package backup_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

// sumBlobKey is the key of the one blob of the backup bkp-sum.
const sumBlobKey = "namespaces/ns-aaaaaaaaaaaaaaaaaaaa/tofu/ws1/sv1/a1"

// checkedBackup runs the backup bkp-sum with the blob sumBlobKey, checks it,
// and returns its fixture.
func checkedBackup(t *testing.T) *fixture {
	t.Helper()

	data := []byte(`{"state":1}`)
	exporter := &stubExporter{sum: testSummary(blobRef(sumBlobKey, data)), data: []byte("sql-bytes"), err: nil}
	fix := newFixture(t, exporter, 0)
	seedSource(t, fix.source, map[string][]byte{sumBlobKey: data})

	if done := mustRun(t, fix.service, "bkp-sum"); done.State != backup.StateSucceeded {
		t.Fatalf("backup = %+v, want succeeded", done)
	}

	if err := backup.Check(t.Context(), fix.target, "bkp-sum"); err != nil {
		t.Fatalf("check of the whole backup: %v", err)
	}

	return fix
}

// flipByte replaces the object key of target with a copy of the same size
// that has one other byte.
func flipByte(t *testing.T, target *memStore, key string) {
	t.Helper()

	data := bytes.Clone(target.readObject(t, key))
	data[len(data)/2] ^= 0xff

	if err := target.Delete(t.Context(), mustKey(t, key)); err != nil {
		t.Fatalf("delete %q: %v", key, err)
	}

	target.storeObject(t, key, data)
}

// TestCheckFindsAChangedByte proves that Check compares the checksum of each
// object, not only its size: a table file or a blob with one other byte and
// the same size fails with ErrCorrupt, and the error names the object.
func TestCheckFindsAChangedByte(t *testing.T) {
	t.Parallel()

	for which, key := range map[string]string{"table file": sqlPartKey("bkp-sum"), "blob": "blobs/" + sumBlobKey} {
		t.Run(which, func(t *testing.T) {
			t.Parallel()

			fix := checkedBackup(t)

			flipByte(t, fix.target, key)

			err := backup.Check(t.Context(), fix.target, "bkp-sum")
			if err == nil || !errors.Is(err, backup.ErrCorrupt) || !strings.Contains(err.Error(), key) ||
				!strings.Contains(err.Error(), "checksum") {
				t.Fatalf("check = %v, want ErrCorrupt that names %q and the checksum", err, key)
			}
		})
	}
}

// TestCheckRefusesAManifestWithNoChecksum proves that Check fails closed when
// the manifest names no valid checksum for an object: an empty MD5 of a blob
// gives ErrCorrupt, even when the object is whole.
func TestCheckRefusesAManifestWithNoChecksum(t *testing.T) {
	t.Parallel()

	fix := checkedBackup(t)
	key := "backups/bkp-sum/manifest.json"

	var manifest map[string]any
	if err := json.Unmarshal(fix.target.readObject(t, key), &manifest); err != nil {
		t.Fatalf("decode the manifest: %v", err)
	}

	blobs, ok := manifest["blobs"].([]any)
	if !ok || len(blobs) != 1 {
		t.Fatalf("manifest blobs = %v, want one", manifest["blobs"])
	}

	first, ok := blobs[0].(map[string]any)
	if !ok {
		t.Fatalf("manifest blob = %v, want an object", blobs[0])
	}

	first["md5"] = ""

	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("encode the manifest: %v", err)
	}

	if delErr := fix.target.Delete(t.Context(), mustKey(t, key)); delErr != nil {
		t.Fatalf("delete the manifest: %v", delErr)
	}

	fix.target.storeObject(t, key, data)

	err = backup.Check(t.Context(), fix.target, "bkp-sum")
	if err == nil || !errors.Is(err, backup.ErrCorrupt) || !strings.Contains(err.Error(), "no valid checksum") {
		t.Fatalf("check = %v, want ErrCorrupt for no valid checksum", err)
	}
}
