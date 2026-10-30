package issuersvc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
	"opendustpan.org/opendustpan/pkg/secret"
)

func (s *Service) scope(ns namespace.Name) (secret.Scope, error) {
	sc, err := secret.NewScope(ns, scopeID)
	if err != nil {
		return secret.Scope{}, fmt.Errorf("issuer scope: %w", err)
	}

	return sc, nil
}

// newKey makes a key of state in ns at the time at. The private key leaves this
// function only as a sealed value.
func (s *Service) newKey(
	ctx context.Context,
	ns namespace.Name,
	state profile.KeyState,
	at time.Time,
) (profile.Key, error) {
	sc, err := s.scope(ns)
	if err != nil {
		return profile.Key{}, err
	}

	priv, err := rsa.GenerateKey(rand.Reader, profile.KeyBits)
	if err != nil {
		return profile.Key{}, fmt.Errorf("generate signing key: %w", err)
	}

	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return profile.Key{}, fmt.Errorf("encode signing key: %w", err)
	}
	defer clear(der)

	sealed, err := s.sealer.Encrypt(ctx, sc, [][]byte{der})
	if err != nil {
		return profile.Key{}, fmt.Errorf("seal signing key: %w", err)
	}

	pub, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return profile.Key{}, fmt.Errorf("encode public key: %w", err)
	}

	k := profile.Key{
		CreatedAt: at, ActivatedAt: time.Time{}, RetiredAt: time.Time{}, DeletedAt: time.Time{},
		ID: profile.NewKeyID(), Namespace: ns, State: state, Public: pub, Sealed: sealed[0],
	}
	if state == profile.KeyActive {
		k.ActivatedAt = at
	}

	return k, nil
}

// errKeyMismatch reports a stored private key that does not belong to the
// public key of its row, or that is not a key of the issuer.
var errKeyMismatch = errors.New("the stored signing key does not match its public key")

// cached returns the opened key of k from the memory of the instance, and
// drops each entry that is older than keyCacheTime.
func (s *Service) cached(k *profile.Key, sum [sha256.Size]byte, now time.Time) (*rsa.PrivateKey, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for key, c := range s.cache {
		if now.Sub(c.at) >= keyCacheTime || now.Before(c.at) {
			delete(s.cache, key)
		}
	}

	c, ok := s.cache[cacheKey{ns: k.Namespace, id: k.ID}]
	if !ok || c.sealed != sum {
		return nil, false
	}

	return c.key, true
}

// open returns the private key of k. It keeps the key for keyCacheTime. Only
// the signer calls it.
func (s *Service) open(ctx context.Context, k *profile.Key) (*rsa.PrivateKey, error) {
	sum := sha256.Sum256(k.Sealed)
	now := s.now()

	if key, ok := s.cached(k, sum, now); ok {
		return key, nil
	}

	priv, err := s.unseal(ctx, k)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.cache[cacheKey{ns: k.Namespace, id: k.ID}] = cachedKey{at: now, key: priv, sealed: sum}
	s.mu.Unlock()

	return priv, nil
}

// unseal opens the sealed private key of k and checks it against the public
// key of the row.
func (s *Service) unseal(ctx context.Context, k *profile.Key) (*rsa.PrivateKey, error) {
	sc, err := s.scope(k.Namespace)
	if err != nil {
		return nil, err
	}

	plain, err := s.sealer.Decrypt(ctx, sc, [][]byte{k.Sealed})
	if err != nil {
		return nil, fmt.Errorf("open signing key %s: %w", k.ID, err)
	}
	defer clear(plain[0])

	parsed, err := x509.ParsePKCS8PrivateKey(plain[0])
	if err != nil {
		return nil, fmt.Errorf("%w: %s: parse", errKeyMismatch, k.ID)
	}

	priv, ok := parsed.(*rsa.PrivateKey)
	if !ok || priv.N.BitLen() != profile.KeyBits {
		return nil, fmt.Errorf("%w: %s: not an RSA key of %d bits", errKeyMismatch, k.ID, profile.KeyBits)
	}

	// Defense in depth: the sealed value is bound to the namespace by its
	// scope. This check also binds it to its row, so a sealed value that is
	// copied from another key of the namespace never signs under this kid.
	pub, err := k.PublicKey()
	if err != nil || !priv.PublicKey.Equal(pub) {
		return nil, fmt.Errorf("%w: %s", errKeyMismatch, k.ID)
	}

	return priv, nil
}

// forget drops every opened key of ns from the memory of the instance.
func (s *Service) forget(ns namespace.Name) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for key := range s.cache {
		if key.ns == ns {
			delete(s.cache, key)
		}
	}
}

// sortedKeys returns the keys by creation time, oldest first.
func sortedKeys(keys []profile.Key) []profile.Key {
	out := slices.Clone(keys)
	slices.SortStableFunc(out, func(a, b profile.Key) int { return a.CreatedAt.Compare(b.CreatedAt) })

	return out
}

func findState(keys []profile.Key, st profile.KeyState) (profile.Key, bool) {
	sorted := sortedKeys(keys)

	for i := range sorted {
		if sorted[i].State == st {
			return sorted[i], true
		}
	}

	return profile.Key{}, false
}
