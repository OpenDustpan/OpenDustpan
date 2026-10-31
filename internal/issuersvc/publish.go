package issuersvc

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
)

// Files are the two files of an issuerURL, for a person to copy.
type Files struct {
	Discovery []byte
	JWKS      []byte
}

// Export answers the discovery document and the JWKS for the issuerURL of the
// namespace. It reads only: it never makes a key.
func (s *Service) Export(ctx context.Context, a *Actor, ns namespace.Name) (Files, error) {
	if err := s.require(ctx, a, ns, identity.ActionNamespaceManageIssuer); err != nil {
		return Files{}, err
	}

	set, err := s.store.GetIssuerSettings(ctx, ns)
	if err != nil {
		return Files{}, fmt.Errorf("read issuer settings: %w", err)
	}

	if set.IssuerURL == "" {
		return Files{}, errNoIssuerURL(ns)
	}

	keys, err := s.store.Published(ctx, ns)
	if err != nil {
		return Files{}, fmt.Errorf("read signing keys: %w", err)
	}

	if len(keys) == 0 {
		return Files{}, errNoKeys(ns)
	}

	return files(set.IssuerURL, keys)
}

func files(issuer string, keys []profile.Key) (Files, error) {
	set, err := profile.BuildJWKS(keys)
	if err != nil {
		return Files{}, fmt.Errorf("build JWKS: %w", err)
	}

	disc, err := marshal(profile.NewDiscovery(issuer))
	if err != nil {
		return Files{}, err
	}

	jwks, err := marshal(set)
	if err != nil {
		return Files{}, err
	}

	return Files{Discovery: disc, JWKS: jwks}, nil
}

// Discovery answers the discovery document of the namespace with the stable ID
// id. It needs no sign-in and holds public data only.
func (s *Service) Discovery(ctx context.Context, id namespace.ID) (profile.Discovery, error) {
	n, err := s.store.NamespaceByID(ctx, id)
	if err != nil {
		return profile.Discovery{}, fmt.Errorf("find namespace: %w", err)
	}

	set, err := s.store.GetIssuerSettings(ctx, n.Name)
	if err != nil {
		return profile.Discovery{}, fmt.Errorf("read issuer settings: %w", err)
	}

	url, err := s.issuerURL(&n, &set)
	if err != nil {
		return profile.Discovery{}, err
	}

	return profile.NewDiscovery(url), nil
}

// JWKS answers the public keys of the namespace with the stable ID id: the
// states next, active and retired. A namespace with no key answers an empty
// set.
func (s *Service) JWKS(ctx context.Context, id namespace.ID) (profile.JWKS, error) {
	n, err := s.store.NamespaceByID(ctx, id)
	if err != nil {
		return profile.JWKS{}, fmt.Errorf("find namespace: %w", err)
	}

	keys, err := s.store.Published(ctx, n.Name)
	if err != nil {
		return profile.JWKS{}, fmt.Errorf("read signing keys: %w", err)
	}

	out, err := profile.BuildJWKS(keys)
	if err != nil {
		return profile.JWKS{}, fmt.Errorf("build JWKS: %w", err)
	}

	return out, nil
}
