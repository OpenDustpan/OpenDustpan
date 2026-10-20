package workspace_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// ctxBlobs fails the deletes of a blob store whose context is done, as an object
// store does when the request that asked for the delete has gone away.
type ctxBlobs struct {
	blob.Store
}

// Delete fails when ctx is done.
func (c ctxBlobs) Delete(ctx context.Context, key blob.Key) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("delete blob: %w", err)
	}

	return c.Store.Delete(ctx, key)
}

// cancelAtEnd sends a body, then cancels the request when the body ends.
type cancelAtEnd struct {
	r      io.Reader
	cancel context.CancelFunc
}

// Read returns the body, and cancels the request at its end.
func (c *cancelAtEnd) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if errors.Is(err, io.EOF) {
		c.cancel()
	}

	return n, err //nolint:wrapcheck // Transparent reader: callers must see io.EOF unchanged.
}

// TestUploadCleanupSurvivesTheRequest proves an upload that the reader refuses
// deletes its blob even when the request is gone by then. The delete runs on the
// cleanup context, not the request context, so the blob does not stay behind
// (ADR 49, decision 8).
func TestUploadCleanupSurvivesTheRequest(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	body := `{"serial":1,"lineage":"l"} junk`

	svc, err := workspace.NewService(workspace.Config{
		ManualLocks:        nil,
		Workspaces:         f.store,
		States:             f.store,
		Blobs:              ctxBlobs{Store: f.blobs},
		Namespaces:         f.namespaces,
		Sealer:             f.secrets,
		Logger:             nil,
		Now:                func() time.Time { return *f.now },
		MaxStateBytes:      64,
		MaxPendingVersions: 0,
		Retention:          0,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	pending, err := svc.BeginState(ctx, f.ws.ID, "alice", workspace.NewState{
		Lineage: "l",
		MD5:     md5Hex(body),
		Outputs: nil,
		Serial:  1,
		Force:   false,
	}, authority(noOverrides(), nil))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	reqCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	_, err = svc.Upload(
		reqCtx,
		pending.Version.ID,
		pending.Upload,
		&cancelAtEnd{r: strings.NewReader(body), cancel: cancel},
	)
	if !errors.Is(err, workspace.ErrInvalidState) {
		t.Fatalf("upload with trailing data: err = %v, want ErrInvalidState", err)
	}

	assertNoOrphanBlobs(t, f)
}
