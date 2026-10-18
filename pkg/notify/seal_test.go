package notify_test

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/secret"
)

// The secret services are the sealers of a SinkSealer.
var (
	_ notify.ScopeSealer        = (*secret.Service)(nil)
	_ notify.InstallationSealer = (*secret.InstallationSecrets)(nil)
)

// scopeSealer seals values with one AES-GCM key and the scope as the
// associated data, so a value opens only for the scope that sealed it, as
// secret.Service does.
type scopeSealer struct {
	aead cipher.AEAD
}

func newScopeSealer(t *testing.T) scopeSealer {
	t.Helper()

	block, err := aes.NewCipher(randomKey(t))
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}

	return scopeSealer{aead: aead}
}

func (s scopeSealer) Encrypt(_ context.Context, scope secret.Scope, ps [][]byte) ([][]byte, error) {
	out := make([][]byte, 0, len(ps))

	for _, p := range ps {
		nonce := make([]byte, s.aead.NonceSize())
		if _, err := rand.Read(nonce); err != nil {
			return nil, fmt.Errorf("nonce: %w", err)
		}

		out = append(out, s.aead.Seal(nonce, nonce, p, []byte(scope.String())))
	}

	return out, nil
}

func (s scopeSealer) Decrypt(_ context.Context, scope secret.Scope, cs [][]byte) ([][]byte, error) {
	out := make([][]byte, 0, len(cs))

	for _, c := range cs {
		n := s.aead.NonceSize()
		if len(c) < n {
			return nil, secret.ErrDecrypt
		}

		p, err := s.aead.Open(nil, c[:n], c[n:], []byte(scope.String()))
		if err != nil {
			return nil, secret.ErrDecrypt
		}

		out = append(out, p)
	}

	return out, nil
}

// installationKeys is a secret.InstallationKeyStore in memory.
type installationKeys struct {
	mu   sync.Mutex
	keys map[string]secret.WrappedKey
}

func (m *installationKeys) CreateInstallationKey(_ context.Context, k secret.WrappedKey) (secret.WrappedKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if old, ok := m.keys[k.Scope.ID]; ok {
		return old, nil
	}

	m.keys[k.Scope.ID] = k

	return k, nil
}

func (m *installationKeys) GetInstallationKey(_ context.Context, id string) (secret.WrappedKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k, ok := m.keys[id]
	if !ok {
		return secret.WrappedKey{}, secret.ErrKeyNotFound
	}

	return k, nil
}

func (m *installationKeys) ListInstallationWrapperIDs(context.Context) ([]string, error) {
	return nil, nil
}

func randomKey(t *testing.T) []byte {
	t.Helper()

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("key: %v", err)
	}

	return key
}

// newSinkSealer returns a SinkSealer with the scope sealer above and the
// installation secrets of the secret package.
func newSinkSealer(t *testing.T) notify.SinkSealer {
	t.Helper()

	wrapper, err := secret.NewBuiltinWrapper(randomKey(t))
	if err != nil {
		t.Fatalf("NewBuiltinWrapper: %v", err)
	}

	inst, err := secret.NewInstallationSecrets(secret.InstallationConfig{
		Keys:    &installationKeys{mu: sync.Mutex{}, keys: map[string]secret.WrappedKey{}},
		Wrapper: wrapper,
		Now:     nil,
	})
	if err != nil {
		t.Fatalf("NewInstallationSecrets: %v", err)
	}

	return notify.SinkSealer{Namespaces: newScopeSealer(t), Installation: inst}
}

func sealSink(id string, scope namespace.Name, refs ...string) notify.Sink {
	return notify.Sink{
		ID: id, Scope: scope, Kind: notify.SinkWebhook, Name: "Ops", Target: "https://hooks.example.com/ops",
		SecretRefs: refs, CreatedBy: "alice", Version: 0,
	}
}

func plain(ref, v string) notify.SinkSecret {
	return notify.SinkSecret{Ref: ref, Value: []byte(v)}
}

// TestSinkSealerRoundTrip checks that a sealed value holds no plaintext,
// and that Open gives the values back in the order of the refs, for a
// sink of a namespace and of the server scope.
func TestSinkSealerRoundTrip(t *testing.T) {
	t.Parallel()

	for _, scope := range []namespace.Name{"shop", "@server"} {
		t.Run(scope.String(), func(t *testing.T) {
			t.Parallel()

			b := newSinkSealer(t)
			s := sealSink("snk-1", scope, "itm-1", "itm-2")

			sealed, err := b.Seal(t.Context(), s, []notify.SinkSecret{
				plain("itm-2", "plaintext key two"), plain("itm-1", "plaintext key one"),
			})
			if err != nil {
				t.Fatalf("Seal: %v", err)
			}

			checkSealed(t, s, sealed)

			got, err := b.Open(t.Context(), s, []notify.SealedSecret{sealed[1], sealed[0]})
			if err != nil {
				t.Fatalf("Open: %v", err)
			}

			if fmt.Sprintf("%s", got) != "[plaintext key one plaintext key two]" {
				t.Fatalf("Open = %s, want the two keys in the order of the refs", got)
			}
		})
	}
}

// checkSealed checks that each item of sealed is sealed for s and holds no
// plaintext.
func checkSealed(t *testing.T, s notify.Sink, sealed []notify.SealedSecret) {
	t.Helper()

	for _, sec := range sealed {
		if bytes.Contains(sec.Ciphertext(), []byte("plaintext")) || sec.Scope() != s.Scope || sec.Sink() != s.ID {
			t.Fatalf("sealed %s = %q for %s/%s, want no plaintext, for %s/%s", sec.Ref(), sec.Ciphertext(),
				sec.Scope(), sec.Sink(), s.Scope, s.ID)
		}
	}

	if err := s.ValidateSealed(sealed); err != nil {
		t.Fatalf("ValidateSealed: %v", err)
	}
}

// TestSinkSealerRefuses checks that a value sealed for one sink, scope or
// ref does not open for another, also when a store gives it back with the
// names of the other.
func TestSinkSealerRefuses(t *testing.T) {
	t.Parallel()

	b := newSinkSealer(t)
	s := sealSink("snk-1", "shop", "itm-1", "itm-2")

	sealed, err := b.Seal(t.Context(), s, []notify.SinkSecret{plain("itm-1", "one"), plain("itm-2", "two")})
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	one, two := sealed[0].Ciphertext(), sealed[1].Ciphertext()
	moved := sealSink("snk-2", "shop", "itm-1", "itm-2")
	elsewhere := sealSink("snk-1", "other", "itm-1", "itm-2")
	server := sealSink("snk-1", "@server", "itm-1", "itm-2")

	cases := []struct {
		name   string
		s      notify.Sink
		sealed []notify.SealedSecret
		want   error
	}{
		{"the values of another sink", moved, sealed, notify.ErrInvalid},
		{"the values of another scope", elsewhere, sealed, notify.ErrInvalid},
		{"one value", s, sealed[:1], notify.ErrInvalid},
		{"the values in another order", s, []notify.SealedSecret{sealed[1], sealed[0]}, notify.ErrInvalid},
		{"the values of another sink, stored for this sink", moved, []notify.SealedSecret{
			notify.StoredSecret("shop", "snk-2", "itm-1", one), notify.StoredSecret("shop", "snk-2", "itm-2", two),
		}, secret.ErrDecrypt},
		{"the values of another scope, stored for this scope", elsewhere, []notify.SealedSecret{
			notify.StoredSecret("other", "snk-1", "itm-1", one), notify.StoredSecret("other", "snk-1", "itm-2", two),
		}, secret.ErrDecrypt},
		{
			"the values of a namespace, stored for the server scope",
			server,
			[]notify.SealedSecret{
				notify.StoredSecret(
					"@server",
					"snk-1",
					"itm-1",
					one,
				),
				notify.StoredSecret("@server", "snk-1", "itm-2", two),
			},
			secret.ErrDecrypt,
		},
		{"two values under each other's ref", s, []notify.SealedSecret{
			notify.StoredSecret("shop", "snk-1", "itm-1", two), notify.StoredSecret("shop", "snk-1", "itm-2", one),
		}, secret.ErrDecrypt},
		{"a plaintext value", s, []notify.SealedSecret{
			notify.StoredSecret("shop", "snk-1", "itm-1", []byte("one")),
			notify.StoredSecret("shop", "snk-1", "itm-2", []byte("two")),
		}, secret.ErrDecrypt},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got, err := b.Open(t.Context(), tc.s, tc.sealed); !errors.Is(err, tc.want) {
				t.Fatalf("Open = %s, %v, want %v", got, err, tc.want)
			}
		})
	}
}

// TestSinkSealerSeal checks that Seal validates the plaintext items, and
// fails with no sealer for the scope of the sink.
func TestSinkSealerSeal(t *testing.T) {
	t.Parallel()

	s := sealSink("snk-1", "shop", "itm-1")

	_, err := newSinkSealer(t).Seal(t.Context(), s, []notify.SinkSecret{plain("itm-2", "v")})
	if !errors.Is(err, notify.ErrInvalid) {
		t.Fatalf("Seal of a ref that the sink does not name: err = %v, want ErrInvalid", err)
	}

	empty := notify.SinkSealer{Namespaces: nil, Installation: nil}

	for _, scope := range []namespace.Name{"shop", "@server"} {
		secrets := []notify.SinkSecret{plain("itm-1", "v")}
		if _, err := empty.Seal(t.Context(), sealSink("snk-1", scope, "itm-1"), secrets); err == nil {
			t.Fatalf("Seal in %s with no sealer: err = nil, want an error", scope)
		}
	}
}

func TestValidateSealed(t *testing.T) {
	t.Parallel()

	s := sealSink("snk-1", "shop", "itm-1", "itm-2")
	stored := func(scope namespace.Name, sink, ref string, n int) notify.SealedSecret {
		return notify.StoredSecret(scope, sink, ref, []byte(strings.Repeat("c", n)))
	}

	cases := []struct {
		name    string
		secrets []notify.SealedSecret
		ok      bool
	}{
		{"no new secret", nil, true},
		{"one value for each ref", []notify.SealedSecret{
			stored("shop", "snk-1", "itm-1", 60), stored("shop", "snk-1", "itm-2", 60),
		}, true},
		{"a value at the bound", []notify.SealedSecret{stored("shop", "snk-1", "itm-1", notify.MaxSealedLen)}, true},
		{"a value of another sink", []notify.SealedSecret{stored("shop", "snk-2", "itm-1", 60)}, false},
		{"a value of another scope", []notify.SealedSecret{stored("other", "snk-1", "itm-1", 60)}, false},
		{"a ref that the sink does not name", []notify.SealedSecret{stored("shop", "snk-1", "itm-3", 60)}, false},
		{"two values for one ref", []notify.SealedSecret{
			stored("shop", "snk-1", "itm-1", 60), stored("shop", "snk-1", "itm-1", 60),
		}, false},
		{"an empty value", []notify.SealedSecret{stored("shop", "snk-1", "itm-1", 0)}, false},
		{"a value above the bound", []notify.SealedSecret{
			stored("shop", "snk-1", "itm-1", notify.MaxSealedLen+1),
		}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := s.ValidateSealed(tc.secrets)
			if tc.ok != (err == nil) || (!tc.ok && !errors.Is(err, notify.ErrInvalid)) {
				t.Fatalf("ValidateSealed = %v, want valid %t", err, tc.ok)
			}
		})
	}
}
