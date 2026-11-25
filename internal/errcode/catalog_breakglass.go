package errcode

const (
	// BreakGlassSelfReview means that a person of a break-glass use tried to record the verdict of its review.
	BreakGlassSelfReview = "break-glass.self-review"
	// BreakGlassNotFound means that no break-glass account has the ID, or that the passkey is not one of the account.
	BreakGlassNotFound = "break-glass.not-found"
	// BreakGlassBusy means that another change held the break-glass lock past its timeout.
	BreakGlassBusy = "break-glass.busy"
)

// breakGlassSpecs are the codes of the reviews of the break-glass uses and of
// the break-glass accounts.
func breakGlassSpecs() []Spec {
	return []Spec{
		{
			Code:    BreakGlassSelfReview,
			Meaning: "a person of the break-glass use cannot record the verdict of its review",
			Args:    nil,
		},
		{
			Code:    BreakGlassNotFound,
			Meaning: "no break-glass account has the ID, or the passkey is not one of the account",
			Args:    nil,
		},
		{
			Code:    BreakGlassBusy,
			Meaning: "another change held the break-glass lock past its timeout",
			Args:    nil,
		},
	}
}
