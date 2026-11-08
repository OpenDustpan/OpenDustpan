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
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxArchiveBytes = 1 << 30
	maxExtractBytes = 4 << 30
	dirMode         = 0o750
	fileMode        = 0o750
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
)

// Fetcher downloads CLI releases into a cache directory.
type Fetcher struct {
	// Client performs every request.
	Client *http.Client
	// CacheDir holds one extracted directory per tool, version and platform.
	CacheDir string
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
	version := strings.TrimPrefix(label, "v")

	if label == Latest {
		if err := checkUnpinnedFetch(tool, label, tool.LatestURL); err != nil {
			return Install{}, err
		}

		resolved, err := f.latest(ctx, tool)
		if err != nil {
			return Install{}, err
		}

		version = resolved
	}

	archive, err := tool.ArchiveName(version)
	if err != nil {
		return Install{}, err
	}

	if _, ok := tool.pinned[archive]; !ok {
		checksumURL := tool.releaseFile(version, tool.checksums(version))

		unpinnedErr := checkUnpinnedFetch(tool, label, checksumURL)
		if unpinnedErr != nil {
			return Install{}, unpinnedErr
		}
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

	if _, err := os.Stat(binary); err == nil {
		return inst, nil
	}

	rel := release{
		url:     tool.releaseFile(version, archive),
		archive: archive,
		sum:     want.sum,
		dest:    dest,
		binary:  filepath.FromSlash(tool.binary),
	}

	if err := f.download(ctx, rel); err != nil {
		return Install{}, err
	}

	return inst, nil
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
		// Another process finished first; its copy was verified the same way.
		if _, statErr := os.Stat(filepath.Join(rel.dest, rel.binary)); statErr == nil {
			return nil
		}

		return fmt.Errorf("publish %s: %w", rel.dest, err)
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
	gz, err := gzip.NewReader(src)
	if err != nil {
		return fmt.Errorf("open gzip: %w", err)
	}

	defer ignoreErr(gz.Close)

	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("open extraction root: %w", err)
	}

	defer ignoreErr(root.Close)

	var total int64

	tr := tar.NewReader(gz)

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}

		if hdr.Typeflag == tar.TypeReg {
			total += hdr.Size
			if total > maxExtractBytes {
				return fmt.Errorf("%w: archive expands beyond %d bytes", ErrUnsafeArchive, maxExtractBytes)
			}
		}

		if err := extractEntry(root, hdr, tr); err != nil {
			return err
		}
	}
}

func extractEntry(root *os.Root, hdr *tar.Header, body io.Reader) error {
	name := path.Clean(hdr.Name)

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

	if err := f.Close(); err != nil {
		return fmt.Errorf("close %q: %w", name, err)
	}

	return nil
}
