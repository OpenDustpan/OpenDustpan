package secret

import (
	"context"
	"crypto/rand"
	"fmt"
)

// wrapNewKey makes a new data key and returns it wrapped by wrapper under
// aad. The raw key never leaves this function: it is cleared before the
// return. what names the key in an error, for example "key for <scope>". All
// three key families (the service scopes, the installation scopes and the
// item keys) make their keys here.
func wrapNewKey(ctx context.Context, wrapper Wrapper, aad []byte, what string) ([]byte, error) {
	// Defense in depth: each caller checks for a wrapper before it calls. This
	// check stays so that a new caller cannot reach a nil wrapper.
	if wrapper == nil {
		return nil, fmt.Errorf("create %s: %w", what, ErrKeyNotConfigured)
	}

	key := make([]byte, dataKeyBytes)
	defer clear(key)

	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate %s: %w", what, err)
	}

	wrapped, err := wrapper.Wrap(ctx, key, aad)
	if err != nil {
		return nil, fmt.Errorf("wrap %s: %w", what, err)
	}

	return wrapped, nil
}

// sealedKey is a stored wrapped data key: its ciphertext and the ID of the
// wrapper that wrapped it.
type sealedKey struct {
	wrapperID  string
	ciphertext []byte
}

// unwrapKey returns the raw data key of stored, which its wrapper wrapped
// under aad. It refuses a key that another
// wrapper wrapped before it asks wrapper to unwrap anything, so the error
// names the two wrapper IDs and not a decrypt failure. what names the key in
// an error. The caller clears the key.
func unwrapKey(ctx context.Context, wrapper Wrapper, stored sealedKey, aad []byte, what string) ([]byte, error) {
	// Defense in depth: each caller checks for a wrapper before it reads the
	// store. This check stays so that a new caller cannot reach a nil wrapper.
	if wrapper == nil {
		return nil, fmt.Errorf("%w for %s: %w", ErrDecrypt, what, ErrKeyNotConfigured)
	}

	if stored.wrapperID != wrapper.ID() {
		return nil, fmt.Errorf("%w: %s is wrapped by %q, server holds %q",
			ErrDecrypt, what, stored.wrapperID, wrapper.ID())
	}

	key, err := wrapper.Unwrap(ctx, stored.ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("unwrap %s: %w", what, err)
	}

	return key, nil
}
