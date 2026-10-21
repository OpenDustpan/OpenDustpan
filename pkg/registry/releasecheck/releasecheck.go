// Package releasecheck checks the archive set of a provider release after the
// server has measured it. The checks run in a fixed order and each one has its
// own tests. The package reads no file and no network: the caller gives it the
// raw bytes of the checksums file and the SHA-256 hash and the size of each
// file that the server computed while it stored the file.
//
// The signature check is not here. The caller checks the signature over the raw
// bytes of the checksums file first, and calls ParseSums only for a checksums
// file whose signature is valid, so this parser never reads bytes that nobody
// signed.
package releasecheck

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"opendustpan.org/opendustpan/pkg/registry"
)

// ErrRefused reports an archive set that fails a check. The text names the
// file that fails.
var ErrRefused = errors.New("archive set refused")

// Bounds of the checksums file.
const (
	// MaxSumsBytes is the largest checksums file. One line holds a hash, two
	// spaces and a file name, so 64 archives need less than 32 KiB.
	MaxSumsBytes = 64 << 10
	// maxLineBytes is the longest line of the checksums file: 64 hash digits,
	// two spaces and a file name.
	maxLineBytes = 64 + 2 + registry.MaxFileNameLength
)

var (
	sumLineRE = regexp.MustCompile(`^([0-9a-f]{64})  ([A-Za-z0-9][A-Za-z0-9._-]*)$`)
	// archiveRE splits "terraform-provider-<name>_<version>_<os>_<arch>.zip".
	// The name has no underscore, because the registry-safe subset of a name has
	// none. The version has none either. The pattern is anchored, so the parts
	// are exact.
	archiveRE = regexp.MustCompile(
		`^terraform-provider-([a-z0-9-]+)_([0-9A-Za-z.+-]+)_([a-z0-9]+)_([a-z0-9]+)\.zip$`,
	)
)

// File is a file of the upload as the server measured it.
type File struct {
	Name   string
	SHA256 string
	Size   int64
}

// Set is the archive set of one provider release: the checksums file, its
// detached signature file and the archives, each measured by the server.
type Set struct {
	Sums      File
	Signature File
	Archives  []File
}

// SumsName returns the name of the checksums file of a release:
// "terraform-provider-<name>_<version>_SHA256SUMS".
func SumsName(name, version string) string {
	return "terraform-provider-" + name + "_" + version + "_SHA256SUMS"
}

// SignatureName returns the name of the signature file of a release.
func SignatureName(name, version string) string {
	return SumsName(name, version) + ".sig"
}

func refuse(file, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrRefused, file, fmt.Sprintf(format, args...))
}

// ParseSums reads the checksums file. Each line is a lowercase SHA-256, two
// spaces and a file name, as the build tool writes it. The result maps a file
// name to its hash. ParseSums refuses a line with another form, a name with a
// path, a duplicate name, an empty file and a file that is too large. The name
// of the file in errors is sumsName.
func ParseSums(sumsName string, raw []byte) (map[string]string, error) {
	if len(raw) == 0 || len(raw) > MaxSumsBytes {
		return nil, refuse(sumsName, "the file is empty or larger than %d bytes", MaxSumsBytes)
	}

	text := strings.TrimSuffix(string(raw), "\n")
	lines := strings.Split(text, "\n")

	if len(lines) > registry.MaxArchives {
		return nil, refuse(sumsName, "more than %d lines", registry.MaxArchives)
	}

	sums := make(map[string]string, len(lines))

	for i, line := range lines {
		if len(line) > maxLineBytes {
			return nil, refuse(sumsName, "line %d is too long", i+1)
		}

		m := sumLineRE.FindStringSubmatch(line)
		if m == nil {
			return nil, refuse(sumsName, "line %d must be a lowercase SHA-256, two spaces and a file name", i+1)
		}

		if _, dup := sums[m[2]]; dup {
			return nil, refuse(sumsName, "line %d repeats the file name %s", i+1, m[2])
		}

		sums[m[2]] = m[1]
	}

	return sums, nil
}

// Check checks set against the parsed checksums for the release of version at
// addr, and returns the release fields that the set decides: the file names, the
// hashes and the archives, in the order of the checksums file names. The caller
// fills the publisher, the time and the fingerprint of the key.
//
// The order of the checks is: the names of the checksums file and the signature
// file, the lines against the files of the upload, the hash of every archive,
// and the name of every archive against the address, the version and the
// platform.
func Check(addr registry.Address, version string, set Set, sums map[string]string) (registry.Release, error) {
	var none registry.Release

	if err := addr.Validate(); err != nil || addr.Kind != registry.KindProvider {
		return none, fmt.Errorf("%w: the address is not a provider", ErrRefused)
	}

	if err := registry.ValidateVersion(version); err != nil {
		return none, fmt.Errorf("%w: %w", ErrRefused, err)
	}

	if set.Sums.Name != SumsName(addr.Name, version) {
		return none, refuse(set.Sums.Name, "the checksums file must be named %s", SumsName(addr.Name, version))
	}

	if set.Signature.Name != SignatureName(addr.Name, version) {
		return none, refuse(set.Signature.Name, "the signature file must be named %s", SignatureName(addr.Name, version))
	}

	if err := checkLines(set, sums); err != nil {
		return none, err
	}

	rel := registry.Release{
		Address: addr, Version: version, SumsFile: set.Sums.Name, SumsSHA256: set.Sums.SHA256,
		SignatureFile: set.Signature.Name, SignatureSHA256: set.Signature.SHA256,
	}

	for _, a := range set.Archives {
		platform, err := checkArchive(addr, version, a, sums)
		if err != nil {
			return none, err
		}

		rel.Archives = append(rel.Archives, registry.Archive{
			Platform: platform, FileName: a.Name, SHA256: a.SHA256, Size: a.Size,
		})
	}

	return rel, nil
}

// checkLines compares the names of the checksums file with the names of the
// archives of the upload, in both directions: a line for a file that is not in
// the upload is refused, and an archive with no line is refused, because the
// signature covers only the lines.
func checkLines(set Set, sums map[string]string) error {
	if len(set.Archives) == 0 {
		return refuse(set.Sums.Name, "the upload has no archive")
	}

	if len(set.Archives) > registry.MaxArchives {
		return refuse(set.Sums.Name, "more than %d archives", registry.MaxArchives)
	}

	uploaded := make(map[string]bool, len(set.Archives))

	for _, a := range set.Archives {
		// The name of the checksums file and of the signature file never equal the
		// name of an archive.
		if a.Name == set.Sums.Name || a.Name == set.Signature.Name {
			return refuse(a.Name, "an archive has the name of the checksums file or of the signature file")
		}

		if uploaded[a.Name] {
			return refuse(a.Name, "the upload repeats the file name")
		}

		uploaded[a.Name] = true
	}

	for name := range sums {
		if !uploaded[name] {
			return refuse(name, "the checksums file names a file that is not in the upload")
		}
	}

	for _, a := range set.Archives {
		if _, ok := sums[a.Name]; !ok {
			return refuse(a.Name, "the checksums file has no line for this archive")
		}
	}

	return nil
}

// checkArchive checks the hash of one archive and its name, and returns its
// platform.
func checkArchive(addr registry.Address, version string, a File, sums map[string]string) (string, error) {
	if a.SHA256 != sums[a.Name] {
		return "", refuse(a.Name, "the SHA-256 of the file is not the SHA-256 in the checksums file")
	}

	m := archiveRE.FindStringSubmatch(a.Name)
	if m == nil {
		return "", refuse(a.Name, "the name must be terraform-provider-<name>_<version>_<os>_<arch>.zip")
	}

	if m[1] != addr.Name || m[2] != version {
		return "", refuse(a.Name, "the name does not match the provider %s and the version %s", addr.Name, version)
	}

	return m[3] + "_" + m[4], nil
}
