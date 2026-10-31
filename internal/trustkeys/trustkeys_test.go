package trustkeys_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
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
	// gate, when set, holds each answer until it is closed.
	gate chan struct{}
}

// newIssuer starts a fake provider with one Ed25519 key with the kid k1.
func newIssuer(t *testing.T) *issuer {
	t.Helper()

	iss := &issuer{
		server: nil, requests: atomic.Int64{}, mu: sync.Mutex{}, discovery: nil, jwks: keySet(t, "k1"), gate: nil,
	}
	iss.discovery = func(base string) (int, string) {
		return http.StatusOK, fmt.Sprintf(`{"issuer":%q,"jwks_uri":%q}`, base, iss.server.URL+"/jwks")
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
	discovery, jwks, gate := iss.discovery, iss.jwks, iss.gate
	iss.mu.Unlock()

	if gate != nil {
		<-gate
	}

	status, body := http.StatusNotFound, ""

	switch {
	case strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration"):
		// An issuer under a path of the server: the discovery document
		// names that issuer, so one server serves many issuers.
		status, body = discovery(iss.server.URL + strings.TrimSuffix(r.URL.Path, "/.well-known/openid-configuration"))
	case r.URL.Path == "/jwks":
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

// TestKeysFetchesOnAContextOfItsOwn checks that a caller who leaves cannot
// leave a failure in the cache: the fetch of a caller whose context ended
// still runs, and the next caller gets the keys with no new fetch.
func TestKeysFetchesOnAContextOfItsOwn(t *testing.T) {
	t.Parallel()

	iss := newIssuer(t)
	f := fetcher(iss, newClock())

	gone, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := f.Keys(gone, iss.server.URL, "k1"); err != nil {
		t.Fatalf("Keys of a caller who left = %v, want the keys", err)
	}

	keys, err := f.Keys(t.Context(), iss.server.URL, "k1")
	if err != nil || !keys.Has("k1") {
		t.Fatalf("Keys after a caller who left = %v, want the key k1", err)
	}

	if got := iss.requests.Load(); got != 2 {
		t.Fatalf("requests = %d, want one fetch", got)
	}
}

// TestKeysKeepsGoodKeysAfterAFailedFetch checks that a failed fetch never
// removes good keys that are younger than KeepFor.
func TestKeysKeepsGoodKeysAfterAFailedFetch(t *testing.T) {
	t.Parallel()

	iss := newIssuer(t)
	c := newClock()
	f := fetcher(iss, c)

	if _, err := f.Keys(t.Context(), iss.server.URL, "k1"); err != nil {
		t.Fatalf("Keys = %v", err)
	}

	iss.mu.Lock()
	iss.discovery = func(string) (int, string) { return http.StatusServiceUnavailable, "" }
	iss.mu.Unlock()

	c.Add(trustkeys.RefreshAfter)

	// An unknown kid makes a fetch, which fails.
	keys, err := f.Keys(t.Context(), iss.server.URL, "unknown")
	if err != nil || !keys.Has("k1") {
		t.Fatalf("Keys after a failed fetch = %v, want the good keys", err)
	}

	if got := iss.requests.Load(); got != 3 {
		t.Fatalf("requests = %d, want the failed fetch of the discovery document", got)
	}

	if keys, err := f.Keys(t.Context(), iss.server.URL, "k1"); err != nil || !keys.Has("k1") {
		t.Fatalf("Keys of k1 after a failed fetch = %v, want the good keys", err)
	}

	c.Add(trustkeys.KeepFor)

	if _, err := f.Keys(t.Context(), iss.server.URL, "k1"); !errors.Is(err, trustrule.ErrKeysUnavailable) {
		t.Fatalf("Keys after KeepFor with a failing issuer = %v, want ErrKeysUnavailable", err)
	}
}

// TestKeysJoinsTheFetchesOfOneIssuer checks that many callers at once make
// one fetch, and that a caller who leaves while it waits gets an error that
// the cache does not keep.
func TestKeysJoinsTheFetchesOfOneIssuer(t *testing.T) {
	t.Parallel()

	const callers = 50

	iss := newIssuer(t)
	iss.gate = make(chan struct{})
	f := fetcher(iss, newClock())

	var (
		wg     sync.WaitGroup
		failed atomic.Int64
	)

	for range callers {
		wg.Go(func() {
			if _, err := f.Keys(t.Context(), iss.server.URL, "k1"); err != nil {
				failed.Add(1)
			}
		})
	}

	// One fetch is now in flight and waits at the gate.
	for iss.requests.Load() == 0 {
		runtime.Gosched()
	}

	gone, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := f.Keys(gone, iss.server.URL, "k1"); !errors.Is(err, context.Canceled) {
		t.Errorf("Keys of a caller who left while it waited = %v, want context.Canceled", err)
	}

	close(iss.gate)
	wg.Wait()

	if got := failed.Load(); got != 0 {
		t.Fatalf("%d of %d callers failed", got, callers)
	}

	if got := iss.requests.Load(); got != 2 {
		t.Fatalf("requests of %d callers = %d, want one fetch (2 requests)", callers, got)
	}
}

// TestKeysRechecksTheIssuer checks the issuer check of the fetcher alone:
// the egress rules allow the loopback, so only the form of the issuer
// refuses it, and the fake gets no request.
func TestKeysRechecksTheIssuer(t *testing.T) {
	t.Parallel()

	iss := newIssuer(t)
	f := fetcher(iss, newClock())

	for _, u := range []string{iss.server.URL + "/?x=1", iss.server.URL + "/#x", "https://user@" +
		strings.TrimPrefix(iss.server.URL, "https://")} {
		if _, err := f.Keys(t.Context(), u, ""); !errors.Is(err, trustrule.ErrKeysUnavailable) {
			t.Errorf("Keys(%s) = %v, want ErrKeysUnavailable", u, err)
		}
	}

	if got := iss.requests.Load(); got != 0 {
		t.Fatalf("the fake got %d requests, want 0", got)
	}
}

// TestKeysNamesTheGuardThatRefused checks each guard of a fetch by the
// reason that it gives. Another guard refuses most of these answers too,
// so the reason shows that this guard acted.
func TestKeysNamesTheGuardThatRefused(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		discovery func(base string) (int, string)
		reason    string
	}{
		{"an http jwks_uri", func(base string) (int, string) {
			return http.StatusOK, fmt.Sprintf(`{"issuer":%q,"jwks_uri":"http://example.com/jwks"}`, base)
		}, "no https jwks_uri"},
		{"a jwks_uri that the rules refuse", func(base string) (int, string) {
			return http.StatusOK, fmt.Sprintf(`{"issuer":%q,"jwks_uri":"https://169.254.169.254/jwks"}`, base)
		}, "jwks_uri:"},
		{"a status other than 200", func(string) (int, string) {
			return http.StatusFound, `{}`
		}, "status other than 200"},
		{"a document over the limit", func(base string) (int, string) {
			return http.StatusOK, fmt.Sprintf(`{"issuer":%q,"jwks_uri":%q}`, base, base+"/jwks") +
				strings.Repeat(" ", trustkeys.MaxDocumentBytes)
		}, "larger than the limit"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			iss := newIssuer(t)
			iss.discovery = tc.discovery

			_, err := fetcher(iss, newClock()).Keys(t.Context(), iss.server.URL, "")
			if !errors.Is(err, trustrule.ErrKeysUnavailable) || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("Keys = %v, want ErrKeysUnavailable with %q", err, tc.reason)
			}
		})
	}
}

// TestKeysHoldsAtMostMaxIssuers checks the cap of the cache: one issuer
// more than MaxIssuers drops the oldest, which then needs a new fetch.
func TestKeysHoldsAtMostMaxIssuers(t *testing.T) {
	t.Parallel()

	iss := newIssuer(t)
	c := newClock()
	f := fetcher(iss, c)

	issuerAt := func(i int) string { return fmt.Sprintf("%s/i/%d", iss.server.URL, i) }

	for i := range trustkeys.MaxIssuers + 1 {
		if _, err := f.Keys(t.Context(), issuerAt(i), "k1"); err != nil {
			t.Fatalf("Keys of issuer %d = %v", i, err)
		}

		// Each fetch is later than the one before, so the first is the
		// oldest.
		c.Add(time.Millisecond)
	}

	before := iss.requests.Load()

	if _, err := f.Keys(t.Context(), issuerAt(trustkeys.MaxIssuers), "k1"); err != nil {
		t.Fatalf("Keys of the last issuer = %v", err)
	}

	if got := iss.requests.Load(); got != before {
		t.Fatalf("the last issuer made %d requests, want 0 from the cache", got-before)
	}

	if _, err := f.Keys(t.Context(), issuerAt(0), "k1"); err != nil {
		t.Fatalf("Keys of the first issuer = %v", err)
	}

	if got := iss.requests.Load(); got != before+2 {
		t.Fatalf("the dropped first issuer made %d requests, want one fetch (2)", got-before)
	}
}
