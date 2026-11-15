package forgelink

import (
	"context"

	"opendustpan.org/opendustpan/pkg/principal"
)

// People finds the principal records that the service needs.
type People interface {
	// GetPrincipalBySubject returns the principal that exists with the subject,
	// or principal.ErrNotFound.
	GetPrincipalBySubject(ctx context.Context, subject string) (principal.Principal, error)
	// GetPrincipal returns the principal with the ID id, or principal.ErrNotFound.
	GetPrincipal(ctx context.Context, id string) (principal.Principal, error)
}
