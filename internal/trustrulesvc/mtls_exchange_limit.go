package trustrulesvc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/trustrule"
)

// maxGuardKeyBytes is the longest limiter key that the guard keeps as it is.
// A longer key becomes its digest, so no key costs more than this in memory.
const maxGuardKeyBytes = 64

// MtlsExchanging is the exchange that a guard protects. *MtlsExchanger meets
// it.
type MtlsExchanging interface {
	Exchange(ctx context.Context, in *MtlsExchangeInput) (MtlsExchangeResult, error)
}

// MtlsGuardConfig holds the settings of an MtlsGuard. Each number must be
// positive.
type MtlsGuardConfig struct {
	// Exchanger is the exchange that the guard protects.
	Exchanger MtlsExchanging
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

// MtlsGuard limits the calls of the mTLS exchange before and around the
// exchange: the size of the body, the rate of the calls of each key and of
// all keys, and it fails closed: a call gets a token only when the exchange
// answers with no error. The guard is safe for concurrent use.
type MtlsGuard struct {
	cfg MtlsGuardConfig
	now func() time.Time

	mu          sync.Mutex
	keys        map[string]*guardWindow
	overflow    guardWindow
	global      guardWindow
	lastCleanup time.Time
}

// guardWindow counts the calls since start.
type guardWindow struct {
	start time.Time
	count int
}

// NewMtlsGuard returns an MtlsGuard, or trustrule.ErrMissingDependency for a
// config with no exchanger or a number that is not positive.
func NewMtlsGuard(cfg MtlsGuardConfig) (*MtlsGuard, error) {
	if cfg.Exchanger == nil {
		return nil, fmt.Errorf("%w: the guard needs an exchanger", trustrule.ErrMissingDependency)
	}

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
		overflow:    guardWindow{start: time.Time{}, count: 0},
		global:      guardWindow{start: time.Time{}, count: 0},
		lastCleanup: time.Time{},
	}, nil
}

// Admit counts one call of the key and checks it. It returns ErrBodyTooLarge
// when bodyLen is over the cap or negative, and ErrRateLimited when the key,
// or all keys together, made too many calls in the window. A call with no
// certificate counts too: it is the call that needs no work of the server and
// the one that a flood sends. The key is an address of the caller: it is not
// trusted, so the guard bounds the memory that keys take.
func (g *MtlsGuard) Admit(key string, bodyLen int64) error {
	if bodyLen < 0 || bodyLen > g.cfg.MaxBodyBytes {
		return fmt.Errorf("%w: %d bytes, at most %d", ErrBodyTooLarge, bodyLen, g.cfg.MaxBodyBytes)
	}

	if len(key) > maxGuardKeyBytes {
		sum := sha256.Sum256([]byte(key))
		key = hex.EncodeToString(sum[:])
	}

	now := g.now()

	g.mu.Lock()
	defer g.mu.Unlock()

	globalOK := g.count(&g.global, now) <= g.cfg.GlobalLimit
	keyOK := g.count(g.windowOf(key, now), now) <= g.cfg.Limit

	if !globalOK || !keyOK {
		return ErrRateLimited
	}

	return nil
}

// Tracked returns the number of keys that the guard holds, for metrics and
// tests. It never passes MaxKeys.
func (g *MtlsGuard) Tracked() int {
	g.mu.Lock()
	defer g.mu.Unlock()

	return len(g.keys)
}

// Exchange admits the call, then runs the exchange of in. It fails closed:
// the result holds a token only when the answer has no error, and an error
// that is not a refusal of the exchange, for example the failure of a store,
// becomes ErrIssueFailed.
func (g *MtlsGuard) Exchange(
	ctx context.Context, key string, bodyLen int64, in *MtlsExchangeInput,
) (MtlsExchangeResult, error) {
	if err := g.Admit(key, bodyLen); err != nil {
		return MtlsExchangeResult{}, err
	}

	res, err := g.cfg.Exchanger.Exchange(ctx, in)
	if err != nil {
		return MtlsExchangeResult{}, failClosed(err)
	}

	return res, nil
}

// count adds one call to w, after it starts a new window when the window of w
// is over, and returns the count of the window.
func (g *MtlsGuard) count(w *guardWindow, now time.Time) int {
	if w.start.IsZero() || !now.Before(w.start.Add(g.cfg.Window)) {
		w.start, w.count = now, 0
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

	w := &guardWindow{start: time.Time{}, count: 0}
	g.keys[key] = w

	return w
}

// failClosed returns err when it is a refusal of the mTLS exchange, and
// ErrIssueFailed with err for every other error.
func failClosed(err error) error {
	for _, refusal := range []error{
		ErrNoCertificate, ErrUnknownIssuer, ErrExpired, ErrNotYetValid, ErrRevoked, ErrRevocationUnknown,
		ErrNoMatchingRule, ErrAmbiguousRule, ErrBundleUnknown, ErrInvalid, ErrIssueFailed, ErrRateLimited,
		ErrBodyTooLarge, trustrule.ErrPrincipal,
	} {
		if errors.Is(err, refusal) {
			return err
		}
	}

	return fmt.Errorf("%w: %w", ErrIssueFailed, err)
}
