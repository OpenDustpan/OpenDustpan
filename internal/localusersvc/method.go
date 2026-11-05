package localusersvc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/session"
)

// PasswordMethod is the password sign-in of the local users as a
// session.PasswordMethod. It checks the credentials at its boundary, calls
// the sign-in of the service with its lockout by name, and translates each
// error of the service into an error of package session. It returns the
// Assertion of the sign-in and never makes a session.
//
// The provider ID is "local", and the subject of the Assertion and of
// BindGate.BeforeBind is the ID of the local user. A local user has no
// groups, and its directory never reports a person as disabled: a disabled
// principal is refused as a wrong password is.
type PasswordMethod struct {
	svc  *Service
	base audit.Entry
}

var _ session.PasswordMethod = (*PasswordMethod)(nil)

// PasswordMethod returns the password method of the service for one request.
// base is the entry of the caller: the source address, the user agent and
// the request ID. The lockout by name counts the source address of base.
func (s *Service) PasswordMethod(base audit.Entry) *PasswordMethod {
	return &PasswordMethod{svc: s, base: base}
}

// Kind implements session.PasswordMethod.
func (*PasswordMethod) Kind() session.MethodKind {
	return session.MethodLocalPassword
}

// Verify implements session.PasswordMethod. On any error, the Assertion is
// the zero value.
func (m *PasswordMethod) Verify(
	ctx context.Context, c session.Credentials, gate session.BindGate,
) (session.Assertion, error) {
	var none session.Assertion

	if err := checkCredentials(c, gate); err != nil {
		return none, err
	}

	in, err := m.svc.checkPassword(ctx, m.base, c.UserName, c.Password.Reveal(), gate)
	if err != nil {
		return none, methodError(err)
	}

	// Defense in depth: the service builds the Assertion from the record of
	// the check. This check stays so that a change of the service cannot
	// return an Assertion that the sign-in service refuses.
	if checkErr := session.CheckAssertion(in.Assertion, m.svc.Now()); checkErr != nil {
		return none, fmt.Errorf("%w: %w", session.ErrMethodAnswer, checkErr)
	}

	return in.Assertion, nil
}

// checkCredentials is the boundary check of the credentials of a local
// password sign-in. The service checks the form of the user name and of the
// password again, with its own rules.
func checkCredentials(c session.Credentials, gate session.BindGate) error {
	switch {
	case c.ProviderID != session.LocalProviderID:
		return fmt.Errorf("%w: the provider is not %q", session.ErrMethodInput, session.LocalProviderID)
	case gate == nil:
		// Fail closed: with no gate, the sign-in service could not count a
		// check on the person.
		return fmt.Errorf("%w: no bind gate", session.ErrMethodInput)
	case c.UserName == "" || len(c.UserName) > session.MaxUserName || !utf8.ValidString(c.UserName) ||
		strings.ContainsFunc(c.UserName, unicode.IsControl):
		return fmt.Errorf("%w: the user name must have 1 to %d bytes of UTF-8 and no control character",
			session.ErrMethodInput, session.MaxUserName)
	case c.Password.Reveal() == "":
		return fmt.Errorf("%w: the password is empty", session.ErrMethodInput)
	default:
		return nil
	}
}

// methodError translates an error of the sign-in of the service into an
// error of package session, and keeps err in the chain. An error of the
// gate is returned as it is. An error of no known class is
// ErrMethodUnavailable, so it fails closed.
func methodError(err error) error {
	switch {
	case errors.Is(err, errBindGate):
		return err
	case errors.Is(err, localuser.ErrPasswordSignInOff):
		return fmt.Errorf("%w: %w", session.ErrProviderDisabled, err)
	case errors.Is(err, localuser.ErrSignInFailed), errors.Is(err, localuser.ErrSecondFactorRequired):
		return fmt.Errorf("%w: %w", session.ErrMethodRejected, err)
	default:
		return fmt.Errorf("%w: %w", session.ErrMethodUnavailable, err)
	}
}
