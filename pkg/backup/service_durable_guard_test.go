package backup_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/installation"
)

// serviceOver builds one instance of the setup over target, which wraps
// the shared target with a fault.
func (d durableSetup) serviceOver(
	t *testing.T, exporter backup.PortableExporter, target blob.Store,
) *backup.Service {
	t.Helper()

	svc, err := backup.NewService(backup.Config{
		Exporter:    exporter,
		Kinds:       fixedKind(installation.KindProduction),
		Registry:    d.reg,
		Operations:  d.reg.ops,
		Blobs:       d.source,
		StateOpener: nil,
		Target:      target,
		TargetName:  "mem:target",
		Now:         time.Now,
		Version:     "test-1.0",
		MaxSQLBytes: 0,
		Owner:       "guard",
		Lease:       0,
		Logger:      nil,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	return svc
}

// wideListStore is a target whose List also returns keys of other backups, as
// a target does that matches the prefix as a plain string prefix.
type wideListStore struct {
	*memStore

	extra []string
}

func (w *wideListStore) List(ctx context.Context, prefix string) ([]blob.Object, error) {
	objects, err := w.memStore.List(ctx, prefix)
	if err != nil {
		return nil, err
	}

	for _, name := range w.extra {
		key, parseErr := blob.ParseKey(name)
		if parseErr != nil {
			return nil, parseErr
		}

		objects = append(objects, blob.Object{ModifiedAt: time.Time{}, Key: key})
	}

	return objects, nil
}

func TestClearPartialRefusesAKeyOfAnotherBackup(t *testing.T) {
	t.Parallel()

	setup := newDurableSetup()
	foreign := "backups/bkp-guard0/manifest.json"
	setup.target.storeObject(t, foreign, []byte("{}"))

	target := &wideListStore{memStore: setup.target, extra: []string{foreign}}
	svc := setup.serviceOver(t, &stubExporter{sum: testSummary(), data: tableData, err: nil}, target)

	_, err := svc.Run(t.Context(), backup.ID("bkp-guard"))
	if !errors.Is(err, backup.ErrFailed) || !strings.Contains(err.Error(), "refuse to delete") {
		t.Fatalf("run = %v, want ErrFailed with the refusal", err)
	}

	if !setup.target.has(foreign) {
		t.Fatalf("the object %q of another backup was deleted", foreign)
	}
}

func TestRunFailsAnOperationThatEndedWithoutAWorker(t *testing.T) {
	t.Parallel()

	setup := newDurableSetup()
	svc := setup.serviceOver(t, &panicExporter{calls: atomic.Int64{}}, setup.target)

	ctx, cancel := context.WithTimeout(t.Context(), durableWait)
	defer cancel()

	_, err := svc.Run(ctx, backup.ID("bkp-run-panics"))
	if !errors.Is(err, backup.ErrFailed) {
		t.Fatalf("run = %v, want ErrFailed", err)
	}
}

// dropAfterManifest deletes dropKey as soon as the manifest lands, as the
// late delete of a holder that lost its claim does after the write step.
type dropAfterManifest struct {
	*memStore

	dropKey string
}

func (d *dropAfterManifest) Put(ctx context.Context, key blob.Key, r io.Reader) error {
	if err := d.memStore.Put(ctx, key, r); err != nil {
		return err
	}

	if strings.HasSuffix(key.String(), "/manifest.json") {
		dropKey, err := blob.ParseKey(d.dropKey)
		if err != nil {
			return err
		}

		return d.Delete(ctx, dropKey)
	}

	return nil
}

func TestFinishFailsABackupThatLostAFile(t *testing.T) {
	t.Parallel()

	setup := newDurableSetup()
	id := backup.ID("bkp-lost-file")
	dropped := "backups/" + id.String() + "/" + backup.PortablePath(backup.Tables[0])
	target := &dropAfterManifest{memStore: setup.target, dropKey: dropped}
	svc := setup.serviceOver(t, &stubExporter{sum: testSummary(), data: tableData, err: nil}, target)

	_, err := svc.Run(t.Context(), id)
	if !errors.Is(err, backup.ErrFailed) || !strings.Contains(err.Error(), "lost the object") {
		t.Fatalf("run = %v, want ErrFailed with the lost object", err)
	}

	row, getErr := setup.reg.GetBackup(t.Context(), id)
	if getErr != nil || row.State != backup.StateFailed {
		t.Fatalf("row = %+v, %v, want failed", row, getErr)
	}
}
