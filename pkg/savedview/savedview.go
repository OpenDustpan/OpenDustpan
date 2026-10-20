// Package savedview holds saved views: a name for a query of the unit list.
//
// A saved view stores a query and never its results. A reader runs the query
// again with the access of the reader, so a view cannot show a unit that the
// reader cannot read. A saved view grants no access.
//
// This package holds the types and the port only. It has no adapter, no route
// and no SQL yet.
package savedview

import (
	"errors"
	"fmt"
	"regexp"
	"time"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

const (
	// MaxLabelFilters is the largest number of label filters in one query. It
	// is the limit of the unit list.
	MaxLabelFilters = 10
	// MaxReferenceFilters is the largest number of reference filters in one
	// query. It is the limit of the unit list.
	MaxReferenceFilters = 20
	// MaxLabelKeyLen is the largest length of a label key, in bytes.
	MaxLabelKeyLen = 128
	// MaxLabelValueLen is the largest length of a label value, in characters.
	MaxLabelValueLen = 256
	// MaxNameLen is the largest length of the name of a view, in characters.
	MaxNameLen = 128
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
	// ErrLimit reports that the owner already has the largest number of views
	// in the namespace.
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
		if !validLabelKey(l.Key) {
			return fmt.Errorf(
				"%w: label %d: the key has 1 to %d characters: letters, digits and . _ : / -",
				ErrInvalid, i+1, MaxLabelKeyLen,
			)
		}

		if !validLabelValue(l.Value) {
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
		if !utf8.ValidString(ref) || hasControl(ref) {
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

// View is a saved view: a named query in one namespace. Owner is the subject of
// the principal that made it. A first version has personal views only, so the
// owner is the only reader.
type View struct {
	CreatedAt time.Time
	UpdatedAt time.Time
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
			"%w: the name has 1 to %d characters, no control character and no space at the ends",
			ErrInvalid, MaxNameLen,
		)
	}

	if v.Owner == "" || !utf8.ValidString(v.Owner) || hasControl(v.Owner) {
		return fmt.Errorf("%w: the view has no valid owner", ErrInvalid)
	}

	return v.Query.Validate(v.Namespace)
}

// validName reports whether s is a view name: 1 to MaxNameLen characters of
// valid UTF-8, with no control character and no space at either end.
func validName(s string) bool {
	if s == "" || !utf8.ValidString(s) || utf8.RuneCountInString(s) > MaxNameLen || hasControl(s) {
		return false
	}

	first, _ := utf8.DecodeRuneInString(s)
	last, _ := utf8.DecodeLastRuneInString(s)

	return !unicode.IsSpace(first) && !unicode.IsSpace(last)
}

func hasControl(s string) bool {
	for _, c := range s {
		if unicode.IsControl(c) {
			return true
		}
	}

	return false
}

// validLabelKey reports whether s is a label key: 1 to MaxLabelKeyLen letters,
// digits and the characters . _ : / -.
func validLabelKey(s string) bool {
	if s == "" || len(s) > MaxLabelKeyLen {
		return false
	}

	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.', c == '_', c == ':', c == '/', c == '-':
		default:
			return false
		}
	}

	return true
}

// validLabelValue reports whether s is a label value: 0 to MaxLabelValueLen
// characters of valid UTF-8, with no control character.
func validLabelValue(s string) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) <= MaxLabelValueLen && !hasControl(s)
}
