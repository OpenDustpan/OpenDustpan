// Package requestid mints and carries the request ID of ADR 32. The root
// middleware in pkg/dustpan mints one ID for each request, returns it in
// the X-Request-Id response header and stores it in the request context.
// Handlers read it back with FromContext, for example for an audit entry.
// The key lives here, not in pkg/dustpan, so HTTP adapters can read the ID
// without importing the server: pkg/dustpan imports the adapters, so the
// reverse import would be a cycle.
package requestid

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"
)

// Header carries the request ID in each response.
const Header = "X-Request-Id"

// idBytes is the entropy of one request ID in bytes, as hex.
const idBytes = 16

// key holds the request ID in the request context.
type key struct{}

// New mints one request ID: 128 bits from crypto/rand as hex. It falls back
// to the time when the reader fails, so it never returns "".
func New() string {
	buf := make([]byte, idBytes)
	if _, err := rand.Read(buf); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}

	return hex.EncodeToString(buf)
}

// WithContext stores id in ctx.
func WithContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, key{}, id)
}

// FromContext returns the request ID in ctx, or "" when ctx holds none.
func FromContext(ctx context.Context) string {
	id, ok := ctx.Value(key{}).(string)
	if !ok {
		return ""
	}

	return id
}
