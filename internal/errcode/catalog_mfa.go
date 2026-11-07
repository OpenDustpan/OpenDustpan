package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// MFACeremonyFailed means that the response of the authenticator failed a check of the passkey protocol.
	MFACeremonyFailed = "mfa.ceremony-failed"
	// MFACeremonyNotOpen means that no open passkey ceremony has the challenge of the response.
	MFACeremonyNotOpen = "mfa.ceremony-not-open"
	// MFACeremonyWrongPerson means that the passkey ceremony belongs to another person or session.
	MFACeremonyWrongPerson = "mfa.ceremony-wrong-person"
	// MFABreakGlassReasonRequired means that the sign-in of a break-glass account needs a reason that the person types.
	MFABreakGlassReasonRequired = "mfa.break-glass-reason-required"
	// MFAFreshStepRequired means that the write needs a fresh authentication step.
	MFAFreshStepRequired = "mfa.fresh-step-required"
	// MFAKeyExists means that a person already holds the passkey of the authenticator.
	MFAKeyExists = "mfa.passkey-exists"
	// MFAKeyInvalid means that a value of the passkey request is not valid.
	MFAKeyInvalid = "mfa.passkey-invalid"
	// MFAKeyLimit means that the person holds the most passkeys that one person can hold.
	MFAKeyLimit = "mfa.passkey-limit"
	// MFAKeyNotFound means that the person has no passkey with this ID.
	MFAKeyNotFound = "mfa.passkey-not-found"
	// MFAKeySuspended means that a counter that went back suspended the passkey.
	MFAKeySuspended = "mfa.passkey-suspended"
	// MFARateLimited means that the person started too many passkey ceremonies in a short time.
	MFARateLimited = "mfa.rate-limited"
	// MFASessionRequired means that the passkey route needs the console session of a person.
	MFASessionRequired = "mfa.session-required"
	// MFASignInSourceLimit means that the source address holds the most open passkey sign-in challenges that one source
	// can hold.
	MFASignInSourceLimit = "mfa.sign-in-source-limit"
	// MFASignInFailed means that the passkey sign-in failed and the person starts it again.
	MFASignInFailed = "mfa.sign-in-failed"
	// MFAUnavailable means that the passkey store cannot answer.
	MFAUnavailable = "mfa.unavailable"
)

// mfaSpecs are the codes of the factors of a person.
func mfaSpecs() []Spec {
	return []Spec{
		{
			Code:    MFACeremonyFailed,
			Meaning: "the response of the authenticator failed a check of the passkey protocol",
			Args:    nil,
		},
		{
			Code:    MFACeremonyNotOpen,
			Meaning: "no open passkey ceremony has the challenge of the response",
			Args:    nil,
		},
		{
			Code:    MFACeremonyWrongPerson,
			Meaning: "the passkey ceremony belongs to another person or session",
			Args:    nil,
		},
		{
			Code:    MFABreakGlassReasonRequired,
			Meaning: "the sign-in of a break-glass account needs a reason that the person types",
			Args:    nil,
		},
		{Code: MFAFreshStepRequired, Meaning: "the write needs a fresh authentication step", Args: nil},
		{
			Code:    MFAKeyExists,
			Meaning: "a person already holds the passkey of the authenticator",
			Args:    nil,
		},
		{Code: MFAKeyInvalid, Meaning: "a value of the passkey request is not valid", Args: nil},
		{
			Code:    MFAKeyLimit,
			Meaning: "the person holds the most passkeys that one person can hold",
			Args:    []Arg{{Name: "limit", Kind: KindQuantity}},
		},
		{Code: MFAKeyNotFound, Meaning: "the person has no passkey with this ID", Args: nil},
		{
			Code:    MFAKeySuspended,
			Meaning: "a counter that went back suspended the passkey",
			Args:    nil,
		},
		{
			Code:    MFARateLimited,
			Meaning: "the person started too many passkey ceremonies in a short time",
			Args:    nil,
		},
		{
			Code:    MFASignInSourceLimit,
			Meaning: "the source address holds the most open passkey sign-in challenges that one source can hold",
			Args:    nil,
		},
		{
			Code:    MFASessionRequired,
			Meaning: "the passkey route needs the console session of a person",
			Args:    nil,
		},
		{
			Code:    MFASignInFailed,
			Meaning: "the passkey sign-in failed and the person starts it again",
			Args:    nil,
		},
		{Code: MFAUnavailable, Meaning: "the passkey store cannot answer", Args: nil},
	}
}
