package errcode

import (
	"fmt"

	"opendustpan.org/opendustpan/pkg/manuallock"
)

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
			},
		},
	}
}

// Locked returns the answer of lock.locked for a refusal of a manual lock.
// It reads the holder, the time and the locked object from the error. It
// never reads the reason of the lock: a person wrote it, and an argument
// holds no free text.
func Locked(e *manuallock.LockedError) (Answer, error) {
	a, err := New(LockLocked,
		Identifier("holder", e.Lock.Holder),
		Time("since", e.Lock.Since),
		Identifier("target", e.Lock.Target.String()),
		Identifier("targetKind", string(e.Lock.Target.Kind)),
	)
	if err != nil {
		return Answer{code: "", args: nil}, fmt.Errorf("answer for a manual lock: %w", err)
	}

	return a, nil
}
