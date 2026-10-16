package dustpan_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob/blobfault"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/namespace"
)

func TestNewServerRefusesNegativeBlobCache(t *testing.T) {
	t.Parallel()

	st, blobs, authn := requireServerPorts(t)
	opts := append(fullServerOptions(t, st, blobs, authn), dustpan.WithBlobCache(-1))

	if _, err := dustpan.NewServer(opts...); !errors.Is(err, dustpan.ErrInvalidOption) {
		t.Fatalf("NewServer with blob cache -1: err = %v, want ErrInvalidOption", err)
	}
}

// TestBlobCacheServesRepeatReads reads one OpenTofu state twice. With the
// blob cache, the second read does not reach the blob store.
func TestBlobCacheServesRepeatReads(t *testing.T) {
	t.Parallel()

	st, blobs, authn := requireServerPorts(t)

	counted, err := blobfault.New(blobs)
	if err != nil {
		t.Fatalf("blobfault: %v", err)
	}

	opts := append(fullServerOptions(t, st, blobs, authn),
		dustpan.WithBlobStore(counted),
		dustpan.WithBlobCache(1<<20),
	)

	srv, err := dustpan.NewServer(opts...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	err = st.CreateNamespace(t.Context(), namespace.Namespace{
		CreatedAt:       time.Now(),
		Name:            "acme",
		HomeRegion:      region,
		FailoverVersion: 1,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	})
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	svc := srv.Workspaces()
	if svc == nil {
		t.Fatal("server has no workspace service")
	}

	const content = `{"version":4,"serial":1,"lineage":"l1"}`

	w := createLockedWorkspace(t, svc)
	finalized := uploadWorkspaceState(t, svc, w.ID, content)

	checkWorkspaceContent(t, svc, w.ID, finalized.ID, content)

	first := counted.Calls("Get")

	checkWorkspaceContent(t, svc, w.ID, finalized.ID, content)

	if n := counted.Calls("Get"); n != first {
		t.Fatalf("blob store Get calls after a second read = %d, want %d", n, first)
	}
}
