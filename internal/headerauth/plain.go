package headerauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"strings"

	"opendustpan.org/opendustpan/pkg/session"
)

// fromPlain returns the Assertion of the plain mode.
func (m *Method) fromPlain(f session.RequestFacts, user string) (session.Assertion, error) {
	var none session.Assertion

	// Layer 2: the second proof of the proxy.
	if !m.secretMatches(f.Headers[m.secretHeader]) {
		return none, fmt.Errorf("%w: the shared secret of the proxy is missing or wrong", session.ErrUntrustedSource)
	}

	// The subject is used byte for byte. It is never case-folded or
	// normalized, so two subjects that only look the same stay two persons.
	if !subjectPattern.MatchString(user) {
		return none, fmt.Errorf("%w: the subject has characters outside the allowlist or a wrong length",
			session.ErrMethodInput)
	}

	a := headerAssertion(m.providerID, user)
	a.UserName = user

	if m.groupsHeader == "" {
		return a, nil
	}

	values, ok := f.Headers[m.groupsHeader]
	if !ok || len(values) == 0 {
		return none, fmt.Errorf("%w: the groups header is missing", session.ErrGroupsMissing)
	}

	if len(values) != 1 {
		return none, fmt.Errorf("%w: the groups header comes more than once", session.ErrMethodInput)
	}

	groups, err := parseGroupsHeader(values[0])
	if err != nil {
		return none, err
	}

	a.Groups, a.GroupsPresent = groups, true

	return a, nil
}

// secretMatches compares the one value of the secret header with the shared
// secret in constant time. It compares the SHA-256 sums, so the time does
// not tell the length of the secret either.
func (m *Method) secretMatches(values []string) bool {
	if len(values) != 1 || len(values[0]) > MaxSecretBytes {
		return false
	}

	got := sha256.Sum256([]byte(values[0]))

	return subtle.ConstantTimeCompare(got[:], m.secretSum[:]) == 1
}

// parseGroupsHeader parses a comma-separated groups header. An empty header
// is an empty list. It returns ErrMethodInput for a header of the wrong form
// and ErrMethodAnswer for more groups than the limit.
func parseGroupsHeader(v string) ([]string, error) {
	if len(v) > maxGroupsHeader || !session.IsSafeText(v) {
		return nil, fmt.Errorf("%w: the groups header is too long or has a character that is not graphic",
			session.ErrMethodInput)
	}

	if strings.TrimSpace(v) == "" {
		return []string{}, nil
	}

	parts := strings.Split(v, ",")
	if len(parts) > session.MaxAssertionGroups {
		return nil, fmt.Errorf("%w: more than %d groups", session.ErrMethodAnswer, session.MaxAssertionGroups)
	}

	out := make([]string, 0, len(parts))

	for _, p := range parts {
		g := strings.TrimSpace(p)
		if g == "" || len(g) > session.MaxAssertionGroup {
			return nil, fmt.Errorf("%w: a group of the groups header is empty or too long", session.ErrMethodInput)
		}

		out = append(out, g)
	}

	return out, nil
}

// printableASCII reports whether s holds only printable ASCII.
func printableASCII(s string) bool {
	for i := range len(s) {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}

	return true
}
