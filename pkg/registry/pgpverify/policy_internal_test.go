package pgpverify

import (
	"crypto"
	"errors"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/packet"

	"opendustpan.org/opendustpan/pkg/registry"
)

// TestCheckPolicy sends a signature and a key that the policy refuses straight
// to checkPolicy, as if the library had accepted them. The library cannot make
// a SHA-1 signature, so the test builds the packet fields that the check reads.
func TestCheckPolicy(t *testing.T) {
	t.Parallel()

	issuer := uint64(42)

	entity, err := openpgp.NewEntity("Release signing", "", "release@example.invalid",
		&packet.Config{Algorithm: packet.PubKeyAlgoEdDSA})
	if err != nil {
		t.Fatalf("new entity: %v", err)
	}

	cases := []struct {
		name   string
		sig    *packet.Signature
		signer *openpgp.Entity
		ok     bool
	}{
		{"SHA-256", &packet.Signature{Hash: crypto.SHA256, IssuerKeyId: &issuer}, entity, true},
		{"SHA-512", &packet.Signature{Hash: crypto.SHA512, IssuerKeyId: &issuer}, entity, true},
		{"SHA-1", &packet.Signature{Hash: crypto.SHA1, IssuerKeyId: &issuer}, entity, false},
		{"MD5", &packet.Signature{Hash: crypto.MD5, IssuerKeyId: &issuer}, entity, false},
		{"RIPEMD-160", &packet.Signature{Hash: crypto.RIPEMD160, IssuerKeyId: &issuer}, entity, false},
		{"no hash", &packet.Signature{IssuerKeyId: &issuer}, entity, false},
		{"no issuer", &packet.Signature{Hash: crypto.SHA256}, entity, false},
		{
			"DSA primary key",
			&packet.Signature{Hash: crypto.SHA256, IssuerKeyId: &issuer},
			&openpgp.Entity{PrimaryKey: &packet.PublicKey{PubKeyAlgo: packet.PubKeyAlgoDSA}},
			false,
		},
		{
			"ElGamal primary key",
			&packet.Signature{Hash: crypto.SHA256, IssuerKeyId: &issuer},
			&openpgp.Entity{PrimaryKey: &packet.PublicKey{PubKeyAlgo: packet.PubKeyAlgoElGamal}},
			false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := checkPolicy(tc.sig, tc.signer)
			if tc.ok && err != nil {
				t.Fatalf("checkPolicy = %v, want nil", err)
			}

			if !tc.ok && !errors.Is(err, registry.ErrBadSignature) {
				t.Fatalf("checkPolicy = %v, want ErrBadSignature", err)
			}
		})
	}
}

// TestClassify holds the rule that an error the adapter does not know is a bad
// signature and never a success or an untrusted key.
func TestClassify(t *testing.T) {
	t.Parallel()

	if err := classify(errors.New("a new error of the library")); !errors.Is(err, registry.ErrBadSignature) {
		t.Fatalf("classify = %v, want ErrBadSignature", err)
	}
}
