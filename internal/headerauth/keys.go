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
	"unicode"

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

// albKey is one key of the AWS ALB and the time of its last use. A key ID
// of the AWS ALB always names the same key, so the key does not expire.
type albKey struct {
	used time.Time
	pub  *ecdsa.PublicKey
}

// flight is one read of the key URL. Each request that needs the read waits
// for it with its own context, and gets its result.
type flight struct {
	err  error
	done chan struct{}
	pub  *ecdsa.PublicKey
	set  trustrule.KeySet
}

// keyCache reads and keeps the keys of the issuer. The mutex guards the
// fields. It is never held during a read of the key URL: a read runs in its
// own goroutine, so a key URL that does not answer does not block a sign-in
// with a key that the cache holds.
//
// A JWKS URL is read at most once in KeyRetryAfter. New key IDs of the AWS
// ALB are read at most newALBKeyReads times in KeyRetryAfter, together, and
// a key ID that the cache holds is never read again.
type keyCache struct {
	set      trustrule.KeySet
	setAt    time.Time
	tried    time.Time
	client   *http.Client
	now      func() time.Time
	alb      map[string]albKey
	flights  map[string]*flight
	albReads []time.Time
	url      string
	timeout  time.Duration
	mu       sync.Mutex
}

// newKeyCache returns an empty cache of the keys at keyURL. The cache
// reads with a copy of client that follows no redirect.
func newKeyCache(client *http.Client, keyURL string, timeout time.Duration, now func() time.Time) *keyCache {
	return &keyCache{
		set: trustrule.KeySet{}, setAt: time.Time{}, tried: time.Time{}, client: refuseRedirects(client), now: now,
		alb: map[string]albKey{}, flights: map[string]*flight{}, albReads: nil, url: keyURL, timeout: timeout,
		mu: sync.Mutex{},
	}
}

// recent reports whether t is less than KeyRetryAfter before now.
func recent(t, now time.Time) bool {
	d := now.Sub(t)

	return !t.IsZero() && d >= 0 && d < KeyRetryAfter
}

// fresh reports whether a key set read at t can still be used at now.
func fresh(t, now time.Time) bool {
	d := now.Sub(t)

	return !t.IsZero() && d >= 0 && d < KeyMaxAge
}

// unavailable returns ErrMethodUnavailable with the reason.
func unavailable(reason string) error {
	return fmt.Errorf("%w: %s", session.ErrMethodUnavailable, reason)
}

// launch starts the read of key in its own goroutine and returns its
// flight. The read runs with the values of ctx but not its end, because
// other requests can wait for it; the timeout of the cache bounds it. When
// the read ends, store runs under the mutex, and the flight ends, also
// after a panic of the read. The caller holds the mutex.
func (c *keyCache) launch(ctx context.Context, key string, read func(ctx context.Context) (flight, error),
	store func(r flight),
) *flight {
	f := &flight{err: nil, done: make(chan struct{}), pub: nil, set: trustrule.KeySet{}}
	c.flights[key] = f
	readCtx := context.WithoutCancel(ctx)

	go func() {
		var (
			r   flight
			err error
		)

		defer func() {
			if p := recover(); p != nil {
				// Fail closed: a read that panics gives no keys, and the
				// flight ends, so that no request waits for it forever.
				r, err = flight{
					err:  nil,
					done: nil,
					pub:  nil,
					set:  trustrule.KeySet{},
				}, unavailable(
					"the read of the keys failed",
				)
			}

			c.mu.Lock()
			defer c.mu.Unlock()

			f.set, f.pub, f.err = r.set, r.pub, err
			if err == nil {
				store(r)
			}

			delete(c.flights, key)
			close(f.done)
		}()

		r, err = read(readCtx)
	}()

	return f
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
// ErrMethodUnavailable when it has no usable set. A failed read is kept for
// KeyRetryAfter: when the set expires and its read fails, each sign-in of
// the provider fails until the next read.
func (c *keyCache) jwks(ctx context.Context, kid string) (trustrule.KeySet, error) {
	c.mu.Lock()

	now := c.now()
	isFresh := fresh(c.setAt, now)

	if isFresh && (kid == "" || c.set.Has(kid)) {
		set := c.set
		c.mu.Unlock()

		return set, nil
	}

	f := c.flights[""]
	if f == nil {
		if recent(c.tried, now) {
			set := c.set
			c.mu.Unlock()

			if isFresh {
				// The token names a key that the set does not hold; the check
				// of the signature refuses it.
				return set, nil
			}

			return trustrule.KeySet{}, unavailable("the keys of the issuer were not read")
		}

		c.tried = now
		f = c.launch(ctx, "", c.readJWKS, func(r flight) { c.set, c.setAt = r.set, c.now() })
	}

	c.mu.Unlock()

	if err := wait(ctx, f); err != nil {
		return trustrule.KeySet{}, err
	}

	return f.set, nil
}

// readJWKS reads and parses the JWKS URL.
func (c *keyCache) readJWKS(ctx context.Context) (flight, error) {
	none := flight{err: nil, done: nil, pub: nil, set: trustrule.KeySet{}}

	body, err := c.get(ctx, c.url)
	if err != nil {
		return none, err
	}

	set, err := trustrule.ParseKeySet(body)
	if err != nil {
		return none, unavailable("the key set of the issuer is not valid")
	}

	none.set = set

	return none, nil
}

// allowNewALBKey reports whether the cache can read a new key ID of the
// AWS ALB at now, and records the read. The caller holds the mutex.
func (c *keyCache) allowNewALBKey(now time.Time) bool {
	kept := c.albReads[:0]

	for _, t := range c.albReads {
		if recent(t, now) {
			kept = append(kept, t)
		}
	}

	c.albReads = kept

	if len(c.albReads) >= newALBKeyReads {
		return false
	}

	c.albReads = append(c.albReads, now)

	return true
}

// storeALBKey keeps the key pub of kid. A full cache drops the key that was
// used least recently, so that the key in use stays. The caller holds the
// mutex.
func (c *keyCache) storeALBKey(kid string, pub *ecdsa.PublicKey) {
	if len(c.alb) >= maxALBKeys {
		oldest := ""

		for id, k := range c.alb {
			if oldest == "" || k.used.Before(c.alb[oldest].used) {
				oldest = id
			}
		}

		delete(c.alb, oldest)
	}

	c.alb[kid] = albKey{used: c.now(), pub: pub}
}

// albKeyOf returns the key of the AWS ALB with the ID kid. It reads the key
// URL of kid when the cache does not hold kid, within the budget of new key
// IDs.
func (c *keyCache) albKeyOf(ctx context.Context, kid string) (*ecdsa.PublicKey, error) {
	u, err := ALBKeyURL(c.url, kid)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()

	now := c.now()

	if k, ok := c.alb[kid]; ok {
		k.used = now
		c.alb[kid] = k
		c.mu.Unlock()

		return k.pub, nil
	}

	f := c.flights[kid]
	if f == nil {
		if !c.allowNewALBKey(now) {
			c.mu.Unlock()

			return nil, unavailable("too many new key IDs of the load balancer were read in the retry time")
		}

		read := func(ctx context.Context) (flight, error) {
			pub, err := c.readALBKey(ctx, u)

			return flight{err: nil, done: nil, pub: pub, set: trustrule.KeySet{}}, err
		}
		f = c.launch(ctx, kid, read, func(r flight) { c.storeALBKey(kid, r.pub) })
	}

	c.mu.Unlock()

	if err := wait(ctx, f); err != nil {
		return nil, err
	}

	return f.pub, nil
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

	// Reject data before the block: pem.Decode would discard it, and with it
	// a malformed block that precedes a valid one.
	trimmed := bytes.TrimLeftFunc(body, unicode.IsSpace)
	if !bytes.HasPrefix(trimmed, []byte("-----BEGIN ")) {
		return nil, errKey
	}

	block, rest := pem.Decode(body)
	if block == nil || block.Type != "PUBLIC KEY" || len(bytes.TrimSpace(rest)) != 0 || len(block.Headers) != 0 {
		return nil, errKey
	}

	// The rest after the block is only white space, so each other block
	// start in the body is a block before this one.
	if bytes.Count(trimmed, []byte("-----BEGIN ")) != 1 {
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
