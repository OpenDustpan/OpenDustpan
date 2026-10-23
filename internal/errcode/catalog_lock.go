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
				{Name: "unit", Kind: KindIdentifier},
			},
		},
	}
}

// Locked returns the answer of lock.locked for a refusal of a manual lock.
// It reads the holder, the time, the locked object and the refused unit from
// the error. The target is the bare name of the object (the namespace name,
// the collection reference or the unit reference), and targetKind says which
// of the three it is. It never reads the reason of the lock: a person wrote it, and an argument
// holds no free text.
func Locked(e *manuallock.LockedError) (Answer, error) {
	a, err := New(LockLocked,
		Identifier("holder", e.Lock.Holder),
		Time("since", e.Lock.Since),
		Identifier("target", targetName(e.Lock.Target)),
		Identifier("targetKind", string(e.Lock.Target.Kind)),
		Identifier("unit", e.Name),
	)
	if err != nil {
		return Answer{code: "", args: nil}, fmt.Errorf("answer for a manual lock: %w", err)
	}

	return a, nil
}

// targetName returns the bare name of the locked object: no prefix word and
// no text for a person.
func targetName(t manuallock.Target) string {
	switch t.Kind {
	case manuallock.KindCollection:
		return t.Collection.String()
	case manuallock.KindNamespace:
		return t.Space.String()
	case manuallock.KindUnit:
		return t.Unit.String()
	}

	return t.Unit.String()
}
