package clientgen

import (
	"errors"
	"fmt"
	"regexp"
)

// ErrVersion means that the version of a client does not follow the version of
// the description, or that a version is not MAJOR.MINOR.PATCH.
var ErrVersion = errors.New("clientgen: client version does not follow the description version")

// GeneratorVersion is the version of the generator. It changes when the
// output for the same description changes.
const GeneratorVersion = "0.1.0"

var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$`)

// CheckClientVersion checks that the client version takes the major and minor
// parts of the description version. The patch part of the client is its own.
func CheckClientVersion(descriptionVersion, clientVersion string) error {
	d := versionPattern.FindStringSubmatch(descriptionVersion)
	if d == nil {
		return fmt.Errorf("%w: description version %q is not MAJOR.MINOR.PATCH", ErrVersion, descriptionVersion)
	}

	c := versionPattern.FindStringSubmatch(clientVersion)
	if c == nil {
		return fmt.Errorf("%w: client version %q is not MAJOR.MINOR.PATCH", ErrVersion, clientVersion)
	}

	if d[1] != c[1] || d[2] != c[2] {
		return fmt.Errorf("%w: description %s gives client %s.%s.x, not %s",
			ErrVersion, descriptionVersion, d[1], d[2], clientVersion)
	}

	return nil
}

// ClientVersion returns the version of a client: the major and minor parts of
// the description version, and the patch part of the client.
func ClientVersion(descriptionVersion string, patch int) (string, error) {
	d := versionPattern.FindStringSubmatch(descriptionVersion)
	if d == nil {
		return "", fmt.Errorf("%w: description version %q is not MAJOR.MINOR.PATCH", ErrVersion, descriptionVersion)
	}

	if patch < 0 {
		return "", fmt.Errorf("%w: patch %d is below 0", ErrVersion, patch)
	}

	return fmt.Sprintf("%s.%s.%d", d[1], d[2], patch), nil
}
