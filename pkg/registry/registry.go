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
	// ErrBadKey reports a public key that a namespace cannot register: it does
	// not parse, it holds private key material, it is expired or revoked, it
	// cannot sign, or it is weak. The text names the rule and holds no byte of
	// the key.
	ErrBadKey = errors.New("public key cannot be registered")
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

// nonArchiveFiles is the number of files of a provider release that are no
// archive: the checksums file and the signature file.
const nonArchiveFiles = 2

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
	md5RE      = regexp.MustCompile(`^[0-9a-f]{32}$`)
	platformRE = regexp.MustCompile(`^[a-z0-9]+_[a-z0-9]+$`)
	// slugRE is the registry-safe subset of a name: lowercase letters and
	// digits, with single dashes inside. A name outside it cannot publish.
	slugRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	// systemRE is the target system of a module. OpenTofu allows only ASCII
	// letters and digits there.
	systemRE      = regexp.MustCompile(`^[a-z0-9]{1,64}$`)
	fingerprintRE = regexp.MustCompile(`^[0-9A-F]{40,64}$`)
	protocolRE    = regexp.MustCompile(`^\d{1,2}\.\d{1,2}$`)
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

// Label returns the label of an address for a record or a message:
// "module/<name>/<system>". For a provider it returns "provider/<name>".
func (a Address) Label() string {
	if a.Kind == KindModule {
		return "module/" + a.Name + "/" + a.System
	}

	return "provider/" + a.Name
}

// CompareAddresses orders a and b by name, then by system, by code point.
func CompareAddresses(a, b Address) int {
	if a.Name != b.Name {
		return strings.Compare(a.Name, b.Name)
	}

	return strings.Compare(a.System, b.System)
}

// AddressKey returns the key of a in a page cursor: the name for a provider,
// and the name and the system joined by a slash for a module. A name and a
// system never hold a slash, so a key names one address.
func AddressKey(a Address) string {
	if a.Kind == KindModule {
		return a.Name + "/" + a.System
	}

	return a.Name
}

// ParseAddressKey reads the page cursor key of kind in ns. An empty key is
// the first page: it returns an address with an empty name. It returns
// ErrInvalid for a key that does not hold the name and the system of an
// address of kind.
func ParseAddressKey(ns namespace.Name, kind Kind, key string) (Address, error) {
	addr := Address{Namespace: ns, Kind: kind, Name: key, System: ""}

	if key == "" {
		return addr, nil
	}

	if kind == KindModule {
		name, system, ok := strings.Cut(key, "/")
		if !ok {
			return Address{}, fmt.Errorf("%w: cursor %q holds no system", ErrInvalid, key)
		}

		addr.Name, addr.System = name, system
	}

	if err := addr.Validate(); err != nil {
		return Address{}, err
	}

	return addr, nil
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
// serves it and refuses it in the view of another namespace.
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
	// Files holds the digests of the stored bytes of the files of the release:
	// the checksums file, the signature file and each archive. A backup copies
	// a file only when Files names it, and checks the copy against these
	// values. A release that an earlier version of the server stored has none.
	// SameContent does not compare Files: the digests follow from the hashes.
	Files []StoredFile
}

// StoredFile is the SHA-256, the size and the MD5 of the bytes that the blob
// store holds for one file of a release. A file is stored as it came, so these
// are the values of the plain file.
type StoredFile struct {
	// SHA256 is the lowercase hex SHA-256. It is also the name of the blob.
	SHA256 string
	// Size is the size in bytes.
	Size int64
	// MD5 is the lowercase hex MD5. The backup uses it to check its copy, as
	// it does for every other blob. It is not a security check.
	MD5 string
}

// FileHashes returns the SHA-256 of each file that the release stores: the
// checksums file, the signature file and each archive, once each and in that
// order. A module has the archive only.
func (r Release) FileHashes() []string {
	out := make([]string, 0, len(r.Archives)+nonArchiveFiles)

	for _, h := range append([]string{r.SumsSHA256, r.SignatureSHA256}, archiveHashes(r.Archives)...) {
		if h != "" && !slices.Contains(out, h) {
			out = append(out, h)
		}
	}

	return out
}

func archiveHashes(in []Archive) []string {
	out := make([]string, 0, len(in))
	for _, a := range in {
		out = append(out, a.SHA256)
	}

	return out
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

	var err error

	if r.Address.Kind == KindModule {
		err = r.validateModule()
	} else {
		err = r.validateProvider()
	}

	if err != nil {
		return err
	}

	return r.validateFiles()
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

// validateFiles checks the digests of r. Each digest names one file of the
// release, once, with a valid hash, size and MD5.
func (r Release) validateFiles() error {
	hashes := r.FileHashes()
	seen := make(map[string]bool, len(r.Files))

	for _, f := range r.Files {
		switch {
		case !sha256RE.MatchString(f.SHA256) || !slices.Contains(hashes, f.SHA256):
			return fmt.Errorf("%w: stored file %q is not a file of the release", ErrInvalid, f.SHA256)
		case seen[f.SHA256]:
			return fmt.Errorf("%w: stored file %q twice", ErrInvalid, f.SHA256)
		case f.Size <= 0 || !md5RE.MatchString(f.MD5):
			return fmt.Errorf("%w: stored file %q has no valid size or MD5", ErrInvalid, f.SHA256)
		}

		seen[f.SHA256] = true
	}

	return nil
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
// match protocolRE, so each part is one or two digits.
func protocolLess(a, b string) bool {
	pa, pb := protocolParts(a), protocolParts(b)

	if pa[0] != pb[0] {
		return pa[0] < pb[0]
	}

	return pa[1] < pb[1]
}

// protocolParts returns the two numbers of a protocol version that matches
// protocolRE. A part that is not a number counts as zero, and the caller has
// checked the form first.
func protocolParts(p string) [2]uint64 {
	major, minor, _ := strings.Cut(p, ".")

	return [2]uint64{digits(major), digits(minor)}
}

// decimalBase is the base of the digits that digits reads.
const decimalBase = 10

// digits returns the number that the decimal digits of s spell.
func digits(s string) uint64 {
	var n uint64

	for _, c := range []byte(s) {
		n = n*decimalBase + uint64(c-'0')
	}

	return n
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
