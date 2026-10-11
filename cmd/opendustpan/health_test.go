package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"flag"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// runHealthCommand runs `health` with args and answers its stdout and
// error. A nil error means exit code 0, a non-nil error exit code 1.
func runHealthCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var stdout, stderr bytes.Buffer

	err := run(t.Context(), append([]string{"health"}, args...), &stdout, &stderr)

	return stdout.String(), err
}

// healthListenOf answers the host:port of server for a --listen flag.
func healthListenOf(t *testing.T, url string) string {
	t.Helper()

	for _, scheme := range []string{"http://", "https://"} {
		if rest, ok := strings.CutPrefix(url, scheme); ok {
			return rest
		}
	}

	t.Fatalf("server URL %q has no http scheme", url)

	return ""
}

// freePort answers a free loopback port for a health test server. The
// port can be taken again before the server binds it, so callers bind it
// at once.
func freePort(t *testing.T) string {
	t.Helper()

	//nolint:exhaustruct_v5 // The test listens with defaults; only the port matters.
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for a free port: %v", err)
	}

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("free port address %v is not TCP", listener.Addr())
	}

	port := addr.Port

	if err := listener.Close(); err != nil {
		t.Fatalf("close free port listener: %v", err)
	}

	return strconv.Itoa(port)
}

// startHealthServer runs serve with args until it logs "listening" and
// answers a stop function that ends the server and waits for it, so the
// health tests probe a running server with no leaked goroutine.
func startHealthServer(t *testing.T, args ...string) func() {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())

	var logs lockedBuffer

	done := make(chan error, 1)

	go func() {
		done <- serve(ctx, args, &logs)
	}()

	ticker := time.NewTicker(serveStartPoll)
	defer ticker.Stop()

	timeout := time.NewTimer(serveStartTimeout)
	defer timeout.Stop()

	for {
		select {
		case <-ticker.C:
			if strings.Contains(logs.String(), "listening") {
				return func() {
					cancel()

					if err := <-done; err != nil {
						t.Errorf("serve: %v", err)
					}
				}
			}
		case <-timeout.C:
			cancel()
			t.Fatal("server did not start in time")
		}
	}
}

// writeHealthCertificate generates a self-signed certificate for
// localhost and writes it to certPath and keyPath, so a test serves TLS
// as the smoke test does with its generated certificate.
func writeHealthCertificate(t *testing.T, certPath, keyPath string) {
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
}

func TestHealthURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		listen  string
		tlsCert string
		want    string
	}{
		{"bare port probes HTTP on loopback", ":8080", "", "http://127.0.0.1:8080/health/ready"},
		{"wildcard probes loopback", "0.0.0.0:8080", "", "http://127.0.0.1:8080/health/ready"},
		{"IPv6 wildcard probes loopback", "[::]:8080", "", "http://127.0.0.1:8080/health/ready"},
		{"loopback stays", "127.0.0.1:8080", "", "http://127.0.0.1:8080/health/ready"},
		{"hostname stays", "localhost:8080", "", "http://localhost:8080/health/ready"},
		{"certificate selects HTTPS", ":8080", "/certs/cert.pem", "https://127.0.0.1:8080/health/ready"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := healthURL(tc.listen, tc.tlsCert)
			if err != nil {
				t.Fatalf("health URL of %q: %v", tc.listen, err)
			}

			if got != tc.want {
				t.Fatalf("health URL of %q = %q, want %q", tc.listen, got, tc.want)
			}
		})
	}

	t.Run("bad address names the setting", func(t *testing.T) {
		t.Parallel()

		_, err := healthURL("not-an-address", "")
		if !errors.Is(err, errHealthFailed) {
			t.Fatalf("health URL of a bad address: err = %v, want ErrHealthFailed", err)
		}

		if !strings.Contains(err.Error(), "DUSTPAN_LISTEN") {
			t.Fatalf("health URL error = %q, want it to name DUSTPAN_LISTEN", err)
		}
	})
}

func TestHealthFlagDefaults(t *testing.T) {
	t.Parallel()

	var f healthFlags

	fs := flag.NewFlagSet("health", flag.ContinueOnError)
	f.register(fs)

	if err := fs.Parse(nil); err != nil {
		t.Fatalf("parse defaults: %v", err)
	}

	if f.listen != ":8080" {
		t.Fatalf("listen = %q, want :8080 as serve uses", f.listen)
	}

	if f.tlsCert != "" {
		t.Fatalf("tls-cert = %q, want empty for plain HTTP", f.tlsCert)
	}
}

func TestHealthProbe(t *testing.T) {
	t.Parallel()

	t.Run("ready answers ready", func(t *testing.T) {
		t.Parallel()
		checkHealthReady(t)
	})

	t.Run("created counts as ready", func(t *testing.T) {
		t.Parallel()
		checkHealthCreated(t)
	})

	t.Run("unavailable names the status and the checks", func(t *testing.T) {
		t.Parallel()
		checkHealthUnavailable(t)
	})

	t.Run("no server names the setting", func(t *testing.T) {
		t.Parallel()
		checkHealthNoServer(t)
	})

	t.Run("positional argument is usage", func(t *testing.T) {
		t.Parallel()
		checkHealthPositionalArg(t)
	})
}

// checkHealthReady proves health prints ready and exits 0 on a 200
// answer from /health/ready.
func checkHealthReady(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health/ready" {
			w.WriteHeader(http.StatusNotFound)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		if _, err := io.WriteString(w, `{"status":"ok"}`); err != nil {
			t.Errorf("write ready body: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	out, err := runHealthCommand(t, "--listen", healthListenOf(t, server.URL))
	if err != nil {
		t.Fatalf("health of a ready server: %v", err)
	}

	if strings.TrimSpace(out) != "ready" {
		t.Fatalf("health output = %q, want ready", out)
	}
}

// checkHealthCreated proves health exits 0 on each 2xx answer, not only
// on 200.
func checkHealthCreated(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(server.Close)

	if _, err := runHealthCommand(t, "--listen", healthListenOf(t, server.URL)); err != nil {
		t.Fatalf("health of a 201 server: %v", err)
	}
}

// checkHealthUnavailable proves health exits 1 with ErrHealthNotReady on
// a 503 answer and names the status with the failed checks.
func checkHealthUnavailable(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)

		if _, err := io.WriteString(w, `{"status":"unavailable","failed":["store"]}`); err != nil {
			t.Errorf("write unavailable body: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	_, err := runHealthCommand(t, "--listen", healthListenOf(t, server.URL))
	if err == nil || !errors.Is(err, errHealthNotReady) {
		t.Fatalf("health of an unavailable server: err = %v, want ErrHealthNotReady", err)
	}

	for _, want := range []string{"503", "store"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("health error = %q, want it to name %q", err, want)
		}
	}
}

// checkHealthNoServer proves health exits 1 with ErrHealthFailed when no
// server listens and names the listen setting.
func checkHealthNoServer(t *testing.T) {
	t.Helper()

	_, err := runHealthCommand(t, "--listen", "127.0.0.1:"+freePort(t))
	if err == nil || !errors.Is(err, errHealthFailed) {
		t.Fatalf("health without a server: err = %v, want ErrHealthFailed", err)
	}

	if !strings.Contains(err.Error(), "DUSTPAN_LISTEN") {
		t.Fatalf("health error = %q, want it to name DUSTPAN_LISTEN", err)
	}
}

// checkHealthPositionalArg proves health with a positional argument
// answers the usage error.
func checkHealthPositionalArg(t *testing.T) {
	t.Helper()

	_, err := runHealthCommand(t, "extra")
	if !errors.Is(err, errUsage) {
		t.Fatalf("health with a positional argument: err = %v, want the usage error", err)
	}
}

// TestHealthTLSFromEnv proves health reads the TLS server through the
// listen and certificate settings in the environment, with no flag.
func TestHealthTLSFromEnv(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	t.Setenv("DUSTPAN_LISTEN", healthListenOf(t, server.URL))
	t.Setenv("DUSTPAN_TLS_CERT", "test-cert.pem")

	out, err := runHealthCommand(t)
	if err != nil {
		t.Fatalf("health of a TLS server from the environment: %v", err)
	}

	if strings.TrimSpace(out) != "ready" {
		t.Fatalf("health output = %q, want ready", out)
	}
}

func TestHealthUsage(t *testing.T) {
	t.Parallel()

	if !strings.Contains(errUsage.Error(), "health") {
		t.Fatalf("usage = %q, want it to name health", errUsage.Error())
	}
}

//nolint:paralleltest // serve reads process environment, so this test sets it and runs its cases in order.
func TestHealthAgainstServer(t *testing.T) {
	healthReadyPlainServer(t)
	healthReadyTLSServer(t)
	healthTLSServerWithoutCert(t)
}

// healthReadyPlainServer proves health exits 0 against a running server
// over plain HTTP.
func healthReadyPlainServer(t *testing.T) {
	t.Helper()

	setServeEnv(t, testKeyBase64())

	port := freePort(t)
	db := "sqlite:" + filepath.Join(t.TempDir(), "dustpan.db")

	stop := startHealthServer(t, "--database", db, "--listen", "127.0.0.1:"+port)
	defer stop()

	out, err := runHealthCommand(t, "--listen", "127.0.0.1:"+port)
	if err != nil {
		t.Fatalf("health of a running server: %v", err)
	}

	if strings.TrimSpace(out) != "ready" {
		t.Fatalf("health output = %q, want ready", out)
	}
}

// healthReadyTLSServer proves health exits 0 against a running server
// over HTTPS with a self-signed certificate.
func healthReadyTLSServer(t *testing.T) {
	t.Helper()

	setServeEnv(t, testKeyBase64())

	dir := t.TempDir()
	cert := filepath.Join(dir, "cert.pem")
	key := filepath.Join(dir, "key.pem")
	writeHealthCertificate(t, cert, key)

	port := freePort(t)
	db := "sqlite:" + filepath.Join(dir, "dustpan.db")

	stop := startHealthServer(
		t,
		"--database", db,
		"--listen", "127.0.0.1:"+port,
		"--tls-cert", cert,
		"--tls-key", key,
	)
	defer stop()

	out, err := runHealthCommand(t, "--listen", "127.0.0.1:"+port, "--tls-cert", cert)
	if err != nil {
		t.Fatalf("health of a running TLS server: %v", err)
	}

	if strings.TrimSpace(out) != "ready" {
		t.Fatalf("health output = %q, want ready", out)
	}
}

// healthTLSServerWithoutCert proves health exits 1 when it probes a TLS
// server over plain HTTP: the scheme must follow the TLS settings.
func healthTLSServerWithoutCert(t *testing.T) {
	t.Helper()

	setServeEnv(t, testKeyBase64())

	dir := t.TempDir()
	cert := filepath.Join(dir, "cert.pem")
	key := filepath.Join(dir, "key.pem")
	writeHealthCertificate(t, cert, key)

	port := freePort(t)
	db := "sqlite:" + filepath.Join(dir, "dustpan.db")

	stop := startHealthServer(
		t,
		"--database", db,
		"--listen", "127.0.0.1:"+port,
		"--tls-cert", cert,
		"--tls-key", key,
	)
	defer stop()

	_, err := runHealthCommand(t, "--listen", "127.0.0.1:"+port)
	if err == nil {
		t.Fatal("health of a TLS server over plain HTTP succeeded, want an error")
	}
}
