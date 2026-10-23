package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// CollectionAlreadyExists means that a collection with the same name exists in the namespace.
	CollectionAlreadyExists = "collection.already-exists"
	// CollectionChanged means that the collection of the unit changed during the move.
	CollectionChanged = "collection.changed"
	// CollectionInvalidName means that the name of the collection is not valid.
	CollectionInvalidName = "collection.invalid-name"
	// CollectionMoveOutLocked means that a manual lock of the collection refuses a move of a unit out of it.
	CollectionMoveOutLocked = "collection.move-out-locked"
	// CollectionNotEmpty means that the collection still holds units.
	CollectionNotEmpty = "collection.not-empty"
	// CollectionNotFound means that the collection does not exist in the namespace.
	CollectionNotFound = "collection.not-found"
)

// collectionSpecs are the codes of the collections of units.
func collectionSpecs() []Spec {
	return []Spec{
		{
			Code:    CollectionAlreadyExists,
			Meaning: "a collection with the same name exists in the namespace",
			Args:    nil,
		},
		{Code: CollectionChanged, Meaning: "the collection of the unit changed during the move", Args: nil},
		{Code: CollectionInvalidName, Meaning: "the name of the collection is not valid", Args: nil},
		{
			Code:    CollectionMoveOutLocked,
			Meaning: "a manual lock of the collection refuses a move of a unit out of it",
			Args:    nil,
		},
		{Code: CollectionNotEmpty, Meaning: "the collection still holds units", Args: nil},
		{Code: CollectionNotFound, Meaning: "the collection does not exist in the namespace", Args: nil},
	}
}
