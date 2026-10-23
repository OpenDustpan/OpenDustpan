package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// StateVersionBusy means that the server has no room for the body of a state version now. The caller tries again
	// after the Retry-After time.
	StateVersionBusy = "state-version.busy"
)

// stateVersionSpecs are the codes of the state versions of the TFE-compatible API.
func stateVersionSpecs() []Spec {
	return []Spec{
		{
			Code:    StateVersionBusy,
			Meaning: "the server has no room for the body of a state version now; try again after the Retry-After time",
			Args:    nil,
		},
	}
}
