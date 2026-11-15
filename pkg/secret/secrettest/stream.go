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

	namespaces, err := namespace.NewService(namespace.Config{Store: s, Now: nil, Installation: "local"})
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

// streamNamespace creates the acme namespace for stream tests, with the
// installation of the stream service as primary installation.
func streamNamespace(t *testing.T, s Store) {
	t.Helper()

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC),
		Name:                "acme",
		PrimaryInstallation: "local",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
}

// sealAll seals plain and returns the sealed bytes.
func sealAll(t *testing.T, svc *secret.Service, scope secret.Scope, plain []byte) []byte {
	t.Helper()

	sealed, err := svc.SealStream(t.Context(), scope, []byte(streamBindingText), bytes.NewReader(plain))
	if err != nil {
		t.Fatalf("seal stream: %v", err)
	}

	out, err := io.ReadAll(sealed)
	if err != nil {
		t.Fatalf("read sealed stream: %v", err)
	}

	return out
}

// closeOpened closes an opened stream, which clears the key that it holds.
func closeOpened(t *testing.T, opened io.Closer) {
	t.Helper()

	if err := opened.Close(); err != nil {
		t.Errorf("close opened stream: %v", err)
	}
}

// openAll opens sealed and returns the plaintext.
func openAll(t *testing.T, svc *secret.Service, scope secret.Scope, sealed []byte) []byte {
	t.Helper()

	opened, err := svc.OpenStream(t.Context(), scope, []byte(streamBindingText), bytes.NewReader(sealed))
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}

	defer closeOpened(t, opened)

	plain, err := io.ReadAll(opened)
	if err != nil {
		t.Fatalf("read opened stream: %v", err)
	}

	return plain
}

// streamBindingText is the binding of the streams of this suite.
const streamBindingText = "state-version-suite"

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

	opened, err := svc.OpenStream(t.Context(), second, []byte(streamBindingText), bytes.NewReader(sealed))
	if err != nil {
		// No key exists for the other scope: the open fails closed.
		if !errors.Is(err, secret.ErrDecrypt) {
			t.Fatalf("open stream in another scope: err = %v, want ErrDecrypt", err)
		}

		return
	}

	defer closeOpened(t, opened)

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
		opened, err := svc.OpenStream(t.Context(), scope, []byte(streamBindingText), bytes.NewReader(bad))
		if err != nil {
			t.Fatalf("%s: open stream: %v", name, err)
		}

		_, err = io.ReadAll(opened)
		closeOpened(t, opened)
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

// testSealStateBindsVersion proves a sealed state version opens only as its
// own version. A swapped blob from the same workspace under another version
// ID fails with ErrDecrypt, and an empty or oversized binding is refused.
func testSealStateBindsVersion(t *testing.T, s Store) {
	t.Helper()
	streamNamespace(t, s)

	svc := streamService(t, s)
	ns, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("parse namespace: %v", err)
	}

	plain := []byte("state of version a")

	sealed, err := svc.SealState(t.Context(), ns, "scope-1", "sv-a", bytes.NewReader(plain))
	if err != nil {
		t.Fatalf("seal state: %v", err)
	}

	sealedBytes, err := io.ReadAll(sealed)
	if err != nil {
		t.Fatalf("read sealed state: %v", err)
	}

	opened, err := svc.OpenState(t.Context(), ns, "scope-1", "sv-a", bytes.NewReader(sealedBytes))
	if err != nil {
		t.Fatalf("open state of its own version: %v", err)
	}

	defer closeOpened(t, opened)

	got, err := io.ReadAll(opened)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("own version opened %q, err %v", got, err)
	}

	swapped, err := svc.OpenState(t.Context(), ns, "scope-1", "sv-b", bytes.NewReader(sealedBytes))
	if err != nil {
		t.Fatalf("open state of another version: %v", err)
	}

	defer closeOpened(t, swapped)

	_, err = io.ReadAll(swapped)
	if !errors.Is(err, secret.ErrDecrypt) {
		t.Fatalf("swapped version err = %v, want ErrDecrypt", err)
	}

	_, err = svc.SealState(t.Context(), ns, "scope-1", "", bytes.NewReader(plain))
	if !errors.Is(err, secret.ErrInvalidBinding) {
		t.Errorf("empty version err = %v, want ErrInvalidBinding", err)
	}

	long := string(bytes.Repeat([]byte("v"), 256))
	_, err = svc.SealState(t.Context(), ns, "scope-1", long, bytes.NewReader(plain))
	if !errors.Is(err, secret.ErrInvalidBinding) {
		t.Errorf("oversized version err = %v, want ErrInvalidBinding", err)
	}
}

// testSealStreamTruncated proves a stream cut at any point fails with
// ErrDecrypt: inside the header, inside a frame header, inside a frame, and
// before the final chunk.
func testSealStreamTruncated(t *testing.T, s Store) {
	t.Helper()
	streamNamespace(t, s)

	svc := streamService(t, s)
	scope := streamScope(t, "scope-1")

	plain := bytes.Repeat([]byte("truncation-check"), 9000)
	sealed := sealAll(t, svc, scope, plain)

	for _, cut := range []int{0, 1, 20, 33, 34, 40, len(sealed) / 2, len(sealed) - 1} {
		opened, err := svc.OpenStream(t.Context(), scope, []byte(streamBindingText), bytes.NewReader(sealed[:cut]))
		if err == nil {
			_, err = io.ReadAll(opened)
			closeOpened(t, opened)
		}

		if !errors.Is(err, secret.ErrDecrypt) {
			t.Errorf("cut at %d: err = %v, want ErrDecrypt", cut, err)
		}

		assertNoLeak(t, "truncated stream", err, []string{"truncation-check", "scope-1", "acme"})
	}
}
