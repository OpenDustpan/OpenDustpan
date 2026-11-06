package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// PlacementConflict means that a placement rule is hidden by an earlier rule, or hides a later rule.
	PlacementConflict = "placement.conflict"
	// PlacementInvalidPattern means that a pattern of a placement rule is not valid.
	PlacementInvalidPattern = "placement.invalid-pattern"
	// PlacementInvalidPosition means that the position of a placement rule is outside the list.
	PlacementInvalidPosition = "placement.invalid-position"
	// PlacementInvalidTool means that the tool of a placement rule has no placement rules.
	PlacementInvalidTool = "placement.invalid-tool"
	// PlacementNoMatch means that no placement rule of the namespace matches a new unit, so the server refuses to make
	// it.
	PlacementNoMatch = "placement.no-match"
	// PlacementRuleNotFound means that the namespace holds no placement rule with the ID.
	PlacementRuleNotFound = "placement.rule-not-found"
	// PlacementTooManyRules means that the namespace holds the most placement rules.
	PlacementTooManyRules = "placement.too-many-rules"
)

// placementSpecs are the codes of the placement rules of collections.
func placementSpecs() []Spec {
	return []Spec{
		{
			Code:    PlacementConflict,
			Meaning: "a placement rule is hidden by an earlier rule, or hides a later rule",
			Args:    nil,
		},
		{Code: PlacementInvalidPattern, Meaning: "a pattern of the placement rule is not valid", Args: nil},
		{
			Code:    PlacementInvalidPosition,
			Meaning: "the position of the placement rule is outside the list",
			Args:    nil,
		},
		{Code: PlacementInvalidTool, Meaning: "the tool of the placement rule has no placement rules", Args: nil},
		{
			Code:    PlacementNoMatch,
			Meaning: "no placement rule of the namespace matches the new unit, so the server does not make it",
			Args:    nil,
		},
		{Code: PlacementRuleNotFound, Meaning: "the namespace holds no placement rule with the ID", Args: nil},
		{Code: PlacementTooManyRules, Meaning: "the namespace holds the most placement rules", Args: nil},
	}
}
