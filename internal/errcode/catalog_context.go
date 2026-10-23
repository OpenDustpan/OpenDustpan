package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// ContextFieldFromCode means that the output of the code of the object sets the context field.
	ContextFieldFromCode = "context.field-from-code"
	// ContextInvalid means that a value of the context is out of its bounds.
	ContextInvalid = "context.invalid"
	// ContextNotSet means that the object has no context that the API set.
	ContextNotSet = "context.not-set"
	// ContextObjectNotFound means that the namespace or the unit does not exist, or the caller cannot read it.
	ContextObjectNotFound = "context.object-not-found"
	// ContextUnitDeleted means that the unit is deleted, so its context does not change.
	ContextUnitDeleted = "context.unit-deleted"
	// ContextUnknownSection means that the request names a section of the context that does not exist.
	ContextUnknownSection = "context.unknown-section"
)

// argField is the name of the argument that names a field of the context.
const argField = "field"

// contextSpecs are the codes of the context of an object.
func contextSpecs() []Spec {
	return []Spec{
		{
			Code:    ContextFieldFromCode,
			Meaning: "the output of the code of the object sets the context field",
			Args:    []Arg{{Name: argField, Kind: KindIdentifier}},
		},
		{Code: ContextInvalid, Meaning: "a value of the context is out of its bounds", Args: nil},
		{Code: ContextNotSet, Meaning: "the object has no context that the API set", Args: nil},
		{
			Code:    ContextObjectNotFound,
			Meaning: "the namespace or the unit does not exist, or the caller cannot read it",
			Args:    nil,
		},
		{Code: ContextUnitDeleted, Meaning: "the unit is deleted, so its context does not change", Args: nil},
		{
			Code:    ContextUnknownSection,
			Meaning: "the request names a section of the context that does not exist",
			Args:    nil,
		},
	}
}
