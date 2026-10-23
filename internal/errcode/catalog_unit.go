package errcode

// UnitNotFound is a unit that does not exist, in a tool-neutral API.
const UnitNotFound = "unit.not-found"

// unitSpecs are the codes of the units of the tool-neutral APIs.
func unitSpecs() []Spec {
	return []Spec{
		{Code: UnitNotFound, Meaning: "the unit does not exist in the namespace", Args: nil},
	}
}
