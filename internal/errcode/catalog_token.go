package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// TokenAlreadyExists means that an access token with the same identity exists.
	TokenAlreadyExists = "token.already-exists"
	// TokenEnded means that the access token ended, so the request cannot change it.
	TokenEnded = "token.ended"
	// TokenInvalid is an access token that the server cannot use for the request.
	TokenInvalid = "token.invalid"
	// TokenKindNone means that the kind of the principal gets no access token.
	TokenKindNone = "token.kind-none"
	// TokenNoPerson means that the caller is not a person with a principal record.
	TokenNoPerson = "token.no-person"
	// TokenNotFound means that the access token does not exist.
	TokenNotFound = "token.not-found"
	// TokenNeedsSignIn means that the request needs a console sign-in, and an access token cannot do it.
	TokenNeedsSignIn = "token.needs-sign-in"
	// TokenUnsupported means that the store of the server keeps no access tokens.
	TokenUnsupported = "token.unsupported"
	// TokenWrongKind means that the kind of the access token does not fit the route.
	TokenWrongKind = "token.wrong-kind"
)

// tokenSpecs are the codes of the access tokens.
func tokenSpecs() []Spec {
	return []Spec{
		{Code: TokenAlreadyExists, Meaning: "an access token with the same identity exists", Args: nil},
		{Code: TokenEnded, Meaning: "the access token ended, so the request cannot change it", Args: nil},
		{Code: TokenInvalid, Meaning: "the access token is not valid for this request", Args: nil},
		{Code: TokenKindNone, Meaning: "the kind of the principal gets no access token", Args: nil},
		{Code: TokenNoPerson, Meaning: "the caller is not a person with a principal record", Args: nil},
		{Code: TokenNotFound, Meaning: "the access token does not exist", Args: nil},
		{
			Code:    TokenNeedsSignIn,
			Meaning: "the request needs a console sign-in, and an access token cannot do it",
			Args:    nil,
		},
		{Code: TokenUnsupported, Meaning: "the store of the server keeps no access tokens", Args: nil},
		{Code: TokenWrongKind, Meaning: "the kind of the access token does not fit the route", Args: nil},
	}
}
