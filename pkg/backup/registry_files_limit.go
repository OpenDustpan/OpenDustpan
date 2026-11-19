package backup

import (
	"errors"
	"fmt"
	"slices"
)

// ErrRegistryFilesTooLarge reports a backup whose registry files are above the
// limit that a restore applies: one file above MaxRegistryFileBytes, or the
// files of one namespace above MaxRegistryFilesTotalBytes. The backup writes
// no manifest, so no backup exists that a restore must refuse. A
// *RegistryFilesTooLargeError matches it.
var ErrRegistryFilesTooLarge = errors.New("the registry files are above the limit of a restore")

// RegistryFileLimit names the limit that a registry file passed.
type RegistryFileLimit string

const (
	// RegistryFileLimitFile is the limit of one registry file.
	RegistryFileLimitFile RegistryFileLimit = "file"
	// RegistryFileLimitNamespace is the limit of all registry files of one
	// namespace.
	RegistryFileLimitNamespace RegistryFileLimit = "namespace"
)

// RegistryFilesTooLargeError carries the facts of ErrRegistryFilesTooLarge:
// the release and the version of the file that passed the limit, the limit
// that it passed, the size of the file and the total with that file. For a
// file above the limit of one file, Total is the size of the file.
type RegistryFilesTooLargeError struct {
	Release string
	Version string
	Limit   RegistryFileLimit
	// Size is the size of the file in bytes.
	Size int64
	// Total is the total of the namespace with this file, in bytes.
	Total int64
	// Max is the limit that the file passed, in bytes.
	Max int64
}

// Error returns the text of the refusal. It names the release, the version,
// the size, the limit and the action that helps.
func (e *RegistryFilesTooLargeError) Error() string {
	if e.Limit == RegistryFileLimitNamespace {
		return fmt.Sprintf(
			"The registry files of one namespace are %d bytes with the file of release %q version %q, "+
				"above the limit of %d bytes. The backup wrote no manifest. "+
				"Remove releases from the namespace, then run the backup again.",
			e.Total, e.Release, e.Version, e.Max,
		)
	}

	return fmt.Sprintf(
		"The registry file of release %q version %q is %d bytes, above the limit of %d bytes for one file. "+
			"The backup wrote no manifest. Remove the release, then run the backup again.",
		e.Release, e.Version, e.Size, e.Max,
	)
}

// Is reports whether target is ErrRegistryFilesTooLarge.
func (e *RegistryFilesTooLargeError) Is(target error) bool {
	return target == ErrRegistryFilesTooLarge
}

// registryFileOverLimit returns the limit that a file of size bytes passes
// when the files before it hold total bytes, or "" when it passes none. A
// negative size is not a size: the caller refuses it before. The test of the
// total is written so that no sum overflows.
func registryFileOverLimit(size, total int64) RegistryFileLimit {
	switch {
	case size > MaxRegistryFileBytes:
		return RegistryFileLimitFile
	case total > MaxRegistryFilesTotalBytes-size:
		return RegistryFileLimitNamespace
	}

	return ""
}

// CheckRegistryFile refuses one registry file at backup. size is the size of
// the file and total is the sum of the files of the namespace before it. It
// returns a *RegistryFilesTooLargeError for a size above a limit that a
// restore applies, and an error that wraps ErrCorrupt for a negative size.
// The restore applies the same constants, so a backup that passes here is not
// refused for its registry files.
func CheckRegistryFile(release, version string, size, total int64) error {
	if size < 0 || total < 0 {
		return fmt.Errorf(
			"registry file of release %q version %q has the size %d and the total %d: %w",
			release, version, size, total, ErrCorrupt,
		)
	}

	limit := registryFileOverLimit(size, total)
	switch limit {
	case "":
		return nil
	case RegistryFileLimitFile:
		return &RegistryFilesTooLargeError{
			Release: release, Version: version, Limit: limit,
			Size: size, Total: size, Max: MaxRegistryFileBytes,
		}
	case RegistryFileLimitNamespace:
		return &RegistryFilesTooLargeError{
			Release: release, Version: version, Limit: limit,
			Size: size, Total: total + size, Max: MaxRegistryFilesTotalBytes,
		}
	}

	return nil
}

// CheckRegistryFileLimits refuses the registry files of a backup that a
// restore refuses. files holds the entries of each namespace, one for each
// content hash. The service calls it before it copies a blob and before it
// writes the manifest.
//
// Defense in depth: the store collector checks each file when it reads the
// release rows. This check stays so that a store that does not check, or a
// later caller, cannot write a manifest that no restore accepts. It does not
// rely on the collector.
func CheckRegistryFileLimits(files map[string][]RegistryFileEntry) error {
	namespaces := make([]string, 0, len(files))
	for id := range files {
		namespaces = append(namespaces, id)
	}

	slices.Sort(namespaces)

	for _, id := range namespaces {
		var total int64

		for _, e := range files[id] {
			if err := CheckRegistryFile(e.Release, e.Version, e.Size, total); err != nil {
				return err
			}

			total += e.Size
		}
	}

	return nil
}
