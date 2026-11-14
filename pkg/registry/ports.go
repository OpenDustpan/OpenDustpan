package registry

import (
	"context"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// MaxAddressPage is the largest limit of Store.Addresses.
const MaxAddressPage = 1000

// Store keeps the releases of the private registry. A SQL adapter serves it
// from the database of the namespace. Every write carries the fence of the
// namespace, and the store checks it in the transaction of the write. The
// bytes of the archives are blobs in the blob port, under BlobKey. The blob
// port must give the registry a scoped view of the namespace. It does not
// have the view yet, so until it exists the namespace-ID prefix of BlobKey is
// the only guard.
//
// The publish flow is fixed. The upload streams each file to a StagingKey,
// and the server computes the SHA-256 while it writes. It checks the
// signature over the raw bytes of the checksums file before it parses them.
// It then parses the checksums, compares the hashes and checks the names.
// Only then does it move the blobs to their BlobKey and call Publish. A blob
// under a BlobKey is readable only through a published release. The server
// removes staging blobs that no publish moved.
type Store interface {
	// Publish stores r and appends the audit entry e in one transaction. A
	// published version never changes. It returns:
	//   - ErrInvalid when r.Validate fails.
	//   - namespace.ErrFenced when the fence is stale or belongs to another
	//     namespace than r.Address.
	//   - ErrVersionExists when the version exists and r.SameContent is false.
	//   - ErrKeyNotFound when r has a signing key and the namespace does not
	//     trust it at the time of the transaction. The key check is in the same
	//     transaction as the write, so a key removed after the checks of the
	//     caller refuses the publish. A release with no key is a module.
	//
	// A publish of a version that exists with the same content returns nil and
	// changes nothing, so a repeat after a timeout is safe. A repeat writes no
	// audit entry. On every error the store changes nothing and writes no audit
	// entry. A new release and its audit entry commit together or not at all.
	// Two publishes of one version with different content at the same time:
	// exactly one succeeds, and the other returns ErrVersionExists.
	Publish(ctx context.Context, r Release, fence namespace.Fence, e audit.Entry) error
	// Get returns the release of version at addr. The result is a copy: a
	// change of it never changes the store. It returns ErrNotFound, and
	// ErrInvalid for an address or a version that fails validation. Get does not
	// check access. The caller does.
	Get(ctx context.Context, addr Address, version string) (Release, error)
	// List returns every release of addr as copies, ordered by semantic
	// version precedence from the lowest (CompareVersions). It
	// returns an empty list for an address with no release, and ErrInvalid for
	// an address that fails validation.
	List(ctx context.Context, addr Address) ([]Release, error)
	// Addresses returns at most limit addresses of kind in namespace ns that
	// have at least one release, ordered by name and system, and the cursor of
	// the next page. The cursor after is the key of the last address of the
	// page before, as AddressKey returns it, and it is empty for the first
	// page; every returned address sorts after the cursor. The next cursor is
	// the key of the last address of this page, and it is empty when this page
	// is the last one. It returns ErrInvalid for a namespace, a kind, a cursor
	// or a limit that fails validation.
	Addresses(ctx context.Context, ns namespace.Name, kind Kind, after string, limit int) ([]Address, string, error)
}

// TrustedKey is a public signing key that a namespace trusts.
type TrustedKey struct {
	// Fingerprint is the fingerprint of the key, in uppercase hex.
	Fingerprint string
	// Armored is the public key in ASCII armor.
	Armored []byte
}

// KeyInspector reads a public key that a namespace wants to trust. The
// verifier adapter implements it, so the same library and the same policy
// parse every key.
type KeyInspector interface {
	// Inspect parses armored. It returns the key in the canonical armored form
	// that the registry stores, with the fingerprint of the primary key in
	// uppercase hex. The server never takes a fingerprint from the caller. It
	// returns ErrBadKey when armored does not hold exactly one public key, holds
	// private key material, is expired or revoked now, has no key that can sign,
	// uses SHA-1, DSA or ElGamal, or uses an RSA key of less than 2048 bits. The
	// error text holds no byte of the key.
	Inspect(ctx context.Context, armored []byte) (TrustedKey, error)
}

// SignatureVerifier checks the detached signature of a checksums file. An
// adapter wraps an OpenPGP library. The package has no adapter yet, because
// the library is a new dependency that the owner decides. A later KeyStore
// port holds the registered keys, with the fence, an audit entry and the
// action registry:manage-keys.
type SignatureVerifier interface {
	// Verify reports whether signature is a valid detached signature of sums,
	// made by one of keys. It checks the expiry and the revocation of the key
	// at the time of the call, which is the time of the upload. It returns the
	// fingerprint of the primary key. A signature made by a subkey returns the
	// fingerprint of its primary key, and that fingerprint is the one of a key
	// in keys. It returns:
	//   - ErrBadSignature when the signature is malformed, does not match
	//     sums, was made by an expired or revoked key, uses SHA-1, uses DSA, or
	//     uses an RSA key of less than 2048 bits.
	//   - ErrUntrustedKey when the signature is valid and no key of keys made it.
	//
	// It denies on any other error (fail closed). The error text holds no
	// byte of the key.
	Verify(ctx context.Context, sums, signature []byte, keys []TrustedKey) (fingerprint string, err error)
}
