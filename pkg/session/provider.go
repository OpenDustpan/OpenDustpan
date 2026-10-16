package session

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrIssuerChanged reports a provider ID of the console sign-in whose
// issuer is not the issuer that the store recorded for it.
var ErrIssuerChanged = errors.New("the issuer of a sign-in provider changed")

// ProviderStore keeps the issuer of each provider ID of the console
// sign-in. The subject of a person holds the provider ID and the sub claim,
// not the issuer, so one provider ID must keep one issuer.
type ProviderStore interface {
	// BindProvider records issuer for the provider ID id at the time at,
	// when id has no record, and returns the issuer of the record: issuer
	// itself after the first call, or the issuer of an earlier call. It
	// writes no audit entry.
	BindProvider(ctx context.Context, id, issuer string, at time.Time) (string, error)
}

// issuerChangedError is the refusal of a provider ID whose issuer changed.
// Its text is one sentence that names the provider ID, both issuers and
// the fix.
type issuerChangedError struct {
	id, recorded, configured string
}

func (e issuerChangedError) Error() string {
	return fmt.Sprintf("sign-in provider %q has the issuer %q in the database and the issuer %q in the "+
		"configuration, so set a new provider ID for the new issuer", e.id, e.recorded, e.configured)
}

// Unwrap returns ErrIssuerChanged.
func (issuerChangedError) Unwrap() error { return ErrIssuerChanged }

// Provider is one provider of the console sign-in: its ID and its issuer.
type Provider struct {
	ID     string
	Issuer string
}

// BindProviders records the issuer of each provider of providers at the
// time at, and checks that no provider ID changed its issuer. It returns
// an error that matches ErrIssuerChanged, whose text is one sentence that
// names the provider ID, both issuers and the fix, for the first provider
// whose recorded issuer is another one.
func BindProviders(ctx context.Context, store ProviderStore, providers []Provider, at time.Time) error {
	for _, p := range providers {
		recorded, err := store.BindProvider(ctx, p.ID, p.Issuer, at)
		if err != nil {
			return fmt.Errorf("record the issuer of sign-in provider %q: %w", p.ID, err)
		}

		if recorded != p.Issuer {
			return issuerChangedError{id: p.ID, recorded: recorded, configured: p.Issuer}
		}
	}

	return nil
}
