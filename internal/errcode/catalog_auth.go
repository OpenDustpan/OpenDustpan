package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// AuthRateLimited means that too many sign-ins from the address of the request failed.
	AuthRateLimited = "auth.rate-limited"
	// AuthUnauthenticated means that the request has no valid credential.
	AuthUnauthenticated = "auth.unauthenticated"
)

// authSpecs are the codes of a request that the authentication refuses.
func authSpecs() []Spec {
	return []Spec{
		{Code: AuthRateLimited, Meaning: "too many sign-ins from the address of the request failed", Args: nil},
		{Code: AuthUnauthenticated, Meaning: "the request has no valid credential", Args: nil},
	}
}
