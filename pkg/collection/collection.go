// Package collection models collections: named sets of units in one
// namespace (ADR 51).
//
// A collection is soft. It has no home region and no fence of its own. Its
// writes carry the fence of its namespace. Each unit is in exactly one
// collection, collections do not nest, and no default collection exists. A
// unit with no collection is readable. The refusal of a write to such a unit
// is not in this package yet: PR 2 adds it (ADR 51, decision 5).
//
// The package is tool-neutral: a unit is named by its tool, its namespace and
// the tool's own name, and the package never parses that name.
package collection

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sentinel errors returned by this package and by Store implementations.
var (
	// ErrNotFound reports that a collection or a unit does not exist.
	ErrNotFound = errors.New("collection not found: create it with " +
		"POST /api/dustpan/v1/namespaces/{namespace}/collections")
	// ErrAlreadyExists reports that a collection with the same name exists,
	// without regard to case.
	ErrAlreadyExists = errors.New("collection already exists")
	// ErrInvalidName reports a collection name that fails validation (ADR 61,
	// consequences: a valid TFE project name).
	ErrInvalidName = errors.New("invalid collection name")
	// ErrNotEmpty reports a delete of a collection that still holds units.
	ErrNotEmpty = errors.New("collection holds units: move each unit to another collection " +
		"with PUT /api/inventory/unit-collection first")
	// ErrCollectionChanged reports a move whose expected source collection is
	// no longer the collection of the unit. The caller reads again and retries.
	ErrCollectionChanged = errors.New("unit collection changed: read it again and retry")
	// ErrMoveUnchecked reports a move that names no stable ID for its target
	// collection. The caller checks the target, then passes its ID in the move.
	ErrMoveUnchecked = errors.New("move names no checked target collection ID")
	// ErrUnitNotFound reports a unit reference that the inventory does not hold.
	ErrUnitNotFound = errors.New("unit not found in the inventory")
	// ErrNoCollection reports a unit that is in no collection.
	ErrNoCollection = errors.New("unit has no collection: put it into one " +
		"with PUT /api/inventory/unit-collection")
)

// Name length limits from ADR 61: 3 to 40 characters.
const (
	minNameLength = 3
	maxNameLength = 40
)

// Name is a validated collection name.
type Name string

// ParseName validates s as a collection name. The name has 3 to 40 characters
// of ASCII letters, digits, '-', '_' and inner spaces. It starts and ends with
// a letter or a digit. Two names that differ only in case are the same name,
// so the store compares them with Key.
func ParseName(s string) (Name, error) {
	if n := utf8.RuneCountInString(s); n < minNameLength || n > maxNameLength {
		return "", fmt.Errorf("%w: %q must have 3 to 40 characters", ErrInvalidName, s)
	}

	for i, r := range s {
		if err := checkNameRune(s, i, r); err != nil {
			return "", err
		}
	}

	return Name(s), nil
}

// checkNameRune reports whether the rune r at byte offset i of name s is
// allowed. A separator may not start or end the name.
func checkNameRune(s string, i int, r rune) error {
	switch {
	case r < utf8.RuneSelf && (unicode.IsLetter(r) || unicode.IsDigit(r)):
		return nil
	case r == '-' || r == '_' || r == ' ':
		if i == 0 || i == len(s)-1 {
			return fmt.Errorf("%w: %q must start and end with a letter or digit", ErrInvalidName, s)
		}

		return nil
	default:
		return fmt.Errorf("%w: %q has a character that is not allowed", ErrInvalidName, s)
	}
}

// String returns the name as written.
func (n Name) String() string {
	return string(n)
}

// Key returns the name in the form that the store compares: lower case.
// Two names with the same key are the same collection.
func (n Name) Key() string {
	return strings.ToLower(string(n))
}

// Ref names one collection: its namespace and its name. Its string form is
// <namespace>/<collection>, for example acme/payments.
type Ref struct {
	Namespace namespace.Name
	Name      Name
}

// String returns the reference as <namespace>/<collection>.
func (r Ref) String() string {
	return r.Namespace.String() + "/" + r.Name.String()
}

// Collection is one collection of a namespace. ID is stable and random, with
// the prefix col-. A grant names a collection by its ID, so a deleted and
// re-created name does not inherit the grants of the old collection.
type Collection struct {
	CreatedAt time.Time
	ID        string
	Ref       Ref
}

// UnitRef names a unit as the inventory keys it: the tool, the namespace and
// the tool's own name. Its string form is <tool>:<namespace>/<name>, the unit
// reference of GLOSSARY.md. The name is opaque to this package.
type UnitRef struct {
	Namespace namespace.Name
	Tool      string
	Name      string
}

// String returns the unit reference as <tool>:<namespace>/<name>.
func (u UnitRef) String() string {
	return u.Tool + ":" + u.Namespace.String() + "/" + u.Name
}

// Move names one change of the collection of a unit. From is the collection
// that the caller read for the unit, or the empty name when it read none. To is
// the collection the unit moves into. ToID is the stable ID of To that the
// caller checked. The store refuses the move when To now has another ID, so a
// collection that was deleted and created again between the check and the
// write is never the target.
type Move struct {
	Unit UnitRef
	From Name
	To   Ref
	ToID string
}

// Store is the persistence port for collections and for the collection of each
// unit. Every write checks the fence and appends its audit entry in the same
// transaction, so a write that cannot be audited is not made (ADR 60). Every
// adapter runs the conformance suite in pkg/collection/collectiontest.
type Store interface {
	// CreateCollection persists c and appends e. It returns ErrAlreadyExists
	// when a collection with the same key exists, and namespace.ErrFenced when
	// the fence is stale.
	CreateCollection(ctx context.Context, c Collection, fence namespace.Fence, e audit.Entry) error
	// GetCollection returns the collection ref, or ErrNotFound.
	GetCollection(ctx context.Context, ref Ref) (Collection, error)
	// ListCollections returns the collections of ns ordered by name.
	ListCollections(ctx context.Context, ns namespace.Name) ([]Collection, error)
	// DeleteCollection removes the collection ref and appends e. It returns
	// ErrNotEmpty when a live unit is still in it. The memberships of the
	// soft-deleted units of the collection are removed in the same transaction,
	// so a deleted unit does not keep the collection. It returns ErrNotFound when
	// it does not exist.
	DeleteCollection(ctx context.Context, ref Ref, fence namespace.Fence, e audit.Entry) error
	// SetUnitCollection applies m and appends e. The store compares m.From with
	// the current collection of the unit inside the transaction, and returns
	// ErrCollectionChanged when they differ. It returns ErrUnitNotFound when the
	// inventory holds no such unit, and ErrNotFound when the target collection
	// does not exist in the namespace of the unit. A collection of another
	// namespace is never found.
	SetUnitCollection(ctx context.Context, m Move, fence namespace.Fence, e audit.Entry) error
	// UnitCollection returns the collection of the unit, or ErrNoCollection when
	// the unit has none, or ErrUnitNotFound when the inventory holds no such
	// unit.
	UnitCollection(ctx context.Context, unit UnitRef) (Ref, error)
}
