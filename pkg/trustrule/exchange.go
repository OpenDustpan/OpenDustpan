package trustrule

import (
	"context"
	"errors"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ErrKeysUnavailable reports that the server could not read the signing keys
// of an issuer. It wraps the reason.
var ErrKeysUnavailable = errors.New("the signing keys of the issuer are not available")

// KeyFetcher reads the signing keys of the issuer of a rule with the key
// source discovery.
type KeyFetcher interface {
	// Keys returns the signing keys of issuer. kid is the key ID that a
	// token names, or empty. A fetcher may answer keys that it read earlier.
	// It returns ErrKeysUnavailable when it cannot read the keys.
	Keys(ctx context.Context, issuer, kid string) (KeySet, error)
}

// Exchange is one exchange as a store writes it: the rule that matched, and
// the access token that the exchange issues, with the digest of its secret.
type Exchange struct {
	Token  accesstoken.Token
	Digest accesstoken.Digest
	Rule   ID
}

// ExchangeStore keeps the access tokens of the exchanges.
type ExchangeStore interface {
	// RecordExchange stores the access token x.Token with the digest
	// x.Digest and appends e, in one transaction that checks the fence. In
	// that transaction it checks again that the rule x.Rule exists in the
	// namespace of the fence and names the principal of the token, and that
	// the principal exists, is not deleted or disabled, is not a person, and
	// is a principal of that namespace. It returns:
	//   - ErrNotFound when the rule does not exist or names another
	//     principal.
	//   - ErrPrincipal when the principal fails the check above.
	//   - ErrInvalid when x.Token is not a token of the kind trust.
	//   - accesstoken.ErrAlreadyExists when the token ID or the digest is
	//     taken.
	//   - namespace.ErrFenced and audit.ErrInvalidEntry.
	//
	// On every error the store changes nothing.
	RecordExchange(ctx context.Context, x Exchange, fence namespace.Fence, e audit.Entry) error
}
