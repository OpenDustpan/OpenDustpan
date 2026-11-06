package headerauth

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"opendustpan.org/opendustpan/internal/egress/egresshttp"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// errRedirect is the error of a key URL that answers with a redirect.
var errRedirect = errors.New("the key URL answered with a redirect")

// refuseRedirects returns a copy of client that follows no redirect. The
// copy shares the transport of client, so the egress rules of the
// transport still apply.
func refuseRedirects(client *http.Client) *http.Client {
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return errRedirect }

	return &c
}

// albKey is one key of the AWS ALB and the time of its read.
type albKey struct {
	at  time.Time
	pub *ecdsa.PublicKey
}

// flight is one read of the key URL. Each request that needs the same keys
// while the read runs waits for it, and gets its result.
type flight struct {
	err  error
	done chan struct{}
	pub  *ecdsa.PublicKey
	kid  string
	set  trustrule.KeySet
}

// keyCache reads and keeps the keys of the issuer. The mutex guards the
// fields. It is never held during a read of the key URL, so a key URL that
// does not answer does not block a sign-in with a key that the cache
// holds.
//
// The cache reads the key URL at most once in KeyRetryAfter, for all keys
// together: a stream of tokens with new key IDs causes one read, not one
// read for each token.
type keyCache struct {
	set      trustrule.KeySet
	setAt    time.Time
	tried    time.Time
	client   *http.Client
	now      func() time.Time
	alb      map[string]albKey
	inflight *flight
	url      string
	timeout  time.Duration
	mu       sync.Mutex
}

// newKeyCache returns an empty cache of the keys at keyURL. The cache
// reads with a copy of client that follows no redirect.
func newKeyCache(client *http.Client, keyURL string, timeout time.Duration, now func() time.Time) *keyCache {
	return &keyCache{
		set: trustrule.KeySet{}, setAt: time.Time{}, tried: time.Time{}, client: refuseRedirects(client), now: now,
		alb: map[string]albKey{}, inflight: nil, url: keyURL, timeout: timeout, mu: sync.Mutex{},
	}
}

// recent reports whether t is less than KeyRetryAfter before now.
func recent(t, now time.Time) bool {
	d := now.Sub(t)

	return !t.IsZero() && d >= 0 && d < KeyRetryAfter
}

// fresh reports whether a key read at t can still be used at now.
func fresh(t, now time.Time) bool {
	d := now.Sub(t)

	return !t.IsZero() && d >= 0 && d < KeyMaxAge
}

// unavailable returns ErrMethodUnavailable with the reason.
func unavailable(reason string) error {
	return fmt.Errorf("%w: %s", session.ErrMethodUnavailable, reason)
}

// start records a new read of the key URL at now. The caller holds the
// mutex.
func (c *keyCache) start(now time.Time, kid string) *flight {
	f := &flight{err: nil, done: make(chan struct{}), pub: nil, kid: kid, set: trustrule.KeySet{}}
	c.tried, c.inflight = now, f

	return f
}

// finish ends the read f and wakes its waiters. The caller holds the
// mutex.
func (c *keyCache) finish(f *flight) {
	c.inflight = nil
	close(f.done)
}

// wait waits for the read f, or for the end of ctx.
func wait(ctx context.Context, f *flight) error {
	select {
	case <-f.done:
		return f.err
	case <-ctx.Done():
		return unavailable("the read of the keys of the issuer did not end in time")
	}
}

// jwks returns the key set of the JWKS URL. It reads the URL when the
// cached set is older than KeyMaxAge, or has no key with the ID kid, and
// no read started less than KeyRetryAfter ago. It returns
// ErrMethodUnavailable when it has no usable set.
func (c *keyCache) jwks(ctx context.Context, kid string) (trustrule.KeySet, error) {
	c.mu.Lock()

	now := c.now()
	isFresh := fresh(c.setAt, now)

	if isFresh && (kid == "" || c.set.Has(kid)) {
		set := c.set
		c.mu.Unlock()

		return set, nil
	}

	if f := c.inflight; f != nil {
		c.mu.Unlock()

		if err := wait(ctx, f); err != nil {
			return trustrule.KeySet{}, err
		}

		return f.set, nil
	}

	if recent(c.tried, now) {
		set := c.set
		c.mu.Unlock()

		if isFresh {
			// The token names a key that the set does not hold; the check of
			// the signature refuses it.
			return set, nil
		}

		return trustrule.KeySet{}, unavailable("the keys of the issuer were not read")
	}

	f := c.start(now, "")
	c.mu.Unlock()

	set, err := c.readJWKS(ctx)

	c.mu.Lock()
	defer c.mu.Unlock()

	f.set, f.err = set, err
	if err == nil {
		c.set, c.setAt = set, c.now()
	}

	c.finish(f)

	return set, err
}

// readJWKS reads and parses the JWKS URL.
func (c *keyCache) readJWKS(ctx context.Context) (trustrule.KeySet, error) {
	body, err := c.get(ctx, c.url)
	if err != nil {
		return trustrule.KeySet{}, err
	}

	set, err := trustrule.ParseKeySet(body)
	if err != nil {
		return trustrule.KeySet{}, unavailable("the key set of the issuer is not valid")
	}

	return set, nil
}

// albKeyOf returns the key of the AWS ALB with the ID kid. It reads the key
// URL of kid when the cache does not hold a fresh key of kid, and no read
// started less than KeyRetryAfter ago.
func (c *keyCache) albKeyOf(ctx context.Context, kid string) (*ecdsa.PublicKey, error) {
	u, err := ALBKeyURL(c.url, kid)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()

	now := c.now()

	if k, ok := c.alb[kid]; ok && fresh(k.at, now) {
		c.mu.Unlock()

		return k.pub, nil
	}

	if f := c.inflight; f != nil {
		c.mu.Unlock()

		if f.kid != kid {
			return nil, unavailable("the key URL is busy with another key")
		}

		if waitErr := wait(ctx, f); waitErr != nil {
			return nil, waitErr
		}

		return f.pub, nil
	}

	if recent(c.tried, now) {
		c.mu.Unlock()

		return nil, unavailable("the key URL was read less than the retry time ago")
	}

	// Limit the memory of the cache: a full map starts again.
	if len(c.alb) >= maxALBKeys {
		clear(c.alb)
	}

	f := c.start(now, kid)
	c.mu.Unlock()

	pub, err := c.readALBKey(ctx, u)

	c.mu.Lock()
	defer c.mu.Unlock()

	f.pub, f.err = pub, err
	if err == nil {
		c.alb[kid] = albKey{at: c.now(), pub: pub}
	}

	c.finish(f)

	return pub, err
}

// readALBKey reads and parses the key at u.
func (c *keyCache) readALBKey(ctx context.Context, u string) (*ecdsa.PublicKey, error) {
	body, err := c.get(ctx, u)
	if err != nil {
		return nil, err
	}

	return ParseALBKey(body)
}

// ParseALBKey parses a key of the AWS ALB: one PEM block "PUBLIC KEY" that
// holds an ECDSA key on P-256. It returns ErrMethodUnavailable for each
// other answer.
func ParseALBKey(body []byte) (*ecdsa.PublicKey, error) {
	errKey := unavailable("the key of the load balancer is not a P-256 public key")

	block, rest := pem.Decode(body)
	if block == nil || block.Type != "PUBLIC KEY" || len(bytes.TrimSpace(rest)) != 0 || len(block.Headers) != 0 {
		return nil, errKey
	}

	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, errKey
	}

	pub, ok := parsed.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		return nil, errKey
	}

	return pub, nil
}

// get reads the body of u with one GET, in the timeout of the cache. The
// read does not end with the request that started it, because other
// requests can wait for it; the timeout bounds it. The error text names no
// address.
func (c *keyCache) get(ctx context.Context, u string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, unavailable("the key URL is not valid")
	}

	resp, err := c.client.Do(req)
	if err != nil {
		// The text does not tell a refused address or a redirect from an
		// unreachable host.
		return nil, unavailable("the key URL did not answer")
	}

	body, readErr := egresshttp.ReadBody(resp)
	if closeErr := resp.Body.Close(); closeErr != nil && readErr == nil {
		readErr = closeErr
	}

	// Defense in depth: the client of the cache follows no redirect. This
	// check stays so that a client that follows one cannot give keys from
	// another URL.
	if resp.Request == nil || resp.Request.URL == nil || resp.Request.URL.String() != u {
		return nil, unavailable("the answer does not come from the key URL")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"%w: the key URL answered with the status %d",
			session.ErrMethodUnavailable,
			resp.StatusCode,
		)
	}

	if readErr != nil {
		return nil, unavailable("the answer of the key URL could not be read")
	}

	return body, nil
}
