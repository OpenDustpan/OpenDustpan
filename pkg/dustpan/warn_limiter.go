package dustpan

import (
	"context"
	"log/slog"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"opendustpan.org/opendustpan/internal/clientip"
)

// warnInterval is the quiet period per peer after one missing-header warning.
const warnInterval = time.Minute

// warnMaxPeers caps the peers the limiter tracks.
const warnMaxPeers = 1024

// warnLimiter rate-limits the missing-header warning to one line per peer
// per minute. It holds at most warnMaxPeers peers; a new peer past the cap
// replaces the oldest entry, so the map never grows without a bound.
type warnLimiter struct {
	mu   sync.Mutex
	last map[string]time.Time
	now  func() time.Time
}

// newWarnLimiter returns a limiter that reads the time from now, or from the
// wall clock when now is nil.
func newWarnLimiter(now func() time.Time) *warnLimiter {
	if now == nil {
		now = time.Now
	}

	return &warnLimiter{mu: sync.Mutex{}, last: make(map[string]time.Time), now: now}
}

// allow reports whether the server may warn for peer now, and records the
// warning it allows: the first sighting, or one at least warnInterval after
// the last warning for the peer. An empty peer never warns.
func (l *warnLimiter) allow(peer string) bool {
	if l == nil || peer == "" {
		return false
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.clock()
	last, seen := l.last[peer]

	if seen && now.Sub(last) < warnInterval {
		return false
	}

	if !seen && len(l.last) >= warnMaxPeers {
		l.evictOldest()
	}

	l.last[peer] = now

	return true
}

// clock answers the time from the injected source, or the wall clock when
// none was injected.
func (l *warnLimiter) clock() time.Time {
	if l.now == nil {
		return time.Now()
	}

	return l.now()
}

// evictOldest drops the peer with the oldest warning. The caller holds the
// lock, and the map holds at least one entry when the limiter calls it.
func (l *warnLimiter) evictOldest() {
	oldest := ""
	var oldestTime time.Time

	first := true

	for peer, at := range l.last {
		if first || at.Before(oldestTime) {
			oldest, oldestTime, first = peer, at, false
		}
	}

	if !first {
		delete(l.last, oldest)
	}
}

// warnMissingHeader writes one rate-limited warning when a trusted peer sent
// no forwarding header and the client address is unknown. The line holds the
// peer and the header name only, never a header value.
func (l *warnLimiter) warnMissingHeader(
	ctx context.Context, r *http.Request, addr netip.Addr, resolver *clientip.Resolver, logger *slog.Logger,
) {
	if l == nil || logger == nil || resolver == nil {
		return
	}

	if addr.IsValid() || !resolver.TrustedPeerWithoutHeader(r) {
		return
	}

	peer := clientip.Peer(r)
	if !peer.IsValid() {
		return
	}

	if !l.allow(peer.String()) {
		return
	}

	logger.WarnContext(
		ctx,
		"a trusted proxy sent no forwarding header: check --trusted-proxies and --trusted-proxy-header",
		slog.String("peer", peer.String()),
		slog.String("header", string(resolver.Header())),
	)
}
