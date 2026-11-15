package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// backupCreateHint is the one command that helps after a registry file
// failure. Error text names it so the operator knows the next step.
const backupCreateHint = "opendustpan backup"

// registryFilesAbsentText is the warning of a restore from a backup with
// registry rows and no registry file section. The restore result carries it
// under the code WarnRegistryFilesAbsent.
const registryFilesAbsentText = "The backup holds registry releases without their files. The restore " +
	"restored the release rows, and the registry serves no file of them. Run `opendustpan backup` again " +
	"on the source installation, then restore the new backup."

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
	Has(ctx context.Context, key string) (bool, error)
	// Read returns the bytes under key, or an error when the key is missing.
	Read(ctx context.Context, key string) ([]byte, error)
	// Write makes content visible under key.
	Write(ctx context.Context, key string, content []byte) error
	// Remove deletes key. It reports no error when the key is already gone.
	Remove(ctx context.Context, key string) error
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
	ctx context.Context,
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

	if err := stageRegistryFiles(ctx, section, files, targetNamespaceID, store); err != nil {
		return RestoreWarning{Code: "", Text: ""}, err
	}

	return RestoreWarning{Code: "", Text: ""}, nil
}

// legacyRegistryFilesWarning is the warning of a restore from a backup with
// registry rows and no registry file manifest section. It names the backup
// command that makes a complete backup.
func legacyRegistryFilesWarning() RestoreWarning {
	return RestoreWarning{Code: WarnRegistryFilesAbsent, Text: registryFilesAbsentText}
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
	ctx context.Context,
	section *RegistryFileSection,
	files map[string][]byte,
	targetNamespaceID string,
	store RegistryFileStore,
) error {
	pending, err := planRegistryWrites(ctx, section, files, targetNamespaceID, store)
	if err != nil {
		return err
	}

	var visible []string

	for _, w := range pending {
		if err := VerifyRegistryFileContent(w.hash, w.content); err != nil {
			removeRegistryKeys(ctx, store, visible)

			return fmt.Errorf("restore registry files: %w, run %s again", err, backupCreateHint)
		}

		if err := store.Write(ctx, w.key, w.content); err != nil {
			removeRegistryKeys(ctx, store, visible)

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
	ctx context.Context,
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

		keep, err := keepExistingRegistryKey(ctx, store, key, e.SHA256)
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
func keepExistingRegistryKey(ctx context.Context, store RegistryFileStore, key, hash string) (bool, error) {
	has, err := store.Has(ctx, key)
	if err != nil {
		return false, fmt.Errorf("restore registry files: check %q: %w", hash, err)
	}

	if !has {
		return false, nil
	}

	stored, err := store.Read(ctx, key)
	if err != nil {
		return false, fmt.Errorf("restore registry files: read %q: %w", hash, err)
	}

	if err := VerifyRegistryFileContent(hash, stored); err != nil {
		return false, fmt.Errorf("restore registry files: %w, run %s again", err, backupCreateHint)
	}

	return true, nil
}

func removeRegistryKeys(ctx context.Context, store RegistryFileStore, keys []string) {
	for _, key := range keys {
		if err := store.Remove(ctx, key); err != nil {
			continue
		}
	}
}

// RegistryFilesOf groups the registry file entries of each namespace into
// the manifest sections: one section per namespace, with the section version
// and the entries ordered by SHA-256 key. It returns an empty map when no
// namespace holds a published registry release, never nil, so the manifest
// of a backup of this change always differs from a manifest from before
// file copy.
func RegistryFilesOf(entries map[string][]RegistryFileEntry) map[string]RegistryFileSection {
	out := make(map[string]RegistryFileSection, len(entries))

	for nsID, list := range entries {
		out[nsID] = RegistryFileSection{Version: RegistryFileSectionVersion, Files: SortRegistryFileEntries(list)}
	}

	return out
}

// RegistryFileReportOf returns the count of the registry files that the
// sections name and their total bytes in the manifest: the size report of a
// backup result.
func RegistryFileReportOf(sections map[string]RegistryFileSection) RegistryFileReport {
	report := RegistryFileReport{Count: 0, TotalBytes: 0}

	for _, section := range sections {
		report.Count += len(section.Files)

		for _, e := range section.Files {
			report.TotalBytes += e.Size
		}
	}

	return report
}

// registryFilesLegacy reports whether the manifest of a backup is from
// before registry file copy: it holds no registry file section while its
// data holds registry release rows. The restore then warns, because the
// registry rows come back without their files.
func registryFilesLegacy(manifest Manifest) bool {
	return manifest.RegistryFiles == nil && manifest.RowCounts["registry_releases"] > 0
}

// registryFileNamespaces returns the namespace IDs of the manifest sections
// in order, and refuses a section key that is not a stable namespace ID
// before any registry file is read or written. The section keys are
// untrusted: the manifest is not signed.
func registryFileNamespaces(manifest Manifest) ([]string, error) {
	for nsID := range manifest.RegistryFiles {
		if _, err := namespace.ParseID(nsID); err != nil {
			return nil, fmt.Errorf("the registry file section names %q, not a namespace ID: %w", nsID, ErrCorrupt)
		}
	}

	return slices.Sorted(maps.Keys(manifest.RegistryFiles)), nil
}

// restoreRegistryFiles writes the registry files of each namespace of the
// backup into the live store, before the copy of the other blobs. Each file
// is hash-checked against its SHA-256 content key before it becomes visible,
// and each write checks the claim of the restore first, as the copy of every
// other blob does. A manifest without the registry file section is a backup
// from before file copy: the rows come back without the files, and the stage
// warned.
func (s *RestoreService) restoreRegistryFiles(
	ctx context.Context, fence durable.StepFence, in restoreInput, manifest Manifest,
) error {
	if manifest.RegistryFiles == nil {
		return nil
	}

	namespaces, err := registryFileNamespaces(manifest)
	if err != nil {
		return fmt.Errorf("restore %s of backup %s: %w", in.ID, in.BackupID, err)
	}

	store := liveRegistryFileStore{blobs: s.blobs, live: s.live, fence: fence, id: in.ID}

	for _, nsID := range namespaces {
		section := manifest.RegistryFiles[nsID]

		files, readErr := s.readRegistryFiles(ctx, in, nsID, section)
		if readErr != nil {
			return readErr
		}

		if _, err := RestoreRegistryFiles(ctx, &section, files, nsID, store); err != nil {
			return fmt.Errorf("restore %s: registry files of namespace %s: %w", in.ID, nsID, err)
		}
	}

	return nil
}

// readRegistryFiles reads the bytes of each file that the section of nsID
// names from the backup target. A file the backup does not hold stops the
// restore before any registry file becomes visible.
func (s *RestoreService) readRegistryFiles(
	ctx context.Context, in restoreInput, nsID string, section RegistryFileSection,
) (map[string][]byte, error) {
	// Defense in depth: RestoreRegistryFiles checks each hash too. This check
	// stays so that a hostile hash never becomes part of a target key at this
	// layer, whatever a later change does to the checks of the restore.
	files := make(map[string][]byte, len(section.Files))

	for _, e := range section.Files {
		if !ValidRegistryFileHash(e.SHA256) {
			return nil, fmt.Errorf(
				"restore %s of backup %s: the registry file section names the key %q: %w",
				in.ID, in.BackupID, e.SHA256, ErrCorrupt,
			)
		}

		raw := "blobs/namespaces/" + nsID + "/registry/sha256/" + e.SHA256

		key, err := blob.ParseKey(raw)
		if err != nil {
			return nil, fmt.Errorf("restore %s: the registry file key %q: %w", in.ID, raw, err)
		}

		content, err := readAllKey(ctx, s.target, key)
		if err != nil {
			return nil, fmt.Errorf("restore %s: registry file %q of namespace %s: %w", in.ID, e.SHA256, nsID, err)
		}

		files[e.SHA256] = content
	}

	return files, nil
}

// readAllKey returns the bytes that the store holds under key.
func readAllKey(ctx context.Context, store blob.Store, key blob.Key) ([]byte, error) {
	reader, err := store.Get(ctx, key)
	if err != nil {
		if errors.Is(err, blob.ErrNotFound) {
			return nil, &MissingObjectError{Key: key}
		}

		return nil, fmt.Errorf("read %q: %w", key, err)
	}

	content, readErr := io.ReadAll(reader)
	closeErr := reader.Close()

	if readErr != nil {
		return nil, fmt.Errorf("read %q: %w", key, readErr)
	}

	if closeErr != nil {
		return nil, fmt.Errorf("close %q: %w", key, closeErr)
	}

	return content, nil
}

// liveRegistryFileStore is the live blob store of one restore as a
// RegistryFileStore. Each write checks the claim of the restore first, as
// the copy of every other blob does.
type liveRegistryFileStore struct {
	blobs blob.Store
	live  RestoreLive
	fence durable.StepFence
	id    RestoreID
}

// Has reports whether the live store holds bytes under key.
func (s liveRegistryFileStore) Has(ctx context.Context, key string) (bool, error) {
	parsed, err := parseRegistryFileStoreKey(key)
	if err != nil {
		return false, err
	}

	if _, err = s.blobs.Get(ctx, parsed); err != nil {
		if errors.Is(err, blob.ErrNotFound) {
			return false, nil
		}

		return false, fmt.Errorf("read registry file %q: %w", key, err)
	}

	return true, nil
}

// Read returns the bytes that the live store holds under key.
func (s liveRegistryFileStore) Read(ctx context.Context, key string) ([]byte, error) {
	parsed, err := parseRegistryFileStoreKey(key)
	if err != nil {
		return nil, err
	}

	content, err := readAllKey(ctx, s.blobs, parsed)
	if err != nil {
		return nil, fmt.Errorf("read registry file %q: %w", key, err)
	}

	return content, nil
}

// Write makes content visible under key, after the claim check of the
// restore. A holder that lost its claim writes nothing.
func (s liveRegistryFileStore) Write(ctx context.Context, key string, content []byte) error {
	parsed, err := parseRegistryFileStoreKey(key)
	if err != nil {
		return err
	}

	if fenceErr := s.live.CheckRestoreFence(ctx, s.fence, s.id); fenceErr != nil {
		return fmt.Errorf("check the claim before the write of registry file %q: %w", key, fenceErr)
	}

	if err := s.blobs.Put(ctx, parsed, bytes.NewReader(content)); err != nil {
		return fmt.Errorf("write registry file %q: %w", key, err)
	}

	return nil
}

// Remove deletes key from the live store. A key that is already gone is no
// error.
func (s liveRegistryFileStore) Remove(ctx context.Context, key string) error {
	parsed, err := parseRegistryFileStoreKey(key)
	if err != nil {
		return err
	}

	if err := s.blobs.Delete(ctx, parsed); err != nil && !errors.Is(err, blob.ErrNotFound) {
		return fmt.Errorf("delete registry file %q: %w", key, err)
	}

	return nil
}

// parseRegistryFileStoreKey parses a registry file key that this package
// built, and refuses any other key at this layer.
func parseRegistryFileStoreKey(key string) (blob.Key, error) {
	parsed, err := blob.ParseKey(key)
	if err != nil {
		return "", fmt.Errorf("registry file key %q: %w: %w", key, err, ErrCorrupt)
	}

	return parsed, nil
}
