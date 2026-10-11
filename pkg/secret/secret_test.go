package secret_test

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// gcmWrapper wraps data keys with a fixed key-encryption key.
type gcmWrapper struct {
	aead  cipher.AEAD
	id    string
	wraps atomic.Int32
}

func newWrapper(t *testing.T, id string) *gcmWrapper {
	t.Helper()

	kek := make([]byte, 32)
	if _, err := rand.Read(kek); err != nil {
		t.Fatalf("kek: %v", err)
	}

	block, err := aes.NewCipher(kek)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}

	return &gcmWrapper{aead: aead, id: id, wraps: atomic.Int32{}}
}

func (w *gcmWrapper) ID() string { return w.id }

func (w *gcmWrapper) Wrap(_ context.Context, key, aad []byte) ([]byte, error) {
	w.wraps.Add(1)

	nonce := make([]byte, w.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}

	return w.aead.Seal(nonce, nonce, key, aad), nil
}

func (w *gcmWrapper) Unwrap(_ context.Context, c, aad []byte) ([]byte, error) {
	n := w.aead.NonceSize()
	if len(c) < n {
		return nil, secret.ErrDecrypt
	}

	key, err := w.aead.Open(nil, c[:n], c[n:], aad)
	if err != nil {
		return nil, secret.ErrDecrypt
	}

	return key, nil
}

// memKeys is a KeyStore in memory.
type memKeys struct {
	keys map[secret.Scope]secret.WrappedKey
	mu   sync.Mutex
}

func (m *memKeys) CreateKey(_ context.Context, k secret.WrappedKey, _ namespace.Fence) (secret.WrappedKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, ok := m.keys[k.Scope]; ok {
		return existing, nil
	}

	m.keys[k.Scope] = k

	return k, nil
}

func (m *memKeys) GetKey(_ context.Context, scope secret.Scope) (secret.WrappedKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k, ok := m.keys[scope]
	if !ok {
		return secret.WrappedKey{}, secret.ErrKeyNotFound
	}

	return k, nil
}

func (m *memKeys) ListWrapperIDs(_ context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	seen := map[string]bool{}

	for _, k := range m.keys {
		seen[k.WrapperID] = true
	}

	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}

	slices.Sort(out)

	return out, nil
}

type oneNamespace struct{}

func (oneNamespace) CreateNamespace(context.Context, namespace.Namespace) error {
	return namespace.ErrAlreadyExists
}

func (oneNamespace) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	return namespace.Namespace{CreatedAt: time.Time{}, Name: name, HomeRegion: "local", FailoverVersion: 1}, nil
}

func (oneNamespace) ListNamespaces(_ context.Context) ([]namespace.Namespace, error) {
	return nil, nil
}

func (oneNamespace) Ping(_ context.Context) error {
	return nil
}

type fixture struct {
	svc     *secret.Service
	keys    *memKeys
	wrapper *gcmWrapper
}

func newFixture(t *testing.T, wrapperID string, keys *memKeys) fixture {
	t.Helper()

	namespaces, err := namespace.NewService(namespace.Config{Store: oneNamespace{}, Now: nil, Region: "local"})
	if err != nil {
		t.Fatalf("namespaces: %v", err)
	}

	if keys == nil {
		keys = &memKeys{keys: map[secret.Scope]secret.WrappedKey{}, mu: sync.Mutex{}}
	}

	w := newWrapper(t, wrapperID)

	svc, err := secret.NewService(secret.Config{
		Keys: keys, Wrapper: w, Namespaces: namespaces, Now: nil, MaxBatchBytes: 4 << 20,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return fixture{svc: svc, keys: keys, wrapper: w}
}

func scope(t *testing.T, id string) secret.Scope {
	t.Helper()

	s, err := secret.NewScope("acme", id)
	if err != nil {
		t.Fatalf("scope: %v", err)
	}

	return s
}

func TestRoundTripUsesOneKeyPerScope(t *testing.T) {
	t.Parallel()

	f := newFixture(t, "kek-1", nil)
	ctx := t.Context()
	dev := scope(t, "stack-1")
	plain := [][]byte{[]byte("s3cr3t"), {}, []byte("another")}

	for range 2 {
		sealed, err := f.svc.Encrypt(ctx, dev, plain)
		if err != nil {
			t.Fatalf("encrypt: %v", err)
		}

		if bytes.Equal(sealed[0], plain[0]) {
			t.Fatal("ciphertext equals plaintext")
		}

		opened, err := f.svc.Decrypt(ctx, dev, sealed)
		if err != nil {
			t.Fatalf("decrypt: %v", err)
		}

		for i := range plain {
			if !bytes.Equal(opened[i], plain[i]) {
				t.Errorf("value %d: got %q, want %q", i, opened[i], plain[i])
			}
		}
	}

	if got := f.wrapper.wraps.Load(); got != 1 {
		t.Errorf("wrapped %d data keys, want 1", got)
	}
}

func TestCiphertextIsBoundToItsScope(t *testing.T) {
	t.Parallel()

	f := newFixture(t, "kek-1", nil)
	ctx := t.Context()

	sealed, err := f.svc.Encrypt(ctx, scope(t, "stack-1"), [][]byte{[]byte("s3cr3t")})
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	if _, err := f.svc.Encrypt(ctx, scope(t, "stack-2"), [][]byte{[]byte("x")}); err != nil {
		t.Fatalf("encrypt other scope: %v", err)
	}

	if _, err := f.svc.Decrypt(ctx, scope(t, "stack-2"), sealed); !errors.Is(err, secret.ErrDecrypt) {
		t.Errorf("decrypt in another scope: %v, want ErrDecrypt", err)
	}

	tampered := bytes.Clone(sealed[0])
	tampered[len(tampered)-1] ^= 1

	tests := map[string][]byte{
		"tampered":        tampered,
		"unknown format":  append([]byte{9}, sealed[0][1:]...),
		"truncated":       sealed[0][:5],
		"empty":           {},
		"plaintext as is": []byte("s3cr3t"),
	}

	for name, c := range tests {
		if _, err := f.svc.Decrypt(ctx, scope(t, "stack-1"), [][]byte{c}); !errors.Is(err, secret.ErrDecrypt) {
			t.Errorf("%s: %v, want ErrDecrypt", name, err)
		}
	}
}

func TestDecryptNeedsTheSameWrapper(t *testing.T) {
	t.Parallel()

	first := newFixture(t, "kek-1", nil)
	ctx := t.Context()

	sealed, err := first.svc.Encrypt(ctx, scope(t, "stack-1"), [][]byte{[]byte("s3cr3t")})
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// Same stored keys, but a server holding a different key-encryption key.
	rotated := newFixture(t, "kek-2", first.keys)

	if _, err := rotated.svc.Decrypt(ctx, scope(t, "stack-1"), sealed); !errors.Is(err, secret.ErrDecrypt) {
		t.Errorf("decrypt with another wrapper: %v, want ErrDecrypt", err)
	}
}

func TestDecryptWithoutKeyAndLimits(t *testing.T) {
	t.Parallel()

	f := newFixture(t, "kek-1", nil)
	ctx := t.Context()

	if _, err := f.svc.Decrypt(ctx, scope(t, "never-used"), [][]byte{{1}}); !errors.Is(err, secret.ErrDecrypt) {
		t.Errorf("decrypt in an unused scope: %v, want ErrDecrypt", err)
	}

	_, err := f.svc.Encrypt(ctx, scope(t, "stack-1"), [][]byte{make([]byte, 1<<20+1)})
	if !errors.Is(err, secret.ErrTooLarge) {
		t.Errorf("oversized value: %v, want ErrTooLarge", err)
	}

	// The CLI bounds batches by size only; many small values are fine.
	if _, err = f.svc.Encrypt(ctx, scope(t, "stack-1"), make([][]byte, 5000)); err != nil {
		t.Errorf("5000 small values: %v", err)
	}

	big := bytes.Repeat([]byte{'x'}, 1<<20)
	_, err = f.svc.Encrypt(ctx, scope(t, "stack-1"), [][]byte{big, big, big, big, big})
	if !errors.Is(err, secret.ErrTooLarge) {
		t.Errorf("oversized batch: %v, want ErrTooLarge", err)
	}
}

// Every value Encrypt accepts must decrypt, including one at the size limit.
func TestLargestValueRoundTrips(t *testing.T) {
	t.Parallel()

	f := newFixture(t, "kek-1", nil)
	ctx := t.Context()
	largest := bytes.Repeat([]byte{'x'}, 1<<20)

	sealed, err := f.svc.Encrypt(ctx, scope(t, "stack-1"), [][]byte{largest})
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	opened, err := f.svc.Decrypt(ctx, scope(t, "stack-1"), sealed)
	if err != nil || !bytes.Equal(opened[0], largest) {
		t.Errorf("decrypt the largest value: %v", err)
	}
}

func TestNewScopeValidates(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		ns namespace.Name
		id string
	}{{ns: "", id: "stack-1"}, {ns: "acme", id: ""}, {ns: "acme", id: "a/b"}} {
		if _, err := secret.NewScope(tt.ns, tt.id); !errors.Is(err, secret.ErrInvalidScope) {
			t.Errorf("NewScope(%q, %q): %v, want ErrInvalidScope", tt.ns, tt.id, err)
		}
	}
}
