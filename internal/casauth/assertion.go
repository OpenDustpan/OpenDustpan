package casauth

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/session"
)

// assertion returns the Assertion of the answer resp of the sign-in of
// pending. It reads the subject, the names, the groups and the time of the
// authentication, and checks each against the limits of package session.
func (m *Method) assertion(resp Response, pending session.PendingRedirect) (session.Assertion, error) {
	var none session.Assertion

	subject, err := attrOrUser(resp, m.subjectAttr)
	if err != nil {
		return none, err
	}

	if !validText(subject, 1, session.MaxAssertionSubject) {
		return none, fmt.Errorf("%w: the subject must have 1 to %d bytes of UTF-8 and no control character",
			session.ErrMethodAnswer, session.MaxAssertionSubject)
	}

	person, err := m.person(resp)
	if err != nil {
		return none, err
	}

	groups, present, err := m.groups(resp)
	if err != nil {
		return none, err
	}

	authTime, known, err := m.authTime(resp, pending)
	if err != nil {
		return none, err
	}

	return session.Assertion{
		AuthTime: authTime, ProviderID: m.providerID, PrincipalID: "", Subject: subject,
		UserName: person.userName, Email: person.email, Method: session.MethodCAS, Names: person.names,
		Groups: groups, Factors: nil, EmailVerified: false, GroupsPresent: present, AuthTimeKnown: known,
	}, nil
}

// personNames are the user name, the email and the display name
// candidates of an answer.
type personNames struct {
	userName string
	email    string
	names    []string
}

// person returns the user name, the email and the display name candidates
// of resp: the display name, the user name and the email, in that order,
// when they are not empty.
func (m *Method) person(resp Response) (personNames, error) {
	var none personNames

	userName, err := attrOrUser(resp, m.userAttr)
	if err != nil {
		return none, err
	}

	name, err := attrValue(resp, m.nameAttr)
	if err != nil {
		return none, err
	}

	email, err := attrValue(resp, m.emailAttr)
	if err != nil {
		return none, err
	}

	candidates := []string{name, userName, email}
	names := make([]string, 0, len(candidates))

	for _, v := range candidates {
		if !validText(v, 0, session.MaxAssertionName) {
			return none, fmt.Errorf("%w: a name, a user name or an email must have at most %d bytes of UTF-8 "+
				"and no control character", session.ErrMethodAnswer, session.MaxAssertionName)
		}

		if v != "" {
			names = append(names, v)
		}
	}

	return personNames{userName: userName, email: email, names: names}, nil
}

// attrOrUser returns the one value of the attribute attr of resp, or the
// CAS user when attr is empty.
func attrOrUser(resp Response, attr string) (string, error) {
	if attr == "" {
		return resp.User, nil
	}

	return attrValue(resp, attr)
}

// attrValue returns the one value of the attribute attr of resp. An empty
// attr and an attribute with no value give the empty value. An attribute
// with more than one value is refused.
func attrValue(resp Response, attr string) (string, error) {
	values := resp.Attributes[attr]

	switch {
	case attr == "" || len(values) == 0:
		return "", nil
	case len(values) == 1:
		return values[0], nil
	default:
		return "", fmt.Errorf("%w: the attribute %q has more than one value", session.ErrMethodAnswer, attr)
	}
}

// groups returns the groups of resp. With groups off, it returns none and
// present false. With groups on, an answer with no groups attribute is
// session.ErrGroupsMissing, one empty value is an empty list, and each
// other value must be a group in the limits of package session.
func (m *Method) groups(resp Response) ([]string, bool, error) {
	if m.groupsAttr == "" {
		return nil, false, nil
	}

	values, ok := resp.Attributes[m.groupsAttr]

	switch {
	case !ok:
		return nil, false, fmt.Errorf("%w: the answer has no attribute %q", session.ErrGroupsMissing, m.groupsAttr)
	case len(values) == 1 && values[0] == "":
		return []string{}, true, nil
	case len(values) > session.MaxAssertionGroups:
		return nil, false, fmt.Errorf("%w: more than %d groups", session.ErrMethodAnswer, session.MaxAssertionGroups)
	}

	for _, g := range values {
		if !validText(g, 1, session.MaxAssertionGroup) {
			return nil, false, fmt.Errorf("%w: a group must have 1 to %d bytes of UTF-8 and no control character",
				session.ErrMethodAnswer, session.MaxAssertionGroup)
		}
	}

	return values, true, nil
}

// authTime returns the time of the authentication of resp. The attribute
// authenticationDate gives it. With no such attribute, a validation with
// renew=true gives the time of the validation, because the CAS server
// validates such a ticket only after a new login; a validation without
// renew gives no time. A fresh sign-in must have a time that is not before
// the start of its request, and an answer that says that the ticket is not
// from a new login is refused. A time in the future is refused.
func (m *Method) authTime(resp Response, pending session.PendingRedirect) (time.Time, bool, error) {
	if err := checkNewLogin(resp, pending); err != nil {
		return time.Time{}, false, err
	}

	at, known, err := m.reportedTime(resp, pending)
	if err != nil || !known {
		return time.Time{}, false, err
	}

	if at.After(m.now().Add(ClockSkew)) {
		return time.Time{}, false, fmt.Errorf("%w: the authentication time is in the future", session.ErrMethodAnswer)
	}

	if pending.Fresh && at.Before(pending.StartedAt.Add(-ClockSkew)) {
		return time.Time{}, false, fmt.Errorf("%w: the fresh authentication is older than its request",
			session.ErrMethodAnswer)
	}

	return at, true, nil
}

// checkNewLogin refuses the answer of a fresh sign-in that says that the
// ticket is not from a new login.
func checkNewLogin(resp Response, pending session.PendingRedirect) error {
	v := resp.Attributes[newLoginAttribute]
	if !pending.Fresh || len(v) == 0 || (len(v) == 1 && v[0] == "true") {
		return nil
	}

	// Defense in depth: the CAS server refuses a ticket of an old login for
	// a validation with renew=true, and the time check of authTime refuses
	// an old authentication. This check stays so that a server that skips
	// renew cannot pass an old login as fresh.
	return fmt.Errorf("%w: the ticket of a fresh sign-in is not from a new login", session.ErrMethodAnswer)
}

// reportedTime returns the time of the authentication that resp reports:
// the attribute authenticationDate, else the time of the validation for a
// validation with renew=true, else no time.
func (m *Method) reportedTime(resp Response, pending session.PendingRedirect) (time.Time, bool, error) {
	date, err := attrValue(resp, authDateAttribute)

	switch {
	case err != nil:
		return time.Time{}, false, err
	case date != "":
		at, parseErr := parseDate(date)

		return at, parseErr == nil, parseErr
	case pending.Fresh:
		return m.now(), true, nil
	default:
		return time.Time{}, false, nil
	}
}

// maxDate is the longest value of the attribute authenticationDate.
const maxDate = 64

// parseDate parses the value of the attribute authenticationDate: an RFC
// 3339 time, with an optional zone ID in brackets after it, as a CAS server
// writes a zoned time.
func parseDate(v string) (time.Time, error) {
	if len(v) > maxDate {
		return time.Time{}, fmt.Errorf("%w: the authentication date is longer than %d bytes", session.ErrMethodAnswer,
			maxDate)
	}

	if i := strings.IndexByte(v, '['); i > 0 && strings.HasSuffix(v, "]") {
		v = v[:i]
	}

	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: the authentication date is not an RFC 3339 time", session.ErrMethodAnswer)
	}

	return t.UTC(), nil
}

// validText reports whether s is UTF-8 with no control character and has
// minLen to maxLen bytes.
func validText(s string, minLen, maxLen int) bool {
	return len(s) >= minLen && len(s) <= maxLen && utf8.ValidString(s) &&
		!strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || (r >= 0x7f && r <= 0x9f) })
}
