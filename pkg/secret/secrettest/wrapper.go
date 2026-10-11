package secrettest

import (
	"bytes"
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/secret"
)

// WrapperFactory returns a Wrapper with a fresh key-encryption key for one
// subtest.
type WrapperFactory func(t *testing.T) secret.Wrapper

// RunWrapper executes the conformance suite against Wrappers produced by
// newWrapper. Every built-in adapter runs it, and custom adapters should too:
//
//	func TestWrapperConformance(t *testing.T) {
//		t.Parallel()
//		secrettest.RunWrapper(t, func(t *testing.T) secret.Wrapper { return newMyWrapper(t) })
//	}
func RunWrapper(t *testing.T, newWrapper WrapperFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, newWrapper WrapperFactory)
		name string
	}{
		{name: "wrap then unwrap returns the data key", test: testWrapRoundTrip},
		{name: "unwrap binds the associated data", test: testWrapBindsAAD},
		{name: "unwrap rejects tampered values", test: testWrapRejectsTampered},
		{name: "another wrapper cannot unwrap", test: testWrapIsolation},
		{name: "ID is stable and names the wrapper", test: testWrapID},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newWrapper)
		})
	}
}

// randomKey returns 32 random bytes: the size of every data key.
func randomKey(t *testing.T) []byte {
	t.Helper()

	key := make([]byte, 32)

	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate data key: %v", err)
	}

	return key
}

func testWrapRoundTrip(t *testing.T, newWrapper WrapperFactory) {
	t.Helper()

	w := newWrapper(t)
	ctx := t.Context()
	key := randomKey(t)
	aad := []byte("namespace/scope")

	wrapped, err := w.Wrap(ctx, key, aad)
	if err != nil {
		t.Fatalf("wrap key: %v", err)
	}

	if bytes.Equal(wrapped, key) {
		t.Fatal("wrapped key equals the data key")
	}

	opened, err := w.Unwrap(ctx, wrapped, aad)
	if err != nil {
		t.Fatalf("unwrap key: %v", err)
	}

	if !bytes.Equal(opened, key) {
		t.Fatal("unwrapped key differs from the data key")
	}

	// A random nonce makes every wrapping differ, and every wrapping opens.
	again, err := w.Wrap(ctx, key, aad)
	if err != nil {
		t.Fatalf("wrap key again: %v", err)
	}

	if bytes.Equal(again, wrapped) {
		t.Fatal("two wrappings of one key are equal")
	}

	if _, err := w.Unwrap(ctx, again, aad); err != nil {
		t.Fatalf("unwrap second wrapping: %v", err)
	}
}

func testWrapBindsAAD(t *testing.T, newWrapper WrapperFactory) {
	t.Helper()

	w := newWrapper(t)
	ctx := t.Context()

	wrapped, err := w.Wrap(ctx, randomKey(t), []byte("namespace/scope-1"))
	if err != nil {
		t.Fatalf("wrap key: %v", err)
	}

	for _, aad := range [][]byte{[]byte("namespace/scope-2"), {}, nil} {
		if _, err := w.Unwrap(ctx, wrapped, aad); !errors.Is(err, secret.ErrDecrypt) {
			t.Errorf("unwrap with other associated data: err = %v, want ErrDecrypt", err)
		}
	}
}

func testWrapRejectsTampered(t *testing.T, newWrapper WrapperFactory) {
	t.Helper()

	w := newWrapper(t)
	ctx := t.Context()
	aad := []byte("namespace/scope")

	wrapped, err := w.Wrap(ctx, randomKey(t), aad)
	if err != nil {
		t.Fatalf("wrap key: %v", err)
	}

	tampered := bytes.Clone(wrapped)
	tampered[len(tampered)-1] ^= 1

	for name, c := range map[string][]byte{
		"tampered":  tampered,
		"truncated": wrapped[:5],
		"empty":     {},
	} {
		if _, err := w.Unwrap(ctx, c, aad); !errors.Is(err, secret.ErrDecrypt) {
			t.Errorf("unwrap %s value: err = %v, want ErrDecrypt", name, err)
		}
	}
}

func testWrapIsolation(t *testing.T, newWrapper WrapperFactory) {
	t.Helper()

	first := newWrapper(t)
	second := newWrapper(t)
	ctx := t.Context()

	wrapped, err := first.Wrap(ctx, randomKey(t), []byte("namespace/scope"))
	if err != nil {
		t.Fatalf("wrap key: %v", err)
	}

	if _, err := second.Unwrap(ctx, wrapped, []byte("namespace/scope")); !errors.Is(err, secret.ErrDecrypt) {
		t.Errorf("unwrap with another wrapper: err = %v, want ErrDecrypt", err)
	}

	if first.ID() == second.ID() {
		t.Errorf("two wrappers share ID %q", first.ID())
	}
}

func testWrapID(t *testing.T, newWrapper WrapperFactory) {
	t.Helper()

	w := newWrapper(t)
	id := w.ID()

	if id == "" {
		t.Fatal("wrapper ID is empty")
	}

	if again := w.ID(); again != id {
		t.Fatalf("wrapper ID changed from %q to %q", id, again)
	}

	if strings.Contains(id, "\n") {
		t.Errorf("wrapper ID %q holds a newline", id)
	}
}
