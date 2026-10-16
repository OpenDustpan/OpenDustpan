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
// not match.
func TestAuditCheckpointsAreWrittenAndChecked(t *testing.T) {
	t.Parallel()

	st := openChainStore(t)
	blobs := openCheckpointBlobs(t)
	acme := createChainNamespace(t, st)
	appendChainEntry(t, st, acme, "req-1")

	key, err := audit.CheckpointKey(0, acme.Name, 1)
	if err != nil {
		t.Fatalf("checkpoint key: %v", err)
	}

	first := runCheckpointServer(t, st, blobs,
		dustpan.WithAuditChain(audit.ChainOn), dustpan.WithAuditCheckpoints(newTestSigner(t, 1)))
	waitForBlob(t, blobs, key)
	first.stop()

	other := runCheckpointServer(t, st, blobs,
		dustpan.WithAuditChain(audit.ChainOn), dustpan.WithAuditCheckpoints(newTestSigner(t, 2)))
	// The scope that the first key signed fails: acme.
	waitForInvalidScopes(t, other)
	other.stop()

	// With the first key as a verify key, the checkpoint matches again.
	rotated := newTestSigner(t, 2)
	if err := rotated.AddVerifyKey(newTestSigner(t, 1).PublicKey()); err != nil {
		t.Fatalf("add verify key: %v", err)
	}

	rotatedSrv := runCheckpointServer(t, st, blobs,
		dustpan.WithAuditChain(audit.ChainOn), dustpan.WithAuditCheckpoints(rotated))
	waitForMetricLine(t, rotatedSrv.srv, "dustpan_audit_chain_invalid 0")
	rotatedSrv.stop()
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
		dustpan.WithRegion(region),
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

// waitForBlob waits until blobs holds an object under key.
func waitForBlob(t *testing.T, blobs blob.Store, key blob.Key) {
	t.Helper()

	deadline := time.Now().Add(chainWait)

	for {
		body, err := blobs.Get(t.Context(), key)
		if err == nil {
			if closeErr := body.Close(); closeErr != nil {
				t.Fatalf("close %s: %v", key, closeErr)
			}

			return
		}

		if !errors.Is(err, blob.ErrNotFound) {
			t.Fatalf("get %s: %v", key, err)
		}

		if time.Now().After(deadline) {
			t.Fatalf("no checkpoint %s after %s", key, chainWait)
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
