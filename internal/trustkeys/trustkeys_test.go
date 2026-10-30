package trustkeys_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/trustkeys"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// issuer is a fake OpenID provider on a TLS server on the loopback. Its
// answers can change during a test.
type issuer struct {
	server   *httptest.Server
	requests atomic.Int64
	mu       sync.Mutex
	// discovery returns the discovery document, from the URL of the server.
	discovery func(base string) (int, string)
	jwks      string
}

// newIssuer starts a fake provider with one Ed25519 key with the kid k1.
func newIssuer(t *testing.T) *issuer {
	t.Helper()

	iss := &issuer{server: nil, requests: atomic.Int64{}, mu: sync.Mutex{}, discovery: nil, jwks: keySet(t, "k1")}
	iss.discovery = func(base string) (int, string) {
		return http.StatusOK, fmt.Sprintf(`{"issuer":%q,"jwks_uri":%q}`, base, base+"/jwks")
	}

	iss.server = httptest.NewUnstartedServer(http.HandlerFunc(iss.serve))
	iss.server.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	iss.server.StartTLS()
	t.Cleanup(iss.server.Close)

	return iss
}

func (iss *issuer) serve(w http.ResponseWriter, r *http.Request) {
	iss.requests.Add(1)

	iss.mu.Lock()
	discovery, jwks := iss.discovery, iss.jwks
	iss.mu.Unlock()

	status, body := http.StatusNotFound, ""

	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		status, body = discovery(iss.server.URL)
	case "/jwks":
		status, body = http.StatusOK, jwks
	default:
	}

	w.WriteHeader(status)

	if _, err := w.Write([]byte(body)); err != nil {
		return
	}
}

// pool trusts the certificate of the fake.
func (iss *issuer) pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(iss.server.Certificate())

	return p
}

// keySet returns a JWKS with one new Ed25519 key with the ID kid.
func keySet(t *testing.T, kid string) string {
	t.Helper()

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	return fmt.Sprintf(`{"keys":[{"kty":"OKP","crv":"Ed25519","kid":%q,"x":%q}]}`, kid,
		base64.RawURLEncoding.EncodeToString(pub))
}

// clock is a clock that a test moves.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

// fetcher returns a fetcher that may reach the loopback and trusts iss.
func fetcher(iss *issuer, c *clock) *trustkeys.Fetcher {
	return trustkeys.New(trustkeys.Config{
		Rules:   egress.Rules{Allow: nil, LoopbackForTests: true},
		RootCAs: iss.pool(),
		Now:     c.Now,
	})
}

func newClock() *clock {
	return &clock{mu: sync.Mutex{}, now: time.Unix(1_700_000_000, 0)}
}

func TestKeysReadsTheDiscoveryDocumentAndTheJWKS(t *testing.T) {
	t.Parallel()

	iss := newIssuer(t)
	f := fetcher(iss, newClock())

	keys, err := f.Keys(t.Context(), iss.server.URL, "k1")
	if err != nil {
		t.Fatalf("Keys = %v", err)
	}

	if keys.Len() != 1 || !keys.Has("k1") {
		t.Fatalf("keys = %d, want the key k1", keys.Len())
	}

	if got := iss.requests.Load(); got != 2 {
		t.Errorf("requests = %d, want 2", got)
	}
}

// TestKeysNeverReachesARefusedAddress checks the egress rules of the
// fetcher: with the rules of a server, a loopback issuer, a cloud metadata
// issuer and a private issuer get no request. This is the guard against a
// request forgery through the issuer of a rule.
func TestKeysNeverReachesARefusedAddress(t *testing.T) {
	t.Parallel()

	iss := newIssuer(t)
	f := trustkeys.New(
		trustkeys.Config{Rules: egress.Rules{Allow: nil, LoopbackForTests: false}, RootCAs: iss.pool(), Now: nil},
	)

	for _, u := range []string{iss.server.URL, "https://169.254.169.254", "https://10.0.0.1", "http://example.com"} {
		if _, err := f.Keys(t.Context(), u, ""); !errors.Is(err, trustrule.ErrKeysUnavailable) {
			t.Errorf("Keys(%s) = %v, want ErrKeysUnavailable", u, err)
		}
	}

	if got := iss.requests.Load(); got != 0 {
		t.Fatalf("the fake got %d requests, want 0", got)
	}
}

// TestKeysRefusesAJWKSURIThatTheRulesRefuse checks that a discovery document
// cannot send the fetcher to an address that the rules refuse, also when the
// issuer itself is allowed.
func TestKeysRefusesAJWKSURIThatTheRulesRefuse(t *testing.T) {
	t.Parallel()

	for _, jwksURI := range []string{
		"https://169.254.169.254/latest/meta-data", "http://example.com/jwks", "https://10.0.0.1/jwks", "file:///etc/passwd",
	} {
		iss := newIssuer(t)
		iss.discovery = func(base string) (int, string) {
			return http.StatusOK, fmt.Sprintf(`{"issuer":%q,"jwks_uri":%q}`, base, jwksURI)
		}

		if _, err := fetcher(iss, newClock()).Keys(t.Context(), iss.server.URL, ""); !errors.Is(
			err, trustrule.ErrKeysUnavailable,
		) {
			t.Errorf("jwks_uri %s: Keys = %v, want ErrKeysUnavailable", jwksURI, err)
		}

		if got := iss.requests.Load(); got != 1 {
			t.Errorf("jwks_uri %s: requests = %d, want only the discovery document", jwksURI, got)
		}
	}
}

func TestKeysRefusesABadDiscoveryDocument(t *testing.T) {
	t.Parallel()

	cases := map[string]func(base string) (int, string){
		"another issuer": func(base string) (int, string) {
			return http.StatusOK, fmt.Sprintf(`{"issuer":%q,"jwks_uri":%q}`, base+"/other", base+"/jwks")
		},
		"a redirect": func(string) (int, string) { return http.StatusFound, "" },
		"an error":   func(string) (int, string) { return http.StatusInternalServerError, "" },
		"not JSON":   func(string) (int, string) { return http.StatusOK, "issuer" },
		"too large": func(base string) (int, string) {
			return http.StatusOK, fmt.Sprintf(`{"issuer":%q,"jwks_uri":%q,"x":%q}`, base, base+"/jwks",
				strings.Repeat("a", trustkeys.MaxDocumentBytes))
		},
	}

	for name, discovery := range cases {
		iss := newIssuer(t)
		iss.discovery = discovery

		if _, err := fetcher(iss, newClock()).Keys(t.Context(), iss.server.URL, ""); !errors.Is(
			err, trustrule.ErrKeysUnavailable,
		) {
			t.Errorf("%s: Keys = %v, want ErrKeysUnavailable", name, err)
		}
	}
}

// TestKeysKeepsAndRefreshesWithALimit checks the cache: a second call reads
// no key, a new kid makes at most one fetch in RefreshAfter, and a failure
// is kept too.
func TestKeysKeepsAndRefreshesWithALimit(t *testing.T) {
	t.Parallel()

	iss := newIssuer(t)
	c := newClock()
	f := fetcher(iss, c)

	if _, err := f.Keys(t.Context(), iss.server.URL, "k1"); err != nil {
		t.Fatalf("Keys = %v", err)
	}

	for range 5 {
		if _, err := f.Keys(t.Context(), iss.server.URL, "unknown"); err != nil {
			t.Fatalf("Keys = %v", err)
		}
	}

	if got := iss.requests.Load(); got != 2 {
		t.Fatalf("requests after unknown kids inside RefreshAfter = %d, want 2", got)
	}

	iss.mu.Lock()
	iss.jwks = keySet(t, "k2")
	iss.mu.Unlock()

	c.Add(trustkeys.RefreshAfter)

	keys, err := f.Keys(t.Context(), iss.server.URL, "k2")
	if err != nil || !keys.Has("k2") {
		t.Fatalf("Keys after RefreshAfter = %v, want the new key k2", err)
	}

	if got := iss.requests.Load(); got != 4 {
		t.Fatalf("requests = %d, want 4", got)
	}

	// A known kid needs no fetch until KeepFor.
	c.Add(trustkeys.RefreshAfter)

	if _, err := f.Keys(t.Context(), iss.server.URL, "k2"); err != nil {
		t.Fatalf("Keys = %v", err)
	}

	if got := iss.requests.Load(); got != 4 {
		t.Fatalf("requests for a known kid = %d, want 4", got)
	}
}
