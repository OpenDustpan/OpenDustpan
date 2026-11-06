package session

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// This file holds the ports of the sign-in methods: the redirect method,
// the password method and the request method. Each method proves who a
// person is and returns an Assertion. A method never makes a principal, a
// membership or a session. The sign-in service does that, in one order, for
// every method, after CheckAssertion. The local password and the passkey are
// not methods of these ports: they make the same Assertion from their own
// records, and the sign-in service starts their session in its own entry. The
// conformance suites in package sessiontest check each adapter.

// Errors of the sign-in methods. Each adapter translates the errors of its
// protocol, its library and its network into these errors. The sign-in
// service gives one answer to the person for each of them, and the audit
// entry records the real reason.
var (
	// ErrMethodInput reports an input that fails the check at the boundary
	// of the method: a callback, credentials or request facts of the wrong
	// form, size or characters. The method sends nothing to the identity
	// provider or the directory.
	ErrMethodInput = errors.New("sign-in input is not valid")
	// ErrMethodRejected reports that the identity provider or the directory
	// refused the person: an unknown user name, a wrong password, or an
	// error answer of the provider. The answer is the same for each case,
	// so that it does not tell that a user exists.
	ErrMethodRejected = errors.New("sign-in was refused")
	// ErrSubjectDisabled reports that the identity provider or the
	// directory knows the person and reports the person as removed or
	// disabled. The method returns it with an Assertion that holds only the
	// provider ID, the subject and the method, so that the sign-in service
	// can disable the principal of that subject.
	ErrSubjectDisabled = errors.New("identity provider reports the person as disabled")
	// ErrMethodUnavailable reports an identity provider or a directory that
	// does not answer in time, or that the network refuses. The sign-in
	// fails closed.
	ErrMethodUnavailable = errors.New("identity provider is not available")
	// ErrMethodAnswer reports an answer of the identity provider that fails
	// a check: a signature, an algorithm, an issuer, an audience, a time, a
	// size, a count of assertions, or a claim of the wrong type.
	ErrMethodAnswer = errors.New("identity provider answer failed a check")
	// ErrRequestBinding reports a callback that does not belong to the
	// sign-in request of the browser: a state, a relay state or an
	// InResponseTo that does not match, or a callback with no request.
	ErrRequestBinding = errors.New("callback does not match the sign-in request")
	// ErrReplay reports a second use of a one-use value of the identity
	// provider: a SAML assertion ID or a CAS ticket.
	ErrReplay = errors.New("sign-in value was already used")
	// ErrUntrustedSource reports a request method whose request does not
	// come from a trusted proxy, or that does not carry the second proof of
	// the proxy.
	ErrUntrustedSource = errors.New("request does not come from a trusted proxy")
	// ErrNoAssertion reports a request that carries none of the headers of
	// a request method. It is not a sign-in attempt.
	ErrNoAssertion = errors.New("request carries no sign-in assertion")
	// ErrGroupsMissing reports a method with groups turned on whose answer
	// holds no groups. A missing groups value never removes memberships, so
	// the sign-in fails.
	ErrGroupsMissing = errors.New("sign-in answer has no groups")
	// ErrInvalidAssertion reports an Assertion that a method returned and
	// that fails the limits of this package. The sign-in service checks
	// each Assertion again before it uses it.
	ErrInvalidAssertion = errors.New("sign-in assertion is not valid")
	// ErrFreshSubject reports a fresh authentication whose person is not
	// the person of the session that asked for it. The step is refused.
	ErrFreshSubject = errors.New("fresh authentication names another person")
)

// Limits of an Assertion.
const (
	// MaxAssertionSubject is the longest subject at the identity provider,
	// in bytes.
	MaxAssertionSubject = 255
	// MaxAssertionGroups is the most groups that one Assertion holds. More
	// groups refuse the sign-in. They are never cut to the limit.
	MaxAssertionGroups = 1000
	// MaxAssertionGroup is the longest group name, in bytes.
	MaxAssertionGroup = 256
	// MaxAssertionName is the longest display name, user name or email, in
	// bytes.
	MaxAssertionName = 256
	// MaxUserName is the longest user name that a password method accepts,
	// in bytes.
	MaxUserName = 256
)

// MethodKind names the kind of a sign-in method. The session records it.
type MethodKind string

// Kinds of a sign-in method.
const (
	// MethodOIDC is an OpenID Connect provider. It is a redirect method.
	MethodOIDC MethodKind = "oidc"
	// MethodOAuth is a plain OAuth 2.0 provider with a user-info endpoint.
	// It is a redirect method. It gives no authentication time.
	MethodOAuth MethodKind = "oauth"
	// MethodSAML is a SAML 2.0 identity provider. It is a redirect method.
	MethodSAML MethodKind = "saml"
	// MethodCAS is a CAS 3.0 server. It is a redirect method.
	MethodCAS MethodKind = "cas"
	// MethodLDAP is an LDAP directory. It is a password method.
	MethodLDAP MethodKind = "ldap"
	// MethodHeader is the trusted header of a proxy. It is a request
	// method. It gives no authentication time.
	MethodHeader MethodKind = "header"
	// MethodLocalPassword is the password of a local user.
	MethodLocalPassword MethodKind = "local-password"
	// MethodPasskey is the sign-in of a local user or of a break-glass
	// account with a discoverable passkey and user verification.
	MethodPasskey MethodKind = "passkey"
)

// Factor names one factor that OpenDustpan checked itself in a sign-in.
// A factor that the identity provider checked is not a Factor: the
// provider does not tell which factors it checked.
type Factor string

// Factors that OpenDustpan checks.
const (
	// FactorPassword is a password that the server or the directory checked.
	FactorPassword Factor = "password"
	// FactorPasskey is a passkey. With user verification, it counts as two
	// factors.
	FactorPasskey Factor = "passkey"
	// FactorTOTP is a TOTP code.
	FactorTOTP Factor = "totp"
	// FactorRecoveryCode is a recovery code. It never gives a fresh
	// authentication.
	FactorRecoveryCode Factor = "recovery-code"
	// FactorSSHSignature is an SSH signature over a server challenge.
	FactorSSHSignature Factor = "ssh-signature"
)

// Assertion is the result of a sign-in method: who the person is at the
// identity provider, and what the method knows about the authentication.
// The adapter of a method port names no principal: the sign-in service finds
// the principal with the subject "<ProviderID>:<Subject>". Only the local
// password and the passkey sign-in, which read their own records, set
// PrincipalID.
type Assertion struct {
	// AuthTime is the time of the authentication at the identity provider,
	// or the time of the check for a password method. It is the zero time
	// when AuthTimeKnown is false.
	AuthTime time.Time
	// ProviderID names the sign-in provider of the method.
	ProviderID string
	// PrincipalID names the principal of a local password or a passkey
	// sign-in. It is empty for each other method, and the sign-in service
	// refuses an Assertion of another method that sets it.
	PrincipalID string
	// Subject is the stable subject at the identity provider, from the
	// attribute that the provider settings name. It is never a value that
	// the person can change, such as an email, unless an administrator
	// names such an attribute.
	Subject string
	// UserName is the user name at the identity provider, or empty.
	UserName string
	// Email is the email at the identity provider, or empty.
	Email string
	// Method is the kind of the method.
	Method MethodKind
	// Names are the display name candidates, in the order of preference.
	Names []string
	// Groups are the names of the groups of the person at the identity
	// provider, flat: a method that reads nested groups returns each group
	// that the person reaches. It is empty when GroupsPresent is false.
	Groups []string
	// Factors are the factors that OpenDustpan checked in this method, in
	// the order of the checks. A redirect method and a request method
	// return none.
	Factors []Factor
	// EmailVerified is true when the identity provider states that it
	// verified Email.
	EmailVerified bool
	// GroupsPresent is true when the method sent a groups value, also an
	// empty one. False means that the method sends no groups, and the
	// sign-in service then changes no membership.
	GroupsPresent bool
	// AuthTimeKnown is true when the method proves the time of the
	// authentication. Plain OAuth 2.0 and the trusted header never do.
	AuthTimeKnown bool
}

// BeginInput is the start of a redirect method.
type BeginInput struct {
	// ProviderID names the sign-in provider.
	ProviderID string
	// State is a random value that the sign-in service makes and binds to
	// the browser. The method sends it to the identity provider when the
	// protocol has a field for it, and expects it back.
	State string
	// Fresh asks the identity provider for a new authentication: max_age=0
	// for OIDC, ForceAuthn for SAML, renew=true for CAS. A method that has
	// no such parameter ignores it, and its Assertion has AuthTimeKnown
	// false.
	Fresh bool
}

// PendingRedirect is what the sign-in service keeps, sealed, between Begin
// and Finish. It holds secrets. The service uses it once and never shows
// it.
type PendingRedirect struct {
	// StartedAt is the time of Begin. A fresh authentication must not be
	// older than it.
	StartedAt time.Time
	// ProviderID names the sign-in provider.
	ProviderID string
	// State is the state of BeginInput.
	State string
	// Nonce is the OIDC nonce, or empty.
	Nonce string
	// Verifier is the PKCE verifier, or empty.
	Verifier string
	// RequestID is the ID of the SAML AuthnRequest, or empty.
	RequestID string
	// PrincipalID names the principal of the session that asked for a
	// fresh authentication, or is empty for a sign-in.
	PrincipalID string
	// SignInID names the sign-in of the session that asked for a fresh
	// authentication, or is empty for a sign-in.
	SignInID string
	// Fresh is the Fresh value of BeginInput.
	Fresh bool
}

// Redirect is the answer of Begin: the URL of the identity provider, and
// the pending request that Finish needs.
type Redirect struct {
	// URL is where the browser goes. It is an absolute HTTPS URL.
	URL string
	// Pending is the pending request.
	Pending PendingRedirect
}

// Callback is the answer of the identity provider that the browser brings
// back. The HTTP adapter copies each field from the request. It never
// passes the request itself.
type Callback struct {
	// Pending is the pending request of the browser, opened from its
	// sealed cookie, or nil when the browser holds none. A method refuses a
	// callback with no pending request.
	Pending *PendingRedirect
	// State is the OAuth state or the SAML RelayState of the callback.
	State string
	// Code is the OAuth authorization code, or empty.
	Code string
	// Ticket is the CAS service ticket, or empty.
	Ticket string
	// ProviderError is the error code that the identity provider sent, or
	// empty.
	ProviderError string
	// SAMLResponse is the decoded SAML response of the HTTP-POST binding,
	// or empty. The adapter limits its size before the decode.
	SAMLResponse []byte
}

// RedirectMethod is a sign-in method that sends the browser to the
// identity provider and back: OIDC, plain OAuth 2.0, SAML and CAS. The
// conformance suite RunRedirectMethod in package sessiontest checks an
// implementation.
type RedirectMethod interface {
	// Kind returns the kind of the method.
	Kind() MethodKind
	// Begin starts a sign-in. It returns ErrMethodInput for an input that
	// is not valid, ErrProviderDisabled for a disabled provider, and
	// ErrMethodUnavailable when the method must read the metadata of the
	// provider and cannot.
	Begin(ctx context.Context, in BeginInput) (Redirect, error)
	// Finish checks a callback against its pending request and returns the
	// Assertion. It returns ErrMethodInput for a callback of the wrong form
	// or size, ErrRequestBinding for a callback that does not match its
	// pending request or has none (Pending is nil), ErrMethodRejected for an error answer of
	// the provider, ErrMethodAnswer for an answer that fails a check,
	// ErrReplay for a second use of a one-use value, ErrMethodUnavailable
	// for a provider that does not answer, ErrGroupsMissing when groups are
	// on and the answer has none, and ErrSubjectDisabled when the provider
	// reports the person as disabled. On any error, the Assertion is the
	// zero value, except for ErrSubjectDisabled.
	Finish(ctx context.Context, cb Callback) (Assertion, error)
}

// BindGate is the check of the sign-in service between the search and the
// bind of a password method. The method calls BeforeBind with the subject
// that the search found, before it sends the password to the directory. So
// the service counts the attempt on the person, whatever form of the user
// name the person typed.
type BindGate interface {
	// BeforeBind returns nil to allow the bind of the person with the
	// subject, or an error to stop it. The method then returns that error,
	// wrapped, and sends no password.
	BeforeBind(ctx context.Context, subject string) error
}

// Password is a password that a person typed. Its String and GoString
// methods never show the value, so a log or an error does not hold it.
type Password struct {
	value string
}

// NewPassword returns a Password that holds v.
func NewPassword(v string) Password {
	return Password{value: v}
}

// Reveal returns the password. Only the code that checks it calls Reveal.
func (p Password) Reveal() string {
	return p.value
}

// String returns a fixed text and never the password.
func (Password) String() string {
	return "[password]"
}

// GoString returns a fixed text and never the password.
func (Password) GoString() string {
	return "[password]"
}

// Credentials are the user name and the password of a password method.
type Credentials struct {
	// ProviderID names the sign-in provider.
	ProviderID string
	// UserName is the user name that the person typed.
	UserName string
	// Password is the password that the person typed.
	Password Password
}

// PasswordMethod is a sign-in method that checks a user name and a
// password at a directory: LDAP. The password of a local user and the
// passkey are not adapters of this port: they read their own records, make
// the same Assertion with the kinds MethodLocalPassword and MethodPasskey,
// and give it to the sign-in service with the session generation of the
// record. The method never reads or compares a
// password hash, and never follows a referral, because a referral sends the
// password to another server. The lockout is not part of the method: the
// sign-in service counts the typed name before it calls Verify, and counts
// the subject in BindGate. The conformance suite RunPasswordMethod in
// package sessiontest checks an implementation.
type PasswordMethod interface {
	// Kind returns the kind of the method.
	Kind() MethodKind
	// Verify searches for the user, calls gate.BeforeBind with the subject
	// that it found, then binds as that user with the password, and returns
	// the Assertion. It returns ErrMethodInput for a user name that fails
	// the boundary check and for an empty password, which would make an
	// unauthenticated bind, the error of gate wrapped when gate stops the
	// bind, ErrMethodRejected for an unknown user name or a wrong password, with
	// the same error for both, ErrSubjectDisabled for a user that the
	// directory reports as disabled, ErrMethodUnavailable for a directory
	// that does not answer, ErrMethodAnswer for an answer that fails a
	// check or that is over a size limit, ErrGroupsMissing when groups are
	// on and the answer has none, and ErrProviderDisabled for a disabled
	// provider.
	Verify(ctx context.Context, c Credentials, gate BindGate) (Assertion, error)
}

// RequestFacts are the facts of an HTTP request that a request method
// reads. The HTTP adapter fills them. It never passes the request itself.
type RequestFacts struct {
	// ReceivedAt is the time when the server received the request.
	ReceivedAt time.Time
	// Headers holds only the headers that the method names, with canonical
	// names. Each other header is absent.
	Headers map[string][]string
	// ProviderID names the sign-in provider.
	ProviderID string
	// ClientChain is the TLS client certificate chain of the connection,
	// leaf first, after the server checked it against the trust bundle of
	// the method. It is empty when the connection has no client
	// certificate.
	ClientChain []*x509.Certificate
	// Peer is the address of the TCP peer.
	Peer netip.Addr
	// Client is the address of the client that the server read from the
	// forwarded headers past the trusted proxies.
	Client netip.Addr
}

// RequestMethod is a sign-in method that reads the identity from a request
// that a trusted proxy sends: the trusted header. The conformance suite
// RunRequestMethod in package sessiontest checks an implementation.
type RequestMethod interface {
	// Kind returns the kind of the method.
	Kind() MethodKind
	// Headers returns the canonical names of the headers that the method
	// reads. The HTTP adapter removes these headers from each request of a
	// peer that is not a trusted proxy, before a handler reads it.
	Headers() []string
	// FromRequest returns the Assertion of the request. It returns
	// ErrNoAssertion for a request without the header that carries the
	// person,
	// ErrUntrustedSource for a peer that is not a trusted proxy or a
	// request with no valid second proof of the proxy, ErrMethodInput for
	// a header of the wrong form, ErrMethodAnswer for a signed header that
	// fails a check, ErrGroupsMissing when groups are on and the request
	// has no groups header, ErrMethodUnavailable when the method cannot
	// read the keys of the issuer, and ErrProviderDisabled for a disabled
	// provider.
	FromRequest(ctx context.Context, f RequestFacts) (Assertion, error)
}

// FreshTarget is the person of the session that asked for a fresh
// authentication: the principal ID and its subject.
type FreshTarget struct {
	PrincipalID string
	Subject     string
}

// CheckFreshSubject returns ErrFreshSubject when the Assertion a of a fresh
// authentication does not name the person of want. A local password or a
// passkey must name the same principal ID. Each other method must name the
// same subject: "<ProviderID>:<Subject>". An empty want, or an Assertion
// that names no person, is refused, so the check fails closed.
func CheckFreshSubject(want FreshTarget, a Assertion) error {
	if want.PrincipalID == "" || want.Subject == "" {
		return fmt.Errorf("%w: the session names no person", ErrFreshSubject)
	}

	if a.PrincipalID != "" {
		// Defense in depth: the sign-in service refuses an Assertion of
		// another method that names a principal too. This check stays so
		// that a new caller cannot skip it.
		if a.Method != MethodLocalPassword && a.Method != MethodPasskey {
			return fmt.Errorf("%w: the method %q cannot name a principal", ErrFreshSubject, a.Method)
		}

		if a.PrincipalID != want.PrincipalID {
			return fmt.Errorf("%w: the principal differs", ErrFreshSubject)
		}

		return nil
	}

	if a.ProviderID == "" || a.Subject == "" || SubjectPrefix(a.ProviderID)+a.Subject != want.Subject {
		return fmt.Errorf("%w: the subject differs", ErrFreshSubject)
	}

	return nil
}

// AssertionSkew is the clock skew that CheckAssertion allows for the
// authentication time of an Assertion.
const AssertionSkew = time.Minute

// CheckAssertion returns ErrInvalidAssertion when the Assertion a fails the
// limits of this package at the time now. The sign-in service calls it for
// each Assertion before it uses it. The error names the rule and never a
// value of a. It checks these rules:
//   - the method is a known kind;
//   - a local password or a passkey has the provider ID "local" and a
//     principal ID; each other method has a valid provider ID that is not
//     "local" or "dev", and no principal ID;
//   - the subject has 1 to MaxAssertionSubject bytes of SafeText;
//   - the user name, the email and each name have at most MaxAssertionName
//     bytes of SafeText, and a verified email is not empty;
//   - groups only with GroupsPresent, at most MaxAssertionGroups of them,
//     each of 1 to MaxAssertionGroup bytes of SafeText;
//   - the factors are known: none for a redirect method or a request
//     method, the password for a password method and a local password, and
//     the passkey for a passkey sign-in;
//   - a known authentication time is not zero and not after now with the
//     skew; an unknown one is zero; plain OAuth 2.0 and the trusted header
//     never know it, and a password method and a passkey always do.
func CheckAssertion(a Assertion, now time.Time) error {
	for _, check := range []func(Assertion, time.Time) error{
		checkAssertionMethod, checkAssertionPerson, checkAssertionGroups, checkAssertionFactors,
		checkAssertionAuthTime,
	} {
		if err := check(a, now); err != nil {
			return err
		}
	}

	return nil
}

// invalidAssertion returns ErrInvalidAssertion with the rule that failed.
func invalidAssertion(rule string) error {
	return fmt.Errorf("%w: %s", ErrInvalidAssertion, rule)
}

// localMethod reports whether k reads its own records: the local password
// and the passkey. Only these methods name a principal.
func localMethod(k MethodKind) bool {
	return k == MethodLocalPassword || k == MethodPasskey
}

// checkAssertionMethod checks the kind, the provider ID and the principal
// ID of a.
func checkAssertionMethod(a Assertion, _ time.Time) error {
	switch a.Method {
	case MethodOIDC, MethodOAuth, MethodSAML, MethodCAS, MethodLDAP, MethodHeader:
		if !ValidProviderID(a.ProviderID) || a.ProviderID == LocalProviderID || a.ProviderID == DevProviderID {
			return invalidAssertion("the provider ID is not valid for the method")
		}

		if a.PrincipalID != "" {
			return invalidAssertion("the method cannot name a principal")
		}

		return nil
	case MethodLocalPassword, MethodPasskey:
		if a.ProviderID != LocalProviderID {
			return invalidAssertion("the provider ID of a local method must be " + LocalProviderID)
		}

		if !validAssertionText(a.PrincipalID, 1, MaxAssertionSubject) {
			return invalidAssertion("a local method must name a principal")
		}

		return nil
	default:
		return invalidAssertion("the method is not a known kind")
	}
}

// checkAssertionPerson checks the subject, the names and the email of a.
func checkAssertionPerson(a Assertion, _ time.Time) error {
	if !validAssertionText(a.Subject, 1, MaxAssertionSubject) {
		return invalidAssertion(fmt.Sprintf(
			"the subject must have 1 to %d bytes of UTF-8 with graphic characters only", MaxAssertionSubject,
		))
	}

	for _, v := range append([]string{a.UserName, a.Email}, a.Names...) {
		if !validAssertionText(v, 0, MaxAssertionName) {
			return invalidAssertion(fmt.Sprintf(
				"a name, a user name or an email must have at most %d bytes of UTF-8 with graphic characters only",
				MaxAssertionName,
			))
		}
	}

	if a.EmailVerified && a.Email == "" {
		return invalidAssertion("a verified email is empty")
	}

	return nil
}

// checkAssertionGroups checks the groups of a.
func checkAssertionGroups(a Assertion, _ time.Time) error {
	if !a.GroupsPresent && len(a.Groups) > 0 {
		return invalidAssertion("the groups are set, and GroupsPresent is false")
	}

	if len(a.Groups) > MaxAssertionGroups {
		return invalidAssertion(fmt.Sprintf("more than %d groups", MaxAssertionGroups))
	}

	for _, g := range a.Groups {
		if !validAssertionText(g, 1, MaxAssertionGroup) {
			return invalidAssertion(fmt.Sprintf(
				"a group must have 1 to %d bytes of UTF-8 with graphic characters only", MaxAssertionGroup,
			))
		}
	}

	return nil
}

// checkAssertionFactors checks the factors of a against its method.
func checkAssertionFactors(a Assertion, _ time.Time) error {
	for _, f := range a.Factors {
		switch f {
		case FactorPassword, FactorPasskey, FactorTOTP, FactorRecoveryCode, FactorSSHSignature:
		default:
			return invalidAssertion("a factor is not a known factor")
		}
	}

	switch a.Method {
	case MethodOIDC, MethodOAuth, MethodSAML, MethodCAS, MethodHeader:
		if len(a.Factors) > 0 {
			return invalidAssertion("a redirect method or a request method checks no factor")
		}
	case MethodLDAP, MethodLocalPassword:
		if !slices.Contains(a.Factors, FactorPassword) {
			return invalidAssertion("a password method must hold the factor " + string(FactorPassword))
		}
	case MethodPasskey:
		if !slices.Contains(a.Factors, FactorPasskey) {
			return invalidAssertion("a passkey sign-in must hold the factor " + string(FactorPasskey))
		}
	default:
		// Fail closed: checkAssertionMethod refuses an unknown kind first.
		// This check stays so that a new order of the checks cannot pass
		// one.
		return invalidAssertion("the method is not a known kind")
	}

	return nil
}

// checkAssertionAuthTime checks the authentication time of a at now. It has
// no lower bound: an old authentication is a valid answer to a sign-in that
// asks for no fresh authentication, for example a person with a long session
// at the identity provider. The method checks the window of a fresh
// authentication against the start of its request, and the session policy
// limits how long the session of an old authentication is fresh.
func checkAssertionAuthTime(a Assertion, now time.Time) error {
	switch {
	case !a.AuthTimeKnown && !a.AuthTime.IsZero():
		return invalidAssertion("an unknown authentication time must be zero")
	case !a.AuthTimeKnown && (a.Method == MethodLDAP || localMethod(a.Method)):
		return invalidAssertion("a password method or a passkey must know the authentication time")
	case !a.AuthTimeKnown:
		return nil
	case a.Method == MethodOAuth || a.Method == MethodHeader:
		return invalidAssertion("plain OAuth 2.0 and the trusted header cannot know the authentication time")
	case a.AuthTime.IsZero():
		return invalidAssertion("a known authentication time is zero")
	case a.AuthTime.After(now.Add(AssertionSkew)):
		return invalidAssertion("the authentication time is in the future")
	default:
		return nil
	}
}

// validAssertionText reports whether s is SafeText and has minLen to maxLen
// bytes.
func validAssertionText(s string, minLen, maxLen int) bool {
	return len(s) >= minLen && len(s) <= maxLen && SafeText(s)
}

// SafeText reports whether s is UTF-8 whose each character is graphic, and
// whose only space is the ASCII space. It refuses control characters (C0
// and C1), format characters such as the bidirectional overrides and the
// zero-width characters, the line and paragraph separators, private-use and
// unassigned code points, and each other space. Such a character can make
// a name look like another name in the console or the audit log, or break
// a line of a log.
func SafeText(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}

	return !strings.ContainsFunc(s, func(r rune) bool {
		return !unicode.IsGraphic(r) || (unicode.Is(unicode.Zs, r) && r != ' ')
	})
}
