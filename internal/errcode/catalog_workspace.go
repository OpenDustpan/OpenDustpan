package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// WorkspaceClientEncrypted means that the server cannot do the request because the client encrypted the state.
	WorkspaceClientEncrypted = "workspace.client-encrypted"
	// WorkspaceLockMismatch means that the lock ID or the lock holder of the request differs from the lock of the
	// workspace.
	WorkspaceLockMismatch = "workspace.lock-mismatch"
	// WorkspaceNotLocked means that the request needs a locked workspace and the workspace is not locked.
	WorkspaceNotLocked = "workspace.not-locked"
)

// workspaceSpecs are the codes of the workspaces of the TFE-compatible API.
func workspaceSpecs() []Spec {
	return []Spec{
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
			Code:    WorkspaceNotLocked,
			Meaning: "the request needs a locked workspace and the workspace is not locked",
			Args:    nil,
		},
	}
}
