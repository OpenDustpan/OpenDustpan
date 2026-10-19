package profile

import (
	"fmt"
	"strconv"
	"strings"

	"opendustpan.org/opendustpan/internal/ident"
)

const (
	idPrefix      = "prf-"
	maxNameLength = 100
	maxIDLength   = 64
	maxSegments   = 2
)

func itoa(n int) string { return strconv.Itoa(n) }

// ID is the stable ID of a profile. It starts with "prf-".
type ID string

// NewID returns a new profile ID.
func NewID() ID { return ID(ident.NewID(idPrefix)) }

// ParseID returns s as a profile ID, or ErrInvalid.
func ParseID(s string) (ID, error) {
	rest, ok := strings.CutPrefix(s, idPrefix)
	if !ok || !ident.ValidSlug("a"+rest, maxIDLength) {
		return "", fmt.Errorf(
			"%w: ID %q must start with %q and hold lowercase letters and digits",
			ErrInvalid,
			s,
			idPrefix,
		)
	}

	return ID(s), nil
}

// Name is the name of a profile: one or two segments joined with "/". The
// first segment of two is the profile folder.
type Name string

// ParseName returns s as a profile name. A segment holds 1 or more of a-z,
// 0-9, ".", "_" and "-". A name has at most 100 characters, and does not
// start with "prf-", so a path segment is never both a name and an ID.
func ParseName(s string) (Name, error) {
	if s == "" || len(s) > maxNameLength {
		return "", fmt.Errorf("%w: name must have 1 to %d characters", ErrInvalid, maxNameLength)
	}

	segments := strings.Split(s, "/")
	if len(segments) > maxSegments {
		return "", fmt.Errorf("%w: name %q has more than 2 segments", ErrInvalid, s)
	}

	for _, seg := range segments {
		if !validSegment(seg) {
			return "", fmt.Errorf("%w: name %q: each segment holds a-z, 0-9, \".\", \"_\" and \"-\"", ErrInvalid, s)
		}
	}

	if strings.HasPrefix(s, idPrefix) {
		return "", fmt.Errorf("%w: name %q must not start with %q", ErrInvalid, s, idPrefix)
	}

	return Name(s), nil
}

func validSegment(seg string) bool {
	if seg == "" {
		return false
	}

	for i := range len(seg) {
		c := seg[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '.' && c != '_' && c != '-' {
			return false
		}
	}

	return true
}

// Folder returns the first segment of a two-segment name, else "".
func (n Name) Folder() string {
	f, _, ok := strings.Cut(string(n), "/")
	if !ok {
		return ""
	}

	return f
}

// String returns the name.
func (n Name) String() string { return string(n) }

// Ref names a profile by ID or by name. Exactly one field is set.
type Ref struct {
	ID   ID
	Name Name
}

// ParseRef reads a path segment: a profile ID when it starts with "prf-",
// else a name.
func ParseRef(s string) (Ref, error) {
	if strings.HasPrefix(s, idPrefix) {
		id, err := ParseID(s)
		if err != nil {
			return Ref{}, err
		}

		return RefByID(id), nil
	}

	n, err := ParseName(s)
	if err != nil {
		return Ref{}, err
	}

	return RefByName(n), nil
}

// RefByID returns the reference to the profile with the ID id.
func RefByID(id ID) Ref { return Ref{ID: id, Name: ""} }

// RefByName returns the reference to the profile called name.
func RefByName(name Name) Ref { return Ref{ID: "", Name: name} }

// String returns the ID or the name.
func (r Ref) String() string {
	if r.ID != "" {
		return string(r.ID)
	}

	return string(r.Name)
}
