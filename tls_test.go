package dustpan_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	dustpan "opendustpan.org/opendustpan"
	"opendustpan.org/opendustpan/blob/fs"
	"opendustpan.org/opendustpan/identity"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/store/sqlite"
)

// writeTestCertificate generates a self-signed certificate for localhost and
// writes it to certPath and keyPath. It returns the parsed certificate so
// clients can trust it.
func writeTestCertificate(t *testing.T, certPath, keyPath string) *x509.Certificate {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("generate serial: %v", err)
	}

	//nolint:exhaustruct_v5 // A test certificate sets only identity and validity; the rest stays zero.
	template := x509.Certificate{
		SerialNumber: serial,
		//nolint:exhaustruct_v5 // The test subject needs only a common name.
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}

	//nolint:exhaustruct_v5 // A test PEM block carries no headers.
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}

	//nolint:exhaustruct_v5 // A test PEM block carries no headers.
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("write certificate: %v", err)
	}

	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	return cert
}

// newServerWithOptions builds a Server on addr backed by a fresh SQLite
// database, with extra options applied last.
func newServerWithOptions(t *testing.T, addr string, opts ...dustpan.Option) *dustpan.Server {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	authn, err := identity.NewStaticToken("admin", adminToken)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	blobs, err := fs.Open(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	base := []dustpan.Option{
		dustpan.WithRegion(region),
		dustpan.WithNamespaceStore(st),
		dustpan.WithStackStore(st),
		dustpan.WithUpdateStore(st),
		dustpan.WithCheckpointStore(st),
		dustpan.WithEventStore(st),
		dustpan.WithSecretKeyStore(st),
		dustpan.WithWorkspaceStore(st),
		dustpan.WithStateStore(st),
		dustpan.WithBlobStore(blobs),
		dustpan.WithInventoryReader(st),
		dustpan.WithAuthenticator(authn),
		dustpan.WithListenAddr(addr),
	}

	srv, err := dustpan.NewServer(append(base, opts...)...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	return srv
}

// freeLocalPort returns an unused loopback port, for tests that must know
// the address before the server starts.
func freeLocalPort(t *testing.T) string {
	t.Helper()

	//nolint:exhaustruct_v5 // The test listens with defaults; only the context matters.
	lc := &net.ListenConfig{}

	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	_, port, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		t.Fatalf("split address: %v", err)
	}

	if err := l.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	return port
}

// waitForTCP waits until addr accepts TCP connections.
func waitForTCP(t *testing.T, addr string) {
	t.Helper()

	//nolint:exhaustruct_v5 // The probe dials with defaults and a timeout only.
	dialer := &net.Dialer{Timeout: 100 * time.Millisecond}
	deadline := time.Now().Add(10 * time.Second)

	for time.Now().Before(deadline) {
		conn, err := dialer.DialContext(t.Context(), "tcp", addr)
		if err == nil {
			if closeErr := conn.Close(); closeErr != nil {
				t.Fatalf("close probe: %v", closeErr)
			}

			return
		}

		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-t.Context().Done():
			timer.Stop()
			t.Fatal("context done while waiting for the server")
		case <-timer.C:
		}
	}

	t.Fatalf("server on %s did not start in time", addr)
}

// runServer starts srv and stops it when ctx is done, reporting its error.
func runServer(ctx context.Context, srv *dustpan.Server) <-chan error {
	done := make(chan error, 1)

	go func() {
		done <- srv.Run(ctx)
	}()

	return done
}

// tlsVersionCase is one handshake the TLS test dials: the client version
// range and whether the handshake must fail.
type tlsVersionCase struct {
	name       string
	minVersion uint16
	maxVersion uint16
	wantFail   bool
}

func TestRunTLSVersions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cert := writeTestCertificate(t, filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem"))

	port := freeLocalPort(t)
	addr := "127.0.0.1:" + port

	srv := newServerWithOptions(
		t,
		addr,
		dustpan.WithTLS(filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")),
	)

	ctx, cancel := context.WithCancel(t.Context())
	done := runServer(ctx, srv)

	t.Cleanup(func() {
		cancel()

		if err := <-done; err != nil {
			t.Errorf("run: %v", err)
		}
	})

	waitForTCP(t, addr)

	pool := x509.NewCertPool()
	pool.AddCert(cert)

	cases := []tlsVersionCase{
		{name: "TLS 1.2", minVersion: tls.VersionTLS12, maxVersion: tls.VersionTLS12, wantFail: false},
		{name: "TLS 1.3", minVersion: tls.VersionTLS13, maxVersion: tls.VersionTLS13, wantFail: false},
		{name: "TLS 1.1 fails", minVersion: tls.VersionTLS10, maxVersion: tls.VersionTLS11, wantFail: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkTLSVersion(t, pool, addr, tc)
		})
	}
}

// checkTLSVersion dials addr with the version range of tc and checks
// /healthz: a handshake failure when tc wants one, else status 204 and the
// negotiated version.
func checkTLSVersion(t *testing.T, pool *x509.CertPool, addr string, tc tlsVersionCase) {
	t.Helper()

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    pool,
				MinVersion: tc.minVersion,
				MaxVersion: tc.maxVersion,
			},
		},
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://"+addr+"/healthz", http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := client.Do(req)
	if tc.wantFail {
		checkHandshakeFailed(t, resp, err)

		return
	}

	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("close body: %v", closeErr)
		}
	}()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("GET /healthz status %d, want %d", resp.StatusCode, http.StatusNoContent)
	}

	if resp.TLS == nil || resp.TLS.Version != tc.maxVersion {
		t.Fatalf("negotiated version %v, want %v", resp.TLS, tc.maxVersion)
	}
}

// checkHandshakeFailed proves the handshake failed: the dial must answer an
// error and never a response with a body to close.
func checkHandshakeFailed(t *testing.T, resp *http.Response, err error) {
	t.Helper()

	if err == nil {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("close body: %v", closeErr)
		}

		t.Fatal("TLS 1.1 connection succeeded, want a handshake failure")
	}
}

func TestNewServerRejectsSingleTLSFile(t *testing.T) {
	t.Parallel()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	authn, err := identity.NewStaticToken("admin", adminToken)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	blobs, err := fs.Open(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	base := []dustpan.Option{
		dustpan.WithRegion(region),
		dustpan.WithNamespaceStore(st),
		dustpan.WithStackStore(st),
		dustpan.WithUpdateStore(st),
		dustpan.WithCheckpointStore(st),
		dustpan.WithEventStore(st),
		dustpan.WithSecretKeyStore(st),
		dustpan.WithWorkspaceStore(st),
		dustpan.WithStateStore(st),
		dustpan.WithBlobStore(blobs),
		dustpan.WithInventoryReader(st),
		dustpan.WithAuthenticator(authn),
	}

	if _, err := dustpan.NewServer(append(base, dustpan.WithTLS("cert.pem", ""))...); err == nil {
		t.Fatal("NewServer with only a certificate succeeded")
	} else if !errors.Is(err, dustpan.ErrMissingOption) || !strings.Contains(err.Error(), "key") {
		t.Fatalf("NewServer with only a certificate: err = %v, want it to name the missing key", err)
	}

	if _, err := dustpan.NewServer(append(base, dustpan.WithTLS("", "key.pem"))...); err == nil {
		t.Fatal("NewServer with only a key succeeded")
	} else if !errors.Is(err, dustpan.ErrMissingOption) || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("NewServer with only a key: err = %v, want it to name the missing certificate", err)
	}
}

func TestNewServerRejectsBadPublicURL(t *testing.T) {
	t.Parallel()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	authn, err := identity.NewStaticToken("admin", adminToken)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	blobs, err := fs.Open(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	_, err = dustpan.NewServer(
		dustpan.WithRegion(region),
		dustpan.WithNamespaceStore(st),
		dustpan.WithStackStore(st),
		dustpan.WithUpdateStore(st),
		dustpan.WithCheckpointStore(st),
		dustpan.WithEventStore(st),
		dustpan.WithSecretKeyStore(st),
		dustpan.WithWorkspaceStore(st),
		dustpan.WithStateStore(st),
		dustpan.WithBlobStore(blobs),
		dustpan.WithInventoryReader(st),
		dustpan.WithAuthenticator(authn),
		dustpan.WithPublicURL("not a url"),
	)
	if err == nil {
		t.Fatal("NewServer with a bad public URL succeeded")
	}

	if !errors.Is(err, baseurl.ErrInvalid) || !strings.Contains(err.Error(), "public URL") {
		t.Fatalf("NewServer with a bad public URL: err = %v, want it to name the public URL", err)
	}
}

func TestRunTLSMissingFiles(t *testing.T) {
	t.Parallel()

	srv := newServerWithOptions(
		t,
		"127.0.0.1:0",
		dustpan.WithTLS(filepath.Join(t.TempDir(), "missing-cert.pem"), filepath.Join(t.TempDir(), "missing-key.pem")),
	)

	if err := srv.Run(t.Context()); err == nil {
		t.Fatal("Run with missing certificate files succeeded")
	} else if !strings.Contains(err.Error(), "TLS") {
		t.Fatalf("Run with missing certificate files: err = %v, want it to name TLS", err)
	}
}

// logSink is a goroutine-safe log sink: the server logs from its serving
// goroutine while the test watches for lines.
type logSink struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logSink) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	n, err := b.buf.Write(p)
	if err != nil {
		return n, fmt.Errorf("write log sink: %w", err)
	}

	return n, nil
}

func (b *logSink) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// newServerWithLog builds a Server on addr that logs to logs.
func newServerWithLog(t *testing.T, addr string, logs *logSink) *dustpan.Server {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(logs, nil))

	return newServerWithOptions(t, addr, dustpan.WithLogger(logger))
}

// runUntilListening starts srv and stops it once it logs "listening".
func runUntilListening(t *testing.T, srv *dustpan.Server, logs *logSink) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := runServer(ctx, srv)

	deadline := time.Now().Add(10 * time.Second)

	for time.Now().Before(deadline) {
		if strings.Contains(logs.String(), "listening") {
			cancel()

			if err := <-done; err != nil {
				t.Fatalf("run: %v", err)
			}

			return
		}
	}

	cancel()
	<-done
	t.Fatal("server did not start in time")
}

func TestRunPlainHTTPWarning(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		addr     string
		wantWarn bool
	}{
		{name: "empty host binds every interface", addr: ":0", wantWarn: true},
		{name: "IPv4 all interfaces", addr: "0.0.0.0:0", wantWarn: true},
		{name: "IPv4 loopback", addr: "127.0.0.1:0", wantWarn: false},
		{name: "IPv6 loopback", addr: "[::1]:0", wantWarn: false},
		{name: "localhost", addr: "localhost:0", wantWarn: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.addr == "[::1]:0" && !ipv6LoopbackAvailable(t) {
				t.Skip("IPv6 loopback is not available on this host")
			}

			var logs logSink

			srv := newServerWithLog(t, tc.addr, &logs)
			runUntilListening(t, srv, &logs)

			if got := strings.Contains(logs.String(), "plain HTTP"); got != tc.wantWarn {
				t.Fatalf("warning logged = %t, want %t (logs %q)", got, tc.wantWarn, logs.String())
			}
		})
	}
}

// ipv6LoopbackAvailable reports whether this host can bind IPv6 loopback,
// so the warning test skips that address where IPv6 is absent.
func ipv6LoopbackAvailable(t *testing.T) bool {
	t.Helper()

	//nolint:exhaustruct_v5 // The probe listens with defaults; only success matters.
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "[::1]:0")
	if err != nil {
		return false
	}

	if err := l.Close(); err != nil {
		t.Fatalf("close IPv6 probe: %v", err)
	}

	return true
}
