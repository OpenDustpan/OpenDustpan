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
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// GitHostAppID and GitHostInstallationID are the app and the installation that
// the fake GitHub API knows.
// jwtParts is the number of parts of a JWT: the header, the claims and the signature.
const jwtParts = 3

// rsaBits is the size of the key of the fake app.
const rsaBits = 2048

const (
	// GitHostAppID is the ID of the app that the fake knows.
	GitHostAppID = "4242"
	// GitHostInstallationID is the ID of the installation that the fake knows.
	GitHostInstallationID = "9001"
)

// GitHostFake is a fake GitHub API on a TLS test server. It holds the key of
// one app, verifies the JWT of each request with it, and knows one installation.
// It makes no call to the real GitHub.
type GitHostFake struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	mu     sync.Mutex
	calls  int
}

// NewGitHostFake starts the fake. It stops when the test ends.
func NewGitHostFake(tb testing.TB) *GitHostFake {
	tb.Helper()

	key, err := rsa.GenerateKey(rand.Reader, rsaBits)
	if err != nil {
		tb.Fatalf("generate the key of the fake GitHub App: %v", err)
	}

	g := &GitHostFake{server: nil, key: key, mu: sync.Mutex{}, calls: 0}
	g.server = httptest.NewUnstartedServer(http.HandlerFunc(g.serve))
	g.server.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	g.server.StartTLS()

	tb.Cleanup(g.server.Close)

	return g
}

// URL is the API address of the fake: an https address on the loopback.
func (g *GitHostFake) URL() string { return g.server.URL }

// PrivateKeyPEM is the private key of the fake app, as GitHub gives it.
func (g *GitHostFake) PrivateKeyPEM() string {
	block := &pem.Block{Type: "RSA PRIVATE KEY", Headers: nil, Bytes: x509.MarshalPKCS1PrivateKey(g.key)}

	return string(pem.EncodeToMemory(block))
}

// Calls is the number of requests that the fake answered.
func (g *GitHostFake) Calls() int {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.calls
}

func (g *GitHostFake) certPool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(g.server.Certificate())

	return pool
}

// verify checks the RS256 signature and the issuer of an app JWT.
func (g *GitHostFake) verify(token string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != jwtParts {
		return false
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}

	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(&g.key.PublicKey, crypto.SHA256, sum[:], sig) != nil {
		return false
	}

	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}

	var claims struct {
		Iss string `json:"iss"`
	}

	return json.Unmarshal(raw, &claims) == nil && claims.Iss == GitHostAppID
}

// writeBody writes the body of an answer.
func writeBody(w http.ResponseWriter, body string) {
	//nolint:errcheck // The client closed the connection: nothing to do.
	_, _ = w.Write([]byte(body))
}

func (g *GitHostFake) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	g.calls++
	g.mu.Unlock()

	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !g.verify(token) {
		w.WriteHeader(http.StatusUnauthorized)

		return
	}

	path := "/app/installations/" + GitHostInstallationID

	w.Header().Set("Content-Type", "application/json")

	switch {
	case r.Method == http.MethodGet && r.URL.Path == path:
		writeBody(w, `{"account":{"id":99,"login":"acme-org"},"permissions":{"contents":"read","metadata":"read"}}`)
	case r.Method == http.MethodPost && r.URL.Path == path+"/access_tokens":
		w.WriteHeader(http.StatusCreated)
		writeBody(w, `{"token":"fake","expires_at":"2099-01-01T00:00:00Z"}`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// gitHostFakes maps a test to its fake, so that the in-process server of that
// test trusts the fake and may reach the loopback.
var gitHostFakes sync.Map //nolint:gochecknoglobals // Test registry, keyed by the test.

// fakeOf returns the fake that the registry holds. The registry holds nothing else.
func fakeOf(v any) *GitHostFake {
	fake, ok := v.(*GitHostFake)
	if !ok {
		return nil
	}

	return fake
}

// NewEnvWithGitHost starts a server as NewEnv does, with the Git host routes
// on, the loopback allowed for Git host connections, and the certificate of
// fake trusted. The server binary variant is not supported.
func NewEnvWithGitHost(ctx context.Context, tb testing.TB, store Store, fake *GitHostFake) *Env {
	tb.Helper()

	gitHostFakes.Store(tb, fake)
	tb.Cleanup(func() { gitHostFakes.Delete(tb) })

	return NewEnv(ctx, tb, NoCLI(), store)
}

// gitHostOptions returns the options that give the in-process server of tb the
// Git host store of st, and the fake of tb when it has one.
func gitHostOptions(tb testing.TB, st serverStore) []dustpan.Option {
	tb.Helper()

	var opts []dustpan.Option

	switch s := st.(type) {
	case *sqlite.Store:
		opts = append(opts, dustpan.WithGitHostStore(s.GitHost()))
	case *postgres.Store:
		opts = append(opts, dustpan.WithGitHostStore(s.GitHost()))
	default:
		return nil
	}

	v, found := gitHostFakes.Load(tb)
	if !found {
		return opts
	}

	if fake := fakeOf(v); fake != nil {
		opts = append(opts,
			dustpan.WithGitHostAllowedRanges(netip.MustParsePrefix("127.0.0.0/8")),
			dustpan.WithGitHostRootCAs(fake.certPool()),
		)
	}

	return opts
}
