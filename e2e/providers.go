//go:build e2e

package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	// providerHost is the registry host of the providers that the suite uses.
	providerHost = "registry.opentofu.org"
	// providerNamespace is the namespace of the providers that the suite uses.
	providerNamespace = "hashicorp"
	// providerMirrorDirName is the directory of the provider mirror inside the
	// cache directory.
	providerMirrorDirName = "provider-mirror"
	// providerReleaseURL is the default format string of a provider archive:
	// %[1]s is the provider name, %[2]s the version and %[3]s the file name.
	providerReleaseURL = "https://github.com/opentofu/terraform-provider-%[1]s/releases/download/v%[2]s/%[3]s"
	// providerSumLength is the length of a hex sha256 sum.
	providerSumLength = 64
	// providerFillTimeout bounds the fill of the mirror.
	providerFillTimeout = 10 * time.Minute
)

// Provider versions of the suite. A test that writes a provider into a
// configuration uses these constants, so that the version in the configuration
// and the pin cannot differ.
const (
	// RandomProviderVersion is the version of the hashicorp/random provider.
	RandomProviderVersion = "3.9.1"
	// TFEProviderVersion is the version of the hashicorp/tfe provider.
	TFEProviderVersion = "0.82.0"
)

// Errors returned while filling the provider mirror.
var (
	// ErrBadProviderPin is returned for a pin that names an unsafe value.
	ErrBadProviderPin = errors.New("provider pin is not valid")
	// ErrNoProviderSum is returned when a provider has no pinned sum for this platform.
	ErrNoProviderSum = errors.New("no pinned checksum for provider on this platform")
	// ErrUnsafeMirror is returned when the mirror root is not a plain directory.
	ErrUnsafeMirror = errors.New("provider mirror root is not a plain directory")
)

var (
	providerNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	providerSumPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// ProviderPin is one provider version of the suite with the sha256 sum of its
// archive on each platform that the suite runs on. The key of Sums is
// "<os>_<arch>".
type ProviderPin struct {
	// Sums maps "<os>_<arch>" to the sha256 sum of the archive.
	Sums map[string]string
	// Name is the provider name, for example "random".
	Name string
	// Version is the x.y.z release.
	Version string
}

// Source returns the address of the provider in a required_providers block.
func (p ProviderPin) Source() string {
	return providerNamespace + "/" + p.Name
}

// File returns the archive name of the provider for platform ("<os>_<arch>").
func (p ProviderPin) File(platform string) string {
	return fmt.Sprintf("terraform-provider-%s_%s_%s.zip", p.Name, p.Version, platform)
}

// check refuses a pin whose name, version or sums cannot be used safely in a
// path or in a URL.
func (p ProviderPin) check() error {
	if !providerNamePattern.MatchString(p.Name) {
		return fmt.Errorf("%w: name %q", ErrBadProviderPin, p.Name)
	}

	if semver(p.Version) == nil {
		return fmt.Errorf("%w: version %q of %s", ErrBadProviderPin, p.Version, p.Name)
	}

	for platform, sum := range p.Sums {
		if len(sum) != providerSumLength || !providerSumPattern.MatchString(sum) {
			return fmt.Errorf("%w: sum of %s on %s", ErrBadProviderPin, p.Name, platform)
		}
	}

	return nil
}

// PinnedProviders returns the providers that the suite installs from the
// mirror. The sums come from the provider's own published checksum file,
// terraform-provider-<name>_<version>_SHA256SUMS on the GitHub release of
// github.com/opentofu/terraform-provider-<name>. The detached signature of
// each file (the .sig file of the release) checked against the OpenTofu
// provider signing key 0C0AF313E5FD9F80 (fingerprint
// E3E6E43D84CB852EADB0051D0C0AF313E5FD9F80, published by the registry), and
// the sum of linux_amd64 equals the shasum of the registry download answer.
func PinnedProviders() []ProviderPin {
	return []ProviderPin{
		{
			Name:    "random",
			Version: RandomProviderVersion,
			Sums: map[string]string{
				"darwin_amd64": "aaa51d905b0e80a28e02f9bee2cf6c91ffade7389d77ab9198aa12809ed04955",
				"darwin_arm64": "a2259540854d5f699c36b89244fb202ebb2c219b64669a51072687d04fb47152",
				"linux_amd64":  "624fb6ed552abc34a5aaac41e76a373da65ac08e524b09b672f29c60e6ac896a",
				"linux_arm64":  "09aaf19b0d22726d2378e0e89fbbefc183494d7bd585759d6c4e69ba50951a2f",
			},
		},
		{
			Name:    "tfe",
			Version: TFEProviderVersion,
			Sums: map[string]string{
				"darwin_amd64": "bd00321bb26f89f37e6f1752411483a47c79c2c679321688334cff8a56c02f59",
				"darwin_arm64": "b8eab95791ecdf061c7f4359a050a13c2061456dbd6fb041b6787c2de847693b",
				"linux_amd64":  "21afa7766afceb08110e9b0a5e3441db971f2b92152670092541cb69728984b3",
				"linux_arm64":  "4646b7b15a141956e54e4417baa0b5795c462016271e1d20fd0e45de0d187f6f",
			},
		},
	}
}

// ProviderMirror fills a filesystem mirror of OpenTofu providers. Each archive
// is checked against its pinned sum. A mismatch fails and leaves nothing in the
// mirror, so the mirror fails closed.
type ProviderMirror struct {
	// Client performs every request.
	Client *http.Client
	// CacheDir holds the mirror in a directory of its own and the lock files.
	CacheDir string
	// ReleaseURL is a format string for an archive: %[1]s is the provider
	// name, %[2]s the version and %[3]s the file name. Empty means the
	// GitHub release of the OpenTofu provider fork. Tests point it at a
	// local server.
	ReleaseURL string
	// Pins are the providers to install. Empty means PinnedProviders.
	Pins []ProviderPin
}

// Dir returns the root of the filesystem mirror, the directory that
// provider_installation names with path.
func (m *ProviderMirror) Dir() string {
	return filepath.Join(m.CacheDir, providerMirrorDirName)
}

// Fill makes the mirror hold each pinned provider for this platform. An
// archive that is in the mirror and matches its sum needs no request. Fill
// replaces an archive that does not match.
func (m *ProviderMirror) Fill(ctx context.Context) error {
	pins := m.Pins
	if pins == nil {
		pins = PinnedProviders()
	}

	platform := runtime.GOOS + "_" + runtime.GOARCH

	for _, pin := range pins {
		if err := m.fillOne(ctx, pin, platform); err != nil {
			return err
		}
	}

	return nil
}

func (m *ProviderMirror) fillOne(ctx context.Context, pin ProviderPin, platform string) error {
	// Defense in depth: os.Root contains each path below too. This check
	// does not rely on it, and the pin also goes into the download URL.
	if err := pin.check(); err != nil {
		return err
	}

	want, ok := pin.Sums[platform]
	if !ok {
		return fmt.Errorf("%w: %s %s on %s", ErrNoProviderSum, pin.Source(), pin.Version, platform)
	}

	file := pin.File(platform)
	rel := filepath.Join(providerHost, providerNamespace, pin.Name)

	unlock, err := lockEntry(m.CacheDir, providerMirrorDirName+"-"+file)
	if err != nil {
		return err
	}

	defer unlock()

	root, err := openMirrorRoot(m.Dir())
	if err != nil {
		return err
	}

	defer ignoreErr(root.Close)

	// os.Root keeps the directories below the mirror root: a symlink in the
	// cache cannot send them elsewhere.
	if err := root.MkdirAll(rel, dirMode); err != nil {
		return fmt.Errorf("create provider mirror dir: %w", err)
	}

	dest := filepath.Join(rel, file)

	// The cache comes from CI and is untrusted: an archive is used only
	// when it matches the pinned sum.
	if got, hashErr := hashRootFile(root, dest); hashErr == nil && got == want {
		return nil
	}

	if err := root.Remove(dest); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove provider archive %s: %w", file, err)
	}

	return m.download(ctx, root, pin, file, want)
}

func (m *ProviderMirror) download(
	ctx context.Context, root *os.Root, pin ProviderPin, file, want string,
) error {
	rel := filepath.Join(providerHost, providerNamespace, pin.Name)
	format := m.ReleaseURL
	if format == "" {
		format = providerReleaseURL
	}

	url := fmt.Sprintf(format, pin.Name, pin.Version, file)

	tmpName := filepath.Join(rel, "download-"+file+".part")

	tmp, err := root.OpenFile(tmpName, os.O_CREATE|os.O_RDWR|os.O_TRUNC, lockMode)
	if err != nil {
		return fmt.Errorf("create provider download file: %w", err)
	}

	defer ignoreErr(func() error { return root.Remove(tmpName) })
	defer ignoreErr(tmp.Close)

	fetcher := &Fetcher{
		Client:     m.Client,
		CacheDir:   m.CacheDir,
		mu:         sync.Mutex{},
		checked:    nil,
		checks:     atomic.Int64{},
		beforeLock: nil,
	}

	got, err := fetcher.fetchWithRetry(ctx, url, tmp)
	if err != nil {
		return err
	}

	// Fail closed: a file with another sum never reaches the mirror path.
	if got != want {
		return fmt.Errorf("%w: %s: want %s, got %s", ErrChecksumMismatch, file, want, got)
	}

	if err := root.Rename(tmpName, filepath.Join(rel, file)); err != nil {
		return fmt.Errorf("publish provider archive %s: %w", file, err)
	}

	return nil
}

// hashRootFile returns the sha256 sum of name in root.
func hashRootFile(root *os.Root, name string) (string, error) {
	f, err := root.Open(name)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", name, err)
	}

	defer ignoreErr(f.Close)

	hash := sha256.New()

	if _, err := io.Copy(hash, io.LimitReader(f, maxArchiveBytes)); err != nil {
		return "", fmt.Errorf("read %s: %w", name, err)
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// CacheDir returns the directory that holds the CLI cache and the provider
// mirror: E2E_CACHE_DIR when set, else a directory in the user cache.
func CacheDir() (string, error) {
	if dir := os.Getenv("E2E_CACHE_DIR"); dir != "" {
		return dir, nil
	}

	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("find user cache dir: %w", err)
	}

	return filepath.Join(base, "opendustpan-e2e"), nil
}

// providerMirrorDir makes the provider mirror hold each pinned provider and
// returns its directory. It keeps no state in the process: Fill checks each
// archive against its pinned sum and makes no request for one that matches,
// and the lock of each archive serializes the callers. A failure stops the
// test: no test falls back to the registry.
func providerMirrorDir(tb testing.TB) string {
	tb.Helper()

	cache, err := CacheDir()
	if err != nil {
		tb.Fatalf("fill the provider mirror: %v", err)
	}

	mirror := &ProviderMirror{Client: http.DefaultClient, CacheDir: cache, ReleaseURL: "", Pins: nil}

	// The fill goes through an interface: the helpers of the harness have no
	// context to pass, and the fill makes its own.
	var filler mirrorFiller = detachedFiller{mirror: mirror}
	if err := filler.fill(tb); err != nil {
		tb.Fatalf("fill the provider mirror: %v", err)
	}

	return mirror.Dir()
}

// providerInstallation returns the provider_installation block of the CLI
// configuration: the providers of the registry host come from the mirror
// only, and no provider of that host comes direct. Other hosts, for example
// the registry of the server under test, keep the direct install.
func providerInstallation(mirrorDir string) string {
	return fmt.Sprintf(`provider_installation {
  filesystem_mirror {
    path    = %q
    include = [%q]
  }
  direct {
    exclude = [%q]
  }
}
`, mirrorDir, providerHost+"/*/*", providerHost+"/*/*")
}

// tofuRC returns the CLI configuration of OpenTofu for host: the token and the
// provider mirror.
func tofuRC(tb testing.TB, host, token string) string {
	tb.Helper()

	return fmt.Sprintf(tofuCLIConfig, host, token) + providerInstallation(providerMirrorDir(tb))
}

// openMirrorRoot opens the mirror root and makes it when it is absent. The
// root itself is the one path that os.Root cannot contain, so a root that is
// a symlink is refused.
func openMirrorRoot(dir string) (*os.Root, error) {
	if err := os.MkdirAll(filepath.Dir(dir), dirMode); err != nil {
		return nil, fmt.Errorf("create provider cache dir: %w", err)
	}

	if err := os.Mkdir(dir, dirMode); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("create provider mirror root: %w", err)
	}

	info, err := os.Lstat(dir)
	if err != nil {
		return nil, fmt.Errorf("check provider mirror root: %w", err)
	}

	if !info.IsDir() {
		return nil, fmt.Errorf("%w: %s is not a directory", ErrUnsafeMirror, dir)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open provider mirror: %w", err)
	}

	return root, nil
}

// mirrorFiller fills a provider mirror for a test.
type mirrorFiller interface {
	fill(tb testing.TB) error
}

// detachedFiller runs Fill with a deadline and without the cancel of the
// test: another test of the process reuses the archives, so the fill does not
// stop when the first test ends.
type detachedFiller struct {
	mirror *ProviderMirror
}

func (d detachedFiller) fill(tb testing.TB) error {
	tb.Helper()

	ctx, cancel := context.WithTimeout(context.WithoutCancel(tb.Context()), providerFillTimeout)
	defer cancel()

	return d.mirror.Fill(ctx)
}
