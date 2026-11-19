//go:build e2e

package e2e_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// providerPlatforms are the platforms that the suite runs on.
func providerPlatforms() []string {
	return []string{"linux_amd64", "linux_arm64", "darwin_amd64", "darwin_arm64"}
}

func sha(body string) string {
	sum := sha256.Sum256([]byte(body))

	return hex.EncodeToString(sum[:])
}

// fakeProvider returns a pin whose sum for this platform is the sum of body,
// and a mirror that downloads from a local server that answers served.
func fakeProvider(t *testing.T, body, served string, hits *atomic.Int32) (*e2e.ProviderMirror, e2e.ProviderPin) {
	t.Helper()

	pin := e2e.ProviderPin{
		Name:    "fake",
		Version: "1.2.3",
		Sums:    map[string]string{runtime.GOOS + "_" + runtime.GOARCH: sha(body)},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)

		if _, err := w.Write([]byte(served)); err != nil {
			t.Errorf("write the fake archive: %v", err)
		}
	}))
	t.Cleanup(ts.Close)

	return &e2e.ProviderMirror{
		Client:     ts.Client(),
		CacheDir:   t.TempDir(),
		ReleaseURL: ts.URL + "/%[1]s/%[2]s/%[3]s",
		Pins:       []e2e.ProviderPin{pin},
	}, pin
}

func mirrorFile(m *e2e.ProviderMirror, pin e2e.ProviderPin) string {
	return filepath.Join(m.Dir(), "registry.opentofu.org", "hashicorp", pin.Name,
		pin.File(runtime.GOOS+"_"+runtime.GOARCH))
}

func TestProviderMirrorFillsVerifiedArchive(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32

	m, pin := fakeProvider(t, "zip-bytes", "zip-bytes", &hits)

	for range 2 {
		if err := m.Fill(t.Context()); err != nil {
			t.Fatalf("fill: %v", err)
		}
	}

	got, err := os.ReadFile(mirrorFile(m, pin))
	if err != nil || string(got) != "zip-bytes" {
		t.Fatalf("mirror file = %q, %v", got, err)
	}

	if hits.Load() != 1 {
		t.Fatalf("downloads = %d, want 1: a valid archive needs no request", hits.Load())
	}
}

// A mirror that gets the wrong bytes fails and publishes nothing.
func TestProviderMirrorFailsClosedOnMismatch(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32

	m, pin := fakeProvider(t, "zip-bytes", "tampered", &hits)

	err := m.Fill(t.Context())
	if !errors.Is(err, e2e.ErrChecksumMismatch) {
		t.Fatalf("fill = %v, want ErrChecksumMismatch", err)
	}

	if _, statErr := os.Stat(mirrorFile(m, pin)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("mismatched archive reached the mirror: %v", statErr)
	}

	entries, readErr := os.ReadDir(filepath.Dir(mirrorFile(m, pin)))
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("mirror dir holds %d entries, %v; want none", len(entries), readErr)
	}
}

// A cached archive that does not match its sum is replaced, and when the
// replacement does not match either, the mirror is empty.
func TestProviderMirrorReplacesTamperedCache(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32

	m, pin := fakeProvider(t, "zip-bytes", "zip-bytes", &hits)
	if err := m.Fill(t.Context()); err != nil {
		t.Fatalf("fill: %v", err)
	}

	if err := os.WriteFile(mirrorFile(m, pin), []byte("evil"), 0o600); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	if err := m.Fill(t.Context()); err != nil {
		t.Fatalf("fill after tamper: %v", err)
	}

	got, readErr := os.ReadFile(mirrorFile(m, pin))
	if readErr != nil || string(got) != "zip-bytes" {
		t.Fatalf("tampered cache stayed: %q, %v", got, readErr)
	}

	if err := os.WriteFile(mirrorFile(m, pin), []byte("evil"), 0o600); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	bad, badPin := fakeProvider(t, "zip-bytes", "other", &hits)
	bad.CacheDir = m.CacheDir

	if err := bad.Fill(t.Context()); !errors.Is(err, e2e.ErrChecksumMismatch) {
		t.Fatalf("fill = %v, want ErrChecksumMismatch", err)
	}

	if _, err := os.Stat(mirrorFile(bad, badPin)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tampered archive stayed in the mirror: %v", err)
	}
}

// A symlink in the cache does not redirect the mirror: not at the root, and
// not at a directory below it.
func TestProviderMirrorRefusesSymlinkInCache(t *testing.T) {
	t.Parallel()

	tests := map[string]func(t *testing.T, m *e2e.ProviderMirror, outside string){
		"root is a symlink": func(t *testing.T, m *e2e.ProviderMirror, outside string) {
			t.Helper()

			if err := os.Symlink(outside, m.Dir()); err != nil {
				t.Fatalf("symlink: %v", err)
			}
		},
		"host directory is a symlink": func(t *testing.T, m *e2e.ProviderMirror, outside string) {
			t.Helper()

			if err := os.MkdirAll(m.Dir(), 0o700); err != nil {
				t.Fatalf("mkdir: %v", err)
			}

			if err := os.Symlink(outside, filepath.Join(m.Dir(), "registry.opentofu.org")); err != nil {
				t.Fatalf("symlink: %v", err)
			}
		},
	}

	for name, setup := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var hits atomic.Int32

			m, _ := fakeProvider(t, "zip-bytes", "zip-bytes", &hits)
			outside := t.TempDir()
			setup(t, m, outside)

			if err := m.Fill(t.Context()); err == nil {
				t.Fatal("fill followed a symlink in the cache")
			}

			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 0 {
				t.Fatalf("outside directory holds %d entries, %v; want none", len(entries), err)
			}
		})
	}
}

func TestProviderMirrorRefusesBadPins(t *testing.T) {
	t.Parallel()

	platform := runtime.GOOS + "_" + runtime.GOARCH
	good := strings.Repeat("a", 64)

	tests := map[string]e2e.ProviderPin{
		"path in name":       {Name: "../x", Version: "1.0.0", Sums: map[string]string{platform: good}},
		"bad version":        {Name: "x", Version: "1.0", Sums: map[string]string{platform: good}},
		"short sum":          {Name: "x", Version: "1.0.0", Sums: map[string]string{platform: "abc"}},
		"no sum for this os": {Name: "x", Version: "1.0.0", Sums: map[string]string{}},
	}

	for name, pin := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var hits atomic.Int32

			m, _ := fakeProvider(t, "a", "a", &hits)
			m.Pins = []e2e.ProviderPin{pin}

			if err := m.Fill(t.Context()); err == nil {
				t.Fatal("fill accepted the pin")
			}

			if hits.Load() != 0 {
				t.Fatalf("requests = %d, want 0", hits.Load())
			}
		})
	}
}

func TestPinnedProvidersHaveSumsForEveryPlatform(t *testing.T) {
	t.Parallel()

	sumRe := regexp.MustCompile(`^[0-9a-f]{64}$`)

	for _, pin := range e2e.PinnedProviders() {
		for _, platform := range providerPlatforms() {
			if !sumRe.MatchString(pin.Sums[platform]) {
				t.Errorf("%s %s has no valid pinned sum for %s", pin.Source(), pin.Version, platform)
			}
		}
	}
}

// providerSourceRe finds a literal provider address in a test configuration.
var providerSourceRe = regexp.MustCompile(`source\s*=\s*"([^"%]+/[^"%]+)"`)

// TestEveryProviderOfTheSuiteIsPinned fails when a configuration of the suite
// names a provider that has no pinned sum, so no test installs a provider
// from the registry.
func TestEveryProviderOfTheSuiteIsPinned(t *testing.T) {
	t.Parallel()

	pinned := map[string]bool{}
	for _, pin := range e2e.PinnedProviders() {
		pinned[pin.Source()] = true
	}

	root, err := os.OpenRoot(".")
	if err != nil {
		t.Fatalf("open the e2e directory: %v", err)
	}

	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Errorf("close the e2e directory: %v", closeErr)
		}
	}()

	files, err := filepath.Glob("*_test.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("list test files: %v (%d)", err, len(files))
	}

	for _, file := range files {
		body, readErr := root.ReadFile(file)
		if readErr != nil {
			t.Fatalf("read %s: %v", file, readErr)
		}

		for _, m := range providerSourceRe.FindAllStringSubmatch(string(body), -1) {
			if !pinned[m[1]] {
				t.Errorf("%s names provider %q with no pinned sum; add it to PinnedProviders", file, m[1])
			}
		}
	}

	if len(pinned) == 0 {
		t.Fatal("no provider is pinned")
	}
}
