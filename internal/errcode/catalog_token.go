package errcode

// TokenInvalid is an access token that the server cannot use for the request.
const TokenInvalid = "token.invalid"

// tokenSpecs are the codes of the access tokens.
func tokenSpecs() []Spec {
	return []Spec{
		{Code: TokenInvalid, Meaning: "the access token is not valid for this request", Args: nil},
	}
}
