package manage

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Errors of the version pin.
var (
	// ErrVersionOutsidePin means that the server version is a version number
	// outside the pin of the client.
	ErrVersionOutsidePin = errors.New("manage: server version is outside the pin")
	// ErrInvalidVersion means that a version or a pin is not in the accepted
	// form. The server version counts too: a client refuses a server that
	// sends no version number.
	ErrInvalidVersion = errors.New("manage: version is not valid")
)

// errNumber is the reason for a version number that is not valid. A caller
// wraps it with ErrInvalidVersion.
var errNumber = errors.New("a number is empty, has a leading zero, is too large or has a character that is not a digit")

// versionParts is the count of numbers in a version: major, minor and patch.
const versionParts = 3

// maxVersionText is the longest text that ParseVersion reads. The server
// version comes from outside the process.
const maxVersionText = 64

// Version is a semantic version: major, minor, patch and an optional
// pre-release text.
type Version struct {
	Major uint64
	Minor uint64
	Patch uint64
	// Pre is the pre-release text without the leading "-", for example
	// "rc.1". It is empty for a release.
	Pre string
}

// ParseVersion reads "1.2.3" or "1.2.3-rc.1", with an optional leading "v".
// It ignores build metadata after "+". It refuses any other form.
func ParseVersion(text string) (Version, error) {
	var zero Version

	if len(text) > maxVersionText {
		return zero, fmt.Errorf("%w: more than %d characters", ErrInvalidVersion, maxVersionText)
	}

	rest := strings.TrimPrefix(text, "v")
	rest, _, _ = strings.Cut(rest, "+")
	core, pre, hasPre := strings.Cut(rest, "-")

	numbers := strings.Split(core, ".")
	if len(numbers) != versionParts {
		return zero, fmt.Errorf("%w: %q has no major, minor and patch number", ErrInvalidVersion, text)
	}

	var parsed [versionParts]uint64

	for i, number := range numbers {
		value, err := parseNumber(number)
		if err != nil {
			return zero, fmt.Errorf("%w: %q: %w", ErrInvalidVersion, text, err)
		}

		parsed[i] = value
	}

	if hasPre && !validPre(pre) {
		return zero, fmt.Errorf("%w: %q has a pre-release text that is not valid", ErrInvalidVersion, text)
	}

	return Version{Major: parsed[0], Minor: parsed[1], Patch: parsed[2], Pre: pre}, nil
}

// parseNumber reads digits only, with no leading zero.
func parseNumber(text string) (uint64, error) {
	if text == "" || (len(text) > 1 && text[0] == '0') {
		return 0, errNumber
	}

	value, err := strconv.ParseUint(text, 10, 64)
	if err != nil || strings.TrimLeft(text, "0123456789") != "" {
		return 0, errNumber
	}

	return value, nil
}

// validPre reports whether text is dot-separated identifiers of letters,
// digits and "-", none empty, with no leading zero in a numeric identifier.
func validPre(text string) bool {
	for id := range strings.SplitSeq(text, ".") {
		if !validIdentifier(id) {
			return false
		}
	}

	return true
}

func validIdentifier(id string) bool {
	if id == "" {
		return false
	}

	digits := strings.TrimLeft(id, "0123456789")
	if digits == "" {
		return len(id) == 1 || id[0] != '0'
	}

	for _, char := range digits {
		letter := (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z')
		if !letter && !strings.ContainsRune("0123456789-", char) {
			return false
		}
	}

	return true
}

// String returns the version in the form that ParseVersion reads.
func (v Version) String() string {
	text := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		text += "-" + v.Pre
	}

	return text
}

// Compare returns -1, 0 or 1 by the precedence rules of semantic versioning:
// a pre-release is lower than its release.
func (v Version) Compare(other Version) int {
	for _, pair := range [][2]uint64{{v.Major, other.Major}, {v.Minor, other.Minor}, {v.Patch, other.Patch}} {
		if pair[0] != pair[1] {
			return compareNumbers(pair[0], pair[1])
		}
	}

	switch {
	case v.Pre == other.Pre:
		return 0
	case v.Pre == "":
		return 1
	case other.Pre == "":
		return -1
	}

	return comparePre(v.Pre, other.Pre)
}

func compareNumbers(a, b uint64) int {
	if a < b {
		return -1
	}

	return 1
}

// comparePre compares two non-empty pre-release texts identifier by
// identifier. A numeric identifier is lower than a text identifier, and a
// shorter list is lower when all its identifiers are equal.
func comparePre(a, b string) int {
	left, right := strings.Split(a, "."), strings.Split(b, ".")

	for i := 0; i < len(left) && i < len(right); i++ {
		if left[i] == right[i] {
			continue
		}

		leftNum, leftErr := strconv.ParseUint(left[i], 10, 64)
		rightNum, rightErr := strconv.ParseUint(right[i], 10, 64)

		switch {
		case leftErr == nil && rightErr == nil:
			return compareNumbers(leftNum, rightNum)
		case leftErr == nil:
			return -1
		case rightErr == nil:
			return 1
		}

		return strings.Compare(left[i], right[i])
	}

	return compareNumbers(uint64(len(left)), uint64(len(right)))
}

// Pin is the range of server versions that a client works with: Min is
// included and Max is not. The zero Pin holds no version, so it refuses every
// server.
type Pin struct {
	Min Version
	Max Version
}

// ParsePin reads ">=MIN <MAX", for example ">=0.12.0 <0.13.0". It refuses a
// range that holds no version.
func ParsePin(text string) (Pin, error) {
	fields := strings.Fields(text)
	if len(fields) != 2 || !strings.HasPrefix(fields[0], ">=") || !strings.HasPrefix(fields[1], "<") {
		return Pin{}, fmt.Errorf("%w: pin %q is not in the form \">=MIN <MAX\"", ErrInvalidVersion, text)
	}

	lower, err := ParseVersion(strings.TrimPrefix(fields[0], ">="))
	if err != nil {
		return Pin{}, fmt.Errorf("pin %q: %w", text, err)
	}

	upper, err := ParseVersion(strings.TrimPrefix(fields[1], "<"))
	if err != nil {
		return Pin{}, fmt.Errorf("pin %q: %w", text, err)
	}

	if lower.Compare(upper) >= 0 {
		return Pin{}, fmt.Errorf("%w: pin %q holds no version", ErrInvalidVersion, text)
	}

	return Pin{Min: lower, Max: upper}, nil
}

// String returns the pin in the form that ParsePin reads.
func (p Pin) String() string {
	return ">=" + p.Min.String() + " <" + p.Max.String()
}

// Contains reports whether the version is in the pin. A pre-release version is
// in the pin only when the lower bound of the pin is a pre-release too, so a
// release candidate of the next minor version does not pass a pin that stops
// before it.
func (p Pin) Contains(v Version) bool {
	if v.Pre != "" && p.Min.Pre == "" {
		return false
	}

	return v.Compare(p.Min) >= 0 && v.Compare(p.Max) < 0
}

// CheckPin returns nil when the server version is in the pin. Otherwise it
// returns ErrVersionOutsidePin or ErrInvalidVersion, wrapped with a message
// that names the server version and the pin. It fails closed: a server
// version that is not a version number is refused.
func CheckPin(serverVersion string, pin Pin) error {
	version, err := ParseVersion(serverVersion)
	if err != nil {
		return fmt.Errorf("%w: the server sent the version %q, and this client needs a server in the pin %q."+
			" Use a server that sends a version number", ErrInvalidVersion, shorten(serverVersion), pin.String())
	}

	if !pin.Contains(version) {
		return fmt.Errorf("%w: the server version is %s and the pin of this client is %q."+
			" Use a client for server %s, or a server in the pin", ErrVersionOutsidePin, version, pin.String(), version)
	}

	return nil
}

// shorten cuts a text from outside the process to a length that is safe to
// show. The %q verb in the caller escapes control characters.
func shorten(text string) string {
	const limit = 32

	if len(text) <= limit {
		return text
	}

	return text[:limit] + "..."
}
