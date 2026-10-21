package pgpverify_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"

	"opendustpan.org/opendustpan/pkg/registry"
	"opendustpan.org/opendustpan/pkg/registry/pgpverify"
	"opendustpan.org/opendustpan/pkg/registry/registrytest"
)

const sumsText = "" +
	"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  terraform-provider-payments_1.0.0_linux_amd64.zip\n"

// newEntity makes a key pair for a test.
func newEntity(t *testing.T, cfg *packet.Config) *openpgp.Entity {
	t.Helper()

	e, err := openpgp.NewEntity("Release signing", "", "release@example.invalid", cfg)
	if err != nil {
		t.Fatalf("new entity: %v", err)
	}

	return e
}

// trusted returns the public key of e as a trusted key.
func trusted(t *testing.T, e *openpgp.Entity) registry.TrustedKey {
	t.Helper()

	var buf bytes.Buffer

	w, err := armor.Encode(&buf, openpgp.PublicKeyType, nil)
	if err != nil {
		t.Fatalf("armor: %v", err)
	}

	if err := e.Serialize(w); err != nil {
		t.Fatalf("serialize public key: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("close armor: %v", err)
	}

	return registry.TrustedKey{
		Fingerprint: strings.ToUpper(hex.EncodeToString(e.PrimaryKey.Fingerprint)),
		Armored:     buf.Bytes(),
	}
}

// detach signs sums with e.
func detach(t *testing.T, e *openpgp.Entity, sums []byte, cfg *packet.Config, armored bool) []byte {
	t.Helper()

	var buf bytes.Buffer

	sign := openpgp.DetachSign
	if armored {
		sign = openpgp.ArmoredDetachSign
	}

	if err := sign(&buf, e, bytes.NewReader(sums), cfg); err != nil {
		t.Fatalf("sign: %v", err)
	}

	return buf.Bytes()
}

func edConfig() *packet.Config {
	return &packet.Config{Algorithm: packet.PubKeyAlgoEdDSA}
}

func harness(t *testing.T) registrytest.VerifierHarness {
	t.Helper()

	signer := newEntity(t, edConfig())
	other := newEntity(t, edConfig())
	sums := []byte(sumsText)

	return registrytest.VerifierHarness{
		Verifier:  pgpverify.Verifier{Now: nil},
		Key:       trusted(t, signer),
		Sums:      sums,
		Signature: detach(t, signer, sums, nil, false),
		OtherKey:  trusted(t, other),
	}
}

func TestVerifierConformance(t *testing.T) {
	t.Parallel()

	registrytest.RunVerifier(t, harness)
}

// TestVerifyPolicy sends signatures that the policy of the registry refuses,
// straight to the verifier, as if every other layer had failed.
func TestVerifyPolicy(t *testing.T) {
	t.Parallel()

	sums := []byte(sumsText)
	type fixture struct {
		keys []registry.TrustedKey
		sig  []byte
		// later moves the time of the check after the time of the signature.
		later time.Duration
	}

	cases := []struct {
		name  string
		build func(t *testing.T) fixture
		want  error
		// fingerprintOf says if the verified fingerprint is the one of the first key.
		ok bool
	}{
		{
			name: "binary signature by an Ed25519 key",
			build: func(t *testing.T) fixture {
				e := newEntity(t, edConfig())

				return fixture{[]registry.TrustedKey{trusted(t, e)}, detach(t, e, sums, nil, false), 0}
			},
			ok: true,
		},
		{
			name: "armored signature",
			build: func(t *testing.T) fixture {
				e := newEntity(t, edConfig())

				return fixture{[]registry.TrustedKey{trusted(t, e)}, detach(t, e, sums, nil, true), 0}
			},
			ok: true,
		},
		{
			name: "RSA 2048 key",
			build: func(t *testing.T) fixture {
				e := newEntity(t, &packet.Config{Algorithm: packet.PubKeyAlgoRSA, RSABits: 2048})

				return fixture{[]registry.TrustedKey{trusted(t, e)}, detach(t, e, sums, nil, false), 0}
			},
			ok: true,
		},
		{
			name: "RSA 1024 key",
			build: func(t *testing.T) fixture {
				e := newEntity(t, &packet.Config{Algorithm: packet.PubKeyAlgoRSA, RSABits: 1024})

				return fixture{[]registry.TrustedKey{trusted(t, e)}, detach(t, e, sums, nil, false), 0}
			},
			want: registry.ErrBadSignature,
		},
		{
			name: "expired key",
			build: func(t *testing.T) fixture {
				e := newEntity(t, &packet.Config{Algorithm: packet.PubKeyAlgoEdDSA, KeyLifetimeSecs: 3600})

				return fixture{[]registry.TrustedKey{trusted(t, e)}, detach(t, e, sums, nil, false), 48 * time.Hour}
			},
			want: registry.ErrBadSignature,
		},
		{
			name: "revoked key",
			build: func(t *testing.T) fixture {
				e := newEntity(t, edConfig())
				sig := detach(t, e, sums, nil, false)

				// The key is revoked after it signed. The registry checks at upload time.
				if err := e.RevokeKey(packet.KeyCompromised, "leaked", nil); err != nil {
					t.Fatalf("revoke: %v", err)
				}

				return fixture{[]registry.TrustedKey{trusted(t, e)}, sig, 0}
			},
			want: registry.ErrBadSignature,
		},
		{
			name: "signature by a subkey",
			build: func(t *testing.T) fixture {
				e := newEntity(t, edConfig())
				if err := e.AddSigningSubkey(edConfig()); err != nil {
					t.Fatalf("add subkey: %v", err)
				}

				return fixture{[]registry.TrustedKey{trusted(t, e)}, detach(t, e, sums, nil, false), 0}
			},
			ok: true,
		},
		{
			name: "garbage key and garbage signature",
			build: func(*testing.T) fixture {
				return fixture{
					[]registry.TrustedKey{{Fingerprint: strings.Repeat("AB", 20), Armored: []byte("not a key")}},
					[]byte("not a signature"), 0,
				}
			},
			want: registry.ErrUntrustedKey,
		},
		{
			name: "armor of another type",
			build: func(t *testing.T) fixture {
				e := newEntity(t, edConfig())
				k := trusted(t, e)

				return fixture{[]registry.TrustedKey{k}, []byte(strings.Replace(
					string(detach(t, e, sums, nil, true)), "PGP SIGNATURE", "PGP MESSAGE", 2)), 0}
			},
			want: registry.ErrBadSignature,
		},
		{
			name: "oversize signature",
			build: func(t *testing.T) fixture {
				e := newEntity(t, edConfig())

				return fixture{
					[]registry.TrustedKey{trusted(t, e)},
					append(detach(t, e, sums, nil, false), make([]byte, pgpverify.MaxSignatureBytes)...), 0,
				}
			},
			want: registry.ErrBadSignature,
		},
		{
			name: "too many keys",
			build: func(t *testing.T) fixture {
				e := newEntity(t, edConfig())
				k := trusted(t, e)
				keys := make([]registry.TrustedKey, pgpverify.MaxKeys+1)

				for i := range keys {
					keys[i] = k
				}

				return fixture{keys, detach(t, e, sums, nil, false), 0}
			},
			want: registry.ErrUntrustedKey,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := tc.build(t)
			v := pgpverify.Verifier{Now: func() time.Time { return time.Now().Add(f.later) }}

			got, err := v.Verify(context.Background(), sums, f.sig, f.keys)
			if tc.ok {
				if err != nil || got != f.keys[0].Fingerprint {
					t.Fatalf("Verify = %q, %v; want %q", got, err, f.keys[0].Fingerprint)
				}

				return
			}

			if !errors.Is(err, tc.want) || got != "" {
				t.Fatalf("Verify = %q, %v; want %v and no fingerprint", got, err, tc.want)
			}

			// The error text holds no byte of a key.
			for _, k := range f.keys {
				if len(k.Armored) > 0 && strings.Contains(err.Error(), string(k.Armored[:min(len(k.Armored), 20)])) {
					t.Fatalf("error text holds key bytes: %v", err)
				}
			}
		})
	}
}

// armorOf serializes e with write and wraps the result in ASCII armor of kind.
func armorOf(t *testing.T, kind string, write func(w *bytes.Buffer) error) []byte {
	t.Helper()

	var buf bytes.Buffer

	w, err := armor.Encode(&buf, kind, nil)
	if err != nil {
		t.Fatalf("armor: %v", err)
	}

	var inner bytes.Buffer
	if err := write(&inner); err != nil {
		t.Fatalf("serialize: %v", err)
	}

	if _, err := w.Write(inner.Bytes()); err != nil {
		t.Fatalf("write armor: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("close armor: %v", err)
	}

	return buf.Bytes()
}

func TestInspect(t *testing.T) {
	t.Parallel()

	good := newEntity(t, edConfig())
	goodKey := trusted(t, good)

	privateArmor := armorOf(t, openpgp.PrivateKeyType, func(w *bytes.Buffer) error { return good.SerializePrivate(w, nil) })

	revoked := newEntity(t, edConfig())
	if err := revoked.RevokeKey(packet.KeyCompromised, "leaked", nil); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	weak := newEntity(t, &packet.Config{Algorithm: packet.PubKeyAlgoRSA, RSABits: 1024})
	other := newEntity(t, edConfig())
	two := append(bytes.Clone(goodKey.Armored), trusted(t, other).Armored...)

	tests := []struct {
		name    string
		armored []byte
		now     time.Time
		ok      bool
	}{
		{"public key", goodKey.Armored, time.Time{}, true},
		{"public key with text around the armor", append([]byte("note\n"), append(bytes.Clone(goodKey.Armored), []byte("\ntrailer")...)...), time.Time{}, true},
		{"private key", privateArmor, time.Time{}, false},
		{"revoked key", trusted(t, revoked).Armored, time.Time{}, false},
		{"weak RSA key", trusted(t, weak).Armored, time.Time{}, false},
		{"two keys", two, time.Time{}, false},
		{"garbage", []byte("not a key"), time.Time{}, false},
		{"empty", nil, time.Time{}, false},
		{"too large", make([]byte, registry.MaxKeyBytes+1), time.Time{}, false},
		{"key that is expired at the time of the check", goodKey.Armored, time.Now().AddDate(100, 0, 0), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			v := pgpverify.Verifier{Now: nil}
			if !tc.now.IsZero() {
				v.Now = func() time.Time { return tc.now }
			}

			got, err := v.Inspect(context.Background(), tc.armored)
			if !tc.ok {
				if !errors.Is(err, registry.ErrBadKey) || got.Fingerprint != "" || got.Armored != nil {
					t.Fatalf("Inspect = %+v, %v; want ErrBadKey and no key", got, err)
				}

				return
			}

			if err != nil || got.Fingerprint != goodKey.Fingerprint {
				t.Fatalf("Inspect = %q, %v; want %q", got.Fingerprint, err, goodKey.Fingerprint)
			}

			// The stored form is the canonical armor of the parsed key. It parses
			// again to the same fingerprint, and it holds no text around the armor.
			again, err := pgpverify.Verifier{Now: nil}.Inspect(context.Background(), got.Armored)
			if err != nil || again.Fingerprint != got.Fingerprint || !bytes.HasPrefix(got.Armored, []byte("-----BEGIN PGP PUBLIC KEY BLOCK-----")) {
				t.Fatalf("canonical key = %q, %v", got.Armored, err)
			}
		})
	}
}

func TestInspectExpiredKey(t *testing.T) {
	t.Parallel()

	e := newEntity(t, &packet.Config{Algorithm: packet.PubKeyAlgoEdDSA, KeyLifetimeSecs: 3600})
	k := trusted(t, e)

	v := pgpverify.Verifier{Now: func() time.Time { return time.Now().Add(48 * time.Hour) }}
	if _, err := v.Inspect(context.Background(), k.Armored); !errors.Is(err, registry.ErrBadKey) {
		t.Fatalf("Inspect of an expired key = %v, want ErrBadKey", err)
	}
}
