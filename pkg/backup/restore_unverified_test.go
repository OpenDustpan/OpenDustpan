package backup_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

// setManifestUnverified rewrites the manifest of the backup of the setup with
// keys as the blobs that the data names without a check value. The manifest is
// not authenticated, so a test edits it like a person who can write to the
// backup target.
func setManifestUnverified(t *testing.T, setup *restoreServiceSetup, keys []string) {
	t.Helper()

	key := "backups/" + setup.id.String() + "/manifest.json"

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(setup.target.readObject(t, key), &doc); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}

	raw, err := json.Marshal(keys)
	if err != nil {
		t.Fatalf("encode the unverified keys: %v", err)
	}

	doc["unverified"] = raw

	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}

	if err := setup.target.Delete(t.Context(), mustKey(t, key)); err != nil {
		t.Fatalf("delete manifest: %v", err)
	}

	setup.target.storeObject(t, key, data)
}

// TestRestoreAppliesAManifestOverTheUnverifiedLimit proves that the bound on
// the unverified keys stops a new backup and not a restore: a restore of a
// manifest above the bound succeeds, and it records the count and the bound in
// its audit entries. A manifest at the bound records no such entry.
func TestRestoreAppliesAManifestOverTheUnverifiedLimit(t *testing.T) {
	t.Parallel()

	const limit = 2

	tests := []struct {
		name     string
		keys     []string
		wantNote []string
	}{
		{name: "at the bound", keys: unverifiedKeys(limit), wantNote: nil},
		{
			name: "one above the bound",
			keys: unverifiedKeys(limit + 1),
			wantNote: []string{
				fmt.Sprintf("%d blobs without a size and an MD5", limit+1),
				fmt.Sprintf("more than the limit of %d", limit),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkRestoreUnverified(t, tc.keys, limit, tc.wantNote)
		})
	}
}

// checkRestoreUnverified restores a manifest whose unverified list holds keys
// and proves that the restore succeeds. It also proves that the restore
// records one audit entry that names the restore, the count and the bound,
// with the fragments of wantNote, or that it records no such entry when
// wantNote is nil.
func checkRestoreUnverified(t *testing.T, keys []string, limit int, wantNote []string) {
	t.Helper()

	setup := newRestoreServiceSetup(t)
	backup.SetMaxUnverifiedKeysForTest(setup.backups, limit)
	setManifestUnverified(t, setup, keys)

	id, err := startRestoreOf(t, setup, plainRestore())
	if err != nil {
		t.Fatalf("start restore: %v", err)
	}

	row := waitRestoreEnd(t, setup.regs, id)

	if row.State != backup.RestoreSucceeded {
		t.Fatalf("restore state = %q, want succeeded: %s", row.State, row.Error)
	}

	appends := setup.live.devSnapshot().appends

	var noted []string

	for i := range appends {
		if strings.Contains(appends[i].Object, "blobs without a size and an MD5") {
			noted = append(noted, appends[i].Object)
		}
	}

	if wantNote == nil {
		if len(noted) != 0 {
			t.Fatalf("the audit entries name the unverified keys of a manifest at the bound: %v", noted)
		}

		return
	}

	if len(noted) != 1 {
		t.Fatalf("audit entries that name the unverified keys = %v, want one", noted)
	}

	for _, want := range append(slices.Clone(wantNote), setup.id.String()) {
		if !strings.Contains(noted[0], want) {
			t.Fatalf("audit entry = %q, want %q", noted[0], want)
		}
	}
}
