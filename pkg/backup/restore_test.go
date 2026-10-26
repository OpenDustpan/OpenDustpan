package backup_test

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // The test computes the MD5 values the manifest records.
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"maps"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// restoreDump is the test-only SQL part: the wrapped keys and the row counts.
// The fake store gob-encodes it as backups/<id>/db.
type restoreDump struct {
	Keys []secret.WrappedKey
	Rows map[string]int64
}

// stubWrapper is a fake secret.Wrapper with one fixed wrapping. Unwrap
// ignores the associated data and opens only the fixed ciphertext.
type stubWrapper struct {
	id         string
	ciphertext []byte
	opened     []byte
	fail       bool
}

func (w stubWrapper) ID() string { return w.id }

func (w stubWrapper) Wrap(_ context.Context, _, _ []byte) ([]byte, error) {
	return bytes.Clone(w.ciphertext), nil
}

func (w stubWrapper) Unwrap(_ context.Context, ciphertext, _ []byte) ([]byte, error) {
	if w.fail || !bytes.Equal(ciphertext, w.ciphertext) {
		return nil, secret.ErrDecrypt
	}

	return bytes.Clone(w.opened), nil
}

func testWrapper() secret.Wrapper { //nolint:ireturn // Test helper returns the interface.
	return stubWrapper{
		id:         "kek-1",
		ciphertext: []byte("wrapped-1"),
		opened:     bytes.Repeat([]byte{7}, 32),
		fail:       false,
	}
}

func testScope(t *testing.T) secret.Scope {
	t.Helper()

	scope, err := secret.NewScope(namespace.Name("acme"), "stk-test01")
	if err != nil {
		t.Fatalf("new scope: %v", err)
	}

	return scope
}

func testWrappedKey(t *testing.T) secret.WrappedKey {
	t.Helper()

	return secret.WrappedKey{
		CreatedAt:  time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC),
		Scope:      testScope(t),
		WrapperID:  "kek-1",
		Ciphertext: []byte("wrapped-1"),
	}
}

func encodeRestoreDump(t *testing.T, dump restoreDump) []byte {
	t.Helper()

	var buf bytes.Buffer

	if err := gob.NewEncoder(&buf).Encode(dump); err != nil {
		t.Fatalf("encode dump: %v", err)
	}

	return buf.Bytes()
}

// restoreBlobsInput groups what writeManifest stores alongside the SQL part.
type restoreBlobsInput struct {
	db      []byte
	dump    restoreDump
	regions []string
}

// writeRestoreManifest stores the SQL part, the blobs and the manifest in the
// target and returns the manifest.
func writeRestoreManifest(
	t *testing.T, target *memStore, blobs map[string][]byte, in restoreBlobsInput,
) backup.Manifest {
	t.Helper()

	const id = backup.ID("bkp-restore")

	files := sqlPartAllFiles(in.db)

	// The descriptor of each table file names the rows of the dump, as the
	// row counts of the manifest do.
	for i := range files {
		files[i].Rows = in.dump.Rows[files[i].Table]
	}

	for _, table := range backup.Tables {
		target.storeObject(t, sqlPartAllKey(id, table), in.db)
	}

	entries := make([]backup.ManifestBlob, 0, len(blobs))

	for _, key := range slices.Sorted(maps.Keys(blobs)) {
		target.storeObject(t, "blobs/"+key, blobs[key])

		sum := md5.Sum(blobs[key]) //nolint:gosec // The test recomputes the MD5 the manifest records.

		entries = append(entries, backup.ManifestBlob{
			Key:    key,
			Size:   int64(len(blobs[key])),
			MD5:    hex.EncodeToString(sum[:]),
			Sealed: false,
		})
	}

	sqlSize, sqlSHA := sqlPartAggregate(files)
	wrappers := []string{}

	for _, k := range in.dump.Keys {
		if !slices.Contains(wrappers, k.WrapperID) {
			wrappers = append(wrappers, k.WrapperID)
		}
	}

	slices.Sort(wrappers)

	manifest := backup.Manifest{
		CreatedAt:     time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC),
		ID:            id,
		Dialect:       backup.DialectSQLite,
		HomeRegions:   in.regions,
		WrapperIDs:    wrappers,
		Blobs:         entries,
		Version:       "test-1.0",
		SQLSHA256:     sqlSHA,
		Format:        backup.FormatVersion,
		SchemaVersion: backup.BaselineSchemaVersion,
		SQLSize:       sqlSize,
		RowCounts:     maps.Clone(in.dump.Rows),
		SQLFormat:     backup.PortableFormat,
		SQLFiles:      files,

		InstallationKind: installation.KindProduction,
		Unverified:       nil,
	}

	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}

	target.storeObject(t, "backups/"+id.String()+"/manifest.json", data)

	return manifest
}
