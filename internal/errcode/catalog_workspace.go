package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// WorkspaceAlreadyExists means that a workspace with the same name exists in the namespace.
	WorkspaceAlreadyExists = "workspace.already-exists"
	// WorkspaceClientEncrypted means that the server cannot do the request because the client encrypted the state.
	WorkspaceClientEncrypted = "workspace.client-encrypted"
	// WorkspaceLockMismatch means that the lock ID or the lock holder of the request differs from the lock of the
	// workspace.
	WorkspaceLockMismatch = "workspace.lock-mismatch"
	// WorkspaceLocked means that another principal holds the lock of the workspace, or that the request names no lock
	// of a locked workspace.
	WorkspaceLocked = "workspace.locked"
	// WorkspaceNotFound means that the workspace, its state version or its namespace does not exist, or that the
	// caller cannot read it.
	WorkspaceNotFound = "workspace.not-found"
	// WorkspaceNotLocked means that the request needs a locked workspace and the workspace is not locked.
	WorkspaceNotLocked = "workspace.not-locked"
)

// workspaceSpecs are the codes of the workspaces of the TFE-compatible API.
func workspaceSpecs() []Spec {
	return []Spec{
		{
			Code:    WorkspaceAlreadyExists,
			Meaning: "a workspace with the same name exists in the namespace",
			Args:    nil,
		},
		{
			Code:    WorkspaceClientEncrypted,
			Meaning: "the server cannot do the request because the client encrypted the state",
			Args:    nil,
		},
		{
			Code:    WorkspaceLockMismatch,
			Meaning: "the lock ID or the lock holder of the request differs from the lock of the workspace",
			Args:    nil,
		},
		{
			Code:    WorkspaceLocked,
			Meaning: "another principal holds the lock of the workspace, or the request names no lock of a locked workspace",
			Args:    nil,
		},
		{
			Code:    WorkspaceNotFound,
			Meaning: "the workspace, its state version or its namespace does not exist, or the caller cannot read it",
			Args:    nil,
		},
		{
			Code:    WorkspaceNotLocked,
			Meaning: "the request needs a locked workspace and the workspace is not locked",
			Args:    nil,
		},
	}
}
