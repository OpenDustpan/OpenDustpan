package refusal

import (
	"fmt"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/manuallock"
)

// Locked returns the answer of lock.locked for a refusal of a manual lock.
// It reads the holder, the time, the locked object and the refused unit from
// the error. The target is the bare name of the object (the namespace name,
// the collection reference or the unit reference), and targetKind says which
// of the three it is. It never reads the reason of the lock: a person wrote it, and an argument
// holds no free text. When the catalog refuses the answer, it returns the
// answer of an internal failure with the error, never an answer with no code.
func Locked(e *manuallock.LockedError) (errcode.Answer, error) {
	a, err := errcode.New(errcode.LockLocked,
		errcode.Identifier("holder", e.Lock.Holder),
		errcode.Time("since", e.Lock.Since),
		errcode.Identifier("target", targetName(e.Lock.Target)),
		errcode.Identifier("targetKind", string(e.Lock.Target.Kind)),
		errcode.Identifier("unit", e.Name),
	)
	if err != nil {
		return errcode.Internal(), fmt.Errorf("answer for a manual lock: %w", err)
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
