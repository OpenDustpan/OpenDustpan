package mfa

import "errors"

// ErrVerification reports a ceremony response that fails a check of the
// WebAuthn protocol: a signature, the origin, the relying party ID, the
// challenge, a flag or the attestation.
var ErrVerification = errors.New("passkey ceremony check failed")

// ErrWrongPerson reports a ceremony, a challenge or a passkey of another
// person or of another session than the caller of the step.
var ErrWrongPerson = errors.New("the passkey ceremony is of another person or session")

// UserVerification is the user verification that a ceremony asks for.
type UserVerification string

// Requirements of user verification.
const (
	// VerificationRequired fails a response without user verification.
	VerificationRequired UserVerification = "required"
	// VerificationPreferred asks for user verification and accepts a
	// response without it.
	VerificationPreferred UserVerification = "preferred"
)

// VerificationOf returns the user verification of the ceremony kind k. A
// second factor after another method prefers it. Each other kind requires
// it: a registration makes a passkey that can sign in alone, a sign-in is
// the only factor, and a fresh authentication is a step-up.
func VerificationOf(k Ceremony) UserVerification {
	if k == CeremonySecondFactor {
		return VerificationPreferred
	}

	return VerificationRequired
}

// Person is the WebAuthn user of a ceremony. ID is the principal ID of the
// person, which the authenticator keeps as the user handle. Name and
// DisplayName are the text that the authenticator shows.
type Person struct {
	ID          string
	Name        string
	DisplayName string
}

// Registration is the credential of a register ceremony that passed each
// check of the relying party.
type Registration struct {
	CredentialID   []byte
	PublicKey      []byte
	Transports     []string
	AAGUID         [16]byte
	SignCount      uint32
	BackupEligible bool
	UserVerified   bool
}

// ResponseHead holds the values of a ceremony response that the service
// reads before the checks: the challenge of the client data, the credential
// ID and the user handle. No signature check has run on them.
type ResponseHead struct {
	Challenge    []byte
	CredentialID []byte
	UserHandle   []byte
}

// Assertion is the result of an assertion that passed each check of the
// relying party.
type Assertion struct {
	SignCount    uint32
	UserVerified bool
}

// RelyingParty is the port of the WebAuthn protocol checks. The adapter holds
// the relying party ID and the origin, which come from the external URL of
// the server, and checks each response against them. It holds no state: the
// service stores each challenge, and the store keeps each passkey.
type RelyingParty interface {
	// CreationOptions returns the JSON of the options of a register
	// ceremony for the person p with the challenge c. exclude lists the
	// passkeys that the person holds, so that an authenticator does not
	// register twice. It returns ErrInvalid for a value out of its limits.
	CreationOptions(p Person, c []byte, exclude []Passkey) ([]byte, error)
	// RequestOptions returns the JSON of the options of an assertion
	// ceremony of the kind k with the challenge c. allow lists the passkeys
	// that the assertion can use, and is empty for CeremonySignIn, which
	// uses a discoverable credential. It returns ErrInvalid for
	// CeremonyRegister or a value out of its limits.
	RequestOptions(k Ceremony, c []byte, allow []Passkey) ([]byte, error)
	// Head reads the response JSON of a register or an assertion ceremony
	// and returns its head, with no check of a signature. It returns
	// ErrInvalid for a response that is longer than MaxResponseBytes or
	// that does not parse.
	Head(response []byte) (ResponseHead, error)
	// VerifyCreation checks the response JSON of a register ceremony of the
	// person p with the challenge c. It returns ErrInvalid for a response
	// that does not parse, and ErrVerification for a response that fails a
	// check.
	VerifyCreation(p Person, c, response []byte) (Registration, error)
	// VerifyAssertion checks the response JSON of an assertion ceremony of
	// the kind k with the challenge c, made with the passkey key of the
	// person owner. It returns ErrInvalid for a response that does not
	// parse, and ErrVerification for a response that fails a check. It does
	// not compare the signature counter: the store does.
	VerifyAssertion(k Ceremony, owner Person, key Passkey, c, response []byte) (Assertion, error)
}
