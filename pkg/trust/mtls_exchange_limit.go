package trust

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// ErrRateLimited reports too many exchange calls in one window.
var ErrRateLimited = errors.New("too many exchange calls")

// Bounds of the limit layer.
const (
	// MaxBodyBytes caps the body of an mTLS exchange call.
	MaxBodyBytes = MaxChainBytes + 1024
	// DefaultMaxPerWindow caps the calls in one window.
	DefaultMaxPerWindow = 100
	// DefaultWindow is the window of the rate limit.
	DefaultWindow = time.Minute
)

// GuardConfig holds the settings of a Guard. MaxBodyBytes caps the body,
// MaxPerWindow caps the calls in Window. Now is the clock. A nil Now means
// time.Now. Zero values take the defaults.
type GuardConfig struct {
	MaxBodyBytes int
	MaxPerWindow int
	Window       time.Duration
	Now          func() time.Time
}

// Guard caps the body size and the request rate of the mTLS exchange. It is
// safe for concurrent use.
type Guard struct {
	mu           sync.Mutex
	windowStart  time.Time
	count        int
	maxBodyBytes int
	maxPerWindow int
	window       time.Duration
	now          func() time.Time
}

// NewGuard returns a Guard, or ErrInvalid for a non-positive cap.
func NewGuard(cfg GuardConfig) (*Guard, error) {
	maxBody := cfg.MaxBodyBytes
	if maxBody == 0 {
		maxBody = MaxBodyBytes
	}

	maxPer := cfg.MaxPerWindow
	if maxPer == 0 {
		maxPer = DefaultMaxPerWindow
	}

	window := cfg.Window
	if window == 0 {
		window = DefaultWindow
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	if maxBody <= 0 || maxPer <= 0 || window <= 0 {
		return nil, fmt.Errorf("%w: the guard needs a positive body cap, rate and window", ErrInvalid)
	}

	return &Guard{
		mu: sync.Mutex{}, windowStart: time.Time{}, count: 0, maxBodyBytes: maxBody, maxPerWindow: maxPer,
		window: window, now: now,
	}, nil
}

// CheckInput is one check of the limit layer.
type CheckInput struct {
	HasCert bool
	BodyLen int
}

// Check refuses a call with no certificate, an oversize body, or a full
// window. Each refusal gives no token.
func (g *Guard) Check(in CheckInput) error {
	if !in.HasCert {
		return ErrNoCertificate
	}

	if in.BodyLen > g.maxBodyBytes {
		return fmt.Errorf("%w: the body holds more than %d bytes", ErrInvalid, g.maxBodyBytes)
	}

	if !g.allow() {
		return ErrRateLimited
	}

	return nil
}

// ReadBody reads the body r up to the body cap and returns ErrInvalid when r
// holds more bytes. The route reads its body with ReadBody, so that the cap
// holds before any bytes are parsed.
func (g *Guard) ReadBody(r io.Reader) ([]byte, error) {
	body, readErr := io.ReadAll(io.LimitReader(r, int64(g.maxBodyBytes)+1))
	if readErr != nil {
		return nil, fmt.Errorf("%w: the body does not read: %w", ErrInvalid, readErr)
	}

	if len(body) > g.maxBodyBytes {
		return nil, fmt.Errorf("%w: the body holds more than %d bytes", ErrInvalid, g.maxBodyBytes)
	}

	return body, nil
}

// allow reports whether one more call fits in the current window.
func (g *Guard) allow() bool {
	now := g.now()

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.windowStart.IsZero() || !now.Before(g.windowStart.Add(g.window)) {
		g.windowStart = now
		g.count = 0
	}

	if g.count >= g.maxPerWindow {
		return false
	}

	g.count++

	return true
}
