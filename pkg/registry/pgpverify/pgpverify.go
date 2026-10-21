// Package pgpverify is the OpenPGP adapter of registry.SignatureVerifier. It
// wraps github.com/ProtonMail/go-crypto and writes no cryptographic code of its
// own: the library parses the keys and the signature and checks the signature.
// The adapter adds the policy of the registry on top of the result: which
// hash functions and key algorithms it accepts, and which key may sign.
package pgpverify

import (
	"bytes"
	"context"
	"crypto"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	pgperrors "github.com/ProtonMail/go-crypto/openpgp/errors"
	"github.com/ProtonMail/go-crypto/openpgp/packet"

	"opendustpan.org/opendustpan/pkg/registry"
)

// Bounds of the input. The caller limits the checksums file. These bounds
// protect the parser of the library from input that cannot be a key or a
// signature of a provider release.
const (
	// MaxSignatureBytes is the largest detached signature.
	MaxSignatureBytes = 64 << 10
	// MaxKeyBytes is the largest armored public key.
	MaxKeyBytes = 1 << 20
	// MaxKeys is the most keys in one call.
	MaxKeys = 64
	// MinRSABits is the smallest RSA key that can sign a release.
	MinRSABits = 2048
)

const armoredSignatureHeader = "-----BEGIN PGP SIGNATURE-----"

// Verifier implements registry.SignatureVerifier with go-crypto.
type Verifier struct {
	// Now returns the time that the expiry and the revocation of a key are
	// checked at. Nil means time.Now.
	Now func() time.Time
}

var _ registry.SignatureVerifier = Verifier{Now: nil}

// Verify implements registry.SignatureVerifier. The signature is binary or in
// ASCII armor. A signature whose issuer is not in keys returns
// registry.ErrUntrustedKey, because the adapter cannot check a signature
// without the key. Any other failure returns registry.ErrBadSignature. The
// returned errors hold no byte of a key.
func (v Verifier) Verify(
	ctx context.Context, sums, signature []byte, keys []registry.TrustedKey,
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("verify signature: %w", err)
	}

	if len(signature) == 0 || len(signature) > MaxSignatureBytes || len(sums) == 0 {
		return "", registry.ErrBadSignature
	}

	ring, err := trustedRing(keys)
	if err != nil {
		return "", err
	}

	if len(ring) == 0 {
		return "", registry.ErrUntrustedKey
	}

	now := time.Now
	if v.Now != nil {
		now = v.Now
	}

	cfg := policy(now)

	var (
		sig    *packet.Signature
		signer *openpgp.Entity
	)

	if bytes.HasPrefix(bytes.TrimSpace(signature), []byte(armoredSignatureHeader)) {
		sig, signer, err = verifyArmored(ring, sums, signature, cfg)
	} else {
		sig, signer, err = openpgp.VerifyDetachedSignature(ring, bytes.NewReader(sums), bytes.NewReader(signature), cfg)
	}

	if err != nil {
		return "", classify(err)
	}

	if policyErr := checkPolicy(sig, signer); policyErr != nil {
		return "", policyErr
	}

	fingerprint := fingerprintOf(signer.PrimaryKey)

	// Defense in depth: the keyring holds only trusted entities. This check stays
	// so that a change of the keyring cannot return a fingerprint that the caller
	// did not name.
	if !named(keys, fingerprint) {
		return "", registry.ErrUntrustedKey
	}

	return fingerprint, nil
}

// verifyArmored unwraps the ASCII armor and verifies the binary signature.
func verifyArmored(
	ring openpgp.EntityList, sums, signature []byte, cfg *packet.Config,
) (*packet.Signature, *openpgp.Entity, error) {
	block, err := armorBody(signature)
	if err != nil {
		return nil, nil, err
	}

	sig, signer, err := openpgp.VerifyDetachedSignature(ring, bytes.NewReader(sums), bytes.NewReader(block), cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("verify detached signature: %w", err)
	}

	return sig, signer, nil
}

// armorBody returns the binary packets inside the ASCII armor of signature.
// It refuses any other armor type and a body above MaxSignatureBytes.
func armorBody(signature []byte) ([]byte, error) {
	block, err := armor.Decode(bytes.NewReader(signature))
	if err != nil || block.Type != openpgp.SignatureType {
		return nil, fmt.Errorf("decode signature armor: %w", registry.ErrBadSignature)
	}

	body, err := io.ReadAll(io.LimitReader(block.Body, MaxSignatureBytes+1))
	if err != nil || len(body) > MaxSignatureBytes {
		return nil, fmt.Errorf("read signature armor: %w", registry.ErrBadSignature)
	}

	return body, nil
}

// policy returns the packet configuration of the registry: the library rejects
// the weak hash functions and key algorithms it knows, and the check of the
// result in checkPolicy repeats the rule.
func policy(now func() time.Time) *packet.Config {
	return &packet.Config{
		Time:       now,
		MinRSABits: MinRSABits,
		RejectPublicKeyAlgorithms: map[packet.PublicKeyAlgorithm]bool{
			packet.PubKeyAlgoDSA:     true,
			packet.PubKeyAlgoElGamal: true,
		},
		RejectHashAlgorithms: map[crypto.Hash]bool{
			crypto.MD5:       true,
			crypto.SHA1:      true,
			crypto.RIPEMD160: true,
		},
	}
}

// trustedRing parses the keys into one keyring. A key whose fingerprint is not
// the one the caller names, or that does not parse, is left out (fail closed:
// a smaller keyring trusts fewer signatures). Too many keys, or a key that is
// too large, is an error.
func trustedRing(keys []registry.TrustedKey) (openpgp.EntityList, error) {
	if len(keys) > MaxKeys {
		return nil, registry.ErrUntrustedKey
	}

	var ring openpgp.EntityList

	for _, k := range keys {
		if len(k.Armored) == 0 || len(k.Armored) > MaxKeyBytes {
			continue
		}

		list, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(k.Armored))
		if err != nil {
			continue
		}

		for _, e := range list {
			if fingerprintOf(e.PrimaryKey) == strings.ToUpper(k.Fingerprint) {
				ring = append(ring, e)
			}
		}
	}

	return ring, nil
}

// named reports whether keys name fingerprint.
func named(keys []registry.TrustedKey, fingerprint string) bool {
	for _, k := range keys {
		if strings.EqualFold(k.Fingerprint, fingerprint) {
			return true
		}
	}

	return false
}

// fingerprintOf returns the fingerprint of k in uppercase hex.
func fingerprintOf(k *packet.PublicKey) string {
	return strings.ToUpper(hex.EncodeToString(k.Fingerprint))
}

// classify maps an error of the library to a sentinel of the registry. An
// issuer that no trusted key holds is an untrusted key. Everything else,
// including an error that the library adds later, is a bad signature (fail
// closed). The text of the library error is not kept: it can name key data.
func classify(err error) error {
	if errors.Is(err, pgperrors.ErrUnknownIssuer) {
		return registry.ErrUntrustedKey
	}

	return registry.ErrBadSignature
}

// checkPolicy applies the rules of the registry to a signature that the library
// accepted: the hash function, and the algorithm and size of every key of the
// signer. Defense in depth: the packet configuration states the same rules.
// This check stays so that a library that ignores a field of its configuration
// cannot accept a weak signature.
func checkPolicy(sig *packet.Signature, signer *openpgp.Entity) error {
	switch sig.Hash {
	case crypto.SHA256, crypto.SHA384, crypto.SHA512:
	default:
		return registry.ErrBadSignature
	}

	if sig.IssuerKeyId == nil {
		return registry.ErrBadSignature
	}

	if err := strongKey(signer.PrimaryKey); err != nil {
		return err
	}

	signers := openpgp.EntityList{signer}

	for _, k := range signers.KeysById(*sig.IssuerKeyId) {
		if err := strongKey(k.PublicKey); err != nil {
			return err
		}
	}

	return nil
}

// strongKey refuses a DSA or ElGamal key and an RSA key below MinRSABits.
func strongKey(k *packet.PublicKey) error {
	switch k.PubKeyAlgo {
	case packet.PubKeyAlgoDSA, packet.PubKeyAlgoElGamal:
		return registry.ErrBadSignature
	case packet.PubKeyAlgoRSA, packet.PubKeyAlgoRSASignOnly, packet.PubKeyAlgoRSAEncryptOnly:
		bits, err := k.BitLength()
		if err != nil || bits < MinRSABits {
			return registry.ErrBadSignature
		}
	}

	return nil
}
