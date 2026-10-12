package workspace

import (
	"context"
	"io"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sealer seals OpenTofu state blobs and outputs documents with the data key
// of a workspace scope (the namespace and the workspace ID). The server
// seals each new state as it streams into the blob store and opens it on
// download, restore copy and backup read, so plain state never reaches the
// database, the blob store or backups.
type Sealer interface {
	// SealState seals the plaintext in r with the data key of the scope,
	// creating the key on first use. The caller streams the result.
	SealState(ctx context.Context, ns namespace.Name, workspaceID string, r io.Reader) (io.Reader, error)
	// OpenState opens a stream sealed by SealState for the same scope.
	OpenState(ctx context.Context, ns namespace.Name, workspaceID string, r io.Reader) (io.Reader, error)
	// SealOutputs seals one outputs document with the data key of the scope.
	SealOutputs(ctx context.Context, ns namespace.Name, workspaceID string, plain []byte) ([]byte, error)
	// OpenOutputs opens one outputs document sealed by SealOutputs.
	OpenOutputs(ctx context.Context, ns namespace.Name, workspaceID string, sealed []byte) ([]byte, error)
}
