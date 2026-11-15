package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
)

// backupCreateHint is the one command that helps after a registry file
// failure. Error text names it so the operator knows the next step.
const backupCreateHint = "opendustpan backup create --namespace <namespace>"

// RegistryFileSectionVersion is the version of the registry file manifest
// section. A reader before this change ignores the section by its version
// field and warns that files are absent.
const RegistryFileSectionVersion = 1

// WarnRegistryFilesAbsent says that the restored backup holds registry
// rows and no registry file manifest section: it is a backup from before
// file copy, so the restore restores rows without files and marks each
// file-backed release unavailable.
const WarnRegistryFilesAbsent RestoreWarningCode = "registry-files-absent"

// registrySHA256RE allows only a SHA-256 in 64 lowercase hex characters, so a
// hostile hash with a separator never becomes part of a blob key.
var registrySHA256RE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ValidRegistryFileHash reports whether hash is a SHA-256 in 64 lowercase hex
// characters.
func ValidRegistryFileHash(hash string) bool {
	return registrySHA256RE.MatchString(hash)
}

// RegistryFileEntry is one copied registry release file: its SHA-256 content
// key, its size in bytes, and the release and version that names it.
type RegistryFileEntry struct {
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
	Release string `json:"release"`
	Version string `json:"version"`
}

// RegistryFileSection is the registry file manifest section of a backup: the
// section version plus one entry per unique file. A nil section means a backup
// from before file copy; an empty non-nil section means a new backup with
// zero release files.
type RegistryFileSection struct {
	Version int                 `json:"version"`
	Files   []RegistryFileEntry `json:"files"`
}

// RegistryFileReport is the count of copied registry files and the total
// bytes copied, as the backup result reports it.
type RegistryFileReport struct {
	Count      int   `json:"count"`
	TotalBytes int64 `json:"totalBytes"`
}

// RegistryFileCollector gathers one entry per unique registry file hash. The
// zero value is not ready; build it with NewRegistryFileCollector.
type RegistryFileCollector struct {
	namespaceID string
	entries     []RegistryFileEntry
	seen        map[string]struct{}
}

// NewRegistryFileCollector builds a collector for the namespace with the
// given ID. It returns an error that names the namespace ID when the ID is
// empty.
func NewRegistryFileCollector(namespaceID string) (*RegistryFileCollector, error) {
	if namespaceID == "" {
		return nil, fmt.Errorf("registry file collector: namespace ID is empty: %w", ErrCorrupt)
	}

	return &RegistryFileCollector{
		namespaceID: namespaceID,
		entries:     nil,
		seen:        make(map[string]struct{}),
	}, nil
}

// AddRelease adds one entry per unique hash of a published release. It keeps
// the first release and version that names a shared key, so a shared content
// key is stored once. It returns an error that names the release and version
// when a hash is empty or outside 64 lowercase hex characters.
func (c *RegistryFileCollector) AddRelease(
	release, version string,
	hashes []string,
	sizes []int64,
) error {
	for i, hash := range hashes {
		var size int64
		if i < len(sizes) {
			size = sizes[i]
		}

		if _, dup := c.seen[hash]; dup {
			continue
		}

		if !ValidRegistryFileHash(hash) {
			return fmt.Errorf(
				"registry file of release %q version %q: hash %q is not valid: %w",
				release, version, hash, ErrCorrupt,
			)
		}

		c.seen[hash] = struct{}{}
		c.entries = append(c.entries, RegistryFileEntry{
			SHA256:  hash,
			Size:    size,
			Release: release,
			Version: version,
		})
	}

	return nil
}

// Entries returns the collected entries in the order added.
func (c *RegistryFileCollector) Entries() []RegistryFileEntry {
	return slices.Clone(c.entries)
}

// Section returns the manifest section with the section version field.
func (c *RegistryFileCollector) Section() RegistryFileSection {
	return RegistryFileSection{Version: RegistryFileSectionVersion, Files: SortRegistryFileEntries(c.entries)}
}

// Report returns the count of files and the total bytes copied.
func (c *RegistryFileCollector) Report() RegistryFileReport {
	var total int64
	for _, e := range c.entries {
		total += e.Size
	}

	return RegistryFileReport{Count: len(c.entries), TotalBytes: total}
}

// VerifyRegistryFileContent checks that content matches the SHA-256 content
// key. It returns an error that names the key when the bytes differ.
func VerifyRegistryFileContent(hash string, content []byte) error {
	sum := sha256.Sum256(content)
	if hex.EncodeToString(sum[:]) != hash {
		return fmt.Errorf("registry file %q does not match its SHA-256 content key: %w", hash, ErrCorrupt)
	}

	return nil
}

// RegistryFileBlobKey builds the blob key of a registry file under the
// namespace with the given ID. It validates the hash first and never joins
// raw input into a path.
func RegistryFileBlobKey(namespaceID, hash string) (string, error) {
	if namespaceID == "" {
		return "", fmt.Errorf("registry file key: namespace ID is empty: %w", ErrCorrupt)
	}

	if !ValidRegistryFileHash(hash) {
		return "", fmt.Errorf("registry file key %q is not valid: %w", hash, ErrCorrupt)
	}

	return "namespaces/" + namespaceID + "/registry/sha256/" + hash, nil
}

// RegistryFileStore is the target blob view that a registry file restore
// writes: it reads, writes, checks and removes keys by their full blob key.
type RegistryFileStore interface {
	// Has reports whether key holds bytes.
	Has(key string) (bool, error)
	// Read returns the bytes under key, or an error when the key is missing.
	Read(key string) ([]byte, error)
	// Write makes content visible under key.
	Write(key string, content []byte) error
	// Remove deletes key. It reports no error when the key is already gone.
	Remove(key string) error
}

// RestoreRegistryFiles checks every named file before making any registry
// file visible, then stages and hash-checks each file and makes it visible
// under the target namespace ID. It removes only the keys this restore made
// visible when it fails, so a retry with the same backup completes.
//
// A nil section is a backup from before file copy: the restore warns and
// restores rows without files. A non-nil section, even an empty one, is a new
// backup and restores without the file warning.
func RestoreRegistryFiles(
	section *RegistryFileSection,
	files map[string][]byte,
	targetNamespaceID string,
	store RegistryFileStore,
) (RestoreWarning, error) {
	if section == nil {
		return legacyRegistryFilesWarning(), nil
	}

	if err := checkRegistrySection(section); err != nil {
		return RestoreWarning{Code: "", Text: ""}, err
	}

	if targetNamespaceID == "" {
		return RestoreWarning{Code: "", Text: ""}, fmt.Errorf(
			"restore registry files: namespace ID is empty: %w",
			ErrCorrupt,
		)
	}

	if err := checkRegistryFilesPresent(section, files); err != nil {
		return RestoreWarning{Code: "", Text: ""}, err
	}

	if err := stageRegistryFiles(section, files, targetNamespaceID, store); err != nil {
		return RestoreWarning{Code: "", Text: ""}, err
	}

	return RestoreWarning{Code: "", Text: ""}, nil
}

// legacyRegistryFilesWarning is the warning of a restore from a backup with
// registry rows and no registry file manifest section. It names the backup
// command that makes a complete backup.
func legacyRegistryFilesWarning() RestoreWarning {
	text := fmt.Sprintf(
		"restore holds registry rows without registry files: "+
			"files are absent, releases are unavailable; run %s again from the source installation",
		backupCreateHint,
	)

	return RestoreWarning{Code: WarnRegistryFilesAbsent, Text: text}
}

// checkRegistrySection rejects a manifest entry outside 64 lowercase hex
// characters before any registry file write.
func checkRegistrySection(section *RegistryFileSection) error {
	for _, e := range section.Files {
		if !ValidRegistryFileHash(e.SHA256) {
			return fmt.Errorf(
				"restore registry files: manifest key %q is not valid, run %s again: %w",
				e.SHA256, backupCreateHint, ErrCorrupt,
			)
		}
	}

	return nil
}

// checkRegistryFilesPresent refuses the restore when a named file is missing
// or does not match its SHA-256 content key, before anything becomes visible.
func checkRegistryFilesPresent(section *RegistryFileSection, files map[string][]byte) error {
	for _, e := range section.Files {
		content, ok := files[e.SHA256]
		if !ok {
			return fmt.Errorf(
				"restore registry files: registry file %q is missing, run %s again: %w",
				e.SHA256, backupCreateHint, ErrCorrupt,
			)
		}

		if err := VerifyRegistryFileContent(e.SHA256, content); err != nil {
			return fmt.Errorf("restore registry files: %w, run %s again", err, backupCreateHint)
		}
	}

	return nil
}

// stageRegistryFiles hash-checks each staged file and makes it visible under
// the target namespace ID. It keeps a pre-existing key only when the stored
// bytes match the SHA-256 key, and it removes only the keys this restore made
// visible when it fails.
func stageRegistryFiles(
	section *RegistryFileSection,
	files map[string][]byte,
	targetNamespaceID string,
	store RegistryFileStore,
) error {
	pending, err := planRegistryWrites(section, files, targetNamespaceID, store)
	if err != nil {
		return err
	}

	var visible []string

	for _, w := range pending {
		if err := VerifyRegistryFileContent(w.hash, w.content); err != nil {
			removeRegistryKeys(store, visible)

			return fmt.Errorf("restore registry files: %w, run %s again", err, backupCreateHint)
		}

		if err := store.Write(w.key, w.content); err != nil {
			removeRegistryKeys(store, visible)

			return fmt.Errorf("restore registry files: write %q: %w", w.key, err)
		}

		visible = append(visible, w.key)
	}

	return nil
}

type registryWrite struct {
	key     string
	hash    string
	content []byte
}

func planRegistryWrites(
	section *RegistryFileSection,
	files map[string][]byte,
	targetNamespaceID string,
	store RegistryFileStore,
) ([]registryWrite, error) {
	var pending []registryWrite

	for _, e := range section.Files {
		key, err := RegistryFileBlobKey(targetNamespaceID, e.SHA256)
		if err != nil {
			return nil, err
		}

		keep, err := keepExistingRegistryKey(store, key, e.SHA256)
		if err != nil {
			return nil, err
		}

		if keep {
			continue
		}

		pending = append(pending, registryWrite{
			key:     key,
			hash:    e.SHA256,
			content: slices.Clone(files[e.SHA256]),
		})
	}

	return pending, nil
}

// keepExistingRegistryKey reports whether key already holds bytes that match
// the SHA-256 key. It refuses when the stored bytes differ.
func keepExistingRegistryKey(store RegistryFileStore, key, hash string) (bool, error) {
	has, err := store.Has(key)
	if err != nil {
		return false, fmt.Errorf("restore registry files: check %q: %w", hash, err)
	}

	if !has {
		return false, nil
	}

	stored, err := store.Read(key)
	if err != nil {
		return false, fmt.Errorf("restore registry files: read %q: %w", hash, err)
	}

	if err := VerifyRegistryFileContent(hash, stored); err != nil {
		return false, fmt.Errorf("restore registry files: %w, run %s again", err, backupCreateHint)
	}

	return true, nil
}

func removeRegistryKeys(store RegistryFileStore, keys []string) {
	for _, key := range keys {
		if err := store.Remove(key); err != nil {
			continue
		}
	}
}
