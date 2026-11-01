package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// PasswordBlocked means that the new password is common, holds a name of the person or of the product, or is a repeat or a sequence.
	PasswordBlocked = "password.blocked"
	// PasswordForm means that the password is not valid UTF-8 or holds a control character.
	PasswordForm = "password.form"
	// PasswordLettersAndDigits means that the new password needs a letter and a digit.
	PasswordLettersAndDigits = "password.letters-and-digits"
	// PasswordTooLong means that the password is over the length limit.
	PasswordTooLong = "password.too-long"
	// PasswordTooShort means that the new password is below the minimum length.
	PasswordTooShort = "password.too-short"
)

// passwordSpecs are the codes of a refused password.
func passwordSpecs() []Spec {
	return []Spec{
		{
			Code:    PasswordBlocked,
			Meaning: "the new password is common, holds a name of the person or of the product, or is a repeat or a sequence",
			Args:    nil,
		},
		{Code: PasswordForm, Meaning: "the password is not valid UTF-8 or holds a control character", Args: nil},
		{Code: PasswordLettersAndDigits, Meaning: "the new password needs a letter and a digit", Args: nil},
		{Code: PasswordTooLong, Meaning: "the password is over the length limit", Args: nil},
		{
			Code:    PasswordTooShort,
			Meaning: "the new password is below the minimum length",
			Args:    []Arg{{Name: "minLength", Kind: KindQuantity}},
		},
	}
}
