package sshsig_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/sshsig"
	"opendustpan.org/opendustpan/internal/stepup"
	"opendustpan.org/opendustpan/internal/stepup/stepuptest"
)

// stepUpKey returns the step-up key of a .pub fixture.
func stepUpKey(t *testing.T, name string) stepup.Key {
	t.Helper()

	keyType, wire := publicKey(t, name)

	return stepup.Key{
		ID:          "stk-" + name,
		PersonID:    "person",
		Factor:      stepup.FactorSSH,
		Type:        keyType,
		Fingerprint: "",
		PublicKey:   wire,
		Name:        name,
		Counter:     0,
		Suspended:   false,
		EnrolledBy:  "",
		CreatedAt:   time.Unix(0, 0),
	}
}

// vector returns the vector of a .sig fixture by the key of a .pub fixture.
func vector(t *testing.T, key, sig string, scope stepup.Scope) *stepuptest.Vector {
	t.Helper()

	return &stepuptest.Vector{
		Key:       stepUpKey(t, key),
		Scope:     scope,
		Message:   fixture(t, "message"),
		Signature: fixture(t, sig+".sig"),
	}
}

// harness returns the harness of the suite. Each vector except OtherHash
// comes from the real ssh-keygen. ssh-keygen writes only sha256 and sha512,
// so the test builds OtherHash with a key of its own.
func harness(t *testing.T) stepuptest.VerifierHarness {
	t.Helper()

	other := stepUpKey(t, "ed25519-other")
	message := fixture(t, "message")

	k := newKey(t, sshsig.TypeEd25519)
	s := k.valid(message)
	s.hash = "sha1"
	s.signHash = "sha1"
	hashKey := stepUpKey(t, "ed25519")
	hashKey.PublicKey = k.pub.Marshal()

	return stepuptest.VerifierHarness{
		Verifier:       sshsig.NewVerifier(),
		Factor:         stepup.FactorSSH,
		Write:          vector(t, "ed25519", "ed25519-sha512", stepup.ScopeWrite),
		KeyAdd:         vector(t, "ed25519", "ed25519-key-add", stepup.ScopeKeyAdd),
		OtherKey:       &other,
		OtherMessage:   []byte("opendustpan step-up\norigin: https://dustpan.example.org\n"),
		DisallowedType: vector(t, "ecdsa-p384", "ecdsa-p384", stepup.ScopeWrite),
		SmallRSA:       vector(t, "rsa-2048", "rsa-2048", stepup.ScopeWrite),
		FIDO:           vector(t, "sk", "sk-presence", stepup.ScopeWrite),
		FIDOAssertion:  assertion(true, false, 42),
		FIDONoPresence: vector(t, "sk", "sk-no-presence", stepup.ScopeWrite),
		OtherNamespace: vector(t, "ed25519", "ed25519-git", stepup.ScopeWrite),
		OtherHash: &stepuptest.Vector{
			Key: hashKey, Scope: stepup.ScopeWrite, Message: message, Signature: k.armored(s),
		},
		NoNotation:       nil,
		UnhashedNotation: nil,
		TextMode:         nil,
		OtherIssuer:      nil,
		ExpiredOrRevoked: nil,
	}
}

func TestVerifierConformance(t *testing.T) {
	t.Parallel()
	stepuptest.RunVerifier(t, harness)
}

// TestVerifierVectors checks each vector of the real ssh-keygen.
func TestVerifierVectors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		key, sig string
		scope    stepup.Scope
		want     stepup.Assertion
		wantErr  error
	}{
		{"ed25519", "ed25519-sha512", stepup.ScopeWrite, assertion(false, false, 0), nil},
		{"ed25519", "ed25519-sha256", stepup.ScopeWrite, assertion(false, false, 0), nil},
		{"ed25519", "ed25519-sha256", stepup.ScopeWindow, assertion(false, false, 0), nil},
		{"ed25519", "ed25519-key-add", stepup.ScopeKeyAdd, assertion(false, false, 0), nil},
		{"ed25519", "ed25519-git", stepup.ScopeWrite, assertion(false, false, 0), stepup.ErrBadAnswer},
		{"ed25519", "ed25519-other", stepup.ScopeWrite, assertion(false, false, 0), stepup.ErrBadAnswer},
		{"ed25519-other", "ed25519-other", stepup.ScopeWrite, assertion(false, false, 0), nil},
		{"ed25519", "sk-presence", stepup.ScopeWrite, assertion(false, false, 0), stepup.ErrBadAnswer},
		{"sk", "ed25519-sha512", stepup.ScopeWrite, assertion(false, false, 0), stepup.ErrBadAnswer},
		{"sk", "sk-presence", stepup.ScopeWrite, assertion(true, false, 42), nil},
		{
			"sk",
			"sk-verified",
			stepup.ScopeWrite,
			assertion(true, true, 7),
			nil,
		},
		{"sk", "sk-counter-zero", stepup.ScopeWrite, assertion(true, false, 0), nil},
		{"sk", "sk-counter-max", stepup.ScopeWrite, assertion(true, false, 0xffffffff), nil},
		{"sk", "sk-key-add", stepup.ScopeKeyAdd, assertion(true, false, 43), nil},
		{"sk", "sk-key-add", stepup.ScopeWrite, assertion(false, false, 0), stepup.ErrBadAnswer},
		{"sk", "sk-presence", stepup.ScopeKeyAdd, assertion(false, false, 0), stepup.ErrBadAnswer},
		{"sk", "sk-no-presence", stepup.ScopeWrite, assertion(false, false, 0), stepup.ErrBadAnswer},
		{"sk", "sk-verified-no-presence", stepup.ScopeWrite, assertion(false, false, 0), stepup.ErrBadAnswer},
		{"sk", "sk-flag-0x02", stepup.ScopeWrite, assertion(false, false, 0), stepup.ErrBadAnswer},
		{"sk", "sk-flag-0x40", stepup.ScopeWrite, assertion(false, false, 0), stepup.ErrBadAnswer},
		{"sk", "sk-flag-0x80", stepup.ScopeWrite, assertion(false, false, 0), stepup.ErrBadAnswer},
		{"ecdsa-p384", "ecdsa-p384", stepup.ScopeWrite, assertion(false, false, 0), stepup.ErrInvalid},
		{"rsa-2048", "rsa-2048", stepup.ScopeWrite, assertion(false, false, 0), stepup.ErrInvalid},
		{"rsa-3072", "rsa-3072", stepup.ScopeWrite, assertion(false, false, 0), stepup.ErrInvalid},
	}

	for _, tc := range tests {
		t.Run(tc.key+" "+tc.sig+" "+string(tc.scope), func(t *testing.T) {
			t.Parallel()

			v := vector(t, tc.key, tc.sig, tc.scope)

			got, err := sshsig.NewVerifier().Verify(t.Context(), v.Key, v.Scope, v.Message, v.Signature)
			if !errors.Is(err, tc.wantErr) || (err == nil) != (tc.wantErr == nil) {
				t.Fatalf("Verify error = %v, want %v", err, tc.wantErr)
			}

			if got != tc.want {
				t.Fatalf("Verify assertion = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestVerifierKeyCheck sends hostile keys and scopes to the first layer of
// the adapter. Each fails with ErrInvalid before the signature.
func TestVerifierKeyCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		edit  func(k *stepup.Key)
		scope stepup.Scope
	}{
		{"an OpenPGP factor", func(k *stepup.Key) { k.Factor = stepup.FactorOpenPGP }, stepup.ScopeWrite},
		{"an empty factor", func(k *stepup.Key) { k.Factor = "" }, stepup.ScopeWrite},
		{"a type outside the allowlist", func(k *stepup.Key) { k.Type = "ecdsa-sha2-nistp256" }, stepup.ScopeWrite},
		{"an empty type", func(k *stepup.Key) { k.Type = "" }, stepup.ScopeWrite},
		{"a stored type that is not the type of the key", func(k *stepup.Key) {
			k.Type = sshsig.TypeSKEd25519
		}, stepup.ScopeWrite},
		{"a key with bytes after it", func(k *stepup.Key) { k.PublicKey = append(k.PublicKey, 0) }, stepup.ScopeWrite},
		{"a truncated key", func(k *stepup.Key) { k.PublicKey = k.PublicKey[:len(k.PublicKey)-1] }, stepup.ScopeWrite},
		{"no key", func(k *stepup.Key) { k.PublicKey = nil }, stepup.ScopeWrite},
		{"an unknown scope", func(*stepup.Key) {}, stepup.Scope("admin")},
		{"an empty scope", func(*stepup.Key) {}, stepup.Scope("")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			v := vector(t, "ed25519", "ed25519-sha512", stepup.ScopeWrite)
			tc.edit(&v.Key)

			got, err := sshsig.NewVerifier().Verify(t.Context(), v.Key, tc.scope, v.Message, v.Signature)
			if !errors.Is(err, stepup.ErrInvalid) {
				t.Fatalf("Verify error = %v, want ErrInvalid", err)
			}

			if got != assertion(false, false, 0) {
				t.Fatalf("Verify returned the assertion %+v with an error", got)
			}
		})
	}
}
