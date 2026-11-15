package backup_test

import (
	"bytes"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
)

// TestStaleAttemptDeleteKeepsSurvivor proves that a stale holder that resumes
// after the survivor finished leaves the survivor complete: its late writes
// land under its own attempt prefix, its finish is refused on the claim, and
// Check still reports the survivor complete with every file readable.
func TestStaleAttemptDeleteKeepsSurvivor(t *testing.T) {
	t.Parallel()

	const lease = 2 * time.Second

	setup := newDurableSetup()
	id := backup.ID("bkp-stale-delete")

	// Instance A holds the claim and stops inside the export of attempt 1.
	exporterA := newGateExporter(t)
	storeA := &frozenStore{Store: setup.reg.ops, frozen: atomic.Bool{}}
	svcA := setup.service(t, exporterA, storeA, lease, "instance-a")
	workerA := startWorker(t, svcA)

	if err := svcA.Start(t.Context(), id); err != nil {
		t.Fatalf("start backup: %v", err)
	}

	<-exporterA.entered
	storeA.frozen.Store(true)

	// Instance B takes the backup over after the lease ends and finishes it
	// under attempt 2.
	svcB := setup.service(t, &stubExporter{sum: testSummary(), data: tableData, err: nil}, setup.reg.ops, lease,
		"instance-b")
	workerB := startWorker(t, svcB)
	defer workerB.stop()

	row := waitRow(t, setup.reg, id, backup.StateSucceeded)

	survivor, readErr := svcB.ReadManifest(t.Context(), id)
	if readErr != nil {
		t.Fatalf("read survivor manifest: %v", readErr)
	}

	if survivor.Attempt == "" {
		t.Fatal("survivor manifest names no attempt")
	}

	survivorFile := "backups/" + id.String() + "/" + survivor.Attempt + "/" + backup.PortablePath(backup.Tables[0])
	wantBytes := setup.target.readObject(t, survivorFile)

	// A resumes after B finished. Its late writes land under attempt 1 only,
	// and its finish is refused on the claim.
	close(exporterA.gate)
	waitFor(t, "the refused finish of the stale holder", func() bool { return setup.reg.lostFinishes.Load() == 1 })
	workerA.stop()

	if checkErr := backup.Check(t.Context(), setup.target, id); checkErr != nil {
		t.Fatalf("check after the stale holder resumed: %v, want the survivor complete", checkErr)
	}

	if got := setup.target.readObject(t, survivorFile); !bytes.Equal(got, wantBytes) {
		t.Fatal("survivor table file changed after the stale holder resumed")
	}

	after, err := setup.reg.GetBackup(t.Context(), id)
	if err != nil || after != row {
		t.Fatalf("row after the stale holder resumed = %+v, %v, want unchanged %+v", after, err, row)
	}

	for _, obj := range mustListPrefix(t, setup.target, "backups/"+id.String()+"/") {
		if strings.HasSuffix(obj.Key.String(), "/manifest.json") &&
			!strings.HasPrefix(obj.Key.String(), "backups/"+id.String()+"/"+survivor.Attempt+"/") {
			t.Fatalf("stale attempt holds a manifest at %s, want none", obj.Key)
		}
	}
}
