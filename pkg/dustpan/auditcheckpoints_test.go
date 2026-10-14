package dustpan_test

import (
	"bytes"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/dustpan"
)

// TestAuditCheckpointsAreWrittenAndChecked proves ADR 60 decision 18: with
// the chain and checkpoints on, the check at start signs the last hash of
// each scope into the blob store. A start with another key finds that the
// checkpoint does not match.
func TestAuditCheckpointsAreWrittenAndChecked(t *testing.T) {
	t.Parallel()

	st := openChainStore(t)
	blobs := newChainBlobs(t)
	acme := createChainNamespace(t, st)
	appendChainEntry(t, st, acme, "req-1")

	key, err := audit.CheckpointKey(0, acme.Name, 1)
	if err != nil {
		t.Fatalf("checkpoint key: %v", err)
	}

	_, stop := runAuditServer(t, st, blobs,
		dustpan.WithAuditChain(audit.ChainOn), dustpan.WithAuditCheckpoints(newTestSigner(t, 1)))
	waitForBlob(t, blobs, key)
	stop()

	srv, stopOther := runAuditServer(t, st, blobs,
		dustpan.WithAuditChain(audit.ChainOn), dustpan.WithAuditCheckpoints(newTestSigner(t, 2)))
	// Every scope that the first key signed fails: acme, and @server when
	// the first server signed it before it stopped.
	waitForInvalidScopes(t, srv)
	stopOther()

	// With the first key as a verify key, the checkpoint matches again.
	rotated := newTestSigner(t, 2)
	if err := rotated.AddVerifyKey(newTestSigner(t, 1).PublicKey()); err != nil {
		t.Fatalf("add verify key: %v", err)
	}

	srv, stopRotated := runAuditServer(t, st, blobs,
		dustpan.WithAuditChain(audit.ChainOn), dustpan.WithAuditCheckpoints(rotated))
	waitForMetricLine(t, srv, "dustpan_audit_chain_invalid 0")
	stopRotated()
}

// TestAuditCheckpointsNeedTheChain proves that checkpoints with the chain
// off stop the start.
func TestAuditCheckpointsNeedTheChain(t *testing.T) {
	t.Parallel()

	st := openChainStore(t)
	srv := newAuditServer(t, st, newChainBlobs(t),
		dustpan.WithAuditChain(audit.ChainAuto), dustpan.WithAuditCheckpoints(newTestSigner(t, 1)))

	if err := srv.Run(t.Context()); !errors.Is(err, dustpan.ErrCheckpointsNeedChain) {
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
func waitForInvalidScopes(t *testing.T, srv *dustpan.Server) {
	t.Helper()

	line := regexp.MustCompile(`(?m)^dustpan_audit_chain_invalid [1-9]\d*$`)
	deadline := time.Now().Add(chainWait)

	for {
		var out strings.Builder
		if err := srv.Metrics().Write(&out); err != nil {
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
