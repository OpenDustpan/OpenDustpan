package stack_test

import (
	"bytes"
	"crypto/sha256"
	"io"
	"strconv"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret/secrettest"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/stack/stacktest"
)

// gzipBestSpeedXFL is the gzip header XFL byte that compress/gzip writes for
// BestSpeed. The default level writes 0. Reading it back proves which level
// the save used without any timing.
const gzipBestSpeedXFL = 4

// largePadDeployment builds a deployment with one stack resource and a pad
// field of n high-entropy letters, like the padded checkpoint of the backup
// e2e steps. A fixed seed keeps the run reproducible.
func largePadDeployment(n int) []byte {
	letters := make([]byte, n)

	offset := 0

	var counter uint64

	for offset < len(letters) {
		sum := sha256.Sum256([]byte("backup-pad-v1:" + strconv.FormatUint(counter, 10)))
		counter++

		for _, b := range sum {
			if offset >= len(letters) {
				break
			}

			letters[offset] = 'a' + b%26
			offset++
		}
	}

	head := `{"resources":[{"type":"pulumi:pulumi:Stack","urn":"urn:pulumi:dev::e2e::pulumi:pulumi:Stack::e2e-dev"}],"e2ePad":"`
	tail := `"}`
	out := make([]byte, 0, len(head)+len(letters)+len(tail))
	out = append(out, head...)
	out = append(out, letters...)
	out = append(out, tail...)

	return out
}

// largeCheckpointFixture holds one started update with a blob store the test
// reads back through.
type largeCheckpointFixture struct {
	svc   *stack.Updates
	blobs *memBlob
	ref   stack.Ref
	id    stack.UpdateID
	token token.Token
}

// openLargeCheckpointFixture starts one update of acme/web/dev that saves
// checkpoints as blobs.
func openLargeCheckpointFixture(t *testing.T) *largeCheckpointFixture {
	t.Helper()

	clk := newClock()

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           clk.now,
		Name:                "acme",
		PrimaryInstallation: "local",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}

	namespaces, err := namespace.NewService(
		namespace.Config{Store: &memNamespaces{ns: ns, reads: atomic.Int64{}}, Now: clk.Now, Installation: "local"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	store := newMemUpdates()
	blobs := newMemBlob(clk)

	svc, err := stack.NewUpdates(stack.UpdatesConfig{
		Sealer:             secrettest.NewMemorySealer(),
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Updates:            store,
		Tokens:             nil,
		Checkpoints:        store,
		Events:             store,
		Namespaces:         namespaces,
		Stacks:             store,
		Blobs:              blobs,
		Refusals:           stacktest.NewRefusals(),
		Now:                clk.Now,
		Logger:             nil,
		MaxCheckpointBytes: 32 << 20,
		Journal:            nil,
		Replayer:           nil,
	})
	if err != nil {
		t.Fatalf("new updates: %v", err)
	}

	ref, err := stack.ParseRef("acme", "web", "dev")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	store.nsID = ns.ID
	store.addStack(ref, "stk-test")

	u, err := svc.Create(t.Context(), ref, stack.Plan{
		Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: "",
	})
	if err != nil {
		t.Fatalf("create update: %v", err)
	}

	started, err := svc.Start(t.Context(), ref, u.ID)
	if err != nil {
		t.Fatalf("start update: %v", err)
	}

	return &largeCheckpointFixture{svc: svc, blobs: blobs, ref: ref, id: u.ID, token: started.Token}
}

// saveLargeCheckpoint saves deployment, in a versioned envelope, as sequence 1
// of the fixture update.
func saveLargeCheckpoint(t *testing.T, f *largeCheckpointFixture, deployment []byte) {
	t.Helper()

	saveErr := f.svc.SaveCheckpoint(t.Context(), f.ref, f.id, f.token, stack.CheckpointInput{
		Features:      nil,
		Deployment:    append(append([]byte(`{"deployment":`), deployment...), '}'),
		SchemaVersion: 3,
		Sequence:      1,
		Invalid:       false,
	})
	if saveErr != nil {
		t.Fatalf("save 24 MiB checkpoint: %v", saveErr)
	}
}

// storedCheckpointBlob returns the stored gzip bytes of the fixture's latest
// checkpoint, after requiring the export to hold deployment.
func storedCheckpointBlob(t *testing.T, f *largeCheckpointFixture, deployment []byte) []byte {
	t.Helper()

	got, err := f.svc.Export(t.Context(), f.ref)
	if err != nil {
		t.Fatalf("export large checkpoint: %v", err)
	}

	if !bytes.Equal(got.Deployment, deployment) {
		t.Fatalf("exported deployment has %d bytes, want %d", len(got.Deployment), len(deployment))
	}

	if got.BlobKey == "" {
		t.Fatal("exported checkpoint names no blob: the save must store the content as a blob")
	}

	raw, err := f.blobs.Get(t.Context(), got.BlobKey)
	if err != nil {
		t.Fatalf("read stored checkpoint blob: %v", err)
	}

	gave, err := io.ReadAll(raw)
	if err != nil {
		t.Fatalf("read stored checkpoint blob: %v", err)
	}

	if closeErr := raw.Close(); closeErr != nil {
		t.Fatalf("close stored checkpoint blob: %v", closeErr)
	}

	return gave
}

// checkBestSpeedXFL requires gave to be a gzip stream whose header XFL byte
// is the one compress/gzip writes for BestSpeed.
func checkBestSpeedXFL(t *testing.T, gave []byte) {
	t.Helper()

	// A gzip header is 10 bytes: magic 1f 8b, method 8, flags, mtime (4),
	// then the XFL byte at index 8.
	if len(gave) < 10 || gave[0] != 0x1f || gave[1] != 0x8b || gave[2] != 8 {
		t.Fatalf("stored blob of %d bytes is not a gzip stream", len(gave))
	}

	if gave[8] != gzipBestSpeedXFL {
		t.Fatalf(
			"stored blob XFL byte is %d, want %d: the save must gzip at BestSpeed, not the default level",
			gave[8],
			gzipBestSpeedXFL,
		)
	}
}

// TestSaveLargeCheckpointUsesFastCompression saves one 24 MiB checkpoint,
// reads it back, and requires the stored blob to carry the gzip header XFL
// byte of the fastest level. It guards the root cause of issue 318: the save
// gzips at the fastest level, so a large checkpoint stays fast even under the
// race detector, where the default level costs over 15 seconds and trips the
// e2e client timeout. The header check is deterministic: unlike the earlier
// wall-clock deadline, it cannot turn flaky on a loaded runner.
func TestSaveLargeCheckpointUsesFastCompression(t *testing.T) {
	t.Parallel()

	deployment := largePadDeployment(24 << 20)
	f := openLargeCheckpointFixture(t)

	saveLargeCheckpoint(t, f, deployment)
	checkBestSpeedXFL(t, storedCheckpointBlob(t, f, deployment))
}
