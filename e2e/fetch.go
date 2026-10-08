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
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	maxArchiveBytes = 1 << 30
	maxExtractBytes = 4 << 30
	dirMode         = 0o750
	fileMode        = 0o750
)

// Errors returned while downloading and unpacking a CLI.
var (
	ErrChecksumMismatch = errors.New("archive checksum mismatch")
	ErrUnsafeArchive    = errors.New("unsafe archive entry")
	ErrMissingBinary    = errors.New("archive has no pulumi binary")
)

// Fetcher downloads Pulumi CLI releases into a cache directory.
type Fetcher struct {
	// Client performs every request.
	Client *http.Client
	// CacheDir holds one extracted directory per version and platform.
	CacheDir string
	// DownloadBase is the URL prefix of release archives and checksum files.
	DownloadBase string
	// LatestURL answers with the newest released version.
	LatestURL string
}

// Install is a CLI ready to run.
type Install struct {
	// Label is what the matrix asked for, for example "latest".
	Label string
	// Version is the resolved release, for example "3.268.0".
	Version string
	// ChecksumSource says where the verifying checksum came from: "pinned" or "published".
	ChecksumSource string
	// Dir is the directory holding pulumi and its language plugins.
	Dir string
}

// Binary returns the path of the pulumi executable.
func (i Install) Binary() string {
	return filepath.Join(i.Dir, "pulumi")
}

// Install resolves label, downloads and verifies the release if it is not
// cached, and returns it. Concurrent calls for different versions are safe.
func (f *Fetcher) Install(ctx context.Context, label string) (Install, error) {
	version := strings.TrimPrefix(label, "v")

	if label == Latest {
		text, err := getText(ctx, f.Client, f.LatestURL)
		if err != nil {
			return Install{}, fmt.Errorf("resolve latest version: %w", err)
		}

		version = strings.TrimPrefix(strings.TrimSpace(text), "v")
		if version == "" || strings.ContainsAny(version, "/\\ \n") {
			return Install{}, fmt.Errorf("resolve latest version: %w: %q", ErrBadVersion, text)
		}
	}

	archive, err := ArchiveName(version)
	if err != nil {
		return Install{}, err
	}

	want, err := f.checksum(ctx, version, archive)
	if err != nil {
		return Install{}, err
	}

	inst := Install{
		Label:          label,
		Version:        version,
		ChecksumSource: want.source,
		Dir:            filepath.Join(f.CacheDir, strings.TrimSuffix(archive, ".tar.gz"), "pulumi"),
	}

	if _, err := os.Stat(inst.Binary()); err == nil {
		return inst, nil
	}

	if err := f.download(ctx, archive, want.sum, filepath.Dir(inst.Dir)); err != nil {
		return Install{}, err
	}

	return inst, nil
}

// expectedSum is the sha256 an archive must have, and where it came from.
type expectedSum struct {
	sum    string
	source string
}

// checksum returns the expected sha256 of archive.
func (f *Fetcher) checksum(ctx context.Context, version, archive string) (expectedSum, error) {
	if sum, ok := pinnedChecksums()[archive]; ok {
		return expectedSum{sum: sum, source: "pinned"}, nil
	}

	listing, err := getText(ctx, f.Client, fmt.Sprintf("%s/pulumi-%s-checksums.txt", f.DownloadBase, version))
	if err != nil {
		return expectedSum{}, fmt.Errorf("fetch published checksums: %w", err)
	}

	sum, ok := parseChecksums(listing)[archive]
	if !ok {
		return expectedSum{}, fmt.Errorf("%w: %s", ErrNoChecksum, archive)
	}

	return expectedSum{sum: sum, source: "published"}, nil
}

// download fetches archive, checks it against wantSum and unpacks it into dest.
// Nothing appears at dest unless the checksum matched and extraction finished.
func (f *Fetcher) download(ctx context.Context, archive, wantSum, dest string) error {
	if err := os.MkdirAll(f.CacheDir, dirMode); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}

	tmp, err := os.CreateTemp(f.CacheDir, "download-*.tar.gz")
	if err != nil {
		return fmt.Errorf("create download file: %w", err)
	}

	defer ignoreErr(func() error { return os.Remove(tmp.Name()) })
	defer ignoreErr(tmp.Close)

	gotSum, err := f.fetchTo(ctx, f.DownloadBase+"/"+archive, tmp)
	if err != nil {
		return err
	}

	if gotSum != wantSum {
		return fmt.Errorf("%w: %s: want %s, got %s", ErrChecksumMismatch, archive, wantSum, gotSum)
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

	if _, statErr := os.Stat(filepath.Join(stage, "pulumi", "pulumi")); statErr != nil {
		return fmt.Errorf("%w: %s", ErrMissingBinary, archive)
	}

	if err := os.Rename(stage, dest); err != nil {
		// Another process finished first; its copy was verified the same way.
		if _, statErr := os.Stat(filepath.Join(dest, "pulumi", "pulumi")); statErr == nil {
			return nil
		}

		return fmt.Errorf("publish %s: %w", dest, err)
	}

	return nil
}

// fetchTo streams url into dst and returns the sha256 of what was written.
func (f *Fetcher) fetchTo(ctx context.Context, url string, dst io.Writer) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("build request for %s: %w", url, err)
	}

	resp, err := f.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("get %s: %w", url, err)
	}

	defer func() { ignoreErr(resp.Body.Close) }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("get %s: %w: %d", url, ErrUnexpectedStatus, resp.StatusCode)
	}

	hash := sha256.New()

	if _, err := io.Copy(io.MultiWriter(dst, hash), io.LimitReader(resp.Body, maxArchiveBytes)); err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
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
