package registry

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// MaxKeysPerNamespace is the most trusted signing keys that a namespace holds.
// A team rotates a key rarely, so a few keys are enough. The bound keeps the
// work of one signature check small.
const MaxKeysPerNamespace = 16

// MaxKeyBytes is the largest armored public key that a namespace can register.
const MaxKeyBytes = 1 << 20

// Sentinel errors of the key store.
var (
	// ErrKeyExists reports a registration of a fingerprint that the namespace
	// holds with other key bytes. The store changes nothing.
	ErrKeyExists = errors.New("a different key with this fingerprint is already registered")
	// ErrKeyNotFound reports that the namespace holds no key with the
	// fingerprint.
	ErrKeyNotFound = errors.New("registry key not found")
	// ErrKeyLimit reports that the namespace holds MaxKeysPerNamespace keys.
	ErrKeyLimit = fmt.Errorf("a namespace holds at most %d signing keys: remove one first", MaxKeysPerNamespace)
)

// KeyRecord is a trusted signing key of one namespace, with who registered it.
type KeyRecord struct {
	AddedAt   time.Time
	Namespace namespace.Name
	Key       TrustedKey
	// AddedBy is the ID of the principal that registered the key.
	AddedBy string
}

// Validate reports whether k is complete: a namespace, a fingerprint in
// uppercase hex, key bytes in ASCII armor within MaxKeyBytes, a registrar and
// a time. It checks the form only. The service parses the key with the
// SignatureVerifier before it stores it, and the verifier checks the key
// again on every use. It returns ErrInvalid otherwise.
func (k KeyRecord) Validate() error {
	if _, err := namespace.ParseName(k.Namespace.String()); err != nil {
		return fmt.Errorf("%w: namespace: %w", ErrInvalid, err)
	}

	if !fingerprintRE.MatchString(k.Key.Fingerprint) {
		return fmt.Errorf("%w: key fingerprint", ErrInvalid)
	}

	if len(k.Key.Armored) == 0 || len(k.Key.Armored) > MaxKeyBytes {
		return fmt.Errorf("%w: key size", ErrInvalid)
	}

	if k.AddedBy == "" || k.AddedAt.IsZero() {
		return fmt.Errorf("%w: registrar and time", ErrInvalid)
	}

	return nil
}

// KeyStore keeps the trusted signing keys of the namespaces. Every write
// carries the fence of the namespace, and the store checks it in the
// transaction of the write and appends the audit entry in the same
// transaction.
type KeyStore interface {
	// AddKey stores k and appends e. A registration of a key that the
	// namespace holds with the same bytes returns nil, changes nothing and
	// writes no audit entry. It returns:
	//   - ErrInvalid when k.Validate fails.
	//   - namespace.ErrFenced when the fence is stale or belongs to another
	//     namespace than k.
	//   - ErrKeyExists when the fingerprint exists with other bytes.
	//   - ErrKeyLimit when the namespace holds MaxKeysPerNamespace keys.
	//
	// On every error the store changes nothing and writes no audit entry.
	AddKey(ctx context.Context, k KeyRecord, fence namespace.Fence, e audit.Entry) error
	// RemoveKey deletes the key with fingerprint and appends e. It returns
	// ErrKeyNotFound, and writes no audit entry, when the namespace holds no
	// such key. A release that the key signed stays published: its record
	// holds the fingerprint.
	RemoveKey(ctx context.Context, ns namespace.Name, fingerprint string, fence namespace.Fence, e audit.Entry) error
	// Keys returns the keys of ns as copies, ordered by fingerprint. It
	// returns an empty list for a namespace with no key, and ErrInvalid for a
	// namespace that fails validation. Keys does not check access. The
	// caller does.
	Keys(ctx context.Context, ns namespace.Name) ([]KeyRecord, error)
}
