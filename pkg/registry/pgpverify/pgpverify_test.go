package pgpverify_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/registry"
	"opendustpan.org/opendustpan/pkg/registry/pgpfixture"
	"opendustpan.org/opendustpan/pkg/registry/pgpverify"
	"opendustpan.org/opendustpan/pkg/registry/registrytest"
)

const sumsText = "" +
	"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  terraform-provider-payments_1.0.0_linux_amd64.zip\n"

func harness(t *testing.T) registrytest.VerifierHarness {
	t.Helper()

	signer := pgpfixture.NewSigner(t)
	other := pgpfixture.NewSigner(t)
	sums := []byte(sumsText)

	return registrytest.VerifierHarness{
		Verifier:  pgpverify.Verifier{Now: nil},
		Key:       signer.Key,
		Sums:      sums,
		Signature: signer.Detach(t, sums),
		OtherKey:  other.Key,
	}
}

func TestVerifierConformance(t *testing.T) {
	t.Parallel()

	registrytest.RunVerifier(t, harness)
}

// scenario says how to make one signed release for TestVerify.
type scenario struct {
	name string
	spec pgpfixture.KeySpec
	// armored makes the signature in ASCII armor.
	armored bool
	// subkey makes the signature with a signing subkey.
	subkey bool
	// revoke revokes the key after it signed.
	revoke bool
	// later moves the time of the check after the time of the signature.
	later time.Duration
	// want is nil when the signature must pass.
	want error
}

func scenarios() []scenario {
	week := 48 * time.Hour

	//nolint:exhaustruct_v5 // Each case names only the fields that it tests.
	return []scenario{
		{name: "binary signature by an Ed25519 key"},
		{name: "armored signature", armored: true},
		{name: "RSA 2048 key", spec: pgpfixture.KeySpec{RSABits: 2048}},
		{name: "signature by a subkey", subkey: true},
		{name: "RSA 1024 key", spec: pgpfixture.KeySpec{RSABits: 1024}, want: registry.ErrBadSignature},
		{
			name:  "expired key",
			spec:  pgpfixture.KeySpec{LifetimeSecs: 3600},
			later: week,
			want:  registry.ErrBadSignature,
		},
		{name: "revoked key", revoke: true, want: registry.ErrBadSignature},
	}
}

// TestVerify sends signatures that the policy accepts and refuses straight to
// the verifier, as if every other layer had failed.
func TestVerify(t *testing.T) {
	t.Parallel()

	for _, sc := range scenarios() {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()

			runScenario(t, sc)
		})
	}
}

// runScenario signs the sums file as sc says and checks the verdict.
func runScenario(t *testing.T, sc scenario) {
	t.Helper()

	sums := []byte(sumsText)
	signer := pgpfixture.New(t, sc.spec)

	if sc.subkey {
		signer.AddSigningSubkey(t)
	}

	sig := signer.Detach(t, sums)
	if sc.armored {
		sig = signer.DetachArmored(t, sums)
	}

	// A revocation after the signature changes the public key that the
	// namespace registers. The registry checks at upload time.
	if sc.revoke {
		signer.Revoke(t)
	}

	v := pgpverify.Verifier{Now: func() time.Time { return time.Now().Add(sc.later) }}

	got, err := v.Verify(context.Background(), sums, sig, []registry.TrustedKey{signer.Key})
	if sc.want == nil {
		if err != nil || got != signer.Key.Fingerprint {
			t.Fatalf("Verify = %q, %v; want %q", got, err, signer.Key.Fingerprint)
		}

		return
	}

	if !errors.Is(err, sc.want) || got != "" {
		t.Fatalf("Verify = %q, %v; want %v and no fingerprint", got, err, sc.want)
	}

	// The error text holds no byte of a key.
	if strings.Contains(err.Error(), "BEGIN PGP") {
		t.Fatalf("error text holds key bytes: %v", err)
	}
}

func TestVerifyRefusesMalformedInput(t *testing.T) {
	t.Parallel()

	sums := []byte(sumsText)
	signer := pgpfixture.NewSigner(t)
	keys := []registry.TrustedKey{signer.Key}
	armoredSig := signer.DetachArmored(t, sums)

	tests := []struct {
		name string
		sig  []byte
		keys []registry.TrustedKey
		want error
	}{
		{
			name: "garbage key and garbage signature",
			sig:  []byte("not a signature"),
			keys: []registry.TrustedKey{{Fingerprint: strings.Repeat("AB", 20), Armored: []byte("not a key")}},
			want: registry.ErrUntrustedKey,
		},
		{
			name: "armor of another type",
			sig:  []byte(strings.ReplaceAll(string(armoredSig), "PGP SIGNATURE", "PGP MESSAGE")),
			keys: keys,
			want: registry.ErrBadSignature,
		},
		{
			name: "oversize signature",
			sig:  append(signer.Detach(t, sums), make([]byte, pgpverify.MaxSignatureBytes)...),
			keys: keys,
			want: registry.ErrBadSignature,
		},
		{
			name: "too many keys",
			sig:  signer.Detach(t, sums),
			keys: slices.Repeat([]registry.TrustedKey{signer.Key}, pgpverify.MaxKeys+1),
			want: registry.ErrUntrustedKey,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := pgpverify.Verifier{Now: nil}.Verify(context.Background(), sums, tc.sig, tc.keys)
			if !errors.Is(err, tc.want) || got != "" {
				t.Fatalf("Verify = %q, %v; want %v and no fingerprint", got, err, tc.want)
			}
		})
	}
}

func TestInspectAcceptsAPublicKey(t *testing.T) {
	t.Parallel()

	signer := pgpfixture.NewSigner(t)
	around := append([]byte("note\n"), append(bytes.Clone(signer.Key.Armored), []byte("\ntrailer")...)...)

	for name, armored := range map[string][]byte{"the armor": signer.Key.Armored, "text around the armor": around} {
		got, err := pgpverify.Verifier{Now: nil}.Inspect(context.Background(), armored)
		if err != nil || got.Fingerprint != signer.Key.Fingerprint {
			t.Fatalf("%s: Inspect = %q, %v; want %q", name, got.Fingerprint, err, signer.Key.Fingerprint)
		}

		// The stored form is the canonical armor of the parsed key. It parses
		// again to the same fingerprint, and it holds no text around the armor.
		again, err := pgpverify.Verifier{Now: nil}.Inspect(context.Background(), got.Armored)
		if err != nil || again.Fingerprint != got.Fingerprint ||
			!bytes.HasPrefix(got.Armored, []byte("-----BEGIN PGP PUBLIC KEY BLOCK-----")) {
			t.Fatalf("%s: canonical key = %q, %v", name, got.Armored, err)
		}
	}
}

func TestInspectRefuses(t *testing.T) {
	t.Parallel()

	good := pgpfixture.NewSigner(t)
	other := pgpfixture.NewSigner(t)
	revoked := pgpfixture.NewSigner(t)
	revoked.Revoke(t)

	weak := pgpfixture.New(t, pgpfixture.KeySpec{RSABits: 1024, LifetimeSecs: 0, CreatedAt: time.Time{}})
	expiring := pgpfixture.New(t, pgpfixture.KeySpec{RSABits: 0, LifetimeSecs: 3600, CreatedAt: time.Time{}})

	//nolint:exhaustruct_v5 // Each case names only the fields that it tests.
	tests := []struct {
		name    string
		armored []byte
		later   time.Duration
	}{
		{name: "private key", armored: good.PrivateArmor(t)},
		{name: "revoked key", armored: revoked.Key.Armored},
		{name: "weak RSA key", armored: weak.Key.Armored},
		{name: "two keys", armored: append(bytes.Clone(good.Key.Armored), other.Key.Armored...)},
		{name: "garbage", armored: []byte("not a key")},
		{name: "empty"},
		{name: "too large", armored: make([]byte, registry.MaxKeyBytes+1)},
		{name: "expired key", armored: expiring.Key.Armored, later: 48 * time.Hour},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			v := pgpverify.Verifier{Now: func() time.Time { return time.Now().Add(tc.later) }}

			got, err := v.Inspect(context.Background(), tc.armored)
			if !errors.Is(err, registry.ErrBadKey) || got.Fingerprint != "" || got.Armored != nil {
				t.Fatalf("Inspect = %+v, %v; want ErrBadKey and no key", got, err)
			}

			// The error text holds no byte of a key.
			if strings.Contains(err.Error(), "BEGIN PGP") {
				t.Fatalf("error text holds key bytes: %v", err)
			}
		})
	}
}
