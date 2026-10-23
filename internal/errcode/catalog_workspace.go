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
	// WorkspaceChangedConcurrently means that another request changed the workspace at the same time.
	WorkspaceChangedConcurrently = "workspace.changed-concurrently"
	// WorkspaceNoState means that the workspace has no finalized state version.
	WorkspaceNoState = "workspace.no-state"
	// WorkspaceNoStateAt means that the time of the request is before the first finalized state version of the
	// workspace, at firstAt.
	WorkspaceNoStateAt = "workspace.no-state-at"
	// WorkspacePendingStateVersion means that the workspace has a pending state version.
	WorkspacePendingStateVersion = "workspace.pending-state-version"
	// WorkspaceTooManyPending means that the workspace has the largest number of pending state versions.
	WorkspaceTooManyPending = "workspace.too-many-pending"
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
		{
			Code:    WorkspaceChangedConcurrently,
			Meaning: "another request changed the workspace at the same time",
			Args:    nil,
		},
		{
			Code:    WorkspaceNoState,
			Meaning: "the workspace has no finalized state version",
			Args:    nil,
		},
		{
			Code:    WorkspaceNoStateAt,
			Meaning: "the time of the request is before the first finalized state version of the workspace, at firstAt",
			Args:    []Arg{{Name: "firstAt", Kind: KindTime}},
		},
		{
			Code:    WorkspacePendingStateVersion,
			Meaning: "the workspace has a pending state version",
			Args:    nil,
		},
		{
			Code:    WorkspaceTooManyPending,
			Meaning: "the workspace has the largest number of pending state versions",
			Args:    nil,
		},
	}
}
