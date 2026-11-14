package backup_test

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/installation"
)

// unverifiedKeys returns n keys of blobs that rows name without a size and an
// MD5, as a row of an earlier version of the server holds them.
func unverifiedKeys(n int) []string {
	keys := make([]string, 0, n)

	for i := range n {
		keys = append(keys, fmt.Sprintf("namespaces/ns-aaaaaaaaaaaaaaaaaaaa/job/job-1/plan/p%04d", i))
	}

	return keys
}

// unverifiedFixture is the service of one case of the bound on the unverified
// keys, with the registry, the target and the log sink that the case reads.
type unverifiedFixture struct {
	service  *backup.Service
	registry *fakeRegistry
	target   *memStore
	logs     *syncBuffer
}

// newUnverifiedFixture returns a backup service over an in-memory target whose
// bound on the unverified keys is limit, with a log sink.
func newUnverifiedFixture(t *testing.T, exporter backup.PortableExporter, limit int) *unverifiedFixture {
	t.Helper()

	now := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)
	registry := newFakeRegistry()
	target := newMemStore()
	logs := &syncBuffer{mu: sync.Mutex{}, buf: bytes.Buffer{}}

	service, err := backup.NewService(backup.Config{
		Exporter:    exporter,
		Kinds:       fixedKind(installation.KindProduction),
		Registry:    registry,
		Operations:  registry.ops,
		Blobs:       newMemStore(),
		StateOpener: nil,
		Target:      target,
		TargetName:  "mem:target",
		Now:         func() time.Time { return now },
		Version:     "test-1.0",
		MaxSQLBytes: 0,
		Owner:       "",
		Lease:       0,
		Logger:      slog.New(slog.NewTextHandler(logs, nil)),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	backup.SetMaxUnverifiedKeysForTest(service, limit)

	return &unverifiedFixture{service: service, registry: registry, target: target, logs: logs}
}

// TestBackupRefusesTooManyUnverifiedKeys proves the bound on the unverified
// keys of a backup: a backup at the bound succeeds with the keys in its
// manifest, and a backup above the bound is refused before the manifest, with
// the count and the bound in its row and in the log.
func TestBackupRefusesTooManyUnverifiedKeys(t *testing.T) {
	t.Parallel()

	const limit = 3

	tests := []struct {
		name    string
		keys    []string
		refused bool
	}{
		{name: "no unverified key", keys: nil, refused: false},
		{name: "at the bound", keys: unverifiedKeys(limit), refused: false},
		{name: "one above the bound", keys: unverifiedKeys(limit + 1), refused: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.refused {
				checkRefusedBackup(t, tc.keys, limit)

				return
			}

			checkAcceptedBackup(t, tc.keys, limit)
		})
	}
}

// checkAcceptedBackup proves that a backup of keys at the bound succeeds and
// lists the keys, with no refusal in the log.
func checkAcceptedBackup(t *testing.T, keys []string, limit int) {
	t.Helper()

	sum := testSummary()
	sum.Unverified = keys

	fix := newUnverifiedFixture(t, &stubExporter{sum: sum, data: []byte("{}"), err: nil}, limit)

	if got := fix.service.UnverifiedKeyLimit(); got != limit {
		t.Fatalf("UnverifiedKeyLimit = %d, want %d", got, limit)
	}

	if _, err := fix.service.Run(t.Context(), "bkp-unverified"); err != nil {
		t.Fatalf("run backup: %v", err)
	}

	manifest, err := fix.service.ReadManifest(t.Context(), "bkp-unverified")
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	if !slices.Equal(manifest.Unverified, keys) {
		t.Fatalf("manifest lists %v as unverified, want %v", manifest.Unverified, keys)
	}

	if strings.Contains(fix.logs.String(), "backup refused") {
		t.Fatalf("the log names a refusal of a backup at the bound:\n%s", fix.logs.String())
	}
}

// checkRefusedBackup proves that a backup of keys above the bound is refused:
// its row is failed, its text names the count and the bound, the target holds
// no manifest that a restore or a check accepts, and the log names the count
// and the bound.
func checkRefusedBackup(t *testing.T, keys []string, limit int) {
	t.Helper()

	const id = backup.ID("bkp-unverified")

	sum := testSummary()
	sum.Unverified = keys

	fix := newUnverifiedFixture(t, &stubExporter{sum: sum, data: []byte("{}"), err: nil}, limit)

	_, runErr := fix.service.Run(t.Context(), id)

	if !errors.Is(runErr, backup.ErrFailed) {
		t.Fatalf("run = %v, want ErrFailed", runErr)
	}

	row := mustGetRecord(t, fix.registry, id)

	if row.State != backup.StateFailed {
		t.Fatalf("backup state = %q, want failed", row.State)
	}

	for _, want := range []string{
		fmt.Sprintf("%d blobs without a size and an MD5", len(keys)),
		fmt.Sprintf("more than the limit of %d", limit),
		"wrote no manifest",
	} {
		if !strings.Contains(row.Error, want) {
			t.Fatalf("backup error = %q, want %q", row.Error, want)
		}
	}

	// The target holds no manifest that a restore or a check would accept as
	// complete.
	if fix.target.has("backups/" + id.String() + "/manifest.json") {
		t.Fatal("a refused backup wrote a manifest")
	}

	if _, err := fix.service.ReadManifest(t.Context(), id); !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("read manifest of a refused backup = %v, want ErrCorrupt", err)
	}

	if err := backup.Check(t.Context(), fix.target, id); !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("check of a refused backup = %v, want ErrCorrupt", err)
	}

	line := fix.logs.String()

	for _, want := range []string{
		"backup refused", fmt.Sprintf("blobs=%d", len(keys)), fmt.Sprintf("limit=%d", limit),
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("the log lacks %q:\n%s", want, line)
		}
	}
}
