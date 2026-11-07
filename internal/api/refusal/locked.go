package refusal

import (
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// errUnknownUnit is the failure of an answer for a refused unit with no
// namespace, no name, or a tool that has no word for its unit.
var errUnknownUnit = errors.New("the refused unit has no namespace, no name or no known tool")

// Locked returns the answer of lock.locked for a refusal of a manual lock.
// It reads the holder, the time, the locked object and the refused unit from
// the typed fields of the error, never from its text. lockedKind is
// namespace, collection, stack or workspace, and locked is the name of that
// object in the form namespace or namespace/name, with no tool prefix.
// refusedKind is stack or workspace, and refused is the refused unit in the
// form namespace/name. It never reads the reason of the lock: a person wrote
// it, and an argument holds no free text. When an argument has no valid value,
// it returns the answer of an internal failure with the error, never an answer
// with no code.
func Locked(e *manuallock.LockedError) (errcode.Answer, error) {
	locked, err := lockedObject(e.Lock.Target)
	if err != nil {
		return errcode.Internal(), fmt.Errorf("answer for a manual lock: %w", err)
	}

	refused, err := unitName(e.Unit.Tool, e.Unit.Namespace.String(), e.Unit.Name)
	if err != nil {
		return errcode.Internal(), fmt.Errorf("answer for a manual lock: %w", err)
	}

	a, err := errcode.New(errcode.LockLocked,
		errcode.Identifier("holder", e.Lock.Holder),
		errcode.Time("since", e.Lock.Since),
		errcode.Identifier("lockedKind", locked.kind),
		errcode.IdentifierUpTo("locked", locked.name, maxQualifiedName),
		errcode.Identifier("refusedKind", refused.kind),
		errcode.IdentifierUpTo("refused", refused.name, maxQualifiedName),
	)
	if err != nil {
		return errcode.Internal(), fmt.Errorf("answer for a manual lock: %w", err)
	}

	return a, nil
}

// maxQualifiedName is the longest value of the locked and refused arguments:
// the longest namespace name, "/", and the longest unit name. The count is in
// bytes, so it is above the count of characters that a legal value has.
const maxQualifiedName = namespace.MaxNameLength + 1 + identity.MaxUnitLength

// objectName is the kind and the name of an object in the answer of a manual
// lock.
type objectName struct {
	kind string
	name string
}

// lockedObject returns the kind and the name of the locked object of t. A
// lock on a unit takes the word of its tool, stack or workspace.
func lockedObject(t manuallock.Target) (objectName, error) {
	switch t.Kind {
	case manuallock.KindNamespace:
		return objectName{kind: string(manuallock.KindNamespace), name: t.Space.String()}, nil
	case manuallock.KindCollection:
		return objectName{kind: string(manuallock.KindCollection), name: t.Collection.String()}, nil
	case manuallock.KindUnit:
		return unitName(t.Unit.Tool, t.Unit.Namespace.String(), t.Unit.Name)
	}

	return objectName{kind: "", name: ""}, fmt.Errorf("%w: lock target kind %q", errUnknownUnit, t.Kind)
}

// unitName returns the word of tool for its unit and the name of the unit in
// the form namespace/name. It fails closed for an empty namespace, an empty
// name and a tool with no word.
func unitName(tool, ns, name string) (objectName, error) {
	if ns == "" || name == "" {
		return objectName{kind: "", name: ""}, errUnknownUnit
	}

	switch tool {
	case "pulumi":
		return objectName{kind: "stack", name: ns + "/" + name}, nil
	case "opentofu":
		return objectName{kind: "workspace", name: ns + "/" + name}, nil
	}

	return objectName{kind: "", name: ""}, fmt.Errorf("%w: tool %q", errUnknownUnit, tool)
}
