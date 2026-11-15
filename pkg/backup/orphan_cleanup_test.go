package backup_test

import (
	"bytes"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
)

// TestOrphanPrefixCleanup proves that the live holder deletes the orphaned
// attempt prefix: after a takeover finishes the backup, no object stays under
// the stale prefix, and the finished backup stays complete. A stale writer
// that recreates its prefix afterwards changes nothing about the live
// prefix.
func TestOrphanPrefixCleanup(t *testing.T) {
	t.Parallel()

	const lease = 2 * time.Second

	setup := newDurableSetup()
	id := backup.ID("bkp-orphan")

	// Instance A holds the claim and stops inside the export of attempt 1
	// after writing one table file.
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

	waitRow(t, setup.reg, id, backup.StateSucceeded)

	// The live holder deleted the orphaned attempt prefix.
	for _, obj := range mustListPrefix(t, setup.target, "backups/"+id.String()+"/") {
		if strings.HasPrefix(obj.Key.String(), "backups/"+id.String()+"/1/") {
			t.Fatalf("orphaned attempt prefix still holds %s after the live holder finished", obj.Key)
		}
	}

	survivor, err := svcB.ReadManifest(t.Context(), id)
	if err != nil {
		t.Fatalf("read survivor manifest: %v", err)
	}

	if survivor.Attempt != "2" {
		t.Fatalf("survivor manifest names attempt %q, want 2", survivor.Attempt)
	}

	survivorFile := "backups/" + id.String() + "/2/" + backup.PortablePath(backup.Tables[0])
	wantBytes := setup.target.readObject(t, survivorFile)

	// A resumes after B finished. Its finish is refused on the claim.
	close(exporterA.gate)
	waitFor(t, "the refused finish of the stale holder", func() bool { return setup.reg.lostFinishes.Load() == 1 })
	workerA.stop()

	// A stale write lands late under the stale prefix only, as an in-flight
	// delete or put does after the survivor finished.
	setup.target.storeObject(t, "backups/"+id.String()+"/1/"+backup.PortablePath(backup.Tables[0]), tableData)

	if err := backup.Check(t.Context(), setup.target, id); err != nil {
		t.Fatalf("check with a recreated stale prefix: %v, want the survivor complete", err)
	}

	if got := setup.target.readObject(t, survivorFile); !bytes.Equal(got, wantBytes) {
		t.Fatal("survivor table file changed after the stale prefix was recreated")
	}
}
