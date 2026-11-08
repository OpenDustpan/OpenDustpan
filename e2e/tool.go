//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

// Latest is the version label that resolves to the newest published release.
const Latest = "latest"

// Tool names, also used as report prefixes.
const (
	ToolPulumi = "pulumi"
	ToolTofu   = "tofu"
)

const (
	maxSmallBody   = 1 << 20
	checksumFields = 2
	semverParts    = 3
)

// Errors returned while choosing and verifying CLI versions.
var (
	ErrUnsupportedPlatform = errors.New("no CLI archive for this platform")
	ErrNoChecksum          = errors.New("no checksum published for archive")
	ErrBadVersion          = errors.New("unexpected version response")
	ErrUnexpectedStatus    = errors.New("unexpected HTTP status")
)

// Tool describes where one CLI is released and how its archives are verified.
type Tool struct {
	parseLatest func(body string) (string, error)
	archive     func(version, goos, arch string) string
	checksums   func(version string) string
	pinned      map[string]string
	// Name is ToolPulumi or ToolTofu.
	Name string
	// LatestURL answers with the newest release; parseLatest reads it.
	LatestURL string
	// ReleaseURL is a format string for release files: %[1]s is the version
	// and %[2]s the file name. Tests point it at a local server.
	ReleaseURL  string
	versionsEnv string
	binary      string
	defaults    []string
}

// Pulumi describes the Pulumi CLI.
//
// The default matrix is the three pinned versions, each from a different older
// minor. Pulumi ships a minor roughly weekly, so the pins are spaced out (about
// two months, four months and one year back) to cover genuinely different CLI
// behavior rather than three near-identical builds. The scheduled nightly run
// sets E2E_PULUMI_VERSIONS to latest to run the newest release.
func Pulumi() Tool {
	return Tool{
		parseLatest: func(body string) (string, error) { return strings.TrimSpace(body), nil },
		archive: func(version, goos, arch string) string {
			if arch == "amd64" {
				arch = "x64"
			}

			return fmt.Sprintf("pulumi-v%s-%s-%s.tar.gz", version, goos, arch)
		},
		checksums: func(version string) string { return "pulumi-" + version + "-checksums.txt" },
		// sha256 sums copied from https://get.pulumi.com/releases/sdk/pulumi-<version>-checksums.txt.
		pinned: map[string]string{
			// The journal tests (make e2e-journal) run this version.
			"pulumi-v3.268.0-darwin-arm64.tar.gz": "ef509a84f0ad8f4ed0ec04be957e6688f094eb9a1cdc676a4864103d66d8a4e9",
			"pulumi-v3.268.0-darwin-x64.tar.gz":   "b0c4e37e9fd9573d3e09b65606124486ef2c2049db1237ca6037c4204c6a99a8",
			"pulumi-v3.268.0-linux-arm64.tar.gz":  "47c1b9283b7997337b0a373d240f771a6aeb42ee5dd2e213c14604247c7ff15a",
			"pulumi-v3.268.0-linux-x64.tar.gz":    "903cafeaa9f6327e4bf53a8b7b99ae660f26198acfa804a2f982bf5de3f1de0b",
			"pulumi-v3.262.0-darwin-arm64.tar.gz": "a3c2ff50ef94962711649e234a28ed3803618cbb2f313505a88ca86c30da98e0",
			"pulumi-v3.262.0-darwin-x64.tar.gz":   "7db45a6fb6f876e7181925861613b1aa2ae4eea7629f2fb495b221c67c0ec117",
			"pulumi-v3.262.0-linux-arm64.tar.gz":  "224ffde7339807abca1ee8866372e37fcf23f27a1e31056326188f81823b69a4",
			"pulumi-v3.262.0-linux-x64.tar.gz":    "0469448b9853f723340bf19273c817482bca90f5c02c4cac9fa18a530df6402a",
			"pulumi-v3.250.0-darwin-arm64.tar.gz": "dbd289ce3069cf567683252082e226aef533775f546df7286f64f3f36f5fda28",
			"pulumi-v3.250.0-darwin-x64.tar.gz":   "8aa1615ed737022949d4050bf183ab37bce1f9cecb88877bc6636db82604e348",
			"pulumi-v3.250.0-linux-arm64.tar.gz":  "deb4bf0d05b4cbaf3adcf82ffa44039ad0ebf039e33338ead8136877a437b4e5",
			"pulumi-v3.250.0-linux-x64.tar.gz":    "8916fd36ccc96adbd45ca679194b685e3b767d94502ae05610878a6d9870b8f3",
			"pulumi-v3.200.0-darwin-arm64.tar.gz": "07d3e22b6a75b38be80acc39df5e2710899da437803e4290dd7c00d2bd9256ec",
			"pulumi-v3.200.0-darwin-x64.tar.gz":   "37637baca916c2a76ae5fef80bad868c140d781c0257d615efb4b22c3329821b",
			"pulumi-v3.200.0-linux-arm64.tar.gz":  "127e30ee5b34b32d616db1a3254ff2520e639cfb3b0f19dfc62b52f743465c2c",
			"pulumi-v3.200.0-linux-x64.tar.gz":    "b48a7f6034c6ef554c25e1762b9e89d451d2ebd4f3671850a56fe395956fecbe",
		},
		Name:        ToolPulumi,
		LatestURL:   "https://www.pulumi.com/latest-version",
		ReleaseURL:  "https://get.pulumi.com/releases/sdk/%[2]s",
		versionsEnv: "E2E_PULUMI_VERSIONS",
		binary:      "pulumi/pulumi",
		defaults:    []string{"3.262.0", "3.250.0", "3.200.0"},
	}
}

// OpenTofu describes the OpenTofu CLI. The default matrix is the three pinned
// versions: the last patch of each of the three previous minors. The scheduled
// nightly run sets E2E_TOFU_VERSIONS to latest to run the newest release.
func OpenTofu() Tool {
	return Tool{
		parseLatest: newestStable,
		archive: func(version, goos, arch string) string {
			return fmt.Sprintf("tofu_%s_%s_%s.tar.gz", version, goos, arch)
		},
		checksums: func(version string) string { return "tofu_" + version + "_SHA256SUMS" },
		// sha256 sums copied from tofu_<version>_SHA256SUMS on each GitHub release.
		pinned: map[string]string{
			"tofu_1.12.12_darwin_arm64.tar.gz": "47f72f1662dfbf5c0f39b213f189be04df6a44f1c666f455ceb54390eb5dbb0a",
			"tofu_1.12.12_darwin_amd64.tar.gz": "b6bc144a5b42713bef7fa2c841edff41835bd4881dbd85d4770529f397c44edd",
			"tofu_1.12.12_linux_amd64.tar.gz":  "b2a2e0ebdebf410d07706e67fb1b6ffc27e049dd80600188d9acc9634035e3a7",
			"tofu_1.12.12_linux_arm64.tar.gz":  "6fff506545782de496d7cb29653359b9fca29b0b9979eb9e985fd8092e89f289",
			"tofu_1.11.14_darwin_arm64.tar.gz": "76d7c6e5be6fbe13496e691873603ffaec28bd734b6138b4e9bc8e6d3c710ec6",
			"tofu_1.11.14_darwin_amd64.tar.gz": "a3ad1623fad49f77b4128cc4af15dac6aea1e61dc191b337962def71e526b383",
			"tofu_1.11.14_linux_amd64.tar.gz":  "88ee1d2e1a69f266ac99b8b30374e07757a42219e745b046ea0426c08ea951ae",
			"tofu_1.11.14_linux_arm64.tar.gz":  "6c4d06e6c1235578f80f192c95257918450c5a28eb1aadbf34973dcd4fc1fdd0",
			"tofu_1.10.10_darwin_arm64.tar.gz": "e2b8199813e311b2da90955cdbca34b11903b3b0e8937abd32888b6f16ea5568",
			"tofu_1.10.10_darwin_amd64.tar.gz": "a4e05949878021717bd73818d23aa32b49445ce7e9ab5a4d1b38419f1e163baa",
			"tofu_1.10.10_linux_amd64.tar.gz":  "75b529fd9481b23e46a895520883f04c537bd6be4bd0dff83ad87e85806b12f1",
			"tofu_1.10.10_linux_arm64.tar.gz":  "cea25e126cc3428aec4bfb7c0e7acf117b4335c12160f4e4dbecd327a3f29bee",
		},
		Name:        ToolTofu,
		LatestURL:   "https://get.opentofu.org/tofu/api.json",
		ReleaseURL:  "https://github.com/opentofu/opentofu/releases/download/v%[1]s/%[2]s",
		versionsEnv: "E2E_TOFU_VERSIONS",
		binary:      "tofu",
		defaults:    []string{"1.12.12", "1.11.14", "1.10.10"},
	}
}

// Pinned is the version label that stands for the pinned versions of the
// default matrix. The default matrix holds no Latest. The nightly run uses it
// to run the pinned versions and Latest as two separate jobs.
const Pinned = "pinned"

// Versions returns the matrix from the tool's environment variable
// (E2E_PULUMI_VERSIONS or E2E_TOFU_VERSIONS). When the variable is not set it
// returns the pinned versions of the default matrix. The label Pinned in the
// variable stands for every pinned version. The scheduled nightly run sets the
// variable to latest to run the newest release.
func (t Tool) Versions() []string {
	raw := os.Getenv(t.versionsEnv)
	if raw == "" {
		return slices.Clone(t.defaults)
	}

	var versions []string

	for part := range strings.SplitSeq(raw, ",") {
		switch v := strings.TrimSpace(part); v {
		case "":
		case Pinned:
			for _, label := range t.defaults {
				if label != Latest {
					versions = append(versions, label)
				}
			}
		default:
			versions = append(versions, v)
		}
	}

	return versions
}

// Single returns the version of a test that installs one version of the
// tool: the first label of Versions that is not Latest. A pinned version has
// its sha256 sum in this file, so a cached binary needs no network request.
// When Versions holds Latest only, Single returns Latest: the scheduled run of
// the newest releases sets the matrix to Latest. When Versions is empty, Single
// returns "".
func (t Tool) Single() string {
	versions := t.Versions()
	if len(versions) == 0 {
		return ""
	}

	for _, label := range versions {
		if label != Latest {
			return label
		}
	}

	return Latest
}

// IsMatrixEnd reports whether label is the newest or the oldest version in
// the matrix of t, so a slow step can run only at the two ends. Latest is the
// newest. Other labels compare as x.y.z releases. A label that is not Latest
// and not x.y.z counts as an end, so such a step never skips a version that
// this function cannot place.
func (t Tool) IsMatrixEnd(label string) bool {
	if label == Latest {
		return true
	}

	own := semver(label)
	if own == nil {
		return true
	}

	newest, oldest := true, true

	for _, other := range t.Versions() {
		if other == Latest {
			newest = false

			continue
		}

		parts := semver(other)
		if parts == nil {
			continue
		}

		if slices.Compare(parts, own) > 0 {
			newest = false
		}

		if slices.Compare(parts, own) < 0 {
			oldest = false
		}
	}

	return newest || oldest
}

// VersionAtLeast reports whether version is the same as or newer than minimum.
// Both are x.y.z releases. A value that is not an x.y.z release is an error,
// so a caller never takes a branch for a version it cannot place.
func VersionAtLeast(version, minimum string) (bool, error) {
	own := semver(version)
	if own == nil {
		return false, fmt.Errorf("%w: %q is not an x.y.z release", ErrBadVersion, version)
	}

	least := semver(minimum)
	if least == nil {
		return false, fmt.Errorf("%w: %q is not an x.y.z release", ErrBadVersion, minimum)
	}

	return slices.Compare(own, least) >= 0, nil
}

// ArchiveName returns the release archive of version for this machine.
func (t Tool) ArchiveName(version string) (string, error) {
	switch {
	case runtime.GOOS != "linux" && runtime.GOOS != "darwin",
		runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64":
		return "", fmt.Errorf("%w: %s/%s", ErrUnsupportedPlatform, runtime.GOOS, runtime.GOARCH)
	default:
		return t.archive(version, runtime.GOOS, runtime.GOARCH), nil
	}
}

func (t Tool) releaseFile(version, file string) string {
	return fmt.Sprintf(t.ReleaseURL, version, file)
}

// tofuReleases is the part of OpenTofu's api.json this suite reads.
type tofuReleases struct {
	Versions []tofuRelease `json:"versions"`
}

type tofuRelease struct {
	ID string `json:"id"`
}

// newestStable picks the highest x.y.z release from OpenTofu's api.json.
func newestStable(body string) (string, error) {
	var doc tofuReleases

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return "", fmt.Errorf("%w: %w", ErrBadVersion, err)
	}

	var best []int

	bestID := ""

	for _, v := range doc.Versions {
		parts := semver(v.ID)
		if parts != nil && (best == nil || slices.Compare(parts, best) > 0) {
			best, bestID = parts, v.ID
		}
	}

	if bestID == "" {
		return "", fmt.Errorf("%w: no stable release listed", ErrBadVersion)
	}

	return bestID, nil
}

// semver parses a plain "x.y.z" release; pre-releases return nil.
func semver(s string) []int {
	fields := strings.Split(s, ".")
	if len(fields) != semverParts {
		return nil
	}

	parts := make([]int, 0, semverParts)

	for _, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil || n < 0 {
			return nil
		}

		parts = append(parts, n)
	}

	return parts
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
