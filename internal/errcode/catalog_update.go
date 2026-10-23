package errcode

// The codes of the updates of the Pulumi Cloud-compatible API. A code is
// never removed and never changes its meaning.
const (
	// UpdateCheckpointConflict is a checkpoint that differs from the one saved
	// with the same sequence number.
	UpdateCheckpointConflict = "update.checkpoint-conflict"
	// UpdateCheckpointStale is a checkpoint that is older than the last one
	// saved.
	UpdateCheckpointStale = "update.checkpoint-stale"
	// UpdateConcurrentModification is an update that changed while the request
	// ran.
	UpdateConcurrentModification = "update.concurrent-modification"
	// UpdateConflict is a request that another update of the stack blocks.
	UpdateConflict = "update.conflict"
	// UpdateConsoleSession is a console session on a route that creates or
	// starts an update.
	UpdateConsoleSession = "update.console-session"
	// UpdateDeltaInvalid is a checkpoint delta that does not apply.
	UpdateDeltaInvalid = "update.delta-invalid"
	// UpdateInvalid is an update ID or a stack version in the path that is not
	// valid, or an update request that is not valid.
	UpdateInvalid = "update.invalid"
	// UpdateLeaseExpired is a lease token of an update that expired.
	UpdateLeaseExpired = "update.lease-expired"
	// UpdateLeaseInvalid is a lease token of an update that is not valid.
	UpdateLeaseInvalid = "update.lease-invalid"
	// UpdateNotFound is an update that does not exist.
	UpdateNotFound = "update.not-found"
	// UpdateTooLarge is a request of the update cycle over a size limit.
	UpdateTooLarge = "update.too-large"
	// UpdateWrongState is a request that the update does not allow in its
	// current state.
	UpdateWrongState = "update.wrong-state"
)

// updateSpecs are the codes of the updates of the Pulumi Cloud-compatible API.
// No code of the area has an argument yet.
func updateSpecs() []Spec {
	return []Spec{
		updateSpec(UpdateCheckpointConflict, "the checkpoint differs from the one saved with the same sequence number"),
		updateSpec(UpdateCheckpointStale, "the checkpoint is older than the last one saved"),
		updateSpec(UpdateConcurrentModification, "the update changed while the request ran"),
		updateSpec(UpdateConflict, "another update of the stack is in progress: wait for it to end, or cancel it"),
		updateSpec(UpdateConsoleSession, "a console session cannot start an update: the request needs an access token"),
		updateSpec(UpdateDeltaInvalid, "the checkpoint delta does not apply to the saved checkpoint"),
		updateSpec(UpdateInvalid, "the update request, the update ID or the stack version is not valid"),
		updateSpec(UpdateLeaseExpired, "the lease of the update expired"),
		updateSpec(UpdateLeaseInvalid, "the lease token of the update is not valid"),
		updateSpec(UpdateNotFound, "the update does not exist"),
		updateSpec(UpdateTooLarge, "a request of the update is larger than its limit"),
		updateSpec(UpdateWrongState, "the update does not allow the request in its current state"),
	}
}

// updateSpec returns the spec of an update code with its meaning and no
// argument.
func updateSpec(code, meaning string) Spec {
	return Spec{Code: code, Meaning: meaning, Args: nil}
}
