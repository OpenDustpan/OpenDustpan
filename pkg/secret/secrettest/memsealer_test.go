package secrettest_test

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/secret/secrettest"
)

// memoryRef returns the reference of the state item with the ID item, of
// the stack stk-1 in acme.
func memoryRef(t *testing.T, item string) secret.ItemRef {
	t.Helper()

	scope, err := secret.NewScope("acme", "stk-1")
	if err != nil {
		t.Fatalf("scope: %v", err)
	}

	ref, err := secret.NewItemRef(scope, secret.ItemKindState, item)
	if err != nil {
		t.Fatalf("ref: %v", err)
	}

	return ref
}

// memorySealed returns a MemorySealer with keys for u1 and u2, and the
// content "plain text" sealed for u1 with the binding b1.
func memorySealed(t *testing.T) (*secrettest.MemorySealer, []byte) {
	t.Helper()

	m := secrettest.NewMemorySealer()
	fence := namespace.Fence{Namespace: "acme", FailoverVersion: 1}

	for _, item := range []string{"u1", "u2"} {
		if _, err := m.NewItemKey(t.Context(), nil, memoryRef(t, item), fence); err != nil {
			t.Fatalf("new item key: %v", err)
		}
	}

	r, err := m.SealContent(t.Context(), memoryRef(t, "u1"), []byte("b1"), bytes.NewReader([]byte("plain text")))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	sealed, err := io.ReadAll(r)
	if err != nil || bytes.Contains(sealed, []byte("plain text")) {
		t.Fatalf("sealed = %q, %v; want no plain text", sealed, err)
	}

	return m, sealed
}

// TestMemorySealerRoundTrip checks that sealed content opens for its
// reference and binding.
func TestMemorySealerRoundTrip(t *testing.T) {
	t.Parallel()

	m, sealed := memorySealed(t)

	opened, err := m.OpenContent(t.Context(), memoryRef(t, "u1"), []byte("b1"), bytes.NewReader(sealed))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	defer func() {
		if closeErr := opened.Close(); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	if got, readErr := io.ReadAll(opened); readErr != nil || string(got) != "plain text" {
		t.Fatalf("opened = %q, %v; want the plain text", got, readErr)
	}
}

// TestMemorySealerRefusesOtherContent checks that content does not open for
// another binding or item, or when it is cut.
func TestMemorySealerRefusesOtherContent(t *testing.T) {
	t.Parallel()

	m, sealed := memorySealed(t)

	for name, tc := range map[string]struct {
		item    string
		binding string
		content []byte
	}{
		"another binding": {"u1", "b2", sealed},
		"another item":    {"u2", "b1", sealed},
		"short content":   {"u1", "b1", sealed[:3]},
	} {
		_, err := m.OpenContent(t.Context(), memoryRef(t, tc.item), []byte(tc.binding), bytes.NewReader(tc.content))
		if !errors.Is(err, secret.ErrDecrypt) {
			t.Errorf("open with %s: err = %v, want ErrDecrypt", name, err)
		}
	}
}

// TestMemorySealerKeyErrors checks the errors of a missing key, of the fence
// of another namespace and of a shredded key.
func TestMemorySealerKeyErrors(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	fresh := secrettest.NewMemorySealer()

	if _, err := fresh.SealContent(ctx, memoryRef(t, "u1"), nil, bytes.NewReader(nil)); !errors.Is(
		err, secret.ErrKeyNotFound,
	) {
		t.Fatalf("seal with no key: err = %v, want ErrKeyNotFound", err)
	}

	other := namespace.Fence{Namespace: "other", FailoverVersion: 1}
	if _, err := fresh.NewItemKey(ctx, nil, memoryRef(t, "u1"), other); !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("key under the fence of another namespace: err = %v, want ErrFenced", err)
	}

	m, sealed := memorySealed(t)
	m.Shred(memoryRef(t, "u1"))

	if _, err := m.OpenContent(ctx, memoryRef(t, "u1"), []byte("b1"), bytes.NewReader(sealed)); !errors.Is(
		err, secret.ErrShredded,
	) {
		t.Fatalf("open after a shred: err = %v, want ErrShredded", err)
	}
}
