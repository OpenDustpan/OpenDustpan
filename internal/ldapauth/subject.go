package ldapauth

import (
	"errors"
	"fmt"
	"strings"

	"github.com/go-ldap/ldap/v3"

	"opendustpan.org/opendustpan/pkg/session"
)

// errSubject reports a subject attribute that the method cannot read: it
// is missing, it has more than one value, or its value has the wrong form.
var errSubject = errors.New("the subject attribute is not valid")

// guidBytes is the length of an objectGUID and of a UUID, in bytes.
const guidBytes = 16

// GUIDText returns the GUID text that Active Directory shows for the 16
// bytes b of an objectGUID, in lowercase: the first three groups from the
// bytes in little-endian order, the last two groups in the order of the
// bytes. It returns an error when b does not have 16 bytes.
func GUIDText(b []byte) (string, error) {
	if len(b) != guidBytes {
		return "", fmt.Errorf("%w: an objectGUID has %d bytes, not %d", errSubject, len(b), guidBytes)
	}

	order := []int{3, 2, 1, 0, -1, 5, 4, -1, 7, 6, -1, 8, 9, -1, 10, 11, 12, 13, 14, 15}

	var s strings.Builder

	s.Grow(len(uuidPattern))

	for _, i := range order {
		if i < 0 {
			s.WriteByte('-')

			continue
		}

		s.WriteByte(hexDigits[b[i]>>4])
		s.WriteByte(hexDigits[b[i]&0x0f])
	}

	return s.String(), nil
}

// uuidPattern is the form of UUID text: "x" is a hex digit.
const uuidPattern = "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"

// UUIDText returns the entryUUID text v (RFC 4530) in lowercase. It returns
// an error when v is not the text form of a UUID.
func UUIDText(v []byte) (string, error) {
	if len(v) != len(uuidPattern) {
		return "", fmt.Errorf("%w: an entryUUID is not UUID text", errSubject)
	}

	for i, c := range v {
		if uuidPattern[i] == '-' && c != '-' || uuidPattern[i] == 'x' && !isHex(c) {
			return "", fmt.Errorf("%w: an entryUUID is not UUID text", errSubject)
		}
	}

	return strings.ToLower(string(v)), nil
}

// isHex reports whether c is a hex digit.
func isHex(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// subjectAttribute returns the name of the attribute that gives the
// subject.
func (m *Method) subjectAttribute() string {
	switch m.subject.Kind {
	case SubjectEntryUUID:
		return "entryUUID"
	case SubjectObjectGUID:
		return "objectGUID"
	case SubjectAttribute:
		return m.subject.Attribute
	default:
		// Fail closed: NewMethod refuses another kind, and no entry has an
		// attribute with no name.
		return ""
	}
}

// subjectOf returns the subject of the entry e in its fixed text form.
func (m *Method) subjectOf(e *ldap.Entry) (string, error) {
	values := e.GetEqualFoldRawAttributeValues(m.subjectAttribute())
	if len(values) != 1 {
		return "", fmt.Errorf("%w: the entry has %d values of the subject attribute, not 1", errSubject,
			len(values))
	}

	v := values[0]

	var (
		s   string
		err error
	)

	switch m.subject.Kind {
	case SubjectEntryUUID:
		s, err = UUIDText(v)
	case SubjectObjectGUID:
		s, err = GUIDText(v)
	case SubjectAttribute:
		s = string(v)
	default:
		// Fail closed: NewMethod refuses another kind.
		err = fmt.Errorf("%w: the subject kind is not known", errSubject)
	}

	if err != nil {
		return "", err
	}

	// Defense in depth: the sign-in service checks the subject of each
	// Assertion too. This check stays so that a binary value or a value
	// over the limit never leaves the method.
	if s == "" || len(s) > session.MaxAssertionSubject || !validText(s) {
		return "", fmt.Errorf("%w: the subject must have 1 to %d bytes of text", errSubject,
			session.MaxAssertionSubject)
	}

	return s, nil
}
