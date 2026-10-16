package server

import (
	"context"

	"opendustpan.org/opendustpan/pkg/identity"
)

// WithPrincipalForTest returns ctx carrying p, as enforce stores the caller
// before it runs a resolver, so a test can call a resolver directly.
func WithPrincipalForTest(ctx context.Context, p identity.Principal) context.Context {
	return withPrincipal(ctx, p)
}
