package secrettest

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// streamWrapper is a Wrapper with a fresh key-encryption key for one test.
type streamWrapper struct {
	aead cipher.AEAD
	id   string
}

func newStreamWrapper(t *testing.T) *streamWrapper {
	t.Helper()

	kek := make([]byte, 32)
	if _, err := rand.Read(kek); err != nil {
		t.Fatalf("generate key-encryption key: %v", err)
	}

	block, err := aes.NewCipher(kek)
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("new GCM: %v", err)
	}

	return &streamWrapper{aead: aead, id: "kek-test"}
}

func (w *streamWrapper) ID() string { return w.id }

func (w *streamWrapper) Wrap(_ context.Context, key, aad []byte) ([]byte, error) {
	nonce := make([]byte, w.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	return w.aead.Seal(nonce, nonce, key, aad), nil
}

func (w *streamWrapper) Unwrap(_ context.Context, sealed, aad []byte) ([]byte, error) {
	n := w.aead.NonceSize()
	if len(sealed) < n {
		return nil, secret.ErrDecrypt
	}

	key, err := w.aead.Open(nil, sealed[:n], sealed[n:], aad)
	if err != nil {
		return nil, secret.ErrDecrypt
	}

	return key, nil
}

// streamService builds a secret Service over s with a fresh wrapper.
func streamService(t *testing.T, s Store) *secret.Service {
	t.Helper()

	namespaces, err := namespace.NewService(namespace.Config{Store: s, Now: nil, Region: "local"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	wrapper := newStreamWrapper(t)

	svc, err := secret.NewService(secret.Config{
		Keys: s, Wrapper: wrapper, Namespaces: namespaces, Now: nil, MaxBatchBytes: 0,
	})
	if err != nil {
		t.Fatalf("secret service: %v", err)
	}

	return svc
}

// streamScope returns the scope of namespace acme and id.
func streamScope(t *testing.T, id string) secret.Scope {
	t.Helper()

	ns, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("parse namespace: %v", err)
	}

	scope, err := secret.NewScope(ns, id)
	if err != nil {
		t.Fatalf("new scope: %v", err)
	}

	return scope
}

// streamNamespace creates the acme namespace for stream tests, homed in the
// region of the stream service.
func streamNamespace(t *testing.T, s Store) {
	t.Helper()

	ns := namespace.Namespace{
		CreatedAt:       time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC),
		Name:            "acme",
		HomeRegion:      "local",
		FailoverVersion: 1,
	}

	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
}

// sealAll seals plain and returns the sealed bytes.
func sealAll(t *testing.T, svc *secret.Service, scope secret.Scope, plain []byte) []byte {
	t.Helper()

	sealed, err := svc.SealStream(t.Context(), scope, bytes.NewReader(plain))
	if err != nil {
		t.Fatalf("seal stream: %v", err)
	}

	out, err := io.ReadAll(sealed)
	if err != nil {
		t.Fatalf("read sealed stream: %v", err)
	}

	return out
}

// openAll opens sealed and returns the plaintext.
func openAll(t *testing.T, svc *secret.Service, scope secret.Scope, sealed []byte) []byte {
	t.Helper()

	opened, err := svc.OpenStream(t.Context(), scope, bytes.NewReader(sealed))
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}

	plain, err := io.ReadAll(opened)
	if err != nil {
		t.Fatalf("read opened stream: %v", err)
	}

	return plain
}

// testSealStreamRoundTrip seals content across chunk borders and opens it.
func testSealStreamRoundTrip(t *testing.T, s Store) {
	t.Helper()
	streamNamespace(t, s)

	svc := streamService(t, s)
	scope := streamScope(t, "scope-1")

	for _, size := range []int{0, 1, 100, 64 << 10, (64 << 10) + 7, 3 * (64 << 10)} {
		plain := make([]byte, size)
		if _, err := rand.Read(plain); err != nil {
			t.Fatalf("generate plaintext: %v", err)
		}

		if got := openAll(t, svc, scope, sealAll(t, svc, scope, plain)); !bytes.Equal(got, plain) {
			t.Fatalf("round trip of %d bytes differs", size)
		}
	}
}

// testSealStreamBindsScope proves one scope cannot open another's stream.
func testSealStreamBindsScope(t *testing.T, s Store) {
	t.Helper()
	streamNamespace(t, s)

	svc := streamService(t, s)
	first := streamScope(t, "scope-1")
	second := streamScope(t, "scope-2")

	sealed := sealAll(t, svc, first, []byte("sensitive state"))

	opened, err := svc.OpenStream(t.Context(), second, bytes.NewReader(sealed))
	if err != nil {
		// No key exists for the other scope: the open fails closed.
		if !errors.Is(err, secret.ErrDecrypt) {
			t.Fatalf("open stream in another scope: err = %v, want ErrDecrypt", err)
		}

		return
	}

	if _, err := io.ReadAll(opened); err == nil {
		t.Fatal("open in another scope succeeded, want ErrDecrypt")
	} else if !errors.Is(err, secret.ErrDecrypt) {
		t.Fatalf("open in another scope: err = %v, want ErrDecrypt", err)
	}
}

// testSealStreamTamper proves a changed byte, a dropped last chunk and
// swapped chunks each fail, with no key, scope or content in the error.
func testSealStreamTamper(t *testing.T, s Store) {
	t.Helper()
	streamNamespace(t, s)

	svc := streamService(t, s)
	scope := streamScope(t, "scope-1")

	plain := bytes.Repeat([]byte("sensitive-state-value"), 8000)
	sealed := sealAll(t, svc, scope, plain)

	mutants := map[string][]byte{}

	changed := bytes.Clone(sealed)
	changed[len(changed)/2] ^= 1
	mutants["changed byte"] = changed

	if len(sealed) > 100 {
		mutants["dropped tail"] = bytes.Clone(sealed[:len(sealed)-10])
	}

	if swapped := swapSealedChunks(sealed); swapped != nil {
		mutants["swapped chunks"] = swapped
	}

	mutants["extended"] = append(bytes.Clone(sealed), 0x00)

	for name, bad := range mutants {
		opened, err := svc.OpenStream(t.Context(), scope, bytes.NewReader(bad))
		if err != nil {
			t.Fatalf("%s: open stream: %v", name, err)
		}

		_, err = io.ReadAll(opened)
		if err == nil {
			t.Errorf("%s: tampered stream opened, want ErrDecrypt", name)

			continue
		}

		if !errors.Is(err, secret.ErrDecrypt) {
			t.Errorf("%s: err = %v, want ErrDecrypt", name, err)
		}

		assertNoLeak(t, name, err, []string{"sensitive-state-value", "scope-1", "acme"})
	}
}

// swapSealedChunks swaps the first two frames of sealed, or returns nil when
// the stream holds less than two frames.
func swapSealedChunks(sealed []byte) []byte {
	const header = 1 + 8

	frames := [][]byte{}
	rest := sealed[header:]

	for len(rest) > 0 {
		if len(rest) < 5 {
			return nil
		}

		length := int(uint32(rest[0])<<24 | uint32(rest[1])<<16 | uint32(rest[2])<<8 | uint32(rest[3]))
		if len(rest) < 5+length {
			return nil
		}

		frames = append(frames, rest[:5+length])
		rest = rest[5+length:]
	}

	if len(frames) < 2 {
		return nil
	}

	frames[0], frames[1] = frames[1], frames[0]

	out := append([]byte{}, sealed[:header]...)
	for _, f := range frames {
		out = append(out, f...)
	}

	return out
}

// assertNoLeak fails when the error text holds a key, scope or content.
func assertNoLeak(t *testing.T, name string, err error, secrets []string) {
	t.Helper()

	text := err.Error()
	for _, s := range secrets {
		if s != "" && strings.Contains(text, s) {
			t.Errorf("%s: error %q holds %q, want no key, scope or content", name, text, s)
		}
	}
}
