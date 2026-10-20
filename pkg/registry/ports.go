package registry

import (
	"context"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Store keeps the releases of the private registry. A SQL adapter serves it
// from the database of the namespace. Every write carries the fence of the
// namespace, and the store checks it in the transaction of the write. The
// bytes of the archives are blobs in the blob port, under BlobKey. The
// caller writes them before Publish, so a release never names a missing blob.
type Store interface {
	// Publish stores r and appends the audit entry e in one transaction. A
	// published version never changes. It returns:
	//   - ErrInvalid when r.Validate fails.
	//   - namespace.ErrFenced when the fence is stale or belongs to another
	//     namespace than r.Address.
	//   - ErrVersionExists when the version exists and r.SameContent is false.
	//
	// A publish of a version that exists with the same content returns nil and
	// changes nothing, so a repeat after a timeout is safe. On every error the
	// store changes nothing.
	Publish(ctx context.Context, r Release, fence namespace.Fence, e audit.Entry) error
	// Get returns the release of version at addr. It returns ErrNotFound, and
	// ErrInvalid for an address or a version that fails validation. Get does not
	// check access. The caller does.
	Get(ctx context.Context, addr Address, version string) (Release, error)
	// List returns every release of addr, ordered by version string. It
	// returns an empty list for an address with no release, and ErrInvalid for
	// an address that fails validation.
	List(ctx context.Context, addr Address) ([]Release, error)
	// Addresses returns the addresses of kind in namespace ns that have at
	// least one release, ordered by name and system. It returns ErrInvalid for
	// a namespace or a kind that fails validation.
	Addresses(ctx context.Context, ns namespace.Name, kind Kind) ([]Address, error)
}

// TrustedKey is a public signing key that a namespace trusts.
type TrustedKey struct {
	// Fingerprint is the fingerprint of the key, in uppercase hex.
	Fingerprint string
	// Armored is the public key in ASCII armor.
	Armored []byte
}

// SignatureVerifier checks the detached signature of a checksums file. An
// adapter wraps an OpenPGP library. The package has no adapter yet, because
// the library is a new dependency that the owner decides.
type SignatureVerifier interface {
	// Verify reports whether signature is a valid detached signature of sums,
	// made by one of keys. It returns the fingerprint of the signing key. It
	// returns:
	//   - ErrBadSignature when the signature is malformed, does not match
	//     sums, or was made by an expired or revoked key.
	//   - ErrUntrustedKey when the signature is valid and no key of keys made it.
	//
	// It denies on any other error (fail closed). The error text holds no
	// byte of the key.
	Verify(ctx context.Context, sums, signature []byte, keys []TrustedKey) (fingerprint string, err error)
}
