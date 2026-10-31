package accesstoken

import (
	"context"
	"fmt"
	"time"
)

// Issued is a new token with its secret and the digest of the secret, before
// a store keeps it.
type Issued struct {
	Token  Token
	Secret Secret
	Digest Digest
}

// NewTrustToken returns a new token of the kind KindTrust for the principal
// principalID, made at the time at, with its secret. It does not store the
// token: the store of the trust rules keeps it with the audit entry of the
// exchange in one transaction. The token lives the exchange lifetime of the
// policy, or its maximum lifetime when that is shorter. name and createdBy
// pass ValidateName and name the trust rule. A failed read of the policy
// answers its error.
func (s *Service) NewTrustToken(
	ctx context.Context, at time.Time, principalID, name, createdBy string,
) (Issued, error) {
	if err := ValidateName(name); err != nil {
		return Issued{}, err
	}

	p, err := s.Policy(ctx)
	if err != nil {
		return Issued{}, fmt.Errorf("read the token policy: %w", err)
	}

	lifetime := p.ExchangeLifetimeUnder()
	if lifetime <= 0 {
		return Issued{}, fmt.Errorf("%w: the exchange lifetime of the token policy is not positive", ErrInvalid)
	}

	n, err := s.newToken(tokenSpec{
		at: at, expiresAt: at.Add(lifetime), principalID: principalID, name: name, kind: KindTrust,
		createdBy: createdBy,
	})
	if err != nil {
		return Issued{}, err
	}

	return Issued{Token: n.token, Secret: n.secret, Digest: n.digest}, nil
}
