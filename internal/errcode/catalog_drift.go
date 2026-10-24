package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// DriftStartConflict means that a drift check of the unit started in the same minute with another pool, location or
	// reason.
	DriftStartConflict = "drift.start-conflict"
)

// driftSpecs are the codes of the drift checks.
func driftSpecs() []Spec {
	return []Spec{
		{
			Code:    DriftStartConflict,
			Meaning: "a drift check of the unit started in the same minute with another pool, location or reason",
			Args:    nil,
		},
	}
}
