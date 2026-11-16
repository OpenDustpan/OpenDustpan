package backup_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/installation"
)

// scopeManifest returns a manifest of backup id for the attempt directory of
// attempt. Every table file holds data.
func scopeManifest(id backup.ID, attempt string, data []byte) backup.Manifest {
	files := sqlPartAllFiles(data)
	counts := make(map[string]int64, len(files))

	for _, file := range files {
		counts[file.Table] = 0
	}

	size, sha := sqlPartAggregate(files)

	return backup.Manifest{
		CreatedAt:            time.Unix(1700000000, 0).UTC(),
		ID:                   id,
		Attempt:              attempt,
		Dialect:              backup.DialectSQLite,
		PrimaryInstallations: nil,
		RegistryFiles:        nil,
		WrapperIDs:           nil,
		Blobs:                nil,
		Version:              "test-1.0",
		SQLSHA256:            sha,
		Format:               backup.FormatVersion,
		SchemaVersion:        backup.BaselineSchemaVersion,
		SQLSize:              size,
		RowCounts:            counts,
		SQLFormat:            backup.PortableFormat,
		SQLFiles:             files,
		InstallationKind:     installation.KindProduction,
		Unverified:           nil,
		Damaged:              nil,
	}
}

// scopeFiles is the content of the table files that a test stores: the first
// table file holds first, every other file holds data.
type scopeFiles struct {
	data  []byte
	first []byte
}

// putScopeAttempt writes manifest and its table files to the attempt
// directory of scopeID, attempt 5. The layout belongs to the scope, only
// the manifest varies. A test that puts the manifest of another backup or
// another attempt there checks that the reader refuses it.
func putScopeAttempt(t *testing.T, store *memStore, manifest backup.Manifest, files scopeFiles) {
	t.Helper()

	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("encode the manifest: %v", err)
	}

	const attempt = "5"

	store.storeObject(t, scopeKey(attempt, "manifest.json"), raw)

	for i, file := range manifest.SQLFiles {
		content := files.data
		if i == 0 {
			content = files.first
		}

		store.storeObject(t, scopeKey(attempt, file.Path), content)
	}
}

// TestFinishRefusesTruncatedFile proves that the finish check refuses a table
// file that is shorter than the manifest says.
func TestFinishRefusesTruncatedFile(t *testing.T) {
	t.Parallel()

	full := []byte("a table file of some length")

	for _, tc := range []struct {
		name    string
		stored  []byte
		wantErr bool
	}{
		{name: "complete", stored: full, wantErr: false},
		{name: "truncated", stored: full[:5], wantErr: true},
		{name: "empty", stored: nil, wantErr: true},
		{name: "same size, other bytes", stored: bytes.ToUpper(full), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := newMemStore()
			putScopeAttempt(t, store, scopeManifest(scopeID, "5", full), scopeFiles{data: full, first: tc.stored})

			err := backup.CheckAttemptForTest(t.Context(), store, scopeID, "5")
			if tc.wantErr && !errors.Is(err, backup.ErrCorrupt) {
				t.Fatalf("finish check = %v, want ErrCorrupt", err)
			}

			if !tc.wantErr && err != nil {
				t.Fatalf("finish check = %v, want nil", err)
			}
		})
	}
}

// TestCheckFailsClosedOnTheLivePointer sends hostile pointers and manifests to
// Check, which reads the pointer first.
func TestCheckFailsClosedOnTheLivePointer(t *testing.T) {
	t.Parallel()

	full := []byte("a table file of some length")
	liveName := livePointerKeyOf(scopeID)
	wrongAttempt := scopeManifest(scopeID, "6", full)
	wrongBackup := scopeManifest("bkp-other", "5", full)
	oversize := `{"attempt":"5","pad":"` + strings.Repeat("x", backup.MaxLivePointerBytesForTest) + `"}`

	good := scopeManifest(scopeID, "5", full)

	// An empty pointer text means that the test writes no pointer object.
	for _, tc := range []struct {
		name     string
		manifest backup.Manifest
		pointer  string
	}{
		{name: "no pointer", manifest: good, pointer: ""},
		{name: "pointer is not JSON", manifest: good, pointer: "not json"},
		{name: "pointer names dot-dot", manifest: good, pointer: `{"attempt":".."}`},
		{name: "pointer names a path", manifest: good, pointer: `{"attempt":"5/../6"}`},
		{name: "pointer is empty", manifest: good, pointer: `{}`},
		{name: "pointer is too large", manifest: good, pointer: oversize},
		{name: "pointer names a directory with no manifest", manifest: good, pointer: `{"attempt":"6"}`},
		{name: "manifest names another attempt", manifest: wrongAttempt, pointer: `{"attempt":"5"}`},
		{name: "manifest names another backup", manifest: wrongBackup, pointer: `{"attempt":"5"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := newMemStore()
			putScopeAttempt(t, store, tc.manifest, scopeFiles{data: full, first: full})

			if tc.pointer != "" {
				store.storeObject(t, liveName, []byte(tc.pointer))
			}

			if err := backup.Check(t.Context(), store, scopeID); !errors.Is(err, backup.ErrCorrupt) {
				t.Fatalf("Check = %v, want ErrCorrupt", err)
			}
		})
	}
}

// TestCheckAcceptsAFinishedBackup is the control of the table above: the
// same layout with a good pointer passes.
func TestCheckAcceptsAFinishedBackup(t *testing.T) {
	t.Parallel()

	full := []byte("a table file of some length")
	store := newMemStore()
	putScopeAttempt(t, store, scopeManifest(scopeID, "5", full), scopeFiles{data: full, first: full})

	if err := backup.SeedLivePointerForTest(t.Context(), store, scopeID, "5"); err != nil {
		t.Fatalf("write the live pointer: %v", err)
	}

	if err := backup.Check(t.Context(), store, scopeID); err != nil {
		t.Fatalf("Check of a finished backup = %v, want nil", err)
	}
}

// TestReadLiveBoundsTheRead proves that the pointer read stops at its limit.
func TestReadLiveBoundsTheRead(t *testing.T) {
	t.Parallel()

	store := newMemStore()

	if err := backup.SeedLivePointerForTest(t.Context(), store, scopeID, "5"); err != nil {
		t.Fatalf("write the live pointer: %v", err)
	}

	size := int64(len(store.readObject(t, livePointerKeyOf(scopeID))))

	if attempt, err := backup.ReadLiveForTest(t.Context(), store, scopeID, size); err != nil || attempt != "5" {
		t.Errorf("read at the size = %q, %v, want 5", attempt, err)
	}

	if _, err := backup.ReadLiveForTest(t.Context(), store, scopeID, size-1); !errors.Is(err, backup.ErrCorrupt) {
		t.Errorf("read under the size = %v, want ErrCorrupt", err)
	}
}
