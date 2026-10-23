package errcode

// LockLocked is a request that a manual lock refuses.
const LockLocked = "lock.locked"

// lockSpecs are the codes of the manual locks.
func lockSpecs() []Spec {
	return []Spec{
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
	}
}
