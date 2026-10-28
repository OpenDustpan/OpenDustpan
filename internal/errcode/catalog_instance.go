package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// InstanceInvalidID is an instance ID that has a character outside the allowed set or a wrong length.
	InstanceInvalidID = "instance.invalid-id"
	// InstanceNotFound is an instance ID that has no version row.
	InstanceNotFound = "instance.not-found"
)

// instanceSpecs are the codes of the instance version rows.
func instanceSpecs() []Spec {
	return []Spec{
		{
			Code:    InstanceInvalidID,
			Meaning: "the instance ID has a character outside letters, digits and . - _ + or has a wrong length",
			Args:    nil,
		},
		{Code: InstanceNotFound, Meaning: "no version row has the instance ID", Args: nil},
	}
}
