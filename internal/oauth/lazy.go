package oauth

import (
	"context"
	"sync"
	"time"
)

// Lazy is a client that reads the discovery document of the provider at
// its first use, not at its creation, so a server starts while the
// provider is down. A failed read is not kept: the next use reads again.
// It is safe for concurrent use.
type Lazy struct {
	cfg    Config
	mu     sync.Mutex
	client *Client
}

// NewLazy returns a Lazy client of cfg. It checks cfg at once, and returns
// ErrConfig as New does.
func NewLazy(cfg Config) (*Lazy, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &Lazy{cfg: cfg, mu: sync.Mutex{}, client: nil}, nil
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
// succeeded yet.
func (l *Lazy) get(ctx context.Context) (*Client, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.client != nil {
		return l.client, nil
	}

	c, err := New(ctx, l.cfg)
	if err != nil {
		return nil, err
	}

	l.client = c

	return c, nil
}
