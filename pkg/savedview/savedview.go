// Package savedview holds saved views: a name for a query of the unit list.
//
// A saved view stores a query and never its results. A reader runs the query
// again with the access of the reader, so a view cannot show a unit that the
// reader cannot read. A saved view grants no access.
//
// This package holds the types, the Store port and the Service that adds the
// fence, the ID and the audit entry to a write. The SQL adapters live in
// pkg/store. The HTTP routes live in the inventory API.
package savedview

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

const (
	// MaxLabelFilters is the largest number of label filters in one query. It
	// is the limit of the unit list.
	MaxLabelFilters = inventory.MaxLabelFilters
	// MaxReferenceFilters is the largest number of reference filters in one
	// query. It is the limit of the unit list.
	MaxReferenceFilters = inventory.MaxReferenceFilters
	// MaxLabelKeyLen is the largest length of a label key, in bytes.
	MaxLabelKeyLen = inventory.MaxLabelKeyLen
	// MaxLabelValueLen is the largest length of a label value, in characters.
	MaxLabelValueLen = inventory.MaxLabelValueLen
	// MaxNameLen is the largest length of the name of a view, in characters.
	MaxNameLen = 128
	// MaxPerOwner is the largest number of views that one owner has in one
	// namespace.
	MaxPerOwner = 100
	// MaxPerNamespace is the largest number of views in one namespace, for all
	// owners together.
	MaxPerNamespace = 10000
	// MaxBodyBytes is the largest request body of a create call, in bytes.
	MaxBodyBytes = 16 << 10
	// maxOwnerLen is the largest length of an owner, in bytes. It is the
	// largest length of a principal ID.
	maxOwnerLen = 64
)

// idPattern is the form of a view ID: the prefix and 32 lowercase hex digits.
var idPattern = regexp.MustCompile(`^view-[0-9a-f]{32}$`)

var (
	// ErrInvalid reports a view, an ID or a query that fails validation.
	ErrInvalid = errors.New("saved view is not valid")
	// ErrNotFound reports that no view with the ID is visible to the caller. A
	// view of another owner gives the same error as an unknown ID.
	ErrNotFound = errors.New("saved view not found")
	// ErrExists reports that the owner already has a view with the name in the
	// namespace.
	ErrExists = errors.New("saved view name already used")
	// ErrLimit reports that the owner already has MaxPerOwner views in the
	// namespace, or that the namespace already has MaxPerNamespace views.
	ErrLimit = errors.New("saved view limit reached")
)

// ID is the stable, opaque identifier of a view, for example
// "view-0123456789abcdef0123456789abcdef". It is unique in the installation.
type ID string

// Validate checks the form of id.
func (id ID) Validate() error {
	if !idPattern.MatchString(string(id)) {
		return fmt.Errorf("%w: an ID is view- and 32 lowercase hex digits", ErrInvalid)
	}

	return nil
}

// Label is one key and value that a unit must have.
type Label struct {
	Key   string
	Value string
}

// Query is the filter of a unit list that a view stores. A unit matches when it
// has each label and, if the query names references, when its reference is one
// of them. The query has the same meaning as the label and reference query
// values of the unit list, and no other. It holds no result and no unit ID.
//
// A reference holds the tool of the unit. A unit that moves to another tool no
// longer matches its reference, and a label filter still matches it.
//
// The unit list accepts a label or a reference that it receives twice, so a
// query does too. A repeated reference selects the same unit once.
type Query struct {
	// Labels are the labels that a unit must have.
	Labels []Label
	// References are unit references in the namespace of the view. The list is
	// empty when the query selects no unit by reference.
	References []string
}

// Validate checks q against an allowlist. It refuses a query; it never repairs
// one. ns is the namespace of the view: a reference must name it.
func (q Query) Validate(ns namespace.Name) error {
	if err := validateLabels(q.Labels); err != nil {
		return err
	}

	return validateReferences(q.References, ns)
}

func validateLabels(labels []Label) error {
	if len(labels) > MaxLabelFilters {
		return fmt.Errorf("%w: send at most %d labels", ErrInvalid, MaxLabelFilters)
	}

	for i, l := range labels {
		if !inventory.ValidLabelKey(l.Key) {
			return fmt.Errorf(
				"%w: label %d: the key has 1 to %d characters: letters, digits and . _ : / -",
				ErrInvalid, i+1, MaxLabelKeyLen,
			)
		}

		if !inventory.ValidLabelValue(l.Value) {
			return fmt.Errorf(
				"%w: label %d: the value has at most %d characters and no control character",
				ErrInvalid, i+1, MaxLabelValueLen,
			)
		}
	}

	return nil
}

func validateReferences(refs []string, ns namespace.Name) error {
	if len(refs) > MaxReferenceFilters {
		return fmt.Errorf("%w: send at most %d references", ErrInvalid, MaxReferenceFilters)
	}

	for i, ref := range refs {
		// Defense in depth: the parser of the core keeps a unit name opaque and
		// accepts a control character. A view is stored text that the server
		// later logs and shows, so this check stays and does not rely on the
		// parser.
		if !utf8.ValidString(ref) || inventory.HasControl(ref) {
			return fmt.Errorf("%w: reference %d: use a unit reference", ErrInvalid, i+1)
		}

		obj, err := identity.ParseObject(ref)
		if err != nil || !obj.IsUnit() {
			return fmt.Errorf("%w: reference %d: use a unit reference", ErrInvalid, i+1)
		}

		if obj.Namespace() != ns {
			return fmt.Errorf("%w: reference %d: it must name the namespace of the view", ErrInvalid, i+1)
		}
	}

	return nil
}

// View is a saved view: a named query in one namespace. Owner is the ID of the
// principal that made it, in the form "prn-...". The ID never changes and the
// installation never uses it again, so a new principal with the same subject
// is a different owner. A view whose owner is not a principal of the
// installation matches no reader. A first version has personal views only, so
// the owner is the only reader. A view has no update time, because a first version
// has no call that changes a view.
type View struct {
	CreatedAt time.Time
	ID        ID
	Namespace namespace.Name
	Name      string
	Owner     string
	Query     Query
}

// Validate checks v against an allowlist.
func (v View) Validate() error {
	if err := v.ID.Validate(); err != nil {
		return err
	}

	if !validName(v.Name) {
		return fmt.Errorf(
			"%w: the name has 1 to %d characters, no control or hidden character and no space at the ends",
			ErrInvalid, MaxNameLen,
		)
	}

	// Defense in depth: the handler takes the owner from the signed-in
	// principal. This check stays so that a new caller of the store cannot
	// store an owner that is not a principal ID, for example a subject that
	// another principal can have later. It does not rely on the handler or on
	// the identity provider.
	if !ValidOwner(v.Owner) {
		return fmt.Errorf("%w: the view has no valid owner", ErrInvalid)
	}

	return v.Query.Validate(v.Namespace)
}

// ValidOwner reports whether s is the ID of a principal: the principal prefix
// and the ID characters, at most maxOwnerLen bytes. The check does not parse
// the ID. A store uses it to refuse a cleanup for an owner that cannot be a
// principal.
func ValidOwner(s string) bool {
	return len(s) > len(principal.IDPrefix) && strings.HasPrefix(s, principal.IDPrefix) && ident.Valid(s, maxOwnerLen)
}

// validName reports whether s is a view name: 1 to MaxNameLen characters of
// valid UTF-8, with no hidden character (see audit.IsHiddenRune) and no space
// at either end.
func validName(s string) bool {
	if s == "" || !utf8.ValidString(s) || utf8.RuneCountInString(s) > MaxNameLen || hasHidden(s) {
		return false
	}

	first, _ := utf8.DecodeRuneInString(s)
	last, _ := utf8.DecodeLastRuneInString(s)

	return !unicode.IsSpace(first) && !unicode.IsSpace(last)
}

// hasHidden reports whether s holds a control, format, line separator or
// paragraph separator character: the set that the audit log refuses.
func hasHidden(s string) bool {
	return strings.ContainsFunc(s, audit.IsHiddenRune)
}
