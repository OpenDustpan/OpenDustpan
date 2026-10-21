package sessionauth

// The wire values of a refused fresh authentication. Each adapter that
// refuses a write for a missing or old sign-in uses them, so the answers stay
// the same on every API.
const (
	// FreshStepRequiredCode is the errorCode of a write that has no fresh
	// authentication.
	FreshStepRequiredCode = "mfa.fresh-step-required"
	// FreshStepRequiredMessage is the text of a write from a request with no
	// console session, for example one with an access token.
	FreshStepRequiredMessage = "a change needs a console session with a recent sign-in; " +
		"sign in to the console again"
	// SignInStartPrefix starts the path of the start of a sign-in, before the
	// provider.
	SignInStartPrefix = "/api/server/sign-in/"
)
