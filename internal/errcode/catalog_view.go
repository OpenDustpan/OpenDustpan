package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// ViewAlreadyExists means that the owner already has a saved view with the name in the namespace.
	ViewAlreadyExists = "view.already-exists"
	// ViewInvalid means that the saved view is not valid.
	ViewInvalid = "view.invalid"
	// ViewLimit means that the owner already has the largest number of saved views in the namespace.
	ViewLimit = "view.limit"
	// ViewNotFound means that no saved view with the ID is visible to the caller.
	ViewNotFound = "view.not-found"
	// ViewNotPerson means that the caller is not a person, and only a person has saved views.
	ViewNotPerson = "view.not-person"
)

// viewSpecs are the codes of the saved views.
func viewSpecs() []Spec {
	return []Spec{
		{
			Code:    ViewAlreadyExists,
			Meaning: "the owner already has a saved view with the name in the namespace",
			Args:    nil,
		},
		{Code: ViewInvalid, Meaning: "the saved view is not valid", Args: nil},
		{
			Code:    ViewLimit,
			Meaning: "the owner already has the largest number of saved views in the namespace",
			Args:    nil,
		},
		{Code: ViewNotFound, Meaning: "no saved view with the ID is visible to the caller", Args: nil},
		{
			Code:    ViewNotPerson,
			Meaning: "the caller is not a person, and only a person has saved views",
			Args:    nil,
		},
	}
}
