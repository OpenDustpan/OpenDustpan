package errcode

// signInSpecs are the codes of the sign-in.
func signInSpecs() []Spec {
	return []Spec{
		{
			Code:    SignInRefused,
			Meaning: "the server refuses the sign-in for a reason that is not a missing permission",
			Args:    nil,
		},
	}
}
