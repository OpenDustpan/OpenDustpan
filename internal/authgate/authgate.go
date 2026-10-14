// Package authgate wraps the stored-token Authenticator for the HTTP server.
// It does two things:
//
//   - It slows the failed token lookups of each source address. A source
//     that fails more often than its budget allows waits a penalty before
//     each further miss is refused (ADR 53). The gate never refuses a valid
//     token: a token that the next Authenticator accepts always passes, so
//     an attacker behind the same address as other clients, for example a
//     NAT or a load balancer, cannot lock them out. Only lookups that
//     Charge selects use the budget: the server charges values that have
//     the form of a stored-token secret.
//   - It keeps the answer of one request, so the request log and the
//     adapter resolve a token once: one store read, one last-used write and
//     one count of a failure for each request.
//
// The server checks the bootstrap admin tokens (ADR 6) before the gate, so
// the gate never sees them. The root middleware in pkg/dustpan calls
// WithRequest for each request. The key lives here, not in pkg/dustpan,
// because pkg/dustpan imports the adapters. A context without WithRequest
// passes through with no budget and no memo.
package authgate

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
)

// Default budget of failed lookups for each source: a burst of 30, refilled
// at one each 2 seconds (30 a minute), and a penalty of 1 second for each
// miss over the budget.
const (
	DefaultBurst   = 30
	DefaultRefill  = 2 * time.Second
	DefaultPenalty = time.Second
	// DefaultMaxSources bounds the sources that the gate tracks, so a flood
	// of addresses cannot grow its memory without limit.
	DefaultMaxSources = 10000
	// ipv6PrefixBits is the part of an IPv6 address that names one source:
	// a /64 is the smallest network that one site usually gets.
	ipv6PrefixBits = 64
)

// ErrLimited reports a miss from a source that failed too many lookups. It
// wraps identity.ErrUnauthenticated, so every adapter answers 401.
var ErrLimited = fmt.Errorf("%w: too many failed sign-ins from this address, try again later",
	identity.ErrUnauthenticated)

// Config holds the settings of a Gate. Zero values take the defaults.
type Config struct {
	// Next resolves the tokens. Required.
	Next identity.Authenticator
	// Charge reports whether a miss of token uses the budget of its source.
	// Nil charges every miss.
	Charge func(token string) bool
	// Logger logs the first limited miss of a source in each limit period.
	// Nil discards it.
	Logger *slog.Logger
	// Now returns the current time. Nil means time.Now.
	Now func() time.Time
	// Burst is the count of failures that a source can have at once.
	Burst int
	// Refill is the time that gives a source back one failure.
	Refill time.Duration
	// Penalty is how long a miss over the budget waits before its answer.
	Penalty time.Duration
	// MaxSources bounds the tracked sources.
	MaxSources int
}

// Gate is an identity.Authenticator that slows failed lookups for each
// source and keeps the answer of each request.
type Gate struct {
	next       identity.Authenticator
	charge     func(token string) bool
	logger     *slog.Logger
	now        func() time.Time
	sources    map[string]*bucket
	refill     time.Duration
	penalty    time.Duration
	burst      float64
	maxSources int
	mu         sync.Mutex
}

var _ identity.Authenticator = (*Gate)(nil)

// bucket is the failure budget of one source.
type bucket struct {
	at     time.Time
	tokens float64
	logged bool
}

// ErrMissingNext reports a Config without Next.
var ErrMissingNext = errors.New("authgate: missing next authenticator")

// New returns a Gate.
func New(cfg Config) (*Gate, error) {
	if cfg.Next == nil {
		return nil, ErrMissingNext
	}

	g := &Gate{
		next:       cfg.Next,
		charge:     cfg.Charge,
		logger:     cfg.Logger,
		now:        cfg.Now,
		sources:    map[string]*bucket{},
		refill:     positiveOr(cfg.Refill, DefaultRefill),
		penalty:    positiveOr(cfg.Penalty, DefaultPenalty),
		burst:      float64(positiveOr(cfg.Burst, DefaultBurst)),
		maxSources: positiveOr(cfg.MaxSources, DefaultMaxSources),
		mu:         sync.Mutex{},
	}

	if g.charge == nil {
		g.charge = func(string) bool { return true }
	}

	if g.logger == nil {
		g.logger = slog.New(slog.DiscardHandler)
	}

	if g.now == nil {
		g.now = time.Now
	}

	return g, nil
}

// positiveOr returns v, or fallback when v is not above zero.
func positiveOr[T int | time.Duration](v, fallback T) T {
	if v > 0 {
		return v
	}

	return fallback
}

// request is the state of one request: its source and the answer it got.
type request struct {
	source string
	memo   *memo
	mu     sync.Mutex
}

// memo is one answer: the digest of the token and the result.
type memo struct {
	err       error
	principal identity.Principal
	digest    [sha256.Size]byte
}

// key holds the request state in the context.
type key struct{}

// WithRequest returns ctx with the state of one request from the address
// source. Call it once for each request, before any authentication.
func WithRequest(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, key{}, &request{source: sourceKey(source), memo: nil, mu: sync.Mutex{}})
}

// sourceKey returns the budget key of the address source: an IPv4 address
// as it is, an IPv6 address as its /64, and any other text as it is.
func sourceKey(source string) string {
	addr, err := netip.ParseAddr(source)
	if err != nil {
		return source
	}

	addr = addr.Unmap()
	if addr.Is4() {
		return addr.String()
	}

	prefix, err := addr.Prefix(ipv6PrefixBits)
	if err != nil {
		return source
	}

	return prefix.String()
}

// AuthenticateToken implements identity.Authenticator. A token that the
// request resolved before gets the same answer with no lookup. Every other
// token is looked up: a valid token always passes. A charged miss uses one
// unit of the budget of the source, and a charged miss from a source with
// no budget left waits the penalty and answers ErrLimited.
func (g *Gate) AuthenticateToken(ctx context.Context, token string) (identity.Principal, error) {
	req, ok := ctx.Value(key{}).(*request)
	if !ok || req == nil {
		return g.next.AuthenticateToken(ctx, token)
	}

	digest := sha256.Sum256([]byte(token))

	req.mu.Lock()
	defer req.mu.Unlock()

	if req.memo != nil && subtle.ConstantTimeCompare(req.memo.digest[:], digest[:]) == 1 {
		return req.memo.principal, req.memo.err
	}

	p, err := g.lookup(ctx, req.source, token)
	req.memo = &memo{err: err, principal: p, digest: digest}

	return p, err
}

// lookup resolves token for the source and charges a miss to its budget.
func (g *Gate) lookup(ctx context.Context, source, token string) (identity.Principal, error) {
	p, err := g.next.AuthenticateToken(ctx, token)
	if !errors.Is(err, identity.ErrUnauthenticated) || !g.charge(token) {
		return p, err
	}

	if g.fail(ctx, source) {
		return p, err
	}

	g.wait(ctx)

	return identity.Principal{}, ErrLimited
}

// wait sleeps the penalty, or less when ctx ends first.
func (g *Gate) wait(ctx context.Context) {
	timer := time.NewTimer(g.penalty)
	defer timer.Stop()

	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// fail uses one unit of the budget of source. It reports whether the
// source had budget left. It logs the first miss over the budget of each
// limit period, with the source only.
func (g *Gate) fail(ctx context.Context, source string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	b, ok := g.sources[source]
	if !ok {
		g.makeRoomLocked()

		b = &bucket{at: g.now(), tokens: g.burst, logged: false}
		g.sources[source] = b
	}

	g.fillLocked(b)

	if b.tokens >= 1 {
		b.tokens--
		b.logged = false

		return true
	}

	if !b.logged {
		b.logged = true
		g.logger.WarnContext(ctx, "failed sign-ins limited", slog.String("source_ip", source))
	}

	return false
}

// fillLocked adds the budget that b earned since its last change.
func (g *Gate) fillLocked(b *bucket) {
	now := g.now()

	elapsed := now.Sub(b.at)
	if elapsed <= 0 {
		return
	}

	b.tokens += float64(elapsed) / float64(g.refill)
	if b.tokens > g.burst {
		b.tokens = g.burst
	}

	b.at = now
}

// makeRoomLocked removes the sources whose budget is full again. When none
// is full, it removes one source at random: a flood of addresses then
// forgets old sources rather than grow.
func (g *Gate) makeRoomLocked() {
	if len(g.sources) < g.maxSources {
		return
	}

	for source, b := range g.sources {
		g.fillLocked(b)

		if b.tokens >= g.burst {
			delete(g.sources, source)
		}
	}

	for source := range g.sources {
		if len(g.sources) < g.maxSources {
			return
		}

		delete(g.sources, source)
	}
}
