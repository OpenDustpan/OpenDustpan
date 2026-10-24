package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// MFAFreshStepRequired means that the write needs a fresh authentication step.
	MFAFreshStepRequired = "mfa.fresh-step-required"
)

// mfaSpecs are the codes of a fresh authentication step.
func mfaSpecs() []Spec {
	return []Spec{
		{Code: MFAFreshStepRequired, Meaning: "the write needs a fresh authentication step", Args: nil},
	}
}
