package job

import (
	"fmt"
	"net/url"
	"path"
	"strings"
	"unicode"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/source"
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
// so that no value reads as an option of git. The repository passes
// CheckRepository. Validate refuses a value and never changes it.
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

	if err := CheckRepository(l.Repository); err != nil {
		return err
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

// CheckRepository returns an error that wraps ErrInvalid for a repository of a
// code location that is neither a path, such as acme/infra, nor an https URL
// (the scheme in lower case) with a host and no user name, password, query or
// fragment. The host is a DNS name that source.AllowedHost accepts: not an IP
// address in any form, not a number that a resolver reads as an IPv4
// address, and not localhost or a name under it. A port is allowed. A path
// holds no ':' and no '@', so that it is not a URL of another scheme or an
// scp-like address with a user name. The credentials that fetch
// the code come from the credential helper of the runner pool, never from the
// repository, so a job record and its answers never hold them. The error never
// holds the repository.
func CheckRepository(r string) error {
	if !strings.Contains(r, "://") {
		if strings.ContainsAny(r, ":@") {
			return fmt.Errorf("%w: the repository of the code location is a path such as acme/infra or an https "+
				"URL, and a path cannot hold ':' or '@'", ErrInvalid)
		}

		return nil
	}

	return checkRepositoryURL(r)
}

// checkRepositoryURL checks a repository that is a URL. See CheckRepository.
func checkRepositoryURL(r string) error {
	u, err := url.Parse(r)

	switch {
	case err != nil || u == nil || !strings.HasPrefix(r, "https://") || u.Host == "" || u.Opaque != "":
		return fmt.Errorf("%w: the repository URL of the code location must be an https URL with a host",
			ErrInvalid)
	case u.User != nil:
		return fmt.Errorf("%w: the repository URL of the code location cannot hold a user name or a password: "+
			"the credential helper of the runner pool gives the credentials", ErrInvalid)
	case u.RawQuery != "" || u.ForceQuery || strings.Contains(r, "#"):
		return fmt.Errorf("%w: the repository URL of the code location cannot have a query or a fragment",
			ErrInvalid)
	// Defense in depth: the API handlers, the unit settings and the catalog
	// documents call CheckRepository on input, and Location.Validate calls it
	// again when a job starts. This check stays so that a stored location
	// never names an IP address, a number or localhost, also for a new caller.
	// It checks the name only. The server never connects to the repository:
	// the runner fetches it, and the runner must check the resolved address of
	// each connection itself.
	case !source.AllowedHost(u.Hostname()):
		return fmt.Errorf("%w: the host of the repository URL of the code location must be a DNS name, not an "+
			"IP address, a number or localhost", ErrInvalid)
	default:
		return nil
	}
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

// hasHidden reports whether s holds a hidden character (see
// audit.IsHiddenRune), a space or another character that does not print.
func hasHidden(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool {
		return audit.IsHiddenRune(r) || !unicode.IsPrint(r) || r == ' '
	})
}

// isHex reports whether s holds lowercase hex digits only.
func isHex(s string) bool {
	return !strings.ContainsFunc(s, func(r rune) bool {
		return (r < '0' || r > '9') && (r < 'a' || r > 'f')
	})
}
