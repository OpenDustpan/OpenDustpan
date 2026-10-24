package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// TriggerCycle means that the save would close a loop of follows triggers.
	TriggerCycle = "trigger.cycle"
	// TriggerDisabled means that the trigger is off, so a fire is refused.
	TriggerDisabled = "trigger.disabled"
	// TriggerFireConflict means that the idempotency key of the fire names a job with another request.
	TriggerFireConflict = "trigger.fire-conflict"
	// TriggerInvalid means that a field of the trigger is not valid.
	TriggerInvalid = "trigger.invalid"
	// TriggerNotFound means that the namespace, the unit or the trigger of the request does not exist or is not
	// readable by the caller.
	TriggerNotFound = "trigger.not-found"
	// TriggerSaverDenied means that the last saver of the trigger no longer holds the actions that a start needs.
	TriggerSaverDenied = "trigger.saver-denied"
)

// triggerSpecs are the codes of the run triggers.
func triggerSpecs() []Spec {
	return []Spec{
		{Code: TriggerCycle, Meaning: "the save would close a loop of follows triggers", Args: nil},
		{Code: TriggerDisabled, Meaning: "the trigger is off, so the fire is refused", Args: nil},
		{
			Code:    TriggerFireConflict,
			Meaning: "the idempotency key of the fire names a job with another request",
			Args:    nil,
		},
		{Code: TriggerInvalid, Meaning: "a field of the trigger is not valid", Args: nil},
		{
			Code: TriggerNotFound,
			Meaning: "the namespace, the unit or the trigger of the request does not exist, " +
				"or the caller cannot read it",
			Args: nil,
		},
		{
			Code:    TriggerSaverDenied,
			Meaning: "the last saver of the trigger no longer holds the actions that a start needs",
			Args:    nil,
		},
	}
}
