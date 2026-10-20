// Package registry is the tool-neutral model of the private registry: the
// address of a provider or module that a team publishes, one immutable
// release of it, and the ports that store releases and check signatures.
//
// A release is the archive set of a provider (the zip archives, the checksums
// file and its detached signature) or the archive of a module. The package
// keeps the names and the SHA-256 hashes. The bytes are blobs in the blob
// port. The package knows no wire format of a tool. The adapter of each tool
// builds its own JSON from a release.
package registry

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sentinel errors of the registry model and its ports.
var (
	// ErrInvalid reports a value that fails validation: an address, a
	// version or a release. The error text names the field.
	ErrInvalid = errors.New("invalid registry value")
	// ErrNotFound reports that no release or address exists for the call.
	ErrNotFound = errors.New("registry release not found")
	// ErrVersionExists reports a publish of a version that exists with other
	// hashes. A published version never changes. The store changes nothing.
	ErrVersionExists = errors.New("registry version exists with other hashes")
	// ErrBadSignature reports a signature that is not valid for the checksums
	// file.
	ErrBadSignature = errors.New("signature is not valid for the checksums file")
	// ErrUntrustedKey reports a signature made by a key that the caller did
	// not name as trusted.
	ErrUntrustedKey = errors.New("signature key is not trusted")
)

// Bounds of a release. The limits of the upload are a setting of the
// installation (RFC 101). These bounds protect the store.
const (
	// MaxNameLength is the longest name, system or file name.
	MaxNameLength = 64
	// MaxFileNameLength is the longest file name of an archive.
	MaxFileNameLength = 255
	// MaxArchives is the most archives in one release.
	MaxArchives = 16
	// MaxVersionLength is the longest version.
	MaxVersionLength = 64
)

// Kind is the kind of a published artifact.
type Kind string

// The kinds. The wire values are final.
const (
	KindProvider Kind = "provider"
	KindModule   Kind = "module"
)

var (
	nameRE    = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`)
	versionRE = regexp.MustCompile(
		`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`,
	)
	fileNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	sha256RE   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	platformRE = regexp.MustCompile(`^[a-z0-9]+_[a-z0-9]+$`)
)

// Address names a provider or a module in a namespace. The host of the
// address is the host of the server and is not stored.
type Address struct {
	Namespace namespace.Name
	Kind      Kind
	// Name is the provider type or the module name.
	Name string
	// System is the target system of a module, for example "aws". It is
	// empty for a provider.
	System string
}

// Validate reports whether a holds an allowed namespace, kind, name and
// system. It uses an allowlist and returns ErrInvalid otherwise.
func (a Address) Validate() error {
	if _, err := namespace.ParseName(a.Namespace.String()); err != nil {
		return fmt.Errorf("%w: namespace: %w", ErrInvalid, err)
	}

	switch a.Kind {
	case KindProvider:
		if a.System != "" {
			return fmt.Errorf("%w: a provider has no system", ErrInvalid)
		}
	case KindModule:
		if !nameRE.MatchString(a.System) {
			return fmt.Errorf("%w: system", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: kind", ErrInvalid)
	}

	if !nameRE.MatchString(a.Name) {
		return fmt.Errorf("%w: name", ErrInvalid)
	}

	return nil
}

// ValidateVersion reports whether v is a semantic version without build
// metadata. It returns ErrInvalid otherwise.
func ValidateVersion(v string) error {
	if len(v) > MaxVersionLength || !versionRE.MatchString(v) {
		return fmt.Errorf("%w: version %q", ErrInvalid, v)
	}

	return nil
}

// Archive is one file of a release. A provider has one archive for each
// platform. A module has one archive and no platform.
type Archive struct {
	// Platform is the operating system and architecture joined by an
	// underscore, for example "linux_amd64". It is empty for a module.
	Platform string
	// FileName is the name of the archive in the archive set.
	FileName string
	// SHA256 is the lowercase hex SHA-256 of the archive bytes.
	SHA256 string
	// Size is the size in bytes.
	Size int64
}

// BlobKey returns the blob key of the bytes with the given SHA-256 in
// namespace ns. A namespace never reads the blob of another namespace.
func BlobKey(ns namespace.Name, sha256 string) (blob.Key, error) {
	if _, err := namespace.ParseName(ns.String()); err != nil {
		return "", fmt.Errorf("%w: namespace: %w", ErrInvalid, err)
	}

	if !sha256RE.MatchString(sha256) {
		return "", fmt.Errorf("%w: sha256", ErrInvalid)
	}

	key, err := blob.ParseKey("registry/" + ns.String() + "/sha256/" + sha256)
	if err != nil {
		return "", fmt.Errorf("%w: blob key: %w", ErrInvalid, err)
	}

	return key, nil
}

// Release is one published version. It never changes after Publish.
type Release struct {
	PublishedAt time.Time
	Address     Address
	Version     string
	// SumsFile is the name of the checksums file. It is empty for a module.
	SumsFile string
	// SumsSHA256 is the SHA-256 of the checksums file. It is empty for a module.
	SumsSHA256 string
	// KeyFingerprint is the fingerprint of the key that made the verified
	// signature. It is empty when the release has no signature.
	KeyFingerprint string
	// PublishedBy is the ID of the principal that published the release.
	PublishedBy string
	Archives    []Archive
}

// Validate reports whether r is complete and allowed. A provider needs at
// least one archive with a platform, and the checksums file. A module needs
// exactly one archive with no platform. It returns ErrInvalid otherwise.
func (r Release) Validate() error {
	if err := r.Address.Validate(); err != nil {
		return err
	}

	if err := ValidateVersion(r.Version); err != nil {
		return err
	}

	if r.PublishedAt.IsZero() || r.PublishedBy == "" {
		return fmt.Errorf("%w: publisher and time", ErrInvalid)
	}

	if len(r.Archives) == 0 || len(r.Archives) > MaxArchives {
		return fmt.Errorf("%w: archive count", ErrInvalid)
	}

	if r.Address.Kind == KindModule {
		return r.validateModule()
	}

	return r.validateProvider()
}

// SameContent reports whether r and other hold the same address, version and
// hashes. The publisher and the time do not count. Publish uses it to tell a
// repeat from a conflict.
func (r Release) SameContent(other Release) bool {
	if r.Address != other.Address || r.Version != other.Version ||
		r.SumsFile != other.SumsFile || r.SumsSHA256 != other.SumsSHA256 ||
		len(r.Archives) != len(other.Archives) {
		return false
	}

	sorted := func(in []Archive) []Archive {
		out := slices.Clone(in)
		slices.SortFunc(out, func(a, b Archive) int {
			if a.FileName < b.FileName {
				return -1
			}

			if a.FileName > b.FileName {
				return 1
			}

			return 0
		})

		return out
	}

	return slices.Equal(sorted(r.Archives), sorted(other.Archives))
}

func (r Release) validateModule() error {
	if len(r.Archives) != 1 || r.SumsFile != "" || r.SumsSHA256 != "" {
		return fmt.Errorf("%w: a module has one archive and no checksums file", ErrInvalid)
	}

	if r.Archives[0].Platform != "" {
		return fmt.Errorf("%w: archive %q: a module has no platform", ErrInvalid, r.Archives[0].FileName)
	}

	return validateArchive(r.Archives[0])
}

func (r Release) validateProvider() error {
	if !fileNameRE.MatchString(r.SumsFile) || len(r.SumsFile) > MaxFileNameLength ||
		!sha256RE.MatchString(r.SumsSHA256) {
		return fmt.Errorf("%w: checksums file", ErrInvalid)
	}

	seenName := map[string]bool{}
	seenPlatform := map[string]bool{}

	for _, a := range r.Archives {
		if err := validateArchive(a); err != nil {
			return err
		}

		if !platformRE.MatchString(a.Platform) {
			return fmt.Errorf("%w: archive %q: platform", ErrInvalid, a.FileName)
		}

		if seenName[a.FileName] || seenPlatform[a.Platform] {
			return fmt.Errorf("%w: duplicate archive %q", ErrInvalid, a.FileName)
		}

		seenName[a.FileName] = true
		seenPlatform[a.Platform] = true
	}

	return nil
}

// validateArchive checks the name, the hash and the size of one archive.
func validateArchive(a Archive) error {
	if !fileNameRE.MatchString(a.FileName) || len(a.FileName) > MaxFileNameLength {
		return fmt.Errorf("%w: archive file name %q", ErrInvalid, a.FileName)
	}

	if !sha256RE.MatchString(a.SHA256) || a.Size <= 0 {
		return fmt.Errorf("%w: archive %q hash or size", ErrInvalid, a.FileName)
	}

	return nil
}
