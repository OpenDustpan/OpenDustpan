package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// LockAlreadyLocked means that the object already has a manual lock.
	LockAlreadyLocked = "lock.already-locked"
	// LockChanged means that the manual lock changed during the removal.
	LockChanged = "lock.changed"
	// LockInvalidTarget means that the lock request names no unit, no collection and no namespace.
	LockInvalidTarget = "lock.invalid-target"
	// LockLocked is a request that a manual lock refuses.
	LockLocked = "lock.locked"
	// LockNotHolder means that another principal holds the manual lock.
	LockNotHolder = "lock.not-holder"
	// LockNotLocked means that the object has no manual lock.
	LockNotLocked = "lock.not-locked"
	// LockReasonRequired means that the lock request has no reason.
	LockReasonRequired = "lock.reason-required"
	// LockTargetNotFound means that the unit, the collection or the namespace to lock does not exist.
	LockTargetNotFound = "lock.target-not-found"
)

// lockSpecs are the codes of the manual locks.
func lockSpecs() []Spec {
	return []Spec{
		{Code: LockAlreadyLocked, Meaning: "the object already has a manual lock", Args: nil},
		{Code: LockChanged, Meaning: "the manual lock changed during the removal", Args: nil},
		{
			Code:    LockInvalidTarget,
			Meaning: "the lock request names no unit, no collection and no namespace",
			Args:    nil,
		},
		{
			Code:    LockLocked,
			Meaning: "a manual lock on the unit, its collection or its namespace refuses the request",
			Args: []Arg{
				{Name: "holder", Kind: KindIdentifier},
				{Name: "since", Kind: KindTime},
				{Name: "target", Kind: KindIdentifier},
				{Name: "targetKind", Kind: KindIdentifier},
				{Name: "unit", Kind: KindIdentifier},
			},
		},
		{Code: LockNotHolder, Meaning: "another principal holds the manual lock", Args: nil},
		{Code: LockNotLocked, Meaning: "the object has no manual lock", Args: nil},
		{Code: LockReasonRequired, Meaning: "the lock request has no reason", Args: nil},
		{
			Code:    LockTargetNotFound,
			Meaning: "the unit, the collection or the namespace to lock does not exist",
			Args:    nil,
		},
	}
}
