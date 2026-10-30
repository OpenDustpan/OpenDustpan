//go:build e2e

package e2e

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// issuerKeyID is the kid of the key of the fake issuer.
const issuerKeyID = "e2e-key-1"

// OIDCIssuerFake is a fake OpenID Connect provider of a CI platform on a TLS
// test server on the loopback. It serves a discovery document and a JWKS
// with one RSA key, and signs tokens with that key. It makes no call to a
// real provider.
type OIDCIssuerFake struct {
	server   *httptest.Server
	key      *rsa.PrivateKey
	requests atomic.Int64
}

// NewOIDCIssuerFake starts the fake. It stops when the test ends.
func NewOIDCIssuerFake(tb testing.TB) *OIDCIssuerFake {
	tb.Helper()

	key, err := rsa.GenerateKey(rand.Reader, rsaBits)
	if err != nil {
		tb.Fatalf("generate the key of the fake issuer: %v", err)
	}

	f := &OIDCIssuerFake{server: nil, key: key, requests: atomic.Int64{}}
	f.server = httptest.NewUnstartedServer(http.HandlerFunc(f.serve))
	f.server.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	f.server.StartTLS()
	tb.Cleanup(f.server.Close)

	return f
}

// URL is the issuer of the fake: an https address on the loopback.
func (f *OIDCIssuerFake) URL() string { return f.server.URL }

// Requests is the number of requests that the fake answered.
func (f *OIDCIssuerFake) Requests() int64 { return f.requests.Load() }

// JWKS returns the key set of the fake, for a rule with static keys.
func (f *OIDCIssuerFake) JWKS() string {
	e := big.NewInt(int64(f.key.E)).Bytes()

	return fmt.Sprintf(`{"keys":[{"kty":"RSA","use":"sig","alg":"RS256","kid":%q,"n":%q,"e":%q}]}`, issuerKeyID,
		base64.RawURLEncoding.EncodeToString(f.key.N.Bytes()), base64.RawURLEncoding.EncodeToString(e))
}

// Sign returns a token with the claims claims, signed with RS256.
func (f *OIDCIssuerFake) Sign(tb testing.TB, claims map[string]any) string {
	tb.Helper()

	header, err := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT", "kid": issuerKeyID})
	if err != nil {
		tb.Fatalf("encode header: %v", err)
	}

	body, err := json.Marshal(claims)
	if err != nil {
		tb.Fatalf("encode claims: %v", err)
	}

	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(body)
	sum := sha256.Sum256([]byte(input))

	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, sum[:])
	if err != nil {
		tb.Fatalf("sign: %v", err)
	}

	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func (f *OIDCIssuerFake) certPool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(f.server.Certificate())

	return pool
}

func (f *OIDCIssuerFake) serve(w http.ResponseWriter, r *http.Request) {
	f.requests.Add(1)

	w.Header().Set("Content-Type", "application/json")

	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		writeBody(w, fmt.Sprintf(`{"issuer":%q,"jwks_uri":%q,"id_token_signing_alg_values_supported":["RS256"]}`,
			f.server.URL, f.server.URL+"/jwks"))
	case "/jwks":
		writeBody(w, f.JWKS())
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// TofuDirWithToken returns a new project directory of the workspace
// Workspace in Namespace, with token as the credential of the server in its
// CLI configuration. Run the CLI in it with RunTofuIn.
func (e *Env) TofuDirWithToken(tb testing.TB, token string) string {
	tb.Helper()

	dir := tb.TempDir()

	e.write(tb, filepath.Join(dir, "main.tf"), fmt.Sprintf(tofuConfig, e.Host(), Namespace, Workspace))
	e.write(tb, filepath.Join(dir, "tofurc"), fmt.Sprintf(tofuCLIConfig, e.Host(), token))

	return dir
}

// RunTofuIn runs the OpenTofu CLI tofu with args in dir, a directory from
// TofuDirWithToken. A non-zero exit is reported in the Result, not as an
// error.
func (e *Env) RunTofuIn(ctx context.Context, tofu Install, dir string, args ...string) (Result, error) {
	return e.RunAs(ctx, tofu, dir, []string{"TF_CLI_CONFIG_FILE=" + filepath.Join(dir, "tofurc")}, args...)
}
