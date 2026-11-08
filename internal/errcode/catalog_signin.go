package errcode

// Codes of a sign-in with a password at a directory.
const (
	// SignInFailed means that the user name or the password is wrong, or that the
	// person cannot sign in now. The answer tells nothing more, so it does not
	// tell that a user exists.
	SignInFailed = "sign-in.failed"
	// SignInUnavailable means that the directory of the sign-in provider does not
	// answer.
	SignInUnavailable = "sign-in.unavailable"
)

// signInSpecs are the codes of the sign-in.
func signInSpecs() []Spec {
	return []Spec{
		{
			Code:    SignInRefused,
			Meaning: "the server refuses the sign-in for a reason that is not a missing permission",
			Args:    nil,
		},
		{
			Code:    SignInFailed,
			Meaning: "the user name or the password is wrong, or the person cannot sign in now",
			Args:    nil,
		},
		{
			Code:    SignInUnavailable,
			Meaning: "the directory of the sign-in provider does not answer",
			Args:    nil,
		},
	}
}
