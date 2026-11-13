package dustpan_test

import (
	"context"
	"crypto/md5" //nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.
	"encoding/hex"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/blobtest"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
	"opendustpan.org/opendustpan/pkg/workspace/workspacetest"
)

// sealedContent is a state file that a service with a sealer stores sealed.
const sealedContent = `{"version":4,"serial":1}`

// TestRunWritesNoWorkspaceBlobAtStartup proves a start writes and deletes no
// workspace blob: a sealed version keeps its blob, its content key and its
// flag.
func TestRunWritesNoWorkspaceBlobAtStartup(t *testing.T) {
	t.Parallel()

	st, blobs, authn := requireServerPorts(t)
	namespaces := seedNamespaces(t, st)
	sv := seedSealedState(t, st, blobs, namespaces)
	before := listBlobs(t, blobs)

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	served := make(chan struct{})

	srv, err := dustpan.NewServer(append(
		fullServerOptions(t, st, blobs, authn),
		dustpan.WithSecretWrapper(wrapper),
		dustpan.WithListenAddr("127.0.0.1:0"),
		dustpan.WithBeforeServe(func(context.Context) error {
			close(served)

			return nil
		}),
	)...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan error, 1)

	go func() {
		done <- srv.Run(ctx)
	}()

	select {
	case <-served:
	case runErr := <-done:
		t.Fatalf("server stopped before it served: %v", runErr)
	case <-time.After(10 * time.Second):
		t.Fatal("server did not finish its start-up steps after 10s")
	}

	if after := listBlobs(t, blobs); !slices.Equal(after, before) {
		t.Errorf("workspace blobs after start = %v, want %v", after, before)
	}

	stored, err := st.GetStateVersion(t.Context(), sv.ID)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}

	if stored.Content != sv.Content || !stored.Sealed {
		t.Errorf("state after start names %q, sealed %v; want %q, sealed", stored.Content, stored.Sealed, sv.Content)
	}

	cancel()

	if err := <-done; err != nil {
		t.Fatalf("run server: %v", err)
	}
}

// listBlobs returns every workspace blob with its modification time, so a
// rewrite of a blob under the same key also shows.
func listBlobs(t *testing.T, blobs *fs.Store) []blob.Object {
	t.Helper()

	listed, err := blobtest.ListAll(t.Context(), blobs, "namespaces/")
	if err != nil {
		t.Fatalf("list blobs: %v", err)
	}

	slices.SortFunc(listed, func(a, b blob.Object) int { return strings.Compare(a.Key.String(), b.Key.String()) })

	return listed
}

// seedNamespaces returns a namespace service holding acme whose primary installation is this installation.
func seedNamespaces(t *testing.T, st *sqlite.Store) *namespace.Service {
	t.Helper()

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Installation: installationName})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.Now(),
		Name:                "acme",
		PrimaryInstallation: installationName,
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}

	err = st.CreateNamespace(t.Context(), ns)
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	return namespaces
}

// seedSealedState writes one finalized version through a service with a
// sealer: a sealed blob with the sealed flag.
func seedSealedState(
	t *testing.T, st *sqlite.Store, blobs *fs.Store, namespaces *namespace.Service,
) workspace.StateVersion {
	t.Helper()

	ctx := t.Context()

	secrets, err := secret.NewService(secret.Config{
		Keys: st, Wrapper: testWrapper(t), Namespaces: namespaces, Now: nil, MaxBatchBytes: 0,
	})
	if err != nil {
		t.Fatalf("secrets: %v", err)
	}

	sealed, err := workspace.NewService(workspace.Config{
		ManualLocks:        nil,
		BlobGuard:          blobguardtest.None{},
		Workspaces:         st,
		States:             st,
		Blobs:              blobs,
		Namespaces:         namespaces,
		OutOfScope:         workspacetest.NewRefusals(),
		Sealer:             secrets,
		Logger:             nil,
		Now:                nil,
		MaxStateBytes:      0,
		MaxPendingVersions: 0,
		Retention:          0,
	})
	if err != nil {
		t.Fatalf("sealed service: %v", err)
	}

	name, err := workspace.ParseName("network")
	if err != nil {
		t.Fatalf("parse name: %v", err)
	}

	ws, err := sealed.Create(ctx, workspace.Ref{Namespace: "acme", Name: name}, placeWorkspace(t, st, "acme"))
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	_, err = sealed.Lock(ctx, ws.ID, "alice", "apply")
	if err != nil {
		t.Fatalf("lock: %v", err)
	}

	sum := md5.Sum([]byte(sealedContent)) //nolint:gosec // The TFE protocol names state by MD5.

	pending, err := sealed.BeginState(ctx, ws.ID, "alice", workspace.NewState{
		Lineage: "l1",
		MD5:     hex.EncodeToString(sum[:]),
		Outputs: nil,
		Serial:  1,
		Force:   false,
	}, workspace.Authority{Allow: namespace.Overrides{Forced: false, Unlocked: false}, Record: nil})
	if err != nil {
		t.Fatalf("begin state: %v", err)
	}

	sv, err := sealed.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader(sealedContent))
	if err != nil {
		t.Fatalf("upload state: %v", err)
	}

	_, err = sealed.Unlock(ctx, ws.ID, "alice")
	if err != nil {
		t.Fatalf("unlock: %v", err)
	}

	return sv
}
