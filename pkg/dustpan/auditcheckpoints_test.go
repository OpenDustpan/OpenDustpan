package dustpan_test

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// TestAuditCheckpointsAreWrittenAndChecked proves that with the chain and
// checkpoints on, the check at start signs the last hash of each scope into
// the blob store. A start with another key finds that the checkpoint does
// not match, and a later start still finds it until a new epoch starts.
func TestAuditCheckpointsAreWrittenAndChecked(t *testing.T) {
	t.Parallel()

	st := openChainStore(t)
	blobs := openCheckpointBlobs(t)
	acme := createChainNamespace(t, st)
	appendChainEntry(t, st, acme, "req-1")

	key, err := audit.FirstLayoutCheckpointKey(0, acme.Name, 1)
	if err != nil {
		t.Fatalf("checkpoint key: %v", err)
	}

	first := runCheckpointServer(t, st, blobs,
		dustpan.WithAuditChain(audit.ChainOn), dustpan.WithAuditCheckpoints(newTestSigner(t, 1)))
	waitForCopy(t, blobs, key)
	first.stop()

	other := runCheckpointServer(t, st, blobs,
		dustpan.WithAuditChain(audit.ChainOn), dustpan.WithAuditCheckpoints(newTestSigner(t, 2)))
	// The scope that the first key signed fails: acme.
	waitForInvalidScopes(t, other)
	other.stop()

	// The mismatch is stored with the epoch, so a start with the first key as
	// a verify key still finds it: only a new epoch ends it.
	rotated := newTestSigner(t, 2)
	if err := rotated.AddVerifyKey(newTestSigner(t, 1).PublicKey()); err != nil {
		t.Fatalf("add verify key: %v", err)
	}

	stillMarked := runCheckpointServer(t, st, blobs,
		dustpan.WithAuditChain(audit.ChainOn), dustpan.WithAuditCheckpoints(rotated))
	waitForInvalidScopes(t, stillMarked)
	stillMarked.stop()

	epochs := audit.NewCheckpoints(
		audit.CheckpointsConfig{Blobs: blobs, Signer: rotated, Marks: nil, Entries: nil, Now: nil},
	)
	if _, err := epochs.NewEpoch(t.Context(), time.Now()); err != nil {
		t.Fatalf("new epoch: %v", err)
	}

	newEpochSrv := runCheckpointServer(t, st, blobs,
		dustpan.WithAuditChain(audit.ChainOn), dustpan.WithAuditCheckpoints(rotated))
	waitForMetricLine(t, newEpochSrv.srv, "dustpan_audit_chain_invalid 0")
	newEpochSrv.stop()
}

// TestAuditCheckpointsNeedTheChain proves that checkpoints with the chain
// off stop the start.
func TestAuditCheckpointsNeedTheChain(t *testing.T) {
	t.Parallel()

	st := openChainStore(t)
	srv, _ := newCheckpointServer(t, st, openCheckpointBlobs(t),
		dustpan.WithAuditChain(audit.ChainOff), dustpan.WithAuditCheckpoints(newTestSigner(t, 1)))

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	if err := srv.Run(ctx); !errors.Is(err, dustpan.ErrCheckpointsNeedChain) {
		t.Fatalf("run: got %v, want ErrCheckpointsNeedChain", err)
	}
}

// newTestSigner returns a signer whose seed repeats b.
func newTestSigner(t *testing.T, b byte) *audit.Signer {
	t.Helper()

	signer, err := audit.NewSigner(bytes.Repeat([]byte{b}, 32))
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}

	return signer
}

// openCheckpointBlobs opens a blob store in a temporary directory that the
// test closes.
func openCheckpointBlobs(t *testing.T) *fs.Store {
	t.Helper()

	blobs, err := fs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	return blobs
}

// newCheckpointServer configures a server on st and blobs with extra
// options and answers it with its address. The caller runs it.
func newCheckpointServer(
	t *testing.T, st *sqlite.Store, blobs blob.Store, extra ...dustpan.Option,
) (*dustpan.Server, string) {
	t.Helper()

	authn, err := identity.NewStaticToken("admin", adminToken)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	ln := openTestListener(t)

	opts := []dustpan.Option{
		dustpan.WithInstallation(installationName),
		dustpan.WithNamespaceStore(st),
		dustpan.WithStackStore(st),
		dustpan.WithUpdateStore(st),
		dustpan.WithCheckpointStore(st),
		dustpan.WithEventStore(st),
		dustpan.WithSecretKeyStore(st),
		dustpan.WithSecretWrapper(testWrapper(t)),
		dustpan.WithWorkspaceStore(st),
		dustpan.WithStateStore(st),
		dustpan.WithBlobStore(blobs),
		dustpan.WithInventoryReader(st),
		dustpan.WithAuthenticator(authn),
		dustpan.WithListener(ln),
	}

	srv, err := dustpan.NewServer(append(opts, extra...)...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	return srv, "http://" + ln.Addr().String()
}

// runCheckpointServer starts a server on st and blobs with extra options,
// waits until it serves, and answers it. The test stops it at its end.
func runCheckpointServer(
	t *testing.T, st *sqlite.Store, blobs blob.Store, extra ...dustpan.Option,
) chainServer {
	t.Helper()

	srv, base := newCheckpointServer(t, st, blobs, extra...)

	ctx, cancel := context.WithCancel(t.Context())
	done := runServer(ctx, srv)

	stopped := false

	stop := func() {
		if stopped {
			return
		}

		stopped = true

		cancel()

		if runErr := <-done; runErr != nil {
			t.Errorf("run: %v", runErr)
		}
	}

	t.Cleanup(stop)

	c := chainServer{srv: srv, stop: stop, base: base}
	waitForLive(t, c)

	return c
}

// waitForCopy waits until the blob store holds a copy of the checkpoint
// that first names in the first layout: first itself, or a key with the same
// sequence and an attempt part.
func waitForCopy(t *testing.T, blobs blob.Store, first blob.Key) {
	t.Helper()

	dir := first.String()[:strings.LastIndexByte(first.String(), '/')+1]
	name := strings.TrimSuffix(first.String()[len(dir):], ".json")
	deadline := time.Now().Add(chainWait)

	for {
		objects, err := blobs.List(t.Context(), dir)
		if err != nil {
			t.Fatalf("list %s: %v", dir, err)
		}

		for _, obj := range objects {
			if strings.HasPrefix(strings.TrimPrefix(obj.Key.String(), dir), name) {
				return
			}
		}

		if time.Now().After(deadline) {
			t.Fatalf("no copy of checkpoint %s after %s", first, chainWait)
		}

		pauseChain(t)
	}
}

// waitForInvalidScopes waits until the chain check of srv counts at least
// one invalid scope, or fails the test.
func waitForInvalidScopes(t *testing.T, srv chainServer) {
	t.Helper()

	line := regexp.MustCompile(`(?m)^dustpan_audit_chain_invalid [1-9]\d*$`)
	deadline := time.Now().Add(chainWait)

	for {
		var out strings.Builder
		if err := srv.srv.Metrics().Write(&out); err != nil {
			t.Fatalf("write metrics: %v", err)
		}

		if line.MatchString(out.String()) {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("no invalid audit scope after %s:\n%s", chainWait, out.String())
		}

		pauseChain(t)
	}
}
