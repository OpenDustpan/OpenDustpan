package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// UnitClientEncrypted means that the client encrypted the state of the unit, so the server cannot read its outputs.
	UnitClientEncrypted = "unit.client-encrypted"
	// UnitNoCollection means that the unit is in no collection.
	UnitNoCollection = "unit.no-collection"
	// UnitNoState means that the unit has no state yet.
	UnitNoState = "unit.no-state"
	// UnitNotFound is a unit that does not exist, in a tool-neutral API.
	UnitNotFound = "unit.not-found"
	// UnitStateMoved means that a new state of the unit replaced the state during the read.
	UnitStateMoved = "unit.state-moved"
)

// unitSpecs are the codes of the units of the tool-neutral APIs.
func unitSpecs() []Spec {
	return []Spec{
		{
			Code:    UnitClientEncrypted,
			Meaning: "the client encrypted the state of the unit, so the server cannot read its outputs",
			Args:    nil,
		},
		{Code: UnitNoCollection, Meaning: "the unit is in no collection", Args: nil},
		{
			Code:    UnitNoState,
			Meaning: "the unit has no state yet",
			Args:    []Arg{{Name: "unit", Kind: KindIdentifier}},
		},
		{Code: UnitNotFound, Meaning: "the unit does not exist in the namespace", Args: nil},
		{
			Code:    UnitStateMoved,
			Meaning: "a new state of the unit replaced the state during the read",
			Args:    nil,
		},
	}
}
