// Package authgate wraps the stored-token Authenticator for the HTTP server.
// It bounds the rate of token guesses and the store load that each source
// address can cause (ADR 53). It does not protect a token: the 256 bits of
// a secret do that.
//
// Each source has a budget of misses. Only lookups that Charge selects
// count: the server charges values that have the form of a stored-token
// secret. While a source has budget, its requests go to the store at once.
// When it has spent the budget, each further charged request from it waits
// a penalty before the store read, a valid token too, so the time of an
// answer tells nothing about the token, and no valid token is refused. At
// most MaxWaiting requests of one source wait at the same time; a request
// over that cap gets identity.ErrRateLimited (429) with no store read. A
// wait ends early when the client goes away, and the request then gets no
// store read either.
//
// With a FailureCounter, the misses of every instance on one database count
// in one budget for each source: a windowed count in the database. Each
// miss writes once; a request that does not miss writes nothing. The gate
// also keeps its own budget in the process. A source is over the budget
// when either budget is spent, so a counter that fails leaves the budget of
// the process in place.
//
// The gate also keeps the answer of one request, so the request log and
// the adapter resolve a token once: one store read, one last-used write and
// one count of a miss for each request.
//
// The server checks the bootstrap admin tokens (ADR 6) before the gate, so
// the gate never sees them. The root middleware in pkg/dustpan calls
// WithRequest for each request. The key lives here, not in pkg/dustpan,
// because pkg/dustpan imports the adapters. A context without WithRequest
// passes through with no budget and no memo.
package authgate

import (
	"container/list"
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

// Defaults of a Gate: a burst of 30 misses for each source, refilled at one
// each 2 seconds (30 a minute), a penalty of 1 second for each request over
// the budget, and at most 4 waiting requests for each source.
const (
	DefaultBurst      = 30
	DefaultRefill     = 2 * time.Second
	DefaultPenalty    = time.Second
	DefaultMaxWaiting = 4
	// DefaultMaxSources bounds the sources that the gate tracks. Over it,
	// the gate forgets the source that it saw least recently.
	DefaultMaxSources = 10000
	// ipv6PrefixBits is the part of an IPv6 address that names one source:
	// a /64 is the smallest network that one site usually gets.
	ipv6PrefixBits = 64
)

// ErrLimited reports a request over the waiting cap of its source. It wraps
// identity.ErrRateLimited, so every adapter answers 429.
var ErrLimited = fmt.Errorf("%w: try again later", identity.ErrRateLimited)

// FailureCounter counts the misses of each source in fixed windows, so that
// every instance on one database shares one budget. A window is named by
// its start. The SQL stores implement it.
type FailureCounter interface {
	// AddFailure adds one miss of source to the window that starts at
	// start, in one write, and answers the count of that window after the
	// add. Concurrent adds each count once. It answers an error when the
	// write fails.
	AddFailure(ctx context.Context, source string, start time.Time) (int64, error)
	// Failures answers the count of source in the window that starts at
	// start, or 0 when that window holds none. It answers an error when the
	// read fails.
	Failures(ctx context.Context, source string, start time.Time) (int64, error)
	// PruneFailures deletes every window that starts before before, and
	// answers the count of windows it deleted. It answers an error when the
	// delete fails.
	PruneFailures(ctx context.Context, before time.Time) (int64, error)
}

// ErrMissingNext reports a Config without Next.
var ErrMissingNext = errors.New("authgate: missing next authenticator")

// Config holds the settings of a Gate. Zero values take the defaults.
type Config struct {
	// Next resolves the tokens. Required.
	Next identity.Authenticator
	// Charge reports whether a lookup of token counts for the budget of
	// its source. Nil charges every lookup.
	Charge func(token string) bool
	// Logger logs the first request over the budget of a source in each
	// limit period, with the source only. Nil discards it.
	Logger *slog.Logger
	// Now returns the current time. Nil means time.Now.
	Now func() time.Time
	// Failures counts the misses of every instance. Nil keeps the budget
	// in the process only.
	Failures FailureCounter
	// Window is the length of one window of Failures. A source is over the
	// budget when its count in the current window reaches Burst. Zero means
	// Burst times Refill.
	Window time.Duration
	// Burst is the count of misses that a source can have at once.
	Burst int
	// Refill is the time that gives a source back one miss.
	Refill time.Duration
	// Penalty is how long a request over the budget waits before the store
	// read.
	Penalty time.Duration
	// MaxWaiting bounds the requests of one source that wait at once.
	MaxWaiting int
	// MaxSources bounds the tracked sources.
	MaxSources int
}

// Gate is an identity.Authenticator that bounds the guess rate of each
// source and keeps the answer of each request.
type Gate struct {
	next       identity.Authenticator
	charge     func(token string) bool
	logger     *slog.Logger
	now        func() time.Time
	failures   FailureCounter
	sources    map[string]*list.Element
	recent     *list.List
	refill     time.Duration
	penalty    time.Duration
	window     time.Duration
	burst      float64
	maxWaiting int
	maxSources int
	// prunedAt is the start of the window in which the gate last pruned
	// Failures, and warnedAt the time of its last warning about Failures.
	prunedAt time.Time
	warnedAt time.Time
	mu       sync.Mutex
}

var _ identity.Authenticator = (*Gate)(nil)

// bucket is the budget of one source. waiting counts its requests that wait
// the penalty now.
type bucket struct {
	at      time.Time
	key     string
	tokens  float64
	waiting int
	logged  bool
}

// New returns a Gate.
func New(cfg Config) (*Gate, error) {
	if cfg.Next == nil {
		return nil, ErrMissingNext
	}

	burst := positiveOr(cfg.Burst, DefaultBurst)
	refill := positiveOr(cfg.Refill, DefaultRefill)

	g := &Gate{
		next:       cfg.Next,
		charge:     cfg.Charge,
		logger:     cfg.Logger,
		now:        cfg.Now,
		failures:   cfg.Failures,
		sources:    map[string]*list.Element{},
		recent:     list.New(),
		refill:     refill,
		penalty:    positiveOr(cfg.Penalty, DefaultPenalty),
		window:     positiveOr(cfg.Window, time.Duration(burst)*refill),
		burst:      float64(burst),
		maxWaiting: positiveOr(cfg.MaxWaiting, DefaultMaxWaiting),
		maxSources: positiveOr(cfg.MaxSources, DefaultMaxSources),
		prunedAt:   time.Time{},
		warnedAt:   time.Time{},
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
// token is looked up, after the penalty when its source is over the budget.
// A charged miss uses one unit of the budget of the source.
func (g *Gate) AuthenticateToken(ctx context.Context, token string) (identity.Principal, error) {
	req, ok := ctx.Value(key{}).(*request)
	if !ok || req == nil {
		return g.next.AuthenticateToken(ctx, token)
	}

	digest := sha256.Sum256([]byte(token))

	if m := req.recall(digest); m != nil {
		return m.principal, m.err
	}

	p, err := g.lookup(ctx, req.source, token)
	req.remember(digest, p, err)

	return p, err
}

// Admit waits as a charged token lookup of the request in ctx waits: it
// returns at once while the source of the request has budget, else after
// the penalty. It returns ErrLimited when the waiting cap of the source is
// full, and identity.ErrRateLimited when ctx ends during the wait. A step
// that is not a token lookup, such as the callback of a sign-in through an
// identity provider, calls it before it does any work. With no request in
// ctx, it returns nil at once.
func (g *Gate) Admit(ctx context.Context) error {
	req, ok := ctx.Value(key{}).(*request)
	if !ok || req == nil {
		return nil
	}

	return g.waitTurn(ctx, req.source)
}

// Fail uses one unit of the budget of the source of the request in ctx, as
// a failed token lookup does. With no request in ctx, it does nothing.
func (g *Gate) Fail(ctx context.Context) {
	req, ok := ctx.Value(key{}).(*request)
	if !ok || req == nil {
		return
	}

	g.spend(ctx, req.source)
}

// recall returns the answer that the request kept for digest, or nil.
func (r *request) recall(digest [sha256.Size]byte) *memo {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.memo == nil || subtle.ConstantTimeCompare(r.memo.digest[:], digest[:]) != 1 {
		return nil
	}

	return r.memo
}

// remember keeps the answer for digest.
func (r *request) remember(digest [sha256.Size]byte, p identity.Principal, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.memo = &memo{err: err, principal: p, digest: digest}
}

// lookup resolves token for the source within its budget.
func (g *Gate) lookup(ctx context.Context, source, token string) (identity.Principal, error) {
	if !g.charge(token) {
		return g.next.AuthenticateToken(ctx, token)
	}

	if err := g.waitTurn(ctx, source); err != nil {
		var none identity.Principal

		return none, err
	}

	p, err := g.next.AuthenticateToken(ctx, token)
	if errors.Is(err, identity.ErrUnauthenticated) {
		g.spend(ctx, source)
	}

	return p, err
}

// waitTurn returns nil when a request of source can go to the store: at once
// while the source has budget, else after the penalty. It returns ErrLimited
// when the waiting cap of the source is full, and identity.ErrRateLimited
// when ctx ends during the wait.
func (g *Gate) waitTurn(ctx context.Context, source string) error {
	if !g.reserve(ctx, source) {
		return nil
	}

	b, ok := g.enterWait(source)
	if !ok {
		return ErrLimited
	}

	defer g.leaveWait(b)

	timer := time.NewTimer(g.penalty)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return fmt.Errorf("%w: the client went away during the wait", identity.ErrRateLimited)
	case <-timer.C:
		return nil
	}
}

// reserve reports whether source is over its budget: the budget of the
// process, or the shared count of the current window. It reads the shared
// count only while the budget of the process holds. It logs the first
// request over the budget of each limit period.
func (g *Gate) reserve(ctx context.Context, source string) bool {
	over := g.overLocal(source)
	if !over {
		over = g.overShared(ctx, source)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	b := g.touchLocked(source)
	if !over {
		b.logged = false

		return false
	}

	if !b.logged {
		b.logged = true
		g.logger.WarnContext(ctx, "failed sign-ins limited", slog.String("source_ip", source))
	}

	return true
}

// overLocal reports whether source spent the budget of this process.
func (g *Gate) overLocal(source string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.touchLocked(source).tokens < 1
}

// overShared reports whether the shared count of source in the current
// window reached the burst. Without a counter, or when the read fails, it
// reports false: the budget of the process still holds.
func (g *Gate) overShared(ctx context.Context, source string) bool {
	if g.failures == nil {
		return false
	}

	n, err := g.failures.Failures(ctx, source, g.windowStart(g.now()))
	if err != nil {
		g.warnCounter(ctx, "read", err)

		return false
	}

	return float64(n) >= g.burst
}

// windowStart returns the start of the window of the shared count that
// holds now.
func (g *Gate) windowStart(now time.Time) time.Time {
	return now.Truncate(g.window)
}

// enterWait takes a waiting place of source, or reports false when its cap
// is full.
func (g *Gate) enterWait(source string) (*bucket, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	b := g.touchLocked(source)
	if b.waiting >= g.maxWaiting {
		return nil, false
	}

	b.waiting++

	return b, true
}

// leaveWait gives back the waiting place of b.
func (g *Gate) leaveWait(b *bucket) {
	g.mu.Lock()
	defer g.mu.Unlock()

	b.waiting--
}

// spend uses one unit of the budget of source, and adds one miss of source
// to the shared count. The add does not stop when the client goes away, so
// a client cannot cancel the count of its miss.
func (g *Gate) spend(ctx context.Context, source string) {
	g.spendLocal(source)

	if g.failures == nil {
		return
	}

	ctx = context.WithoutCancel(ctx)
	start := g.windowStart(g.now())

	if _, err := g.failures.AddFailure(ctx, source, start); err != nil {
		g.warnCounter(ctx, "add", err)
	}

	g.prune(ctx, start)
}

// spendLocal uses one unit of the budget of source in this process.
func (g *Gate) spendLocal(source string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	b := g.touchLocked(source)

	b.tokens--
	if b.tokens < 0 {
		b.tokens = 0
	}
}

// prune deletes the windows of the shared count that ended before the
// window before start, once for each window in this process.
func (g *Gate) prune(ctx context.Context, start time.Time) {
	g.mu.Lock()
	due := !g.prunedAt.Equal(start)
	g.prunedAt = start
	g.mu.Unlock()

	if !due {
		return
	}

	if _, err := g.failures.PruneFailures(ctx, start.Add(-g.window)); err != nil {
		g.warnCounter(ctx, "prune", err)
	}
}

// warnCounter logs a failed call of the shared count, at most once for each
// window.
func (g *Gate) warnCounter(ctx context.Context, call string, err error) {
	now := g.now()

	g.mu.Lock()
	due := g.warnedAt.IsZero() || now.Sub(g.warnedAt) >= g.window
	if due {
		g.warnedAt = now
	}
	g.mu.Unlock()

	if due {
		g.logger.WarnContext(ctx, "sign-in failure count failed; the budget of this process still applies",
			slog.String("call", call), slog.String("error", err.Error()))
	}
}

// touchLocked returns the bucket of source with the budget it earned since
// its last change, and marks it as the source seen most recently. A new
// source starts with a full budget. Over MaxSources, it forgets the source
// seen least recently, in O(1). A source with waiting requests keeps its
// bucket alive through their pointers.
func (g *Gate) touchLocked(source string) *bucket {
	now := g.now()

	if el, ok := g.sources[source]; ok {
		if b, isBucket := el.Value.(*bucket); isBucket {
			g.recent.MoveToFront(el)
			g.fillLocked(b, now)

			return b
		}

		delete(g.sources, source)
		g.recent.Remove(el)
	}

	if g.recent.Len() >= g.maxSources {
		if oldest := g.recent.Back(); oldest != nil {
			if old, ok := oldest.Value.(*bucket); ok {
				delete(g.sources, old.key)
			}

			g.recent.Remove(oldest)
		}
	}

	b := &bucket{at: now, key: source, tokens: g.burst, waiting: 0, logged: false}
	g.sources[source] = g.recent.PushFront(b)

	return b
}

// fillLocked adds the budget that b earned since its last change.
func (g *Gate) fillLocked(b *bucket, now time.Time) {
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
