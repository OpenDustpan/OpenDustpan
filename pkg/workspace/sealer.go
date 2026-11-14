package workspace

import (
	"context"
	"io"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sealer seals OpenTofu state blobs and outputs documents with the data key
// of a workspace scope (the namespace and the workspace ID). Each sealed
// value is bound to one state version ID, so a value sealed for one version
// does not open as another version of the same workspace. The server seals
// each new state as it streams into the blob store and opens it on download,
// restore copy and backup read, so plain state never reaches the database,
// the blob store or backups.
type Sealer interface {
	// SealState seals the plaintext in r with the data key of the scope,
	// bound to versionID, creating the key on first use. The caller streams
	// the result.
	SealState(
		ctx context.Context, ns namespace.Name, workspaceID, versionID string, r io.Reader,
	) (io.Reader, error)
	// OpenState opens a stream sealed by SealState for the same scope and
	// state version. The caller closes the reader: Close clears the key that
	// the reader holds, also when the reader was never read. Close does not
	// close r.
	OpenState(
		ctx context.Context, ns namespace.Name, workspaceID, versionID string, r io.Reader,
	) (io.ReadCloser, error)
	// SealOutputs seals one outputs document with the data key of the scope,
	// bound to versionID.
	SealOutputs(
		ctx context.Context, ns namespace.Name, workspaceID, versionID string, plain []byte,
	) ([]byte, error)
	// OpenOutputs opens one outputs document sealed by SealOutputs for the
	// same state version.
	OpenOutputs(
		ctx context.Context, ns namespace.Name, workspaceID, versionID string, sealed []byte,
	) ([]byte, error)
}
