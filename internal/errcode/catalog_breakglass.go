package errcode

// BreakGlassSelfReview means that a person of a break-glass use tried to record the verdict of its review.
const BreakGlassSelfReview = "break-glass.self-review"

// breakGlassSpecs are the codes of the reviews of the break-glass uses.
func breakGlassSpecs() []Spec {
	return []Spec{
		{
			Code:    BreakGlassSelfReview,
			Meaning: "a person of the break-glass use cannot record the verdict of its review",
			Args:    nil,
		},
	}
}
