package secret_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/secret"
)

// memInstallationKeys is an InstallationKeyStore in memory.
type memInstallationKeys struct {
	keys map[string]secret.WrappedKey
	mu   sync.Mutex
}

func newMemInstallationKeys() *memInstallationKeys {
	return &memInstallationKeys{keys: map[string]secret.WrappedKey{}, mu: sync.Mutex{}}
}

func (m *memInstallationKeys) CreateInstallationKey(
	_ context.Context, k secret.WrappedKey,
) (secret.WrappedKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if k.Scope.Namespace != "" {
		return secret.WrappedKey{}, secret.ErrInvalidScope
	}

	if existing, ok := m.keys[k.Scope.ID]; ok {
		return existing, nil
	}

	m.keys[k.Scope.ID] = k

	return k, nil
}

func (m *memInstallationKeys) GetInstallationKey(_ context.Context, id string) (secret.WrappedKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k, ok := m.keys[id]
	if !ok {
		return secret.WrappedKey{}, secret.ErrKeyNotFound
	}

	return k, nil
}

func (m *memInstallationKeys) ListInstallationWrapperIDs(_ context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := []string{}

	for _, k := range m.keys {
		if !slices.Contains(out, k.WrapperID) {
			out = append(out, k.WrapperID)
		}
	}

	slices.Sort(out)

	return out, nil
}

func newInstallationSecrets(
	t *testing.T, keys secret.InstallationKeyStore, wrapper secret.Wrapper,
) *secret.InstallationSecrets {
	t.Helper()

	s, err := secret.NewInstallationSecrets(secret.InstallationConfig{Keys: keys, Wrapper: wrapper, Now: nil})
	if err != nil {
		t.Fatalf("installation secrets: %v", err)
	}

	return s
}

func TestInstallationSealRoundTripAndBinding(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	keys := newMemInstallationKeys()
	s := newInstallationSecrets(t, keys, newWrapper(t, "kek-1"))

	sealed, err := s.Seal(ctx, "sign-in-corp", []byte("client-secret-value"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	if bytes.Contains(sealed, []byte("client-secret-value")) {
		t.Fatal("the sealed value holds the plaintext")
	}

	opened, err := s.Open(ctx, "sign-in-corp", sealed)
	if err != nil || string(opened) != "client-secret-value" {
		t.Fatalf("open = %q, %v; want the plaintext", opened, err)
	}

	if _, sealErr := s.Seal(ctx, "sign-in-other", []byte("x")); sealErr != nil {
		t.Fatalf("seal in another scope: %v", sealErr)
	}

	if _, openErr := s.Open(ctx, "sign-in-other", sealed); !errors.Is(openErr, secret.ErrDecrypt) {
		t.Fatalf("open in another scope = %v, want ErrDecrypt", openErr)
	}

	ids, err := keys.ListInstallationWrapperIDs(ctx)
	if err != nil || !slices.Equal(ids, []string{"kek-1"}) {
		t.Fatalf("wrapper IDs = %v, %v; want [kek-1]", ids, err)
	}
}

// TestInstallationScopeIsNotANamespaceScope proves that the key of an
// installation scope is bound to "@server", a name that no namespace scope
// can have, so no namespace key opens as an installation key.
func TestInstallationScopeIsNotANamespaceScope(t *testing.T) {
	t.Parallel()

	wrapper := &recordingWrapper{inner: newWrapper(t, "kek-1"), aads: nil, mu: sync.Mutex{}}
	s := newInstallationSecrets(t, newMemInstallationKeys(), wrapper)

	if _, err := s.Seal(t.Context(), "stk-1", []byte("x")); err != nil {
		t.Fatalf("seal: %v", err)
	}

	if got, want := wrapper.lastAAD(), []byte("opendustpan/secret/v1\x00@server\x00stk-1"); !bytes.Equal(got, want) {
		t.Fatalf("installation associated data = %q, want %q", got, want)
	}

	if _, err := secret.NewScope("@server", "stk-1"); !errors.Is(err, secret.ErrInvalidScope) {
		t.Fatalf("namespace scope @server = %v, want ErrInvalidScope", err)
	}
}

func TestInstallationNeedsAWrapper(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	keys := newMemInstallationKeys()
	s := newInstallationSecrets(t, keys, nil)

	if _, err := s.Seal(ctx, "sign-in-corp", []byte("x")); !errors.Is(err, secret.ErrKeyNotConfigured) {
		t.Fatalf("seal with no wrapper = %v, want ErrKeyNotConfigured", err)
	}

	sealed, err := newInstallationSecrets(t, keys, newWrapper(t, "kek-1")).Seal(ctx, "sign-in-corp", []byte("x"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	other := newInstallationSecrets(t, keys, newWrapper(t, "kek-2"))
	if _, err := other.Open(ctx, "sign-in-corp", sealed); !errors.Is(err, secret.ErrDecrypt) {
		t.Fatalf("open with another key-encryption key = %v, want ErrDecrypt", err)
	}

	if err := secret.CheckInstallationWrapper(ctx, keys, newWrapper(t, "kek-2")); !errors.Is(
		err, secret.ErrWrapperMismatch,
	) {
		t.Fatalf("check with another wrapper = %v, want ErrWrapperMismatch", err)
	}

	if err := secret.CheckInstallationWrapper(ctx, keys, newWrapper(t, "kek-1")); err != nil {
		t.Fatalf("check with the wrapper: %v", err)
	}

	if err := secret.CheckInstallationWrapper(ctx, newMemInstallationKeys(), nil); err != nil {
		t.Fatalf("check of an empty store with no wrapper: %v", err)
	}
}

func TestInstallationLimitsAndScope(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newInstallationSecrets(t, newMemInstallationKeys(), newWrapper(t, "kek-1"))

	if _, err := s.Seal(ctx, "sign-in-corp", make([]byte, 1<<20+1)); !errors.Is(err, secret.ErrTooLarge) {
		t.Fatalf("seal of 1 MiB + 1 = %v, want ErrTooLarge", err)
	}

	if _, err := s.Seal(ctx, "bad id/", []byte("x")); !errors.Is(err, secret.ErrInvalidScope) {
		t.Fatalf("seal with a bad id = %v, want ErrInvalidScope", err)
	}

	if _, err := s.Open(ctx, "sign-in-none", []byte("x")); !errors.Is(err, secret.ErrDecrypt) {
		t.Fatalf("open with no key = %v, want ErrDecrypt", err)
	}

	_, err := secret.NewInstallationSecrets(secret.InstallationConfig{Keys: nil, Wrapper: nil, Now: nil})
	if !errors.Is(err, secret.ErrMissingDependency) {
		t.Fatalf("no key store = %v, want ErrMissingDependency", err)
	}
}
