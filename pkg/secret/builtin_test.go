package secret_test

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/secret/secrettest"
)

// vectorKeyString is the fixed key-encryption key of the wrapper ID test below.
const vectorKeyString = "0123456789abcdef0123456789abcdef"

// vectorKey returns the fixed key-encryption key as bytes.
func vectorKey() []byte {
	return []byte(vectorKeyString)
}

// TestBuiltinWrapperConformance runs the Wrapper suite against the built-in
// adapter.
func TestBuiltinWrapperConformance(t *testing.T) {
	t.Parallel()

	secrettest.RunWrapper(t, func(t *testing.T) secret.Wrapper {
		t.Helper()

		key := make([]byte, 32)

		if _, err := rand.Read(key); err != nil {
			t.Fatalf("generate key-encryption key: %v", err)
		}

		w, err := secret.NewBuiltinWrapper(key)
		if err != nil {
			t.Fatalf("new wrapper: %v", err)
		}

		return w
	})
}

// TestBuiltinWrapperIDVector pins the wrapper ID of a fixed key. The ID
// format is part of the rotation story (ADR 23), so it must not drift.
func TestBuiltinWrapperIDVector(t *testing.T) {
	t.Parallel()

	w, err := secret.NewBuiltinWrapper(vectorKey())
	if err != nil {
		t.Fatalf("new wrapper: %v", err)
	}

	if got, want := w.ID(), "builtin-9dc42aefb47260fa"; got != want {
		t.Fatalf("wrapper ID = %q, want %q", got, want)
	}
}

// TestBuiltinWrapperIDHidesTheKey proves that the ID contains neither the key
// nor its hexadecimal form: a one-way function makes it (ADR 23).
func TestBuiltinWrapperIDHidesTheKey(t *testing.T) {
	t.Parallel()

	w, err := secret.NewBuiltinWrapper(vectorKey())
	if err != nil {
		t.Fatalf("new wrapper: %v", err)
	}

	keyHex := hex.EncodeToString(vectorKey())

	if strings.Contains(w.ID(), vectorKeyString) || strings.Contains(w.ID(), keyHex) {
		t.Fatalf("wrapper ID %q contains the key", w.ID())
	}
}

func TestBuiltinWrapperRejectsWrongSize(t *testing.T) {
	t.Parallel()

	for _, size := range []int{0, 16, 31, 33, 64} {
		if _, err := secret.NewBuiltinWrapper(make([]byte, size)); !errors.Is(err, secret.ErrInvalidKey) {
			t.Errorf("key of %d bytes: err = %v, want ErrInvalidKey", size, err)
		}
	}
}

// unconfiguredService returns a Service without a key-encryption key.
func unconfiguredService(t *testing.T) *secret.Service {
	t.Helper()

	namespaces, err := namespace.NewService(namespace.Config{Store: oneNamespace{}, Now: nil, Region: "local"})
	if err != nil {
		t.Fatalf("namespaces: %v", err)
	}

	svc, err := secret.NewService(secret.Config{
		Keys:          &memKeys{keys: map[secret.Scope]secret.WrappedKey{}, mu: sync.Mutex{}},
		Wrapper:       nil,
		Namespaces:    namespaces,
		Now:           nil,
		MaxBatchBytes: 0,
	})
	if err != nil {
		t.Fatalf("service without a key: %v", err)
	}

	return svc
}

// TestEncryptWithoutKeyNamesTheVariable proves that encrypting without a
// key-encryption key answers with an error that names the setting.
func TestEncryptWithoutKeyNamesTheVariable(t *testing.T) {
	t.Parallel()

	svc := unconfiguredService(t)

	_, err := svc.Encrypt(t.Context(), scope(t, "stack-1"), [][]byte{[]byte("s3cr3t")})
	if err == nil || !strings.Contains(err.Error(), "DUSTPAN_KEY_ENCRYPTION_KEY") {
		t.Fatalf("encrypt without a key: err = %v, want it to name DUSTPAN_KEY_ENCRYPTION_KEY", err)
	}

	if !errors.Is(err, secret.ErrKeyNotConfigured) {
		t.Fatalf("encrypt without a key: err = %v, want ErrKeyNotConfigured", err)
	}
}

// TestDecryptWithoutKeyNamesTheVariable proves that decrypting without a
// key-encryption key answers with an error that names the setting, even when
// the scope was never encrypted.
func TestDecryptWithoutKeyNamesTheVariable(t *testing.T) {
	t.Parallel()

	svc := unconfiguredService(t)

	_, err := svc.Decrypt(t.Context(), scope(t, "never-used"), [][]byte{[]byte("s3cr3t")})
	if err == nil || !strings.Contains(err.Error(), "DUSTPAN_KEY_ENCRYPTION_KEY") {
		t.Fatalf("decrypt without a key: err = %v, want it to name DUSTPAN_KEY_ENCRYPTION_KEY", err)
	}

	if !errors.Is(err, secret.ErrKeyNotConfigured) {
		t.Fatalf("decrypt without a key: err = %v, want ErrKeyNotConfigured", err)
	}
}

// seedMemKeys stores one wrapped key for scope with the wrapper ID id.
func seedMemKeys(t *testing.T, keys *memKeys, sc secret.Scope, id string) {
	t.Helper()

	stored, err := keys.CreateKey(t.Context(), secret.WrappedKey{
		CreatedAt:  time.Now(),
		Scope:      sc,
		WrapperID:  id,
		Ciphertext: []byte("wrapped"),
	}, namespace.Fence{Namespace: sc.Namespace, FailoverVersion: 1})
	if err != nil {
		t.Fatalf("seed wrapped key: %v", err)
	}

	if stored.WrapperID != id {
		t.Fatalf("seeded wrapper ID = %q, want %q", stored.WrapperID, id)
	}
}

// rightWrapper returns the wrapper holding the vector key.
func rightWrapper(t *testing.T) *secret.BuiltinWrapper {
	t.Helper()

	wrapper, err := secret.NewBuiltinWrapper(vectorKey())
	if err != nil {
		t.Fatalf("new wrapper: %v", err)
	}

	return wrapper
}

// wrongWrapper returns a wrapper holding another random key.
func wrongWrapper(t *testing.T) *secret.BuiltinWrapper {
	t.Helper()

	other := make([]byte, 32)

	if _, err := rand.Read(other); err != nil {
		t.Fatalf("generate other key: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper(other)
	if err != nil {
		t.Fatalf("new other wrapper: %v", err)
	}

	return wrapper
}

// fullMemKeys returns a key store holding one wrapped key of right.
func fullMemKeys(t *testing.T, right *secret.BuiltinWrapper) *memKeys {
	t.Helper()

	keys := &memKeys{keys: map[secret.Scope]secret.WrappedKey{}, mu: sync.Mutex{}}
	seedMemKeys(t, keys, scope(t, "stack-1"), right.ID())

	return keys
}

func TestCheckWrapper(t *testing.T) {
	t.Parallel()

	right, wrong := rightWrapper(t), wrongWrapper(t)

	t.Run("empty store without a key is fine", func(t *testing.T) {
		t.Parallel()

		keys := &memKeys{keys: map[secret.Scope]secret.WrappedKey{}, mu: sync.Mutex{}}

		if err := secret.CheckWrapper(t.Context(), keys, nil); err != nil {
			t.Fatalf("check empty store without a key: %v", err)
		}
	})

	t.Run("wrapped keys without a key are refused", func(t *testing.T) {
		t.Parallel()

		assertCheckRefused(t, fullMemKeys(t, right), nil, secret.ErrKeyNotConfigured)
	})

	t.Run("a key that matches none is refused", func(t *testing.T) {
		t.Parallel()

		assertCheckRefused(t, fullMemKeys(t, right), wrong, secret.ErrWrapperMismatch)
	})

	t.Run("the matching key passes", func(t *testing.T) {
		t.Parallel()

		if err := secret.CheckWrapper(t.Context(), fullMemKeys(t, right), right); err != nil {
			t.Fatalf("check with the matching key: %v", err)
		}
	})

	t.Run("a missing key store fails", func(t *testing.T) {
		t.Parallel()

		if err := secret.CheckWrapper(t.Context(), nil, right); !errors.Is(err, secret.ErrMissingDependency) {
			t.Fatalf("check without a key store: err = %v, want ErrMissingDependency", err)
		}
	})
}

// assertCheckRefused proves that CheckWrapper refuses wrapper for keys with
// an error that names the setting and wraps sentinel.
func assertCheckRefused(t *testing.T, keys *memKeys, wrapper secret.Wrapper, sentinel error) {
	t.Helper()

	err := secret.CheckWrapper(t.Context(), keys, wrapper)
	if err == nil || !strings.Contains(err.Error(), "DUSTPAN_KEY_ENCRYPTION_KEY") {
		t.Fatalf("check: err = %v, want it to name DUSTPAN_KEY_ENCRYPTION_KEY", err)
	}

	if !errors.Is(err, sentinel) {
		t.Fatalf("check: err = %v, want %v", err, sentinel)
	}
}
