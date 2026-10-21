// Package registry is the model of the private registry; the address and the
// ports are tool-neutral. It holds the address of a provider or module that a
// team publishes, one immutable release of it, and the ports that store
// releases and check signatures.
//
// A release is the archive set of a provider (the zip archives, the checksums
// file and its detached signature) or the archive of a module. The package
// keeps the names and the SHA-256 hashes. The bytes are blobs in the blob
// port. The package knows no wire format of a tool. The adapter of each tool
// builds its own JSON from a release.
//
// The ports and the blob keys are shared by every tool. The Release shape is
// not: it fits the providers and the modules of OpenTofu. A mirror of Pulumi
// plugins needs its own release shape later.
package registry

import (
	"cmp"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
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
	// MaxNameLength is the longest name or system. A file name has its own
	// limit, MaxFileNameLength.
	MaxNameLength = 64
	// MaxFileNameLength is the longest file name of an archive.
	MaxFileNameLength = 255
	// MaxArchives is the most archives in one release. A provider has one
	// archive for each platform.
	MaxArchives = 64
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
	versionRE = regexp.MustCompile(
		`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`,
	)
	fileNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	sha256RE   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	platformRE = regexp.MustCompile(`^[a-z0-9]+_[a-z0-9]+$`)
	// slugRE is the registry-safe subset of a name: lowercase letters and
	// digits, with single dashes inside. A name outside it cannot publish.
	slugRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	// systemRE is the target system of a module. OpenTofu allows only ASCII
	// letters and digits there.
	systemRE      = regexp.MustCompile(`^[a-z0-9]{1,64}$`)
	fingerprintRE = regexp.MustCompile(`^[0-9A-F]{40,64}$`)
	protocolRE    = regexp.MustCompile(`^[0-9]{1,2}\.[0-9]{1,2}$`)
	uploadIDRE    = regexp.MustCompile(`^[a-z0-9]{20,64}$`)
)

const slugRule = "use lowercase letters, digits and single dashes, with no dash at the start or the end"

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
//
// The namespace and the name use only the registry-safe subset: lowercase
// letters, digits and single dashes, with no dash at the start or the end,
// at most 64 characters. A namespace whose name is outside the subset cannot
// publish. The check does not fold case, so "Acme" and "acme" never meet.
// A module name refuses "_" too, although OpenTofu allows it. The system of a
// module is lowercase letters and digits only, as OpenTofu asks.
func (a Address) Validate() error {
	if _, err := namespace.ParseName(a.Namespace.String()); err != nil {
		return fmt.Errorf("%w: namespace: %w", ErrInvalid, err)
	}

	if !validSlug(a.Namespace.String()) {
		return fmt.Errorf("%w: namespace %q cannot publish: %s", ErrInvalid, a.Namespace, slugRule)
	}

	switch a.Kind {
	case KindProvider:
		if a.System != "" {
			return fmt.Errorf("%w: a provider has no system", ErrInvalid)
		}
	case KindModule:
		if !systemRE.MatchString(a.System) {
			return fmt.Errorf("%w: system: use 1 to 64 lowercase letters and digits", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: kind", ErrInvalid)
	}

	if !validSlug(a.Name) {
		return fmt.Errorf("%w: name %q: %s", ErrInvalid, a.Name, slugRule)
	}

	return nil
}

// validSlug reports whether s is in the registry-safe subset.
func validSlug(s string) bool {
	return len(s) <= MaxNameLength && slugRE.MatchString(s)
}

// ValidateVersion reports whether v is a semantic version without build
// metadata, with numbers that fit 64 bits. It returns ErrInvalid otherwise.
func ValidateVersion(v string) error {
	if _, _, ok := splitVersion(v); !ok {
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

// BlobKey returns the content key of the bytes with the given SHA-256 in the
// namespace with ID id: "namespaces/<id>/registry/sha256/<hash>". The key
// starts with the prefix of the namespace. The scoped view of the blob port
// must serve it and refuse the key of another namespace. The blob port does
// not have that view yet, so until it exists the namespace-ID prefix is the
// only guard.
//
// A blob under this key exists only after a publish passed every check, and
// a caller reads it only through a published release. The upload writes to
// StagingKey first, and the server moves the bytes to this key last.
func BlobKey(id namespace.ID, sha256 string) (blob.Key, error) {
	if _, err := namespace.ParseID(string(id)); err != nil {
		return "", fmt.Errorf("%w: namespace ID: %w", ErrInvalid, err)
	}

	if !sha256RE.MatchString(sha256) {
		return "", fmt.Errorf("%w: sha256", ErrInvalid)
	}

	key, err := blob.ParseKey("namespaces/" + string(id) + "/registry/sha256/" + sha256)
	if err != nil {
		return "", fmt.Errorf("%w: blob key: %w", ErrInvalid, err)
	}

	return key, nil
}

// StagingKey returns the key where an upload streams its bytes before any
// check: "namespaces/<id>/registry/staging/<upload>". The upload is a random
// token of 20 to 64 lowercase letters and digits. The server computes the
// SHA-256 while it writes, and it never serves a staging key. A sweeper
// removes a staging blob that no publish moved, after a grace period.
func StagingKey(id namespace.ID, upload string) (blob.Key, error) {
	if _, err := namespace.ParseID(string(id)); err != nil {
		return "", fmt.Errorf("%w: namespace ID: %w", ErrInvalid, err)
	}

	if !uploadIDRE.MatchString(upload) {
		return "", fmt.Errorf("%w: upload token", ErrInvalid)
	}

	key, err := blob.ParseKey("namespaces/" + string(id) + "/registry/staging/" + upload)
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
	// SignatureFile is the name of the detached signature file of the
	// checksums file. It is required on a provider and empty on a module.
	SignatureFile string
	// SignatureSHA256 is the SHA-256 of the signature file. It is required on
	// a provider and empty on a module.
	SignatureSHA256 string
	// KeyFingerprint is the fingerprint of the primary key that made the
	// verified signature, in uppercase hex. It is required on a provider and
	// empty on a module.
	KeyFingerprint string
	// PublishedBy is the subject of the principal that published the release.
	PublishedBy string
	// Protocols lists the plugin protocol versions of the provider, for example
	// "5.0" and "6.0", from the lowest to the highest. The publisher states
	// them, and the registry protocol of OpenTofu serves them. It is required
	// on a provider and empty on a module.
	Protocols []string
	Archives  []Archive
}

// Validate reports whether r is complete and allowed. A provider needs at
// least one archive with a platform, the checksums file, and a verified
// signature: the signature file, its hash and the key fingerprint. A module needs
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

// SameContent reports whether r and other hold the same address, version,
// hashes, signature, key fingerprint and protocols. The publisher and the time do not count. Publish uses it to tell a
// repeat from a conflict.
func (r Release) SameContent(other Release) bool {
	if r.Address != other.Address || r.Version != other.Version ||
		r.SumsFile != other.SumsFile || r.SumsSHA256 != other.SumsSHA256 ||
		r.SignatureFile != other.SignatureFile || r.SignatureSHA256 != other.SignatureSHA256 ||
		r.KeyFingerprint != other.KeyFingerprint || !slices.Equal(r.Protocols, other.Protocols) ||
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
	if len(r.Archives) != 1 || r.SumsFile != "" || r.SumsSHA256 != "" ||
		r.SignatureFile != "" || r.SignatureSHA256 != "" || r.KeyFingerprint != "" || len(r.Protocols) != 0 {
		return fmt.Errorf("%w: a module has one archive, no checksums file and no signature", ErrInvalid)
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

	if err := r.validateSignature(); err != nil {
		return err
	}

	if err := validateProtocols(r.Protocols); err != nil {
		return err
	}

	seenName := map[string]bool{r.SumsFile: true, r.SignatureFile: true}

	seenPlatform := map[string]bool{}

	for _, a := range r.Archives {
		if err := validateArchive(a); err != nil {
			return err
		}

		if !platformRE.MatchString(a.Platform) {
			return fmt.Errorf("%w: archive %q: platform", ErrInvalid, a.FileName)
		}

		// An archive never has the name of the checksums file or of the
		// signature file, and two archives never share a name or a platform.
		if seenName[a.FileName] || seenPlatform[a.Platform] {
			return fmt.Errorf("%w: duplicate or reserved archive name %q", ErrInvalid, a.FileName)
		}

		seenName[a.FileName] = true
		seenPlatform[a.Platform] = true
	}

	return nil
}

// MaxProtocols is the most plugin protocol versions of one provider release.
const MaxProtocols = 4

// validateProtocols checks that the protocols are one to MaxProtocols plugin
// protocol versions in the form "5.0", from the lowest to the highest, with no
// repeat.
func validateProtocols(protocols []string) error {
	if len(protocols) == 0 || len(protocols) > MaxProtocols {
		return fmt.Errorf("%w: a provider needs 1 to %d plugin protocol versions", ErrInvalid, MaxProtocols)
	}

	for i, p := range protocols {
		if !protocolRE.MatchString(p) {
			return fmt.Errorf("%w: plugin protocol %q: use the form 5.0", ErrInvalid, p)
		}

		if i > 0 && !protocolLess(protocols[i-1], p) {
			return fmt.Errorf("%w: plugin protocols must rise and not repeat", ErrInvalid)
		}
	}

	return nil
}

// protocolLess reports whether the protocol version a is lower than b. Both
// match protocolRE.
func protocolLess(a, b string) bool {
	am, an, _ := strings.Cut(a, ".")
	bm, bn, _ := strings.Cut(b, ".")

	ai, _ := strconv.Atoi(am)
	bi, _ := strconv.Atoi(bm)

	if ai != bi {
		return ai < bi
	}

	ani, _ := strconv.Atoi(an)
	bni, _ := strconv.Atoi(bn)

	return ani < bni
}

// validateSignature checks the signature fields of a provider. The file, its
// hash and the fingerprint are all required, because an unsigned provider
// release is refused (fail closed) and a stored signature names the key that
// verified it.
func (r Release) validateSignature() error {
	if !fileNameRE.MatchString(r.SignatureFile) || len(r.SignatureFile) > MaxFileNameLength ||
		!sha256RE.MatchString(r.SignatureSHA256) || !fingerprintRE.MatchString(r.KeyFingerprint) {
		return fmt.Errorf("%w: a provider needs a signature file, its hash and a key fingerprint", ErrInvalid)
	}

	if r.SignatureFile == r.SumsFile {
		return fmt.Errorf("%w: the signature file has the name of the checksums file", ErrInvalid)
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

// CompareVersions orders two valid versions by semantic version precedence:
// the numbers compare as numbers, a version with a pre-release is lower than
// the same version without one, and the pre-release identifiers compare one
// by one (numbers as numbers, and a number is lower than text). It returns a
// negative number, zero or a positive number. It returns 0 for an invalid
// version, so a caller validates first.
func CompareVersions(a, b string) int {
	ac, ap, aok := splitVersion(a)
	bc, bp, bok := splitVersion(b)

	if !aok || !bok {
		return 0
	}

	for i := range ac {
		if c := cmp.Compare(ac[i], bc[i]); c != 0 {
			return c
		}
	}

	switch {
	case ap == "" && bp == "":
		return 0
	case ap == "":
		return 1
	case bp == "":
		return -1
	}

	return comparePre(strings.Split(ap, "."), strings.Split(bp, "."))
}

// splitVersion returns the three numbers and the pre-release of v.
func splitVersion(v string) ([3]uint64, string, bool) {
	var nums [3]uint64

	if len(v) > MaxVersionLength || !versionRE.MatchString(v) {
		return nums, "", false
	}

	core, pre, _ := strings.Cut(v, "-")

	// A numeric pre-release identifier has no leading zero (SemVer 2.0.0,
	// section 9), and it fits 64 bits, as the version numbers do. The pattern
	// cannot say it, so the check is here.
	for id := range strings.SplitSeq(pre, ".") {
		if id == "" || strings.Trim(id, "0123456789") != "" {
			continue
		}

		if len(id) > 1 && id[0] == '0' {
			return nums, "", false
		}

		if _, err := strconv.ParseUint(id, 10, 64); err != nil {
			return nums, "", false
		}
	}

	for i, part := range strings.Split(core, ".") {
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			// A number that does not fit 64 bits is not a version of this registry.
			return nums, "", false
		}

		nums[i] = n
	}

	return nums, pre, true
}

func comparePre(a, b []string) int {
	for i := range min(len(a), len(b)) {
		an, aErr := strconv.ParseUint(a[i], 10, 64)
		bn, bErr := strconv.ParseUint(b[i], 10, 64)

		var c int

		switch {
		case aErr == nil && bErr == nil:
			c = cmp.Compare(an, bn)
		case aErr == nil:
			c = -1
		case bErr == nil:
			c = 1
		default:
			c = strings.Compare(a[i], b[i])
		}

		if c != 0 {
			return c
		}
	}

	return cmp.Compare(len(a), len(b))
}
