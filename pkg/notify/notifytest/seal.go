package notifytest

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
	secretpkg "opendustpan.org/opendustpan/pkg/secret"
)

// suiteKey is the AES-256 key of the sealer of the suite. It seals test
// values only.
const suiteKey = "notifytest: a key of 32 bytes..."

// sealer seals the secret values of the suite with suiteKey and the scope
// as the associated data, so a value opens only for the scope that sealed
// it, as the secret services do.
type sealer struct {
	aead cipher.AEAD
}

// suiteSealer returns the sealer of the suite. Each call uses suiteKey, so
// a value that one call seals opens with another.
func suiteSealer() notify.SinkSealer {
	block, err := aes.NewCipher([]byte(suiteKey))
	if err != nil {
		panic(err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}

	s := sealer{aead: aead}

	return notify.SinkSealer{Namespaces: s, Installation: s}
}

func (s sealer) Encrypt(_ context.Context, scope secretpkg.Scope, ps [][]byte) ([][]byte, error) {
	out := make([][]byte, 0, len(ps))

	for _, p := range ps {
		c, err := sealAAD(s.aead, p, scope.String())
		if err != nil {
			return nil, err
		}

		out = append(out, c)
	}

	return out, nil
}

func (s sealer) Decrypt(_ context.Context, scope secretpkg.Scope, cs [][]byte) ([][]byte, error) {
	out := make([][]byte, 0, len(cs))

	for _, c := range cs {
		p, err := openAAD(s.aead, c, scope.String())
		if err != nil {
			return nil, err
		}

		out = append(out, p)
	}

	return out, nil
}

func (s sealer) Seal(_ context.Context, id string, p []byte) ([]byte, error) {
	return sealAAD(s.aead, p, "installation/"+id)
}

func (s sealer) Open(_ context.Context, id string, c []byte) ([]byte, error) {
	return openAAD(s.aead, c, "installation/"+id)
}

// sealAAD seals p with aead and the associated data aad.
func sealAAD(aead cipher.AEAD, p []byte, aad string) ([]byte, error) {
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}

	return aead.Seal(nonce, nonce, p, []byte(aad)), nil
}

// openAAD opens c, which sealAAD made with aead and aad.
func openAAD(aead cipher.AEAD, c []byte, aad string) ([]byte, error) {
	n := aead.NonceSize()
	if len(c) < n {
		return nil, secretpkg.ErrDecrypt
	}

	p, err := aead.Open(nil, c[:n], c[n:], []byte(aad))
	if err != nil {
		return nil, secretpkg.ErrDecrypt
	}

	return p, nil
}

// plaintext returns the plaintext value of the secret item ref of the
// suite.
func plaintext(ref string) string {
	return "key of " + ref
}

// secret returns the new secret item ref of the sink s, sealed for s.
func secret(ctx context.Context, s notify.Sink, ref string) notify.SealedSecret {
	return sealed(ctx, s, ref, plaintext(ref))
}

// sealed returns the value v of the new secret item ref of the sink s,
// sealed for s.
func sealed(ctx context.Context, s notify.Sink, ref, v string) notify.SealedSecret {
	s.SecretRefs = []string{ref}

	out, err := suiteSealer().Seal(ctx, s, []notify.SinkSecret{{Ref: ref, Value: []byte(v)}})
	if err != nil {
		panic(err)
	}

	return out[0]
}

// fresh returns a new secret item for each ref of the new sink s.
func fresh(ctx context.Context, s notify.Sink) []notify.SealedSecret {
	out := make([]notify.SealedSecret, 0, len(s.SecretRefs))
	for _, ref := range s.SecretRefs {
		out = append(out, secret(ctx, s, ref))
	}

	return out
}

// testSinkSecretsSealed checks that the store keeps the sealed values of a
// sink as it got them, so it never holds a plaintext value; that it
// refuses a value sealed for another sink or scope; and that a value read
// back opens only for its own sink.
func testSinkSecretsSealed(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)

	if _, err := h.Domain.CreateNamespace(t.Context(), other); err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	s := withRefs(sink("snk-1", shop), "itm-snk-1", "itm-snk-1-b")
	put := fresh(t.Context(), s)

	if err := h.Subscriptions.PutSink(t.Context(), s, put, fence, manage()); err != nil {
		t.Fatalf("PutSink: %v", err)
	}

	got, err := h.Subscriptions.SinkSecrets(t.Context(), shop, "snk-1")
	if err != nil || len(got) != len(put) {
		t.Fatalf("SinkSecrets = %d items (%v), want %d", len(got), err, len(put))
	}

	for i := range got {
		if got[i].Ref() != put[i].Ref() || got[i].Scope() != shop || got[i].Sink() != "snk-1" ||
			!bytes.Equal(got[i].Ciphertext(), put[i].Ciphertext()) {
			t.Fatalf("SinkSecrets item %d = %s of %s/%s, want the sealed value of %s as PutSink got it", i,
				got[i].Ref(), got[i].Scope(), got[i].Sink(), put[i].Ref())
		}

		if bytes.Contains(got[i].Ciphertext(), []byte(plaintext(got[i].Ref()))) {
			t.Fatalf("SinkSecrets item %d holds the plaintext value", i)
		}
	}

	checkSecrets(t, h, "[key of itm-snk-1 key of itm-snk-1-b]")

	refuseForeignSeals(t, h, fence)
	checkMoved(t, got)
}

// refuseForeignSeals checks that PutSink refuses a new secret item sealed
// for another sink or another scope, and stores nothing.
func refuseForeignSeals(t *testing.T, h Harness, fence namespace.Fence) {
	t.Helper()

	s := sink("snk-2", shop)

	cases := []struct {
		name string
		item notify.SealedSecret
	}{
		{"a value sealed for another sink", secret(t.Context(), sink("snk-1", shop), "itm-snk-2")},
		{"a value sealed for another scope", secret(t.Context(), sink("snk-2", other), "itm-snk-2")},
	}

	for _, tc := range cases {
		err := h.Subscriptions.PutSink(t.Context(), s, []notify.SealedSecret{tc.item}, fence, manage())
		if !errors.Is(err, notify.ErrInvalid) {
			t.Fatalf("PutSink with %s: err = %v, want ErrInvalid", tc.name, err)
		}
	}

	if _, err := h.Subscriptions.SinkSecrets(t.Context(), shop, "snk-2"); !errors.Is(err, notify.ErrNotFound) {
		t.Fatalf("SinkSecrets after the refused puts: err = %v, want ErrNotFound", err)
	}
}

// checkMoved checks that the sealed values got of the sink snk-1 in shop
// do not open as the values of another sink or another scope.
func checkMoved(t *testing.T, got []notify.SealedSecret) {
	t.Helper()

	targets := []notify.Sink{
		withRefs(sink("snk-2", shop), "itm-snk-1", "itm-snk-1-b"),
		withRefs(sink("snk-1", other), "itm-snk-1", "itm-snk-1-b"),
	}

	for i := range targets {
		to := &targets[i]
		moved := make([]notify.SealedSecret, 0, len(got))
		for _, g := range got {
			moved = append(moved, notify.StoredSecret(to.Scope, to.ID, g.Ref(), g.Ciphertext()))
		}

		if v, err := suiteSealer().Open(t.Context(), *to, moved); !errors.Is(err, secretpkg.ErrDecrypt) {
			t.Fatalf("Open as the values of %s/%s = %d values, %v, want secretpkg.ErrDecrypt", to.Scope, to.ID,
				len(v), err)
		}
	}
}
