package trustrulesvc

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/trustrule"
)

// maxGuardKeyBytes is the longest limiter key that the guard keeps as it is.
// A longer key becomes its digest, so no key costs more than this in memory.
const maxGuardKeyBytes = 64

// MtlsGuardConfig holds the settings of an MtlsGuard. Each number must be
// positive.
type MtlsGuardConfig struct {
	// MaxBodyBytes is the largest request body that the guard admits.
	MaxBodyBytes int64
	// Limit is the number of calls that one key makes in one window.
	Limit int
	// GlobalLimit is the number of calls of all keys in one window.
	GlobalLimit int
	// Window is the length of a rate window.
	Window time.Duration
	// MaxKeys bounds the number of keys that the guard holds. A key beyond
	// the bound shares the one overflow key, so the memory of the guard has
	// a fixed upper bound, also for calls that no sign-in came before.
	MaxKeys int
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
}

// MtlsGuard limits the calls of the mTLS exchange before any other work: the
// size of the body, and the rate of the calls of each key and of all keys.
// The guard is safe for concurrent use.
type MtlsGuard struct {
	cfg MtlsGuardConfig
	now func() time.Time

	mu          sync.Mutex
	keys        map[string]*guardWindow
	overflow    guardWindow
	global      guardWindow
	lastCleanup time.Time
}

// guardWindow counts the calls since start. refused is true after the guard
// reported the first refusal of the window.
type guardWindow struct {
	start   time.Time
	count   int
	refused bool
}

// NewMtlsGuard returns an MtlsGuard, or trustrule.ErrMissingDependency for a
// config with a number that is not positive.
func NewMtlsGuard(cfg MtlsGuardConfig) (*MtlsGuard, error) {
	if cfg.MaxBodyBytes < 0 || cfg.Limit < 1 || cfg.GlobalLimit < 1 || cfg.Window <= 0 || cfg.MaxKeys < 1 {
		return nil, fmt.Errorf("%w: the guard needs a body cap that is not negative, and a rate limit, a global "+
			"limit, a window and a key bound that are positive", trustrule.ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	cfg.Now = now

	return &MtlsGuard{
		cfg:         cfg,
		now:         now,
		mu:          sync.Mutex{},
		keys:        make(map[string]*guardWindow),
		overflow:    guardWindow{start: time.Time{}, count: 0, refused: false},
		global:      guardWindow{start: time.Time{}, count: 0, refused: false},
		lastCleanup: time.Time{},
	}, nil
}

// Admit counts one call of the key and checks it. It returns ErrBodyTooLarge
// when bodyLen is over the cap or negative, and ErrRateLimited when the key,
// or all keys together, made too many calls in the window. A call with no
// certificate counts too: it is the call that needs no work of the server and
// the one that a flood sends. The key is an address of the caller: it is not
// trusted, so the guard bounds the memory that keys take.
//
// first is true for the first refusal of a window and false for each later
// one, so the caller does work for a refusal at most once for each window:
// the guard counts the others in memory. While all keys together are over
// the limit, the window of all keys is the one that counts, so a flood from
// many keys also gives one first refusal for each window. first is false when
// err is nil.
func (g *MtlsGuard) Admit(key string, bodyLen int64) (bool, error) {
	if len(key) > maxGuardKeyBytes {
		sum := sha256.Sum256([]byte(key))
		key = hex.EncodeToString(sum[:])
	}

	now := g.now()

	g.mu.Lock()
	defer g.mu.Unlock()

	keyWindow := g.windowOf(key, now)
	globalOver := g.count(&g.global, now) > g.cfg.GlobalLimit
	keyOver := g.count(keyWindow, now) > g.cfg.Limit

	var err error

	switch {
	case bodyLen < 0 || bodyLen > g.cfg.MaxBodyBytes:
		err = fmt.Errorf("%w: %d bytes, at most %d", ErrBodyTooLarge, bodyLen, g.cfg.MaxBodyBytes)
	case globalOver || keyOver:
		err = ErrRateLimited
	default:
		return false, nil
	}

	w := keyWindow
	if globalOver {
		w = &g.global
	}

	first := !w.refused
	w.refused = true

	return first, err
}

// Tracked returns the number of keys that the guard holds, for metrics and
// tests. It never passes MaxKeys.
func (g *MtlsGuard) Tracked() int {
	g.mu.Lock()
	defer g.mu.Unlock()

	return len(g.keys)
}

// count adds one call to w, after it starts a new window when the window of w
// is over, and returns the count of the window.
func (g *MtlsGuard) count(w *guardWindow, now time.Time) int {
	if w.start.IsZero() || !now.Before(w.start.Add(g.cfg.Window)) {
		w.start, w.count, w.refused = now, 0, false
	}

	w.count++

	return w.count
}

// windowOf returns the window of key. It holds at most MaxKeys windows. When
// the map is full, it first drops the windows that are over, at most once for
// each window of time, and a key that still finds no place shares the
// overflow window. The caller holds g.mu.
func (g *MtlsGuard) windowOf(key string, now time.Time) *guardWindow {
	if w, ok := g.keys[key]; ok {
		return w
	}

	if len(g.keys) >= g.cfg.MaxKeys && now.Sub(g.lastCleanup) >= g.cfg.Window {
		g.lastCleanup = now

		for k, w := range g.keys {
			if !now.Before(w.start.Add(g.cfg.Window)) {
				delete(g.keys, k)
			}
		}
	}

	if len(g.keys) >= g.cfg.MaxKeys {
		return &g.overflow
	}

	w := &guardWindow{start: time.Time{}, count: 0, refused: false}
	g.keys[key] = w

	return w
}
