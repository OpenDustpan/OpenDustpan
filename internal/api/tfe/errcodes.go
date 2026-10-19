package tfe

// The machine codes of the errors that this API answers with a JSON:API
// code member. A client selects its message by the code, not by the English
// text of the detail. A code is never removed and never changes its
// meaning. Add a new code to ErrorCodes in the same change.
const (
	// codeNotLocked is a 409 for a request that needs a locked workspace
	// and finds it unlocked, for example a force-unlock.
	codeNotLocked = "workspace.not-locked"
	// codeLockMismatch is a 409 for a request that names a lock ID or a lock
	// holder that differs from the current lock of the workspace.
	codeLockMismatch = "workspace.lock-mismatch"
	// codeClientEncrypted is a 409 for a request that the server cannot do
	// because the client encrypted the state, for example a force-unlock
	// without the forced-write switches.
	codeClientEncrypted = "workspace.client-encrypted"
)

// ErrorCodes lists every machine code of this API. It is the one place that
// names them.
func ErrorCodes() []string {
	return []string{codeNotLocked, codeLockMismatch, codeClientEncrypted}
}
