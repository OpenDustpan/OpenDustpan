package oauth

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Lazy is a client that reads the discovery document of the provider at
// its first use, not at its creation, so a server starts while the
// provider is down. A failed read is not kept: the next use reads again.
// One use at a time reads the document, with a context of its own that a
// canceled request does not end and that discoveryLimit bounds. The other
// uses wait for that read, and a use whose context ends while it waits
// returns at once. It is safe for concurrent use.
type Lazy struct {
	cfg Config
	// turn holds a value while one use reads the discovery document.
	turn chan struct{}
	// mu guards client.
	mu     sync.Mutex
	client *Client
}

// discoveryLimit bounds one read of the discovery document and the JWKS.
const discoveryLimit = 2 * defaultTimeout

// NewLazy returns a Lazy client of cfg. It checks cfg at once, and returns
// ErrConfig as New does.
func NewLazy(cfg Config) (*Lazy, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &Lazy{cfg: cfg, turn: make(chan struct{}, 1), mu: sync.Mutex{}, client: nil}, nil
}

// Begin returns the authorization URL and the pending request, as
// Client.Begin does. It returns ErrProvider when the discovery document
// cannot be read.
func (l *Lazy) Begin(ctx context.Context, opts Options) (string, Pending, error) {
	c, err := l.get(ctx)
	if err != nil {
		return "", Pending{State: "", Nonce: "", Verifier: "", MaxAge: 0, HasMaxAge: false}, err
	}

	authURL, p := c.Begin(opts)

	return authURL, p, nil
}

// Finish exchanges the code and checks the ID token, as Client.Finish does.
func (l *Lazy) Finish(ctx context.Context, code string, p Pending) (Identity, error) {
	c, err := l.get(ctx)
	if err != nil {
		return Identity{AuthTime: time.Time{}, Subject: "", Name: "", PreferredUsername: "", Email: ""}, err
	}

	return c.Finish(ctx, code, p)
}

// get returns the client, and reads the discovery document when no read
// succeeded yet. A use that waits for the read of another use returns
// ErrProvider when its context ends.
func (l *Lazy) get(ctx context.Context) (*Client, error) {
	if c := l.loaded(); c != nil {
		return c, nil
	}

	select {
	case l.turn <- struct{}{}:
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: wait for the discovery document: %w", ErrProvider, ctx.Err())
	}

	defer func() { <-l.turn }()

	if c := l.loaded(); c != nil {
		return c, nil
	}

	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), discoveryLimit)
	defer cancel()

	c, err := New(readCtx, l.cfg)
	if err != nil {
		return nil, err
	}

	l.mu.Lock()
	l.client = c
	l.mu.Unlock()

	return c, nil
}

// loaded returns the client of an earlier read, or nil.
func (l *Lazy) loaded() *Client {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.client
}
