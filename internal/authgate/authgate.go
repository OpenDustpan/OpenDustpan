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
// miss writes once, and the gate decides on the count that the write
// answers: when it reaches the burst, the source is over the budget in this
// process until the window ends. A request that does not miss reads and
// writes nothing. The gate also keeps its own budget in the process. A
// source is over the budget when either budget is spent, so a counter that
// fails leaves the budget of the process in place.
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
	"strings"
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
	// ipv6GroupBits is the part of an IPv6 address that names one site group:
	// a /48 is the allocation that RFC 6177 advises for an end site, and it
	// holds 65,536 ranges of /64.
	ipv6GroupBits = 48
)

// ErrLimited reports a request over the waiting cap of its source. It wraps
// identity.ErrRateLimited, so every adapter answers 429.
var ErrLimited = fmt.Errorf("%w: try again later", identity.ErrRateLimited)

// FailureCounter counts the misses of each source in fixed windows, so that
// every instance on one database shares one budget. The store takes the
// time of each call from its own clock, never from the clock of the
// instance: a window of length L holds the times from k*L to (k+1)*L on
// that clock, in Unix milliseconds. The conformance suite
// storetest.RunSignInFailures checks an implementation.
type FailureCounter interface {
	// AddFailure adds one miss of source to the current window of length
	// length, in one atomic write, and answers the count of that window
	// after the add, the time of the add and the end of the window.
	// Concurrent adds each count once. length must be 1 ms or longer: it
	// returns ErrInvalidWindow for a shorter one. It answers an error when
	// the write fails.
	AddFailure(ctx context.Context, source string, length time.Duration) (FailureCount, error)
	// PruneFailures deletes every window that ended age or longer ago, and
	// answers the count of windows it deleted. It answers an error when the
	// delete fails.
	PruneFailures(ctx context.Context, age time.Duration) (int64, error)
	// PruneFailuresAt deletes every window that ended age or longer ago on
	// now, and answers the count of windows it deleted. It takes now
	// instead of reading the clock of the store, so a test can pin the
	// prune at the boundary of a window. It answers an error when the
	// delete fails.
	PruneFailuresAt(ctx context.Context, now time.Time, age time.Duration) (int64, error)
}

// FailureCount is the answer of FailureCounter.AddFailure: the count of the
// window after the add, and the time of the add and the end of the window
// on the clock of the store.
type FailureCount struct {
	At    time.Time
	End   time.Time
	Count int64
}

// ErrInvalidWindow reports a window of the shared count shorter than one
// millisecond.
var ErrInvalidWindow = errors.New("authgate: the failure window must be 1 ms or longer")

// ErrMissingNext reports a Config without Next.
var ErrMissingNext = errors.New("authgate: missing next authenticator")

// ErrInvalidScope reports a Config with a Scope that is not empty and not
// made of the letters a to z only.
var ErrInvalidScope = errors.New("authgate: the scope must be empty or the letters a to z")

// unknownSource is the budget key of a source that holds the scope
// separator. All such sources share one budget. The key is the separator
// itself, so no source that is not mapped to it can have the same key.
const unknownSource = scopeSeparator

// scopeSeparator joins the scope and the source in the key of the shared
// count. A scope cannot hold it.
const scopeSeparator = "|"

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
	// in the process only. A failed counter call does not block the sign-in,
	// and the budget of the process still applies, because an unavailable
	// database must not lock every user out.
	Failures FailureCounter
	// Window is the length of one window of Failures, 1 ms or longer. A
	// source whose count in a window reaches Burst is over the budget until
	// the window ends. Zero means Burst times Refill. New returns
	// ErrInvalidWindow for a shorter window.
	Window time.Duration
	// CounterErrors is called with the operation, OperationAdd or
	// OperationPrune, each time a call of Failures fails. Nil counts
	// nothing.
	CounterErrors func(operation string)
	// Scope names the budget in the shared count. Gates with different
	// scopes keep separate counts for one source, so the misses of one gate
	// never spend the budget of another. Empty is the scope of the token
	// lookups.
	Scope string
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
	scope      string
	errors     func(operation string)
	sources    map[string]*list.Element
	recent     *list.List
	refill     time.Duration
	penalty    time.Duration
	window     time.Duration
	burst      float64
	maxWaiting int
	maxSources int
	// refuse makes a source over the budget get ErrLimited at once.
	refuse bool
	// prunedAt is the time of the last prune of Failures, and warnedAt the
	// time of the last warning about Failures.
	prunedAt time.Time
	warnedAt time.Time
	mu       sync.Mutex
}

var _ identity.Authenticator = (*Gate)(nil)

// bucket is the budget of one source. waiting counts its requests that wait
// the penalty now. inFlight counts the units that unresolved lookups hold:
// tokens does not hold them, and a unit in flight is not a failure.
// resolved is closed and replaced each time a lookup in flight resolves.
// sharedUntil is the end, on the clock of this process, of the window of the
// shared count that the source spent.
type bucket struct {
	at          time.Time
	sharedUntil time.Time
	resolved    chan struct{}
	key         string
	tokens      float64
	waiting     int
	inFlight    int
	logged      bool
}

// New returns a Gate.
func New(cfg Config) (*Gate, error) {
	if cfg.Next == nil {
		return nil, ErrMissingNext
	}

	if !validScope(cfg.Scope) {
		return nil, ErrInvalidScope
	}

	burst := positiveOr(cfg.Burst, DefaultBurst)
	refill := positiveOr(cfg.Refill, DefaultRefill)

	window := positiveOr(cfg.Window, time.Duration(burst)*refill)
	if window < time.Millisecond {
		return nil, ErrInvalidWindow
	}

	g := &Gate{
		next:       cfg.Next,
		charge:     cfg.Charge,
		logger:     cfg.Logger,
		now:        cfg.Now,
		failures:   cfg.Failures,
		scope:      cfg.Scope,
		errors:     cfg.CounterErrors,
		sources:    map[string]*list.Element{},
		recent:     list.New(),
		refill:     refill,
		penalty:    positiveOr(cfg.Penalty, DefaultPenalty),
		window:     window,
		burst:      float64(burst),
		maxWaiting: positiveOr(cfg.MaxWaiting, DefaultMaxWaiting),
		maxSources: positiveOr(cfg.MaxSources, DefaultMaxSources),
		refuse:     false,
		prunedAt:   time.Time{},
		warnedAt:   time.Time{},
		mu:         sync.Mutex{},
	}

	if g.errors == nil {
		g.errors = func(string) {}
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

// NewRefusing returns a Gate that refuses a source over the budget at once
// with ErrLimited, where a Gate from New makes it wait the penalty. The sign-in
// routes that check no token use it: a wait would only hold a connection. The
// other settings are the ones of New.
func NewRefusing(cfg Config) (*Gate, error) {
	g, err := New(cfg)
	if err != nil {
		return nil, err
	}

	g.refuse = true

	return g, nil
}

// validScope reports whether scope is empty or made of the letters a to z.
func validScope(scope string) bool {
	for _, r := range scope {
		if r < 'a' || r > 'z' {
			return false
		}
	}

	return true
}

// positiveOr returns v, or fallback when v is not above zero.
func positiveOr[T int | time.Duration](v, fallback T) T {
	if v > 0 {
		return v
	}

	return fallback
}

// request is the state of one request: its source and the answer that each
// gate gave, by the scope of the gate. Gates of different scopes look up
// different kinds of token, so the miss of one gate is not the answer of
// another.
type request struct {
	memos  map[string]*memo
	source string
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
	return context.WithValue(ctx, key{}, &request{
		memos: map[string]*memo{}, source: SourceKey(source), mu: sync.Mutex{},
	})
}

// SourceKey returns the budget key that the gate uses for the address source.
// Other budgets by source address use it too, so that one address is one
// source in every budget: an IPv4 address as it is, an IPv6 address as its
// /64, and a text with the scope separator as one unknown source.
func SourceKey(source string) string {
	// Defense in depth: the counter key adds the scope in front of the source
	// with a separator, and this check keeps a source from forging another
	// scope. It does not rely on the scope check of New.
	if strings.Contains(source, scopeSeparator) {
		return unknownSource
	}

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

// SourceGroupKey returns the key of the site group of the address source: the
// /48 of an IPv6 address, because one site can use every /64 range of its /48.
// An IPv4 address, a text that is not an address and a text with the scope
// separator give the same key as SourceKey.
func SourceGroupKey(source string) string {
	// Defense in depth: the counter key adds the scope in front of the source
	// with a separator, and this check keeps a source from forging another
	// scope. It does not rely on the scope check of New.
	if strings.Contains(source, scopeSeparator) {
		return unknownSource
	}

	addr, err := netip.ParseAddr(source)
	if err != nil {
		return source
	}

	addr = addr.Unmap()
	if addr.Is4() {
		return addr.String()
	}

	prefix, err := addr.Prefix(ipv6GroupBits)
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

	if m := req.recall(g.scope, digest); m != nil {
		return m.principal, m.err
	}

	p, err := g.lookup(ctx, req.source, token)
	req.remember(g.scope, digest, p, err)

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

	_, err := g.waitTurn(ctx, req.source, checkOnly)

	return err
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

// recall returns the answer that the request kept for digest in the gate
// scope scope, or nil.
func (r *request) recall(scope string, digest [sha256.Size]byte) *memo {
	r.mu.Lock()
	defer r.mu.Unlock()

	m := r.memos[scope]
	if m == nil || subtle.ConstantTimeCompare(m.digest[:], digest[:]) != 1 {
		return nil
	}

	return m
}

// remember keeps the answer for digest in the gate scope scope.
func (r *request) remember(scope string, digest [sha256.Size]byte, p identity.Principal, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.memos[scope] = &memo{err: err, principal: p, digest: digest}
}

// lookup resolves token for the source within its budget. A lookup within
// the budget takes its unit before the store read, so concurrent lookups of
// one source cannot pass the budget. A miss keeps the unit; any other
// answer, a panic of the store read too, gives it back.
func (g *Gate) lookup(ctx context.Context, source, token string) (identity.Principal, error) {
	if !g.charge(token) {
		return g.next.AuthenticateToken(ctx, token)
	}

	held, err := g.waitTurn(ctx, source, takeUnit)
	if err != nil {
		var none identity.Principal

		return none, err
	}

	if held != nil {
		defer g.giveBack(held)
	}

	p, err := g.next.AuthenticateToken(ctx, token)
	if !errors.Is(err, identity.ErrUnauthenticated) {
		return p, err
	}

	if held == nil {
		// The lookup waited the penalty and took no unit.
		g.spend(ctx, source)

		return p, err
	}

	g.keep(held)
	g.countShared(ctx, source)

	return p, err
}

// turn says what a request takes of the budget when it is within it.
type turn int

const (
	// checkOnly takes nothing: the caller reports a failure with Fail.
	checkOnly turn = iota
	// takeUnit takes one unit before the lookup.
	takeUnit
)

// waitTurn returns when a request of source can go to the store: at once
// while the source has budget, else after the penalty. With takeUnit, a
// request within the budget takes one unit, and waitTurn returns its hold;
// the caller resolves it. While lookups in flight hold the
// rest of the budget, a request waits for one of them to resolve, with no
// penalty and no waiting cap: a unit in flight is not a failure. It returns
// ErrLimited when the waiting cap of the penalty is full, and
// identity.ErrRateLimited when ctx ends during a wait.
func (g *Gate) waitTurn(ctx context.Context, source string, mode turn) (*hold, error) {
	for {
		got := g.reserve(ctx, source, mode)

		switch {
		case got.held != nil || got.state == withinBudget:
			return got.held, nil
		case got.state == heldInFlight:
			// No MaxWaiting cap bounds the requests that wait behind
			// lookups in flight; each one is bounded by its context. Each
			// end of a unit in flight wakes all of them, and they take
			// the budget again in turn.
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("%w: the client went away during the wait", identity.ErrRateLimited)
			case <-got.resolved:
				continue
			}
		case g.refuse:
			// A refusing gate has no token to wait for: a source over the
			// budget gets ErrLimited at once, with no wait and no waiting
			// place.
			return nil, ErrLimited
		default:
			return nil, g.waitPenalty(ctx, source)
		}
	}
}

// waitPenalty waits the penalty of a request of source over its budget. It
// returns ErrLimited when the waiting cap of source is full, and
// identity.ErrRateLimited when ctx ends during the wait.
func (g *Gate) waitPenalty(ctx context.Context, source string) error {
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

// budgetState is the state of the budget of a source for one request.
type budgetState int

const (
	// withinBudget: the request can go to the store.
	withinBudget budgetState = iota
	// heldInFlight: lookups in flight hold the rest of the budget.
	heldInFlight
	// overBudget: failures spent the budget.
	overBudget
)

// reservation is the answer of reserve: the state of the budget, the hold
// of the unit that the request took, or nil, and the channel that closes
// when a lookup in flight resolves.
type reservation struct {
	held     *hold
	resolved <-chan struct{}
	state    budgetState
}

// reserve reports the state of the budget of source: the budget of the
// process, and the shared count of a window that has not ended. Within the
// budget and with takeUnit, it takes one unit in the same lock and returns
// its hold. It reads nothing from the store. It logs the
// first request over the budget of each limit period.
func (g *Gate) reserve(ctx context.Context, source string, mode turn) reservation {
	g.mu.Lock()
	defer g.mu.Unlock()

	b := g.touchLocked(source)
	shared := g.now().Before(b.sharedUntil)

	switch {
	case b.tokens >= 1 && !shared:
		b.logged = false

		if mode != takeUnit {
			return reservation{held: nil, resolved: nil, state: withinBudget}
		}

		b.tokens--
		b.inFlight++

		return reservation{held: &hold{b: b, done: false}, resolved: nil, state: withinBudget}
	case b.inFlight > 0 && !shared && b.tokens+float64(b.inFlight) >= 1:
		return reservation{held: nil, resolved: b.resolved, state: heldInFlight}
	default:
		if !b.logged {
			b.logged = true
			g.logger.WarnContext(ctx, "failed sign-ins limited", slog.String("source_ip", source))
		}

		return reservation{held: nil, resolved: nil, state: overBudget}
	}
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

// Operations of the shared count that Config.CounterErrors names.
const (
	OperationAdd   = "add"
	OperationPrune = "prune"
)

// counterTimeout bounds one call of the shared count, so a stalled
// database cannot hold the goroutines of the misses.
const counterTimeout = 5 * time.Second

// spend uses one unit of the budget of source and adds one miss of source
// to the shared count.
func (g *Gate) spend(ctx context.Context, source string) {
	g.spendLocal(source)
	g.countShared(ctx, source)
}

// countShared adds one miss of source to the shared count. When the count
// that the add answers reaches the burst, the source is over the budget
// until the window ends. The add does not stop when the client goes away,
// so a client cannot cancel the count of its miss.
func (g *Gate) countShared(ctx context.Context, source string) {
	if g.failures == nil {
		return
	}

	callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), counterTimeout)
	defer cancel()

	count, err := g.failures.AddFailure(callCtx, g.counterSource(source), g.window)
	if err != nil {
		g.counterFailed(ctx, OperationAdd, err)
	} else if float64(count.Count) >= g.burst {
		g.markShared(source, count.End.Sub(count.At))
	}

	g.prune(callCtx, ctx)
}

// counterSource returns the key of source in the shared count: source as it
// is for the scope of the token lookups, else the scope and source. A scope
// has a separator that a source cannot hold, so no source of one scope
// meets a source of another.
func (g *Gate) counterSource(source string) string {
	if g.scope == "" {
		return source
	}

	return g.scope + scopeSeparator + source
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

// hold is the unit that one lookup in flight holds in a bucket.
type hold struct {
	b    *bucket
	done bool
}

// keep ends the unit of h as a failure of a miss. A second end of h does
// nothing.
func (g *Gate) keep(h *hold) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.endLocked(h)
}

// giveBack ends the unit of h and gives it back, unless the unit already
// ended. A lookup defers it, so a panic of the store read gives the unit
// back too.
func (g *Gate) giveBack(h *hold) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if !h.done {
		// The refill keeps tokens plus the units in flight within the
		// burst, so the unit that comes back stays within it too.
		h.b.tokens++
	}

	g.endLocked(h)
}

// endLocked ends the unit of h once and wakes the requests that wait for
// a lookup in flight.
func (g *Gate) endLocked(h *hold) {
	if h.done {
		return
	}

	h.done = true
	h.b.inFlight--

	close(h.b.resolved)
	h.b.resolved = make(chan struct{})
}

// markShared marks source as over the budget for left, the rest of the
// window on the clock of the store, counted on the clock of this process.
func (g *Gate) markShared(source string, left time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()

	b := g.touchLocked(source)
	b.sharedUntil = g.now().Add(left)
}

// prune deletes the windows of the shared count that ended one window or
// longer ago, at most once for each window length in this process. callCtx
// carries the call, and ctx is the request.
func (g *Gate) prune(callCtx, ctx context.Context) {
	now := g.now()

	g.mu.Lock()
	due := g.prunedAt.IsZero() || now.Sub(g.prunedAt) >= g.window
	if due {
		g.prunedAt = now
	}
	g.mu.Unlock()

	if !due {
		return
	}

	if _, err := g.failures.PruneFailures(callCtx, g.window); err != nil {
		g.counterFailed(ctx, OperationPrune, err)
	}
}

// counterFailed counts a failed call of the shared count, and logs it at
// most once for each window. It logs nothing when the client of ctx went
// away.
func (g *Gate) counterFailed(ctx context.Context, operation string, err error) {
	g.errors(operation)

	if ctx.Err() != nil {
		return
	}

	now := g.now()

	g.mu.Lock()
	due := g.warnedAt.IsZero() || now.Sub(g.warnedAt) >= g.window
	if due {
		g.warnedAt = now
	}
	g.mu.Unlock()

	if due {
		g.logger.WarnContext(ctx, "sign-in failure count failed; the budget of this process still applies",
			slog.String("operation", operation), slog.String("error", err.Error()))
	}
}

// touchLocked returns the bucket of source with the budget it earned since
// its last change, and marks it as the source seen most recently. A new
// source starts with a full budget. Over MaxSources, it forgets the source
// seen least recently, in O(1). The requests that wait on a forgotten
// bucket keep their pointer to it, so their waits and units still end
// there, but the source itself gets a new bucket with a full budget.
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

	b := &bucket{
		at: now, sharedUntil: time.Time{}, resolved: make(chan struct{}), key: source, tokens: g.burst,
		waiting: 0, inFlight: 0, logged: false,
	}
	g.sources[source] = g.recent.PushFront(b)

	return b
}

// fillLocked adds the budget that b earned since its last change.
func (g *Gate) fillLocked(b *bucket, now time.Time) {
	elapsed := now.Sub(b.at)
	if elapsed <= 0 {
		return
	}

	b.tokens = min(b.tokens+float64(elapsed)/float64(g.refill), g.burst-float64(b.inFlight))

	b.at = now
}
