// Package denylimit bounds how many audit entries repeated denials write. A
// denial storm from one principal must not fill the audit log, and it must
// not cost unbounded memory.
//
// The rule: for each key, the first denial of a window writes an entry. Each
// later denial in the same window is counted and writes nothing. The count
// goes out with the first entry of the next window of that key, or, when the
// key stays quiet, with a flush entry that the first call after the window
// returns. A Limiter tracks at most MaxKeys keys, and at most MaxKeysPerPrincipal
// keys for each principal. A denial of a new key of a principal beyond its bound
// goes into the aggregate key of that principal, so its entry still names the
// principal and one principal cannot use the room of the others. A denial of a
// new key beyond MaxKeys goes into one aggregate for all principals: one entry
// for each window, with the count of the denials that it stands for. The
// Limiter starts no goroutine.
package denylimit

import (
	"sync"
	"time"
)

// Default bounds. The values keep one principal to one entry for each action,
// object kind and scope for each minute, and the table to a few hundred kilobytes.
const (
	DefaultWindow   = time.Minute
	DefaultMaxKeys  = 4096
	DefaultMaxFlush = 32
	// DefaultMaxKeysPerPrincipal is the number of keys that one principal may
	// hold. The aggregate key of the principal is not in the count.
	DefaultMaxKeysPerPrincipal = 64
)

// AggregateMark is the value of the Action, Kind and Scope of the aggregate key
// of a principal.
const AggregateMark = "*"

// Key names one stream of denials. The Kind of a unit holds its tool, for
// example "unit:opentofu", so a stack and a workspace in one namespace have
// separate keys. The Kind also starts with the name of the adapter, so the
// refusals of two adapters of one tool have separate keys. The sets of tools
// and adapters are fixed in code, so the keys stay bounded.
type Key struct {
	Principal string
	Action    string
	Kind      string
	Scope     string
}

// Pending is a count of denials that no entry carries yet, found after the
// window of its key ended.
type Pending struct {
	Key        Key
	Suppressed int
	// Overflow is true for the aggregate of the denials of keys beyond MaxKeys.
	// Key is empty then.
	Overflow bool
}

// Decision is the answer of Allow.
type Decision struct {
	// Write is true when the caller must write the entry of this denial.
	Write bool
	// Suppressed is the number of denials of this key that no entry carries yet.
	// It is not zero only when Write is true.
	Suppressed int
	// Overflow is true when the entry is the aggregate of the denials of keys
	// beyond MaxKeys. Suppressed then counts the denials of the aggregate.
	Overflow bool
	// Flush holds counts of earlier windows that the caller writes as entries of
	// their own. It holds at most Config.MaxFlush items.
	Flush []Pending
}

// Config sets the bounds of a Limiter. A zero field takes its default.
type Config struct {
	Window   time.Duration
	MaxKeys  int
	MaxFlush int
	// MaxKeysPerPrincipal bounds the keys of one principal.
	MaxKeysPerPrincipal int
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
}

type slot struct {
	start      time.Time
	suppressed int
	// window is the window of this key. Zero means the window of the Limiter.
	window time.Duration
}

// span returns the window of s: its own, or def when s has none.
func (s *slot) span(def time.Duration) time.Duration {
	if s.window > 0 {
		return s.window
	}

	return def
}

// Limiter decides which denials write an entry. It is safe for concurrent use.
type Limiter struct {
	window   time.Duration
	maxKeys  int
	maxFlush int
	perPrin  int
	now      func() time.Time

	mu        sync.Mutex
	keys      map[Key]*slot
	perCount  map[string]int
	overflow  slot
	lastSweep time.Time
}

// New returns a Limiter.
func New(cfg Config) *Limiter {
	l := &Limiter{
		window:    cfg.Window,
		maxKeys:   cfg.MaxKeys,
		maxFlush:  cfg.MaxFlush,
		perPrin:   cfg.MaxKeysPerPrincipal,
		now:       cfg.Now,
		mu:        sync.Mutex{},
		keys:      make(map[Key]*slot),
		perCount:  make(map[string]int),
		overflow:  slot{start: time.Time{}, suppressed: 0, window: 0},
		lastSweep: time.Time{},
	}

	if l.window <= 0 {
		l.window = DefaultWindow
	}

	if l.maxKeys <= 0 {
		l.maxKeys = DefaultMaxKeys
	}

	if l.maxFlush <= 0 {
		l.maxFlush = DefaultMaxFlush
	}

	if l.perPrin <= 0 {
		l.perPrin = DefaultMaxKeysPerPrincipal
	}

	if l.now == nil {
		l.now = time.Now
	}

	return l
}

// Allow records one denial of key and says whether it writes an entry. The
// window of the key is the window of the Limiter.
func (l *Limiter) Allow(key Key) Decision {
	return l.AllowWindow(key, 0)
}

// AllowWindow is Allow with a window for key. The window applies when the key
// opens its first window; a window of zero or less means the window of the
// Limiter. A caller uses one window for each key.
func (l *Limiter) AllowWindow(key Key, window time.Duration) Decision {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	d := Decision{Write: false, Suppressed: 0, Overflow: false, Flush: l.sweep(now, key)}

	if s, ok := l.keys[key]; ok {
		return l.touch(d, s, now)
	}

	// A principal at its bound counts a new key in its own aggregate key. The
	// entry of the aggregate still names the principal.
	if l.perCount[key.Principal] >= l.perPrin {
		key = aggregateKey(key.Principal)

		if s, ok := l.keys[key]; ok {
			return l.touch(d, s, now)
		}
	}

	if len(l.keys) < l.maxKeys {
		l.keys[key] = &slot{start: now, suppressed: 0, window: max(window, 0)}

		if !isAggregate(key) {
			l.perCount[key.Principal]++
		}

		d.Write = true

		return d
	}

	// The table is full: this denial joins the aggregate.
	d.Overflow = true

	if !l.overflow.start.IsZero() && now.Sub(l.overflow.start) < l.window {
		l.overflow.suppressed++

		return d
	}

	d.Write, d.Suppressed = true, l.overflow.suppressed
	l.overflow = slot{start: now, suppressed: 0, window: 0}

	return d
}

// aggregateKey returns the key that holds the denials of a principal beyond its
// bound of keys.
func aggregateKey(principal string) Key {
	return Key{Principal: principal, Action: AggregateMark, Kind: AggregateMark, Scope: AggregateMark}
}

// isAggregate reports whether key is the aggregate key of its principal.
func isAggregate(key Key) bool {
	return key == aggregateKey(key.Principal)
}

// Drain removes every key and returns the counts that no entry carries yet, with
// no bound on their number except MaxKeys. The server calls it once at
// shutdown, so that a count is not lost. The Limiter is empty afterward.
func (l *Limiter) Drain() []Pending {
	l.mu.Lock()
	defer l.mu.Unlock()

	var out []Pending

	for k, s := range l.keys {
		if s.suppressed > 0 {
			out = append(out, Pending{Key: k, Suppressed: s.suppressed, Overflow: false})
		}
	}

	if l.overflow.suppressed > 0 {
		out = append(out, Pending{
			Key:        Key{Principal: "", Action: "", Kind: "", Scope: ""},
			Suppressed: l.overflow.suppressed, Overflow: true,
		})
	}

	l.keys = make(map[Key]*slot)
	l.perCount = make(map[string]int)
	l.overflow = slot{start: time.Time{}, suppressed: 0, window: 0}

	return out
}

// Len returns the number of tracked keys. It never exceeds MaxKeys.
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return len(l.keys)
}

// sweep drops the keys whose window ended and returns their counts that no
// entry carries. It runs at most once in a quarter of a window. It returns at
// most maxFlush items; the rest stay and the next sweep returns them. The key
// named current is skipped. The caller holds l.mu.
func (l *Limiter) sweep(now time.Time, current Key) []Pending {
	if now.Sub(l.lastSweep) < l.window/4 {
		return nil
	}

	var out []Pending

	complete := true

	for k, s := range l.keys {
		// The key of this denial is not flushed: Allow carries its count in the
		// entry of this denial.
		if k == current || now.Sub(s.start) < s.span(l.window) {
			continue
		}

		if s.suppressed == 0 {
			l.forget(k)

			continue
		}

		if len(out) >= l.maxFlush {
			complete = false

			continue
		}

		out = append(out, Pending{Key: k, Suppressed: s.suppressed, Overflow: false})

		l.forget(k)
	}

	if !l.overflow.start.IsZero() && now.Sub(l.overflow.start) >= l.window && l.overflow.suppressed > 0 &&
		len(out) < l.maxFlush {
		out = append(out, Pending{
			Key:        Key{Principal: "", Action: "", Kind: "", Scope: ""},
			Suppressed: l.overflow.suppressed, Overflow: true,
		})
		l.overflow = slot{start: time.Time{}, suppressed: 0, window: 0}
	}

	if complete {
		l.lastSweep = now
	}

	return out
}

// touch counts a denial of a key that the table holds, or opens its next window.
func (l *Limiter) touch(d Decision, s *slot, now time.Time) Decision {
	if now.Sub(s.start) < s.span(l.window) {
		s.suppressed++

		return d
	}

	d.Write, d.Suppressed = true, s.suppressed
	s.start, s.suppressed = now, 0

	return d
}

// forget removes key from the table and from the count of its principal. The
// caller holds l.mu.
func (l *Limiter) forget(key Key) {
	delete(l.keys, key)

	if isAggregate(key) {
		return
	}

	if l.perCount[key.Principal] <= 1 {
		delete(l.perCount, key.Principal)

		return
	}

	l.perCount[key.Principal]--
}
