package backup_test

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
)

// hostileKeys are manifest keys that a restore must refuse, by the layer that
// stops them.
func hostileKeys() map[string]string {
	return map[string]string{
		"audit checkpoint copy":      "audit-checkpoints/epochs/0001/6163/abc",
		"audit checkpoint directory": "audit-checkpoints/x",
		"store check object":         "store-checks/chk-1/probe",
		"restore temporary key":      "restore-tmp/rst-1/1/namespaces/ns-a/pulumi/stk-1/upd-1/1",
		"another top-level prefix":   "tofu/ws-1/sv-1/a1",
		"log sink prefix":            "logs/app/1",
		"namespaces without an ID":   "namespaces/x",
		"namespaces with a bad ID":   "namespaces/acme/pulumi/stk-1/upd-1/1",
		"namespaces directory only":  "namespaces/ns-a",
		"dot segments":               "namespaces/ns-a/../audit-checkpoints/x",
		"upper case prefix":          "Audit-Checkpoints/x",
		"empty segment":              "namespaces//audit-checkpoints/x",
		"trailing slash":             "namespaces/ns-a/",
		"leading slash":              "/audit-checkpoints/x",
		"percent encoded dash":       "audit%2dcheckpoints/x",
		"percent encoded letter":     "%61udit-checkpoints/x",
		"percent encoded slash":      "audit-checkpoints%2fx",
		"backslash":                  "audit-checkpoints\\x",
		"backslash parent directory": "namespaces\\ns-a\\..\\audit-checkpoints\\x",
		"space in the prefix":        "audit-checkpoints /x",
		"trailing dot":               "audit-checkpoints./x",
		"NUL byte":                   "audit-checkpoints\x00/x",
		"line feed":                  "audit-checkpoints\n/x",
		"carriage return":            "store-checks\r/x",
		"fullwidth hyphen":           "audit\uff0dcheckpoints/x",
		"Cyrillic a":                 "\u0430udit-checkpoints/x",
		"overlong key":               "namespaces/ns-a/" + strings.Repeat("a", 2000),
		"empty key":                  "",
	}
}

// TestRestoreKeyLayers sends hostile keys straight to each layer, as if the
// other layer failed. The deny layer refuses the prefixes that the server
// owns. The allowlist refuses every key outside namespaces/<namespace ID>/.
func TestRestoreKeyLayers(t *testing.T) {
	t.Parallel()

	for name, key := range hostileKeys() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if err := backup.CheckRestoreKeyForTest(key); !errors.Is(err, backup.ErrProtectedKey) ||
				!errors.Is(err, backup.ErrCorrupt) {
				t.Fatalf("check %q: err = %v, want ErrProtectedKey wrapping ErrCorrupt", key, err)
			}

			if backup.WritableKeyForTest(key) {
				t.Errorf("allowlist layer admits %q, want a refusal", key)
			}
		})
	}

	denied := []string{"audit-checkpoints/x", "store-checks/c/x", "restore-tmp/r/1/k"}
	for _, key := range denied {
		parsed, err := blob.ParseKey(key)
		if err != nil {
			t.Fatalf("parse %q: %v", key, err)
		}

		if !backup.ProtectedKeyForTest(parsed) {
			t.Errorf("deny layer admits %q, want a refusal", key)
		}
	}

	if !audit.IsCheckpointKey(blob.Key("audit-checkpoints/x")) {
		t.Fatal("audit.IsCheckpointKey does not know the checkpoint prefix")
	}
}

// TestRestoreKeyAdmitsTheKeysOfABackup proves that the keys which a backup
// names pass both layers.
func TestRestoreKeyAdmitsTheKeysOfABackup(t *testing.T) {
	t.Parallel()

	for _, key := range []string{
		"namespaces/ns-abc/pulumi/stk-1/upd-1/1",
		"namespaces/ns-abc/pulumi/stk-1/upd-1/journal/1",
		"namespaces/ns-abc/item-keys/ik-1/v1",
		"namespaces/ns-abc/registry/sha256/" + strings.Repeat("a", 64),
		"namespaces/ns-abc/jobs/job-1/plan/p",
	} {
		if err := backup.CheckRestoreKeyForTest(key); err != nil {
			t.Errorf("check %q: %v, want it admitted", key, err)
		}
	}
}

// addManifestBlob adds an entry for key to the manifest of the backup of the
// setup, as a person who can write to the backup target does. The manifest is
// not authenticated.
func addManifestBlob(t *testing.T, setup *restoreServiceSetup, key string) {
	t.Helper()

	path := "backups/" + setup.id.String() + "/manifest.json"

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(setup.target.readObject(t, path), &doc); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}

	blobs := make([]backup.ManifestBlob, 0, 1)
	if raw, ok := doc["blobs"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &blobs); err != nil {
			t.Fatalf("decode manifest blobs: %v", err)
		}
	}

	blobs = append(blobs, backup.ManifestBlob{Key: key, Size: 1, MD5: strings.Repeat("0", 32), Sealed: false})

	raw, err := json.Marshal(blobs)
	if err != nil {
		t.Fatalf("encode manifest blobs: %v", err)
	}

	doc["blobs"] = raw

	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}

	if err := setup.target.Delete(t.Context(), mustKey(t, path)); err != nil {
		t.Fatalf("delete manifest: %v", err)
	}

	setup.target.storeObject(t, path, out)
}

// TestRestoreRefusesProtectedKeysInTheManifest sends a crafted manifest for
// each hostile key through the whole restore. Each restore ends failed with
// the stable text, writes no live blob, stages nothing, and leaves an audit
// entry that names the refusal.
func TestRestoreRefusesProtectedKeysInTheManifest(t *testing.T) {
	t.Parallel()

	for name, key := range hostileKeys() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			setup := newRestoreServiceSetup(t)
			addManifestBlob(t, setup, key)

			id, err := startRestoreOf(t, setup, plainRestore())
			if err != nil {
				t.Fatalf("start: %v", err)
			}

			assertRefusedWithoutWrites(t, setup, id, key)

			// A key in the manifest is refused before the first byte is staged.
			if calls := setup.live.stageCallCount(); calls != 0 {
				t.Errorf("stage calls = %d, want none before the refusal", calls)
			}
		})
	}
}

// assertRefusedWithoutWrites waits for the restore id and checks that it
// ended failed with the stable text, wrote no live blob and applied nothing.
func assertRefusedWithoutWrites(t *testing.T, setup *restoreServiceSetup, id backup.RestoreID, key string) {
	t.Helper()

	row := waitRestoreEnd(t, setup.regs, id)
	if row.State != backup.RestoreFailed || !strings.Contains(row.Error, backup.ErrProtectedKey.Error()) {
		t.Fatalf("restore = %s %q, want failed with the protected key text", row.State, row.Error)
	}

	if key != "" && strings.Contains(row.Error, key) {
		t.Errorf("the stored error text %q holds the hostile key", row.Error)
	}

	if n := setup.blobs.count(); n != 0 {
		t.Errorf("live store holds %d blobs after the refusal, want none", n)
	}

	if c := setup.live.counts(); c.applied != 0 {
		t.Errorf("applies = %d, want none", c.applied)
	}

	assertRefusalAudited(t, setup, id, key)
}

// assertRefusalAudited checks that the audit events and the audit entries of
// the restore name the refusal of key.
func assertRefusalAudited(t *testing.T, setup *restoreServiceSetup, id backup.RestoreID, key string) {
	t.Helper()

	events, err := setup.live.RestoreEvents(t.Context(), id)
	if err != nil {
		t.Fatalf("restore events: %v", err)
	}

	found := false

	for _, event := range events {
		if strings.Contains(event, "refused blob key") && strings.Contains(event, id.String()) {
			found = true
		}
	}

	if !found {
		t.Errorf("events = %v, want one that names the refused key of restore %s", events, id)
	}

	entries := 0

	appends := setup.live.devSnapshot().appends

	for i := range appends {
		if strings.Contains(appends[i].Object, "refused blob key") {
			entries++
		}
	}

	if entries != 1 {
		t.Errorf("audit entries for the refusal of %q = %d, want 1", key, entries)
	}
}

// TestRestoreRefusesAManifestThatChangesAfterTheStage swaps the manifest in the
// backup target at three points: after the stage, and between the check and the
// copy, with a hostile key and with a benign extra key. The blob list is
// pinned with the rest of the manifest, so each swap ends the restore failed
// before the first live key is written.
func TestRestoreRefusesAManifestThatChangesAfterTheStage(t *testing.T) {
	t.Parallel()

	const goodKey = "namespaces/ns-abc/pulumi/stk-1/upd-1/1"

	tests := []struct {
		name string
		swap func(setup *restoreServiceSetup, change func())
		keys []string
	}{
		{
			name: "after the stage, hostile key",
			swap: func(setup *restoreServiceSetup, change func()) { setup.live.afterStage = change },
			keys: []string{"audit-checkpoints/epochs/0001/6163/abc"},
		},
		{
			name: "between the check and the copy, good key then hostile key",
			swap: func(setup *restoreServiceSetup, change func()) { setup.live.onApplying = change },
			keys: []string{goodKey, "audit-checkpoints/epochs/0001/6163/abc"},
		},
		{
			name: "between the check and the copy, benign extra key",
			swap: func(setup *restoreServiceSetup, change func()) { setup.live.onApplying = change },
			keys: []string{goodKey},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runSwappedRestore(t, tc.swap, tc.keys)
		})
	}
}

// runSwappedRestore starts a restore, lets swap choose the hook that adds keys
// to the manifest in the target once, and checks that the restore ends failed
// with nothing written.
func runSwappedRestore(t *testing.T, swap func(*restoreServiceSetup, func()), keys []string) {
	t.Helper()

	setup := newRestoreServiceSetup(t)

	var once sync.Once

	swap(setup, func() {
		once.Do(func() {
			for _, key := range keys {
				addManifestBlob(t, setup, key)
			}
		})
	})

	id, err := startRestoreOf(t, setup, plainRestore())
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	row := waitRestoreEnd(t, setup.regs, id)
	if row.State != backup.RestoreFailed || !strings.Contains(row.Error, backup.ErrCorrupt.Error()) {
		t.Fatalf("restore = %s %q, want failed with the corrupt text", row.State, row.Error)
	}

	if n := setup.blobs.count(); n != 0 {
		t.Errorf("live store holds %d blobs after the swap, want none", n)
	}

	if c := setup.live.counts(); c.applied != 0 {
		t.Errorf("applies = %d, want none", c.applied)
	}
}

// TestRestoreRefusesAStagedRowThatNamesAProtectedKey makes the staged data
// name a blob under a protected prefix, with a manifest that does not. The
// restore ends failed with an audit entry, and applies nothing.
func TestRestoreRefusesAStagedRowThatNamesAProtectedKey(t *testing.T) {
	t.Parallel()

	setup := newRestoreServiceSetup(t)
	setup.live.stageBlobs = []backup.BlobRef{
		{Key: "audit-checkpoints/epochs/0001/6163/abc", Size: 1, MD5: [16]byte{}, Sealed: false},
	}

	id, err := startRestoreOf(t, setup, plainRestore())
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	assertRefusedWithoutWrites(t, setup, id, "audit-checkpoints/epochs/0001/6163/abc")
}

// TestExportRefusesARowKeyThatARestoreWouldRefuse makes the data of a backup
// name a blob under a protected prefix, as a poisoned row does. The backup
// fails with a clear error that names the key, copies no blob, and writes no
// manifest, so no backup exists that a restore would refuse.
func TestExportRefusesARowKeyThatARestoreWouldRefuse(t *testing.T) {
	t.Parallel()

	for name, key := range map[string]string{
		"audit checkpoint": "audit-checkpoints/epochs/0001/6163/abc",
		"store check":      "store-checks/chk-1/probe",
		"restore temp":     "restore-tmp/rst-1/1/k",
		"other prefix":     "logs/app/1",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			data := []byte("poisoned")
			exporter := &stubExporter{sum: testSummary(blobRef(key, data)), data: []byte("sql-bytes"), err: nil}
			fix := newFixture(t, exporter, 0)
			seedSource(t, fix.source, map[string][]byte{key: data})

			_, err := fix.service.Run(t.Context(), "bkp-poison")
			if !errors.Is(err, backup.ErrFailed) {
				t.Fatalf("run: err = %v, want ErrFailed", err)
			}

			row, getErr := fix.service.Get(t.Context(), "bkp-poison")
			if getErr != nil || row.State != backup.StateFailed || !strings.Contains(row.Error, key) ||
				!strings.Contains(row.Error, "does not write") {
				t.Fatalf("row = %+v, %v, want failed with a text that names the key", row, getErr)
			}

			if fix.target.has("blobs/"+key) || fix.target.has("backups/bkp-poison/manifest.json") {
				t.Fatal("the target holds a blob or a manifest of the refused backup")
			}
		})
	}
}
