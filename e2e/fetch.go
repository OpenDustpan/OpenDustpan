//go:build e2e

package e2e

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	maxArchiveBytes = 1 << 30
	maxExtractBytes = 4 << 30
	dirMode         = 0o750
	fileMode        = 0o750
	lockMode        = 0o600
	// downloadAttempts is how many times a download of an archive is tried.
	// The wait before attempt n+1 is n times downloadRetryWait.
	downloadAttempts  = 3
	downloadRetryWait = 2 * time.Second
	// localhostName is the host name that always means the loopback address.
	localhostName = "localhost"
)

// Errors returned while downloading and unpacking a CLI.
var (
	// ErrUnpinnedFetch is returned when Install would ask a release host for the
	// newest version or for a published checksum file while the matrix of the tool
	// is the default. Set E2E_PULUMI_VERSIONS or E2E_TOFU_VERSIONS to allow it.
	ErrUnpinnedFetch    = errors.New("unpinned CLI version needs a network request")
	ErrChecksumMismatch = errors.New("archive checksum mismatch")
	ErrUnsafeArchive    = errors.New("unsafe archive entry")
	ErrMissingBinary    = errors.New("archive has no CLI binary")
	ErrCacheMismatch    = errors.New("cached CLI does not match its archive")
)

// Fetcher downloads CLI releases into a cache directory.
type Fetcher struct {
	// Client performs every request.
	Client *http.Client
	// CacheDir holds one extracted directory per tool, version and platform,
	// and the verified archive next to it.
	CacheDir string

	mu      sync.Mutex
	checked map[checkKey]bool

	// checks counts the checks of cache entries. beforeLock, if set, runs
	// after a call finds no record of a check and before it takes the lock
	// of the entry. Only tests read checks and set beforeLock.
	checks     atomic.Int64
	beforeLock func()
}

// checkKey names one cache check: the archive and the sum it was checked
// against.
type checkKey struct {
	archive string
	sum     string
}

// Install is a CLI ready to run.
type Install struct {
	// Tool is ToolPulumi or ToolTofu.
	Tool string
	// Label is what the matrix asked for, for example "latest".
	Label string
	// Version is the resolved release, for example "3.268.0".
	Version string
	// ChecksumSource says where the verifying checksum came from: "pinned" or "published".
	ChecksumSource string
	// Dir is the directory holding the binary (and, for Pulumi, its language plugins).
	Dir    string
	binary string
}

// NoCLI returns the Install of a test that drives the API without a CLI.
func NoCLI() Install {
	return Install{Tool: "", Label: "", Version: "", ChecksumSource: "", Dir: "", binary: ""}
}

// Binary returns the path of the executable.
func (i Install) Binary() string {
	return i.binary
}

// Install resolves label for tool, downloads and verifies the release if it is
// not cached, and returns it. Concurrent calls for different versions are safe.
func (f *Fetcher) Install(ctx context.Context, tool Tool, label string) (Install, error) {
	version, err := f.version(ctx, tool, label)
	if err != nil {
		return Install{}, err
	}

	archive, err := tool.ArchiveName(version)
	if err != nil {
		return Install{}, err
	}

	if uerr := checkUnpinnedChecksum(tool, label, version, archive); uerr != nil {
		return Install{}, uerr
	}
	want, err := f.checksum(ctx, tool, version, archive)
	if err != nil {
		return Install{}, err
	}

	dest := filepath.Join(f.CacheDir, strings.TrimSuffix(archive, ".tar.gz"))
	binary := filepath.Join(dest, filepath.FromSlash(tool.binary))
	inst := Install{
		Tool:           tool.Name,
		Label:          label,
		Version:        version,
		ChecksumSource: want.source,
		Dir:            filepath.Dir(binary),
		binary:         binary,
	}

	key := checkKey{archive: archive, sum: want.sum}
	if f.cacheChecked(key) {
		return inst, nil
	}

	// Many Fetchers and processes can share one cache directory. Without
	// this lock, one call could see the directory of another call before its
	// archive is published, refuse it and remove a CLI that is in use.
	unlock, err := f.lockEntry(archive)
	if err != nil {
		return Install{}, err
	}

	defer unlock()

	// Another call of this Fetcher can have checked the entry while this call
	// waited for the lock. Do not check it again.
	if f.cacheChecked(key) {
		return inst, nil
	}

	rel := release{
		url:     tool.releaseFile(version, archive),
		archive: archive,
		sum:     want.sum,
		dest:    dest,
		binary:  filepath.FromSlash(tool.binary),
	}

	// The cache comes from CI and is untrusted: an entry is used only when
	// its archive and extracted files match the expected sum and the archive.
	if f.checkCache(archive, rel.binary, want.sum) == nil {
		f.markCacheChecked(key)

		return inst, nil
	}

	if err := removeCacheEntry(dest, filepath.Join(f.CacheDir, archive)); err != nil {
		return Install{}, err
	}

	if err := f.download(ctx, rel); err != nil {
		return Install{}, err
	}

	f.markCacheChecked(key)

	return inst, nil
}

// lockEntry runs the beforeLock function of f, if it is set, and then locks
// the entry of archive in the cache directory of f.
func (f *Fetcher) lockEntry(archive string) (func(), error) {
	if f.beforeLock != nil {
		f.beforeLock()
	}

	return lockEntry(f.CacheDir, archive)
}

// checkCache counts the check and checks the entry of archive in the cache
// directory of f.
func (f *Fetcher) checkCache(archive, wantBinary, wantSum string) error {
	f.checks.Add(1)

	return checkCache(f.CacheDir, archive, wantBinary, wantSum)
}

// lockEntry takes an exclusive lock on the lock file of archive in cacheDir
// and returns the unlock function. The lock holds across Fetchers and
// processes, because each call opens the file again.
func lockEntry(cacheDir, archive string) (func(), error) {
	if err := os.MkdirAll(cacheDir, dirMode); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}

	cacheRoot, err := os.OpenRoot(cacheDir)
	if err != nil {
		return nil, fmt.Errorf("open cache dir: %w", err)
	}

	defer ignoreErr(cacheRoot.Close)

	lockFile, err := cacheRoot.OpenFile(archive+".lock", os.O_CREATE|os.O_RDWR, lockMode)
	if err != nil {
		return nil, fmt.Errorf("open cache lock: %w", err)
	}

	if err := flock(lockFile, syscall.LOCK_EX); err != nil {
		return nil, errors.Join(fmt.Errorf("lock cache entry: %w", err), lockFile.Close())
	}

	return func() {
		ignoreErr(func() error { return flock(lockFile, syscall.LOCK_UN) })
		ignoreErr(lockFile.Close)
	}, nil
}

// flock applies how to the open file f.
func flock(f *os.File, how int) error {
	conn, err := f.SyscallConn()
	if err != nil {
		return fmt.Errorf("lock file: %w", err)
	}

	var lockErr error

	controlErr := conn.Control(func(fd uintptr) {
		lockErr = syscall.Flock(int(fd), how)
	})
	if controlErr != nil {
		return fmt.Errorf("lock file: %w", controlErr)
	}

	if lockErr != nil {
		return fmt.Errorf("lock file: %w", lockErr)
	}

	return nil
}

func (f *Fetcher) cacheChecked(key checkKey) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.checked[key]
}

func (f *Fetcher) markCacheChecked(key checkKey) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.checked == nil {
		f.checked = make(map[checkKey]bool)
	}

	f.checked[key] = true
}

// version returns the release that label names: the latest release for
// Latest, else the label without a leading "v". It refuses a version that is
// not a plain "x.y.z" release.
func (f *Fetcher) version(ctx context.Context, tool Tool, label string) (string, error) {
	version := strings.TrimPrefix(label, "v")

	if label == Latest {
		if err := checkUnpinnedFetch(tool, label, tool.LatestURL); err != nil {
			return "", err
		}

		resolved, err := f.latest(ctx, tool)
		if err != nil {
			return "", err
		}

		version = resolved
	}

	// Defense in depth: os.Root contains each cache path too. This check does
	// not rely on it: the version also goes into the download URL, and a
	// label that is not a plain release must not name a path at all.
	if semver(version) == nil {
		return "", fmt.Errorf("install %s: %w: %q", tool.Name, ErrBadVersion, label)
	}

	return version, nil
}

// checkUnpinnedFetch returns ErrUnpinnedFetch when a request to rawURL would
// resolve the newest version or a published checksum while the matrix of tool is
// the default. Set the tool's versions variable to allow it. A loopback host is
// allowed, so a test can serve a fake release. A URL that does not parse is not
// loopback.
func checkUnpinnedFetch(tool Tool, label, rawURL string) error {
	if os.Getenv(tool.versionsEnv) != "" {
		return nil
	}

	parsed, err := neturl.Parse(rawURL)
	if err == nil {
		host := parsed.Hostname()
		ip := net.ParseIP(host)

		if host == localhostName || (ip != nil && ip.IsLoopback()) {
			return nil
		}
	}

	return fmt.Errorf("%w: %s %q: set %s to allow it", ErrUnpinnedFetch, tool.Name, label, tool.versionsEnv)
}

// checkUnpinnedChecksum returns ErrUnpinnedFetch when archive has no pinned
// checksum and its published checksum file needs a request that
// checkUnpinnedFetch refuses.
func checkUnpinnedChecksum(tool Tool, label, version, archive string) error {
	if _, ok := tool.pinned[archive]; ok {
		return nil
	}

	return checkUnpinnedFetch(tool, label, tool.releaseFile(version, tool.checksums(version)))
}

func (f *Fetcher) latest(ctx context.Context, tool Tool) (string, error) {
	body, err := getText(ctx, f.Client, tool.LatestURL)
	if err != nil {
		return "", fmt.Errorf("resolve latest %s version: %w", tool.Name, err)
	}

	version, err := tool.parseLatest(body)
	if err != nil {
		return "", fmt.Errorf("resolve latest %s version: %w", tool.Name, err)
	}

	version = strings.TrimPrefix(version, "v")
	if semver(version) == nil {
		return "", fmt.Errorf("resolve latest %s version: %w: %q", tool.Name, ErrBadVersion, version)
	}

	return version, nil
}

// expectedSum is the sha256 an archive must have, and where it came from.
type expectedSum struct {
	sum    string
	source string
}

// checksum returns the expected sha256 of archive: pinned in this repository
// if known, otherwise from the checksum file published with the release.
func (f *Fetcher) checksum(ctx context.Context, tool Tool, version, archive string) (expectedSum, error) {
	if sum, ok := tool.pinned[archive]; ok {
		return expectedSum{sum: sum, source: "pinned"}, nil
	}

	listing, err := getText(ctx, f.Client, tool.releaseFile(version, tool.checksums(version)))
	if err != nil {
		return expectedSum{}, fmt.Errorf("fetch published checksums: %w", err)
	}

	sum, ok := parseChecksums(listing)[archive]
	if !ok {
		return expectedSum{}, fmt.Errorf("%w: %s", ErrNoChecksum, archive)
	}

	return expectedSum{sum: sum, source: "published"}, nil
}

// release is one archive to download and where to put it.
type release struct {
	url     string
	archive string
	sum     string
	dest    string
	// binary is the executable's path inside the archive, used to check extraction.
	binary string
}

// download fetches rel.url, checks it against rel.sum and unpacks it into
// rel.dest. Nothing appears at rel.dest unless the checksum matched and
// extraction finished.
func (f *Fetcher) download(ctx context.Context, rel release) error {
	if err := os.MkdirAll(f.CacheDir, dirMode); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}

	tmp, err := os.CreateTemp(f.CacheDir, "download-*.tar.gz")
	if err != nil {
		return fmt.Errorf("create download file: %w", err)
	}

	defer ignoreErr(func() error { return os.Remove(tmp.Name()) })
	defer ignoreErr(tmp.Close)

	gotSum, err := f.fetchWithRetry(ctx, rel.url, tmp)
	if err != nil {
		return err
	}

	if gotSum != rel.sum {
		return fmt.Errorf("%w: %s: want %s, got %s", ErrChecksumMismatch, rel.archive, rel.sum, gotSum)
	}

	if _, seekErr := tmp.Seek(0, io.SeekStart); seekErr != nil {
		return fmt.Errorf("rewind download: %w", seekErr)
	}

	stage, err := os.MkdirTemp(f.CacheDir, "stage-*")
	if err != nil {
		return fmt.Errorf("create staging dir: %w", err)
	}

	defer ignoreErr(func() error { return os.RemoveAll(stage) })

	if extractErr := extract(tmp, stage); extractErr != nil {
		return extractErr
	}

	if _, statErr := os.Stat(filepath.Join(stage, rel.binary)); statErr != nil {
		return fmt.Errorf("%w: %s", ErrMissingBinary, rel.archive)
	}

	if err := os.Rename(stage, rel.dest); err != nil {
		// Another process finished first; keep this case only when its entry
		// passes the same check.
		if checkCache(f.CacheDir, rel.archive, rel.binary, rel.sum) == nil {
			return nil
		}

		return fmt.Errorf("publish %s: %w", rel.dest, err)
	}

	// Publish the verified archive next to the extracted directory. A later
	// run can then check the extracted files against the archive.
	if err := os.Rename(tmp.Name(), filepath.Join(f.CacheDir, rel.archive)); err != nil {
		return fmt.Errorf("publish archive %s: %w", rel.archive, err)
	}

	return nil
}

// checkCache reports whether the cache entry for archive in cacheDir holds
// exactly the contents of that archive, and whether the archive has wantSum.
// The caller refuses the entry on any error.
func checkCache(cacheDir, archive, wantBinary, wantSum string) error {
	cacheRoot, err := os.OpenRoot(cacheDir)
	if err != nil {
		return fmt.Errorf("open cache dir: %w", err)
	}

	defer ignoreErr(cacheRoot.Close)

	// Defense in depth: the archive name comes from the tool, not from the
	// cache; the root keeps the open inside cacheDir.
	archiveFile, err := cacheRoot.Open(archive)
	if err != nil {
		return fmt.Errorf("open cached archive: %w", err)
	}

	defer ignoreErr(archiveFile.Close)

	if sizeErr := checkArchiveSize(archiveFile); sizeErr != nil {
		return sizeErr
	}

	dest := strings.TrimSuffix(archive, ".tar.gz")

	root, err := cacheRoot.OpenRoot(dest)
	if err != nil {
		return fmt.Errorf("open cached CLI: %w", err)
	}

	defer ignoreErr(root.Close)

	// Defense in depth: download checks the sum before it publishes the
	// archive; this check does not rely on that or on the cache state.
	gotSum, err := hashSum(archiveFile)
	if err != nil {
		return fmt.Errorf("read cached archive: %w", err)
	}

	if gotSum != wantSum {
		return fmt.Errorf("%w: %s: want %s, got %s", ErrChecksumMismatch, archive, wantSum, gotSum)
	}

	if _, seekErr := archiveFile.Seek(0, io.SeekStart); seekErr != nil {
		return fmt.Errorf("rewind cached archive: %w", seekErr)
	}

	want, err := archiveContents(archiveFile)
	if err != nil {
		return err
	}

	if dirErr := checkCacheDir(root, want); dirErr != nil {
		return dirErr
	}

	// Defense in depth: the directory check already covers the binary path;
	// this check does not rely on it, so a missing binary is still refused.
	if _, err := root.Lstat(wantBinary); err != nil {
		return fmt.Errorf("inspect cached CLI binary: %w", err)
	}

	return nil
}

// checkArchiveSize refuses a cached archive above the download limit.
// Defense in depth: download caps the archive size; this check does not rely
// on that and refuses a larger cached archive before it reads it.
func checkArchiveSize(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("inspect cached archive: %w", err)
	}

	if info.Size() > maxArchiveBytes {
		return fmt.Errorf("%w: cached archive is larger than %d bytes", ErrCacheMismatch, maxArchiveBytes)
	}

	return nil
}

// archiveEntry is the type, size and content hash of one entry that the
// extracted directory must hold.
type archiveEntry struct {
	kind byte
	size int64
	sum  string
}

// archiveContents returns the entries of the gzipped tarball in src, keyed by
// their cleaned names.
func archiveContents(src io.Reader) (map[string]archiveEntry, error) {
	entries := make(map[string]archiveEntry)

	err := walkArchive(src, func(hdr *tar.Header, body io.Reader) error {
		name := entryName(hdr)
		if name == "." {
			return nil
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			addParentDirs(entries, name)
			entries[name] = archiveEntry{kind: tar.TypeDir, size: 0, sum: ""}
		case tar.TypeReg:
			addParentDirs(entries, name)

			hash := sha256.New()
			if _, err := io.Copy(hash, body); err != nil {
				return fmt.Errorf("read archive entry %q: %w", name, err)
			}

			entries[name] = archiveEntry{
				kind: tar.TypeReg,
				size: hdr.Size,
				sum:  hex.EncodeToString(hash.Sum(nil)),
			}
		default:
			// Extraction refuses every other type, so a cache entry that holds
			// one did not come from extraction.
			return fmt.Errorf("%w: %q has type %q", ErrUnsafeArchive, hdr.Name, hdr.Typeflag)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return entries, nil
}

// addParentDirs adds the directories that extraction creates for name.
func addParentDirs(entries map[string]archiveEntry, name string) {
	for dir := path.Dir(name); dir != "." && dir != "/"; dir = path.Dir(dir) {
		if _, ok := entries[dir]; !ok {
			entries[dir] = archiveEntry{kind: tar.TypeDir, size: 0, sum: ""}
		}
	}
}

// checkCacheDir reports whether root holds exactly the entries that the
// archive names, and no other file.
func checkCacheDir(root *os.Root, want map[string]archiveEntry) error {
	seen := make(map[string]bool, len(want))

	err := fs.WalkDir(root.FS(), ".", func(name string, _ fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if name == "." {
			return nil
		}

		entry, ok := want[name]
		if !ok {
			// A path that the archive does not name is refused.
			return fmt.Errorf("%w: cached path %q is not in the archive", ErrCacheMismatch, name)
		}

		seen[name] = true

		return checkCacheEntry(root, name, entry)
	})
	if err != nil {
		return fmt.Errorf("walk cached CLI: %w", err)
	}

	for name := range want {
		if !seen[name] {
			// Every path of the archive must exist in the cache.
			return fmt.Errorf("%w: archive path %q is missing from the cache", ErrCacheMismatch, name)
		}
	}

	return nil
}

func checkCacheEntry(root *os.Root, name string, want archiveEntry) error {
	info, err := root.Lstat(name)
	if err != nil {
		return fmt.Errorf("inspect cached %q: %w", name, err)
	}

	switch want.kind {
	case tar.TypeDir:
		return checkDir(name, info)
	case tar.TypeReg:
		return checkFile(root, name, info, want)
	}

	return fmt.Errorf("%w: cached %q has an unknown type", ErrCacheMismatch, name)
}

func checkDir(name string, info fs.FileInfo) error {
	if !info.IsDir() {
		return fmt.Errorf("%w: cached %q is not a directory", ErrCacheMismatch, name)
	}

	return nil
}

func checkFile(root *os.Root, name string, info fs.FileInfo, want archiveEntry) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: cached %q is not a regular file", ErrCacheMismatch, name)
	}

	// The full mode, not only the permission bits: a setuid, setgid or
	// sticky bit refuses the entry too.
	if info.Mode() != fileMode {
		return fmt.Errorf("%w: cached %q has mode %v, want %v",
			ErrCacheMismatch, name, info.Mode(), fs.FileMode(fileMode))
	}

	if info.Size() != want.size {
		// Defense in depth: the hash below also covers a change in size; this
		// check does not rely on it and refuses a large file before it reads it.
		return fmt.Errorf("%w: cached %q has size %d, want %d", ErrCacheMismatch, name, info.Size(), want.size)
	}

	got, err := fileSum(root, name)
	if err != nil {
		return err
	}

	if got != want.sum {
		return fmt.Errorf("%w: cached %q does not match the archive", ErrCacheMismatch, name)
	}

	return nil
}

func fileSum(root *os.Root, name string) (string, error) {
	f, err := root.Open(name)
	if err != nil {
		return "", fmt.Errorf("open cached %q: %w", name, err)
	}

	defer ignoreErr(f.Close)

	sum, err := hashSum(f)
	if err != nil {
		return "", fmt.Errorf("read cached %q: %w", name, err)
	}

	return sum, nil
}

func hashSum(r io.Reader) (string, error) {
	hash := sha256.New()
	if _, err := io.Copy(hash, r); err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// removeCacheEntry removes a refused entry: the extracted directory and its
// archive file.
func removeCacheEntry(dest, archivePath string) error {
	if err := os.RemoveAll(dest); err != nil {
		return fmt.Errorf("remove refused cache directory: %w", err)
	}

	if err := os.RemoveAll(archivePath); err != nil {
		return fmt.Errorf("remove refused cache archive: %w", err)
	}

	return nil
}

// fetchWithRetry calls fetchTo up to downloadAttempts times. It tries again
// only after a network error or a 5xx answer, and empties dst before each
// new attempt. A checksum mismatch is not retried: download checks the sum
// after this returns.
func (f *Fetcher) fetchWithRetry(ctx context.Context, url string, dst *os.File) (string, error) {
	for attempt := 1; ; attempt++ {
		if err := dst.Truncate(0); err != nil {
			return "", fmt.Errorf("empty download file: %w", err)
		}

		if _, err := dst.Seek(0, io.SeekStart); err != nil {
			return "", fmt.Errorf("rewind download file: %w", err)
		}

		sum, retry, err := f.fetchTo(ctx, url, dst)
		if err == nil {
			return sum, nil
		}

		if !retry {
			return "", err
		}

		if attempt == downloadAttempts {
			return "", fmt.Errorf("%w (attempt %d of %d)", err, attempt, downloadAttempts)
		}

		timer := time.NewTimer(time.Duration(attempt) * downloadRetryWait)

		select {
		case <-ctx.Done():
			timer.Stop()

			return "", fmt.Errorf("%w (attempt %d of %d): %w", err, attempt, downloadAttempts, ctx.Err())
		case <-timer.C:
		}
	}
}

// fetchTo streams url into dst and returns the sha256 of what was written.
// The bool is true when the error may not happen again: a network error or
// a 5xx answer.
func (f *Fetcher) fetchTo(ctx context.Context, url string, dst io.Writer) (string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return "", false, fmt.Errorf("build request for %s: %w", url, err)
	}

	resp, err := f.Client.Do(req)
	if err != nil {
		return "", true, fmt.Errorf("get %s: %w", url, err)
	}

	defer func() { ignoreErr(resp.Body.Close) }()

	if resp.StatusCode != http.StatusOK {
		return "", resp.StatusCode >= http.StatusInternalServerError,
			fmt.Errorf("get %s: %w: %d", url, ErrUnexpectedStatus, resp.StatusCode)
	}

	hash := sha256.New()

	if _, err := io.Copy(io.MultiWriter(dst, hash), io.LimitReader(resp.Body, maxArchiveBytes)); err != nil {
		return "", true, fmt.Errorf("download %s: %w", url, err)
	}

	return hex.EncodeToString(hash.Sum(nil)), false, nil
}

// extract unpacks a gzipped tarball into dir. Only directories and regular
// files are accepted, and every path is confined to dir by os.Root.
func extract(src io.Reader, dir string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("open extraction root: %w", err)
	}

	defer ignoreErr(root.Close)

	var total int64

	return walkArchive(src, func(hdr *tar.Header, body io.Reader) error {
		if hdr.Typeflag == tar.TypeReg {
			total += hdr.Size
			if total > maxExtractBytes {
				return fmt.Errorf("%w: archive expands beyond %d bytes", ErrUnsafeArchive, maxExtractBytes)
			}
		}

		return extractEntry(root, hdr, body)
	})
}

// walkArchive reads the gzipped tarball in src and calls visit for each entry.
// Extraction and the cache check share this one reader, so they apply the
// same name rules.
func walkArchive(src io.Reader, visit func(*tar.Header, io.Reader) error) error {
	gz, err := gzip.NewReader(src)
	if err != nil {
		return fmt.Errorf("open gzip: %w", err)
	}

	defer ignoreErr(gz.Close)

	tr := tar.NewReader(gz)

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}

		if err := visit(hdr, tr); err != nil {
			return err
		}
	}
}

// entryName returns the cleaned name that extraction and the cache check use.
func entryName(hdr *tar.Header) string {
	return path.Clean(hdr.Name)
}

func extractEntry(root *os.Root, hdr *tar.Header, body io.Reader) error {
	name := entryName(hdr)

	switch hdr.Typeflag {
	case tar.TypeDir:
		if err := root.MkdirAll(name, dirMode); err != nil {
			return fmt.Errorf("%w: %q: %w", ErrUnsafeArchive, hdr.Name, err)
		}

		return nil
	case tar.TypeReg:
		return writeFile(root, name, body, hdr.Size)
	default:
		return fmt.Errorf("%w: %q has type %q", ErrUnsafeArchive, hdr.Name, hdr.Typeflag)
	}
}

func writeFile(root *os.Root, name string, src io.Reader, size int64) error {
	if err := root.MkdirAll(path.Dir(name), dirMode); err != nil {
		return fmt.Errorf("%w: %q: %w", ErrUnsafeArchive, name, err)
	}

	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fileMode)
	if err != nil {
		return fmt.Errorf("%w: %q: %w", ErrUnsafeArchive, name, err)
	}

	if _, err := io.CopyN(f, src, size); err != nil {
		return errors.Join(fmt.Errorf("write %q: %w", name, err), f.Close())
	}

	// The umask can remove bits from the mode of OpenFile. Set the mode
	// exactly, so that the cache check can compare it.
	if err := f.Chmod(fileMode); err != nil {
		return errors.Join(fmt.Errorf("set the mode of %q: %w", name, err), f.Close())
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("close %q: %w", name, err)
	}

	return nil
}
