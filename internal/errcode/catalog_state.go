package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// StateBadPath means that a version or a value path of a state read is invalid.
	StateBadPath = "state.bad-path"
)

// stateSpecs are the codes of the tool-neutral state read.
func stateSpecs() []Spec {
	return []Spec{
		{Code: StateBadPath, Meaning: "a version or a value path of a state read is invalid", Args: nil},
	}
}
