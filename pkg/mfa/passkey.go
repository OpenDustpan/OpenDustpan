package mfa

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
)

// Limits of the values of a passkey and of a ceremony.
const (
	// PasskeyIDPrefix is the prefix of the ID of a passkey.
	PasskeyIDPrefix = "pky-"
	// MaxPasskeyNameLength is the most characters of the name of a passkey.
	// The name has the character rules of the name of a principal.
	MaxPasskeyNameLength = 100
	// MaxCredentialIDBytes is the most bytes of a credential ID. WebAuthn
	// sets the same limit.
	MaxCredentialIDBytes = 1023
	// MaxPublicKeyBytes is the most bytes of a COSE public key.
	MaxPublicKeyBytes = 4096
	// MaxResponseBytes is the most bytes of the JSON of a ceremony response
	// from a browser.
	MaxResponseBytes = 64 << 10
	// MaxIDLength is the most characters of the ID of a passkey, a person,
	// a pending sign-in or a session in a record of this package.
	MaxIDLength = 128
)

// Transports that a passkey can name. A browser sends them at the
// registration, and the server gives them back in each assertion request.
const (
	TransportUSB       = "usb"
	TransportNFC       = "nfc"
	TransportBLE       = "ble"
	TransportSmartCard = "smart-card"
	TransportHybrid    = "hybrid"
	TransportInternal  = "internal"
)

// transports is the allowlist of the transports of a passkey.
func transports() []string {
	return []string{
		TransportUSB, TransportNFC, TransportBLE, TransportSmartCard, TransportHybrid, TransportInternal,
	}
}

// ValidTransport reports whether t is a transport of the allowlist.
func ValidTransport(t string) bool {
	return slices.Contains(transports(), t)
}

// ValidatePasskeyName returns ErrInvalid unless name has the character rules
// of the name of a principal: 1 to MaxPasskeyNameLength ASCII letters,
// digits, '-', '_' and '.', not starting with '-' or '.'.
func ValidatePasskeyName(name string) error {
	if !ident.Valid(name, MaxPasskeyNameLength) {
		return fmt.Errorf("%w: passkey name: %w", ErrInvalid, ident.Check(name, MaxPasskeyNameLength))
	}

	return nil
}

// validID returns ErrInvalid unless s is an ID of the rules of an
// identifier, with at most MaxIDLength characters.
func validID(field, s string) error {
	if !ident.Valid(s, MaxIDLength) {
		return fmt.Errorf("%w: %s: %w", ErrInvalid, field, ident.Check(s, MaxIDLength))
	}

	return nil
}

// Validate returns ErrInvalid for a passkey that a store must not hold: an
// ID without the prefix, an empty field, a name that the rules of a
// principal name refuse, a credential ID or a key out of its limits, a
// transport out of the allowlist or named twice, or a zero time of the add.
func (p Passkey) Validate() error {
	if !strings.HasPrefix(p.ID, PasskeyIDPrefix) {
		return fmt.Errorf("%w: passkey ID has no prefix %q", ErrInvalid, PasskeyIDPrefix)
	}

	if err := validID("passkey ID", p.ID); err != nil {
		return err
	}

	if err := validID("person ID", p.PersonID); err != nil {
		return err
	}

	if err := ValidatePasskeyName(p.Name); err != nil {
		return err
	}

	if len(p.CredentialID) == 0 || len(p.CredentialID) > MaxCredentialIDBytes {
		return fmt.Errorf("%w: credential ID has %d bytes, want 1 to %d", ErrInvalid, len(p.CredentialID),
			MaxCredentialIDBytes)
	}

	if len(p.PublicKey) == 0 || len(p.PublicKey) > MaxPublicKeyBytes {
		return fmt.Errorf("%w: public key has %d bytes, want 1 to %d", ErrInvalid, len(p.PublicKey),
			MaxPublicKeyBytes)
	}

	if err := validTransports(p.Transports); err != nil {
		return err
	}

	if p.CreatedAt.IsZero() {
		return fmt.Errorf("%w: passkey has no time of the add", ErrInvalid)
	}

	return nil
}

// validTransports returns ErrInvalid for a transport out of the allowlist or
// one that ts names twice.
func validTransports(ts []string) error {
	for i, t := range ts {
		if !ValidTransport(t) {
			return fmt.Errorf("%w: transport %s is not on the allowlist", ErrInvalid, ident.Echo(t))
		}

		if slices.Contains(ts[:i], t) {
			return fmt.Errorf("%w: transport %s is named twice", ErrInvalid, ident.Echo(t))
		}
	}

	return nil
}

// ValidCeremony reports whether c is a kind of a ceremony.
func ValidCeremony(c Ceremony) bool {
	switch c {
	case CeremonyRegister, CeremonySignIn, CeremonySecondFactor, CeremonyFresh:
		return true
	default:
		return false
	}
}

// Validate returns ErrInvalid for a challenge that a store must not hold:
// an unknown kind, a lifetime that is not above 0 or is longer than
// ChallengeLifetime, or the wrong set of IDs for the kind. A
// CeremonySignIn names no person, no pending sign-in and no session. A
// CeremonyRegister and a CeremonyFresh name a person and a session. A
// CeremonySecondFactor names a person and a pending sign-in.
func (c Challenge) Validate() error {
	if !ValidCeremony(c.Kind) {
		return fmt.Errorf("%w: ceremony kind %s", ErrInvalid, ident.Echo(string(c.Kind)))
	}

	if c.CreatedAt.IsZero() || !c.ExpiresAt.After(c.CreatedAt) {
		return fmt.Errorf("%w: challenge expiry is not after its creation", ErrInvalid)
	}

	if c.ExpiresAt.Sub(c.CreatedAt) > ChallengeLifetime {
		return fmt.Errorf("%w: challenge lifetime is above %s", ErrInvalid, ChallengeLifetime)
	}

	want := map[Ceremony][3]bool{
		CeremonySignIn:       {false, false, false},
		CeremonyRegister:     {true, false, true},
		CeremonyFresh:        {true, false, true},
		CeremonySecondFactor: {true, true, false},
	}[c.Kind]

	fields := []struct {
		name  string
		value string
		set   bool
	}{
		{name: "person ID", value: c.PersonID, set: want[0]},
		{name: "pending ID", value: c.PendingID, set: want[1]},
		{name: "session ID", value: c.SessionID, set: want[2]},
	}

	if err := c.validateSource(); err != nil {
		return err
	}

	for _, f := range fields {
		if !f.set {
			if f.value != "" {
				return fmt.Errorf("%w: a %s ceremony has no %s", ErrInvalid, c.Kind, f.name)
			}

			continue
		}

		if err := validID(f.name, f.value); err != nil {
			return err
		}
	}

	return nil
}

// ValidSource reports whether source is 1 to MaxSourceBytes printable ASCII
// characters.
func ValidSource(source string) bool {
	return source != "" && len(source) <= MaxSourceBytes &&
		!strings.ContainsFunc(source, func(r rune) bool { return r < ' ' || r > '~' })
}

// validateSource returns ErrInvalid unless only a CeremonySignIn has a source,
// and that source is 1 to MaxSourceBytes printable ASCII characters. The
// source goes to a count by source and to the audit log, so it holds no line
// break and no control character.
func (c Challenge) validateSource() error {
	if (c.Kind == CeremonySignIn) != (c.Source != "") {
		return fmt.Errorf("%w: only a %s ceremony has a source", ErrInvalid, CeremonySignIn)
	}

	if c.Source != "" && !ValidSource(c.Source) {
		return fmt.Errorf("%w: challenge source is not %d printable ASCII bytes or fewer", ErrInvalid, MaxSourceBytes)
	}

	return nil
}

// DigestOf returns the SHA-256 digest of the secret b.
func DigestOf(b []byte) Digest {
	return sha256.Sum256(b)
}

// String returns the digest as 64 lowercase hex digits, the form that the
// stores keep.
func (d Digest) String() string {
	return hex.EncodeToString(d[:])
}

// ChallengeExpiry returns the expiry of a ceremony that starts at at.
func ChallengeExpiry(at time.Time) time.Time {
	return at.Add(ChallengeLifetime)
}
