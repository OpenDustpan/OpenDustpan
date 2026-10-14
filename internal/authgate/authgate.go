// Package authgate wraps an identity.Authenticator for the HTTP server. It
// does two things:
//
//   - It limits the failed token lookups of each source address. A source
//     that fails too often gets ErrUnauthenticated with no lookup until its
//     budget refills, so a guess costs no store read (ADR 53).
//   - It keeps the answer of one request, so the request log and the adapter
//     resolve a token once: one store read, one last-used write and one
//     count of a failure for each request.
//
// The root middleware in pkg/dustpan calls WithRequest for each request.
// The key lives here, not in pkg/dustpan, because pkg/dustpan imports the
// adapters. A context without WithRequest passes through with no limit and
// no memo.
package authgate

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
)

// Default budget of failed lookups for each source: a burst of 30, refilled
// at one each 2 seconds (30 a minute).
const (
	DefaultBurst  = 30
	DefaultRefill = 2 * time.Second
	// DefaultMaxSources bounds the sources that the gate tracks, so a flood
	// of addresses cannot grow its memory without limit.
	DefaultMaxSources = 10000
)

// ErrLimited reports a source that failed too many lookups. It wraps
// identity.ErrUnauthenticated, so every adapter answers 401.
var ErrLimited = fmt.Errorf("%w: too many failed sign-ins from this address, try again later",
	identity.ErrUnauthenticated)

// Config holds the settings of a Gate. Zero values take the defaults.
type Config struct {
	// Next resolves the tokens. Required.
	Next identity.Authenticator
	// Logger logs the first refusal of a source in each limit period. Nil
	// discards it.
	Logger *slog.Logger
	// Now returns the current time. Nil means time.Now.
	Now func() time.Time
	// Burst is the count of failures that a source can have at once.
	Burst int
	// Refill is the time that gives a source back one failure.
	Refill time.Duration
	// MaxSources bounds the tracked sources.
	MaxSources int
}

// Gate is an identity.Authenticator that limits failed lookups for each
// source and keeps the answer of each request.
type Gate struct {
	next       identity.Authenticator
	logger     *slog.Logger
	now        func() time.Time
	sources    map[string]*bucket
	refill     time.Duration
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
		logger:     cfg.Logger,
		now:        cfg.Now,
		sources:    map[string]*bucket{},
		refill:     cfg.Refill,
		burst:      float64(cfg.Burst),
		maxSources: cfg.MaxSources,
		mu:         sync.Mutex{},
	}

	if g.logger == nil {
		g.logger = slog.New(slog.DiscardHandler)
	}

	if g.now == nil {
		g.now = time.Now
	}

	if g.refill <= 0 {
		g.refill = DefaultRefill
	}

	if g.burst <= 0 {
		g.burst = DefaultBurst
	}

	if g.maxSources <= 0 {
		g.maxSources = DefaultMaxSources
	}

	return g, nil
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
	return context.WithValue(ctx, key{}, &request{source: source, memo: nil, mu: sync.Mutex{}})
}

// AuthenticateToken implements identity.Authenticator. A token that the
// request resolved before gets the same answer with no lookup. A source
// with no budget left gets ErrLimited with no lookup. A lookup that answers
// identity.ErrUnauthenticated uses one unit of the budget of the source.
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

// lookup resolves token for the source, within its budget.
func (g *Gate) lookup(ctx context.Context, source, token string) (identity.Principal, error) {
	if !g.allow(ctx, source) {
		return identity.Principal{}, ErrLimited
	}

	p, err := g.next.AuthenticateToken(ctx, token)
	if errors.Is(err, identity.ErrUnauthenticated) {
		g.fail(source)
	}

	return p, err
}

// allow reports whether source has budget for one more lookup. It logs the
// first refusal of each limit period, with the source only.
func (g *Gate) allow(ctx context.Context, source string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	b, ok := g.sources[source]
	if !ok {
		return true
	}

	g.fillLocked(b)

	if b.tokens >= 1 {
		b.logged = false

		return true
	}

	if !b.logged {
		b.logged = true
		g.logger.WarnContext(ctx, "failed sign-ins limited", slog.String("source_ip", source))
	}

	return false
}

// fail uses one unit of the budget of source.
func (g *Gate) fail(source string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	b, ok := g.sources[source]
	if !ok {
		g.makeRoomLocked()

		b = &bucket{at: g.now(), tokens: g.burst, logged: false}
		g.sources[source] = b
	}

	g.fillLocked(b)

	b.tokens--
	if b.tokens < 0 {
		b.tokens = 0
	}
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
