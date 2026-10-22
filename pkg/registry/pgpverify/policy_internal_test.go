package pgpverify

import (
	"crypto"
	"errors"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/packet"

	"opendustpan.org/opendustpan/internal/registry/pgpfixture"
	"opendustpan.org/opendustpan/pkg/registry"
)

// signatureOf returns a signature packet with the fields that checkPolicy
// reads. The library cannot make a SHA-1 signature, so the test builds the
// packet fields itself.
func signatureOf(hash crypto.Hash, issuer *uint64) *packet.Signature {
	var sig packet.Signature

	sig.Hash, sig.IssuerKeyId = hash, issuer

	return &sig
}

// signatureTypeOf returns a signature packet of the given type. A text-mode
// signature (SigTypeText) hashes the data after a change of its line endings.
func signatureTypeOf(kind packet.SignatureType) *packet.Signature {
	issuer := uint64(42)
	sig := signatureOf(crypto.SHA256, &issuer)
	sig.SigType = kind

	return sig
}

// entityWith returns an entity whose primary key has the given algorithm.
func entityWith(algo packet.PublicKeyAlgorithm) *openpgp.Entity {
	var key packet.PublicKey

	key.PubKeyAlgo = algo

	var entity openpgp.Entity

	entity.PrimaryKey = &key

	return &entity
}

// TestCheckPolicy sends a signature and a key that the policy refuses straight
// to checkPolicy, as if the library had accepted them.
func TestCheckPolicy(t *testing.T) {
	t.Parallel()

	issuer := uint64(42)
	signer := pgpfixture.NewSigner(t)

	//nolint:exhaustruct_v5 // Each case names only the fields that it tests.
	tests := []struct {
		name   string
		sig    *packet.Signature
		signer *openpgp.Entity
		ok     bool
	}{
		{name: "signature with SHA-256", sig: signatureOf(crypto.SHA256, &issuer), signer: signer.Entity, ok: true},
		{name: "signature with SHA-512", sig: signatureOf(crypto.SHA512, &issuer), signer: signer.Entity, ok: true},
		{name: "signature with SHA-1", sig: signatureOf(crypto.SHA1, &issuer), signer: signer.Entity},
		{name: "signature with MD5", sig: signatureOf(crypto.MD5, &issuer), signer: signer.Entity},
		{name: "signature with RIPEMD-160", sig: signatureOf(crypto.RIPEMD160, &issuer), signer: signer.Entity},
		{name: "text-mode signature", sig: signatureTypeOf(packet.SigTypeText), signer: signer.Entity},
		{name: "certification signature", sig: signatureTypeOf(packet.SigTypeGenericCert), signer: signer.Entity},
		{name: "binary signature type", sig: signatureTypeOf(packet.SigTypeBinary), signer: signer.Entity, ok: true},
		{name: "no hash", sig: signatureOf(0, &issuer), signer: signer.Entity},
		{name: "no issuer", sig: signatureOf(crypto.SHA256, nil), signer: signer.Entity},
		{
			name: "DSA primary key", sig: signatureOf(crypto.SHA256, &issuer),
			signer: entityWith(packet.PubKeyAlgoDSA),
		},
		{
			name: "ElGamal primary key", sig: signatureOf(crypto.SHA256, &issuer),
			signer: entityWith(packet.PubKeyAlgoElGamal),
		},
	}

	for _, tc := range tests {
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

// errNewInLibrary stands for an error that a later version of the library adds.
var errNewInLibrary = errors.New("a new error of the library")

// TestClassify holds the rule that an error the adapter does not know is a bad
// signature and never a success or an untrusted key.
func TestClassify(t *testing.T) {
	t.Parallel()

	if err := classify(errNewInLibrary); !errors.Is(err, registry.ErrBadSignature) {
		t.Fatalf("classify = %v, want ErrBadSignature", err)
	}
}
