package job

import (
	"fmt"
	"path"
	"strings"
	"unicode"
)

// Limits of a code location, in bytes. They bound the job record.
const (
	maxLocationField = 255
	maxDirectory     = 512
	minRevision      = 7
	maxRevision      = 64
)

// IsZero reports whether no field of l is set.
func (l Location) IsZero() bool {
	return l == Location{Connection: "", Repository: "", Ref: "", Directory: "", Revision: ""}
}

// Validate returns an error that wraps ErrInvalid for a code location that a
// runner must not use. A runner job needs a repository and a ref. The
// directory is relative and clean and stays in the checkout. The revision is
// empty or a hex commit ID. No field holds a control character, a space or a
// backslash, and a repository, a ref or a connection never starts with '-',
// so that no value reads as an option of git. Validate refuses a value and
// never changes it.
//
// Defense in depth: the planner of the server and the runner check the same
// fields. This check stays so that a new caller of Service.Start cannot hand
// a runner a location that another layer would refuse.
func (l Location) Validate() error {
	for _, f := range []struct{ name, value string }{
		{"repository", l.Repository}, {"ref", l.Ref}, {"connection", l.Connection},
	} {
		if err := checkLocationField(f.name, f.value, f.name != "connection"); err != nil {
			return err
		}
	}

	if err := l.validateDirectory(); err != nil {
		return err
	}

	if l.Revision == "" {
		return nil
	}

	if len(l.Revision) < minRevision || len(l.Revision) > maxRevision || !isHex(l.Revision) {
		return fmt.Errorf("%w: the revision must be a commit ID of %d to %d hex digits", ErrInvalid, minRevision,
			maxRevision)
	}

	return nil
}

// validateDirectory checks the directory of l.
func (l Location) validateDirectory() error {
	d := l.Directory
	if d == "" {
		return nil
	}

	if len(d) > maxDirectory || hasHidden(d) || strings.ContainsRune(d, '\\') || strings.HasPrefix(d, "/") ||
		path.Clean(d) != d || d == "." || d == ".." || strings.HasPrefix(d, "../") {
		return fmt.Errorf("%w: the directory must be a clean relative path in the repository", ErrInvalid)
	}

	return nil
}

// checkLocationField checks one text field of a location. A required field
// must not be empty.
func checkLocationField(name, v string, required bool) error {
	switch {
	case v == "" && required:
		return fmt.Errorf("%w: the %s of the code location is empty", ErrInvalid, name)
	case len(v) > maxLocationField:
		return fmt.Errorf("%w: the %s of the code location has more than %d bytes", ErrInvalid, name,
			maxLocationField)
	case strings.HasPrefix(v, "-"):
		return fmt.Errorf("%w: the %s of the code location cannot start with '-'", ErrInvalid, name)
	case hasHidden(v) || strings.ContainsAny(v, " \\") || strings.Contains(v, ".."):
		return fmt.Errorf("%w: the %s of the code location has a character that it cannot have", ErrInvalid, name)
	default:
		return nil
	}
}

// hasHidden reports whether s holds a control character, a space or another
// character that does not print.
func hasHidden(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return !unicode.IsPrint(r) || r == ' ' })
}

// isHex reports whether s holds lowercase hex digits only.
func isHex(s string) bool {
	return !strings.ContainsFunc(s, func(r rune) bool {
		return (r < '0' || r > '9') && (r < 'a' || r > 'f')
	})
}
