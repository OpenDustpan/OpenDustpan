// Package trustkeys reads the signing keys of the issuer of a workload trust
// rule with the key source discovery. It reads the OpenID Connect discovery
// document of the issuer and the JWKS that it names, through the egress rules
// of the server, and keeps them in a cache with a limit.
package trustkeys

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"opendustpan.org/opendustpan/internal/egress/egresshttp"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// Limits of the fetcher.
const (
	// MaxDocumentBytes is the largest discovery document or JWKS that the
	// fetcher reads.
	MaxDocumentBytes = 256 << 10
	// FetchTimeout bounds one fetch: the discovery document and the JWKS.
	FetchTimeout = 10 * time.Second
	// KeepFor is how long the fetcher keeps the keys of an issuer.
	KeepFor = 5 * time.Minute
	// RefreshAfter is the shortest time between two fetches for one issuer,
	// also when a token names a kid that the cached keys do not hold. So a
	// caller who sends tokens with new kids cannot make the server fetch
	// more than once in this time.
	RefreshAfter = 30 * time.Second
	// MaxIssuers is the most issuers that the cache holds. The oldest entry
	// goes first.
	MaxIssuers = 256
	// discoveryPath is the path of the discovery document under an issuer.
	discoveryPath = "/.well-known/openid-configuration"
)

// Config holds the settings of a Fetcher.
type Config struct {
	// Rules are the egress rules. The zero value allows public addresses
	// only.
	Rules egress.Rules
	// RootCAs holds the certificate authorities that the fetcher trusts.
	// Nil means the system roots. A test sets it for a local TLS server.
	RootCAs *x509.CertPool
	// Now is the clock of the cache. Nil means time.Now.
	Now func() time.Time
}

// Fetcher reads the keys of an issuer and keeps them. It implements
// trustrule.KeyFetcher. It is safe for concurrent use.
type Fetcher struct {
	client *http.Client
	rules  egress.Rules
	now    func() time.Time
	mu     sync.Mutex
	cache  map[string]entry
	// flights holds a channel for each issuer with a fetch that runs. The
	// fetch closes it when it ends. Each other caller for that issuer waits
	// on it, so one issuer has at most one fetch at a time.
	flights map[string]chan struct{}
}

var _ trustrule.KeyFetcher = (*Fetcher)(nil)

// entry is what the fetcher knows of one issuer. triedAt is the time of the
// last fetch, good or failed. keys and keysAt are the keys of the last good
// fetch; keysAt is zero before the first one. err is the failure of the last
// fetch and is nil after a good one. A failure never removes good keys: the
// fetcher serves them until they are KeepFor old. The fetcher keeps a failure
// too, so a bad issuer costs at most one fetch in RefreshAfter.
type entry struct {
	triedAt time.Time
	keysAt  time.Time
	keys    trustrule.KeySet
	err     error
}

// New returns a Fetcher.
func New(cfg Config) *Fetcher {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	client := egresshttp.ClientWith(cfg.Rules, egress.NoRedirect, egress.HTTPSOnly,
		egresshttp.Options{RootCAs: cfg.RootCAs, Resolver: nil})

	return &Fetcher{
		client: client, rules: cfg.Rules, now: now, mu: sync.Mutex{}, cache: map[string]entry{},
		flights: map[string]chan struct{}{},
	}
}

// Keys returns the signing keys of issuer. It serves the cached keys while
// they are younger than KeepFor and hold kid (an empty kid needs no key). Else
// it fetches again, at most once in RefreshAfter for each issuer, and with at
// most one fetch at a time for each issuer.
//
// The fetch does not end when ctx ends: it runs to its own FetchTimeout, so
// a caller who closes its request cannot leave a failure in the cache. A
// caller whose ctx ends while it waits gets the error of ctx, and the cache
// does not keep it.
func (f *Fetcher) Keys(ctx context.Context, issuer, kid string) (trustrule.KeySet, error) {
	if err := trustrule.ValidateDiscoveryIssuer(issuer); err != nil {
		// Defense in depth: the rule passed the same check when a person
		// saved it. This check stays so that a rule of another version never
		// sends a request to an address that the rules refuse.
		return trustrule.KeySet{}, fmt.Errorf("%w: %w", trustrule.ErrKeysUnavailable, err)
	}

	for {
		f.mu.Lock()

		if wait, ok := f.flights[issuer]; ok {
			f.mu.Unlock()

			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return trustrule.KeySet{}, fmt.Errorf("%w: wait for the fetch: %w", trustrule.ErrKeysUnavailable,
					context.Cause(ctx))
			}
		}

		now := f.now()
		cached, ok := f.cache[issuer]

		if ok && !mustFetch(cached, now, kid) {
			f.mu.Unlock()

			return cached.answer(now)
		}

		done := make(chan struct{})
		f.flights[issuer] = done
		f.mu.Unlock()

		e := f.fly(context.WithoutCancel(ctx), issuer, now, cached, done)

		return e.answer(now)
	}
}

// fly runs the fetch of the flight done for issuer and records its outcome.
// It ends the flight also when the fetch panics, so no caller waits for ever.
func (f *Fetcher) fly(ctx context.Context, issuer string, now time.Time, prev entry, done chan struct{}) entry {
	defer close(done)

	ended := false

	defer func() {
		if !ended {
			f.mu.Lock()
			delete(f.flights, issuer)
			f.mu.Unlock()
		}
	}()

	keys, err := f.fetch(ctx, issuer)
	e := f.record(issuer, now, prev, keys, err)
	ended = true

	return e
}

// mustFetch reports whether e cannot answer at now for kid without a fetch.
func mustFetch(e entry, now time.Time, kid string) bool {
	switch {
	case now.Sub(e.triedAt) < RefreshAfter:
		return false
	case !e.hasKeys(now):
		return true
	default:
		return kid != "" && !e.keys.Has(kid)
	}
}

// hasKeys reports whether e holds good keys that are younger than KeepFor
// at now.
func (e *entry) hasKeys(now time.Time) bool {
	return !e.keysAt.IsZero() && now.Sub(e.keysAt) < KeepFor
}

// answer returns the keys of e at now, or the failure of its last fetch when
// it holds no good keys.
func (e *entry) answer(now time.Time) (trustrule.KeySet, error) {
	if e.hasKeys(now) {
		return e.keys, nil
	}

	if e.err != nil {
		return trustrule.KeySet{}, e.err
	}

	return trustrule.KeySet{}, fmt.Errorf("%w: the keys are older than %s", trustrule.ErrKeysUnavailable, KeepFor)
}

// record keeps the outcome of the fetch that started at now for issuer, ends
// the flight of issuer, and returns the new entry. prev is the entry before
// the fetch. A failure keeps the good keys of prev.
func (f *Fetcher) record(issuer string, now time.Time, prev entry, keys trustrule.KeySet, err error) entry {
	e := prev
	e.triedAt = now
	e.err = err

	if err == nil {
		e.keys, e.keysAt = keys, now
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	delete(f.flights, issuer)
	f.store(issuer, e)

	return e
}

// store keeps e for issuer, and drops the entry of the oldest fetch when the
// cache is full. The caller holds f.mu.
func (f *Fetcher) store(issuer string, e entry) {
	if _, ok := f.cache[issuer]; !ok && len(f.cache) >= MaxIssuers {
		oldest := ""

		for k, v := range f.cache {
			if oldest == "" || v.triedAt.Before(f.cache[oldest].triedAt) {
				oldest = k
			}
		}

		delete(f.cache, oldest)
	}

	f.cache[issuer] = e
}

// fetch reads the discovery document of issuer and the JWKS that it names.
func (f *Fetcher) fetch(ctx context.Context, issuer string) (trustrule.KeySet, error) {
	ctx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()

	doc, err := f.get(ctx, strings.TrimSuffix(issuer, "/")+discoveryPath)
	if err != nil {
		return trustrule.KeySet{}, fmt.Errorf("%w: discovery document: %w", trustrule.ErrKeysUnavailable, err)
	}

	jwksURI, err := checkDiscovery(doc, issuer)
	if err != nil {
		return trustrule.KeySet{}, fmt.Errorf("%w: discovery document: %w", trustrule.ErrKeysUnavailable, err)
	}

	// Defense in depth: the client checks each URL again before it sends.
	// This check stays so that a jwks_uri that the rules refuse fails here,
	// with its own reason.
	if urlErr := f.rules.CheckURL(jwksURI, egress.HTTPSOnly); urlErr != nil {
		return trustrule.KeySet{}, fmt.Errorf("%w: jwks_uri: %w", trustrule.ErrKeysUnavailable, urlErr)
	}

	raw, err := f.get(ctx, jwksURI)
	if err != nil {
		return trustrule.KeySet{}, fmt.Errorf("%w: JWKS: %w", trustrule.ErrKeysUnavailable, err)
	}

	keys, err := trustrule.ParseKeySet(raw)
	if err != nil {
		return trustrule.KeySet{}, fmt.Errorf("%w: JWKS: %w", trustrule.ErrKeysUnavailable, err)
	}

	return keys, nil
}

// Errors of a fetch. The error of a fetch goes to the audit log, never to
// the caller of the exchange.
var (
	errStatus   = errors.New("the issuer answered a status other than 200")
	errTooLarge = errors.New("the answer is larger than the limit")
	errIssuer   = errors.New("the issuer of the discovery document is not the issuer of the rule")
	errJWKSURI  = errors.New("the discovery document has no https jwks_uri")
)

// get reads the body of a GET of u, at most MaxDocumentBytes. It refuses an
// answer with a status other than 200.
func (f *Fetcher) get(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, MaxDocumentBytes+1))
	closeErr := resp.Body.Close()

	switch {
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("%w: %d", errStatus, resp.StatusCode)
	case readErr != nil:
		return nil, fmt.Errorf("read body: %w", readErr)
	case closeErr != nil:
		return nil, fmt.Errorf("close body: %w", closeErr)
	case len(body) > MaxDocumentBytes:
		return nil, fmt.Errorf("%w: %d bytes", errTooLarge, MaxDocumentBytes)
	default:
		return body, nil
	}
}

// checkDiscovery returns the jwks_uri of the discovery document raw. The
// issuer of the document must be issuer, compared exactly (OpenID Connect
// Discovery 1.0, section 4.3).
func checkDiscovery(raw []byte, issuer string) (string, error) {
	var members map[string]json.RawMessage

	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&members); err != nil {
		return "", fmt.Errorf("not a JSON object: %w", err)
	}

	var got, jwksURI string

	if err := json.Unmarshal(members["issuer"], &got); err != nil || got != issuer {
		return "", errIssuer
	}

	if err := json.Unmarshal(members["jwks_uri"], &jwksURI); err != nil || !strings.HasPrefix(jwksURI, "https://") {
		return "", errJWKSURI
	}

	return jwksURI, nil
}
