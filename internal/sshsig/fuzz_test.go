package sshsig_test

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/sshsig"
	"opendustpan.org/opendustpan/internal/stepup"
)

// seeds returns the .sig fixtures of the real ssh-keygen.
func seeds(f *testing.F) [][]byte {
	f.Helper()

	dir := os.DirFS("testdata")

	names, err := fs.Glob(dir, "*.sig")
	if err != nil || len(names) == 0 {
		f.Fatalf("no signature fixture: %v", err)
	}

	out := make([][]byte, 0, len(names))

	for _, name := range names {
		b, err := fs.ReadFile(dir, name)
		if err != nil {
			f.Fatalf("read fixture: %v", err)
		}

		out = append(out, b)
	}

	return out
}

// FuzzParse checks that the parser never panics, and that each signature
// that it accepts holds only allowed values.
func FuzzParse(f *testing.F) {
	for _, s := range seeds(f) {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, armored []byte) {
		sig, err := sshsig.Parse(armored)
		if err != nil {
			return
		}

		if sig.HashAlgorithm != sshsig.HashSHA256 && sig.HashAlgorithm != sshsig.HashSHA512 {
			t.Fatalf("Parse accepted the hash %q", sig.HashAlgorithm)
		}

		if sig.Type != sshsig.TypeEd25519 && sig.Type != sshsig.TypeSKEd25519 {
			t.Fatalf("Parse accepted the type %q", sig.Type)
		}

		if sig.Namespace == "" || strings.ContainsAny(sig.Namespace, " \r\n\x00") {
			t.Fatalf("Parse accepted the namespace %q", sig.Namespace)
		}

		if len(sig.Blob) != 64 {
			t.Fatalf("Parse accepted a signature of %d bytes", len(sig.Blob))
		}
	})
}

// FuzzParsePublicKey checks that the key parser never panics, and that
// Marshal gives the bytes of each key that it accepts.
func FuzzParsePublicKey(f *testing.F) {
	for _, name := range []string{"ed25519", "sk", "rsa-2048", "ecdsa-p384"} {
		b, err := fs.ReadFile(os.DirFS("testdata"), name+".pub")
		if err != nil {
			f.Fatalf("read fixture: %v", err)
		}

		f.Add(b)
	}

	f.Fuzz(func(t *testing.T, wire []byte) {
		k, err := sshsig.ParsePublicKey(wire)
		if err != nil {
			return
		}

		if !bytes.Equal(k.Marshal(), wire) {
			t.Fatal("Marshal does not give the parsed bytes")
		}
	})
}

// FuzzVerifier checks that the verifier never panics, never accepts a
// changed signature of the real ssh-keygen, and fails only with ErrBadAnswer
// for a signature by a valid key. An accepted signature must parse to the
// same values as a valid fixture, so that another armor of a fixture is not
// reported as a forgery.
func FuzzVerifier(f *testing.F) {
	for _, s := range seeds(f) {
		f.Add(s, uint8(0))
	}

	f.Fuzz(func(t *testing.T, sig []byte, scope uint8) {
		v := vector(t, "sk", "sk-presence", stepup.ScopeWrite)
		scopes := []stepup.Scope{stepup.ScopeWrite, stepup.ScopeWindow, stepup.ScopeKeyAdd}

		_, err := sshsig.NewVerifier().Verify(t.Context(), v.Key, scopes[int(scope)%len(scopes)], v.Message, sig)
		if err == nil {
			if !isFixture(t, sig) {
				t.Fatal("Verify accepted a signature that the key did not make")
			}

			return
		}

		if !errors.Is(err, stepup.ErrBadAnswer) {
			t.Fatalf("Verify error = %v, want ErrBadAnswer", err)
		}
	})
}

// sameSignature reports whether a and b hold the same values.
func sameSignature(a, b sshsig.Signature) bool {
	return bytes.Equal(a.PublicKey, b.PublicKey) &&
		a.Namespace == b.Namespace &&
		a.HashAlgorithm == b.HashAlgorithm &&
		a.Type == b.Type &&
		bytes.Equal(a.Blob, b.Blob) &&
		a.Flags == b.Flags &&
		a.Counter == b.Counter
}

// isFixture reports whether sig parses to the values of a valid sk fixture.
func isFixture(t *testing.T, sig []byte) bool {
	t.Helper()

	got, err := sshsig.Parse(sig)
	if err != nil {
		t.Fatalf("Verify accepted a signature that Parse refuses: %v", err)
	}

	for _, ok := range []string{"sk-presence", "sk-verified", "sk-counter-zero", "sk-counter-max", "sk-key-add"} {
		want, err := sshsig.Parse(fixture(t, ok+".sig"))
		if err != nil {
			t.Fatalf("parse fixture %s: %v", ok, err)
		}

		if sameSignature(got, want) {
			return true
		}
	}

	return false
}
