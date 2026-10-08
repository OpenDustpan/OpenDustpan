//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
)

// Latest is the version label that resolves to the newest published CLI.
const Latest = "latest"

const (
	// DownloadBase hosts release archives and their published checksum files.
	DownloadBase = "https://get.pulumi.com/releases/sdk"
	// LatestVersionURL answers with the newest released version as plain text.
	LatestVersionURL = "https://www.pulumi.com/latest-version"

	versionsEnv    = "E2E_PULUMI_VERSIONS"
	maxSmallBody   = 1 << 20
	checksumFields = 2
)

// Errors returned while choosing and verifying CLI versions.
var (
	ErrUnsupportedPlatform = errors.New("no Pulumi CLI archive for this platform")
	ErrNoChecksum          = errors.New("no checksum published for archive")
	ErrBadVersion          = errors.New("unexpected version response")
	ErrUnexpectedStatus    = errors.New("unexpected HTTP status")
)

// DefaultVersions is the CLI matrix: the newest release plus two older minors.
//
// Pulumi ships a minor roughly weekly, so the older entries are spaced out
// (about four months and one year back) to cover genuinely different CLI
// behavior rather than three near-identical builds.
func DefaultVersions() []string {
	return []string{Latest, "3.250.0", "3.200.0"}
}

// Versions returns the matrix from E2E_PULUMI_VERSIONS, or DefaultVersions.
func Versions() []string {
	raw := os.Getenv(versionsEnv)
	if raw == "" {
		return DefaultVersions()
	}

	var versions []string

	for part := range strings.SplitSeq(raw, ",") {
		if v := strings.TrimSpace(part); v != "" {
			versions = append(versions, v)
		}
	}

	return versions
}

// pinnedChecksums returns sha256 sums for older releases, copied from
// https://get.pulumi.com/releases/sdk/pulumi-<version>-checksums.txt and keyed
// by archive name. Pinning them in the repository means a tampered download
// host cannot also tamper with the checksum. Versions that are not pinned
// (including "latest") are verified against the published checksum file.
func pinnedChecksums() map[string]string {
	return map[string]string{
		"pulumi-v3.250.0-darwin-arm64.tar.gz": "dbd289ce3069cf567683252082e226aef533775f546df7286f64f3f36f5fda28",
		"pulumi-v3.250.0-darwin-x64.tar.gz":   "8aa1615ed737022949d4050bf183ab37bce1f9cecb88877bc6636db82604e348",
		"pulumi-v3.250.0-linux-arm64.tar.gz":  "deb4bf0d05b4cbaf3adcf82ffa44039ad0ebf039e33338ead8136877a437b4e5",
		"pulumi-v3.250.0-linux-x64.tar.gz":    "8916fd36ccc96adbd45ca679194b685e3b767d94502ae05610878a6d9870b8f3",
		"pulumi-v3.200.0-darwin-arm64.tar.gz": "07d3e22b6a75b38be80acc39df5e2710899da437803e4290dd7c00d2bd9256ec",
		"pulumi-v3.200.0-darwin-x64.tar.gz":   "37637baca916c2a76ae5fef80bad868c140d781c0257d615efb4b22c3329821b",
		"pulumi-v3.200.0-linux-arm64.tar.gz":  "127e30ee5b34b32d616db1a3254ff2520e639cfb3b0f19dfc62b52f743465c2c",
		"pulumi-v3.200.0-linux-x64.tar.gz":    "b48a7f6034c6ef554c25e1762b9e89d451d2ebd4f3671850a56fe395956fecbe",
	}
}

// platform returns the suffix Pulumi uses in archive names for this machine.
func platform() (string, error) {
	var goos, arch string

	switch runtime.GOOS {
	case "linux", "darwin":
		goos = runtime.GOOS
	default:
		return "", fmt.Errorf("%w: %s/%s", ErrUnsupportedPlatform, runtime.GOOS, runtime.GOARCH)
	}

	switch runtime.GOARCH {
	case "amd64":
		arch = "x64"
	case "arm64":
		arch = "arm64"
	default:
		return "", fmt.Errorf("%w: %s/%s", ErrUnsupportedPlatform, runtime.GOOS, runtime.GOARCH)
	}

	return goos + "-" + arch, nil
}

// ArchiveName returns the release archive for a version such as "3.250.0".
func ArchiveName(version string) (string, error) {
	plat, err := platform()
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("pulumi-v%s-%s.tar.gz", version, plat), nil
}

// getText fetches a small text resource.
func getText(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("build request for %s: %w", url, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("get %s: %w", url, err)
	}

	defer func() { ignoreErr(resp.Body.Close) }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("get %s: %w: %d", url, ErrUnexpectedStatus, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSmallBody))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", url, err)
	}

	return string(body), nil
}

// parseChecksums reads a "<sha256>  <file>" listing into a map keyed by file.
func parseChecksums(listing string) map[string]string {
	sums := make(map[string]string)

	for line := range strings.SplitSeq(listing, "\n") {
		fields := strings.Fields(line)
		if len(fields) == checksumFields {
			sums[fields[1]] = fields[0]
		}
	}

	return sums
}

// ignoreErr runs fn and discards its error. It is for releasing read-only
// handles and scratch files whose contents were already consumed or verified,
// where a failure to release changes nothing the caller could act on.
func ignoreErr(fn func() error) {
	_ = fn() //nolint:errcheck // Documented above: nothing useful can be done with the error.
}
