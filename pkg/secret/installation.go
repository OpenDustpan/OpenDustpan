package secret

import (
	"context"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
)

// installationLabel names the installation in the associated data of an
// installation scope. No namespace name can start with "@", so an
// installation scope never shares its associated data with a namespace
// scope.
const installationLabel = "@server"

// InstallationKeyStore persists the wrapped data keys of installation
// scopes: keys for values that belong to the installation and to no
// namespace, for example the client secret of a sign-in provider. The Scope
// of each key has an empty Namespace. A write carries no fence: the
// installation has no home region.
type InstallationKeyStore interface {
	// CreateInstallationKey stores k unless a key for k.Scope.ID exists, and
	// returns the stored key either way, so concurrent first seals agree on
	// one key. It returns ErrInvalidScope for a scope with a namespace.
	CreateInstallationKey(ctx context.Context, k WrappedKey) (WrappedKey, error)
	// GetInstallationKey returns the key of the installation scope id, or
	// ErrKeyNotFound.
	GetInstallationKey(ctx context.Context, id string) (WrappedKey, error)
	// ListInstallationWrapperIDs returns the distinct wrapper IDs of the
	// stored installation keys, ordered, for the start-up check. It returns
	// an empty list when no key exists.
	ListInstallationWrapperIDs(ctx context.Context) ([]string, error)
}

// InstallationScope returns the scope of the installation value id. Its
// Namespace is empty.
func InstallationScope(id string) (Scope, error) {
	if !ident.Valid(id, maxScopeID) {
		return Scope{}, fmt.Errorf("%w: installation id %q", ErrInvalidScope, id)
	}

	return Scope{Namespace: "", ID: id}, nil
}

// installationAAD is the associated data that binds a key and its values
// to the installation scope id.
func installationAAD(id string) []byte {
	return []byte(aadLabel + "\x00" + installationLabel + "\x00" + id)
}

// InstallationConfig holds the dependencies of InstallationSecrets.
type InstallationConfig struct {
	Keys InstallationKeyStore
	// Wrapper holds the key-encryption key. It may be nil when no key is
	// configured; Seal and Open then fail with ErrKeyNotConfigured.
	Wrapper Wrapper
	Now     func() time.Time
}

// InstallationSecrets seals and opens values of installation scopes, each
// under its own data key, as Service does for namespace scopes.
type InstallationSecrets struct {
	keys    InstallationKeyStore
	wrapper Wrapper
	now     func() time.Time
}

// NewInstallationSecrets returns InstallationSecrets. Keys is required.
func NewInstallationSecrets(cfg InstallationConfig) (*InstallationSecrets, error) {
	if cfg.Keys == nil {
		return nil, fmt.Errorf("%w: installation key store", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &InstallationSecrets{keys: cfg.Keys, wrapper: cfg.Wrapper, now: now}, nil
}

// Seal seals plaintext under the data key of the installation scope id,
// and creates the key at its first use. plaintext is at most 1 MiB.
func (s *InstallationSecrets) Seal(ctx context.Context, id string, plaintext []byte) ([]byte, error) {
	if len(plaintext) > maxValueBytes {
		return nil, fmt.Errorf("%w: value has %d bytes, limit %d", ErrTooLarge, len(plaintext), maxValueBytes)
	}

	aead, err := s.dataKey(ctx, id, true)
	if err != nil {
		return nil, err
	}

	return sealValue(aead, installationAAD(id), plaintext)
}

// Open opens a ciphertext that Seal made for the same installation scope.
// Any failure is ErrDecrypt.
func (s *InstallationSecrets) Open(ctx context.Context, id string, ciphertext []byte) ([]byte, error) {
	if len(ciphertext) > maxValueBytes+sealOverhead {
		return nil, fmt.Errorf("%w: value has %d bytes", ErrTooLarge, len(ciphertext))
	}

	aead, err := s.dataKey(ctx, id, false)
	if err != nil {
		return nil, err
	}

	p, ok := openValue(aead, installationAAD(id), ciphertext)
	if !ok {
		return nil, fmt.Errorf("%w: value of installation scope %s", ErrDecrypt, id)
	}

	return p, nil
}

// dataKey returns the data key of the installation scope id as an AEAD,
// and creates it when create is set and none exists yet.
func (s *InstallationSecrets) dataKey(ctx context.Context, id string, create bool) (cipher.AEAD, error) {
	scope, err := InstallationScope(id)
	if err != nil {
		return nil, err
	}

	if s.wrapper == nil {
		return nil, fmt.Errorf("%w for installation scope %s: %w", ErrDecrypt, id, ErrKeyNotConfigured)
	}

	stored, err := s.keys.GetInstallationKey(ctx, id)

	switch {
	case errors.Is(err, ErrKeyNotFound) && create:
		stored, err = s.createKey(ctx, scope)
		if err != nil {
			return nil, err
		}
	case errors.Is(err, ErrKeyNotFound):
		return nil, fmt.Errorf("%w: no key for installation scope %s", ErrDecrypt, id)
	case err != nil:
		return nil, fmt.Errorf("get key for installation scope %s: %w", id, err)
	default:
	}

	if stored.WrapperID != s.wrapper.ID() {
		return nil, fmt.Errorf("%w: key for installation scope %s is wrapped by %q, server holds %q",
			ErrDecrypt, id, stored.WrapperID, s.wrapper.ID())
	}

	key, err := s.wrapper.Unwrap(ctx, stored.Ciphertext, installationAAD(id))
	if err != nil {
		return nil, fmt.Errorf("unwrap key for installation scope %s: %w", id, err)
	}

	defer clear(key)

	return newAEAD(key)
}

func (s *InstallationSecrets) createKey(ctx context.Context, scope Scope) (WrappedKey, error) {
	key := make([]byte, dataKeyBytes)
	defer clear(key)

	if _, err := rand.Read(key); err != nil {
		return WrappedKey{}, fmt.Errorf("generate key: %w", err)
	}

	wrapped, err := s.wrapper.Wrap(ctx, key, installationAAD(scope.ID))
	if err != nil {
		return WrappedKey{}, fmt.Errorf("wrap key for installation scope %s: %w", scope.ID, err)
	}

	stored, err := s.keys.CreateInstallationKey(ctx, WrappedKey{
		CreatedAt:  s.now().UTC(),
		Scope:      scope,
		WrapperID:  s.wrapper.ID(),
		Ciphertext: wrapped,
	})
	if err != nil {
		return WrappedKey{}, fmt.Errorf("store key for installation scope %s: %w", scope.ID, err)
	}

	return stored, nil
}

// CheckInstallationWrapper is CheckWrapper for the installation keys: it
// returns nil when keys holds no key, and else needs wrapper to name every
// stored wrapper ID.
func CheckInstallationWrapper(ctx context.Context, keys InstallationKeyStore, wrapper Wrapper) error {
	if keys == nil {
		return fmt.Errorf("%w: installation key store", ErrMissingDependency)
	}

	ids, err := keys.ListInstallationWrapperIDs(ctx)
	if err != nil {
		return fmt.Errorf("list wrapped installation keys: %w", err)
	}

	return checkWrapperIDs(ids, wrapper)
}

// OpenInstallationKey is OpenKey for the key of an installation scope: it
// binds the ciphertext to the installation scope of k. It returns
// ErrInvalidScope for a key with a namespace, and ErrDecrypt when wrapper
// does not open k.
func OpenInstallationKey(ctx context.Context, wrapper Wrapper, k WrappedKey) ([]byte, error) {
	if k.Scope.Namespace != "" {
		return nil, fmt.Errorf("%w: installation key of namespace %q", ErrInvalidScope, k.Scope.Namespace)
	}

	if wrapper == nil {
		return nil, fmt.Errorf("open installation key of %s: %w", k.Scope.ID, ErrKeyNotConfigured)
	}

	key, err := wrapper.Unwrap(ctx, k.Ciphertext, installationAAD(k.Scope.ID))
	if err != nil {
		return nil, fmt.Errorf("open installation key of %s: %w", k.Scope.ID, err)
	}

	return key, nil
}
