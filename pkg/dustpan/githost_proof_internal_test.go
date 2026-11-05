package dustpan

import (
	"context"
	"crypto/rand"
	"errors"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/secret"
)

// proofKeys is a secret.InstallationKeyStore in memory.
type proofKeys struct {
	mu   sync.Mutex
	keys map[string]secret.WrappedKey
}

func (m *proofKeys) CreateInstallationKey(_ context.Context, k secret.WrappedKey) (secret.WrappedKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if old, ok := m.keys[k.Scope.ID]; ok {
		return old, nil
	}

	m.keys[k.Scope.ID] = k

	return k, nil
}

func (m *proofKeys) GetInstallationKey(_ context.Context, id string) (secret.WrappedKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k, ok := m.keys[id]
	if !ok {
		return secret.WrappedKey{}, secret.ErrKeyNotFound
	}

	return k, nil
}

func (m *proofKeys) ListInstallationWrapperIDs(context.Context) ([]string, error) { return nil, nil }

// newProofSecrets returns installation secrets with a new key-encryption key.
func newProofSecrets(t *testing.T) *secret.InstallationSecrets {
	t.Helper()

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}

	wrapper, err := secret.NewBuiltinWrapper(key)
	if err != nil {
		t.Fatal(err)
	}

	return proofSecretsWith(t, wrapper)
}

// proofSecretsWith returns installation secrets with the wrapper, which may be
// nil: the secrets then have no key-encryption key.
func proofSecretsWith(t *testing.T, wrapper secret.Wrapper) *secret.InstallationSecrets {
	t.Helper()

	s, err := secret.NewInstallationSecrets(secret.InstallationConfig{
		Keys: &proofKeys{mu: sync.Mutex{}, keys: map[string]secret.WrappedKey{}}, Wrapper: wrapper, Now: nil,
	})
	if err != nil {
		t.Fatal(err)
	}

	return s
}

// TestCommitProverOpensOnlyItsOwnProof checks the Prover of the server: a proof
// opens for the binding that it was sealed for, and for no other value.
func TestCommitProverOpensOnlyItsOwnProof(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	secrets := newProofSecrets(t)
	p := commitProver{secrets: secrets}
	binding := []byte("repository 7\x00Dustpan-Job: j-1\x00base")

	proof, err := p.Seal(ctx, binding)
	if err != nil || len(proof) == 0 {
		t.Fatalf("Seal = %x, %v", proof, err)
	}

	// A value sealed with the right binding and another text, and a value
	// sealed in another scope with the same text, are not proofs.
	otherText, err := secrets.SealBound(ctx, commitProofScope, binding, []byte("another text"))
	if err != nil {
		t.Fatal(err)
	}

	otherScope, err := secrets.SealBound(ctx, "other-scope", binding, []byte(commitProofText))
	if err != nil {
		t.Fatal(err)
	}

	unbound, err := secrets.Seal(ctx, commitProofScope, []byte(commitProofText))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		binding []byte
		proof   []byte
		want    bool
	}{
		{"the proof for its binding", binding, proof, true},
		{"another binding", []byte("repository 8\x00Dustpan-Job: j-1\x00base"), proof, false},
		{"a prefix of the binding", binding[:len(binding)-1], proof, false},
		{"the binding and one more byte", append(append([]byte{}, binding...), 'x'), proof, false},
		{"a proof with a changed byte", binding, append([]byte{proof[0] ^ 1}, proof[1:]...), false},
		{"a proof that is cut", binding, proof[:len(proof)-1], false},
		{"an empty proof", binding, nil, false},
		{"text that is not a sealed value", binding, []byte("not a proof"), false},
		{"another text under the same binding", binding, otherText, false},
		{"the same text in another scope", binding, otherScope, false},
		{"the same text with no binding", binding, unbound, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, openErr := p.Open(ctx, tt.binding, tt.proof)
			if openErr != nil || got != tt.want {
				t.Fatalf("Open = %v, %v; want %v and no error", got, openErr, tt.want)
			}
		})
	}
}

// TestCommitProverFailsWithNoKey checks that a server with no key-encryption
// key neither seals a proof nor says that a proof is good.
func TestCommitProverFailsWithNoKey(t *testing.T) {
	t.Parallel()

	p := commitProver{secrets: proofSecretsWith(t, nil)}

	if _, err := p.Seal(t.Context(), []byte("binding")); !errors.Is(err, secret.ErrKeyNotConfigured) {
		t.Fatalf("Seal = %v, want ErrKeyNotConfigured", err)
	}

	// The secret package reports each failure to open as a value that does not
	// open, so Open is false, with or without an error. It is never true.
	if ok, err := p.Open(t.Context(), []byte("binding"), []byte("proof")); ok {
		t.Fatalf("Open = %v, %v; want false", ok, err)
	}
}
