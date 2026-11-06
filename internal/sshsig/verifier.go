package sshsig

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/internal/stepup"
)

// Verifier is the stepup.Verifier for SSH step-up keys. It accepts the key
// types ssh-ed25519, sk-ssh-ed25519@openssh.com and ecdsa-sha2-nistp256.
type Verifier struct{}

// The interface check.
var _ stepup.Verifier = Verifier{}

// NewVerifier returns the Verifier for SSH step-up keys.
func NewVerifier() Verifier {
	return Verifier{}
}

// Verify returns the assertion of a valid signature of k over message for
// the scope scope. A write and a window need NamespaceStepUp, and a key add
// needs NamespaceKeyAdd. It returns stepup.ErrInvalid for a key that is not
// an SSH key of an accepted type, or for an unknown scope, and
// stepup.ErrBadAnswer for a signature that does not verify.
func (Verifier) Verify(_ context.Context, k stepup.Key, scope stepup.Scope, message, signature []byte) (
	stepup.Assertion, error,
) {
	key, err := checkKey(k)
	if err != nil {
		return stepup.Assertion{}, err
	}

	namespace, err := scopeNamespace(scope)
	if err != nil {
		return stepup.Assertion{}, err
	}

	sig, err := Parse(signature)
	if err != nil {
		return stepup.Assertion{}, fmt.Errorf("%w: %w", stepup.ErrBadAnswer, err)
	}

	res, err := sig.Verify(key, namespace, message)
	if err != nil {
		return stepup.Assertion{}, fmt.Errorf("%w: %w", stepup.ErrBadAnswer, err)
	}

	return stepup.Assertion{
		UserPresent:  res.UserPresent,
		UserVerified: res.UserVerified,
		Counter:      res.Counter,
	}, nil
}

// checkKey returns the public key of k. It fails closed with
// stepup.ErrInvalid for a factor, a type or a key outside the allowlist.
func checkKey(k stepup.Key) (PublicKey, error) {
	if k.Factor != stepup.FactorSSH {
		return PublicKey{}, fmt.Errorf(
			"%w: factor %q is not an SSH key",
			stepup.ErrInvalid,
			printable(string(k.Factor)),
		)
	}

	if k.Type != TypeEd25519 && k.Type != TypeSKEd25519 && k.Type != TypeECDSAP256 {
		return PublicKey{}, fmt.Errorf("%w: SSH key type %q not allowed", stepup.ErrInvalid, printable(k.Type))
	}

	key, err := ParsePublicKey(k.PublicKey)
	if err != nil {
		return PublicKey{}, fmt.Errorf("%w: %w", stepup.ErrInvalid, err)
	}

	// Defense in depth: the store keeps the type and the key together; this
	// check stays so that a row whose type and key differ cannot pass.
	if key.Type != k.Type {
		return PublicKey{}, fmt.Errorf("%w: key type %q is not the stored type %q",
			stepup.ErrInvalid, key.Type, printable(k.Type))
	}

	return key, nil
}

// scopeNamespace returns the namespace of the signature for a scope. An
// unknown scope fails closed with stepup.ErrInvalid.
func scopeNamespace(scope stepup.Scope) (string, error) {
	switch scope {
	case stepup.ScopeWrite, stepup.ScopeWindow:
		return NamespaceStepUp, nil
	case stepup.ScopeKeyAdd:
		return NamespaceKeyAdd, nil
	default:
		return "", fmt.Errorf("%w: scope %q", stepup.ErrInvalid, printable(string(scope)))
	}
}
