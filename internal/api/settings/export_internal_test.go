package settings

import (
	"context"
	"net/http"

	"opendustpan.org/opendustpan/pkg/identity"
)

// CheckAddress runs the address check of h on r for p.
func CheckAddress(ctx context.Context, h *Handler, r *http.Request, p identity.Principal) error {
	return h.checkAddress(ctx, r, p)
}
