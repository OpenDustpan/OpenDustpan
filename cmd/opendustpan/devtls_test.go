package main

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDevCertNames(t *testing.T) {
	t.Parallel()

	local := []string{"127.0.0.1", "::1", "localhost"}

	tests := []struct {
		name       string
		listenHost string
		hostName   string
		extra      []string
		want       []string
	}{
		{name: "defaults", listenHost: "", hostName: "", extra: nil, want: local},
		{name: "loopback listen host adds nothing", listenHost: "127.0.0.1", hostName: "", extra: nil, want: local},
		{name: "wildcard listen host adds nothing", listenHost: "0.0.0.0", hostName: "", extra: nil, want: local},
		{
			name: "an address that is not a wildcard", listenHost: "10.1.2.3", hostName: "", extra: nil,
			want: []string{"10.1.2.3", "127.0.0.1", "::1", "localhost"},
		},
		{
			name: "a listen name", listenHost: "dev.local", hostName: "", extra: nil,
			want: []string{"127.0.0.1", "::1", "dev.local", "localhost"},
		},
		{
			name: "the host name", listenHost: "", hostName: "laptop", extra: nil,
			want: []string{"127.0.0.1", "::1", "laptop", "localhost"},
		},
		{
			name: "each hostname, with no repeat and no blank", listenHost: "", hostName: "laptop",
			extra: []string{"dustpan", " ", "laptop", "dustpan"},
			want:  []string{"127.0.0.1", "::1", "dustpan", "laptop", "localhost"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := devCertNames(tt.listenHost, tt.hostName, tt.extra)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("names = %v, want %v", got, tt.want)
			}
		})
	}
}

// parseLeaf reads the certificate file at path.
func parseLeaf(t *testing.T, path string) *x509.Certificate {
	t.Helper()

	raw, err := os.ReadFile(path) //nolint:gosec // The test reads the certificate that it made.
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	block, _ := pem.Decode(raw)
	if block == nil {
		t.Fatalf("%s holds no PEM block", path)
	}

	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	return leaf
}

func certInput(dir string, now time.Time, names []string, roots string, bundle bool) devCertInput {
	return devCertInput{
		now: func() time.Time { return now }, dir: dir, names: names, roots: []byte(roots), writeBundle: bundle,
	}
}

// verifyLeaf verifies the leaf for a name against a pool that holds only the
// leaf, at the time now.
func verifyLeaf(leaf *x509.Certificate, name string, now time.Time) error {
	pool := x509.NewCertPool()
	pool.AddCert(leaf)

	//nolint:exhaustruct_v5 // The check sets the roots, the name, the time and the usage only.
	_, err := leaf.Verify(x509.VerifyOptions{
		Roots: pool, DNSName: name, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	if err != nil {
		return fmt.Errorf("verify %s: %w", name, err)
	}

	return nil
}

func TestEnsureDevCertMakesALeaf(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	now := time.Date(2026, time.October, 19, 12, 0, 0, 0, time.UTC)
	names := []string{"127.0.0.1", "::1", "dustpan", "localhost"}

	cert, err := ensureDevCert(certInput(dir, now, names, "", false))
	if err != nil {
		t.Fatalf("ensureDevCert: %v", err)
	}

	for _, path := range []string{cert.certFile, cert.keyFile} {
		info, statErr := os.Stat(path)
		if statErr != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s = %v, %v, want mode 0600", path, info, statErr)
		}
	}

	leaf := parseLeaf(t, cert.certFile)

	if cert.bundleFile != "" || leaf.IsCA || !cert.notAfter.Equal(leaf.NotAfter) {
		t.Fatalf("certificate = %+v, CA = %v, want no bundle, no CA and the end of the leaf", cert, leaf.IsCA)
	}

	if got := leaf.NotAfter.Sub(now); got != 30*24*time.Hour {
		t.Fatalf("the leaf lives %s, want 30 days", got)
	}

	if got := leafNames(leaf); !slices.Equal(got, names) {
		t.Fatalf("leaf names = %v, want %v", got, names)
	}
}

// TestDevLeafIsItsOwnTrustAnchor proves a client that trusts only the leaf
// verifies the name of the server, and no other name.
func TestDevLeafIsItsOwnTrustAnchor(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 19, 12, 0, 0, 0, time.UTC)
	names := []string{"127.0.0.1", "::1", "dustpan", "localhost"}

	cert, err := ensureDevCert(certInput(t.TempDir(), now, names, "", false))
	if err != nil {
		t.Fatalf("ensureDevCert: %v", err)
	}

	leaf := parseLeaf(t, cert.certFile)

	for _, name := range []string{"localhost", "dustpan", "127.0.0.1", "::1"} {
		if verifyErr := verifyLeaf(leaf, name, now); verifyErr != nil {
			t.Errorf("verify the name %s: %v", name, verifyErr)
		}
	}

	if verifyErr := verifyLeaf(leaf, "other.example", now); verifyErr == nil {
		t.Error("the leaf verifies for a name that it does not hold")
	}
}

func TestEnsureDevCertReuse(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 19, 12, 0, 0, 0, time.UTC)
	names := []string{"127.0.0.1", "::1", "localhost"}

	tests := []struct {
		name      string
		later     time.Duration
		names     []string
		wantReuse bool
	}{
		{name: "next day with the same names", later: 24 * time.Hour, names: names, wantReuse: true},
		{
			name: "23 days later, less than 7 days remain", later: 23*24*time.Hour + time.Hour, names: names,
			wantReuse: false,
		},
		{name: "22 days later, 8 days remain", later: 22 * 24 * time.Hour, names: names, wantReuse: true},
		{
			name: "another name", later: time.Hour, names: []string{"127.0.0.1", "::1", "dustpan", "localhost"},
			wantReuse: false,
		},
		{name: "a name fewer", later: time.Hour, names: []string{"::1", "localhost"}, wantReuse: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			first, err := ensureDevCert(certInput(dir, now, names, "", false))
			if err != nil {
				t.Fatalf("first start: %v", err)
			}

			firstKey := readFileOf(t, first.keyFile)

			second, err := ensureDevCert(certInput(dir, now.Add(tt.later), tt.names, "", false))
			if err != nil {
				t.Fatalf("second start: %v", err)
			}

			reused := bytes.Equal(firstKey, readFileOf(t, second.keyFile)) && first.notAfter.Equal(second.notAfter)
			if reused != tt.wantReuse {
				t.Fatalf("reused = %v, want %v", reused, tt.wantReuse)
			}
		})
	}
}

// readFileOf reads a file that a test made.
func readFileOf(t *testing.T, path string) []byte {
	t.Helper()

	raw, err := os.ReadFile(path) //nolint:gosec // The test reads a file that it made.
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	return raw
}

func TestEnsureDevCertRefusesLoosePrivateKey(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	now := time.Date(2026, time.October, 19, 12, 0, 0, 0, time.UTC)

	cert, err := ensureDevCert(certInput(dir, now, []string{"localhost"}, "", false))
	if err != nil {
		t.Fatalf("first start: %v", err)
	}

	writeFileMode(t, cert.keyFile, 0o644)

	_, err = ensureDevCert(certInput(dir, now, []string{"localhost"}, "", false))
	if err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("err = %v, want it to hold chmod 600", err)
	}
}

// lastCertificate answers the last certificate block of a PEM text, or nil.
func lastCertificate(raw []byte) *pem.Block {
	var last *pem.Block

	for {
		block, rest := pem.Decode(raw)
		if block == nil {
			return last
		}

		if block.Type == "CERTIFICATE" {
			last = block
		}

		raw = rest
	}
}

// bundleRoots is the text of the roots that the bundle tests start with.
const bundleRoots = "-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"

func TestEnsureDevCertBundle(t *testing.T) {
	t.Parallel()

	rootsWithNewline := bundleRoots

	tests := []struct {
		name  string
		roots string
	}{
		{name: "roots that end with a line feed", roots: rootsWithNewline},
		{name: "roots that do not end with a line feed", roots: strings.TrimSuffix(rootsWithNewline, "\n")},
		{name: "no roots", roots: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			now := time.Date(2026, time.October, 19, 12, 0, 0, 0, time.UTC)

			cert, err := ensureDevCert(certInput(dir, now, []string{"localhost"}, tt.roots, true))
			if err != nil {
				t.Fatalf("ensureDevCert: %v", err)
			}

			checkBundle(t, dir, cert, tt.roots)
		})
	}
}

// checkBundle proves the bundle file of a certificate: the file with mode
// 0600, the roots first when there are some, and the leaf of dev last.
func checkBundle(t *testing.T, dir string, cert devCert, roots string) {
	t.Helper()

	info, err := os.Stat(cert.bundleFile)
	if cert.bundleFile != filepath.Join(dir, devBundleName) || err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("bundle = %q, %v, %v, want the bundle file with mode 0600", cert.bundleFile, info, err)
	}

	raw := readFileOf(t, cert.bundleFile)

	if roots != "" && !strings.HasPrefix(string(raw), bundleRoots+"-----BEGIN CERTIFICATE-----") {
		t.Fatalf("the bundle does not start with the roots and then the leaf: %q", raw)
	}

	last := lastCertificate(raw)
	if last == nil {
		t.Fatal("the bundle holds no leaf")
	}

	leaf, parseErr := x509.ParseCertificate(last.Bytes)
	if parseErr != nil || leaf.Subject.CommonName != "opendustpan dev" {
		t.Fatalf("last block = %v, %v, want the leaf of dev", leaf, parseErr)
	}
}

func TestDevRoots(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fileA := filepath.Join(dir, "a.pem")
	fileB := filepath.Join(dir, "b.pem")
	certDir := filepath.Join(dir, "certs")
	none := filepath.Join(dir, "none")

	for path, content := range map[string]string{fileA: "A\n", fileB: "B\n"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	if err := os.Mkdir(certDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	for name, content := range map[string]string{"1.pem": "D1", "2.pem": "D2\n"} {
		if err := os.WriteFile(filepath.Join(certDir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	tests := []struct {
		name  string
		env   map[string]string
		files []string
		dirs  []string
		want  string
	}{
		{
			name: "SSL_CERT_FILE wins", env: map[string]string{"SSL_CERT_FILE": fileA, "SSL_CERT_DIR": certDir},
			files: []string{fileB}, dirs: nil, want: "A\n",
		},
		{
			name: "SSL_CERT_DIR next", env: map[string]string{"SSL_CERT_DIR": certDir}, files: []string{fileB},
			dirs: nil, want: "D1\nD2\n",
		},
		{
			name: "the first root file that exists", env: nil, files: []string{none, fileB, fileA}, dirs: nil,
			want: "B\n",
		},
		{
			name: "the system directories last", env: nil, files: []string{none}, dirs: []string{certDir},
			want: "D1\nD2\n",
		},
		{
			name: "a SSL_CERT_FILE that cannot be read falls through", env: map[string]string{"SSL_CERT_FILE": none},
			files: []string{fileB}, dirs: nil, want: "B\n",
		},
		{name: "nothing", env: nil, files: nil, dirs: nil, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			lookup := func(key string) (string, bool) {
				v, ok := tt.env[key]

				return v, ok
			}

			got := devRoots(devRootsInput{lookup: lookup, files: tt.files, dirs: tt.dirs})
			if string(got) != tt.want {
				t.Fatalf("roots = %q, want %q", got, tt.want)
			}
		})
	}
}
