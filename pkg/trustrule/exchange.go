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
// SPIFFEID is the verified SPIFFE ID of an mTLS exchange: set for a token of
// the kind mtls-exchange, empty for every other kind.
type Exchange struct {
	Token    accesstoken.Token
	Digest   accesstoken.Digest
	Rule     ID
	SPIFFEID string
}

// ExchangeStore keeps the access tokens of the exchanges.
type ExchangeStore interface {
	// RecordExchange stores the access token x.Token with the digest
	// x.Digest and appends e, in one transaction that checks the fence. In
	// that transaction it checks again that the rule x.Rule exists in the
	// namespace of the fence and names the principal of the token, and that
	// the principal exists, is not deleted or disabled, is not a person, and
	// is a principal of that namespace. For a token of the kind
	// mtls-exchange it also matches x.SPIFFEID again in that transaction:
	// exactly one rule of the namespace, in the bundle of x.Rule, has the
	// SPIFFE ID pattern x.SPIFFEID, and that rule is x.Rule. It returns:
	//   - ErrNotFound when the rule does not exist, names another
	//     principal, or is not the one rule that names x.SPIFFEID.
	//   - ErrPrincipal when the principal fails the check above.
	//   - ErrInvalid when x.Token is not a token of the kind jwt-exchange or
	//     mtls-exchange, or when x.SPIFFEID is empty for mtls-exchange or set
	//     for jwt-exchange.
	//   - accesstoken.ErrAlreadyExists when the token ID or the digest is
	//     taken.
	//   - namespace.ErrFenced and audit.ErrInvalidEntry.
	//
	// On every error the store changes nothing.
	RecordExchange(ctx context.Context, x Exchange, fence namespace.Fence, e audit.Entry) error
}
